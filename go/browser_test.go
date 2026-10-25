package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrowserURL(t *testing.T) {
	for _, test := range []struct{ host, zone, want string }{
		{"0.0.0.0", "", "http://127.0.0.1:5001/"},
		{"::", "", "http://[::1]:5001/"},
		{"127.0.0.1", "", "http://127.0.0.1:5001/"},
		{"192.168.1.10", "", "http://192.168.1.10:5001/"},
		{"::1", "", "http://[::1]:5001/"},
		{"fe80::1", "eth0", "http://[fe80::1%25eth0]:5001/"},
	} {
		got := browserURL(&net.TCPAddr{IP: net.ParseIP(test.host), Zone: test.zone, Port: 5001})
		if got != test.want {
			t.Errorf("%s: got %s, want %s", test.host, got, test.want)
		}
	}
}

func TestBrowserWaitsForUIAndOpensOnce(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	opened := 0
	err := openBrowserWhenReady(context.Background(), server.URL, func(address string) error {
		opened++
		if address != server.URL || requests.Load() < 2 {
			t.Error("browser opened before UI was ready")
		}
		return nil
	})
	if err != nil || opened != 1 {
		t.Fatalf("opens=%d error=%v", opened, err)
	}
}

func TestBrowserDoesNotOpenAfterCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := openBrowserWhenReady(ctx, server.URL, func(string) error { t.Error("browser opened for unavailable UI"); return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestBrowserLaunchFailureIsReturnedWithoutRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	want := errors.New("no browser association")
	opened := 0
	err := openBrowserWhenReady(context.Background(), server.URL, func(string) error { opened++; return want })
	if !errors.Is(err, want) || opened != 1 {
		t.Fatalf("opens=%d error=%v", opened, err)
	}
}

func TestBrowserConfig(t *testing.T) {
	for _, test := range []struct {
		args     []string
		disabled bool
	}{
		{nil, false}, {[]string{"--no-browser"}, true}, {[]string{"--no-browser=false"}, false},
	} {
		c, err := parseConfig(test.args, io.Discard)
		if err != nil || c.noBrowser != test.disabled {
			t.Fatalf("%v: %+v %v", test.args, c, err)
		}
	}
}
