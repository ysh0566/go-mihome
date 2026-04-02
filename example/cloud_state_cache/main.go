package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

const (
	defaultClientID      = "2882303761520431603"
	defaultCloudServer   = "cn"
	defaultStorageDir    = ".miot-example-cache"
	defaultLanguage      = "zh-Hans"
	defaultRequestTimout = 90 * time.Second
	propertyBatchSize    = 150
)

type stateCache struct {
	UID           string                       `json:"uid"`
	RefreshedAt   time.Time                    `json:"refreshed_at"`
	DeviceCount   int                          `json:"device_count"`
	OnlineCount   int                          `json:"online_count"`
	PropertyCount int                          `json:"property_count"`
	Devices       map[string]*deviceStateCache `json:"-"`
}

type deviceStateCache struct {
	DID          string                         `json:"did"`
	Name         string                         `json:"name"`
	Model        string                         `json:"model"`
	URN          string                         `json:"urn"`
	Online       bool                           `json:"online"`
	Manufacturer string                         `json:"manufacturer,omitempty"`
	HomeID       string                         `json:"home_id,omitempty"`
	HomeName     string                         `json:"home_name,omitempty"`
	RoomID       string                         `json:"room_id,omitempty"`
	RoomName     string                         `json:"room_name,omitempty"`
	LoadError    string                         `json:"load_error,omitempty"`
	Properties   map[string]*propertyStateCache `json:"-"`
}

type propertyStateCache struct {
	Key           string         `json:"key"`
	Category      string         `json:"category"`
	SemanticType  string         `json:"semantic_type"`
	CanonicalUnit string         `json:"canonical_unit,omitempty"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	ServiceIID    int            `json:"service_iid"`
	PropertyIID   int            `json:"property_iid"`
	Readable      bool           `json:"readable"`
	Writable      bool           `json:"writable"`
	Notifiable    bool           `json:"notifiable"`
	Value         miot.SpecValue `json:"value,omitempty"`
	ValueKind     string         `json:"value_kind,omitempty"`
	ValueText     string         `json:"value_text,omitempty"`
}

type stateCacheView struct {
	UID           string            `json:"uid"`
	RefreshedAt   time.Time         `json:"refreshed_at"`
	DeviceCount   int               `json:"device_count"`
	OnlineCount   int               `json:"online_count"`
	PropertyCount int               `json:"property_count"`
	Devices       []deviceStateView `json:"devices"`
}

type deviceStateView struct {
	DID          string              `json:"did"`
	Name         string              `json:"name"`
	Model        string              `json:"model"`
	URN          string              `json:"urn"`
	Online       bool                `json:"online"`
	Manufacturer string              `json:"manufacturer,omitempty"`
	HomeID       string              `json:"home_id,omitempty"`
	HomeName     string              `json:"home_name,omitempty"`
	RoomID       string              `json:"room_id,omitempty"`
	RoomName     string              `json:"room_name,omitempty"`
	LoadError    string              `json:"load_error,omitempty"`
	Properties   []propertyStateView `json:"properties"`
}

type propertyStateView struct {
	Key           string         `json:"key"`
	Category      string         `json:"category"`
	SemanticType  string         `json:"semantic_type"`
	CanonicalUnit string         `json:"canonical_unit,omitempty"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	ServiceIID    int            `json:"service_iid"`
	PropertyIID   int            `json:"property_iid"`
	Readable      bool           `json:"readable"`
	Writable      bool           `json:"writable"`
	Notifiable    bool           `json:"notifiable"`
	Value         miot.SpecValue `json:"value,omitempty"`
	ValueKind     string         `json:"value_kind,omitempty"`
	ValueText     string         `json:"value_text,omitempty"`
}

