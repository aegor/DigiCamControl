package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func jpegFixture(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 3), uint8(y * 4), shade, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestNativePreviewWithoutFFmpegExecutable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var raw []byte
	for i := 0; i < 4; i++ {
		raw = append(raw, jpegFixture(t, uint8(i*40))...)
	}
	path := filepath.Join(t.TempDir(), "preview.mjpeg")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	count := 0
	err := streamVideo(context.Background(), videoSource{URL: path, Format: "mjpeg"}, func(b []byte) {
		img, err := jpeg.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 64 || img.Bounds().Dy() != 48 {
			t.Fatal(img.Bounds())
		}
		count++
	})
	if !errors.Is(err, io.EOF) || count != 4 {
		t.Fatal(count, err)
	}
}
func TestNativeMP4RecordingFinishesAfterParentShutdown(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := newRecorder(ctx, t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		r.frames <- jpegFixture(t, uint8(i*15))
	}
	cancel()
	if err = r.stop(); err != nil {
		t.Fatal(err)
	}
	// Reopen the actual encoded file with libavformat. This verifies its MP4
	// header/trailer, codec parameters and all video packets without ffprobe.
	input, err := openVideoInput(context.Background(), videoSource{URL: r.path, Format: "mov"})
	if err != nil {
		t.Fatal(err)
	}
	defer input.close()
	width, height := input.dimensions()
	if input.codec() != "mpeg4" || width != 64 || height != 48 {
		t.Fatal(input.codec(), width, height)
	}
	count := 0
	for {
		packet, err := input.read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(packet) == 0 {
			t.Fatal("empty MPEG-4 packet")
		}
		count++
	}
	if count != 12 {
		t.Fatalf("got %d packets, want 12", count)
	}
}
func TestNativeRecorderRejectsBadJPEGAndRemovesIncompleteFile(t *testing.T) {
	r, err := newRecorder(context.Background(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	r.frames <- []byte("invalid JPEG")
	if err = r.stop(); err == nil {
		t.Fatal("invalid JPEG was accepted")
	}
	if _, err = os.Stat(r.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("incomplete MP4 left behind", err)
	}
}
func TestNativeRecorderWithoutFramesReportsFailure(t *testing.T) {
	r, err := newRecorder(context.Background(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.stop(); err == nil {
		t.Fatal("empty recording reported as successful")
	}
}
func TestNativeOpenCancelledBeforeStarting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := openVideoInput(ctx, videoSource{URL: "rtsp://127.0.0.1:1/MJPG"})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestNativeCancellationInterruptsStalledRTSP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	release := make(chan struct{})
	defer close(release)
	requested := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		b := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err = conn.Read(b); err != nil {
			return
		}
		close(requested)
		// Deliberately keep the socket open without sending a reply. Only the
		// native interrupt callback can unblock the client before this is released.
		<-release
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		in, err := openVideoInput(ctx, videoSource{URL: "rtsp://" + listener.Addr().String() + "/MJPG"})
		if in != nil {
			in.close()
		}
		result <- err
	}()
	select {
	case <-requested:
	case <-time.After(3 * time.Second):
		t.Fatal("no RTSP request received")
	}
	started := time.Now()
	cancel()
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native RTSP read did not cancel")
	}
	t.Logf("stalled RTSP interrupted in %s", time.Since(started))
}

func TestNativeCancellationInterruptsStalledVideoRead(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	jpeg := jpegFixture(t, 100)
	release := make(chan struct{})
	defer close(release)
	sent := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		for i := 0; i < 100; i++ {
			if _, err = conn.Write(jpeg); err != nil {
				return
			}
		}
		close(sent)
		<-release
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 1)
	result := make(chan error, 1)
	go func() {
		result <- streamVideo(ctx, videoSource{URL: "tcp://" + listener.Addr().String(), Format: "mjpeg"}, func([]byte) {
			select {
			case ready <- struct{}{}:
			default:
			}
		})
	}()
	select {
	case <-ready:
	case <-time.After(4 * time.Second):
		t.Fatal("no video frames received")
	}
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("fixture was not sent")
	}
	// Let libavformat consume the buffered frames and block on the open socket.
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled video read did not cancel")
	}
}
