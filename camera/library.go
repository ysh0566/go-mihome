package camera

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	miot "github.com/ysh0566/go-mihome"
)

const libraryVersion = "camera-go/0.1.0"

// LibraryOptions configures the high-level camera runtime facade.
type LibraryOptions struct {
	CloudServer string
	ClientID    string
	AccessToken string
	HTTPClient  miot.HTTPDoer
	Logger      any

	DriverFactory DriverFactory
}

// Library manages camera instances and the shared runtime backing them.
type Library struct {
	mu      sync.Mutex
	runtime *Runtime
	tokens  *libraryMutableTokenProvider
	cameras map[string]*Instance
}

type libraryMutableTokenProvider struct {
	mu    sync.RWMutex
	token string
}

func (p *libraryMutableTokenProvider) AccessToken(context.Context) (string, error) {
	if p == nil {
		return "", nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.token, nil
}

func (p *libraryMutableTokenProvider) Update(token string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.token = strings.TrimSpace(token)
	p.mu.Unlock()
}

func (p *libraryMutableTokenProvider) accessToken() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.token
}

type libraryNoopFrameDecoder struct{}

func (libraryNoopFrameDecoder) Start(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error) {
	return libraryNoopDecoderSession{}, nil
}

type libraryNoopDecoderSession struct{}

func (libraryNoopDecoderSession) Decode(AccessUnit) error { return nil }
func (libraryNoopDecoderSession) Close() error            { return nil }
func (libraryNoopDecoderSession) Err() <-chan error {
	ch := make(chan error)
	close(ch)
	return ch
}

type fallbackSessionResolverBackend struct {
	mu        sync.RWMutex
	backends  []SessionResolverBackend
	preferred map[string]int
}

// NewLibrary constructs a high-level camera runtime facade.
func NewLibrary(options LibraryOptions) (*Library, error) {
	tokens := &libraryMutableTokenProvider{token: strings.TrimSpace(options.AccessToken)}
	factory := options.DriverFactory
	if factory == nil {
		var err error
		factory, err = newDefaultLibraryDriverFactory(options, tokens)
		if err != nil {
			return nil, err
		}
	}
	return &Library{
		runtime: NewRuntime(RuntimeOptions{
			AccessToken: tokens.accessToken(),
			Factory:     factory,
		}),
		tokens:  tokens,
		cameras: map[string]*Instance{},
	}, nil
}

func newDefaultLibraryDriverFactory(options LibraryOptions, tokens *libraryMutableTokenProvider) (DriverFactory, error) {
	clientID := strings.TrimSpace(options.ClientID)
	if clientID == "" {
		return nil, fmt.Errorf("camera: client id is required when DriverFactory is nil")
	}

	cloudClient, err := NewMicoAPITokenCameraCloudClient(MicoAPITokenCameraCloudClientOptions{
		CloudConfig: miot.CloudConfig{
			ClientID:    clientID,
			CloudServer: firstNonEmpty(options.CloudServer, "cn"),
		},
		Tokens:     tokens,
		HTTPClient: options.HTTPClient,
	})
	if err != nil {
		return nil, err
	}

	tokenBackend := NewTokenCameraSessionResolverBackend(TokenCameraSessionResolverBackendOptions{
		Cloud:  cloudClient,
		Logger: options.Logger,
	})
	backend := newFallbackSessionResolverBackend(
		NewMiHomeCameraSessionResolverBackend(options.Logger),
		tokenBackend,
	)
	return NewProbeDriverFactory(ProbeDriverFactoryOptions{
		Resolvers: []Resolver{
			NewDirectResolver(DirectResolverOptions{
				Name:    "camera-library-direct",
				Backend: backend,
			}),
		},
		Streamer: NewProbeStreamClient(ProbeStreamClientOptions{
			Backend: backend,
		}),
		Decoder: libraryNoopFrameDecoder{},
	}), nil
}

func newFallbackSessionResolverBackend(backends ...SessionResolverBackend) SessionResolverBackend {
	filtered := make([]SessionResolverBackend, 0, len(backends))
	for _, backend := range backends {
		if backend != nil {
			filtered = append(filtered, backend)
		}
	}
	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return &fallbackSessionResolverBackend{
			backends:  filtered,
			preferred: map[string]int{},
		}
	}
}

