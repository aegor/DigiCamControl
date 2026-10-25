package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MJPEG packets pass through unchanged. H.264 is decoded and encoded to JPEG
// inside the linked libraries; no subprocess is used.
func streamVideo(ctx context.Context, source videoSource, frame func([]byte)) error {
	input, err := openVideoInput(ctx, source)
	if err != nil {
		return err
	}
	defer input.close()
	input.interrupt.arm(8 * time.Second)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		jpeg, err := input.readJPEG()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errVideoAgain) {
			if !pause(ctx, 10*time.Millisecond) {
				return ctx.Err()
			}
			continue
		}
		if err != nil {
			return err
		}
		input.interrupt.arm(8 * time.Second)
		frame(jpeg)
	}
}

// A bounded queue separates disk I/O and encoding from the preview. Shutdown
// drains queued frames and writes the MP4 trailer before releasing native state.
type recorder struct {
	frames chan []byte
	done   chan struct{}
	cancel context.CancelFunc
	err    error // read only after done is closed
	path   string
}

func newRecorder(parent context.Context, directory string, slot int) (*recorder, error) {
	if err := os.MkdirAll(directory, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, fmt.Sprintf("usb%d_%s.mp4", slot, time.Now().Format("20060102_150405.000000000")))
	reserved, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	if err = reserved.Close(); err != nil {
		os.Remove(path)
		return nil, err
	}
	// Worker cleanup calls stop() after its context is cancelled; keep the
	// encoder alive long enough to finalize the file, with its own cancellation.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	encoder, err := openNativeRecorder(ctx, path)
	if err != nil {
		cancel()
		os.Remove(path)
		return nil, err
	}
	r := &recorder{frames: make(chan []byte, 60), done: make(chan struct{}), cancel: cancel, path: path}
	go func() {
		defer close(r.done)
		defer cancel()
		defer func() {
			encoder.close()
			if r.err != nil {
				_ = os.Remove(path)
			}
		}()
		for {
			select {
			case <-ctx.Done():
				r.err = ctx.Err()
				return
			case jpeg, ok := <-r.frames:
				if !ok {
					r.err = encoder.finish()
					return
				}
				if err := encoder.write(jpeg); err != nil {
					r.err = err
					return
				}
			}
		}
	}()
	return r, nil
}
func (r *recorder) stop() error {
	close(r.frames)
	timer := time.AfterFunc(5*time.Second, r.cancel)
	defer timer.Stop()
	<-r.done
	return r.err
}

var errEncoderSlow = errors.New("recording encoder cannot keep up")
