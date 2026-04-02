package miot

import (
	"context"
	"testing"
)

func TestEntityMapperBuildsGenericLightEntity(t *testing.T) {
	spec := mustLoadSpecFixture(t, "testdata/spec/light_instance.json")
	reg := NewEntityRegistry(newStubEntityBackend())
	dev, err := reg.Build(DeviceInfo{DID: "demo-hub-1", Model: "yeelink.light.ml1"}, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(dev.Entities) == 0 {
		t.Fatal("expected generic entities")
	}
	if dev.EntityByKey("s:2") == nil {
		t.Fatal("expected service entity")
	}
	if dev.EntityByKey("s:2").Descriptor().Category != "light" {
		t.Fatalf("service category = %q", dev.EntityByKey("s:2").Descriptor().Category)
	}
	if dev.EntityByKey("p:2:1") == nil {
		t.Fatal("expected property entity")
	}
	if dev.EntityByKey("p:2:1").Descriptor().Category != "switch" {
		t.Fatalf("property category = %q", dev.EntityByKey("p:2:1").Descriptor().Category)
	}
}

func TestEntityDescriptorIncludesPropertyMetadata(t *testing.T) {
	spec := mustLoadSpecFixture(t, "testdata/spec/light_instance.json")
	dev, err := NewDevice(DeviceInfo{DID: "demo-hub-1", Model: "yeelink.light.ml1"}, spec, newStubEntityBackend())
	if err != nil {
		t.Fatal(err)
	}

	entity := dev.EntityByKey("p:2:1")
	if entity == nil {
		t.Fatal("expected property entity")
	}
	desc := entity.Descriptor()
	if !desc.Readable || !desc.Writable || !desc.Notifiable {
		t.Fatalf("descriptor flags = %#v", desc)
	}
	if desc.IconHint != "power" {
		t.Fatalf("icon hint = %q", desc.IconHint)
	}
	if len(desc.ValueList.Items) != 2 {
		t.Fatalf("value list = %#v", desc.ValueList)
	}
	if desc.ValueList.Items[0].Description != "Close" || desc.ValueList.Items[1].Description != "Open" {
		t.Fatalf("value list = %#v", desc.ValueList)
	}
}

func TestEntitySetDelegatesToBackend(t *testing.T) {
	backend := newStubEntityBackend()
	reg := NewEntityRegistry(backend)
	spec := mustLoadSpecFixture(t, "testdata/spec/light_instance.json")

	dev, err := reg.Build(DeviceInfo{DID: "demo-hub-1", Model: "yeelink.light.ml1"}, spec)
	if err != nil {
		t.Fatal(err)
	}
	entity := dev.EntityByKey("p:2:1")
	if entity == nil {
		t.Fatal("expected on property entity")
	}

	result, err := entity.Set(context.Background(), NewSpecValueBool(true))
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 {
		t.Fatalf("result = %#v", result)
	}
	if backend.lastSet.DID != "demo-hub-1" || backend.lastSet.SIID != 2 || backend.lastSet.PIID != 1 {
		t.Fatalf("last set = %#v", backend.lastSet)
	}
	value, ok := backend.lastSet.Value.Bool()
	if !ok || !value {
		t.Fatalf("set value = %#v", backend.lastSet.Value)
	}
}

func TestEntitySubscribePropertyDelegatesToBackend(t *testing.T) {
	backend := newStubEntityBackend()
	reg := NewEntityRegistry(backend)
	spec := mustLoadSpecFixture(t, "testdata/spec/light_instance.json")

	dev, err := reg.Build(DeviceInfo{DID: "demo-hub-1", Model: "yeelink.light.ml1"}, spec)
	if err != nil {
		t.Fatal(err)
	}
	entity := dev.EntityByKey("p:2:1")
	if entity == nil {
		t.Fatal("expected property entity")
	}
	sub, err := entity.SubscribeProperty(context.Background(), func(PropertyResult) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	if backend.lastPropSub.DID != "demo-hub-1" || backend.lastPropSub.SIID != 2 || backend.lastPropSub.PIID != 1 {
		t.Fatalf("last property sub = %#v", backend.lastPropSub)
	}
}

func TestEntitySubscribeStateDelegatesToBackend(t *testing.T) {
	backend := newStubEntityBackend()
	reg := NewEntityRegistry(backend)
	spec := mustLoadSpecFixture(t, "testdata/spec/light_instance.json")

	dev, err := reg.Build(DeviceInfo{DID: "demo-hub-1", Model: "yeelink.light.ml1"}, spec)
	if err != nil {
		t.Fatal(err)
	}
	entity := dev.EntityByKey("s:2")
	if entity == nil {
		t.Fatal("expected service entity")
	}
	sub, err := entity.SubscribeState(context.Background(), func(string, DeviceState) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	if backend.lastStateSub != "demo-hub-1" {
		t.Fatalf("last state sub = %q", backend.lastStateSub)
	}
}

func TestEntityMapperNormalizesParserBackedPropertyAliases(t *testing.T) {
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parser := newFixtureSpecParser(t, store, map[string][]string{
		testSpecURN: {"testdata/spec/tofan_wk01_instance.json"},
	})

	spec, err := parser.Parse(context.Background(), testSpecURN)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := NewDevice(DeviceInfo{DID: "tofan-1", Model: "tofan.wk01"}, spec, newStubEntityBackend())
	if err != nil {
		t.Fatal(err)
	}

	mode := dev.EntityByKey("p:2:2")
	if mode == nil {
		t.Fatal("expected mode entity")
	}
	if got := mode.Descriptor().SemanticType; got != "mode" {
		t.Fatalf("mode semantic = %q", got)
	}
	if got := mode.Descriptor().IconHint; got != "mode" {
		t.Fatalf("mode icon = %q", got)
	}
}

func TestEntityMapperAppliesServiceAndBinarySensorRules(t *testing.T) {
	spec := SpecInstance{
		URN:              "urn:miot-spec-v2:device:test:0000A000:test-device:1",
		Name:             "test-device",
		Description:      "Test Device",
		DescriptionTrans: "Test Device",
		Services: []SpecService{
			{
				IID:              2,
				Type:             "urn:miot-spec-v2:service:curtain:0000780C:test:1",
				Name:             "curtain",
				Description:      "Curtain",
				DescriptionTrans: "Curtain",
				Properties: []SpecProperty{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:property:motor-control:00000072:test:1",
						Name:             "motor-control",
						Description:      "Motor Control",
						DescriptionTrans: "Motor Control",
						Format:           "uint8",
						Readable:         false,
						Writable:         true,
					},
				},
			},
			{
				IID:              3,
				Type:             "urn:miot-spec-v2:service:environment:0000780A:test:1",
				Name:             "environment",
				Description:      "Environment",
				DescriptionTrans: "Environment",
				Properties: []SpecProperty{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:property:contact-state:0000002D:test:1",
						Name:             "contact-state",
						Description:      "Contact State",
						DescriptionTrans: "Contact State",
						Format:           "bool",
						Readable:         true,
						Notifiable:       true,
					},
				},
			},
		},
	}

	dev, err := NewDevice(DeviceInfo{DID: "test-device"}, spec, newStubEntityBackend())
	if err != nil {
		t.Fatal(err)
	}

	curtain := dev.EntityByKey("s:2")
	if curtain == nil {
		t.Fatal("expected curtain entity")
	}
	if got := curtain.Descriptor().Category; got != "cover" {
		t.Fatalf("curtain category = %q", got)
	}

	contact := dev.EntityByKey("p:3:1")
	if contact == nil {
		t.Fatal("expected contact entity")
	}
	if got := contact.Descriptor().Category; got != "binary_sensor" {
		t.Fatalf("contact category = %q", got)
	}
	if got := contact.Descriptor().SemanticType; got != "door" {
		t.Fatalf("contact semantic = %q", got)
	}
	if got := contact.Descriptor().IconHint; got != "door" {
		t.Fatalf("contact icon = %q", got)
	}
}

func TestEntityMapperMapsEventSemanticHints(t *testing.T) {
	spec := SpecInstance{
		URN:              "urn:miot-spec-v2:device:test:0000A000:test-device:1",
		Name:             "test-device",
		Description:      "Test Device",
		DescriptionTrans: "Test Device",
		Services: []SpecService{
			{
				IID:              2,
				Type:             "urn:miot-spec-v2:service:doorbell:000078AA:test:1",
				Name:             "doorbell",
				Description:      "Doorbell",
				DescriptionTrans: "Doorbell",
				Events: []SpecEvent{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:event:doorbell-ring:0000502C:test:1",
						Name:             "doorbell-ring",
						Description:      "Doorbell Ring",
						DescriptionTrans: "Doorbell Ring",
					},
				},
			},
		},
	}

	dev, err := NewDevice(DeviceInfo{DID: "doorbell-1"}, spec, newStubEntityBackend())
	if err != nil {
		t.Fatal(err)
	}

	event := dev.EntityByKey("e:2:1")
	if event == nil {
		t.Fatal("expected event entity")
	}
	if got := event.Descriptor().SemanticType; got != "doorbell" {
		t.Fatalf("event semantic = %q", got)
	}
	if got := event.Descriptor().IconHint; got != "doorbell" {
		t.Fatalf("event icon = %q", got)
	}
}

