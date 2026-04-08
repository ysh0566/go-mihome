package camera

import "context"

// Compatibility aliases for the external Xiaomi camera probe/session API.
type CameraProbeTarget = Target
type CameraProbeProfile = Profile
type CameraProbeSession = Session
type CameraProbeAccessUnit = AccessUnit
type CameraProbeJPEGFrame = JPEGFrame

type CameraProbeLoader = Loader
type CameraProbeCatalog = Catalog
type CameraProbeCataloger interface {
	List(context.Context) ([]Target, error)
	SelectCamera(context.Context, string) (Target, error)
}

type CameraSessionResolver = Resolver
type CameraSessionResolverChain = ResolverChain
type CameraProbeHTTPResolverOptions = HTTPResolverOptions
type CameraProbeHTTPResolver = HTTPResolver
type CameraProbeDirectResolverOptions = DirectResolverOptions
type CameraProbeDirectResolver = DirectResolver

type CameraSessionResolverProbe = SessionResolverProbe
type CameraSessionResolverStreamRequest = SessionResolverStreamRequest
type CameraSessionResolverStream = SessionResolverStream
type CameraSessionResolverBackend = SessionResolverBackend
type CameraSessionResolverHTTPServerOptions = SessionResolverHTTPServerOptions
type CameraSessionResolverHTTPServer = SessionResolverHTTPServer

type CameraProbeStreamSession = ProbeStreamSession
type CameraProbeStreamer = ProbeStreamer
type CameraProbeStreamClientOptions = ProbeStreamClientOptions
type CameraProbeStreamClient = ProbeStreamClient

type CameraProbeDecoderConfig = ProbeDecoderConfig
type CameraProbeDecoderSession = ProbeDecoderSession
type CameraProbeFrameDecoder = ProbeFrameDecoder
type FFmpegCameraFrameDecoder = FFmpegFrameDecoder

type CameraProbeFrameStore = FrameStore
type CameraProbeHTTPServerOptions = HTTPServerOptions
type CameraProbeHTTPServer = HTTPServer

type CameraProbeDriverFactoryOptions = ProbeDriverFactoryOptions

// NewCameraProbeCatalog is the compatibility wrapper for NewCatalog.
func NewCameraProbeCatalog(loader Loader) Catalog {
	return NewCatalog(loader)
}

// SelectCamera is the compatibility wrapper for Select.
func (catalog Catalog) SelectCamera(ctx context.Context, cameraID string) (Target, error) {
	return catalog.Select(ctx, cameraID)
}

// NewCameraProbeHTTPResolver is the compatibility wrapper for NewHTTPResolver.
func NewCameraProbeHTTPResolver(options HTTPResolverOptions) *HTTPResolver {
	return NewHTTPResolver(options)
}

// NewCameraProbeDirectResolver is the compatibility wrapper for NewDirectResolver.
func NewCameraProbeDirectResolver(options DirectResolverOptions) *DirectResolver {
	return NewDirectResolver(options)
}

// NewCameraSessionResolverHTTPServer is the compatibility wrapper for NewSessionResolverHTTPServer.
func NewCameraSessionResolverHTTPServer(options SessionResolverHTTPServerOptions) *SessionResolverHTTPServer {
	return NewSessionResolverHTTPServer(options)
}

// NewCameraProbeStreamClient is the compatibility wrapper for NewProbeStreamClient.
func NewCameraProbeStreamClient(options ProbeStreamClientOptions) *ProbeStreamClient {
	return NewProbeStreamClient(options)
}

// NewFFmpegCameraFrameDecoder is the compatibility wrapper for NewFFmpegFrameDecoder.
func NewFFmpegCameraFrameDecoder(ffmpegPath string) *FFmpegFrameDecoder {
	return NewFFmpegFrameDecoder(ffmpegPath)
}

// NewCameraProbeFrameStore is the compatibility wrapper for NewFrameStore.
func NewCameraProbeFrameStore() *FrameStore {
	return NewFrameStore()
}

// NewCameraProbeHTTPServer is the compatibility wrapper for NewHTTPServer.
func NewCameraProbeHTTPServer(options HTTPServerOptions) *HTTPServer {
	return NewHTTPServer(options)
}

// NewCameraProbeDriverFactory is the compatibility wrapper for NewProbeDriverFactory.
func NewCameraProbeDriverFactory(options ProbeDriverFactoryOptions) DriverFactory {
	return NewProbeDriverFactory(options)
}

// MatchCameraProbeProfile is the compatibility wrapper for MatchProfile.
func MatchCameraProbeProfile(model string) Profile {
	return MatchProfile(model)
}
