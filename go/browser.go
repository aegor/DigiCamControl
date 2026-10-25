package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func browserURL(address *net.TCPAddr) string {
	host := address.IP.String()
	if address.IP.IsUnspecified() {
		if address.IP.To4() != nil {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
	} else if address.Zone != "" {
		host += "%" + address.Zone
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(address.Port)), Path: "/"}).String()
}

// Wait for the UI, not the camera. The callback is also a test boundary so
// automated tests never open a real browser.
func openBrowserWhenReady(parent context.Context, address string, open func(string) error) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return open(address)
			}
		}
		if !pause(ctx, 100*time.Millisecond) {
			return fmt.Errorf("HTTP server not ready: %w", ctx.Err())
		}
	}
}
