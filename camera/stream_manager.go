package camera

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

var (
	// ErrCameraBridgeUnavailable reports that the configured snapshot bridge cannot be used.
	ErrCameraBridgeUnavailable = errors.New("miot camera bridge unavailable")
	// ErrCameraFrameUnavailable reports that no JPEG frame became available in time.
	ErrCameraFrameUnavailable = errors.New("miot camera frame unavailable")
)

const defaultCameraSnapshotWaitTimeout = 15 * time.Second

// CameraStreamDescriptor describes one managed camera stream worker.
type CameraStreamDescriptor struct {
	CameraID     string
	Model        string
	Region       string
	AccessToken  string
	ChannelCount int
	VideoQuality VideoQuality
}

// CameraStreamManagerOptions configures a reusable camera stream manager.
type CameraStreamManagerOptions struct {
	FFmpegPath string
	Logger     any
	Factory    cameraPipelineFactory
}

// CameraStreamManager reuses one JPEG-producing pipeline per camera.
type CameraStreamManager struct {
	mu      sync.Mutex
	workers map[string]*cameraWorker
	factory cameraPipelineFactory
}

type cameraPipelineFactory interface {
	Start(CameraStreamDescriptor, func(JPEGFrame)) (io.Closer, error)
}

type unavailableCameraPipelineFactory struct{}

type cameraWorker struct {
	desc    CameraStreamDescriptor
	factory cameraPipelineFactory

	lifecycleMu sync.Mutex
	startOnce   sync.Once
	startErr    error

	mu           sync.Mutex
	closer       io.Closer
	snapshot     JPEGFrame
	hasFrame     bool
	waiters      []chan struct{}
	frameTimeout time.Duration
}

// NewCameraStreamManager constructs a reusable stream manager.
func NewCameraStreamManager(options CameraStreamManagerOptions) *CameraStreamManager {
	factory := options.Factory
	if factory == nil {
		factory = newDefaultCameraPipelineFactory(options.FFmpegPath, options.Logger)
	}
	return &CameraStreamManager{
		workers: map[string]*cameraWorker{},
		factory: factory,
	}
}

// GetSnapshot returns the latest available JPEG frame for one camera descriptor.
func (m *CameraStreamManager) GetSnapshot(ctx context.Context, desc CameraStreamDescriptor) (JPEGFrame, error) {
	if m == nil {
		return JPEGFrame{}, ErrCameraBridgeUnavailable
	}
	worker := m.ensureWorker(desc)
	return worker.GetSnapshot(ctx)
}

// CachedSnapshot returns the most recent cached JPEG frame for one camera identifier.
func (m *CameraStreamManager) CachedSnapshot(cameraID string) (JPEGFrame, bool) {
	if m == nil {
		return JPEGFrame{}, false
	}
	m.mu.Lock()
	worker := m.workers[strings.TrimSpace(cameraID)]
	m.mu.Unlock()
	if worker == nil {
		return JPEGFrame{}, false
	}
	return worker.lastSnapshot()
}

func (m *CameraStreamManager) ensureWorker(desc CameraStreamDescriptor) *cameraWorker {
	m.mu.Lock()
	defer m.mu.Unlock()

	desc.CameraID = strings.TrimSpace(desc.CameraID)
	desc.Model = strings.TrimSpace(desc.Model)
	desc.Region = strings.TrimSpace(desc.Region)
	desc.AccessToken = strings.TrimSpace(desc.AccessToken)
	if desc.ChannelCount <= 0 {
		desc.ChannelCount = 1
	}
	if worker, ok := m.workers[desc.CameraID]; ok {
		return worker
	}
	worker := &cameraWorker{
		desc:    desc,
		factory: m.factory,
	}
	m.workers[desc.CameraID] = worker
	return worker
}

func (w *cameraWorker) GetSnapshot(ctx context.Context) (JPEGFrame, error) {
	if w == nil {
		return JPEGFrame{}, ErrCameraBridgeUnavailable
	}
	const maxSnapshotAttempts = 2

	for attempt := 1; attempt <= maxSnapshotAttempts; attempt++ {
		if err := w.ensureStarted(); err != nil {
			if attempt < maxSnapshotAttempts && errors.Is(err, ErrCameraBridgeUnavailable) {
				w.restartPipeline(true)
				continue
			}
			return JPEGFrame{}, err
		}
		if snapshot, fresh := w.currentSnapshot(); fresh {
			return snapshot, nil
		}
		if w.snapshotExpired() {
			w.restartPipeline(true)
			continue
		}

		snapshot, err := w.waitForSnapshot(ctx)
		if err == nil {
			return snapshot, nil
		}
		if attempt == maxSnapshotAttempts || !errors.Is(err, ErrCameraFrameUnavailable) {
			return JPEGFrame{}, err
		}
	}

	return JPEGFrame{}, ErrCameraFrameUnavailable
}

