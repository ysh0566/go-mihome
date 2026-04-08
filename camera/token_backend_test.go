package camera

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	miot "github.com/ysh0566/go-mihome"
)

type capturePrintfLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *capturePrintfLogger) Printf(format string, args ...any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *capturePrintfLogger) String() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(append([]string(nil), l.lines...), "\n")
}

func TestNewTokenCameraSessionResolverBackendProbeUsesCloudClientAndSkipsMiHomeSession(t *testing.T) {
	previous := runMiHomePlutilJSON
	called := false
	runMiHomePlutilJSON = func(context.Context, string) ([]byte, error) {
		called = true
		return nil, errors.New("unexpected Mi Home plist access")
	}
	t.Cleanup(func() {
		runMiHomePlutilJSON = previous
	})

	backend := NewTokenCameraSessionResolverBackend(TokenCameraSessionResolverBackendOptions{
		Cloud: fakeTokenCameraCloudClient{
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
		},
		CallerUUID: "caller-1",
	}).(*tokenCameraSessionResolverBackend)
	backend.newMedia = func(string, miot.CameraVendorInfo, string, string, []byte, []byte) (miHomeCameraMediaClient, error) {
		return &sequencedMiHomeCameraMediaClient{
			packets: []*miHomeMissPacket{
				{CodecID: miHomeMissCodecH265, Payload: []byte{0x00, 0x00, 0x00, 0x01, 0x26, 0x01}},
			},
		}, nil
	}

	probe, err := backend.Probe(context.Background(), Target{
		CameraID: "camera-1",
		Model:    "chuangmi.camera.060a02",
	}, Profile{Name: "mijia.camera.family"})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if called {
		t.Fatal("Probe() accessed local Mi Home session path")
	}
	if got, want := probe.Codec, "h265"; got != want {
		t.Fatalf("probe.Codec = %q, want %q", got, want)
	}
}

