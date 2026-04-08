package camera

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	transportHTTPAnnexB   = "http-annexb"
	transportDirectAnnexB = "direct-annexb"
	defaultSessionTTL     = time.Minute
)

// SessionResolverProbe describes one backend probe result before a stream is opened.
type SessionResolverProbe struct {
	CameraID string
	Model    string
	Codec    string
}

// SessionResolverStreamRequest describes one backend stream-open request.
type SessionResolverStreamRequest struct {
	CameraID     string
	Model        string
	Profile      string
	Codec        string
	VideoQuality VideoQuality
}

// SessionResolverStream streams access units from one backend stream.
type SessionResolverStream interface {
	Recv(context.Context) (AccessUnit, error)
	Close() error
}

// SessionResolverBackend provides probe and open operations for non-RTSP session backends.
type SessionResolverBackend interface {
	Probe(context.Context, Target, Profile) (SessionResolverProbe, error)
	Open(context.Context, SessionResolverStreamRequest) (SessionResolverStream, error)
}

// DirectResolverOptions configures a direct backend-backed session resolver.
type DirectResolverOptions struct {
	Name      string
	Backend   SessionResolverBackend
	SkipProbe bool
}

// DirectResolver resolves sessions through a local backend instead of an HTTP endpoint.
type DirectResolver struct {
	name      string
	backend   SessionResolverBackend
	skipProbe bool
}

// SessionResolverHTTPServerOptions configures the camera session resolver HTTP server.
type SessionResolverHTTPServerOptions struct {
	Backend    SessionResolverBackend
	SessionTTL time.Duration
}

// SessionResolverHTTPServer exposes resolver and stream endpoints for backend-backed camera sessions.
type SessionResolverHTTPServer struct {
	backend    SessionResolverBackend
	sessionTTL time.Duration
	now        func() time.Time

	mu       sync.Mutex
	sessions map[string]sessionResolverSession
	mux      *http.ServeMux
}

type sessionResolverSession struct {
	ID        string
	Token     string
	CameraID  string
	Model     string
	Profile   string
	Codec     string
	ExpiresAt time.Time
}

// NewDirectResolver constructs a direct backend-backed session resolver.
func NewDirectResolver(options DirectResolverOptions) *DirectResolver {
	return &DirectResolver{
		name:      strings.TrimSpace(options.Name),
		backend:   options.Backend,
		skipProbe: options.SkipProbe,
	}
}

// Name returns the configured resolver name or a stable default.
func (resolver *DirectResolver) Name() string {
	if resolver == nil {
		return ""
	}
	if resolver.name == "" {
		return "direct"
	}
	return resolver.name
}

