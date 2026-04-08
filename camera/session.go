package camera

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrSessionResolverUnavailable reports that no usable session resolver exists.
	ErrSessionResolverUnavailable = errors.New("miot camera session resolver unavailable")
	// ErrSessionResolution reports that a session could not be resolved or parsed.
	ErrSessionResolution = errors.New("miot camera session resolution failed")
)

var rawURLPattern = regexp.MustCompile(`(?i)(https?|rtsp)://[^\s"'<>]+`)

// Resolver resolves a stream session for one camera target and profile.
type Resolver interface {
	Name() string
	Resolve(context.Context, Target, Profile) (Session, error)
}

// ResolverChain tries configured resolvers in order until one succeeds.
type ResolverChain struct {
	Resolvers []Resolver
}

// Resolve tries each resolver until a usable session is returned.
func (chain ResolverChain) Resolve(ctx context.Context, target Target, profile Profile) (Session, error) {
	if len(chain.Resolvers) == 0 {
		return Session{}, fmt.Errorf("%w: no resolvers configured", ErrSessionResolverUnavailable)
	}

	var attempts []error
	for _, resolver := range chain.Resolvers {
		if resolver == nil {
			continue
		}
		session, err := resolver.Resolve(ctx, target, profile)
		if err != nil {
			attempts = append(attempts, fmt.Errorf("%s: %w", resolver.Name(), err))
			continue
		}
		if session.CameraID == "" {
			session.CameraID = target.CameraID
		}
		if session.Model == "" {
			session.Model = target.Model
		}
		if session.ProfileName == "" {
			session.ProfileName = profile.Name
		}
		if session.ResolverName == "" {
			session.ResolverName = resolver.Name()
		}
		return session, nil
	}

	if len(attempts) == 0 {
		return Session{}, fmt.Errorf("%w: no usable resolvers configured", ErrSessionResolverUnavailable)
	}
	return Session{}, fmt.Errorf("%w: %w", ErrSessionResolution, errors.Join(attempts...))
}

// HTTPResolverOptions configures one HTTP-backed session resolver.
type HTTPResolverOptions struct {
	Name    string
	Client  *http.Client
	Request func(context.Context, Target, Profile) (*http.Request, error)
	Parse   func([]byte) (Session, error)
}

// HTTPResolver resolves sessions by fetching and parsing one HTTP response.
type HTTPResolver struct {
	name    string
	client  *http.Client
	request func(context.Context, Target, Profile) (*http.Request, error)
	parse   func([]byte) (Session, error)
}

// NewHTTPResolver constructs one HTTP-backed session resolver.
func NewHTTPResolver(options HTTPResolverOptions) *HTTPResolver {
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPResolver{
		name:    strings.TrimSpace(options.Name),
		client:  client,
		request: options.Request,
		parse:   options.Parse,
	}
}

// Name returns the configured resolver name or a stable default.
func (resolver *HTTPResolver) Name() string {
	if resolver == nil {
		return ""
	}
	if resolver.name == "" {
		return "http"
	}
	return resolver.name
}

// Resolve fetches and parses one session payload.
func (resolver *HTTPResolver) Resolve(ctx context.Context, target Target, profile Profile) (Session, error) {
	if resolver == nil {
		return Session{}, fmt.Errorf("%w: resolver unavailable", ErrSessionResolverUnavailable)
	}
	if resolver.request == nil || resolver.parse == nil {
		return Session{}, fmt.Errorf("%w: resolver %s not configured", ErrSessionResolverUnavailable, resolver.Name())
	}

	req, err := resolver.request(ctx, target, profile)
	if err != nil {
		return Session{}, fmt.Errorf("%s: %w", resolver.Name(), err)
	}

	res, err := resolver.client.Do(req)
	if err != nil {
		return Session{}, fmt.Errorf("%s: %w", resolver.Name(), err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return Session{}, fmt.Errorf("%s: %w", resolver.Name(), err)
	}

	session, err := resolver.parse(payload)
	if err != nil {
		return Session{}, fmt.Errorf("%s: %w", resolver.Name(), err)
	}
	if session.ResolverName == "" {
		session.ResolverName = resolver.Name()
	}
	return session, nil
}

// ParseSessionPayload parses one Xiaomi camera session payload.
func ParseSessionPayload(payload []byte) (Session, error) {
	var envelope struct {
		Code      int             `json:"code"`
		Message   string          `json:"message"`
		Session   string          `json:"session_id"`
		Transport string          `json:"transport"`
		StreamURL string          `json:"stream_url"`
		Codec     string          `json:"codec"`
		Token     string          `json:"token"`
		Data      json.RawMessage `json:"data"`
		Result    json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return Session{}, fmt.Errorf("%w: invalid Xiaomi session payload: %w", ErrSessionResolution, err)
	}
	if envelope.Code != 0 && envelope.Code != http.StatusOK {
		return Session{}, fmt.Errorf("%w: raw Xiaomi session payload: %s", ErrSessionResolution, redactPayload(payload))
	}

	session := Session{}
	mergeSessionFields(&session, envelope.Result)
	mergeSessionFields(&session, envelope.Data)
	mergeSessionFieldsFromEnvelope(&session, envelope)

	if strings.TrimSpace(session.SessionID) == "" {
		return Session{}, fmt.Errorf("%w: missing session_id in Xiaomi session payload", ErrSessionResolution)
	}
	if strings.TrimSpace(session.Transport) == "" {
		return Session{}, fmt.Errorf("%w: missing transport in Xiaomi session payload", ErrSessionResolution)
	}
	if strings.TrimSpace(session.StreamURL) == "" {
		return Session{}, fmt.Errorf("%w: missing stream_url in Xiaomi session payload", ErrSessionResolution)
	}
	if strings.TrimSpace(session.Codec) == "" {
		return Session{}, fmt.Errorf("%w: missing codec in Xiaomi session payload", ErrSessionResolution)
	}
	return session, nil
}

func sanitizeStreamURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return sanitizeURL(parsed).String()
	}
	return rawURLPattern.ReplaceAllStringFunc(raw, func(match string) string {
		parsed, err := url.Parse(match)
		if err != nil {
			return "redacted-url"
		}
		return sanitizeURL(parsed).String()
	})
}

