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

type cameraPipelineFactoryFunc func(CameraStreamDescriptor, func(JPEGFrame)) (io.Closer, error)

func (f cameraPipelineFactoryFunc) Start(desc CameraStreamDescriptor, onJPEG func(JPEGFrame)) (io.Closer, error) {
	return f(desc, onJPEG)
}

type nopCameraPipelineCloser struct{}

func (nopCameraPipelineCloser) Close() error {
	return nil
}
