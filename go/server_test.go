package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeCamera struct {
	mu    sync.Mutex
	s     status
	jpeg  []byte
	seq   uint64
	err   error
	calls []bool
}

func (f *fakeCamera) snapshot() status        { f.mu.Lock(); defer f.mu.Unlock(); return f.s }
func (f *fakeCamera) frame() ([]byte, uint64) { f.mu.Lock(); defer f.mu.Unlock(); return f.jpeg, f.seq }
func (f *fakeCamera) record(on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, on)
	if f.err == nil {
		f.s.Recording = on
	}
	return f.err
}
func testApp(t *testing.T) (*application, *fakeCamera, http.Handler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := &fakeCamera{s: status{State: "running"}}
	a := &application{ctx: ctx, cameras: []controlledCamera{f}}
	return a, f, a.routes()
}
func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(method, path, strings.NewReader(body)))
	return r
}
func TestConfig(t *testing.T) {
	c, err := parseConfig(nil, io.Discard)
	if err != nil || c.bind != "192.168.1.10" || c.port != 5000 || c.cameras[0] != "192.168.1.1" {
		t.Fatal(c, err)
	}
	c, err = parseConfig([]string{"--port", "5002", "--bind", "", "--camera", "192.168.1.2", "--camera", "192.168.1.3"}, io.Discard)
	if err != nil || c.port != 5002 || c.bind != "" || len(c.cameras) != 2 {
		t.Fatal(c, err)
	}
	for _, args := range [][]string{{"--port", "0"}, {"--camera", "-bad"}, {"--bind", "bad"}, {"oops"}} {
		if _, err = parseConfig(args, io.Discard); err == nil {
			t.Fatal(args)
		}
	}
}
func TestUIAndRecordingRoutes(t *testing.T) {
	_, f, h := testApp(t)
	html := call(h, "GET", "/", "")
	if html.Code != 200 || !strings.Contains(html.Body.String(), `id="preview-0"`) || !strings.Contains(html.Body.String(), "Server is down") {
		t.Fatal(html.Code, html.Body.String())
	}
	if strings.Contains(html.Body.String(), "{%") || strings.Contains(html.Body.String(), "/api/capture") {
		t.Fatal("unconverted or obsolete UI")
	}
	for _, command := range []string{"start", "stop"} {
		r := call(h, "POST", "/api/record/"+command, "")
		if r.Code != 200 || !strings.Contains(r.Body.String(), `"ok":true`) {
			t.Fatal(r.Body.String())
		}
		if f.snapshot().Recording != (command == "start") {
			t.Fatal("wrong recording state")
		}
	}
	if r := call(h, "GET", "/api/record/start", ""); r.Code != 405 {
		t.Fatal(r.Code)
	}
	if r := call(h, "POST", "/api/capture", ""); r.Code != 404 {
		t.Fatal(r.Code)
	}
	if r := call(h, "GET", "/video_feed/9", ""); r.Code != 404 {
		t.Fatal(r.Code)
	}
}
func TestBurstStopsAutomatically(t *testing.T) {
	_, f, h := testApp(t)
	r := call(h, "POST", "/api/burst", `{"duration":1}`)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if call(h, "POST", "/api/burst", `{"duration":1}`).Code != 409 {
		t.Fatal("overlapping burst accepted")
	}
	deadline := time.Now().Add(3 * time.Second)
	for f.snapshot().Recording && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.snapshot().Recording {
		t.Fatal("burst did not stop")
	}
	var body struct{ Burst burstState }
	json.Unmarshal(call(h, "GET", "/api/status", "").Body.Bytes(), &body)
	if body.Burst.Active {
		t.Fatal("burst remains active")
	}
}
func TestManualStopCancelsOldBurstTimer(t *testing.T) {
	_, f, h := testApp(t)
	call(h, "POST", "/api/burst", `{"duration":1}`)
	call(h, "POST", "/api/record/stop", "")
	call(h, "POST", "/api/record/start", "")
	time.Sleep(1100 * time.Millisecond)
	if !f.snapshot().Recording {
		t.Fatal("old burst stopped new recording")
	}
}
func TestMJPEGSurvivesConnectionLoss(t *testing.T) {
	_, f, h := testApp(t)
	server := httptest.NewServer(h)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/video_feed/0", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(response.Body, params["boundary"])
	f.mu.Lock()
	f.jpeg = []byte{0xff, 0xd8, 1, 0xff, 0xd9}
	f.seq = 1
	f.mu.Unlock()
	// multipart.NextPart requires the following boundary to finish a part;
	// read the advertised Content-Length instead of waiting for another frame.
	first, err := reader.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 5)
	if _, err = io.ReadFull(first, b); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.jpeg = nil
	f.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	f.mu.Lock()
	f.jpeg = []byte{0xff, 0xd8, 2, 0xff, 0xd9}
	f.seq = 2
	f.mu.Unlock()
	second, err := reader.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(second, b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, []byte{0xff, 0xd8, 2, 0xff, 0xd9}) {
		t.Fatal(b)
	}
}

func TestFailedBurstReportsErrorWithoutStartingTimer(t *testing.T) {
	a, f, h := testApp(t)
	f.err = io.ErrClosedPipe
	r := call(h, "POST", "/api/burst", `{"duration":1}`)
	var body struct {
		Started bool                     `json:"started"`
		Results map[string]controlResult `json:"results"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Started || body.Results["0"].OK || body.Results["0"].Error == "" {
		t.Fatal(r.Body.String())
	}
	a.mu.Lock()
	active := a.burst.Active
	a.mu.Unlock()
	if active {
		t.Fatal("failed burst started a timer")
	}
	if call(h, "POST", "/api/burst", `{"duration":`).Code != 400 {
		t.Fatal("invalid JSON accepted")
	}
}
