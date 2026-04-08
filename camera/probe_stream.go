package camera

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph265"
	"github.com/pion/rtp"
)

var (
	// ErrProbeStreamUnavailable reports that the configured probe streamer cannot be used.
	ErrProbeStreamUnavailable = errors.New("miot camera probe stream unavailable")
)

// ProbeStreamClientOptions configures the default probe stream client.
type ProbeStreamClientOptions struct {
	PacketSource probeRTPPacketSource
	Backend      SessionResolverBackend
}

// ProbeStreamClient is a reusable default ProbeStreamer implementation.
type ProbeStreamClient struct {
	packetSource probeRTPPacketSource
	backend      SessionResolverBackend
}

type probeRTPPacket struct {
	Codec            string
	PresentationTime time.Duration
	ParameterSets    [][]byte
	Decoder          probeAccessUnitDecoder
	Packet           *rtp.Packet
}

type probeRTPPacketSource interface {
	Start(context.Context, Session, func(probeRTPPacket)) (ProbeStreamSession, error)
}

type probeRTPPacketSourceFunc func(context.Context, Session, func(probeRTPPacket)) (ProbeStreamSession, error)

func (f probeRTPPacketSourceFunc) Start(ctx context.Context, session Session, onPacket func(probeRTPPacket)) (ProbeStreamSession, error) {
	return f(ctx, session, onPacket)
}

type probeAccessUnitDecoder interface {
	Decode(*rtp.Packet) ([][]byte, error)
}

type gortsplibProbePacketSource struct{}

type probeStreamSessionWrapper struct {
	closer    io.Closer
	errCh     chan error
	doneCh    chan struct{}
	stateMu   sync.Mutex
	closed    bool
	closeMu   sync.Once
	closeFunc func() error
}

func (session *probeStreamSessionWrapper) Close() error {
	if session == nil {
		return nil
	}
	var err error
	session.closeMu.Do(func() {
		session.stateMu.Lock()
		defer session.stateMu.Unlock()
		session.closed = true
		if session.doneCh != nil {
			close(session.doneCh)
		}
		switch {
		case session.closeFunc != nil:
			err = session.closeFunc()
		case session.closer != nil:
			err = session.closer.Close()
		}
	})
	return err
}

func (session *probeStreamSessionWrapper) Err() <-chan error {
	if session == nil {
		return nil
	}
	return session.errCh
}

func (session *probeStreamSessionWrapper) reportError(err error) {
	if session == nil || err == nil {
		return
	}
	session.stateMu.Lock()
	defer session.stateMu.Unlock()
	if session.closed {
		return
	}
	select {
	case session.errCh <- err:
	default:
	}
}

func newProbeStreamSessionWrapper() *probeStreamSessionWrapper {
	return &probeStreamSessionWrapper{
		closer: io.NopCloser(strings.NewReader("")),
		errCh:  make(chan error, 1),
		doneCh: make(chan struct{}),
	}
}

// NewProbeStreamClient constructs a default probe stream client.
func NewProbeStreamClient(options ProbeStreamClientOptions) *ProbeStreamClient {
	packetSource := options.PacketSource
	if packetSource == nil {
		packetSource = gortsplibProbePacketSource{}
	}
	return &ProbeStreamClient{
		packetSource: packetSource,
		backend:      options.Backend,
	}
}

