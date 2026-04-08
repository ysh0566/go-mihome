package camera

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestRuntimeCreateNormalizesInfoAndReusesInstance(t *testing.T) {
	t.Parallel()

	runtime := NewRuntime(RuntimeOptions{
		Factory: fakeDriverFactory{
			newDriver: func(Info) Driver { return fakeDriver{} },
		},
	})

	first, err := runtime.Create(Info{
		CameraID:     " camera-1 ",
		Model:        " xiaomi.camera.demo ",
		ChannelCount: 0,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	second, err := runtime.Create(Info{
		CameraID: "camera-1",
		Model:    "xiaomi.camera.demo",
	})
	if err != nil {
		t.Fatalf("Create() second error = %v", err)
	}

	if first != second {
		t.Fatal("Create() returned different instances, want reused instance")
	}
	info := first.Info()
	if got, want := info.CameraID, "camera-1"; got != want {
		t.Fatalf("info.CameraID = %q, want %q", got, want)
	}
	if got, want := info.Model, "xiaomi.camera.demo"; got != want {
		t.Fatalf("info.Model = %q, want %q", got, want)
	}
	if got, want := info.ChannelCount, 1; got != want {
		t.Fatalf("info.ChannelCount = %d, want %d", got, want)
	}
	if got := runtime.Get("camera-1"); got != first {
		t.Fatalf("Get() = %p, want %p", got, first)
	}
}

func TestRuntimeCreateRejectsBlankCameraIDOrModel(t *testing.T) {
	t.Parallel()

	runtime := NewRuntime(RuntimeOptions{
		Factory: fakeDriverFactory{
			newDriver: func(Info) Driver { return fakeDriver{} },
		},
	})

	cases := []Info{
		{CameraID: "", Model: "xiaomi.camera.demo"},
		{CameraID: "camera-1", Model: ""},
	}
	for _, info := range cases {
		_, err := runtime.Create(info)
		if err == nil {
			t.Fatalf("Create(%+v) error = nil, want validation error", info)
		}
	}
}

func TestInstanceStartValidatesPincodeRequirement(t *testing.T) {
	t.Parallel()

	runtime := NewRuntime(RuntimeOptions{
		Factory: fakeDriverFactory{
			newDriver: func(Info) Driver { return fakeDriver{} },
		},
	})
	camera, err := runtime.Create(Info{
		CameraID:        "camera-1",
		Model:           "xiaomi.camera.demo",
		ChannelCount:    1,
		PincodeRequired: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	err = camera.Start(context.Background(), StartOptions{})
	if err == nil {
		t.Fatal("Start() error = nil, want pincode validation error")
	}
}

func TestInstanceStartRejectsMalformedPincode(t *testing.T) {
	t.Parallel()

	runtime := NewRuntime(RuntimeOptions{
		Factory: fakeDriverFactory{
			newDriver: func(Info) Driver { return fakeDriver{} },
		},
	})
	camera, err := runtime.Create(Info{
		CameraID:     "camera-1",
		Model:        "xiaomi.camera.demo",
		ChannelCount: 1,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	err = camera.Start(context.Background(), StartOptions{PinCode: "12"})
	if err == nil {
		t.Fatal("Start() error = nil, want malformed pincode error")
	}
}

func TestRuntimeDispatchesStatusAndJPEGCallbacksByChannel(t *testing.T) {
	t.Parallel()

	driver := &fakeDriver{
		start: func(_ context.Context, options StartOptions, sink EventSink) error {
			if got, want := len(options.VideoQualities), 2; got != want {
				t.Fatalf("len(VideoQualities) = %d, want %d", got, want)
			}
			if got, want := options.VideoQualities[0], VideoQualityHigh; got != want {
				t.Fatalf("VideoQualities[0] = %d, want %d", got, want)
			}
			if got, want := options.VideoQualities[1], VideoQualityHigh; got != want {
				t.Fatalf("VideoQualities[1] = %d, want %d", got, want)
			}
			sink.UpdateStatus(StatusConnected)
			sink.EmitRawVideo(Frame{Channel: 0, Sequence: 1, Data: []byte{0x01}})
			sink.EmitRawAudio(Frame{Channel: 1, Sequence: 2, Data: []byte{0x02}})
			sink.EmitJPEG(JPEGFrame{Channel: 0, Payload: []byte("jpeg-0")})
			sink.EmitPCM(DecodedFrame{Channel: 1, Data: []byte("pcm-1")})
			return nil
		},
	}
	runtime := NewRuntime(RuntimeOptions{
		Factory: fakeDriverFactory{
			newDriver: func(Info) Driver { return driver },
		},
	})
	camera, err := runtime.Create(Info{
		CameraID:     "camera-1",
		Model:        "xiaomi.camera.demo",
		ChannelCount: 2,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var statuses []Status
	var rawVideoCount int
	var rawAudioCount int
	var jpegPayloads [][]byte
	var decodedJPEGCount int
	var pcmCount int
	camera.RegisterStatusChanged(func(did string, status Status) {
		if got, want := did, "camera-1"; got != want {
			t.Fatalf("did = %q, want %q", got, want)
		}
		statuses = append(statuses, status)
	})
	camera.RegisterRawVideo(0, func(_ string, frame Frame) {
		rawVideoCount++
		if got, want := frame.Sequence, uint32(1); got != want {
			t.Fatalf("frame.Sequence = %d, want %d", got, want)
		}
	})
	camera.RegisterRawAudio(1, func(_ string, frame Frame) {
		rawAudioCount++
		if got, want := frame.Sequence, uint32(2); got != want {
			t.Fatalf("frame.Sequence = %d, want %d", got, want)
		}
	})
	camera.RegisterJPEG(0, func(_ string, frame JPEGFrame) {
		jpegPayloads = append(jpegPayloads, append([]byte(nil), frame.Payload...))
	})
	camera.RegisterDecodedJPEG(0, func(_ string, frame DecodedFrame) {
		decodedJPEGCount++
		if !bytes.Equal(frame.Data, []byte("jpeg-0")) {
			t.Fatalf("frame.Data = %q, want %q", frame.Data, []byte("jpeg-0"))
		}
	})
	camera.RegisterDecodedPCM(1, func(_ string, frame DecodedFrame) {
		pcmCount++
		if !bytes.Equal(frame.Data, []byte("pcm-1")) {
			t.Fatalf("frame.Data = %q, want %q", frame.Data, []byte("pcm-1"))
		}
	})

	if err := camera.Start(context.Background(), StartOptions{
		PinCode:        "1234",
		VideoQualities: []VideoQuality{VideoQualityHigh},
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got, want := statuses, []Status{StatusConnecting, StatusConnected}; !equalStatuses(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	if got, want := rawVideoCount, 1; got != want {
		t.Fatalf("rawVideoCount = %d, want %d", got, want)
	}
	if got, want := rawAudioCount, 1; got != want {
		t.Fatalf("rawAudioCount = %d, want %d", got, want)
	}
	if got, want := len(jpegPayloads), 1; got != want {
		t.Fatalf("len(jpegPayloads) = %d, want %d", got, want)
	}
	if !bytes.Equal(jpegPayloads[0], []byte("jpeg-0")) {
		t.Fatalf("jpegPayloads[0] = %q, want %q", jpegPayloads[0], []byte("jpeg-0"))
	}
	if got, want := decodedJPEGCount, 1; got != want {
		t.Fatalf("decodedJPEGCount = %d, want %d", got, want)
	}
	if got, want := pcmCount, 1; got != want {
		t.Fatalf("pcmCount = %d, want %d", got, want)
	}
}

func TestInstanceStartSetsErrorStatusWhenDriverFails(t *testing.T) {
	t.Parallel()

	runtime := NewRuntime(RuntimeOptions{
		Factory: fakeDriverFactory{
			newDriver: func(Info) Driver {
				return fakeDriver{
					start: func(context.Context, StartOptions, EventSink) error {
						return errors.New("boom")
					},
				}
			},
		},
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
	camera.RegisterStatusChanged(func(_ string, status Status) {
		statuses = append(statuses, status)
	})

	err = camera.Start(context.Background(), StartOptions{})
	if err == nil {
		t.Fatal("Start() error = nil, want driver error")
	}
	if got, want := statuses, []Status{StatusConnecting, StatusError}; !equalStatuses(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
}

func TestRuntimeDestroyStopsInstanceAndRemovesIt(t *testing.T) {
	t.Parallel()

	stopCalls := 0
	runtime := NewRuntime(RuntimeOptions{
		Factory: fakeDriverFactory{
			newDriver: func(Info) Driver {
				return fakeDriver{
					stop: func() error {
						stopCalls++
						return nil
					},
				}
			},
		},
	})
	camera, err := runtime.Create(Info{
		CameraID:     "camera-1",
		Model:        "xiaomi.camera.demo",
		ChannelCount: 1,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	camera.UpdateStatus(StatusConnected)
	if err := runtime.Destroy("camera-1"); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if got, want := stopCalls, 1; got != want {
		t.Fatalf("stopCalls = %d, want %d", got, want)
	}
	if got := runtime.Get("camera-1"); got != nil {
		t.Fatalf("Get() after Destroy = %v, want nil", got)
	}
	if got, want := camera.Status(), StatusDisconnected; got != want {
		t.Fatalf("Status() after Destroy = %v, want %v", got, want)
	}
}

type fakeDriverFactory struct {
	newDriver func(Info) Driver
}

func (f fakeDriverFactory) New(info Info) Driver {
	return f.newDriver(info)
}

type fakeDriver struct {
	start func(context.Context, StartOptions, EventSink) error
	stop  func() error
}

func (f fakeDriver) Start(ctx context.Context, options StartOptions, sink EventSink) error {
	if f.start == nil {
		return nil
	}
	return f.start(ctx, options, sink)
}

func (f fakeDriver) Stop() error {
	if f.stop == nil {
		return nil
	}
	return f.stop()
}

func equalStatuses(got []Status, want []Status) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
