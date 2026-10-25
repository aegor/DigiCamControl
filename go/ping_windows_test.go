package main

import (
	"context"
	"testing"
	"time"
)

func TestWindowsPingLoopback(t *testing.T) {
	for _, bind := range []string{"", "127.0.0.1"} {
		ok, err := pingCamera(context.Background(), "127.0.0.1", bind)
		if err != nil || !ok {
			t.Fatalf("bind %q: reachable=%v err=%v", bind, ok, err)
		}
	}
}

func TestWindowsPingInvalidAddresses(t *testing.T) {
	for _, pair := range [][2]string{{"::1", ""}, {"bad", ""}, {"127.0.0.1", "::1"}, {"127.0.0.1", "bad"}} {
		if ok, err := pingCamera(context.Background(), pair[0], pair[1]); ok || err == nil {
			t.Fatalf("%v: %v %v", pair, ok, err)
		}
	}
}

func TestWindowsPingCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if ok, err := pingCamera(ctx, "127.0.0.1", ""); ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("cancelled request did not return immediately")
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if ok, err := pingCamera(ctx, "127.0.0.1", ""); ok || err != nil {
		t.Fatalf("expired: %v %v", ok, err)
	}
}
