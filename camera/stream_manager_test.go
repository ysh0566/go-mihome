package camera

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestCameraWorkerRetriesAfterTransientStartFailure(t *testing.T) {
	wantErr := errors.New("transient start failure")
	starts := 0
	worker := &cameraWorker{
		desc: CameraStreamDescriptor{
			CameraID: "camera-1",
			Model:    "xiaomi.camera.v1",
		},
		factory: cameraPipelineFactoryFunc(func(_ CameraStreamDescriptor, onJPEG func(JPEGFrame)) (io.Closer, error) {
			starts++
			if starts == 1 {
				return nil, wantErr
			}
			onJPEG(JPEGFrame{Payload: append([]byte(nil), testJPEGFrame...)})
			return nopCameraPipelineCloser{}, nil
		}),
	}

	if _, err := worker.GetSnapshot(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("first GetSnapshot() error = %v, want %v", err, wantErr)
	}

	snapshot, err := worker.GetSnapshot(context.Background())
	if err != nil {
		t.Fatalf("second GetSnapshot() error = %v", err)
	}
	if starts != 2 {
		t.Fatalf("factory starts = %d, want 2", starts)
	}
	if !bytes.Equal(snapshot.Payload, testJPEGFrame) {
		t.Fatalf("snapshot bytes = %x, want %x", snapshot.Payload, testJPEGFrame)
	}
}

func TestCameraWorkerRestartsWhenCachedSnapshotIsStale(t *testing.T) {
	starts := 0
	worker := &cameraWorker{
		desc: CameraStreamDescriptor{
			CameraID: "camera-1",
			Model:    "xiaomi.camera.v1",
		},
		factory: cameraPipelineFactoryFunc(func(_ CameraStreamDescriptor, onJPEG func(JPEGFrame)) (io.Closer, error) {
			starts++
			onJPEG(JPEGFrame{Payload: append([]byte(nil), testJPEGFrame...)})
			return nopCameraPipelineCloser{}, nil
		}),
		closer: nopCameraPipelineCloser{},
		snapshot: JPEGFrame{
			Payload:    []byte("stale"),
			CapturedAt: time.Now().Add(-(defaultCameraSnapshotWaitTimeout + time.Second)).UTC(),
		},
		hasFrame: true,
	}
	worker.startOnce.Do(func() {})

	snapshot, err := worker.GetSnapshot(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshot() error = %v", err)
	}
	if starts != 1 {
		t.Fatalf("factory starts = %d, want 1 restart", starts)
	}
	if !bytes.Equal(snapshot.Payload, testJPEGFrame) {
		t.Fatalf("snapshot bytes = %x, want %x", snapshot.Payload, testJPEGFrame)
	}
}

func TestCameraWorkerRetriesOnceAfterFrameTimeout(t *testing.T) {
	starts := 0
	worker := &cameraWorker{
		desc: CameraStreamDescriptor{
			CameraID: "camera-1",
			Model:    "xiaomi.camera.v1",
		},
		factory: cameraPipelineFactoryFunc(func(_ CameraStreamDescriptor, onJPEG func(JPEGFrame)) (io.Closer, error) {
			starts++
			if starts == 2 {
				onJPEG(JPEGFrame{Payload: append([]byte(nil), testJPEGFrame...)})
			}
			return nopCameraPipelineCloser{}, nil
		}),
		frameTimeout: 10 * time.Millisecond,
	}

	snapshot, err := worker.GetSnapshot(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshot() error = %v", err)
	}
	if starts != 2 {
		t.Fatalf("factory starts = %d, want 2", starts)
	}
	if !bytes.Equal(snapshot.Payload, testJPEGFrame) {
		t.Fatalf("snapshot bytes = %x, want %x", snapshot.Payload, testJPEGFrame)
	}
}

func TestCameraWorkerRetriesBridgeUnavailableWithinSingleSnapshotCall(t *testing.T) {
	starts := 0
	worker := &cameraWorker{
		desc: CameraStreamDescriptor{
			CameraID: "camera-1",
			Model:    "xiaomi.camera.v1",
		},
		factory: cameraPipelineFactoryFunc(func(_ CameraStreamDescriptor, onJPEG func(JPEGFrame)) (io.Closer, error) {
			starts++
			if starts == 1 {
				return nil, ErrCameraBridgeUnavailable
			}
			onJPEG(JPEGFrame{Payload: append([]byte(nil), testJPEGFrame...)})
			return nopCameraPipelineCloser{}, nil
		}),
	}

	snapshot, err := worker.GetSnapshot(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshot() error = %v", err)
	}
	if starts != 2 {
		t.Fatalf("factory starts = %d, want 2", starts)
	}
	if !bytes.Equal(snapshot.Payload, testJPEGFrame) {
		t.Fatalf("snapshot bytes = %x, want %x", snapshot.Payload, testJPEGFrame)
	}
}

func TestCameraStreamManagerRestartsWhenVideoQualityChanges(t *testing.T) {
	var started []VideoQuality
	manager := NewCameraStreamManager(CameraStreamManagerOptions{
		Factory: cameraPipelineFactoryFunc(func(desc CameraStreamDescriptor, onJPEG func(JPEGFrame)) (io.Closer, error) {
			started = append(started, desc.VideoQuality)
			payload := []byte("low")
			if desc.VideoQuality == VideoQualityHigh {
				payload = []byte("high")
			}
			onJPEG(JPEGFrame{Payload: payload})
			return nopCameraPipelineCloser{}, nil
		}),
	})

	lowSnapshot, err := manager.GetSnapshot(context.Background(), CameraStreamDescriptor{
		CameraID:     "camera-1",
		Model:        "xiaomi.camera.v1",
		VideoQuality: VideoQualityLow,
	})
	if err != nil {
		t.Fatalf("first GetSnapshot() error = %v", err)
	}
	if !bytes.Equal(lowSnapshot.Payload, []byte("low")) {
		t.Fatalf("first snapshot bytes = %q, want low", lowSnapshot.Payload)
	}

	highSnapshot, err := manager.GetSnapshot(context.Background(), CameraStreamDescriptor{
		CameraID:     "camera-1",
		Model:        "xiaomi.camera.v1",
		VideoQuality: VideoQualityHigh,
	})
	if err != nil {
		t.Fatalf("second GetSnapshot() error = %v", err)
	}
	if !bytes.Equal(highSnapshot.Payload, []byte("high")) {
		t.Fatalf("second snapshot bytes = %q, want high", highSnapshot.Payload)
	}
	if got, want := started, []VideoQuality{VideoQualityLow, VideoQualityHigh}; !equalVideoQualities(got, want) {
		t.Fatalf("started qualities = %v, want %v", got, want)
	}
}

type cameraPipelineFactoryFunc func(CameraStreamDescriptor, func(JPEGFrame)) (io.Closer, error)

func (f cameraPipelineFactoryFunc) Start(desc CameraStreamDescriptor, onJPEG func(JPEGFrame)) (io.Closer, error) {
	return f(desc, onJPEG)
}

type nopCameraPipelineCloser struct{}

func (nopCameraPipelineCloser) Close() error {
	return nil
}

func equalVideoQualities(a []VideoQuality, b []VideoQuality) bool {
	if len(a) != len(b) {
		return false
	}
	for idx := range a {
		if a[idx] != b[idx] {
			return false
		}
	}
	return true
}
