package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"time"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

const (
	defaultClientID      = "2882303761520431603"
	defaultCloudServer   = "cn"
	defaultMIPSUUID      = "go-mihome-example"
	defaultListenTimeout = 60 * time.Hour
)

type cloudMIPSReadyClient interface {
	Start(ctx context.Context) error
	AwaitConnection(ctx context.Context) error
	SubscribeDeviceState(ctx context.Context, did string, fn miot.DeviceStateHandler) (miot.Subscription, error)
	SubscribeProperty(ctx context.Context, req miot.PropertySubscription, fn miot.PropertyEventHandler) (miot.Subscription, error)
	SubscribeEvent(ctx context.Context, req miot.EventSubscription, fn miot.EventHandler) (miot.Subscription, error)
}

type cloudMIPSReadyHandlers struct {
	deviceState func(string, miot.DeviceState)
	property    func(miot.PropertyResult)
	event       func(miot.EventOccurrence)
}

type cloudMIPSReadySubscriptions struct {
	stateSub    miot.Subscription
	propertySub miot.Subscription
	eventSub    miot.Subscription
}

func (s *cloudMIPSReadySubscriptions) Close() error {
	if s == nil {
		return nil
	}
	if s.eventSub != nil {
		_ = s.eventSub.Close()
	}
	if s.propertySub != nil {
		_ = s.propertySub.Close()
	}
	if s.stateSub != nil {
		_ = s.stateSub.Close()
	}
	return nil
}

func main() {
	log.SetFlags(0)

	cfg, err := exampleutil.LoadCloudConfig(exampleutil.CloudConfig{
		ClientID:    defaultClientID,
		CloudServer: defaultCloudServer,
		StorageDir:  ".miot-example-cache",
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		log.Fatal(err)
	}

	deviceDID := exampleutil.LookupString("MIOT_DEVICE_DID", "demo-device-1")
	if deviceDID == "" {
		log.Fatal("missing MIOT_DEVICE_DID for cloud MIPS subscription example")
	}

	siid, err := exampleutil.LookupInt("MIOT_PROPERTY_SIID", 2)
	if err != nil {
		log.Fatal(err)
	}
	piid, err := exampleutil.LookupInt("MIOT_PROPERTY_PIID", 1)
	if err != nil {
		log.Fatal(err)
	}

	client, err := miot.NewCloudMIPSClient(miot.MIPSCloudConfig{
		UUID:        exampleutil.LookupString("MIOT_MIPS_UUID", defaultMIPSUUID),
		CloudServer: cfg.CloudServer,
		AppID:       cfg.ClientID,
		Token:       cfg.AccessToken,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, defaultListenTimeout)
	defer cancel()

	handlers := cloudMIPSReadyHandlers{
		deviceState: func(did string, state miot.DeviceState) {
			_ = exampleutil.PrintJSONStdout(struct {
				Type  string           `json:"type"`
				DID   string           `json:"did"`
				State miot.DeviceState `json:"state"`
			}{
				Type:  "device_state",
				DID:   did,
				State: state,
			})
		},
		property: func(result miot.PropertyResult) {
			_ = exampleutil.PrintJSONStdout(struct {
				Type      string              `json:"type"`
				Result    miot.PropertyResult `json:"result"`
				ValueKind string              `json:"value_kind"`
				ValueText string              `json:"value_text"`
			}{
				Type:      "property",
				Result:    result,
				ValueKind: string(result.Value.Kind()),
				ValueText: exampleutil.FormatSpecValue(result.Value),
			})
		},
		event: func(event miot.EventOccurrence) {
			_ = exampleutil.PrintJSONStdout(struct {
				Type  string               `json:"type"`
				Event miot.EventOccurrence `json:"event"`
			}{
				Type:  "event",
				Event: event,
			})
		},
	}

	subs, err := runCloudMIPSReadySequence(ctx, client, deviceDID, siid, piid, handlers, func() error {
		return exampleutil.PrintJSONStdout(struct {
			Host      string                    `json:"host"`
			Port      int                       `json:"port"`
			ClientID  string                    `json:"client_id"`
			DeviceDID string                    `json:"device_did"`
			Property  miot.PropertySubscription `json:"property_subscription"`
			Timeout   string                    `json:"timeout"`
		}{
			Host:      client.Host(),
			Port:      client.Port(),
			ClientID:  client.ClientID(),
			DeviceDID: deviceDID,
			Property: miot.PropertySubscription{
				DID:  deviceDID,
				SIID: siid,
				PIID: piid,
			},
			Timeout: defaultListenTimeout.String(),
		})
	})
	if err != nil {
		log.Fatal(err)
	}
	defer subs.Close()

	<-ctx.Done()
}

func runCloudMIPSReadySequence(ctx context.Context, client cloudMIPSReadyClient, deviceDID string, siid, piid int, handlers cloudMIPSReadyHandlers, emitReady func() error) (*cloudMIPSReadySubscriptions, error) {
	if err := client.Start(ctx); err != nil {
		return nil, err
	}
	if err := client.AwaitConnection(ctx); err != nil {
		return nil, err
	}

	stateHandler := handlers.deviceState
	if stateHandler == nil {
		stateHandler = func(string, miot.DeviceState) {}
	}
	propertyHandler := handlers.property
	if propertyHandler == nil {
		propertyHandler = func(miot.PropertyResult) {}
	}
	eventHandler := handlers.event
	if eventHandler == nil {
		eventHandler = func(miot.EventOccurrence) {}
	}

	stateSub, err := client.SubscribeDeviceState(ctx, deviceDID, stateHandler)
	if err != nil {
		return nil, err
	}
	propertySub, err := client.SubscribeProperty(ctx, miot.PropertySubscription{
		DID:  deviceDID,
		SIID: siid,
		PIID: piid,
	}, propertyHandler)
	if err != nil {
		_ = stateSub.Close()
		return nil, err
	}
	eventSub, err := client.SubscribeEvent(ctx, miot.EventSubscription{DID: deviceDID}, eventHandler)
	if err != nil {
		_ = propertySub.Close()
		_ = stateSub.Close()
		return nil, err
	}

	subs := &cloudMIPSReadySubscriptions{
		stateSub:    stateSub,
		propertySub: propertySub,
		eventSub:    eventSub,
	}
	if emitReady != nil {
		if err := emitReady(); err != nil {
			_ = subs.Close()
			return nil, err
		}
	}
	return subs, nil
}
