package camera

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

type probeCameraPipelineFactory struct {
	resolvers    []Resolver
	streamClient ProbeStreamer
	decoder      ProbeFrameDecoder
	fallback     cameraPipelineFactory
}

type probeCameraPipeline struct {
	cancel         context.CancelFunc
	streamSession  ProbeStreamSession
	decoderSession ProbeDecoderSession
	closeOnce      sync.Once
}

func newDefaultCameraPipelineFactory(ffmpegPath string, logger any) cameraPipelineFactory {
	backend := newRuntimeCameraSessionResolverBackend(logger)
	probeFactory := &probeCameraPipelineFactory{
		resolvers:    defaultRuntimeCameraSessionResolversWithBackend(backend),
		streamClient: defaultRuntimeCameraProbeStreamClient(backend),
		decoder:      NewFFmpegFrameDecoder(ffmpegPath),
	}
	if backend != nil {
		probeFactory.fallback = newMiHomeCameraPipelineFactory(ffmpegPath, logger, nil)
	}
	return probeFactory
}

func (f *probeCameraPipelineFactory) Available() bool {
	return f != nil && len(f.resolvers) > 0 && f.streamClient != nil && f.decoder != nil
}

func (f *probeCameraPipelineFactory) Start(desc CameraStreamDescriptor, onJPEG func(JPEGFrame)) (io.Closer, error) {
	if len(f.resolvers) == 0 || f.streamClient == nil || f.decoder == nil {
		return f.tryFallback(desc, onJPEG, ErrCameraBridgeUnavailable)
	}

	ctx, cancel := context.WithCancel(context.Background())
	target := Target{
		CameraID: strings.TrimSpace(desc.CameraID),
		Model:    strings.TrimSpace(desc.Model),
		Online:   true,
	}
	profile := MatchProfile(target.Model)
	session, err := ResolverChain{Resolvers: f.resolvers}.Resolve(ctx, target, profile)
	if err != nil {
		cancel()
		return f.tryFallback(desc, onJPEG, fmt.Errorf("%w: %w", ErrCameraBridgeUnavailable, err))
	}

	pipeline := &probeCameraPipeline{
		cancel: cancel,
	}
	decoderSession, err := f.decoder.Start(ctx, ProbeDecoderConfig{Codec: session.Codec}, func(frame JPEGFrame) {
		if len(frame.Payload) == 0 || onJPEG == nil {
			return
		}
		onJPEG(cloneJPEGFrame(frame))
	})
	if err != nil {
		cancel()
		return f.tryFallback(desc, onJPEG, fmt.Errorf("%w: %w", ErrCameraBridgeUnavailable, err))
	}

	streamSession, err := f.streamClient.Start(ctx, session, func(unit AccessUnit) {
		_ = decoderSession.Decode(unit)
	})
	if err != nil {
		cancel()
		_ = decoderSession.Close()
		return f.tryFallback(desc, onJPEG, fmt.Errorf("%w: %w", ErrCameraBridgeUnavailable, err))
	}

	pipeline.decoderSession = decoderSession
	pipeline.streamSession = streamSession
	return pipeline, nil
}

func (f *probeCameraPipelineFactory) tryFallback(desc CameraStreamDescriptor, onJPEG func(JPEGFrame), cause error) (io.Closer, error) {
	if f != nil && f.fallback != nil {
		if availability, ok := f.fallback.(interface{ Available() bool }); ok && !availability.Available() {
			return nil, cause
		}
		return f.fallback.Start(desc, onJPEG)
	}
	return nil, cause
}

func (p *probeCameraPipeline) Close() error {
	if p == nil {
		return nil
	}
	var closeErr error
	p.closeOnce.Do(func() {
		if p.cancel != nil {
			p.cancel()
		}
		if p.streamSession != nil {
			closeErr = errors.Join(closeErr, p.streamSession.Close())
		}
		if p.decoderSession != nil {
			closeErr = errors.Join(closeErr, p.decoderSession.Close())
		}
	})
	return closeErr
}
