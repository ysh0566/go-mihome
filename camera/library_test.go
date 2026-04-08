package camera

import (
	"context"
	"testing"
	"time"
)

type libraryFakeDriverFactory struct {
	drivers []*libraryFakeDriver
}

func (f *libraryFakeDriverFactory) New(info Info) Driver {
	driver := &libraryFakeDriver{info: info}
	f.drivers = append(f.drivers, driver)
	return driver
}

func (f *libraryFakeDriverFactory) latest() *libraryFakeDriver {
	if len(f.drivers) == 0 {
		return nil
	}
	return f.drivers[len(f.drivers)-1]
}

type libraryFakeDriver struct {
	info    Info
	options StartOptions
	sink    EventSink
	started bool
	stopped bool
}

func (d *libraryFakeDriver) Start(_ context.Context, options StartOptions, sink EventSink) error {
	d.options = options
	d.sink = sink
	d.started = true
	return nil
}

func (d *libraryFakeDriver) Stop() error {
	d.stopped = true
	return nil
}

func TestNewLibraryCreateCameraAndVersion(t *testing.T) {
	factory := &libraryFakeDriverFactory{}
	lib, err := NewLibrary(LibraryOptions{
		AccessToken:   "access-token",
		DriverFactory: factory,
	})
	if err != nil {
		t.Fatalf("NewLibrary() error = %v", err)
	}
	defer func() {
		if err := lib.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	if got := lib.Version(); got == "" {
		t.Fatalf("Version() returned empty string")
	}

	camera, err := lib.NewCamera(Info{
		CameraID:     "camera-1",
		Model:        "chuangmi.camera.046c04",
		ChannelCount: 1,
	})
	if err != nil {
		t.Fatalf("NewCamera() error = %v", err)
	}
	if got, want := camera.Info().CameraID, "camera-1"; got != want {
		t.Fatalf("camera.Info().CameraID = %q, want %q", got, want)
	}

	second, err := lib.NewCamera(Info{
		CameraID:     "camera-1",
		Model:        "chuangmi.camera.046c04",
		ChannelCount: 1,
	})
	if err != nil {
		t.Fatalf("NewCamera() second call error = %v", err)
	}
	if camera != second {
		t.Fatalf("NewCamera() did not reuse existing camera instance")
	}
}

func TestNewLibraryRequiresClientIDForDefaultFactory(t *testing.T) {
	t.Parallel()

	_, err := NewLibrary(LibraryOptions{})
	if err == nil {
		t.Fatal("NewLibrary() error = nil, want client id validation")
	}
}

func TestLibraryUpdateAccessTokenAffectsNewCamera(t *testing.T) {
	factory := &libraryFakeDriverFactory{}
	lib, err := NewLibrary(LibraryOptions{
		AccessToken:   "old-token",
		DriverFactory: factory,
	})
	if err != nil {
		t.Fatalf("NewLibrary() error = %v", err)
	}
	defer func() {
		if err := lib.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	if err := lib.UpdateAccessToken("new-token"); err != nil {
		t.Fatalf("UpdateAccessToken() error = %v", err)
	}

	camera, err := lib.NewCamera(Info{
		CameraID: "camera-2",
		Model:    "chuangmi.camera.046c04",
	})
	if err != nil {
		t.Fatalf("NewCamera() error = %v", err)
	}
	if got, want := camera.Info().Token, "new-token"; got != want {
		t.Fatalf("camera.Info().Token = %q, want %q", got, want)
	}
}

func TestLibraryCloseStopsManagedCameras(t *testing.T) {
	factory := &libraryFakeDriverFactory{}
	lib, err := NewLibrary(LibraryOptions{
		AccessToken:   "access-token",
		DriverFactory: factory,
	})
	if err != nil {
		t.Fatalf("NewLibrary() error = %v", err)
	}

	camera, err := lib.NewCamera(Info{
		CameraID: "camera-3",
		Model:    "chuangmi.camera.046c04",
	})
	if err != nil {
		t.Fatalf("NewCamera() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := camera.Start(ctx, StartOptions{}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	driver := factory.latest()
	if driver == nil || !driver.started {
		t.Fatal("fake driver was not started")
	}

	if err := lib.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !driver.stopped {
		t.Fatal("Close() did not stop the managed camera")
	}
}
