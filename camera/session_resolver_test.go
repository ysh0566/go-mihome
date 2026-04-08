package camera

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDirectResolverUsesBackendProbe(t *testing.T) {
	t.Parallel()

	resolver := NewDirectResolver(DirectResolverOptions{
		Name: "runtime-local",
		Backend: fakeSessionResolverBackend{
			probe: func(context.Context, Target, Profile) (SessionResolverProbe, error) {
				return SessionResolverProbe{
					CameraID: "camera-1",
					Model:    "xiaomi.camera.v1",
					Codec:    "h265",
				}, nil
			},
		},
	})

	session, err := resolver.Resolve(context.Background(), Target{
		CameraID: "camera-1",
		Model:    "xiaomi.camera.v1",
	}, Profile{Name: "mijia.camera.family"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got, want := session.ResolverName, "runtime-local"; got != want {
		t.Fatalf("session.ResolverName = %q, want %q", got, want)
	}
	if got, want := session.Transport, transportDirectAnnexB; got != want {
		t.Fatalf("session.Transport = %q, want %q", got, want)
	}
	if got, want := session.Codec, "h265"; got != want {
		t.Fatalf("session.Codec = %q, want %q", got, want)
	}
	if got, want := session.SessionID, "camera-1"; got != want {
		t.Fatalf("session.SessionID = %q, want %q", got, want)
	}
}

func TestDirectResolverSkipProbeReturnsDirectSessionWithoutBackendProbe(t *testing.T) {
	t.Parallel()

	probed := false
	resolver := NewDirectResolver(DirectResolverOptions{
		Name:      "runtime-local",
		SkipProbe: true,
		Backend: fakeSessionResolverBackend{
			probe: func(context.Context, Target, Profile) (SessionResolverProbe, error) {
				probed = true
				return SessionResolverProbe{}, errors.New("unexpected probe")
			},
		},
	})

	session, err := resolver.Resolve(context.Background(), Target{
		CameraID: "camera-1",
		Model:    "xiaomi.camera.v1",
	}, Profile{Name: "mijia.camera.family"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if probed {
		t.Fatalf("Resolve() unexpectedly called backend Probe")
	}
	if got, want := session.Transport, transportDirectAnnexB; got != want {
		t.Fatalf("session.Transport = %q, want %q", got, want)
	}
	if got, want := session.SessionID, "camera-1"; got != want {
		t.Fatalf("session.SessionID = %q, want %q", got, want)
	}
	if got := session.Codec; got != "" {
		t.Fatalf("session.Codec = %q, want empty codec when SkipProbe is enabled", got)
	}
}

func TestDirectResolverRequiresBackend(t *testing.T) {
	t.Parallel()

	resolver := NewDirectResolver(DirectResolverOptions{Name: "runtime-local"})
	_, err := resolver.Resolve(context.Background(), Target{
		CameraID: "camera-1",
		Model:    "xiaomi.camera.v1",
	}, Profile{Name: "mijia.camera.family"})
	if !errors.Is(err, ErrSessionResolverUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrSessionResolverUnavailable", err)
	}
}

func TestSessionResolverHTTPServerResolveReturnsHTTPAnnexBSession(t *testing.T) {
	t.Parallel()

	server := NewSessionResolverHTTPServer(SessionResolverHTTPServerOptions{
		Backend: fakeSessionResolverBackend{
			probe: func(context.Context, Target, Profile) (SessionResolverProbe, error) {
				return SessionResolverProbe{
					CameraID: "camera-1",
					Model:    "xiaomi.camera.v1",
					Codec:    "h264",
				}, nil
			},
		},
		SessionTTL: time.Minute,
	})
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	body, err := json.Marshal(map[string]string{
		"camera_id": "camera-1",
		"model":     "xiaomi.camera.v1",
		"profile":   "mijia.camera.family",
	})
	if err != nil {
		t.Fatalf("Marshal(request) error = %v", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, testServer.URL+"/resolve", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := testServer.Client().Do(req)
	if err != nil {
		t.Fatalf("Do(request) error = %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	if got, want := res.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}
	payload, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("ReadAll(response) error = %v", err)
	}
	session, err := ParseSessionPayload(payload)
	if err != nil {
		t.Fatalf("ParseSessionPayload() error = %v", err)
	}
	if session.SessionID == "" {
		t.Fatal("session.SessionID = empty, want generated session id")
	}
	if got, want := session.Transport, transportHTTPAnnexB; got != want {
		t.Fatalf("session.Transport = %q, want %q", got, want)
	}
	if got, want := session.Codec, "h264"; got != want {
		t.Fatalf("session.Codec = %q, want %q", got, want)
	}
	if got := session.StreamURL; !strings.Contains(got, "/stream/") || !strings.Contains(got, "token=") {
		t.Fatalf("session.StreamURL = %q, want stream URL with token query", got)
	}
	if session.Token == "" {
		t.Fatal("session.Token = empty, want generated token")
	}
}

func TestSessionResolverHTTPServerStreamsAccessUnits(t *testing.T) {
	t.Parallel()

	wantPayload := annexBPayload([][]byte{{0x65, 0x88, 0x84}})
	server := NewSessionResolverHTTPServer(SessionResolverHTTPServerOptions{
		Backend: fakeSessionResolverBackend{
			probe: func(context.Context, Target, Profile) (SessionResolverProbe, error) {
				return SessionResolverProbe{
					CameraID: "camera-1",
					Model:    "xiaomi.camera.v1",
					Codec:    "h264",
				}, nil
			},
			open: func(context.Context, SessionResolverStreamRequest) (SessionResolverStream, error) {
				return &fakeSessionResolverStream{
					units: []AccessUnit{
						{
							Codec:            "h264",
							Payload:          wantPayload,
							PresentationTime: 250 * time.Millisecond,
						},
					},
				}, nil
			},
		},
		SessionTTL: time.Minute,
	})
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	body, err := json.Marshal(map[string]string{
		"camera_id": "camera-1",
		"model":     "xiaomi.camera.v1",
		"profile":   "mijia.camera.family",
	})
	if err != nil {
		t.Fatalf("Marshal(request) error = %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, testServer.URL+"/resolve", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := testServer.Client().Do(req)
	if err != nil {
		t.Fatalf("Do(resolve) error = %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	payload, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("ReadAll(resolve) error = %v", err)
	}
	session, err := ParseSessionPayload(payload)
	if err != nil {
		t.Fatalf("ParseSessionPayload() error = %v", err)
	}

	var got []AccessUnit
	delivered := make(chan struct{}, 1)
	client := NewProbeStreamClient(ProbeStreamClientOptions{})
	streamSession, err := client.Start(context.Background(), session, func(unit AccessUnit) {
		got = append(got, unit)
		select {
		case delivered <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = streamSession.Close() })

	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("access units not delivered before timeout")
	}

	if len(got) != 1 {
		t.Fatalf("access units = %d, want 1", len(got))
	}
	if got[0].PresentationTime != 250*time.Millisecond {
		t.Fatalf("access unit PTS = %s, want %s", got[0].PresentationTime, 250*time.Millisecond)
	}
	if !bytes.Equal(got[0].Payload, wantPayload) {
		t.Fatalf("access unit payload = %x, want %x", got[0].Payload, wantPayload)
	}
}

func TestSessionResolverHTTPServerDoesNotAppendHTTPErrorAfterStreamStarts(t *testing.T) {
	t.Parallel()

	wantPayload := annexBPayload([][]byte{{0x65, 0x88, 0x84}})
	server := NewSessionResolverHTTPServer(SessionResolverHTTPServerOptions{
		Backend: fakeSessionResolverBackend{
			open: func(context.Context, SessionResolverStreamRequest) (SessionResolverStream, error) {
				return &fakeSessionResolverStream{
					units: []AccessUnit{
						{
							Codec:            "h264",
							Payload:          wantPayload,
							PresentationTime: 250 * time.Millisecond,
						},
					},
					err: errors.New("stream backend failed"),
				}, nil
			},
		},
		SessionTTL: time.Minute,
	})
	server.storeSession(sessionResolverSession{
		ID:        "session-1",
		Token:     "token-1",
		CameraID:  "camera-1",
		Model:     "xiaomi.camera.v1",
		Profile:   "mijia.camera.family",
		Codec:     "h264",
		ExpiresAt: time.Now().Add(time.Minute),
	})

	req := httptest.NewRequest(http.MethodGet, "/stream/session-1?token=token-1", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}
	var framed bytes.Buffer
	if err := writeHTTPAnnexBFrame(&framed, AccessUnit{
		Codec:            "h264",
		Payload:          wantPayload,
		PresentationTime: 250 * time.Millisecond,
	}); err != nil {
		t.Fatalf("writeHTTPAnnexBFrame() error = %v", err)
	}
	if got, want := recorder.Body.Bytes(), framed.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("stream response body = %x, want %x", got, want)
	}
}

type fakeSessionResolverBackend struct {
	probe func(context.Context, Target, Profile) (SessionResolverProbe, error)
	open  func(context.Context, SessionResolverStreamRequest) (SessionResolverStream, error)
}

func (f fakeSessionResolverBackend) Probe(ctx context.Context, target Target, profile Profile) (SessionResolverProbe, error) {
	if f.probe == nil {
		return SessionResolverProbe{}, errors.New("probe not implemented")
	}
	return f.probe(ctx, target, profile)
}

func (f fakeSessionResolverBackend) Open(ctx context.Context, req SessionResolverStreamRequest) (SessionResolverStream, error) {
	if f.open == nil {
		return nil, errors.New("open not implemented")
	}
	return f.open(ctx, req)
}

type fakeSessionResolverStream struct {
	units []AccessUnit
	index int
	err   error
}

func (f *fakeSessionResolverStream) Recv(context.Context) (AccessUnit, error) {
	if f.index >= len(f.units) {
		if f.err != nil {
			return AccessUnit{}, f.err
		}
		return AccessUnit{}, io.EOF
	}
	unit := f.units[f.index]
	f.index++
	return unit, nil
}

func (f *fakeSessionResolverStream) Close() error {
	return nil
}
