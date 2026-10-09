package preview

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func serve(e *env, id int64, rng string) (*httptest.ResponseRecorder, error) {
	r := httptest.NewRequest("GET", "/x", nil)
	if rng != "" {
		r.Header.Set("Range", rng)
	}
	w := httptest.NewRecorder()
	return w, e.svc.Serve(w, r, id)
}

// Review focus 5: an iPhone's first two requests while the file is still growing.
func TestServeRangesWhileDownloading(t *testing.T) {
	e := newEnv(t)
	e.dl.meta.Size = 1000
	p, _ := e.svc.Start(context.Background(), e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
	e.dl.chunks <- 10
	e.waitFile("pv_00000001.audio.m4a", 10)
	w, err := serve(e, p.ID, "bytes=0-1")
	if err != nil || w.Code != 206 || w.Header().Get("Content-Range") != "bytes 0-1/1000" || !bytes.Equal(w.Body.Bytes(), e.dl.data[:2]) ||
		w.Header().Get("Content-Type") != "audio/mp4" || w.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("%v %d %v %q", err, w.Code, w.Header(), w.Body.Bytes())
	}
	type result struct {
		w   *httptest.ResponseRecorder
		err error
	}
	got := make(chan result, 1)
	go func() { w, err := serve(e, p.ID, "bytes=0-"); got <- result{w, err} }()
	time.Sleep(50 * time.Millisecond)
	e.dl.chunks <- 300
	e.dl.chunks <- 300
	close(e.dl.chunks)
	r := <-got
	if r.err != nil || r.w.Code != 206 || r.w.Header().Get("Content-Range") != "bytes 0-999/1000" || r.w.Header().Get("Content-Length") != "1000" ||
		!bytes.Equal(r.w.Body.Bytes(), e.dl.data) {
		t.Fatalf("%v %d %v %d bytes", r.err, r.w.Code, r.w.Header(), r.w.Body.Len())
	}
	if w, _ := serve(e, p.ID, "bytes=2000-"); w.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("past the end: %d", w.Code)
	}
	e.waitIdle()
}

func TestServeWithoutRangeStreams200(t *testing.T) {
	e := newEnv(t)
	e.dl.meta.Size = 1000
	p, _ := e.svc.Start(context.Background(), e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
	e.dl.chunks <- 100
	e.waitFile("pv_00000001.audio.m4a", 100)
	go func() { time.Sleep(30 * time.Millisecond); close(e.dl.chunks) }()
	w, err := serve(e, p.ID, "")
	if err != nil || w.Code != 200 || w.Header().Get("Content-Length") != "1000" || !bytes.Equal(w.Body.Bytes(), e.dl.data) {
		t.Fatalf("%v %d %d", err, w.Code, w.Body.Len())
	}
	e.waitIdle()
}

func TestServeUnknownSizeWaitsForTheEnd(t *testing.T) {
	e := newEnv(t)
	p, _ := e.svc.Start(context.Background(), e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
	e.dl.chunks <- 10
	go func() { time.Sleep(50 * time.Millisecond); close(e.dl.chunks) }()
	w, err := serve(e, p.ID, "bytes=0-1")
	if err != nil || w.Code != 206 || w.Header().Get("Content-Range") != "bytes 0-1/1000" {
		t.Fatalf("%v %d %v", err, w.Code, w.Header())
	}
	e.waitIdle()
}

func TestServeWaitCapAndFailure(t *testing.T) {
	e := newEnv(t)
	e.svc.WaitCap = 100 * time.Millisecond
	e.dl.meta.Size = 1000
	p, _ := e.svc.Start(context.Background(), e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
	w, err := serve(e, p.ID, "bytes=0-1")
	if !errors.Is(err, ErrNotReady) || w.Body.Len() != 0 || len(w.Header()) != 0 {
		t.Fatalf("no byte within the cap: %v, nothing written (%d %v)", err, w.Code, w.Header())
	}
	first := e.dl.chunks
	// A second preview whose download fails after 10 bytes.
	e.svc.WaitCap = 2 * time.Second
	e.dl.setErr(errors.New("yt-dlp: boom"))
	chunks := e.dl.next(false)
	q, _ := e.svc.Start(context.Background(), e.anna, Request{VideoID: "pv_00000002", Media: "audio"})
	chunks <- 10
	e.waitFile("pv_00000002.audio.m4a", 10)
	go func() { time.Sleep(50 * time.Millisecond); close(chunks) }()
	w, err = serve(e, q.ID, "bytes=0-")
	if err != nil || w.Code != 206 || w.Body.Len() != 10 {
		t.Fatalf("a failure ends the response early: %v %d %d", err, w.Code, w.Body.Len())
	}
	e.waitStatus(q.ID, "failed")
	if _, err := serve(e, q.ID, "bytes=0-1"); !errors.Is(err, ErrFailed) {
		t.Fatal(err)
	}
	close(first) // let the first preview's download end too
	e.waitIdle()
}

// Review fix: the size arrives after the player's first Range request
// (yt-dlp announces it seconds after the POST): the request waits for it
// and is answered 206 with the exact total while the download still runs.
func TestServeSizeArrivesAfterTheRequest(t *testing.T) {
	e := newEnv(t)
	e.dl.meta.Size = 1000
	gate := make(chan struct{})
	e.dl.metaGate = gate
	p, _ := e.svc.Start(context.Background(), e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
	type result struct {
		w   *httptest.ResponseRecorder
		err error
	}
	got := make(chan result, 1)
	go func() { w, err := serve(e, p.ID, "bytes=0-1"); got <- result{w, err} }()
	time.Sleep(50 * time.Millisecond)
	close(gate)
	e.dl.chunks <- 10
	r := <-got
	if r.err != nil || r.w.Code != 206 || r.w.Header().Get("Content-Range") != "bytes 0-1/1000" || !bytes.Equal(r.w.Body.Bytes(), e.dl.data[:2]) {
		t.Fatalf("%v %d %v", r.err, r.w.Code, r.w.Header())
	}
	if q, _ := e.svc.Get(context.Background(), p.ID); q.Status != "downloading" {
		t.Fatal("answered before the download finished")
	}
	close(e.dl.chunks)
	e.waitIdle()
}
