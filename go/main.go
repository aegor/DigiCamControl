package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type stringsFlag []string

func (s *stringsFlag) String() string { return fmt.Sprint([]string(*s)) }
func (s *stringsFlag) Set(v string) error {
	if v == "" {
		return errors.New("camera/device must not be empty")
	}
	*s = append(*s, v)
	return nil
}

type config struct {
	cameras, usb          stringsFlag
	bind, host, recordDir string
	port                  int
	noBrowser             bool
	desktop               bool
}

func parseConfig(args []string, output io.Writer) (config, error) {
	var c config
	fs := flag.NewFlagSet("dicam", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Var(&c.cameras, "camera", "WiFi camera IP; repeat for multiple cameras")
	fs.Var(&c.usb, "usb", "Linux UVC device (/dev/video0), or auto; repeat for multiple devices")
	fs.StringVar(&c.bind, "bind", "192.168.1.10", "local IP for PTP and ping; empty disables source binding")
	fs.StringVar(&c.host, "host", "127.0.0.1", "HTTP listening address")
	fs.IntVar(&c.port, "port", 5000, "HTTP listening port")
	fs.BoolVar(&c.noBrowser, "no-browser", false, "do not open the browser on startup")
	fs.BoolVar(&c.desktop, "desktop", false, "desktop child process: ephemeral port, readiness on stdout, shutdown on stdin EOF")
	home, _ := os.UserHomeDir()
	fs.StringVar(&c.recordDir, "record-dir", filepath.Join(home, "larkfly_recordings"), "USB recordings directory")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() != 0 {
		return c, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	if c.port < 0 || c.port > 65535 || (c.port == 0 && !c.desktop) {
		return c, errors.New("port must be between 1 and 65535")
	}
	if c.desktop {
		c.host = "127.0.0.1"
		c.noBrowser = true
	}
	if c.bind != "" && net.ParseIP(c.bind) == nil {
		return c, fmt.Errorf("invalid bind IP %q", c.bind)
	}
	for _, host := range c.cameras {
		if net.ParseIP(host) == nil {
			return c, fmt.Errorf("invalid camera IP %q", host)
		}
	}
	if len(c.cameras) == 0 && len(c.usb) == 0 {
		c.cameras = append(c.cameras, "192.168.1.1")
	}
	return c, nil
}
func usbDevices(specs []string) ([]string, error) {
	if len(specs) > 0 && runtime.GOOS != "linux" {
		return nil, errors.New("--usb requires Linux V4L2; WiFi cameras work on macOS, Linux and Windows")
	}
	seen := map[string]bool{}
	var devices []string
	for _, spec := range specs {
		paths := []string{spec}
		if spec == "auto" {
			matches, err := filepath.Glob("/dev/video[0-9]*")
			if err != nil {
				return nil, err
			}
			sort.Strings(matches)
			paths = nil
			for _, path := range matches {
				n, err := strconv.Atoi(filepath.Base(path)[5:])
				if err == nil && n%2 == 0 {
					paths = append(paths, path)
				}
			}
			if len(paths) == 0 {
				return nil, errors.New("no UVC devices found")
			}
		}
		for _, path := range paths {
			if !seen[path] {
				seen[path] = true
				devices = append(devices, path)
			}
		}
	}
	return devices, nil
}
func run(args []string) error {
	c, err := parseConfig(args, os.Stderr)
	if err != nil {
		return err
	}
	if len(c.cameras) > 0 && runtime.GOOS != "windows" {
		if _, err = exec.LookPath("ping"); err != nil {
			return fmt.Errorf("ping is required for camera monitoring: %w", err)
		}
	}
	devices, err := usbDevices(c.usb)
	if err != nil {
		return err
	}
	address := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	signalCtx, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignal()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()
	if c.desktop {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			cancel()
		}()
	}
	app := &application{ctx: ctx}
	var workers []*worker
	for _, host := range c.cameras {
		workers = append(workers, newWorker(len(workers), host, c.bind, c.recordDir, false))
	}
	for _, device := range devices {
		workers = append(workers, newWorker(len(workers), device, "", c.recordDir, true))
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		w.ctx = ctx
		app.cameras = append(app.cameras, w)
		wg.Add(1)
		go func(w *worker) { defer wg.Done(); w.run(ctx) }(w)
	}
	server := &http.Server{Handler: app.routes(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	if c.desktop {
		fmt.Printf("DICAM_READY %s\n", browserURL(listener.Addr().(*net.TCPAddr)))
	}
	log.Printf("DiCam Controller: http://%s/ (FFmpeg %s, linked libraries)", address, ffmpegVersion())
	if !c.noBrowser {
		url := browserURL(listener.Addr().(*net.TCPAddr))
		go func() {
			if err := openBrowserWhenReady(ctx, url, openBrowser); err != nil && ctx.Err() == nil {
				log.Printf("Cannot open browser: %v; open %s manually", err, url)
			}
		}()
	}
	select {
	case <-ctx.Done():
	case err = <-serveErr:
	}
	cancel()
	shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
		server.Close()
	}
	wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Print(err)
		os.Exit(1)
	}
}