func (b *fallbackSessionResolverBackend) Probe(ctx context.Context, target Target, profile Profile) (SessionResolverProbe, error) {
	if b == nil || len(b.backends) == 0 {
		return SessionResolverProbe{}, fmt.Errorf("%w: fallback session resolver backend unavailable", ErrRuntimeUnavailable)
	}

	key := fallbackBackendKey(target.CameraID, target.Model)
	order := b.backendOrder(key)
	var err error
	for _, idx := range order {
		probe, probeErr := b.backends[idx].Probe(ctx, target, profile)
		if probeErr == nil {
			b.setPreferred(key, idx)
			return probe, nil
		}
		err = errors.Join(err, probeErr)
	}
	return SessionResolverProbe{}, err
}

func (b *fallbackSessionResolverBackend) Open(ctx context.Context, req SessionResolverStreamRequest) (SessionResolverStream, error) {
	if b == nil || len(b.backends) == 0 {
		return nil, fmt.Errorf("%w: fallback session resolver backend unavailable", ErrRuntimeUnavailable)
	}

	key := fallbackBackendKey(req.CameraID, req.Model)
	order := b.backendOrder(key)
	var err error
	for _, idx := range order {
		stream, openErr := b.backends[idx].Open(ctx, req)
		if openErr == nil {
			b.setPreferred(key, idx)
			return stream, nil
		}
		err = errors.Join(err, openErr)
	}
	return nil, err
}

func (b *fallbackSessionResolverBackend) backendOrder(key string) []int {
	order := make([]int, 0, len(b.backends))
	preferred, ok := b.preferredIndex(key)
	if ok {
		order = append(order, preferred)
	}
	for idx := range b.backends {
		if ok && idx == preferred {
			continue
		}
		order = append(order, idx)
	}
	return order
}

func (b *fallbackSessionResolverBackend) preferredIndex(key string) (int, bool) {
	if b == nil || key == "" {
		return 0, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	idx, ok := b.preferred[key]
	return idx, ok
}

func (b *fallbackSessionResolverBackend) setPreferred(key string, idx int) {
	if b == nil || key == "" {
		return
	}
	b.mu.Lock()
	b.preferred[key] = idx
	b.mu.Unlock()
}

func fallbackBackendKey(cameraID string, model string) string {
	cameraID = strings.TrimSpace(cameraID)
	model = strings.TrimSpace(model)
	if cameraID == "" && model == "" {
		return ""
	}
	return cameraID + "\x00" + model
}

// Version returns the high-level facade version string.
func (l *Library) Version() string {
	return libraryVersion
}

// UpdateAccessToken updates the shared access token used by new camera instances.
func (l *Library) UpdateAccessToken(accessToken string) error {
	if l == nil {
		return fmt.Errorf("camera: library is nil")
	}
	l.tokens.Update(accessToken)
	if l.runtime != nil {
		l.runtime.UpdateAccessToken(strings.TrimSpace(accessToken))
	}
	return nil
}

// NewCamera creates or reuses a managed camera instance.
func (l *Library) NewCamera(info Info) (*Instance, error) {
	if l == nil || l.runtime == nil {
		return nil, fmt.Errorf("camera: library is unavailable")
	}

	info = normalizeInfo(info)
	if info.CameraID == "" || info.Model == "" {
		return nil, fmt.Errorf("camera: camera id and model are required")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if current, ok := l.cameras[info.CameraID]; ok {
		return current, nil
	}

	info.Token = l.tokens.accessToken()
	instance, err := l.runtime.Create(info)
	if err != nil {
		return nil, err
	}

	l.cameras[info.CameraID] = instance
	return instance, nil
}

// Close stops and destroys all managed cameras.
func (l *Library) Close() error {
	if l == nil || l.runtime == nil {
		return nil
	}

	l.mu.Lock()
	cameraIDs := make([]string, 0, len(l.cameras))
	for cameraID := range l.cameras {
		cameraIDs = append(cameraIDs, cameraID)
	}
	l.cameras = map[string]*Instance{}
	l.mu.Unlock()

	var closeErr error
	for _, cameraID := range cameraIDs {
		closeErr = errors.Join(closeErr, l.runtime.Destroy(cameraID))
	}
	return closeErr
}
