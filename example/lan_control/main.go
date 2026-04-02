package main

import (
	"context"
	"log"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

const (
	defaultLANDID       = ""
	defaultLANToken     = ""
	defaultLANIP        = ""
	defaultLANInterface = ""
	defaultPropertySIID = 0
	defaultPropertyPIID = 0
)

func main() {
	log.SetFlags(0)

	cfg, err := exampleutil.LoadLANConfig(exampleutil.LANConfig{
		Device: miot.LANDeviceConfig{
			DID:       defaultLANDID,
			Token:     defaultLANToken,
			IP:        defaultLANIP,
			Interface: defaultLANInterface,
		},
		Property: exampleutil.PropertySelection{
			DeviceDID: defaultLANDID,
			SIID:      defaultPropertySIID,
			PIID:      defaultPropertyPIID,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}

	client := miot.NewLANClient(nil)
	defer client.Close()

	if cfg.Device.Interface != "" {
		if err := client.UpdateInterfaces([]string{cfg.Device.Interface}); err != nil {
			log.Fatal(err)
		}
	}
	if err := client.AddDevice(cfg.Device); err != nil {
		log.Fatal(err)
	}
	client.VoteForLANControl("example", true)
	if err := client.Start(context.Background()); err != nil {
		log.Fatal(err)
	}

	result, err := client.GetProp(context.Background(), cfg.Property.Query())
	if err != nil {
		log.Fatal(err)
	}

	if err := exampleutil.PrintJSONStdout(struct {
		DeviceList []miot.LANDeviceSummary `json:"device_list"`
		Query      miot.PropertyQuery      `json:"query"`
		Result     miot.PropertyResult     `json:"result"`
		ValueKind  string                  `json:"value_kind"`
		ValueText  string                  `json:"value_text"`
	}{
		DeviceList: client.GetDeviceList(),
		Query:      cfg.Property.Query(),
		Result:     result,
		ValueKind:  string(result.Value.Kind()),
		ValueText:  exampleutil.FormatSpecValue(result.Value),
	}); err != nil {
		log.Fatal(err)
	}
}
