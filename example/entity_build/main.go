package main

import (
	"context"
	"log"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

const (
	defaultStorageDir = ".miot-example-cache"
	defaultSpecURN    = "urn:miot-spec-v2:device:light:0000A001:yeelink-ceil39:1:0000C802"
	defaultDeviceDID  = "demo-device-1"
)

type entitySummary struct {
	Key           string `json:"key"`
	Kind          string `json:"kind"`
	Category      string `json:"category"`
	SemanticType  string `json:"semantic_type"`
	CanonicalUnit string `json:"canonical_unit"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Readable      bool   `json:"readable"`
	Writable      bool   `json:"writable"`
	Notifiable    bool   `json:"notifiable"`
	ServiceIID    int    `json:"service_iid"`
	PropertyIID   int    `json:"property_iid"`
	EventIID      int    `json:"event_iid"`
	ActionIID     int    `json:"action_iid"`
}

func main() {
	log.SetFlags(0)

	cfg, err := exampleutil.LoadCloudConfig(exampleutil.CloudConfig{
		StorageDir: defaultStorageDir,
	})
	if err != nil {
		log.Fatal(err)
	}
	specSelection, err := exampleutil.LoadSpecSelection(defaultSpecURN)
	if err != nil {
		log.Fatal(err)
	}
	if err := specSelection.Validate(); err != nil {
		log.Fatal(err)
	}

	storage, err := cfg.NewStorage()
	if err != nil {
		log.Fatal(err)
	}

	parser, err := miot.NewSpecParser("zh-Hans", storage)
	if err != nil {
		log.Fatal(err)
	}
	defer parser.Close()

	spec, err := parser.Parse(context.Background(), specSelection.URN)
	if err != nil {
		log.Fatal(err)
	}

	device, err := miot.NewDevice(miot.DeviceInfo{
		DID:   exampleutil.LookupString("MIOT_DEVICE_DID", defaultDeviceDID),
		Name:  spec.DescriptionTrans,
		URN:   spec.URN,
		Model: spec.Name,
	}, spec, nil)
	if err != nil {
		log.Fatal(err)
	}

	if err := exampleutil.PrintJSONStdout(struct {
		Device   miot.DeviceDescriptor `json:"device"`
		Info     miot.DeviceInfo       `json:"info"`
		Entities []entitySummary       `json:"entities"`
	}{
		Device:   device.Descriptor,
		Info:     device.Info,
		Entities: summarizeEntities(device.Entities),
	}); err != nil {
		log.Fatal(err)
	}
}

func summarizeEntities(entities []*miot.Entity) []entitySummary {
	out := make([]entitySummary, 0, len(entities))
	for _, entity := range entities {
		if entity == nil {
			continue
		}
		desc := entity.Descriptor()
		out = append(out, entitySummary{
			Key:           desc.Key,
			Kind:          string(desc.Kind),
			Category:      desc.Category,
			SemanticType:  desc.SemanticType,
			CanonicalUnit: desc.CanonicalUnit,
			Name:          desc.Name,
			Description:   desc.Description,
			Readable:      desc.Readable,
			Writable:      desc.Writable,
			Notifiable:    desc.Notifiable,
			ServiceIID:    desc.ServiceIID,
			PropertyIID:   desc.PropertyIID,
			EventIID:      desc.EventIID,
			ActionIID:     desc.ActionIID,
		})
	}
	return out
}
