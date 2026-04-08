package camera

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProbeDriverFactoryBridgesRuntimeCallbacks(t *testing.T) {
	t.Parallel()

	runtime := NewRuntime(RuntimeOptions{
		Factory: NewProbeDriverFactory(ProbeDriverFactoryOptions{
			Resolvers: []Resolver{
				resolverFunc{
					name: "resolver",
					resolve: func(context.Context, Target, Profile) (Session, error) {
						return Session{
							SessionID: "session-1",
							Transport: "rtsp",
							StreamURL: "rtsp://camera.example.invalid/live/main",
							Codec:     "h264",
						}, nil
					},
				},
			},
			Streamer: probeStreamerFunc(func(ctx context.Context, session Session, onAccessUnit func(AccessUnit)) (ProbeStreamSession, error) {
				onAccessUnit(AccessUnit{
					Codec:   session.Codec,
					Payload: []byte{0x00, 0x00, 0x00, 0x01, 0x65},
				})
				return nopProbeStreamSession{}, nil
			}),
			Decoder: probeFrameDecoderFunc(func(ctx context.Context, config ProbeDecoderConfig, onFrame func(JPEGFrame)) (ProbeDecoderSession, error) {
				return probeDecoderSessionFunc{
					decode: func(AccessUnit) error {
						onFrame(JPEGFrame{Channel: 0, Payload: append([]byte(nil), testJPEGFrame...)})
						return nil
					},
				}, nil
			}),
		}),
	})
	camera, err := runtime.Create(Info{
		CameraID:     "camera-1",
		Model:        "xiaomi.camera.demo",
		ChannelCount: 1,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var statuses []Status
	var rawVideo []Frame
	var jpegFrames [][]byte
	camera.RegisterStatusChanged(func(_ string, status Status) {
		statuses = append(statuses, status)
	})
	camera.RegisterRawVideo(0, func(_ string, frame Frame) {
		rawVideo = append(rawVideo, frame)
	})
	camera.RegisterJPEG(0, func(_ string, frame JPEGFrame) {
		jpegFrames = append(jpegFrames, append([]byte(nil), frame.Payload...))
	})

	if err := camera.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got, want := statuses, []Status{StatusConnecting, StatusConnected}; !equalStatuses(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	if got, want := len(rawVideo), 1; got != want {
		t.Fatalf("len(rawVideo) = %d, want %d", got, want)
	}
	if got, want := rawVideo[0].Codec, CodecVideoH264; got != want {
		t.Fatalf("rawVideo[0].Codec = %d, want %d", got, want)
	}
	if got, want := rawVideo[0].FrameType, FrameTypeI; got != want {
		t.Fatalf("rawVideo[0].FrameType = %d, want %d", got, want)
	}
	if got, want := len(jpegFrames), 1; got != want {
		t.Fatalf("len(jpegFrames) = %d, want %d", got, want)
	}
	if !bytes.Equal(jpegFrames[0], testJPEGFrame) {
		t.Fatalf("jpegFrames[0] = %x, want %x", jpegFrames[0], testJPEGFrame)
	}
}

func TestProbeDriverFactoryPassesRequestedVideoQualityToStreamer(t *testing.T) {
	t.Parallel()

	var gotSession Session
	runtime := NewRuntime(RuntimeOptions{
		Factory: NewProbeDriverFactory(ProbeDriverFactoryOptions{
			Resolvers: []Resolver{
				resolverFunc{
					name: "resolver",
					resolve: func(context.Context, Target, Profile) (Session, error) {
						return Session{
							SessionID: "session-1",
							Transport: transportDirectAnnexB,
							Codec:     "h264",
						}, nil
					},
				},
			},
			Streamer: probeStreamerFunc(func(_ context.Context, session Session, _ func(AccessUnit)) (ProbeStreamSession, error) {
				gotSession = session
				return nopProbeStreamSession{}, nil
			}),
			Decoder: probeFrameDecoderFunc(func(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error) {
				return probeDecoderSessionFunc{}, nil
			}),
		}),
	})
	camera, err := runtime.Create(Info{
		CameraID: "camera-1",
		Model:    "xiaomi.camera.demo",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := camera.Start(context.Background(), StartOptions{
		VideoQualities: []VideoQuality{VideoQualityLow},
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got, want := gotSession.VideoQuality, VideoQualityLow; got != want {
		t.Fatalf("session.VideoQuality = %d, want %d", got, want)
	}
}

func TestProbeDriverFactoryReturnsUnavailableWithoutResolvers(t *testing.T) {
	t.Parallel()

	runtime := NewRuntime(RuntimeOptions{
		Factory: NewProbeDriverFactory(ProbeDriverFactoryOptions{
			Streamer: probeStreamerFunc(func(context.Context, Session, func(AccessUnit)) (ProbeStreamSession, error) {
				return nopProbeStreamSession{}, nil
			}),
			Decoder: probeFrameDecoderFunc(func(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error) {
				return probeDecoderSessionFunc{}, nil
			}),
		}),
	})
	camera, err := runtime.Create(Info{
		CameraID:     "camera-1",
		Model:        "xiaomi.camera.demo",
		ChannelCount: 1,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	err = camera.Start(context.Background(), StartOptions{})
	if !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("Start() error = %v, want %v", err, ErrRuntimeUnavailable)
	}
	if got, want := camera.Status(), StatusError; got != want {
		t.Fatalf("Status() = %v, want %v", got, want)
	}
}

func TestProbeDriverFactoryMarksRuntimeErrorOnDecodeFailure(t *testing.T) {
	t.Parallel()

	runtime := NewRuntime(RuntimeOptions{
		Factory: NewProbeDriverFactory(ProbeDriverFactoryOptions{
			Resolvers: []Resolver{
				resolverFunc{
					name: "resolver",
					resolve: func(context.Context, Target, Profile) (Session, error) {
						return Session{
							SessionID: "session-1",
							Transport: "rtsp",
							StreamURL: "rtsp://camera.example.invalid/live/main",
							Codec:     "h264",
						}, nil
					},
				},
			},
			Streamer: probeStreamerFunc(func(ctx context.Context, session Session, onAccessUnit func(AccessUnit)) (ProbeStreamSession, error) {
				onAccessUnit(AccessUnit{
					Codec:   session.Codec,
					Payload: []byte{0x00, 0x00, 0x00, 0x01, 0x65},
				})
				return nopProbeStreamSession{}, nil
			}),
			Decoder: probeFrameDecoderFunc(func(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error) {
				return probeDecoderSessionFunc{
					decode: func(AccessUnit) error {
						return errors.New("decode failed")
					},
				}, nil
			}),
		}),
	})
	camera, err := runtime.Create(Info{
		CameraID: "camera-1",
		Model:    "xiaomi.camera.demo",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var statuses []Status
	camera.RegisterStatusChanged(func(_ string, status Status) {
		statuses = append(statuses, status)
	})

	if err := camera.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got, want := statuses, []Status{StatusConnecting, StatusError, StatusConnected}; !equalStatuses(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
}

func TestProbeDriverFactoryMarksRuntimeErrorOnAsyncFailures(t *testing.T) {
	t.Parallel()

	streamErrCh := make(chan error, 1)
	decoderErrCh := make(chan error, 1)
	runtime := NewRuntime(RuntimeOptions{
		Factory: NewProbeDriverFactory(ProbeDriverFactoryOptions{
			Resolvers: []Resolver{
				resolverFunc{
					name: "resolver",
					resolve: func(context.Context, Target, Profile) (Session, error) {
						return Session{
							SessionID: "session-1",
							Transport: "rtsp",
							StreamURL: "rtsp://camera.example.invalid/live/main",
							Codec:     "h264",
						}, nil
					},
				},
			},
			Streamer: probeStreamerFunc(func(context.Context, Session, func(AccessUnit)) (ProbeStreamSession, error) {
				return probeStreamSessionFunc{
					errCh: streamErrCh,
				}, nil
			}),
			Decoder: probeFrameDecoderFunc(func(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error) {
				return probeDecoderSessionFunc{
					errCh: decoderErrCh,
				}, nil
			}),
		}),
	})
	camera, err := runtime.Create(Info{
		CameraID: "camera-1",
		Model:    "xiaomi.camera.demo",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	statuses := make(chan Status, 4)
	camera.RegisterStatusChanged(func(_ string, status Status) {
		statuses <- status
	})

	if err := camera.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	expectStatus(t, statuses, StatusConnecting)
	expectStatus(t, statuses, StatusConnected)

	streamErrCh <- errors.New("stream failed")
	expectStatus(t, statuses, StatusError)

	decoderErrCh <- errors.New("decoder failed")
}

func TestProbeDriverFactoryLogsAsyncStreamFailureReason(t *testing.T) {
	t.Parallel()

	streamErrCh := make(chan error, 1)
	logger := &capturePrintfLogger{}
	runtime := NewRuntime(RuntimeOptions{
		Factory: NewProbeDriverFactory(ProbeDriverFactoryOptions{
			Logger: logger,
			Resolvers: []Resolver{
				resolverFunc{
					name: "resolver",
					resolve: func(context.Context, Target, Profile) (Session, error) {
						return Session{
							SessionID: "session-1",
							Transport: "rtsp",
							StreamURL: "rtsp://camera.example.invalid/live/main",
							Codec:     "h264",
						}, nil
					},
				},
			},
			Streamer: probeStreamerFunc(func(context.Context, Session, func(AccessUnit)) (ProbeStreamSession, error) {
				return probeStreamSessionFunc{
					errCh: streamErrCh,
				}, nil
			}),
			Decoder: probeFrameDecoderFunc(func(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error) {
				return probeDecoderSessionFunc{}, nil
			}),
		}),
	})
	camera, err := runtime.Create(Info{
		CameraID: "camera-1",
		Model:    "xiaomi.camera.demo",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := camera.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	streamErrCh <- errors.New("stream failed")
	time.Sleep(50 * time.Millisecond)

	if output := logger.String(); !strings.Contains(output, `camera_id=camera-1 stage=stream_runtime_error error="stream failed"`) {
		t.Fatalf("logger output = %q, want stream runtime error", output)
	}
}

func TestProbeDriverFactoryLogsDecoderWriteFailureReason(t *testing.T) {
	t.Parallel()

	logger := &capturePrintfLogger{}
	runtime := NewRuntime(RuntimeOptions{
		Factory: NewProbeDriverFactory(ProbeDriverFactoryOptions{
			Logger: logger,
			Resolvers: []Resolver{
				resolverFunc{
					name: "resolver",
					resolve: func(context.Context, Target, Profile) (Session, error) {
						return Session{
							SessionID: "session-1",
							Transport: "rtsp",
							StreamURL: "rtsp://camera.example.invalid/live/main",
							Codec:     "h264",
						}, nil
					},
				},
			},
			Streamer: probeStreamerFunc(func(ctx context.Context, session Session, onAccessUnit func(AccessUnit)) (ProbeStreamSession, error) {
				onAccessUnit(AccessUnit{
					Codec:   session.Codec,
					Payload: []byte{0x00, 0x00, 0x00, 0x01, 0x65},
				})
				return nopProbeStreamSession{}, nil
			}),
			Decoder: probeFrameDecoderFunc(func(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error) {
				return probeDecoderSessionFunc{
					decode: func(AccessUnit) error {
						return errors.New("decoder write failed")
					},
				}, nil
			}),
		}),
	})
	camera, err := runtime.Create(Info{
		CameraID: "camera-1",
		Model:    "xiaomi.camera.demo",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := camera.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if output := logger.String(); !strings.Contains(output, `camera_id=camera-1 stage=decoder_write_error error="decoder write failed"`) {
		t.Fatalf("logger output = %q, want decoder write error", output)
	}
}

func expectStatus(t *testing.T, statuses <-chan Status, want Status) {
	t.Helper()

	select {
	case got := <-statuses:
		if got != want {
			t.Fatalf("status = %v, want %v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for status %v", want)
	}
}

type probeStreamerFunc func(context.Context, Session, func(AccessUnit)) (ProbeStreamSession, error)

func (f probeStreamerFunc) Start(ctx context.Context, session Session, onAccessUnit func(AccessUnit)) (ProbeStreamSession, error) {
	return f(ctx, session, onAccessUnit)
}

type probeFrameDecoderFunc func(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error)

func (f probeFrameDecoderFunc) Start(ctx context.Context, config ProbeDecoderConfig, onFrame func(JPEGFrame)) (ProbeDecoderSession, error) {
	return f(ctx, config, onFrame)
}

type probeDecoderSessionFunc struct {
	decode func(AccessUnit) error
	close  func() error
	errCh  chan error
}

func (f probeDecoderSessionFunc) Decode(unit AccessUnit) error {
	if f.decode == nil {
		return nil
	}
	return f.decode(unit)
}

func (f probeDecoderSessionFunc) Close() error {
	if f.close == nil {
		return nil
	}
	return f.close()
}

func (f probeDecoderSessionFunc) Err() <-chan error {
	return f.errCh
}

type probeStreamSessionFunc struct {
	close func() error
	errCh chan error
}

func (f probeStreamSessionFunc) Close() error {
	if f.close == nil {
		return nil
	}
	return f.close()
}

func (f probeStreamSessionFunc) Err() <-chan error {
	return f.errCh
}

type nopProbeStreamSession struct{}

func (nopProbeStreamSession) Close() error {
	return nil
}

func (nopProbeStreamSession) Err() <-chan error {
	return nil
}