func TestEntityMapperAppliesAdditionalServiceAndSensorRules(t *testing.T) {
	spec := SpecInstance{
		URN:              "urn:miot-spec-v2:device:test:0000A000:test-device:1",
		Name:             "test-device",
		Description:      "Test Device",
		DescriptionTrans: "Test Device",
		Services: []SpecService{
			{
				IID:              2,
				Type:             "urn:miot-spec-v2:service:water-heater:0000780C:test:1",
				Name:             "water-heater",
				Description:      "Water Heater",
				DescriptionTrans: "Water Heater",
				Properties: []SpecProperty{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:property:temperature:00000020:test:1",
						Name:             "temperature",
						Description:      "Temperature",
						DescriptionTrans: "Temperature",
						Format:           "float",
						Readable:         true,
						Notifiable:       true,
						Unit:             "celsius",
					},
					{
						IID:              2,
						Type:             "urn:miot-spec-v2:property:battery-level:00000014:test:1",
						Name:             "battery-level",
						Description:      "Battery Level",
						DescriptionTrans: "Battery Level",
						Format:           "uint8",
						Readable:         true,
						Notifiable:       true,
						Unit:             "percentage",
					},
					{
						IID:              3,
						Type:             "urn:miot-spec-v2:property:power-consumption:00000021:test:1",
						Name:             "power-consumption",
						Description:      "Power Consumption",
						DescriptionTrans: "Power Consumption",
						Format:           "float",
						Readable:         true,
						Notifiable:       true,
						Unit:             "kWh",
					},
					{
						IID:              4,
						Type:             "urn:miot-spec-v2:property:submersion-state:00000065:test:1",
						Name:             "submersion-state",
						Description:      "Submersion State",
						DescriptionTrans: "Submersion State",
						Format:           "bool",
						Readable:         true,
						Notifiable:       true,
					},
					{
						IID:              99,
						Type:             "urn:miot-spec-v2:property:on:00000006:test:1",
						Name:             "on",
						Description:      "Power",
						DescriptionTrans: "Power",
						Format:           "bool",
						Readable:         true,
						Writable:         true,
					},
				},
			},
		},
	}

	dev, err := NewDevice(DeviceInfo{DID: "water-heater-1"}, spec, newStubEntityBackend())
	if err != nil {
		t.Fatal(err)
	}

	service := dev.EntityByKey("s:2")
	if service == nil {
		t.Fatal("expected water heater entity")
	}
	if got := service.Descriptor().Category; got != "water_heater" {
		t.Fatalf("service category = %q", got)
	}

	temperature := dev.EntityByKey("p:2:1")
	if temperature == nil {
		t.Fatal("expected temperature entity")
	}
	if got := temperature.Descriptor().CanonicalUnit; got != "C" {
		t.Fatalf("temperature unit = %q", got)
	}

	battery := dev.EntityByKey("p:2:2")
	if battery == nil {
		t.Fatal("expected battery entity")
	}
	if got := battery.Descriptor().SemanticType; got != "battery" {
		t.Fatalf("battery semantic = %q", got)
	}
	if got := battery.Descriptor().CanonicalUnit; got != "%" {
		t.Fatalf("battery unit = %q", got)
	}

	energy := dev.EntityByKey("p:2:3")
	if energy == nil {
		t.Fatal("expected energy entity")
	}
	if got := energy.Descriptor().SemanticType; got != "energy" {
		t.Fatalf("energy semantic = %q", got)
	}

	submersion := dev.EntityByKey("p:2:4")
	if submersion == nil {
		t.Fatal("expected submersion entity")
	}
	if got := submersion.Descriptor().Category; got != "binary_sensor" {
		t.Fatalf("submersion category = %q", got)
	}
	if got := submersion.Descriptor().SemanticType; got != "moisture" {
		t.Fatalf("submersion semantic = %q", got)
	}
}

