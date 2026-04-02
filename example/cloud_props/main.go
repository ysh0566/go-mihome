package main

import (
	"context"
	"log"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

const (
	defaultClientID     = "2882303761520431603"
	defaultCloudServer  = "cn"
	defaultDeviceDID    = "demo-device-1"
	defaultPropertySIID = 2
	defaultPropertyPIID = 1
)

func main() {
	log.SetFlags(0)

	cfg, err := exampleutil.LoadCloudConfig(exampleutil.CloudConfig{
		ClientID:    defaultClientID,
		CloudServer: defaultCloudServer,
	})
	if err != nil {
		log.Fatal(err)
	}

	selection, err := exampleutil.LoadPropertySelection(exampleutil.PropertySelection{
		DeviceDID: defaultDeviceDID,
		SIID:      defaultPropertySIID,
		PIID:      defaultPropertyPIID,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := selection.Validate(); err != nil {
		log.Fatal(err)
	}

	client, err := cfg.NewCloudClient()
	if err != nil {
		log.Fatal(err)
	}

	result, err := client.GetProp(context.Background(), selection.Query())
	if err != nil {
		log.Fatal(err)
	}

	if err := exampleutil.PrintJSONStdout(struct {
		Query     miot.PropertyQuery  `json:"query"`
		Result    miot.PropertyResult `json:"result"`
		ValueKind string              `json:"value_kind"`
		ValueText string              `json:"value_text"`
	}{
		Query:     selection.Query(),
		Result:    result,
		ValueKind: string(result.Value.Kind()),
		ValueText: exampleutil.FormatSpecValue(result.Value),
	}); err != nil {
		log.Fatal(err)
	}
}
