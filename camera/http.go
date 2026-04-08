package camera

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	// ErrHTTPUnavailable reports that the frame store or HTTP server is unavailable.
	ErrHTTPUnavailable = errors.New("miot camera http unavailable")
)

// FrameStore stores the latest decoded JPEG frame and notifies waiters on updates.
type FrameStore struct {
	mu       sync.Mutex
	latest   JPEGFrame
	version  uint64
	hasFrame bool
	updateCh chan struct{}
}

// NewFrameStore constructs an empty frame store.
func NewFrameStore() *FrameStore {
	return &FrameStore{updateCh: make(chan struct{})}
}

// Store updates the latest frame and notifies listeners waiting for a newer version.
func (store *FrameStore) Store(frame JPEGFrame) {
	if store == nil || len(frame.Payload) == 0 {
		return
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	store.latest = cloneJPEGFrame(frame)
	store.hasFrame = true
	store.version++
	oldCh := store.updateCh
	store.updateCh = make(chan struct{})
	close(oldCh)
}

// Snapshot returns the latest frame and version if one exists.
func (store *FrameStore) Snapshot() (JPEGFrame, uint64, bool) {
	if store == nil {
		return JPEGFrame{}, 0, false
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if !store.hasFrame {
		return JPEGFrame{}, 0, false
	}
	return cloneJPEGFrame(store.latest), store.version, true
}

// WaitForUpdate blocks until a newer frame than afterVersion is available.
func (store *FrameStore) WaitForUpdate(ctx context.Context, afterVersion uint64) (JPEGFrame, uint64, error) {
	if store == nil {
		return JPEGFrame{}, afterVersion, fmt.Errorf("%w: frame store unavailable", ErrHTTPUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		store.mu.Lock()
		if store.hasFrame && store.version > afterVersion {
			frame := cloneJPEGFrame(store.latest)
			version := store.version
			store.mu.Unlock()
			return frame, version, nil
		}
		ch := store.updateCh
		store.mu.Unlock()

		select {
		case <-ctx.Done():
			return JPEGFrame{}, afterVersion, ctx.Err()
		case <-ch:
		}
	}
}

// HTTPServerOptions configures the preview HTTP server.
type HTTPServerOptions struct {
	FrameStore     *FrameStore
	Boundary       string
	RepeatInterval time.Duration
}

// HTTPServer serves health, snapshot, and MJPEG endpoints backed by a frame store.
type HTTPServer struct {
	frameStore     *FrameStore
	boundary       string
	repeatInterval time.Duration
	mux            *http.ServeMux
}

// NewHTTPServer constructs a preview HTTP server.
func NewHTTPServer(options HTTPServerOptions) *HTTPServer {
	boundary := strings.TrimSpace(options.Boundary)
	if boundary == "" {
		boundary = "camera-probe-frame"
	}
	repeatInterval := options.RepeatInterval
	if repeatInterval <= 0 {
		repeatInterval = 250 * time.Millisecond
	}

	server := &HTTPServer{
		frameStore:     options.FrameStore,
		boundary:       boundary,
		repeatInterval: repeatInterval,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.handleHealthz)
	mux.HandleFunc("/snapshot.jpg", server.handleSnapshot)
	mux.HandleFunc("/stream.mjpeg", server.handleStream)
	server.mux = mux
	return server
}

// Handler returns the configured HTTP handler.
func (server *HTTPServer) Handler() http.Handler {
	if server == nil || server.mux == nil {
		return http.NotFoundHandler()
	}
	return server.mux
}

func (server *HTTPServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("ok\n"))
	}
}

func (server *HTTPServer) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	frame, _, ok := server.snapshot()
	if !ok {
		http.Error(w, "latest frame unavailable", http.StatusServiceUnavailable)
		return
	}
	_ = server.writeJPEGFrame(w, r.Method, frame)
}

func (server *HTTPServer) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if server.frameStore == nil {
		http.Error(w, "latest frame unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Connection", "close")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+server.boundary)

	flusher, _ := w.(http.Flusher)
	ctx := r.Context()
	frame, version, err := server.waitForSnapshot(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if err := server.writeMJPEGFrame(w, frame); err != nil {
		return
	}
	if flusher != nil {
		flusher.Flush()
	}

	for {
		waitCtx := ctx
		var cancel context.CancelFunc
		if server.repeatInterval > 0 {
			waitCtx, cancel = context.WithTimeout(ctx, server.repeatInterval)
		}
		frame, version, err = server.frameStore.WaitForUpdate(waitCtx, version)
		if cancel != nil {
			cancel()
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				frame, _, ok := server.snapshot()
				if !ok {
					continue
				}
				if err := server.writeMJPEGFrame(w, frame); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
				continue
			}
			return
		}
		if err := server.writeMJPEGFrame(w, frame); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (server *HTTPServer) snapshot() (JPEGFrame, uint64, bool) {
	if server == nil || server.frameStore == nil {
		return JPEGFrame{}, 0, false
	}
	return server.frameStore.Snapshot()
}

func (server *HTTPServer) waitForSnapshot(ctx context.Context) (JPEGFrame, uint64, error) {
	if server == nil || server.frameStore == nil {
		return JPEGFrame{}, 0, fmt.Errorf("%w: frame store unavailable", ErrHTTPUnavailable)
	}

	frame, version, ok := server.frameStore.Snapshot()
	if ok {
		return frame, version, nil
	}
	return server.frameStore.WaitForUpdate(ctx, 0)
}

func (server *HTTPServer) writeJPEGFrame(w http.ResponseWriter, method string, frame JPEGFrame) error {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(frame.Payload)))
	w.WriteHeader(http.StatusOK)
	if method == http.MethodHead {
		return nil
	}
	_, err := w.Write(frame.Payload)
	return err
}

func (server *HTTPServer) writeMJPEGFrame(w http.ResponseWriter, frame JPEGFrame) error {
	if _, err := fmt.Fprintf(w, "--%s\r\n", server.boundary); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Type: image/jpeg\r\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(frame.Payload)); err != nil {
		return err
	}
	if _, err := w.Write(frame.Payload); err != nil {
		return err
	}
	_, err := writeString(w, "\r\n")
	return err
}

func cloneJPEGFrame(frame JPEGFrame) JPEGFrame {
	frame.Payload = append([]byte(nil), frame.Payload...)
	return frame
}

func writeString(w http.ResponseWriter, value string) (int, error) {
	if writer, ok := any(w).(interface{ WriteString(string) (int, error) }); ok {
		return writer.WriteString(value)
	}
	return w.Write([]byte(value))
}
