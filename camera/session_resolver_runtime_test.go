package camera

import "testing"

func TestNewCameraSessionResolverAppUsesRuntimeBackendByDefault(t *testing.T) {
	backend := fakeSessionResolverBackend{}
	previous := newRuntimeCameraSessionResolverBackend
	newRuntimeCameraSessionResolverBackend = func(any) SessionResolverBackend {
		return backend
	}
	t.Cleanup(func() {
		newRuntimeCameraSessionResolverBackend = previous
	})

	app := NewCameraSessionResolverApp(CameraSessionResolverAppOptions{})
	if app == nil {
		t.Fatal("NewCameraSessionResolverApp() = nil")
	}
	if app.backend == nil {
		t.Fatal("app.backend = nil, want runtime backend")
	}
}