func TestEntityMapperNormalizesAdditionalUnitsAndServiceAliases(t *testing.T) {
	spec := SpecInstance{
		URN:              "urn:miot-spec-v2:device:test:0000A000:test-device:1",
		Name:             "test-device",
		Description:      "Test Device",
		DescriptionTrans: "Test Device",
		Services: []SpecService{
			{
				IID:              2,
				Type:             "urn:miot-spec-v2:service:air-purifier:0000780C:test:1",
				Name:             "air-purifier",
				Description:      "Air Purifier",
				DescriptionTrans: "Air Purifier",
				Properties: []SpecProperty{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:property:voltage:0000000A:test:1",
						Name:             "voltage",
						Description:      "Voltage",
						DescriptionTrans: "Voltage",
						Format:           "float",
						Readable:         true,
						Unit:             "volt",
					},
					{
						IID:              2,
						Type:             "urn:miot-spec-v2:property:electric-current:0000000B:test:1",
						Name:             "electric-current",
						Description:      "Current",
						DescriptionTrans: "Current",
						Format:           "float",
						Readable:         true,
						Unit:             "ampere",
					},
					{
						IID:              3,
						Type:             "urn:miot-spec-v2:property:atmospheric-pressure:0000000C:test:1",
						Name:             "atmospheric-pressure",
						Description:      "Pressure",
						DescriptionTrans: "Pressure",
						Format:           "float",
						Readable:         true,
						Unit:             "pascal",
					},
					{
						IID:              4,
						Type:             "urn:miot-spec-v2:property:illumination:0000000D:test:1",
						Name:             "illumination",
						Description:      "Illumination",
						DescriptionTrans: "Illumination",
						Format:           "float",
						Readable:         true,
						Unit:             "lux",
					},
					{
						IID:              5,
						Type:             "urn:miot-spec-v2:property:electric-power:0000000E:test:1",
						Name:             "electric-power",
						Description:      "Power",
						DescriptionTrans: "Power",
						Format:           "float",
						Readable:         true,
						Unit:             "watt",
					},
					{
						IID:              98,
						Type:             "urn:miot-spec-v2:property:on:00000006:test:1",
						Name:             "on",
						Description:      "Power",
						DescriptionTrans: "Power",
						Format:           "bool",
						Readable:         true,
						Writable:         true,
					},
					{
						IID:              99,
						Type:             "urn:miot-spec-v2:property:fan-level:00000016:test:1",
						Name:             "fan-level",
						Description:      "Fan Level",
						DescriptionTrans: "Fan Level",
						Format:           "uint8",
						Readable:         true,
						Writable:         true,
					},
				},
			},
		},
	}

	dev, err := NewDevice(DeviceInfo{DID: "air-purifier-1"}, spec, newStubEntityBackend())
	if err != nil {
		t.Fatal(err)
	}

	service := dev.EntityByKey("s:2")
	if service == nil {
		t.Fatal("expected air purifier entity")
	}
	if got := service.Descriptor().Category; got != "fan" {
		t.Fatalf("service category = %q", got)
	}

	if got := dev.EntityByKey("p:2:1").Descriptor().CanonicalUnit; got != "V" {
		t.Fatalf("voltage unit = %q", got)
	}
	if got := dev.EntityByKey("p:2:2").Descriptor().CanonicalUnit; got != "A" {
		t.Fatalf("current unit = %q", got)
	}
	if got := dev.EntityByKey("p:2:3").Descriptor().CanonicalUnit; got != "Pa" {
		t.Fatalf("pressure unit = %q", got)
	}
	if got := dev.EntityByKey("p:2:4").Descriptor().CanonicalUnit; got != "lx" {
		t.Fatalf("illumination unit = %q", got)
	}
	if got := dev.EntityByKey("p:2:5").Descriptor().CanonicalUnit; got != "W" {
		t.Fatalf("power unit = %q", got)
	}
}