func TestTokenCameraSessionResolverBackendOpenStreamsAccessUnitsWithoutMiHomeSession(t *testing.T) {
	previous := runMiHomePlutilJSON
	called := false
	runMiHomePlutilJSON = func(context.Context, string) ([]byte, error) {
		called = true
		return nil, errors.New("unexpected Mi Home plist access")
	}
	t.Cleanup(func() {
		runMiHomePlutilJSON = previous
	})

	payload := annexBPayload([][]byte{
		{0x67, 0x64, 0x00, 0x1f},
		{0x68, 0xeb, 0xe3, 0xcb, 0x22, 0xc0},
		{0x65, 0x88, 0x84},
	})

	backend := NewTokenCameraSessionResolverBackend(TokenCameraSessionResolverBackendOptions{
		Cloud: fakeTokenCameraCloudClient{
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
		},
		CallerUUID: "caller-1",
	}).(*tokenCameraSessionResolverBackend)
	backend.newMedia = func(string, miot.CameraVendorInfo, string, string, []byte, []byte) (miHomeCameraMediaClient, error) {
		return &sequencedMiHomeCameraMediaClient{
			packets: []*miHomeMissPacket{
				{CodecID: miHomeMissCodecH264, Timestamp: 250, Payload: payload},
			},
		}, nil
	}

	stream, err := backend.Open(context.Background(), SessionResolverStreamRequest{
		CameraID: "camera-1",
		Model:    "chuangmi.camera.060a02",
		Codec:    "h264",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	unit, err := stream.Recv(context.Background())
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	if called {
		t.Fatal("Open()/Recv() accessed local Mi Home session path")
	}
	if got, want := unit.Codec, "h264"; got != want {
		t.Fatalf("unit.Codec = %q, want %q", got, want)
	}
	if got, want := unit.PresentationTime, 250*time.Millisecond; got != want {
		t.Fatalf("unit.PresentationTime = %s, want %s", got, want)
	}
	if len(unit.Payload) == 0 {
		t.Fatal("unit.Payload = empty, want Annex-B access unit")
	}
}

func TestTokenCameraSessionResolverBackendOpenExplainsRejectedBootstrapEndpoint(t *testing.T) {
	backend := NewTokenCameraSessionResolverBackend(TokenCameraSessionResolverBackendOptions{
		Cloud: fakeTokenCameraCloudClient{
			device: miot.DeviceInfo{
				DID:     "camera-1",
				Model:   "chuangmi.camera.060a02",
				LocalIP: "192.168.31.75",
			},
			vendorErr: &miot.Error{
				Code: miot.ErrTransportFailure,
				Op:   "cloud request",
				Msg:  "status 204",
			},
		},
		CallerUUID: "caller-1",
	})

	_, err := backend.Open(context.Background(), SessionResolverStreamRequest{
		CameraID: "camera-1",
		Model:    "chuangmi.camera.060a02",
		Codec:    "h264",
	})
	if err == nil {
		t.Fatal("Open() error = nil, want unsupported token bootstrap error")
	}
	if got := err.Error(); !strings.Contains(got, "verify MIOT_CLIENT_ID and MIOT_CLOUD_SERVER") {
		t.Fatalf("Open() error = %q, want region/client guidance", got)
	}
}

func TestTokenCameraSessionResolverBackendOpenRequiresLocalIP(t *testing.T) {
	backend := NewTokenCameraSessionResolverBackend(TokenCameraSessionResolverBackendOptions{
		Cloud: fakeTokenCameraCloudClient{
			device: miot.DeviceInfo{
				DID:   "camera-1",
				Model: "chuangmi.camera.060a02",
			},
			vendor: miot.CameraVendorInfo{
				SupportVendor: "cs2",
				Sign:          "vendor-sign",
				PublicKey:     "vendor-public",
			},
		},
		CallerUUID: "caller-1",
	})

	_, err := backend.Open(context.Background(), SessionResolverStreamRequest{
		CameraID: "camera-1",
		Model:    "chuangmi.camera.060a02",
		Codec:    "h264",
	})
	if err == nil {
		t.Fatal("Open() error = nil, want missing local_ip failure")
	}
	if got := err.Error(); !strings.Contains(got, "strict token-local preview requires a LAN local_ip") {
		t.Fatalf("Open() error = %q, want strict local_ip guidance", got)
	}
}

func TestTokenCameraSessionResolverBackendOpenDoesNotScanSubnetWhenDisabled(t *testing.T) {
	backend := NewTokenCameraSessionResolverBackend(TokenCameraSessionResolverBackendOptions{
		Cloud: fakeTokenCameraCloudClient{
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
		},
		CallerUUID:  "caller-1",
		MaxAttempts: 1,
		DisableScan: true,
	}).(*tokenCameraSessionResolverBackend)

	var openCalls int
	backend.newMedia = func(string, miot.CameraVendorInfo, string, string, []byte, []byte) (miHomeCameraMediaClient, error) {
		openCalls++
		return nil, errors.New("dial timeout")
	}

	_, err := backend.Open(context.Background(), SessionResolverStreamRequest{
		CameraID: "camera-1",
		Model:    "chuangmi.camera.060a02",
		Codec:    "h264",
	})
	if err == nil {
		t.Fatal("Open() error = nil, want direct connect failure")
	}
	if got, want := openCalls, 1; got != want {
		t.Fatalf("newMedia calls = %d, want %d", got, want)
	}
}

func TestTokenCameraSessionResolverBackendOpenExplainsMissingPPCSConnect(t *testing.T) {
	backend := NewTokenCameraSessionResolverBackend(TokenCameraSessionResolverBackendOptions{
		Cloud: fakeTokenCameraCloudClient{
			device: miot.DeviceInfo{
				DID:     "camera-1",
				Model:   "chuangmi.camera.060a02",
				LocalIP: "192.168.31.75",
			},
			vendor: miot.CameraVendorInfo{
				SupportVendor: "cs2",
				Sign:          "vendor-sign",
				PublicKey:     "vendor-public",
				P2PID:         "p2p-id",
				InitString:    "init-string",
			},
		},
		CallerUUID:  "caller-1",
		MaxAttempts: 1,
		DisableScan: true,
	}).(*tokenCameraSessionResolverBackend)
	backend.newMedia = func(string, miot.CameraVendorInfo, string, string, []byte, []byte) (miHomeCameraMediaClient, error) {
		return nil, errors.New("dial timeout")
	}

	_, err := backend.Open(context.Background(), SessionResolverStreamRequest{
		CameraID: "camera-1",
		Model:    "chuangmi.camera.060a02",
		Codec:    "h264",
	})
	if err == nil {
		t.Fatal("Open() error = nil, want PPCS guidance")
	}
	if got := err.Error(); !strings.Contains(got, "p2p_id/init_string") {
		t.Fatalf("Open() error = %q, want vendor bootstrap context", got)
	}
	if got := err.Error(); !strings.Contains(got, "PPCS-style connect") {
		t.Fatalf("Open() error = %q, want PPCS guidance", got)
	}
}

func TestTokenCameraSessionResolverBackendOpenLogsConnectionStages(t *testing.T) {
	logger := &capturePrintfLogger{}
	backend := NewTokenCameraSessionResolverBackend(TokenCameraSessionResolverBackendOptions{
		Cloud: fakeTokenCameraCloudClient{
			device: miot.DeviceInfo{
				DID:     "camera-1",
				Model:   "chuangmi.camera.060a02",
				LocalIP: "192.168.31.75",
			},
			vendor: miot.CameraVendorInfo{
				SupportVendor: "cs2",
				VendorID:      4,
				Sign:          "vendor-sign",
				PublicKey:     "vendor-public",
				P2PID:         "p2p-id",
				InitString:    "init-string",
			},
		},
		CallerUUID:  "caller-1",
		MaxAttempts: 1,
		DisableScan: true,
		Logger:      logger,
	}).(*tokenCameraSessionResolverBackend)
	backend.newMedia = func(string, miot.CameraVendorInfo, string, string, []byte, []byte) (miHomeCameraMediaClient, error) {
		return &sequencedMiHomeCameraMediaClient{}, nil
	}

	stream, err := backend.Open(context.Background(), SessionResolverStreamRequest{
		CameraID: "camera-1",
		Model:    "chuangmi.camera.060a02",
		Codec:    "h264",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	output := logger.String()
	for _, pattern := range []string{
		"camera_id=camera-1 stage=token_get_device",
		"camera_id=camera-1 stage=token_device_resolved",
		"camera_id=camera-1 stage=token_get_vendor_bootstrap",
		"camera_id=camera-1 stage=token_vendor_resolved",
		"camera_id=camera-1 stage=local_media_connect attempt=1 host=192.168.31.75",
		"camera_id=camera-1 stage=local_media_started attempt=1 host=192.168.31.75",
	} {
		if !strings.Contains(output, pattern) {
			t.Fatalf("logger output missing %q:\n%s", pattern, output)
		}
	}
}

func TestTokenCameraSessionResolverBackendOpenUsesRequestedVideoQuality(t *testing.T) {
	backend := NewTokenCameraSessionResolverBackend(TokenCameraSessionResolverBackendOptions{
		Cloud: fakeTokenCameraCloudClient{
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
		},
		CallerUUID: "caller-1",
	}).(*tokenCameraSessionResolverBackend)

	var gotQuality string
	backend.newMedia = func(string, miot.CameraVendorInfo, string, string, []byte, []byte) (miHomeCameraMediaClient, error) {
		return &fakeRetryableMiHomeCameraMediaClient{
			startMedia: func(_ string, quality string, _ string) error {
				gotQuality = quality
				return nil
			},
		}, nil
	}

	stream, err := backend.Open(context.Background(), SessionResolverStreamRequest{
		CameraID:     "camera-1",
		Model:        "chuangmi.camera.060a02",
		Codec:        "h264",
		VideoQuality: VideoQualityLow,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	if got, want := gotQuality, "sd"; got != want {
		t.Fatalf("StartMedia() quality = %q, want %q", got, want)
	}
}

type fakeTokenCameraCloudClient struct {
	device      miot.DeviceInfo
	deviceErr   error
	vendor      miot.CameraVendorInfo
	vendorErr   error
	security    miot.CameraVendorSecurity
	securityErr error
}

func (f fakeTokenCameraCloudClient) GetDevice(context.Context, string) (miot.DeviceInfo, error) {
	if f.deviceErr != nil {
		return miot.DeviceInfo{}, f.deviceErr
	}
	return f.device, nil
}

func (f fakeTokenCameraCloudClient) GetCameraVendor(context.Context, string, []byte, string, string) (miot.CameraVendorInfo, error) {
	if f.vendorErr != nil {
		return miot.CameraVendorInfo{}, f.vendorErr
	}
	return f.vendor, nil
}

func (f fakeTokenCameraCloudClient) GetCameraVendorSecurity(context.Context, string) (miot.CameraVendorSecurity, error) {
	if f.securityErr != nil {
		return miot.CameraVendorSecurity{}, f.securityErr
	}
	return f.security, nil
}
