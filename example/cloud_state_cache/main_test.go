package main

import (
	"testing"

	miot "github.com/ysh0566/go-mihome"
)

func TestCollectReadablePropertyQueriesSkipsOfflineAndNonReadableEntities(t *testing.T) {
	t.Parallel()

	onlineDevice := buildTestDevice(t, miot.DeviceInfo{
		DID:    "online.did",
		Name:   "Online Lamp",
		URN:    "urn:miot-spec-v2:device:light:0000A001:test-light:1",
		Model:  "test.light.v1",
		Online: true,
	}, miot.SpecInstance{
		URN:              "urn:miot-spec-v2:device:light:0000A001:test-light:1",
		Name:             "light",
		Description:      "Light",
		DescriptionTrans: "Light",
		Services: []miot.SpecService{
			{
				IID:              2,
				Type:             "urn:miot-spec-v2:service:light:00007802:test-light:1",
				Name:             "light",
				Description:      "Light",
				DescriptionTrans: "Light",
				Properties: []miot.SpecProperty{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:property:on:00000006:test-light:1",
						Name:             "on",
						Description:      "Power",
						DescriptionTrans: "Power",
						Format:           "bool",
						Readable:         true,
						Writable:         true,
					},
					{
						IID:              2,
						Type:             "urn:miot-spec-v2:property:mode:00000008:test-light:1",
						Name:             "mode",
						Description:      "Mode",
						DescriptionTrans: "Mode",
						Format:           "int",
						Writable:         true,
					},
				},
			},
		},
	})

	offlineDevice := buildTestDevice(t, miot.DeviceInfo{
		DID:    "offline.did",
		Name:   "Offline Lamp",
		URN:    "urn:miot-spec-v2:device:light:0000A001:test-light:1",
		Model:  "test.light.v1",
		Online: false,
	}, miot.SpecInstance{
		URN:              "urn:miot-spec-v2:device:light:0000A001:test-light:1",
		Name:             "light",
		Description:      "Light",
		DescriptionTrans: "Light",
		Services: []miot.SpecService{
			{
				IID:              2,
				Type:             "urn:miot-spec-v2:service:light:00007802:test-light:1",
				Name:             "light",
				Description:      "Light",
				DescriptionTrans: "Light",
				Properties: []miot.SpecProperty{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:property:on:00000006:test-light:1",
						Name:             "on",
						Description:      "Power",
						DescriptionTrans: "Power",
						Format:           "bool",
						Readable:         true,
					},
				},
			},
		},
	})

	cache, queries := collectReadablePropertyQueries([]miot.Device{onlineDevice, offlineDevice})
	if len(queries) != 1 {
		t.Fatalf("query count = %d, want 1", len(queries))
	}
	if queries[0].DID != "online.did" || queries[0].SIID != 2 || queries[0].PIID != 1 {
		t.Fatalf("query = %+v, want online.did 2/1", queries[0])
	}

	onlineEntry := cache.Devices["online.did"]
	if onlineEntry == nil {
		t.Fatal("missing online device cache entry")
	}
	if len(onlineEntry.Properties) != 1 {
		t.Fatalf("online property count = %d, want 1", len(onlineEntry.Properties))
	}

	offlineEntry := cache.Devices["offline.did"]
	if offlineEntry == nil {
		t.Fatal("missing offline device cache entry")
	}
	if len(offlineEntry.Properties) != 0 {
		t.Fatalf("offline property count = %d, want 0", len(offlineEntry.Properties))
	}
}

func TestApplyPropertyResultsUpdatesCachedValues(t *testing.T) {
	t.Parallel()

	cache := &stateCache{
		Devices: map[string]*deviceStateCache{
			"123": {
				DID: "123",
				Properties: map[string]*propertyStateCache{
					"p:2:1": {
						Key:         "p:2:1",
						ServiceIID:  2,
						PropertyIID: 1,
					},
				},
			},
		},
	}

	applyPropertyResults(cache, []miot.PropertyResult{
		{
			DID:   "123",
			SIID:  2,
			PIID:  1,
			Value: miot.NewSpecValueBool(true),
		},
		{
			DID:   "missing",
			SIID:  2,
			PIID:  1,
			Value: miot.NewSpecValueBool(false),
		},
	})

	prop := cache.Devices["123"].Properties["p:2:1"]
	if prop == nil {
		t.Fatal("missing cached property")
	}
	if prop.ValueKind != string(miot.SpecValueKindBool) {
		t.Fatalf("value kind = %q, want %q", prop.ValueKind, miot.SpecValueKindBool)
	}
	if prop.ValueText != "true" {
		t.Fatalf("value text = %q, want true", prop.ValueText)
	}
}

func buildTestDevice(t *testing.T, info miot.DeviceInfo, spec miot.SpecInstance) miot.Device {
	t.Helper()

	device, err := miot.NewDevice(info, spec, nil)
	if err != nil {
		t.Fatalf("NewDevice returned error: %v", err)
	}
	return device
}