func main() {
	log.SetFlags(0)

	cfg, err := exampleutil.LoadCloudConfig(exampleutil.CloudConfig{
		ClientID:    defaultClientID,
		CloudServer: defaultCloudServer,
		StorageDir:  defaultStorageDir,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		log.Fatal(err)
	}

	client, err := cfg.NewCloudClient()
	if err != nil {
		log.Fatal(err)
	}
	storage, err := cfg.NewStorage()
	if err != nil {
		log.Fatal(err)
	}

	parser, err := miot.NewSpecParser(
		exampleutil.LookupString("MIOT_LANGUAGE", defaultLanguage),
		storage,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer parser.Close()

	ctx, cancel := context.WithTimeout(context.Background(), defaultRequestTimout)
	defer cancel()

	cache, err := buildStateCache(ctx, client, parser)
	if err != nil {
		log.Fatal(err)
	}
	if err := exampleutil.PrintJSONStdout(cacheView(cache)); err != nil {
		log.Fatal(err)
	}
}

func buildStateCache(ctx context.Context, client *miot.CloudClient, parser *miot.SpecParser) (*stateCache, error) {
	snapshot, err := client.GetDevices(ctx, nil)
	if err != nil {
		return nil, err
	}

	cache := newStateCache(snapshot)
	devices := make([]miot.Device, 0, len(snapshot.Devices))

	dids := sortedDeviceIDs(snapshot.Devices)
	for _, did := range dids {
		info := snapshot.Devices[did]
		spec, err := parser.Parse(ctx, info.URN)
		if err != nil {
			cache.Devices[did].LoadError = fmt.Sprintf("parse spec: %v", err)
			continue
		}

		device, err := miot.NewDevice(info, spec, nil)
		if err != nil {
			cache.Devices[did].LoadError = fmt.Sprintf("build entities: %v", err)
			continue
		}
		devices = append(devices, device)
	}

	typedCache, queries := collectReadablePropertyQueries(devices)
	mergeStateCache(cache, typedCache)

	results, err := fetchPropertyResults(ctx, client, queries)
	if err != nil {
		return nil, err
	}
	applyPropertyResults(cache, results)
	finalizeStateCache(cache)
	return cache, nil
}

func newStateCache(snapshot miot.DeviceSnapshot) *stateCache {
	cache := &stateCache{
		UID:     snapshot.UID,
		Devices: make(map[string]*deviceStateCache, len(snapshot.Devices)),
	}
	for did, info := range snapshot.Devices {
		cache.Devices[did] = &deviceStateCache{
			DID:          info.DID,
			Name:         info.Name,
			Model:        info.Model,
			URN:          info.URN,
			Online:       info.Online,
			Manufacturer: info.Manufacturer,
			HomeID:       info.HomeID,
			HomeName:     info.HomeName,
			RoomID:       info.RoomID,
			RoomName:     info.RoomName,
			Properties:   map[string]*propertyStateCache{},
		}
	}
	finalizeStateCache(cache)
	return cache
}

func collectReadablePropertyQueries(devices []miot.Device) (*stateCache, []miot.PropertyQuery) {
	cache := &stateCache{
		Devices: make(map[string]*deviceStateCache, len(devices)),
	}
	queries := make([]miot.PropertyQuery, 0)
	seenQueries := make(map[string]struct{})

	sorted := append([]miot.Device(nil), devices...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Info.DID < sorted[j].Info.DID
	})

	for _, device := range sorted {
		entry := &deviceStateCache{
			DID:          device.Info.DID,
			Name:         device.Info.Name,
			Model:        device.Info.Model,
			URN:          device.Info.URN,
			Online:       device.Info.Online,
			Manufacturer: device.Info.Manufacturer,
			HomeID:       device.Info.HomeID,
			HomeName:     device.Info.HomeName,
			RoomID:       device.Info.RoomID,
			RoomName:     device.Info.RoomName,
			Properties:   map[string]*propertyStateCache{},
		}
		cache.Devices[device.Info.DID] = entry

		if !device.Info.Online {
			continue
		}

		entities := append([]*miot.Entity(nil), device.Entities...)
		sort.Slice(entities, func(i, j int) bool {
			return entities[i].Descriptor().Key < entities[j].Descriptor().Key
		})
		for _, entity := range entities {
			if entity == nil {
				continue
			}
			desc := entity.Descriptor()
			if desc.Kind != miot.EntityKindProperty || !desc.Readable {
				continue
			}

			entry.Properties[desc.Key] = &propertyStateCache{
				Key:           desc.Key,
				Category:      desc.Category,
				SemanticType:  desc.SemanticType,
				CanonicalUnit: desc.CanonicalUnit,
				Name:          desc.Name,
				Description:   desc.Description,
				ServiceIID:    desc.ServiceIID,
				PropertyIID:   desc.PropertyIID,
				Readable:      desc.Readable,
				Writable:      desc.Writable,
				Notifiable:    desc.Notifiable,
			}

			queryKey := propertyQueryKey(device.Info.DID, desc.ServiceIID, desc.PropertyIID)
			if _, ok := seenQueries[queryKey]; ok {
				continue
			}
			seenQueries[queryKey] = struct{}{}
			queries = append(queries, miot.PropertyQuery{
				DID:  device.Info.DID,
				SIID: desc.ServiceIID,
				PIID: desc.PropertyIID,
			})
		}
	}

	finalizeStateCache(cache)
	return cache, queries
}

func mergeStateCache(dst, src *stateCache) {
	if dst == nil || src == nil {
		return
	}
	for did, srcDevice := range src.Devices {
		dstDevice := dst.Devices[did]
		if dstDevice == nil {
			dst.Devices[did] = srcDevice
			continue
		}
		if dstDevice.Name == "" {
			dstDevice.Name = srcDevice.Name
		}
		if dstDevice.Model == "" {
			dstDevice.Model = srcDevice.Model
		}
		if dstDevice.URN == "" {
			dstDevice.URN = srcDevice.URN
		}
		if dstDevice.Manufacturer == "" {
			dstDevice.Manufacturer = srcDevice.Manufacturer
		}
		if dstDevice.HomeID == "" {
			dstDevice.HomeID = srcDevice.HomeID
		}
		if dstDevice.HomeName == "" {
			dstDevice.HomeName = srcDevice.HomeName
		}
		if dstDevice.RoomID == "" {
			dstDevice.RoomID = srcDevice.RoomID
		}
		if dstDevice.RoomName == "" {
			dstDevice.RoomName = srcDevice.RoomName
		}
		if dstDevice.Properties == nil {
			dstDevice.Properties = map[string]*propertyStateCache{}
		}
		for key, prop := range srcDevice.Properties {
			dstDevice.Properties[key] = prop
		}
	}
	finalizeStateCache(dst)
}

func fetchPropertyResults(ctx context.Context, client *miot.CloudClient, queries []miot.PropertyQuery) ([]miot.PropertyResult, error) {
	if len(queries) == 0 {
		return nil, nil
	}

	results := make([]miot.PropertyResult, 0, len(queries))
	for start := 0; start < len(queries); start += propertyBatchSize {
		end := start + propertyBatchSize
		if end > len(queries) {
			end = len(queries)
		}
		batch, err := client.GetProps(ctx, miot.GetPropsRequest{
			Params: queries[start:end],
		})
		if err != nil {
			return nil, err
		}
		results = append(results, batch...)
	}
	return results, nil
}

func applyPropertyResults(cache *stateCache, results []miot.PropertyResult) {
	if cache == nil {
		return
	}
	for _, result := range results {
		device := cache.Devices[result.DID]
		if device == nil {
			continue
		}
		prop := device.Properties[propertyEntityKey(result.SIID, result.PIID)]
		if prop == nil {
			continue
		}
		prop.Value = result.Value
		prop.ValueKind = string(result.Value.Kind())
		prop.ValueText = exampleutil.FormatSpecValue(result.Value)
	}
	finalizeStateCache(cache)
}

func cacheView(cache *stateCache) stateCacheView {
	view := stateCacheView{
		UID:           cache.UID,
		RefreshedAt:   cache.RefreshedAt,
		DeviceCount:   cache.DeviceCount,
		OnlineCount:   cache.OnlineCount,
		PropertyCount: cache.PropertyCount,
		Devices:       make([]deviceStateView, 0, len(cache.Devices)),
	}

	for _, did := range sortedCacheDeviceIDs(cache) {
		device := cache.Devices[did]
		props := make([]propertyStateView, 0, len(device.Properties))
		for _, key := range sortedPropertyKeys(device.Properties) {
			prop := device.Properties[key]
			props = append(props, propertyStateView{
				Key:           prop.Key,
				Category:      prop.Category,
				SemanticType:  prop.SemanticType,
				CanonicalUnit: prop.CanonicalUnit,
				Name:          prop.Name,
				Description:   prop.Description,
				ServiceIID:    prop.ServiceIID,
				PropertyIID:   prop.PropertyIID,
				Readable:      prop.Readable,
				Writable:      prop.Writable,
				Notifiable:    prop.Notifiable,
				Value:         prop.Value,
				ValueKind:     prop.ValueKind,
				ValueText:     prop.ValueText,
			})
		}

		view.Devices = append(view.Devices, deviceStateView{
			DID:          device.DID,
			Name:         device.Name,
			Model:        device.Model,
			URN:          device.URN,
			Online:       device.Online,
			Manufacturer: device.Manufacturer,
			HomeID:       device.HomeID,
			HomeName:     device.HomeName,
			RoomID:       device.RoomID,
			RoomName:     device.RoomName,
			LoadError:    device.LoadError,
			Properties:   props,
		})
	}

	return view
}

func finalizeStateCache(cache *stateCache) {
	if cache == nil {
		return
	}
	cache.RefreshedAt = time.Now().UTC()
	cache.DeviceCount = len(cache.Devices)
	cache.OnlineCount = 0
	cache.PropertyCount = 0
	for _, device := range cache.Devices {
		if device.Online {
			cache.OnlineCount++
		}
		cache.PropertyCount += len(device.Properties)
	}
}

func sortedDeviceIDs(devices map[string]miot.DeviceInfo) []string {
	keys := make([]string, 0, len(devices))
	for did := range devices {
		keys = append(keys, did)
	}
	sort.Strings(keys)
	return keys
}

func sortedCacheDeviceIDs(cache *stateCache) []string {
	keys := make([]string, 0, len(cache.Devices))
	for did := range cache.Devices {
		keys = append(keys, did)
	}
	sort.Strings(keys)
	return keys
}

func sortedPropertyKeys(props map[string]*propertyStateCache) []string {
	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func propertyQueryKey(did string, siid, piid int) string {
	return fmt.Sprintf("%s|%d|%d", did, siid, piid)
}

func propertyEntityKey(siid, piid int) string {
	return fmt.Sprintf("p:%d:%d", siid, piid)
}
