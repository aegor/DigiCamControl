package main

/*
#cgo CFLAGS: -std=c11
#cgo pkg-config: libavdevice libavformat libavcodec libswscale libavutil
#include <stdlib.h>
#include "ffmpeg_bridge.h"
*/
import "C"

// Build through scripts/go.sh: Windows needs the static pkg-config wrapper's
// linker group to resolve references between FFmpeg archives.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
	"unsafe"
)

var initFFmpeg sync.Once
var errVideoAgain = errors.New("no video packet ready")

func ffmpegVersion() string { initFFmpeg.Do(func() { C.dc_init() }); return C.GoString(C.dc_version()) }
func ffmpegError(operation string, code C.int) error {
	if code >= 0 {
		return nil
	}
	if C.dc_is_eof(code) != 0 {
		return io.EOF
	}
	if C.dc_is_again(code) != 0 {
		return errVideoAgain
	}
	var message [256]C.char
	C.dc_error(code, &message[0], C.size_t(len(message)))
	return fmt.Errorf("%s: %s", operation, C.GoString(&message[0]))
}

// All retained native objects live in C-owned memory. The cancellation callback
// touches only an atomic flag; it never frees or closes an AVFormatContext
// concurrently with FFmpeg. stopWatching waits for the callback before freeing.
type nativeInterrupt struct {
	ptr  *C.dc_interrupt
	stop func() bool
	done chan struct{}
}

func newNativeInterrupt(ctx context.Context) (*nativeInterrupt, error) {
	initFFmpeg.Do(func() { C.dc_init() })
	ptr := C.dc_interrupt_new()
	if ptr == nil {
		return nil, errors.New("allocate FFmpeg interrupt flag")
	}
	c := &nativeInterrupt{ptr: ptr, done: make(chan struct{})}
	c.stop = context.AfterFunc(ctx, func() { C.dc_interrupt_cancel(ptr); close(c.done) })
	return c, nil
}
func (c *nativeInterrupt) arm(timeout time.Duration) {
	C.dc_interrupt_arm(c.ptr, C.int64_t(timeout.Microseconds()))
}
func (c *nativeInterrupt) stopWatching() {
	if !c.stop() {
		<-c.done
	}
}
func (c *nativeInterrupt) free() { C.dc_interrupt_free(c.ptr) }

type videoSource struct {
	URL, Format string
	USB         bool
}
type nativeInput struct {
	ptr       *C.dc_input
	interrupt *nativeInterrupt
}

func openVideoInput(ctx context.Context, source videoSource) (*nativeInput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	control, err := newNativeInterrupt(ctx)
	if err != nil {
		return nil, err
	}
	input := &nativeInput{ptr: C.dc_input_new(control.ptr), interrupt: control}
	if input.ptr == nil {
		input.close()
		return nil, errors.New("allocate FFmpeg input")
	}
	url := C.CString(source.URL)
	defer C.free(unsafe.Pointer(url))
	format := C.CString(source.Format)
	defer C.free(unsafe.Pointer(format))
	usb := C.int(0)
	if source.USB {
		usb = 1
	}
	control.arm(8 * time.Second)
	ret := C.dc_input_open(input.ptr, url, format, usb)
	if ret < 0 {
		input.close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ffmpegError("open video", ret)
	}
	return input, nil
}
func (in *nativeInput) close() {
	in.interrupt.stopWatching()
	C.dc_input_free(in.ptr)
	in.interrupt.free()
}
func (in *nativeInput) codec() string { return C.GoString(C.dc_input_codec(in.ptr)) }
func (in *nativeInput) dimensions() (int, int) {
	return int(C.dc_input_width(in.ptr)), int(C.dc_input_height(in.ptr))
}
func (in *nativeInput) read() ([]byte, error) {
	var data *C.uint8_t
	var size C.int
	code := C.dc_input_read(in.ptr, &data, &size)
	if code < 0 {
		return nil, ffmpegError("read video", code)
	}
	return C.GoBytes(unsafe.Pointer(data), size), nil
}

func (in *nativeInput) readJPEG() ([]byte, error) {
	var data *C.uint8_t
	var size C.int
	code := C.dc_input_read_jpeg(in.ptr, &data, &size)
	if code < 0 {
		return nil, ffmpegError("read preview", code)
	}
	return C.GoBytes(unsafe.Pointer(data), size), nil
}

type nativeRecorder struct {
	ptr       *C.dc_recorder
	interrupt *nativeInterrupt
}

func openNativeRecorder(ctx context.Context, path string) (*nativeRecorder, error) {
	control, err := newNativeInterrupt(ctx)
	if err != nil {
		return nil, err
	}
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	var code C.int
	ptr := C.dc_recorder_new(control.ptr, name, &code)
	if ptr == nil {
		control.stopWatching()
		control.free()
		return nil, ffmpegError("create MP4 recorder", code)
	}
	return &nativeRecorder{ptr: ptr, interrupt: control}, nil
}
func (r *nativeRecorder) write(jpeg []byte) error {
	if len(jpeg) == 0 || len(jpeg) > maxPacket {
		return errors.New("invalid JPEG packet size")
	}
	r.interrupt.arm(5 * time.Second)
	// C copies this borrowed slice; no Go pointer is retained by the decoder.
	return ffmpegError("encode MP4 frame", C.dc_recorder_write(r.ptr, (*C.uint8_t)(unsafe.Pointer(&jpeg[0])), C.int(len(jpeg))))
}
func (r *nativeRecorder) finish() error {
	r.interrupt.arm(5 * time.Second)
	return ffmpegError("finalize MP4", C.dc_recorder_finish(r.ptr))
}
func (r *nativeRecorder) close() {
	r.interrupt.stopWatching()
	C.dc_recorder_free(r.ptr)
	r.interrupt.free()
}