func TestEntityMapperFallsBackToBinarySensorForReadableBoolProperties(t *testing.T) {
	spec := SpecInstance{
		URN:              "urn:miot-spec-v2:device:test:0000A000:test-device:1",
		Name:             "test-device",
		Description:      "Test Device",
		DescriptionTrans: "Test Device",
		Services: []SpecService{
			{
				IID:              2,
				Type:             "urn:miot-spec-v2:service:environment:0000780A:test:1",
				Name:             "environment",
				Description:      "Environment",
				DescriptionTrans: "Environment",
				Properties: []SpecProperty{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:property:some-state:0000002D:test:1",
						Name:             "some-state",
						Description:      "Some State",
						DescriptionTrans: "Some State",
						Format:           "bool",
						Readable:         true,
						Access:           []string{"read"},
					},
				},
			},
		},
	}

	dev, err := NewDevice(DeviceInfo{DID: "test-device"}, spec, newStubEntityBackend())
	if err != nil {
		t.Fatal(err)
	}

	property := dev.EntityByKey("p:2:1")
	if property == nil {
		t.Fatal("expected property entity")
	}
	if got := property.Descriptor().Category; got != "binary_sensor" {
		t.Fatalf("property category = %q", got)
	}
}

func TestDeviceDescriptorUsesParserBackedThermostatSemantics(t *testing.T) {
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parser := newFixtureSpecParser(t, store, map[string][]string{
		testSpecURN: {"testdata/spec/tofan_wk01_instance.json"},
	})

	spec, err := parser.Parse(context.Background(), testSpecURN)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := NewDevice(DeviceInfo{DID: "tofan-1", Model: "tofan.wk01"}, spec, newStubEntityBackend())
	if err != nil {
		t.Fatal(err)
	}

	if got := dev.Descriptor.Category; got != "climate" {
		t.Fatalf("device category = %q", got)
	}
	if got := dev.Descriptor.SemanticType; got != "thermostat" {
		t.Fatalf("device semantic = %q", got)
	}
	if got := dev.Descriptor.IconHint; got != "thermostat" {
		t.Fatalf("device icon = %q", got)
	}
}

