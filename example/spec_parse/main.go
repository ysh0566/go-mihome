package main

import (
	"context"
	"log"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

const (
	defaultStorageDir = ".miot-example-cache"
	defaultSpecURN    = "urn:miot-spec-v2:device:camera:0000A01C:chuangmi-069a01:4"
)

type propertySummary struct {
	IID         int    `json:"iid"`
	Name        string `json:"name"`
	Format      string `json:"format"`
	Readable    bool   `json:"readable"`
	Writable    bool   `json:"writable"`
	Notifiable  bool   `json:"notifiable"`
	Unit        string `json:"unit,omitempty"`
	Description string `json:"description"`
}

type eventSummary struct {
	IID         int    `json:"iid"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type actionSummary struct {
	IID         int    `json:"iid"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type serviceSummary struct {
	IID         int               `json:"iid"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Properties  []propertySummary `json:"properties"`
	Events      []eventSummary    `json:"events"`
	Actions     []actionSummary   `json:"actions"`
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

	if err := exampleutil.PrintJSONStdout(struct {
		CacheDir    string           `json:"cache_dir"`
		URN         string           `json:"urn"`
		Name        string           `json:"name"`
		Description string           `json:"description"`
		Services    []serviceSummary `json:"services"`
	}{
		CacheDir:    storage.RootPath(),
		URN:         spec.URN,
		Name:        spec.Name,
		Description: spec.DescriptionTrans,
		Services:    summarizeServices(spec.Services),
	}); err != nil {
		log.Fatal(err)
	}
}

func summarizeServices(services []miot.SpecService) []serviceSummary {
	out := make([]serviceSummary, 0, len(services))
	for _, service := range services {
		properties := make([]propertySummary, 0, len(service.Properties))
		for _, property := range service.Properties {
			properties = append(properties, propertySummary{
				IID:         property.IID,
				Name:        property.Name,
				Format:      property.Format,
				Readable:    property.Readable,
				Writable:    property.Writable,
				Notifiable:  property.Notifiable,
				Unit:        property.Unit,
				Description: property.DescriptionTrans,
			})
		}

		events := make([]eventSummary, 0, len(service.Events))
		for _, event := range service.Events {
			events = append(events, eventSummary{
				IID:         event.IID,
				Name:        event.Name,
				Description: event.DescriptionTrans,
			})
		}

		actions := make([]actionSummary, 0, len(service.Actions))
		for _, action := range service.Actions {
			actions = append(actions, actionSummary{
				IID:         action.IID,
				Name:        action.Name,
				Description: action.DescriptionTrans,
			})
		}

		out = append(out, serviceSummary{
			IID:         service.IID,
			Name:        service.Name,
			Description: service.DescriptionTrans,
			Properties:  properties,
			Events:      events,
			Actions:     actions,
		})
	}
	return out
}
