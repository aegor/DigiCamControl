package main

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestMonitorClearsFramesAndCancelsSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWorker(0, "192.168.1.1", "", "", false)
	w.s.State = "running"
	w.s.Recording = true
	w.jpeg = []byte{1}
	w.last = time.Now()
	session, stop := context.WithCancel(ctx)
	defer stop()
	w.cancelSession = stop
	var count atomic.Int32
	w.ping = func(context.Context) (bool, error) { return count.Add(1) > 1, nil }
	done := make(chan struct{})
	go func() { w.monitor(ctx); close(done) }()
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("loss did not cancel session")
	}
	s := w.snapshot()
	if s.Reachable == nil || *s.Reachable || s.Recording || s.HasPreview || s.State != "reconnecting" {
		t.Fatal(s)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s = w.snapshot()
		if s.Reachable != nil && *s.Reachable {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.Reachable == nil || !*s.Reachable {
		t.Fatal("ping recovery not detected")
	}
	cancel()
	<-done
}
func TestWorkerRetriesPastFiveFailuresAndResumesFrames(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	w := newWorker(0, "192.168.1.1", "", "", false)
	w.ctx = ctx
	w.ping = func(context.Context) (bool, error) { return true, nil }
	attempts := 0
	w.connect = func(ctx context.Context) (*camera, error) {
		attempts++
		if attempts <= 6 {
			return nil, errors.New("camera offline")
		}
		client, server := net.Pipe()
		go serveCameraOps(server)
		c := &camera{cmd: client}
		c.stopCancel = context.AfterFunc(ctx, func() { client.Close() })
		return c, nil
	}
	var received status
	w.stream = func(ctx context.Context, frame func([]byte)) error {
		frame([]byte{0xff, 0xd8, 0xff, 0xd9})
		received = w.snapshot()
		cancel()
		return nil
	}
	w.run(ctx)
	if attempts != 7 || received.State != "running" || !received.HasPreview || received.Generation == "" {
		t.Fatal(attempts, received)
	}
}

func TestVideoRestartPreservesPTPAndRecording(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	w := newWorker(0, "192.168.1.1", "", "", false)
	connects := 0
	w.connect = func(ctx context.Context) (*camera, error) {
		connects++
		client, server := net.Pipe()
		go serveCameraOps(server)
		c := &camera{cmd: client}
		c.stopCancel = context.AfterFunc(ctx, func() { client.Close() })
		return c, nil
	}
	streams := 0
	w.stream = func(ctx context.Context, frame func([]byte)) error {
		streams++
		if streams == 1 {
			frame([]byte{0xff, 0xd8, 0xff, 0xd9})
			if err := w.record(true); err != nil {
				t.Fatal(err)
			}
			return errors.New("RTSP restarted after mode change")
		}
		if !w.snapshot().Recording {
			t.Fatal("recording status lost during video restart")
		}
		if err := w.record(false); err != nil {
			t.Fatal("Stop unavailable while preview reconnects:", err)
		}
		if w.snapshot().Recording {
			t.Fatal("Stop failed")
		}
		cancel()
		return ctx.Err()
	}
	_ = w.session(ctx)
	w.camera.close()
	if connects != 1 || streams != 2 {
		t.Fatal(connects, streams)
	}
}
