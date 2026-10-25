package main

import (
	"context"
	"fmt"
	"math"
	"net"
	"sync"
	"time"
)

type status struct {
	Slot       int      `json:"slot"`
	Host       string   `json:"host"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	State      string   `json:"state"`
	Error      string   `json:"error"`
	Model      string   `json:"model"`
	Firmware   string   `json:"firmware"`
	Reachable  *bool    `json:"reachable"`
	Generation string   `json:"stream_generation"`
	Mode       uint16   `json:"mode"`
	ModeName   string   `json:"mode_name"`
	Recording  bool     `json:"recording"`
	FPS        float64  `json:"fps_actual"`
	HasPreview bool     `json:"has_preview"`
	Age        *float64 `json:"last_frame_age_s"`
	RecordPath string   `json:"record_path,omitempty"`
}
type worker struct {
	mu              sync.RWMutex
	s               status
	jpeg            []byte
	seq             uint64
	last            time.Time
	cancelSession   context.CancelFunc
	commandMu       sync.Mutex
	camera          *camera
	recordMu        sync.Mutex
	recorder        *recorder
	bind, recordDir string
	usb             bool
	ctx             context.Context
	// Injectable transport boundaries allow recovery tests without hardware.
	ping    func(context.Context) (bool, error)
	connect func(context.Context) (*camera, error)
	stream  func(context.Context, func([]byte)) error
}

func newWorker(slot int, host, bind, recordDir string, usb bool) *worker {
	kind := "wifi"
	if usb {
		kind = "usb-uvc"
	}
	w := &worker{s: status{Slot: slot, Host: host, Name: fmt.Sprintf("%s%d", kind, slot), Kind: kind, State: "starting", ModeName: "?"}, bind: bind, recordDir: recordDir, usb: usb}
	w.ping = func(ctx context.Context) (bool, error) { return pingCamera(ctx, host, bind) }
	w.connect = func(ctx context.Context) (*camera, error) {
		return connectCamera(ctx, net.JoinHostPort(host, "15740"), bind)
	}
	w.stream = func(ctx context.Context, frame func([]byte)) error {
		source := videoSource{URL: host, Format: "v4l2", USB: true}
		if !usb {
			source = videoSource{URL: "rtsp://" + net.JoinHostPort(host, "554") + "/MJPG?W=720&H=400&Q=50&BR=5000000"}
		}
		return streamVideo(ctx, source, frame)
	}
	return w
}
func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func (w *worker) disconnectedLocked() {
	w.s.State = "reconnecting"
	w.s.Mode = 0
	w.s.ModeName = "?"
	w.s.Recording = false
	w.s.FPS = 0
	w.jpeg = nil
	w.last = time.Time{}
}
func (w *worker) monitor(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		reachable, err := w.ping(ctx)
		if ctx.Err() != nil {
			return
		}
		w.mu.Lock()
		w.s.Reachable = &reachable
		if !reachable {
			w.disconnectedLocked()
			if err != nil {
				w.s.Error = err.Error()
			}
			if w.cancelSession != nil {
				w.cancelSession()
			}
		}
		w.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (w *worker) run(ctx context.Context) {
	monitorDone := make(chan struct{})
	if !w.usb {
		go func() { defer close(monitorDone); w.monitor(ctx) }()
	} else {
		close(monitorDone)
	}
	defer func() { <-monitorDone }()
	for ctx.Err() == nil {
		w.mu.Lock()
		if !w.usb && (w.s.Reachable == nil || !*w.s.Reachable) {
			w.mu.Unlock()
			if !pause(ctx, 100*time.Millisecond) {
				return
			}
			continue
		}
		session, cancel := context.WithCancel(ctx)
		w.cancelSession = cancel
		w.s.State = "starting"
		w.mu.Unlock()
		err := w.session(session)
		cancel()
		w.mu.Lock()
		w.cancelSession = nil
		w.disconnectedLocked()
		if err != nil && ctx.Err() == nil {
			w.s.Error = err.Error()
		}
		w.mu.Unlock()
		w.commandMu.Lock()
		if w.camera != nil {
			w.camera.close()
			w.camera = nil
		}
		w.stopUSBRecording()
		w.commandMu.Unlock()
		if !pause(ctx, time.Second) {
			return
		}
	}
}
func (w *worker) session(ctx context.Context) error {
	if !w.usb {
		w.commandMu.Lock()
		c, err := w.connect(ctx)
		if err != nil {
			w.commandMu.Unlock()
			return err
		}
		w.camera = c
		model := c.model()
		firmware := c.firmware()
		mode, err := c.mode()
		w.mu.Lock()
		w.s.Model = model
		w.s.Firmware = firmware
		if err == nil {
			w.setModeLocked(mode)
		}
		w.mu.Unlock()
		w.commandMu.Unlock()
		if err != nil {
			return err
		}
	} else {
		w.mu.Lock()
		w.s.Model = "UVC camera"
		w.s.ModeName = "UVC"
		w.mu.Unlock()
	}
	for ctx.Err() == nil {
		err := w.preview(ctx)
		if w.usb {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Recording mode changes can restart RTSP while PTP remains healthy.
		// Keep the command connection and recording state across that restart.
		w.mu.Lock()
		w.s.State = "reconnecting"
		w.jpeg = nil
		w.last = time.Time{}
		w.s.FPS = 0
		if err != nil {
			w.s.Error = err.Error()
		}
		w.mu.Unlock()
		if !pause(ctx, time.Second) {
			return ctx.Err()
		}
	}
	return ctx.Err()
}

func (w *worker) preview(ctx context.Context) error {
	first := true
	window := time.Now()
	count := 0
	return w.stream(ctx, func(jpeg []byte) {
		if ctx.Err() != nil {
			return
		}
		now := time.Now()
		w.mu.Lock()
		if ctx.Err() != nil {
			w.mu.Unlock()
			return
		}
		if first {
			w.s.Generation = fmt.Sprint(now.UnixNano())
			w.s.Error = ""
			first = false
		}
		w.s.State = "running"
		w.jpeg = jpeg
		w.last = now
		w.seq++
		count++
		if elapsed := now.Sub(window).Seconds(); elapsed >= 1 {
			w.s.FPS = float64(count) / elapsed
			count = 0
			window = now
		}
		w.mu.Unlock()
		w.feedRecorder(jpeg)
	})
}
func (w *worker) setModeLocked(mode uint16) {
	w.s.Mode = mode
	w.s.Recording = mode == 17
	w.s.ModeName = fmt.Sprintf("MODE_%d", mode)
	if mode == 1 {
		w.s.ModeName = "VIDEO_OFF"
	}
	if mode == 17 {
		w.s.ModeName = "VIDEO_ON"
	}
}
func (w *worker) snapshot() status {
	w.mu.RLock()
	defer w.mu.RUnlock()
	s := w.s
	s.FPS = math.Round(s.FPS*10) / 10
	s.HasPreview = w.jpeg != nil
	if !w.last.IsZero() {
		age := math.Round(time.Since(w.last).Seconds()*100) / 100
		s.Age = &age
	}
	return s
}
func (w *worker) frame() ([]byte, uint64) { w.mu.RLock(); defer w.mu.RUnlock(); return w.jpeg, w.seq }
func (w *worker) record(on bool) error {
	w.commandMu.Lock()
	defer w.commandMu.Unlock()
	s := w.snapshot()
	if (w.usb && s.State != "running") || (!w.usb && (w.camera == nil || (s.Reachable != nil && !*s.Reachable))) {
		return fmt.Errorf("camera is %s: %s", s.State, s.Error)
	}
	if w.usb {
		if !on {
			return w.stopUSBRecording()
		}
		w.recordMu.Lock()
		defer w.recordMu.Unlock()
		if w.recorder != nil {
			return nil
		}
		r, err := newRecorder(w.ctx, w.recordDir, w.s.Slot)
		if err != nil {
			return err
		}
		w.recorder = r
		w.mu.Lock()
		w.s.Recording = true
		w.s.RecordPath = r.path
		w.mu.Unlock()
		return nil
	}
	if w.camera == nil {
		return fmt.Errorf("camera is not connected")
	}
	if err := w.camera.setRecording(on); err != nil {
		w.mu.Lock()
		w.s.Error = err.Error()
		if w.cancelSession != nil {
			w.cancelSession()
		}
		w.mu.Unlock()
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	mode := uint16(1)
	if on {
		mode = 17
	}
	w.setModeLocked(mode)
	return nil
}
func (w *worker) stopUSBRecording() error {
	w.recordMu.Lock()
	defer w.recordMu.Unlock()
	if w.recorder == nil {
		return nil
	}
	r := w.recorder
	w.recorder = nil
	err := r.stop()
	w.mu.Lock()
	w.s.Recording = false
	if err != nil {
		w.s.Error = err.Error()
	}
	w.mu.Unlock()
	return err
}
func (w *worker) feedRecorder(frame []byte) {
	w.recordMu.Lock()
	defer w.recordMu.Unlock()
	r := w.recorder
	if r == nil {
		return
	}
	var err error
	select {
	case <-r.done:
		err = r.err
		if err == nil {
			err = fmt.Errorf("recording encoder stopped")
		}
	default:
	}
	if err == nil {
		select {
		case r.frames <- frame:
			return
		default:
			err = errEncoderSlow
		}
	}
	r.cancel()
	close(r.frames)
	w.recorder = nil
	w.mu.Lock()
	w.s.Recording = false
	w.s.Error = err.Error()
	w.mu.Unlock()
}
