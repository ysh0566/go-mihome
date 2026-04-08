package camera

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	miot "github.com/ysh0566/go-mihome"
)

var (
	// ErrCatalogUnavailable reports that no camera catalog source is configured.
	ErrCatalogUnavailable = errors.New("miot camera catalog unavailable")
	// ErrInvalidCameraID reports an empty or malformed camera identifier.
	ErrInvalidCameraID = errors.New("miot camera invalid camera id")
	// ErrNotFound reports that a requested camera target does not exist.
	ErrNotFound = errors.New("miot camera not found")
)

// Loader provides camera catalog data from the root MIoT device snapshot.
type Loader interface {
	Load(context.Context) (miot.DeviceSnapshot, error)
}

// LoaderFunc adapts a function to the Loader interface.
type LoaderFunc func(context.Context) (miot.DeviceSnapshot, error)

// Load executes the wrapped loader function.
func (f LoaderFunc) Load(ctx context.Context) (miot.DeviceSnapshot, error) {
	return f(ctx)
}

// Catalog normalizes root-package device snapshots into supported camera targets.
type Catalog struct {
	loader Loader
}

// NewCatalog constructs a camera catalog backed by a loader.
func NewCatalog(loader Loader) Catalog {
	return Catalog{loader: loader}
}

// NewCatalogFromSnapshot constructs a catalog backed by one static device snapshot.
func NewCatalogFromSnapshot(snapshot miot.DeviceSnapshot) Catalog {
	return NewCatalog(LoaderFunc(func(context.Context) (miot.DeviceSnapshot, error) {
		return snapshot, nil
	}))
}

// List returns all supported cameras sorted by camera identifier.
func (catalog Catalog) List(ctx context.Context) ([]Target, error) {
	if catalog.loader == nil {
		return nil, fmt.Errorf("%w: loader unavailable", ErrCatalogUnavailable)
	}
	snapshot, err := catalog.loader.Load(ctx)
	if err != nil {
		return nil, err
	}

	targets := make([]Target, 0, len(snapshot.Devices))
	for _, device := range snapshot.Devices {
		target, ok, err := normalizeTarget(device)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		targets = append(targets, target)
	}

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].CameraID < targets[j].CameraID
	})
	return targets, nil
}

// Select returns the matching supported camera target by identifier.
func (catalog Catalog) Select(ctx context.Context, cameraID string) (Target, error) {
	cameraID = strings.TrimSpace(cameraID)
	if cameraID == "" {
		return Target{}, fmt.Errorf("%w: camera id is blank", ErrInvalidCameraID)
	}

	targets, err := catalog.List(ctx)
	if err != nil {
		return Target{}, err
	}
	for _, target := range targets {
		if target.CameraID == cameraID {
			return target, nil
		}
	}
	return Target{}, fmt.Errorf("%w: camera_id=%s", ErrNotFound, cameraID)
}

func normalizeTarget(device miot.DeviceInfo) (Target, bool, error) {
	cameraID := strings.TrimSpace(device.DID)
	model := strings.TrimSpace(device.Model)
	if cameraID == "" || model == "" {
		return Target{}, false, nil
	}

	supportInfo, ok, err := LookupSupport(model)
	if err != nil {
		return Target{}, false, err
	}
	if !ok {
		return Target{}, false, nil
	}

	return Target{
		CameraID:    cameraID,
		Name:        strings.TrimSpace(device.Name),
		Model:       model,
		HomeID:      strings.TrimSpace(device.HomeID),
		Home:        strings.TrimSpace(device.HomeName),
		RoomID:      strings.TrimSpace(device.RoomID),
		Room:        strings.TrimSpace(device.RoomName),
		Online:      device.Online,
		SupportInfo: supportInfo,
	}, true, nil
}
