package camera

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// ProbeStreamSession is one active probe streaming session.
type ProbeStreamSession interface {
	Close() error
	Err() <-chan error
}

// ProbeStreamer streams access units from a resolved camera session.
type ProbeStreamer interface {
	Start(context.Context, Session, func(AccessUnit)) (ProbeStreamSession, error)
}

// ProbeDecoderConfig describes one probe decoder startup request.
type ProbeDecoderConfig struct {
	Codec string
}

// ProbeDecoderSession is one active probe decoder session.
type ProbeDecoderSession interface {
	Decode(AccessUnit) error
	Close() error
	Err() <-chan error
}

// ProbeFrameDecoder decodes probe access units into JPEG frames.
type ProbeFrameDecoder interface {
	Start(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error)
}

// ProbeDriverFactoryOptions configures the probe driver factory bridge.
type ProbeDriverFactoryOptions struct {
	Resolvers []Resolver
	Streamer  ProbeStreamer
	Decoder   ProbeFrameDecoder
	Logger    any
}

type probeDriverFactory struct {
	resolvers []Resolver
	streamer  ProbeStreamer
	decoder   ProbeFrameDecoder
	logger    any
}

type probeDriver struct {
	info      Info
	resolvers []Resolver
	streamer  ProbeStreamer
	decoder   ProbeFrameDecoder
	logger    any

	mu             sync.Mutex
	cancel         context.CancelFunc
	streamSession  ProbeStreamSession
	decoderSession ProbeDecoderSession
	sink           EventSink
	sequence       atomic.Uint32
}

// NewProbeDriverFactory bridges resolver, streamer, and decoder abstractions into a runtime driver factory.
func NewProbeDriverFactory(options ProbeDriverFactoryOptions) DriverFactory {
	streamer := options.Streamer
	if streamer == nil {
		streamer = NewProbeStreamClient(ProbeStreamClientOptions{})
	}
	return &probeDriverFactory{
		resolvers: append([]Resolver(nil), options.Resolvers...),
		streamer:  streamer,
		decoder:   options.Decoder,
		logger:    options.Logger,
	}
}

func (f *probeDriverFactory) New(info Info) Driver {
	return &probeDriver{
		info:      info,
		resolvers: append([]Resolver(nil), f.resolvers...),
		streamer:  f.streamer,
		decoder:   f.decoder,
		logger:    f.logger,
	}
}

func (d *probeDriver) Start(ctx context.Context, options StartOptions, sink EventSink) error {
	if d == nil || sink == nil || d.streamer == nil || d.decoder == nil || len(d.resolvers) == 0 {
		return fmt.Errorf("%w: probe driver is not fully configured", ErrRuntimeUnavailable)
	}

	target := Target{
		CameraID: strings.TrimSpace(d.info.CameraID),
		Model:    strings.TrimSpace(d.info.Model),
		Online:   true,
	}
	profile := MatchProfile(target.Model)
	session, err := ResolverChain{Resolvers: d.resolvers}.Resolve(ctx, target, profile)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRuntimeUnavailable, err)
	}
	session.VideoQuality = requestedProbeVideoQuality(options.VideoQualities)

	runCtx, cancel := context.WithCancel(ctx)
	decoderSession, err := d.decoder.Start(runCtx, ProbeDecoderConfig{Codec: session.Codec}, func(frame JPEGFrame) {
		sink.EmitJPEG(cloneJPEGFrame(frame))
	})
	if err != nil {
		cancel()
		return fmt.Errorf("%w: %w", ErrRuntimeUnavailable, err)
	}

	streamSession, err := d.streamer.Start(runCtx, session, func(unit AccessUnit) {
		sequence := d.sequence.Add(1)
		sink.EmitRawVideo(Frame{
			Codec:     codecFromProbeCodec(unit.Codec),
			Length:    uint32(len(unit.Payload)),
			Timestamp: uint64(unit.PresentationTime),
			Sequence:  sequence,
			FrameType: frameTypeFromAccessUnit(unit.Payload, unit.Codec),
			Channel:   0,
			Data:      append([]byte(nil), unit.Payload...),
		})
		if err := decoderSession.Decode(unit); err != nil {
			logPrintf(d.logger, "camera_id=%s stage=decoder_write_error error=%q", strings.TrimSpace(d.info.CameraID), err)
			sink.UpdateStatus(StatusError)
		}
	})
	if err != nil {
		cancel()
		_ = decoderSession.Close()
		return fmt.Errorf("%w: %w", ErrRuntimeUnavailable, err)
	}

	d.mu.Lock()
	d.cancel = cancel
	d.streamSession = streamSession
	d.decoderSession = decoderSession
	d.sink = sink
	d.mu.Unlock()

	d.watchErrors("stream_runtime_error", streamSession.Err())
	d.watchErrors("decoder_runtime_error", decoderSession.Err())
	sink.UpdateStatus(StatusConnected)
	return nil
}

