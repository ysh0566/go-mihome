package camera

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// CameraSessionResolverAppOptions configures the local session-resolver HTTP app.
type CameraSessionResolverAppOptions struct {
	Stdout     io.Writer
	Backend    SessionResolverBackend
	SessionTTL time.Duration
	Logger     any
}

// CameraSessionResolverApp exposes a simple local session-resolver service.
type CameraSessionResolverApp struct {
	stdout     io.Writer
	backend    SessionResolverBackend
	sessionTTL time.Duration
}

// NewCameraSessionResolverApp constructs a local session-resolver HTTP app.
func NewCameraSessionResolverApp(options CameraSessionResolverAppOptions) *CameraSessionResolverApp {
	stdout := options.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	backend := options.Backend
	if backend == nil {
		backend = newRuntimeCameraSessionResolverBackend(options.Logger)
	}
	return &CameraSessionResolverApp{
		stdout:     stdout,
		backend:    backend,
		sessionTTL: options.SessionTTL,
	}
}

// Serve starts the local resolver HTTP server until the context is canceled.
func (app *CameraSessionResolverApp) Serve(ctx context.Context, listen string) error {
	if app == nil || app.backend == nil {
		return fmt.Errorf("%w: session resolver backend unavailable", ErrRuntimeUnavailable)
	}
	listen = strings.TrimSpace(listen)
	if listen == "" {
		listen = "127.0.0.1:18082"
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	server := &http.Server{
		Handler: NewSessionResolverHTTPServer(SessionResolverHTTPServerOptions{
			Backend:    app.backend,
			SessionTTL: app.sessionTTL,
		}).Handler(),
	}

	_, _ = fmt.Fprintf(app.stdout, "resolver_url=http://%s/resolve\n", listener.Addr().String())

	serveErrCh := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			serveErrCh <- err
			return
		}
		close(serveErrCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serveErrCh:
		return err
	}
}