func TestDeviceDescriptorMatchesHumidifierRule(t *testing.T) {
	spec := SpecInstance{
		URN:              "urn:miot-spec-v2:device:humidifier:0000A00E:test-device:1",
		Name:             "humidifier",
		Description:      "Humidifier",
		DescriptionTrans: "Humidifier",
		Services: []SpecService{
			{
				IID:              2,
				Type:             "urn:miot-spec-v2:service:humidifier:00007816:test:1",
				Name:             "humidifier",
				Description:      "Humidifier",
				DescriptionTrans: "Humidifier",
				Properties: []SpecProperty{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:property:on:00000006:test:1",
						Name:             "on",
						Description:      "Power",
						DescriptionTrans: "Power",
						Format:           "bool",
						Readable:         true,
						Writable:         true,
					},
					{
						IID:              2,
						Type:             "urn:miot-spec-v2:property:mode:00000008:test:1",
						Name:             "mode",
						Description:      "Mode",
						DescriptionTrans: "Mode",
						Format:           "uint8",
						Readable:         true,
						Writable:         true,
					},
				},
			},
			{
				IID:              3,
				Type:             "urn:miot-spec-v2:service:environment:0000780A:test:1",
				Name:             "environment",
				Description:      "Environment",
				DescriptionTrans: "Environment",
				Properties: []SpecProperty{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:property:relative-humidity:0000000D:test:1",
						Name:             "relative-humidity",
						Description:      "Relative Humidity",
						DescriptionTrans: "Relative Humidity",
						Format:           "float",
						Readable:         true,
					},
				},
			},
		},
	}

	dev, err := NewDevice(DeviceInfo{DID: "humidifier-1"}, spec, newStubEntityBackend())
	if err != nil {
		t.Fatal(err)
	}

	if got := dev.Descriptor.Category; got != "humidifier" {
		t.Fatalf("device category = %q", got)
	}
	if got := dev.Descriptor.SemanticType; got != "humidifier" {
		t.Fatalf("device semantic = %q", got)
	}
}

func TestDeviceDescriptorMatchesVacuumRule(t *testing.T) {
	spec := SpecInstance{
		URN:              "urn:miot-spec-v2:device:vacuum:0000A006:test-device:1",
		Name:             "vacuum",
		Description:      "Vacuum",
		DescriptionTrans: "Vacuum",
		Services: []SpecService{
			{
				IID:              2,
				Type:             "urn:miot-spec-v2:service:vacuum:00007816:test:1",
				Name:             "vacuum",
				Description:      "Vacuum",
				DescriptionTrans: "Vacuum",
				Actions: []SpecAction{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:action:start-sweep:00002801:test:1",
						Name:             "start-sweep",
						Description:      "Start Sweep",
						DescriptionTrans: "Start Sweep",
					},
					{
						IID:              2,
						Type:             "urn:miot-spec-v2:action:stop-sweeping:00002802:test:1",
						Name:             "stop-sweeping",
						Description:      "Stop Sweeping",
						DescriptionTrans: "Stop Sweeping",
					},
				},
			},
		},
	}

	dev, err := NewDevice(DeviceInfo{DID: "vacuum-1"}, spec, newStubEntityBackend())
	if err != nil {
		t.Fatal(err)
	}

	if got := dev.Descriptor.Category; got != "vacuum" {
		t.Fatalf("device category = %q", got)
	}
	if got := dev.Descriptor.SemanticType; got != "vacuum" {
		t.Fatalf("device semantic = %q", got)
	}
}

