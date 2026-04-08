package camera

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	miot "github.com/ysh0566/go-mihome"
)

func TestMiHomeCameraPipelineFactoryRetriesWithDiscoveredHost(t *testing.T) {
	var mediaHosts []string
	factory := &miHomeCameraPipelineFactory{
		loadSession: func(context.Context) (miHomeSession, error) {
			return miHomeSession{DeviceID: "caller-1"}, nil
		},
		newCloud: func(miHomeSession) (miHomeCameraCloud, error) {
			return fakeMiHomeCameraCloud{
				device: miot.DeviceInfo{
					DID:     "camera-1",
					Model:   "chuangmi.camera.060a02",
					LocalIP: "192.168.31.75",
				},
				vendor: miot.CameraVendorInfo{
					SupportVendor: "cs2",
					Sign:          "vendor-sign",
					PublicKey:     "vendor-public",
				},
			}, nil
		},
		newMedia: func(host string, _ miot.CameraVendorInfo, _ string, _ string, _ []byte, _ []byte) (miHomeCameraMediaClient, error) {
			mediaHosts = append(mediaHosts, host)
			if host == "192.168.31.75" {
				return nil, errors.New("read udp [::]:12345: i/o timeout")
			}
			return fakeMiHomeCameraMediaClient{}, nil
		},
		discoverHosts: func(context.Context, string) ([]string, error) {
			return []string{"192.168.31.105"}, nil
		},
		decoder: probeFrameDecoderFunc(func(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error) {
			return probeDecoderSessionFunc{}, nil
		}),
	}

	closer, err := factory.Start(CameraStreamDescriptor{
		CameraID: "camera-1",
		Model:    "chuangmi.camera.060a02",
	}, func(JPEGFrame) {})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	if got, want := mediaHosts, []string{"192.168.31.75", "192.168.31.105"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("newMedia hosts = %#v, want %#v", got, want)
	}
}

func TestMiHomeCameraPipelineFactoryRetriesTransientMediaBootstrap(t *testing.T) {
	startCalls := 0
	factory := &miHomeCameraPipelineFactory{
		loadSession: func(context.Context) (miHomeSession, error) {
			return miHomeSession{DeviceID: "caller-1"}, nil
		},
		newCloud: func(miHomeSession) (miHomeCameraCloud, error) {
			return fakeMiHomeCameraCloud{
				device: miot.DeviceInfo{
					DID:     "camera-1",
					Model:   "chuangmi.camera.060a02",
					LocalIP: "192.168.31.75",
				},
				vendor: miot.CameraVendorInfo{
					SupportVendor: "cs2",
					Sign:          "vendor-sign",
					PublicKey:     "vendor-public",
				},
			}, nil
		},
		newMedia: func(string, miot.CameraVendorInfo, string, string, []byte, []byte) (miHomeCameraMediaClient, error) {
			return &fakeRetryableMiHomeCameraMediaClient{
				startMedia: func(string, string, string) error {
					startCalls++
					if startCalls == 1 {
						return errors.New("temporary media startup failure")
					}
					return nil
				},
			}, nil
		},
		discoverHosts: func(context.Context, string) ([]string, error) {
			return nil, nil
		},
		decoder: probeFrameDecoderFunc(func(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error) {
			return probeDecoderSessionFunc{}, nil
		}),
		firstTimeout: time.Millisecond,
	}

	closer, err := factory.Start(CameraStreamDescriptor{
		CameraID: "camera-1",
		Model:    "chuangmi.camera.060a02",
	}, func(JPEGFrame) {})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	if got, want := startCalls, 2; got != want {
		t.Fatalf("StartMedia() calls = %d, want %d", got, want)
	}
}

func TestMiHomeCameraPipelineFactoryUsesRequestedVideoQuality(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		quality     VideoQuality
		wantQuality string
	}{
		{name: "low", quality: VideoQualityLow, wantQuality: "sd"},
		{name: "high", quality: VideoQualityHigh, wantQuality: "hd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotQuality string
			factory := &miHomeCameraPipelineFactory{
				loadSession: func(context.Context) (miHomeSession, error) {
					return miHomeSession{DeviceID: "caller-1"}, nil
				},
				newCloud: func(miHomeSession) (miHomeCameraCloud, error) {
					return fakeMiHomeCameraCloud{
						device: miot.DeviceInfo{
							DID:     "camera-1",
							Model:   "chuangmi.camera.060a02",
							LocalIP: "192.168.31.75",
						},
						vendor: miot.CameraVendorInfo{
							SupportVendor: "cs2",
							Sign:          "vendor-sign",
							PublicKey:     "vendor-public",
						},
					}, nil
				},
				newMedia: func(string, miot.CameraVendorInfo, string, string, []byte, []byte) (miHomeCameraMediaClient, error) {
					return &fakeRetryableMiHomeCameraMediaClient{
						startMedia: func(_ string, quality string, _ string) error {
							gotQuality = quality
							return nil
						},
					}, nil
				},
				decoder: probeFrameDecoderFunc(func(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error) {
					return probeDecoderSessionFunc{}, nil
				}),
			}

			closer, err := factory.Start(CameraStreamDescriptor{
				CameraID:     "camera-1",
				Model:        "chuangmi.camera.060a02",
				VideoQuality: tt.quality,
			}, func(JPEGFrame) {})
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			t.Cleanup(func() { _ = closer.Close() })

			if got, want := gotQuality, tt.wantQuality; got != want {
				t.Fatalf("StartMedia() quality = %q, want %q", got, want)
			}
		})
	}
}