func (w *cameraWorker) waitForSnapshot(ctx context.Context) (JPEGFrame, error) {
	w.mu.Lock()
	if w.hasFrame && !w.snapshotExpiredLocked() {
		snapshot := cloneJPEGFrame(w.snapshot)
		w.mu.Unlock()
		return snapshot, nil
	}
	waiter := make(chan struct{}, 1)
	w.waiters = append(w.waiters, waiter)
	w.mu.Unlock()

	timeout := time.NewTimer(w.snapshotWaitTimeout())
	defer timeout.Stop()

	select {
	case <-ctx.Done():
		w.removeWaiter(waiter)
		return JPEGFrame{}, ctx.Err()
	case <-timeout.C:
		w.removeWaiter(waiter)
		w.restartPipeline(true)
		return JPEGFrame{}, ErrCameraFrameUnavailable
	case <-waiter:
		w.mu.Lock()
		snapshot := cloneJPEGFrame(w.snapshot)
		hasFrame := w.hasFrame
		w.mu.Unlock()
		if !hasFrame || len(snapshot.Payload) == 0 {
			w.restartPipeline(true)
			return JPEGFrame{}, ErrCameraFrameUnavailable
		}
		return snapshot, nil
	}
}

func (w *cameraWorker) ensureStarted() error {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()

	if w.startErr != nil {
		w.startErr = nil
		w.startOnce = sync.Once{}
	}
	w.startOnce.Do(func() {
		closer, err := w.factory.Start(w.desc, w.onJPEG)
		if err != nil {
			w.startErr = err
			return
		}
		w.mu.Lock()
		w.closer = closer
		w.mu.Unlock()
	})
	return w.startErr
}

func (w *cameraWorker) restartPipeline(clearSnapshot bool) {
	if w == nil {
		return
	}
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()

	w.startErr = nil
	w.startOnce = sync.Once{}

	w.mu.Lock()
	closer := w.closer
	w.closer = nil
	if clearSnapshot {
		w.snapshot = JPEGFrame{}
		w.hasFrame = false
	}
	w.mu.Unlock()

	if closer != nil {
		_ = closer.Close()
	}
}

func (w *cameraWorker) onJPEG(frame JPEGFrame) {
	if len(frame.Payload) == 0 {
		return
	}
	if frame.CapturedAt.IsZero() {
		frame.CapturedAt = time.Now().UTC()
	}
	w.mu.Lock()
	w.snapshot = cloneJPEGFrame(frame)
	w.hasFrame = true
	waiters := append([]chan struct{}(nil), w.waiters...)
	w.waiters = nil
	w.mu.Unlock()

	for _, waiter := range waiters {
		select {
		case waiter <- struct{}{}:
		default:
		}
	}
}

func (w *cameraWorker) removeWaiter(waiter chan struct{}) {
	if w == nil || waiter == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	next := w.waiters[:0]
	for _, current := range w.waiters {
		if current == waiter {
			continue
		}
		next = append(next, current)
	}
	w.waiters = next
}

func (w *cameraWorker) currentSnapshot() (JPEGFrame, bool) {
	if w == nil {
		return JPEGFrame{}, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.hasFrame || w.snapshotExpiredLocked() {
		return JPEGFrame{}, false
	}
	return cloneJPEGFrame(w.snapshot), true
}

func (w *cameraWorker) lastSnapshot() (JPEGFrame, bool) {
	if w == nil {
		return JPEGFrame{}, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.hasFrame || len(w.snapshot.Payload) == 0 {
		return JPEGFrame{}, false
	}
	return cloneJPEGFrame(w.snapshot), true
}

func (w *cameraWorker) snapshotExpired() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.snapshotExpiredLocked()
}

func (w *cameraWorker) snapshotExpiredLocked() bool {
	if !w.hasFrame || len(w.snapshot.Payload) == 0 {
		return false
	}
	if w.snapshot.CapturedAt.IsZero() {
		return true
	}
	return time.Since(w.snapshot.CapturedAt) > w.snapshotWaitTimeout()
}

func (w *cameraWorker) snapshotWaitTimeout() time.Duration {
	if w != nil && w.frameTimeout > 0 {
		return w.frameTimeout
	}
	return defaultCameraSnapshotWaitTimeout
}

func (unavailableCameraPipelineFactory) Start(CameraStreamDescriptor, func(JPEGFrame)) (io.Closer, error) {
	return nil, ErrCameraBridgeUnavailable
}