func TestEntityMapperRequiresStructuredServiceCapabilities(t *testing.T) {
	spec := SpecInstance{
		URN:              "urn:miot-spec-v2:device:test:0000A000:test-device:1",
		Name:             "test-device",
		Description:      "Test Device",
		DescriptionTrans: "Test Device",
		Services: []SpecService{
			{
				IID:              2,
				Type:             "urn:miot-spec-v2:service:air-conditioner:0000780C:test:1",
				Name:             "air-conditioner",
				Description:      "Air Conditioner",
				DescriptionTrans: "Air Conditioner",
				Properties: []SpecProperty{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:property:on:00000006:test:1",
						Name:             "on",
						Description:      "Power",
						DescriptionTrans: "Power",
						Format:           "bool",
						Readable:         true,
						Writable:         true,
					},
				},
			},
			{
				IID:              3,
				Type:             "urn:miot-spec-v2:service:fan-control:0000780C:test:1",
				Name:             "fan-control",
				Description:      "Fan Control",
				DescriptionTrans: "Fan Control",
				Properties: []SpecProperty{
					{
						IID:              1,
						Type:             "urn:miot-spec-v2:property:on:00000006:test:1",
						Name:             "on",
						Description:      "Power",
						DescriptionTrans: "Power",
						Format:           "bool",
						Readable:         true,
						Writable:         true,
					},
				},
			},
		},
	}

	dev, err := NewDevice(DeviceInfo{DID: "incomplete-service-1"}, spec, newStubEntityBackend())
	if err != nil {
		t.Fatal(err)
	}

	airConditioner := dev.EntityByKey("s:2")
	if airConditioner == nil {
		t.Fatal("expected air-conditioner service entity")
	}
	if got := airConditioner.Descriptor().Category; got != "service" {
		t.Fatalf("air-conditioner category = %q", got)
	}

	fanControl := dev.EntityByKey("s:3")
	if fanControl == nil {
		t.Fatal("expected fan-control service entity")
	}
	if got := fanControl.Descriptor().Category; got != "service" {
		t.Fatalf("fan-control category = %q", got)
	}
}

type stubEntityBackend struct {
	lastSet      SetPropertyRequest
	lastPropSub  PropertySubscription
	lastStateSub string
}

func newStubEntityBackend() *stubEntityBackend {
	return &stubEntityBackend{}
}

func (b *stubEntityBackend) DeviceOnline(context.Context, string) (bool, error) {
	return true, nil
}

func (b *stubEntityBackend) GetProperty(context.Context, PropertyQuery) (PropertyResult, error) {
	return PropertyResult{}, nil
}

func (b *stubEntityBackend) SetProperty(_ context.Context, req SetPropertyRequest) (SetPropertyResult, error) {
	b.lastSet = req
	return SetPropertyResult{DID: req.DID, SIID: req.SIID, PIID: req.PIID, Code: 0}, nil
}

func (b *stubEntityBackend) SubscribeProperty(_ context.Context, req PropertySubscription, _ PropertyEventHandler) (Subscription, error) {
	b.lastPropSub = req
	return subscriptionFunc(func() error { return nil }), nil
}

func (b *stubEntityBackend) SubscribeEvent(context.Context, EventSubscription, EventHandler) (Subscription, error) {
	return subscriptionFunc(func() error { return nil }), nil
}

func (b *stubEntityBackend) SubscribeDeviceState(_ context.Context, did string, _ DeviceStateHandler) (Subscription, error) {
	b.lastStateSub = did
	return subscriptionFunc(func() error { return nil }), nil
}

func (b *stubEntityBackend) InvokeAction(context.Context, ActionRequest) (ActionResult, error) {
	return ActionResult{Code: 0}, nil
}