// Start starts streaming access units from the resolved session.
func (client *ProbeStreamClient) Start(ctx context.Context, session Session, onAccessUnit func(AccessUnit)) (ProbeStreamSession, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: stream client unavailable", ErrProbeStreamUnavailable)
	}
	if strings.EqualFold(strings.TrimSpace(session.Transport), transportDirectAnnexB) {
		return startDirectAnnexBStream(ctx, client.backend, session, onAccessUnit)
	}
	if strings.EqualFold(strings.TrimSpace(session.Transport), transportHTTPAnnexB) {
		return startHTTPAnnexBStream(ctx, session, onAccessUnit)
	}
	if client.packetSource == nil {
		return nil, fmt.Errorf("%w: packet source unavailable", ErrProbeStreamUnavailable)
	}
	if onAccessUnit == nil {
		return nil, fmt.Errorf("%w: access unit callback unavailable", ErrProbeStreamUnavailable)
	}

	wrappedSession := newProbeStreamSessionWrapper()
	var decoder probeAccessUnitDecoder
	decoderCodec := ""
	prependParameterSets := true
	underlyingSession, err := client.packetSource.Start(ctx, session, func(packet probeRTPPacket) {
		if packet.Packet == nil {
			return
		}
		if decoder == nil {
			if packet.Decoder != nil {
				decoder = packet.Decoder
				decoderCodec = normalizeCodec(firstNonEmptyCodec(packet.Codec, session.Codec))
			} else {
				var err error
				decoder, decoderCodec, err = newProbeAccessUnitDecoder(firstNonEmptyCodec(packet.Codec, session.Codec))
				if err != nil {
					wrappedSession.reportError(err)
					return
				}
			}
		}
		nalus, err := decoder.Decode(packet.Packet)
		if err != nil {
			if isMorePacketsError(err) {
				return
			}
			wrappedSession.reportError(fmt.Errorf("%w: decode RTP packet: %w", ErrProbeStreamUnavailable, err))
			return
		}
		if len(nalus) == 0 {
			return
		}
		if prependParameterSets && len(packet.ParameterSets) > 0 {
			nalus = append(cloneNALUs(packet.ParameterSets), nalus...)
		}
		prependParameterSets = false
		onAccessUnit(AccessUnit{
			Codec:            decoderCodec,
			Payload:          annexBPayload(nalus),
			PresentationTime: packet.PresentationTime,
		})
	})
	if err != nil {
		return nil, err
	}
	if underlyingSession != nil {
		wrappedSession.closer = underlyingSession
		if errCh := underlyingSession.Err(); errCh != nil {
			go func() {
				for {
					select {
					case <-wrappedSession.doneCh:
						return
					case err, ok := <-errCh:
						if !ok || err == nil {
							return
						}
						select {
						case <-wrappedSession.doneCh:
							return
						default:
						}
						wrappedSession.reportError(err)
					}
				}
			}()
		}
	}
	return wrappedSession, nil
}

func startDirectAnnexBStream(ctx context.Context, backend SessionResolverBackend, session Session, onAccessUnit func(AccessUnit)) (ProbeStreamSession, error) {
	if backend == nil {
		return nil, fmt.Errorf("%w: direct resolver backend unavailable", ErrProbeStreamUnavailable)
	}
	if onAccessUnit == nil {
		return nil, fmt.Errorf("%w: access unit callback unavailable", ErrProbeStreamUnavailable)
	}

	stream, err := backend.Open(ctx, SessionResolverStreamRequest{
		CameraID:     strings.TrimSpace(session.CameraID),
		Model:        strings.TrimSpace(session.Model),
		Profile:      strings.TrimSpace(session.ProfileName),
		Codec:        strings.TrimSpace(session.Codec),
		VideoQuality: session.VideoQuality,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: open direct stream: %w", ErrProbeStreamUnavailable, err)
	}

	wrappedSession := newProbeStreamSessionWrapper()
	wrappedSession.closeFunc = func() error {
		return stream.Close()
	}
	go func() {
		defer func() { _ = stream.Close() }()
		for {
			unit, recvErr := stream.Recv(ctx)
			if recvErr != nil {
				if errors.Is(recvErr, io.EOF) || ctx.Err() != nil {
					return
				}
				wrappedSession.reportError(fmt.Errorf("%w: read direct stream: %w", ErrProbeStreamUnavailable, recvErr))
				return
			}
			unit.Codec = firstNonEmptyCodec(unit.Codec, session.Codec)
			onAccessUnit(unit)
		}
	}()
	return wrappedSession, nil
}

func startHTTPAnnexBStream(ctx context.Context, session Session, onAccessUnit func(AccessUnit)) (ProbeStreamSession, error) {
	if onAccessUnit == nil {
		return nil, fmt.Errorf("%w: access unit callback unavailable", ErrProbeStreamUnavailable)
	}
	streamURL := strings.TrimSpace(session.StreamURL)
	if streamURL == "" {
		return nil, fmt.Errorf("%w: stream url unavailable", ErrProbeStreamUnavailable)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create HTTP stream request: %w", ErrProbeStreamUnavailable, err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: open HTTP stream: %w", ErrProbeStreamUnavailable, err)
	}
	if res.StatusCode != http.StatusOK {
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return nil, fmt.Errorf("%w: open HTTP stream returned %d: %s", ErrProbeStreamUnavailable, res.StatusCode, strings.TrimSpace(string(body)))
	}

	wrappedSession := newProbeStreamSessionWrapper()
	wrappedSession.closeFunc = func() error {
		return res.Body.Close()
	}
	go func() {
		defer func() { _ = res.Body.Close() }()
		for {
			unit, readErr := readHTTPAnnexBFrame(res.Body, session.Codec)
			if readErr != nil {
				if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) || ctx.Err() != nil {
					return
				}
				wrappedSession.reportError(fmt.Errorf("%w: read HTTP stream: %w", ErrProbeStreamUnavailable, readErr))
				return
			}
			onAccessUnit(unit)
		}
	}()
	return wrappedSession, nil
}