// Resolve probes the backend and returns a direct Annex-B session descriptor.
func (resolver *DirectResolver) Resolve(ctx context.Context, target Target, profile Profile) (Session, error) {
	if resolver == nil || resolver.backend == nil {
		return Session{}, fmt.Errorf("%w: resolver unavailable", ErrSessionResolverUnavailable)
	}

	if resolver.skipProbe {
		cameraID := strings.TrimSpace(target.CameraID)
		model := strings.TrimSpace(target.Model)
		if cameraID == "" {
			return Session{}, fmt.Errorf("%w: camera id is empty", ErrSessionResolution)
		}
		return Session{
			CameraID:     cameraID,
			Model:        model,
			ProfileName:  strings.TrimSpace(profile.Name),
			ResolverName: resolver.Name(),
			Transport:    transportDirectAnnexB,
			SessionID:    cameraID,
		}, nil
	}

	probe, err := resolver.backend.Probe(ctx, target, profile)
	if err != nil {
		return Session{}, err
	}

	cameraID := firstNonEmptyString(strings.TrimSpace(probe.CameraID), strings.TrimSpace(target.CameraID))
	model := firstNonEmptyString(strings.TrimSpace(probe.Model), strings.TrimSpace(target.Model))
	codec := normalizeCodec(probe.Codec)
	if cameraID == "" {
		return Session{}, fmt.Errorf("%w: camera id is empty", ErrSessionResolution)
	}
	if codec == "" {
		return Session{}, fmt.Errorf("%w: codec is empty", ErrSessionResolution)
	}

	return Session{
		CameraID:     cameraID,
		Model:        model,
		ProfileName:  strings.TrimSpace(profile.Name),
		ResolverName: resolver.Name(),
		Transport:    transportDirectAnnexB,
		SessionID:    cameraID,
		Codec:        codec,
	}, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

// NewSessionResolverHTTPServer constructs a new backend-backed session resolver HTTP server.
func NewSessionResolverHTTPServer(options SessionResolverHTTPServerOptions) *SessionResolverHTTPServer {
	ttl := options.SessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}

	server := &SessionResolverHTTPServer{
		backend:    options.Backend,
		sessionTTL: ttl,
		now:        time.Now,
		sessions:   map[string]sessionResolverSession{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/resolve", server.handleResolve)
	mux.HandleFunc("/stream/", server.handleStream)
	mux.HandleFunc("/", server.handleRoot)
	server.mux = mux
	return server
}

// Handler returns the configured HTTP handler.
func (s *SessionResolverHTTPServer) Handler() http.Handler {
	if s == nil || s.mux == nil {
		return http.NotFoundHandler()
	}
	return s.mux
}

func (s *SessionResolverHTTPServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.handleResolve(w, r)
}

func (s *SessionResolverHTTPServer) handleResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if s == nil || s.backend == nil {
		http.Error(w, "camera session resolver backend unavailable", http.StatusServiceUnavailable)
		return
	}

	var request struct {
		CameraID string `json:"camera_id"`
		Model    string `json:"model"`
		Profile  string `json:"profile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
		return
	}
	request.CameraID = strings.TrimSpace(request.CameraID)
	request.Model = strings.TrimSpace(request.Model)
	request.Profile = strings.TrimSpace(request.Profile)
	if request.CameraID == "" {
		http.Error(w, "camera_id is required", http.StatusBadRequest)
		return
	}

	profile := Profile{Name: request.Profile}
	probe, err := s.backend.Probe(r.Context(), Target{
		CameraID: request.CameraID,
		Model:    request.Model,
	}, profile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	session := sessionResolverSession{
		ID:        randomSessionSecret(12),
		Token:     randomSessionSecret(12),
		CameraID:  firstNonEmptyString(strings.TrimSpace(probe.CameraID), request.CameraID),
		Model:     firstNonEmptyString(strings.TrimSpace(probe.Model), request.Model),
		Profile:   request.Profile,
		Codec:     normalizeCodec(probe.Codec),
		ExpiresAt: s.now().Add(s.sessionTTL),
	}
	if session.Codec == "" {
		http.Error(w, "camera session codec unavailable", http.StatusBadGateway)
		return
	}
	s.storeSession(session)

	response := map[string]any{
		"code":    0,
		"message": "ok",
		"result": map[string]any{
			"camera_id":  session.CameraID,
			"model":      session.Model,
			"profile":    session.Profile,
			"session_id": session.ID,
			"transport":  transportHTTPAnnexB,
			"stream_url": s.streamURL(r, session),
			"codec":      session.Codec,
			"token":      session.Token,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (s *SessionResolverHTTPServer) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if s == nil || s.backend == nil {
		http.Error(w, "camera session resolver backend unavailable", http.StatusServiceUnavailable)
		return
	}

	sessionID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/stream/"))
	if sessionID == "" {
		http.NotFound(w, r)
		return
	}
	session, ok := s.takeSession(sessionID, strings.TrimSpace(r.URL.Query().Get("token")))
	if !ok {
		http.Error(w, "session not found", http.StatusUnauthorized)
		return
	}

	stream, err := s.backend.Open(r.Context(), SessionResolverStreamRequest{
		CameraID: session.CameraID,
		Model:    session.Model,
		Profile:  session.Profile,
		Codec:    session.Codec,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = stream.Close() }()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)
	wroteFrame := false

	for {
		unit, recvErr := stream.Recv(r.Context())
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) || r.Context().Err() != nil {
				return
			}
			if wroteFrame {
				return
			}
			http.Error(w, recvErr.Error(), http.StatusBadGateway)
			return
		}
		if err := writeHTTPAnnexBFrame(w, unit); err != nil {
			return
		}
		wroteFrame = true
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (s *SessionResolverHTTPServer) storeSession(session sessionResolverSession) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneSessionsLocked()
	s.sessions[session.ID] = session
}

func (s *SessionResolverHTTPServer) takeSession(sessionID string, token string) (sessionResolverSession, bool) {
	if s == nil {
		return sessionResolverSession{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneSessionsLocked()

	session, ok := s.sessions[sessionID]
	if !ok || session.Token != token {
		return sessionResolverSession{}, false
	}
	delete(s.sessions, sessionID)
	return session, true
}

func (s *SessionResolverHTTPServer) pruneSessionsLocked() {
	if s == nil {
		return
	}
	now := s.now()
	for id, session := range s.sessions {
		if !session.ExpiresAt.IsZero() && !session.ExpiresAt.After(now) {
			delete(s.sessions, id)
		}
	}
}

func (s *SessionResolverHTTPServer) streamURL(r *http.Request, session sessionResolverSession) string {
	base := sessionResolverBaseURL(r)
	if base == "" {
		return ""
	}
	streamPath := path.Join("/", "stream", session.ID)
	return fmt.Sprintf("%s%s?token=%s", base, streamPath, session.Token)
}

func sessionResolverBaseURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

func randomSessionSecret(size int) string {
	if size <= 0 {
		size = 12
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func writeHTTPAnnexBFrame(w io.Writer, unit AccessUnit) error {
	if w == nil {
		return fmt.Errorf("camera HTTP Annex-B frame writer is unavailable")
	}
	header := make([]byte, 12)
	binary.BigEndian.PutUint64(header[0:8], uint64(unit.PresentationTime/time.Millisecond))
	binary.BigEndian.PutUint32(header[8:12], uint32(len(unit.Payload)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(unit.Payload) == 0 {
		return nil
	}
	_, err := w.Write(unit.Payload)
	return err
}

func readHTTPAnnexBFrame(r io.Reader, codec string) (AccessUnit, error) {
	header := make([]byte, 12)
	if _, err := io.ReadFull(r, header); err != nil {
		return AccessUnit{}, err
	}
	payloadSize := binary.BigEndian.Uint32(header[8:12])
	payload := make([]byte, payloadSize)
	if _, err := io.ReadFull(r, payload); err != nil {
		return AccessUnit{}, err
	}
	return AccessUnit{
		Codec:            normalizeCodec(codec),
		Payload:          payload,
		PresentationTime: time.Duration(binary.BigEndian.Uint64(header[0:8])) * time.Millisecond,
	}, nil
}
