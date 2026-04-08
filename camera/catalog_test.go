package camera

import (
	"context"
	"errors"
	"testing"

	miot "github.com/ysh0566/go-mihome"
)

func TestCatalogFromSnapshotListsSupportedCamerasInStableOrder(t *testing.T) {
	t.Parallel()

	catalog := NewCatalogFromSnapshot(miot.DeviceSnapshot{
		Devices: map[string]miot.DeviceInfo{
			"camera-2": {
				DID:      "camera-2",
				Name:     "Backyard",
				Model:    "chuangmi.camera.068ac1",
				HomeID:   "home-1",
				HomeName: "Main Home",
				RoomID:   "room-2",
				RoomName: "Garden",
				Online:   false,
			},
			"light-1": {
				DID:   "light-1",
				Name:  "Ceiling",
				Model: "yeelight.light.ceiling1",
			},
			"camera-1": {
				DID:      "camera-1",
				Name:     "Front Door",
				Model:    "xiaomi.camera.082ac1",
				HomeID:   "home-1",
				HomeName: "Main Home",
				RoomID:   "room-1",
				RoomName: "Entry",
				Online:   true,
			},
		},
	})

	targets, err := catalog.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got, want := len(targets), 2; got != want {
		t.Fatalf("len(targets) = %d, want %d", got, want)
	}
	if got, want := targets[0].CameraID, "camera-1"; got != want {
		t.Fatalf("targets[0].CameraID = %q, want %q", got, want)
	}
	if got, want := targets[0].SupportInfo.ChannelCount, 2; got != want {
		t.Fatalf("targets[0].SupportInfo.ChannelCount = %d, want %d", got, want)
	}
	if got, want := targets[1].CameraID, "camera-2"; got != want {
		t.Fatalf("targets[1].CameraID = %q, want %q", got, want)
	}
	if got, want := targets[1].SupportInfo.Vendor, "上海创米科技"; got != want {
		t.Fatalf("targets[1].SupportInfo.Vendor = %q, want %q", got, want)
	}
}

func TestCatalogRejectsMissingOrBlankCameraID(t *testing.T) {
	t.Parallel()

	catalog := NewCatalogFromSnapshot(miot.DeviceSnapshot{})
	for _, input := range []string{"", "   "} {
		_, err := catalog.Select(context.Background(), input)
		if !errors.Is(err, ErrInvalidCameraID) {
			t.Fatalf("Select(%q) error = %v, want ErrInvalidCameraID", input, err)
		}
	}
}

func TestCatalogReportsMissingLoaderExplicitly(t *testing.T) {
	t.Parallel()

	var catalog Catalog
	_, err := catalog.List(context.Background())
	if !errors.Is(err, ErrCatalogUnavailable) {
		t.Fatalf("List() error = %v, want ErrCatalogUnavailable", err)
	}
}

func TestCatalogReportsNotFoundWithContext(t *testing.T) {
	t.Parallel()

	catalog := NewCatalogFromSnapshot(miot.DeviceSnapshot{
		Devices: map[string]miot.DeviceInfo{
			"camera-1": {
				DID:   "camera-1",
				Model: "xiaomi.camera.082ac1",
			},
		},
	})

	_, err := catalog.Select(context.Background(), "missing-camera")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Select() error = %v, want ErrNotFound", err)
	}
	if got := err.Error(); got == ErrNotFound.Error() {
		t.Fatalf("Select() error = %q, want contextual message", got)
	}
}