func (d *probeDriver) Stop() error {
	if d == nil {
		return nil
	}

	d.mu.Lock()
	cancel := d.cancel
	streamSession := d.streamSession
	decoderSession := d.decoderSession
	d.cancel = nil
	d.streamSession = nil
	d.decoderSession = nil
	d.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	var closeErr error
	if streamSession != nil {
		closeErr = errors.Join(closeErr, streamSession.Close())
	}
	if decoderSession != nil {
		closeErr = errors.Join(closeErr, decoderSession.Close())
	}
	return closeErr
}

func (d *probeDriver) watchErrors(stage string, errCh <-chan error) {
	if d == nil || errCh == nil {
		return
	}
	go func() {
		for err := range errCh {
			if err == nil {
				continue
			}
			d.mu.Lock()
			sink := d.sink
			d.mu.Unlock()
			logPrintf(d.logger, "camera_id=%s stage=%s error=%q", strings.TrimSpace(d.info.CameraID), strings.TrimSpace(stage), err)
			if sink != nil {
				sink.UpdateStatus(StatusError)
			}
			return
		}
	}()
}

func codecFromProbeCodec(codec string) Codec {
	switch normalizeCodec(codec) {
	case "h265":
		return CodecVideoH265
	default:
		return CodecVideoH264
	}
}

func frameTypeFromAccessUnit(payload []byte, codec string) FrameType {
	switch normalizeCodec(codec) {
	case "h265":
		for _, nalu := range splitAnnexBNALUs(payload) {
			if len(nalu) < 2 {
				continue
			}
			naluType := (nalu[0] >> 1) & 0x3f
			if naluType >= 16 && naluType <= 21 {
				return FrameTypeI
			}
			if naluType <= 31 {
				return FrameTypeP
			}
		}
	default:
		for _, nalu := range splitAnnexBNALUs(payload) {
			if len(nalu) == 0 {
				continue
			}
			switch nalu[0] & 0x1f {
			case 5:
				return FrameTypeI
			case 1:
				return FrameTypeP
			}
		}
	}
	return FrameTypeP
}

func requestedProbeVideoQuality(qualities []VideoQuality) VideoQuality {
	for _, quality := range qualities {
		switch quality {
		case VideoQualityLow, VideoQualityHigh:
			return quality
		}
	}
	return VideoQualityLow
}

func splitAnnexBNALUs(payload []byte) [][]byte {
	if len(payload) == 0 {
		return nil
	}
	var nalus [][]byte
	for start := 0; start < len(payload); {
		prefix := findAnnexBPrefix(payload, start)
		if prefix < 0 {
			break
		}
		naluStart := prefix
		for naluStart < len(payload) && payload[naluStart] == 0x00 {
			naluStart++
		}
		if naluStart < len(payload) && payload[naluStart] == 0x01 {
			naluStart++
		}
		next := findAnnexBPrefix(payload, naluStart)
		if next < 0 {
			next = len(payload)
		}
		if naluStart < next {
			nalus = append(nalus, append([]byte(nil), payload[naluStart:next]...))
		}
		start = next
	}
	return nalus
}

func findAnnexBPrefix(payload []byte, start int) int {
	for idx := start; idx+3 <= len(payload); idx++ {
		if payload[idx] == 0x00 && payload[idx+1] == 0x00 {
			if idx+2 < len(payload) && payload[idx+2] == 0x01 {
				return idx
			}
			if idx+3 < len(payload) && payload[idx+2] == 0x00 && payload[idx+3] == 0x01 {
				return idx
			}
		}
	}
	return -1
}
