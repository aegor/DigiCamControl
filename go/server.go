package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"sync"
	"time"
)

//go:embed web/index.html
var webFiles embed.FS
var page = template.Must(template.ParseFS(webFiles, "web/index.html"))

type controlledCamera interface {
	snapshot() status
	frame() ([]byte, uint64)
	record(bool) error
}
type controlResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}
type burstState struct {
	Active    bool    `json:"active"`
	StartedAt float64 `json:"started_at"`
	Duration  int     `json:"duration_s"`
	Elapsed   float64 `json:"elapsed_s"`
}
type application struct {
	cameras     []controlledCamera
	controlMu   sync.Mutex // Prevent old burst timers stopping a newer recording.
	mu          sync.Mutex
	burst       burstState
	burstID     uint64
	cancelBurst context.CancelFunc
	ctx         context.Context
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func (a *application) controls(on bool) map[string]controlResult {
	results := make(map[string]controlResult)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, c := range a.cameras {
		wg.Add(1)
		go func(i int, c controlledCamera) {
			defer wg.Done()
			err := c.record(on)
			r := controlResult{OK: err == nil}
			if err != nil {
				r.Error = err.Error()
			}
			mu.Lock()
			results[strconv.Itoa(i)] = r
			mu.Unlock()
		}(i, c)
	}
	wg.Wait()
	return results
}
func (a *application) stopBurstLocked() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelBurst != nil {
		a.cancelBurst()
		a.cancelBurst = nil
	}
	a.burstID++
	a.burst.Active = false
}
func (a *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		slots := make([]int, len(a.cameras))
		for i := range slots {
			slots[i] = i
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = page.Execute(w, slots)
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		slots := make([]status, 0, len(a.cameras))
		for _, c := range a.cameras {
			slots = append(slots, c.snapshot())
		}
		a.mu.Lock()
		burst := a.burst
		a.mu.Unlock()
		if burst.Active {
			burst.Elapsed = float64(time.Now().UnixNano())/1e9 - burst.StartedAt
		}
		writeJSON(w, 200, map[string]any{"slots": slots, "burst": burst})
	})
	mux.HandleFunc("GET /video_feed/{slot}", a.video)
	mux.HandleFunc("POST /api/record/start", func(w http.ResponseWriter, r *http.Request) {
		a.controlMu.Lock()
		defer a.controlMu.Unlock()
		a.mu.Lock()
		active := a.burst.Active
		a.mu.Unlock()
		if active {
			writeJSON(w, 409, map[string]string{"error": "burst already active"})
			return
		}
		writeJSON(w, 200, map[string]any{"results": a.controls(true)})
	})
	mux.HandleFunc("POST /api/record/stop", func(w http.ResponseWriter, r *http.Request) {
		a.controlMu.Lock()
		defer a.controlMu.Unlock()
		a.stopBurstLocked()
		writeJSON(w, 200, map[string]any{"results": a.controls(false)})
	})
	mux.HandleFunc("POST /api/burst", a.startBurst)
	return mux
}
func (a *application) startBurst(w http.ResponseWriter, r *http.Request) {
	body := struct {
		Duration int `json:"duration"`
	}{Duration: 3}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "expected JSON: {\"duration\": 3}"})
		return
	}
	if body.Duration < 1 {
		body.Duration = 1
	}
	if body.Duration > 60 {
		body.Duration = 60
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	a.mu.Lock()
	active := a.burst.Active
	a.mu.Unlock()
	if active {
		writeJSON(w, 409, map[string]string{"error": "burst already active"})
		return
	}
	results := a.controls(true)
	success := false
	for _, r := range results {
		success = success || r.OK
	}
	if !success {
		writeJSON(w, 200, map[string]any{"started": false, "results": results})
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.burstID++
	id := a.burstID
	a.cancelBurst = cancel
	a.burst = burstState{Active: true, StartedAt: float64(time.Now().UnixNano()) / 1e9, Duration: body.Duration}
	a.mu.Unlock()
	go func() {
		defer cancel()
		if !pause(ctx, time.Duration(body.Duration)*time.Second) {
			return
		}
		a.controlMu.Lock()
		defer a.controlMu.Unlock()
		a.mu.Lock()
		valid := a.burst.Active && a.burstID == id
		a.mu.Unlock()
		if valid {
			a.controls(false)
			a.stopBurstLocked()
		}
	}()
	writeJSON(w, 200, map[string]any{"started": true, "duration": body.Duration, "results": results})
}
func (a *application) video(w http.ResponseWriter, r *http.Request) {
	slot, err := strconv.Atoi(r.PathValue("slot"))
	if err != nil || slot < 0 || slot >= len(a.cameras) {
		http.NotFound(w, r)
		return
	}
	controller := http.NewResponseController(w)
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("X-Accel-Buffering", "no")
	if err = controller.Flush(); err != nil {
		return
	}
	ticker := time.NewTicker(time.Second / 15)
	defer ticker.Stop()
	var previous uint64
	for {
		select {
		case <-r.Context().Done():
			return
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			jpeg, seq := a.cameras[slot].frame()
			if len(jpeg) == 0 || seq == previous {
				continue
			}
			previous = seq
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err = fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(jpeg)); err != nil {
				return
			}
			if _, err = w.Write(jpeg); err != nil {
				return
			}
			if _, err = w.Write([]byte("\r\n")); err != nil {
				return
			}
			if err = controller.Flush(); err != nil {
				return
			}
		}
	}
}
