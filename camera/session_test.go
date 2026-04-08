package camera

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResolverChainPrefersEarlierResolvers(t *testing.T) {
	t.Parallel()

	var calls []string
	chain := ResolverChain{
		Resolvers: []Resolver{
			resolverFunc{
				name: "preferred",
				resolve: func(context.Context, Target, Profile) (Session, error) {
					calls = append(calls, "preferred")
					return Session{
						SessionID: "session-preferred",
						Transport: "rtsp",
						StreamURL: "rtsp://preferred.example.invalid/live/main",
						Codec:     "h264",
					}, nil
				},
			},
			resolverFunc{
				name: "fallback",
				resolve: func(context.Context, Target, Profile) (Session, error) {
					calls = append(calls, "fallback")
					return Session{}, errors.New("unexpected fallback")
				},
			},
		},
	}

	session, err := chain.Resolve(context.Background(), Target{CameraID: "camera-1", Model: "xiaomi.camera.v1"}, Profile{Name: "mijia.camera.family"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got, want := strings.Join(calls, ","), "preferred"; got != want {
		t.Fatalf("resolver order = %q, want %q", got, want)
	}
	if got, want := session.ResolverName, "preferred"; got != want {
		t.Fatalf("session.ResolverName = %q, want %q", got, want)
	}
	if got, want := session.CameraID, "camera-1"; got != want {
		t.Fatalf("session.CameraID = %q, want %q", got, want)
	}
}

func TestResolverChainFallsBackToLaterResolver(t *testing.T) {
	t.Parallel()

	var calls []string
	chain := ResolverChain{
		Resolvers: []Resolver{
			resolverFunc{
				name: "profile-specific",
				resolve: func(context.Context, Target, Profile) (Session, error) {
					calls = append(calls, "profile-specific")
					return Session{}, errors.New("profile-specific unavailable")
				},
			},
			resolverFunc{
				name: "generic",
				resolve: func(context.Context, Target, Profile) (Session, error) {
					calls = append(calls, "generic")
					return Session{
						SessionID: "session-generic",
						Transport: "rtsp",
						StreamURL: "rtsp://generic.example.invalid/live/main",
						Codec:     "h264",
					}, nil
				},
			},
		},
	}

	session, err := chain.Resolve(context.Background(), Target{CameraID: "camera-1", Model: "xiaomi.camera.v1"}, Profile{Name: "mijia.camera.family"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got, want := strings.Join(calls, ","), "profile-specific,generic"; got != want {
		t.Fatalf("resolver order = %q, want %q", got, want)
	}
	if got, want := session.ResolverName, "generic"; got != want {
		t.Fatalf("session.ResolverName = %q, want %q", got, want)
	}
}

func TestParseSessionPayloadSuccess(t *testing.T) {
	t.Parallel()

	session, err := ParseSessionPayload([]byte(`{
		"code": 0,
		"message": "ok",
		"data": {
			"session_id": "session-abc123",
			"transport": "rtsp",
			"stream_url": "rtsp://camera.example.invalid/live/main?token=redacted",
			"codec": "h264",
			"token": "redacted-token"
		}
	}`))
	if err != nil {
		t.Fatalf("ParseSessionPayload() error = %v", err)
	}
	if got, want := session.SessionID, "session-abc123"; got != want {
		t.Fatalf("session.SessionID = %q, want %q", got, want)
	}
	if got, want := session.StreamURL, "rtsp://camera.example.invalid/live/main?token=redacted"; got != want {
		t.Fatalf("session.StreamURL = %q, want %q", got, want)
	}
}

func TestHTTPResolverReportsRedactedFailurePayload(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"code": 401,
		"message": "xiaomi camera session rejected",
		"data": {
			"error": "token expired",
			"request_id": "req-123",
			"token": "supersecret",
			"stream_url": "rtsp://private.camera.local/live/main?token=supersecret"
		}
	}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)

	resolver := NewHTTPResolver(HTTPResolverOptions{
		Name:   "generic",
		Client: server.Client(),
		Request: func(context.Context, Target, Profile) (*http.Request, error) {
			return http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		},
		Parse: ParseSessionPayload,
	})

	_, err := resolver.Resolve(context.Background(), Target{CameraID: "camera-1"}, Profile{Name: "generic"})
	if err == nil {
		t.Fatal("Resolve() error = nil, want session failure")
	}
	if !errors.Is(err, ErrSessionResolution) {
		t.Fatalf("Resolve() error = %v, want ErrSessionResolution", err)
	}
	if got := err.Error(); !strings.Contains(got, `"code":401`) || !strings.Contains(got, "token expired") {
		t.Fatalf("Resolve() error = %q, want Xiaomi payload context", got)
	}
	if got := err.Error(); strings.Contains(got, "private.camera.local") || strings.Contains(got, "supersecret") || strings.Contains(got, "req-123") {
		t.Fatalf("Resolve() error = %q, want redacted secrets", got)
	}
}

func TestNewHTTPResolverDefaultsToThirtySecondTimeout(t *testing.T) {
	t.Parallel()

	resolver := NewHTTPResolver(HTTPResolverOptions{})
	if resolver == nil {
		t.Fatal("resolver = nil, want resolver")
	}
	if resolver.client == nil {
		t.Fatal("resolver.client = nil, want default client")
	}
	if got, want := resolver.client.Timeout, 30*time.Second; got != want {
		t.Fatalf("resolver.client.Timeout = %s, want %s", got, want)
	}
}

type resolverFunc struct {
	name    string
	resolve func(context.Context, Target, Profile) (Session, error)
}

func (f resolverFunc) Name() string {
	return f.name
}

func (f resolverFunc) Resolve(ctx context.Context, target Target, profile Profile) (Session, error) {
	return f.resolve(ctx, target, profile)
}