func redactPayload(payload []byte) string {
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err == nil {
		if encoded, err := json.Marshal(sanitizeValue(decoded)); err == nil {
			return string(encoded)
		}
	}
	return redactText(string(payload))
}

func redactText(raw string) string {
	raw = rawURLPattern.ReplaceAllStringFunc(raw, func(match string) string {
		parsed, err := url.Parse(match)
		if err != nil {
			return "redacted-url"
		}
		return sanitizeURL(parsed).String()
	})
	for _, key := range []string{"token", "request_id", "requestId", "access_token"} {
		raw = redactJSONKey(raw, key)
	}
	return raw
}

func redactJSONKey(raw string, key string) string {
	pattern := regexp.MustCompile(`(?i)("` + regexp.QuoteMeta(key) + `"\s*:\s*")([^"]*)(")`)
	return pattern.ReplaceAllString(raw, `${1}redacted${3}`)
}

func sanitizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		sanitized := make(map[string]any, len(typed))
		for key, child := range typed {
			if isSensitiveKey(key) {
				sanitized[key] = "redacted"
				continue
			}
			sanitized[key] = sanitizeValue(child)
		}
		return sanitized
	case []any:
		sanitized := make([]any, len(typed))
		for i, child := range typed {
			sanitized[i] = sanitizeValue(child)
		}
		return sanitized
	case string:
		return sanitizeStreamURL(typed)
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "token", "request_id", "requestid", "access_token":
		return true
	default:
		return false
	}
}

func sanitizeURL(parsed *url.URL) *url.URL {
	copy := *parsed
	copy.User = nil
	copy.Host = "redacted.invalid"
	if copy.RawQuery != "" {
		query := copy.Query()
		for key := range query {
			query.Set(key, "redacted")
		}
		copy.RawQuery = query.Encode()
	}
	return &copy
}

func mergeSessionFields(session *Session, raw json.RawMessage) {
	if session == nil || len(raw) == 0 {
		return
	}
	var fields struct {
		SessionID string `json:"session_id"`
		Transport string `json:"transport"`
		StreamURL string `json:"stream_url"`
		Codec     string `json:"codec"`
		Token     string `json:"token"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return
	}
	if session.SessionID == "" {
		session.SessionID = strings.TrimSpace(fields.SessionID)
	}
	if session.Transport == "" {
		session.Transport = strings.TrimSpace(fields.Transport)
	}
	if session.StreamURL == "" {
		session.StreamURL = strings.TrimSpace(fields.StreamURL)
	}
	if session.Codec == "" {
		session.Codec = strings.TrimSpace(fields.Codec)
	}
	if session.Token == "" {
		session.Token = strings.TrimSpace(fields.Token)
	}
}

func mergeSessionFieldsFromEnvelope(session *Session, envelope struct {
	Code      int             `json:"code"`
	Message   string          `json:"message"`
	Session   string          `json:"session_id"`
	Transport string          `json:"transport"`
	StreamURL string          `json:"stream_url"`
	Codec     string          `json:"codec"`
	Token     string          `json:"token"`
	Data      json.RawMessage `json:"data"`
	Result    json.RawMessage `json:"result"`
}) {
	if session == nil {
		return
	}
	if session.SessionID == "" {
		session.SessionID = strings.TrimSpace(envelope.Session)
	}
	if session.Transport == "" {
		session.Transport = strings.TrimSpace(envelope.Transport)
	}
	if session.StreamURL == "" {
		session.StreamURL = strings.TrimSpace(envelope.StreamURL)
	}
	if session.Codec == "" {
		session.Codec = strings.TrimSpace(envelope.Codec)
	}
	if session.Token == "" {
		session.Token = strings.TrimSpace(envelope.Token)
	}
}