func newProbeAccessUnitDecoder(codec string) (probeAccessUnitDecoder, string, error) {
	switch normalizeCodec(codec) {
	case "h264":
		forma := &format.H264{}
		decoder, err := forma.CreateDecoder()
		if err != nil {
			return nil, "", fmt.Errorf("%w: create H264 decoder: %w", ErrProbeStreamUnavailable, err)
		}
		return decoder, "h264", nil
	case "h265":
		forma := &format.H265{}
		decoder, err := forma.CreateDecoder()
		if err != nil {
			return nil, "", fmt.Errorf("%w: create H265 decoder: %w", ErrProbeStreamUnavailable, err)
		}
		return decoder, "h265", nil
	default:
		return nil, "", fmt.Errorf("%w: unsupported codec %q", ErrProbeStreamUnavailable, strings.TrimSpace(codec))
	}
}

func probeAccessUnitDecoderFromFormat(forma format.Format) (probeAccessUnitDecoder, error) {
	switch typed := forma.(type) {
	case *format.H264:
		return typed.CreateDecoder()
	case *format.H265:
		return typed.CreateDecoder()
	default:
		return nil, fmt.Errorf("unsupported negotiated format %T", forma)
	}
}

func (gortsplibProbePacketSource) Start(ctx context.Context, session Session, onPacket func(probeRTPPacket)) (ProbeStreamSession, error) {
	if onPacket == nil {
		return nil, fmt.Errorf("%w: packet callback unavailable", ErrProbeStreamUnavailable)
	}
	u, err := base.ParseURL(strings.TrimSpace(session.StreamURL))
	if err != nil {
		return nil, fmt.Errorf("%w: parse stream url: %w", ErrProbeStreamUnavailable, err)
	}

	client := &gortsplib.Client{
		Scheme: u.Scheme,
		Host:   u.Host,
	}
	if protocol, ok := transportProtocol(session.Transport); ok {
		client.Protocol = &protocol
	}

	if err := client.Start(); err != nil {
		return nil, fmt.Errorf("%w: start rtsp client: %w", ErrProbeStreamUnavailable, err)
	}

	desc, _, err := client.Describe(u)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("%w: describe stream: %w", ErrProbeStreamUnavailable, err)
	}

	media, forma, codec, err := findMedia(desc, session.Codec)
	if err != nil {
		client.Close()
		return nil, err
	}

	if _, err := client.Setup(desc.BaseURL, media, 0, 0); err != nil {
		client.Close()
		return nil, fmt.Errorf("%w: setup stream: %w", ErrProbeStreamUnavailable, err)
	}
	parameterSets := formatParameterSets(forma)
	decoder, err := probeAccessUnitDecoderFromFormat(forma)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("%w: create negotiated decoder: %w", ErrProbeStreamUnavailable, err)
	}

	client.OnPacketRTP(media, forma, func(pkt *rtp.Packet) {
		pts, ok := client.PacketPTS(media, pkt)
		if !ok {
			return
		}
		onPacket(probeRTPPacket{
			Codec:            codec,
			PresentationTime: time.Duration(pts),
			ParameterSets:    cloneNALUs(parameterSets),
			Decoder:          decoder,
			Packet:           pkt,
		})
	})

	if _, err := client.Play(nil); err != nil {
		client.Close()
		return nil, fmt.Errorf("%w: play stream: %w", ErrProbeStreamUnavailable, err)
	}

	sessionWrapper := newProbeStreamSessionWrapper()
	sessionWrapper.closeFunc = func() error {
		client.Close()
		return nil
	}
	go func() {
		closeOnDone(ctx, sessionWrapper.doneCh, client.Close)
	}()
	go func() {
		if err := client.Wait(); err != nil && ctx.Err() == nil {
			sessionWrapper.reportError(fmt.Errorf("%w: rtsp client terminated: %w", ErrProbeStreamUnavailable, err))
		}
	}()
	return sessionWrapper, nil
}

