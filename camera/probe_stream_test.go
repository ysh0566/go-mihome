package camera

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func TestProbeStreamReportsFirstAccessUnit(t *testing.T) {
	t.Parallel()

	client := NewProbeStreamClient(ProbeStreamClientOptions{
		PacketSource: probeRTPPacketSourceFunc(func(ctx context.Context, session Session, onPacket func(probeRTPPacket)) (ProbeStreamSession, error) {
			onPacket(probeRTPPacket{
				Codec:            "h264",
				PresentationTime: 125 * time.Millisecond,
				Packet: &rtp.Packet{
					Header: rtp.Header{
						Version:        2,
						SequenceNumber: 1,
						Timestamp:      9000,
						Marker:         true,
					},
					Payload: []byte{0x65, 0x88, 0x84},
				},
			})
			return nopProbeStreamSession{}, nil
		}),
	})

	var got []AccessUnit
	closer, err := client.Start(context.Background(), Session{Codec: "h264"}, func(unit AccessUnit) {
		got = append(got, unit)
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	if len(got) == 0 {
		t.Fatal("access units = empty, want at least one unit")
	}
	if got[0].PresentationTime != 125*time.Millisecond {
		t.Fatalf("first access unit PTS = %s, want %s", got[0].PresentationTime, 125*time.Millisecond)
	}
}

func TestProbeStreamDeliversH264AccessUnits(t *testing.T) {
	t.Parallel()

	got := runProbeStreamSinglePacket(t, "h264", []byte{0x65, 0x88, 0x84})
	want := annexBPayload([][]byte{{0x65, 0x88, 0x84}})
	if !bytes.Equal(got.Payload, want) {
		t.Fatalf("H264 access unit payload = %x, want %x", got.Payload, want)
	}
}

func TestProbeStreamDeliversH265AccessUnits(t *testing.T) {
	t.Parallel()

	got := runProbeStreamSinglePacket(t, "h265", []byte{0x26, 0x01, 0x88, 0x84})
	want := annexBPayload([][]byte{{0x26, 0x01, 0x88, 0x84}})
	if !bytes.Equal(got.Payload, want) {
		t.Fatalf("H265 access unit payload = %x, want %x", got.Payload, want)
	}
}

func TestProbeStreamPrependsSDPParameterSetsToFirstAccessUnit(t *testing.T) {
	t.Parallel()

	client := NewProbeStreamClient(ProbeStreamClientOptions{
		PacketSource: probeRTPPacketSourceFunc(func(ctx context.Context, session Session, onPacket func(probeRTPPacket)) (ProbeStreamSession, error) {
			onPacket(probeRTPPacket{
				Codec:            "h265",
				PresentationTime: 250 * time.Millisecond,
				ParameterSets: [][]byte{
					{0x40, 0x01, 0x0c, 0x01},
					{0x42, 0x01, 0x01, 0x01},
					{0x44, 0x01, 0xc0, 0xf1},
				},
				Packet: &rtp.Packet{
					Header: rtp.Header{
						Version:        2,
						SequenceNumber: 1,
						Timestamp:      90000,
						Marker:         true,
					},
					Payload: []byte{0x26, 0x01, 0x88, 0x84},
				},
			})
			return nopProbeStreamSession{}, nil
		}),
	})

	var got []AccessUnit
	session, err := client.Start(context.Background(), Session{Codec: "h265"}, func(unit AccessUnit) {
		got = append(got, unit)
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if len(got) != 1 {
		t.Fatalf("access units = %d, want 1", len(got))
	}
	want := annexBPayload([][]byte{
		{0x40, 0x01, 0x0c, 0x01},
		{0x42, 0x01, 0x01, 0x01},
		{0x44, 0x01, 0xc0, 0xf1},
		{0x26, 0x01, 0x88, 0x84},
	})
	if !bytes.Equal(got[0].Payload, want) {
		t.Fatalf("first access unit payload = %x, want %x", got[0].Payload, want)
	}
}

func TestProbeStreamUsesNegotiatedDecoderFromPacketSource(t *testing.T) {
	t.Parallel()

	client := NewProbeStreamClient(ProbeStreamClientOptions{
		PacketSource: probeRTPPacketSourceFunc(func(ctx context.Context, session Session, onPacket func(probeRTPPacket)) (ProbeStreamSession, error) {
			onPacket(probeRTPPacket{
				Codec:   "h264",
				Decoder: probeAccessUnitDecoderFunc(func(*rtp.Packet) ([][]byte, error) { return [][]byte{{0xaa, 0xbb, 0xcc}}, nil }),
				Packet: &rtp.Packet{
					Header: rtp.Header{
						Version:        2,
						SequenceNumber: 1,
						Timestamp:      90000,
						Marker:         true,
					},
					Payload: []byte{0x00},
				},
			})
			return nopProbeStreamSession{}, nil
		}),
	})

	var got []AccessUnit
	session, err := client.Start(context.Background(), Session{Codec: "h264"}, func(unit AccessUnit) {
		got = append(got, unit)
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if len(got) != 1 {
		t.Fatalf("access units = %d, want 1", len(got))
	}
	want := annexBPayload([][]byte{{0xaa, 0xbb, 0xcc}})
	if !bytes.Equal(got[0].Payload, want) {
		t.Fatalf("access unit payload = %x, want %x", got[0].Payload, want)
	}
}

func TestProbeStreamCloseStopsUnderlyingErrorRelay(t *testing.T) {
	t.Parallel()

	underlyingErrCh := make(chan error, 1)
	client := NewProbeStreamClient(ProbeStreamClientOptions{
		PacketSource: probeRTPPacketSourceFunc(func(ctx context.Context, session Session, onPacket func(probeRTPPacket)) (ProbeStreamSession, error) {
			return probeStaticErrStreamSession{errCh: underlyingErrCh}, nil
		}),
	})

	session, err := client.Start(context.Background(), Session{Codec: "h264"}, func(AccessUnit) {})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	underlyingErrCh <- errors.New("late stream failure")

	select {
	case err := <-session.Err():
		t.Fatalf("Err() = %v after Close(), want no forwarded error", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestProbeCloseOnDoneClosesWithoutContextCancellation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	doneCh := make(chan struct{})
	closed := make(chan struct{}, 1)

	go closeOnDone(ctx, doneCh, func() {
		closed <- struct{}{}
	})

	close(doneCh)

	select {
	case <-closed:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("close callback not invoked after done signal")
	}
}

func TestProbeStreamSessionWrapperSuppressesErrorsAfterClose(t *testing.T) {
	t.Parallel()

	session := newProbeStreamSessionWrapper()
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	session.reportError(errors.New("late wait failure"))

	select {
	case err := <-session.Err():
		t.Fatalf("Err() after Close() = %v, want no forwarded error", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHTTPAnnexBFrameRoundTrip(t *testing.T) {
	t.Parallel()

	want := AccessUnit{
		Codec:            "h265",
		Payload:          annexBPayload([][]byte{{0x40, 0x01}, {0x26, 0x01, 0x88, 0x84}}),
		PresentationTime: 375 * time.Millisecond,
	}

	var buf bytes.Buffer
	if err := writeHTTPAnnexBFrame(&buf, want); err != nil {
		t.Fatalf("writeHTTPAnnexBFrame() error = %v", err)
	}

	got, err := readHTTPAnnexBFrame(&buf, want.Codec)
	if err != nil {
		t.Fatalf("readHTTPAnnexBFrame() error = %v", err)
	}
	if got.Codec != want.Codec {
		t.Fatalf("frame codec = %q, want %q", got.Codec, want.Codec)
	}
	if got.PresentationTime != want.PresentationTime {
		t.Fatalf("frame PTS = %s, want %s", got.PresentationTime, want.PresentationTime)
	}
	if !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("frame payload = %x, want %x", got.Payload, want.Payload)
	}
}

func TestProbeStreamDirectAnnexBUsesResolverBackend(t *testing.T) {
	t.Parallel()

	client := NewProbeStreamClient(ProbeStreamClientOptions{
		Backend: fakeSessionResolverBackend{
			open: func(context.Context, SessionResolverStreamRequest) (SessionResolverStream, error) {
				return &fakeSessionResolverStream{
					units: []AccessUnit{
						{
							Codec:            "h264",
							Payload:          annexBPayload([][]byte{{0x65, 0x88, 0x84}}),
							PresentationTime: 250 * time.Millisecond,
						},
					},
				}, nil
			},
		},
	})

	var got []AccessUnit
	gotCh := make(chan struct{}, 1)
	session, err := client.Start(context.Background(), Session{
		CameraID:    "camera-1",
		Model:       "xiaomi.camera.v1",
		ProfileName: "mijia.camera.family",
		Transport:   transportDirectAnnexB,
		Codec:       "h264",
	}, func(unit AccessUnit) {
		got = append(got, unit)
		select {
		case gotCh <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	select {
	case <-gotCh:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("timed out waiting for direct access unit")
	}

	if len(got) != 1 {
		t.Fatalf("access units = %d, want 1", len(got))
	}
	if got[0].PresentationTime != 250*time.Millisecond {
		t.Fatalf("first access unit PTS = %s, want %s", got[0].PresentationTime, 250*time.Millisecond)
	}
}

func TestProbeStreamDirectAnnexBPassesVideoQualityToResolverBackend(t *testing.T) {
	t.Parallel()

	var gotReq SessionResolverStreamRequest
	client := NewProbeStreamClient(ProbeStreamClientOptions{
		Backend: fakeSessionResolverBackend{
			open: func(_ context.Context, req SessionResolverStreamRequest) (SessionResolverStream, error) {
				gotReq = req
				return &fakeSessionResolverStream{}, nil
			},
		},
	})

	session, err := client.Start(context.Background(), Session{
		CameraID:     "camera-1",
		Model:        "xiaomi.camera.v1",
		ProfileName:  "mijia.camera.family",
		Transport:    transportDirectAnnexB,
		Codec:        "h264",
		VideoQuality: VideoQualityLow,
	}, func(AccessUnit) {})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if got, want := gotReq.VideoQuality, VideoQualityLow; got != want {
		t.Fatalf("Open() VideoQuality = %d, want %d", got, want)
	}
}

func TestProbeStreamHTTPAnnexBReadsAccessUnits(t *testing.T) {
	t.Parallel()

	want := AccessUnit{
		Codec:            "h264",
		Payload:          annexBPayload([][]byte{{0x65, 0x88, 0x84}}),
		PresentationTime: 250 * time.Millisecond,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := writeHTTPAnnexBFrame(w, want); err != nil {
			t.Fatalf("writeHTTPAnnexBFrame() error = %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client := NewProbeStreamClient(ProbeStreamClientOptions{})
	var got []AccessUnit
	gotCh := make(chan struct{}, 1)
	session, err := client.Start(context.Background(), Session{
		Transport: transportHTTPAnnexB,
		StreamURL: server.URL,
		Codec:     "h264",
	}, func(unit AccessUnit) {
		got = append(got, unit)
		select {
		case gotCh <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	select {
	case <-gotCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for HTTP access unit")
	}

	if len(got) != 1 {
		t.Fatalf("access units = %d, want 1", len(got))
	}
	if got[0].PresentationTime != want.PresentationTime {
		t.Fatalf("first access unit PTS = %s, want %s", got[0].PresentationTime, want.PresentationTime)
	}
	if !bytes.Equal(got[0].Payload, want.Payload) {
		t.Fatalf("first access unit payload = %x, want %x", got[0].Payload, want.Payload)
	}
}

func runProbeStreamSinglePacket(t *testing.T, codec string, payload []byte) AccessUnit {
	t.Helper()

	client := NewProbeStreamClient(ProbeStreamClientOptions{
		PacketSource: probeRTPPacketSourceFunc(func(ctx context.Context, session Session, onPacket func(probeRTPPacket)) (ProbeStreamSession, error) {
			onPacket(probeRTPPacket{
				Codec:            codec,
				PresentationTime: 250 * time.Millisecond,
				Packet: &rtp.Packet{
					Header: rtp.Header{
						Version:        2,
						SequenceNumber: 1,
						Timestamp:      90000,
						Marker:         true,
					},
					Payload: append([]byte(nil), payload...),
				},
			})
			return nopProbeStreamSession{}, nil
		}),
	})

	var got []AccessUnit
	closer, err := client.Start(context.Background(), Session{Codec: codec}, func(unit AccessUnit) {
		got = append(got, unit)
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	if len(got) != 1 {
		t.Fatalf("access units = %d, want 1", len(got))
	}
	if got[0].Codec != codec {
		t.Fatalf("access unit codec = %q, want %q", got[0].Codec, codec)
	}
	return got[0]
}

type probeStaticErrStreamSession struct {
	errCh <-chan error
}

func (probeStaticErrStreamSession) Close() error {
	return nil
}

func (s probeStaticErrStreamSession) Err() <-chan error {
	return s.errCh
}

type probeAccessUnitDecoderFunc func(*rtp.Packet) ([][]byte, error)

func (f probeAccessUnitDecoderFunc) Decode(pkt *rtp.Packet) ([][]byte, error) {
	return f(pkt)
}

type probeReadCloser struct {
	io.Reader
}

func (probeReadCloser) Close() error {
	return nil
}
