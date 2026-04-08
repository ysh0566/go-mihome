package camera

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPServerServesLatestSnapshot(t *testing.T) {
	t.Parallel()

	store := NewFrameStore()
	older := []byte{0xFF, 0xD8, 0x01, 0x02, 0x03, 0xFF, 0xD9}
	newer := append([]byte(nil), testJPEGFrame...)
	store.Store(JPEGFrame{Payload: append([]byte(nil), older...)})
	store.Store(JPEGFrame{Payload: append([]byte(nil), newer...)})

	server := NewHTTPServer(HTTPServerOptions{FrameStore: store})
	req := httptest.NewRequest(http.MethodGet, "/snapshot.jpg", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "image/jpeg"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if !bytes.Equal(rec.Body.Bytes(), newer) {
		t.Fatalf("body = %x, want %x", rec.Body.Bytes(), newer)
	}
}

func TestHTTPServerReportsHealthz(t *testing.T) {
	t.Parallel()

	server := NewHTTPServer(HTTPServerOptions{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := strings.TrimSpace(rec.Body.String()), "ok"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestHTTPServerStreamsMJPEGWithBoundary(t *testing.T) {
	t.Parallel()

	store := NewFrameStore()
	store.Store(JPEGFrame{Payload: append([]byte(nil), testJPEGFrame...)})

	server := NewHTTPServer(HTTPServerOptions{FrameStore: store})
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	resp, err := http.Get(testServer.URL + "/stream.mjpeg")
	if err != nil {
		t.Fatalf("http.Get() error = %v", err)
	}
	defer resp.Body.Close()

	if got, want := resp.Header.Get("Content-Type"), "multipart/x-mixed-replace; boundary=camera-probe-frame"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if got, want := resp.Header.Get("Cache-Control"), "no-store, no-cache, must-revalidate, max-age=0"; got != want {
		t.Fatalf("Cache-Control = %q, want %q", got, want)
	}

	chunk := readStreamChunk(t, resp.Body)
	if !strings.Contains(chunk, "--camera-probe-frame") {
		t.Fatalf("stream chunk = %q, want frame boundary", chunk)
	}
	if !strings.Contains(chunk, "Content-Type: image/jpeg") {
		t.Fatalf("stream chunk = %q, want JPEG content type", chunk)
	}
	if !bytes.Contains([]byte(chunk), testJPEGFrame) {
		t.Fatalf("stream chunk = %x, want JPEG bytes", []byte(chunk))
	}
}

func TestHTTPServerRepeatsLatestMJPEGFrameWhileIdle(t *testing.T) {
	t.Parallel()

	store := NewFrameStore()
	store.Store(JPEGFrame{Payload: append([]byte(nil), testJPEGFrame...)})

	server := NewHTTPServer(HTTPServerOptions{
		FrameStore:     store,
		RepeatInterval: 20 * time.Millisecond,
	})
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	resp, err := http.Get(testServer.URL + "/stream.mjpeg")
	if err != nil {
		t.Fatalf("http.Get() error = %v", err)
	}
	defer resp.Body.Close()

	count := countMJPEGBoundaries(t, resp.Body, 2, 300*time.Millisecond)
	if got, want := count, 2; got < want {
		t.Fatalf("boundary count = %d, want at least %d repeated frames", got, want)
	}
}

func TestFrameStoreWaitsForNextFrame(t *testing.T) {
	t.Parallel()

	store := NewFrameStore()
	done := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		store.Store(JPEGFrame{Payload: append([]byte(nil), testJPEGFrame...)})
		close(done)
	}()

	frame, version, err := store.WaitForUpdate(context.Background(), 0)
	if err != nil {
		t.Fatalf("WaitForUpdate() error = %v", err)
	}
	if version == 0 {
		t.Fatal("version = 0, want updated frame version")
	}
	if !bytes.Equal(frame.Payload, testJPEGFrame) {
		t.Fatalf("frame = %x, want %x", frame.Payload, testJPEGFrame)
	}
	<-done
}

func readStreamChunk(t *testing.T, body io.ReadCloser) string {
	t.Helper()

	buf := make([]byte, 1024)
	deadline := time.After(2 * time.Second)
	var chunk []byte
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for MJPEG chunk")
		default:
		}

		n, err := body.Read(buf)
		if n > 0 {
			chunk = append(chunk, buf[:n]...)
			if bytes.Contains(chunk, testJPEGFrame) && strings.Contains(string(chunk), "--camera-probe-frame") {
				return string(chunk)
			}
		}
		if err != nil {
			return string(chunk)
		}
	}
}

func countMJPEGBoundaries(t *testing.T, body io.ReadCloser, target int, timeout time.Duration) int {
	t.Helper()

	countCh := make(chan int, 1)
	go func() {
		buf := make([]byte, 1024)
		count := 0
		var chunk []byte
		for {
			n, err := body.Read(buf)
			if n > 0 {
				chunk = append(chunk, buf[:n]...)
				count = bytes.Count(chunk, []byte("--camera-probe-frame"))
				if count >= target {
					countCh <- count
					return
				}
			}
			if err != nil {
				countCh <- count
				return
			}
		}
	}()

	select {
	case count := <-countCh:
		return count
	case <-time.After(timeout):
		return 0
	}
}

var testJPEGFrame = []byte{0xFF, 0xD8, 0xFF, 0xD9}