func TestMiHomeCameraPipelineFactoryPreservesNativeCauseWhenFallbackUnavailable(t *testing.T) {
	cause := errors.New("open native camera media: read udp [::]:12345: i/o timeout")
	factory := &miHomeCameraPipelineFactory{
		fallback: &probeCameraPipelineFactory{},
	}

	_, err := factory.tryFallback(CameraStreamDescriptor{
		CameraID: "camera-1",
		Model:    "chuangmi.camera.060a02",
	}, func(JPEGFrame) {}, cause)
	if !errors.Is(err, cause) {
		t.Fatalf("tryFallback() error = %v, want native cause %v", err, cause)
	}
}

func TestMiHomeCameraPipelineFirstFrameDecodeAttemptsOnlyOnce(t *testing.T) {
	payload := annexBPayload([][]byte{
		{0x67, 0x64, 0x00, 0x1f},
		{0x68, 0xeb, 0xe3, 0xcb, 0x22, 0xc0},
		{0x65, 0x88, 0x84},
	})
	decodeCalls := 0
	pipeline := &miHomeCameraPipeline{
		client: &sequencedMiHomeCameraMediaClient{
			packets: []*miHomeMissPacket{
				{CodecID: miHomeMissCodecH264, Payload: payload},
				{CodecID: miHomeMissCodecH264, Payload: payload},
				{CodecID: miHomeMissCodecH264, Payload: payload},
			},
		},
	}

	pipeline.run(
		context.Background(),
		probeFrameDecoderFunc(func(context.Context, ProbeDecoderConfig, func(JPEGFrame)) (ProbeDecoderSession, error) {
			return probeDecoderSessionFunc{}, nil
		}),
		func(context.Context, string, []byte) ([]byte, error) {
			decodeCalls++
			return nil, errors.New("synthetic first-frame decode failure")
		},
		nil,
	)

	if got, want := decodeCalls, 1; got != want {
		t.Fatalf("first-frame decode calls = %d, want %d", got, want)
	}
}

type fakeMiHomeCameraCloud struct {
	device miot.DeviceInfo
	vendor miot.CameraVendorInfo
}

func (f fakeMiHomeCameraCloud) GetDevice(context.Context, string) (miot.DeviceInfo, error) {
	return f.device, nil
}

func (f fakeMiHomeCameraCloud) GetCameraVendor(context.Context, string, []byte, string) (miot.CameraVendorInfo, error) {
	return f.vendor, nil
}

func (fakeMiHomeCameraCloud) GetCameraVendorSecurity(context.Context, string) (miot.CameraVendorSecurity, error) {
	return miot.CameraVendorSecurity{}, nil
}

func (fakeMiHomeCameraCloud) WakeupCamera(context.Context, string) error {
	return nil
}

type fakeMiHomeCameraMediaClient struct{}

func (fakeMiHomeCameraMediaClient) StartMedia(string, string, string) error {
	return nil
}

func (fakeMiHomeCameraMediaClient) StopMedia() error {
	return nil
}

func (fakeMiHomeCameraMediaClient) ReadPacket() (*miHomeMissPacket, error) {
	return nil, io.EOF
}

func (fakeMiHomeCameraMediaClient) SetDeadline(time.Time) error {
	return nil
}

func (fakeMiHomeCameraMediaClient) Close() error {
	return nil
}

type fakeRetryableMiHomeCameraMediaClient struct {
	startMedia func(string, string, string) error
}

func (f *fakeRetryableMiHomeCameraMediaClient) StartMedia(channel string, quality string, audio string) error {
	if f != nil && f.startMedia != nil {
		return f.startMedia(channel, quality, audio)
	}
	return nil
}

func (*fakeRetryableMiHomeCameraMediaClient) StopMedia() error {
	return nil
}

func (*fakeRetryableMiHomeCameraMediaClient) ReadPacket() (*miHomeMissPacket, error) {
	return nil, io.EOF
}

func (*fakeRetryableMiHomeCameraMediaClient) SetDeadline(time.Time) error {
	return nil
}

func (*fakeRetryableMiHomeCameraMediaClient) Close() error {
	return nil
}

type sequencedMiHomeCameraMediaClient struct {
	packets []*miHomeMissPacket
	index   int
}

func (*sequencedMiHomeCameraMediaClient) StartMedia(string, string, string) error {
	return nil
}

func (*sequencedMiHomeCameraMediaClient) StopMedia() error {
	return nil
}

func (c *sequencedMiHomeCameraMediaClient) ReadPacket() (*miHomeMissPacket, error) {
	if c == nil || c.index >= len(c.packets) {
		return nil, io.EOF
	}
	packet := c.packets[c.index]
	c.index++
	return packet, nil
}

func (*sequencedMiHomeCameraMediaClient) SetDeadline(time.Time) error {
	return nil
}

func (*sequencedMiHomeCameraMediaClient) Close() error {
	return nil
}