func closeOnDone(ctx context.Context, doneCh <-chan struct{}, closeFn func()) {
	select {
	case <-ctx.Done():
		closeFn()
	case <-doneCh:
		closeFn()
	}
}

func findMedia(desc *description.Session, codec string) (*description.Media, format.Format, string, error) {
	switch normalizeCodec(codec) {
	case "h264":
		var forma *format.H264
		media := desc.FindFormat(&forma)
		if media == nil || forma == nil {
			return nil, nil, "", fmt.Errorf("%w: H264 media not found", ErrProbeStreamUnavailable)
		}
		return media, forma, "h264", nil
	case "h265":
		var forma *format.H265
		media := desc.FindFormat(&forma)
		if media == nil || forma == nil {
			return nil, nil, "", fmt.Errorf("%w: H265 media not found", ErrProbeStreamUnavailable)
		}
		return media, forma, "h265", nil
	default:
		return nil, nil, "", fmt.Errorf("%w: unsupported codec %q", ErrProbeStreamUnavailable, strings.TrimSpace(codec))
	}
}

func transportProtocol(transport string) (gortsplib.Protocol, bool) {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "tcp":
		return gortsplib.ProtocolTCP, true
	case "udp":
		return gortsplib.ProtocolUDP, true
	default:
		return 0, false
	}
}

func isMorePacketsError(err error) bool {
	return errors.Is(err, rtph264.ErrMorePacketsNeeded) ||
		errors.Is(err, rtph264.ErrNonStartingPacketAndNoPrevious) ||
		errors.Is(err, rtph265.ErrMorePacketsNeeded) ||
		errors.Is(err, rtph265.ErrNonStartingPacketAndNoPrevious)
}

func formatParameterSets(forma format.Format) [][]byte {
	switch typed := forma.(type) {
	case *format.H264:
		sps, pps := typed.SafeParams()
		return filterEmptyNALUs([][]byte{sps, pps})
	case *format.H265:
		vps, sps, pps := typed.SafeParams()
		return filterEmptyNALUs([][]byte{vps, sps, pps})
	default:
		return nil
	}
}

func annexBPayload(nalus [][]byte) []byte {
	size := 0
	for _, nalu := range nalus {
		size += 4 + len(nalu)
	}
	payload := make([]byte, 0, size)
	for _, nalu := range nalus {
		payload = append(payload, 0x00, 0x00, 0x00, 0x01)
		payload = append(payload, nalu...)
	}
	return payload
}

func cloneNALUs(nalus [][]byte) [][]byte {
	if len(nalus) == 0 {
		return nil
	}
	cloned := make([][]byte, 0, len(nalus))
	for _, nalu := range nalus {
		cloned = append(cloned, append([]byte(nil), nalu...))
	}
	return cloned
}

func filterEmptyNALUs(nalus [][]byte) [][]byte {
	filtered := make([][]byte, 0, len(nalus))
	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}
		filtered = append(filtered, append([]byte(nil), nalu...))
	}
	return filtered
}

func firstNonEmptyCodec(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
