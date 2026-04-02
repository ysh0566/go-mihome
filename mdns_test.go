package miot

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"testing"
)

func TestMIPSDiscoveryParsesServiceProfile(t *testing.T) {
	record := ServiceRecord{
		Name:      "xiaomi-hub._miot-central._tcp.local.",
		Type:      MIPSMDNSType,
		Server:    "xiaomi-hub.local.",
		Addresses: []string{"192.168.1.20"},
		Port:      8883,
		Profile:   encodeProfile(123456789, []byte{0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01}, 1, true),
	}

	info, err := ParseMIPSServiceRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if info.DID != "123456789" {
		t.Fatalf("did = %q", info.DID)
	}
	if info.GroupID != "0102030405060708" {
		t.Fatalf("group_id = %q", info.GroupID)
	}
	if info.Role != 1 {
		t.Fatalf("role = %d", info.Role)
	}
	if !info.SupportsMQTT {
		t.Fatal("expected mqtt support")
	}
}

func TestNewMIPSDiscoveryBuildsDefaultServiceDiscovery(t *testing.T) {
	discovery := NewMIPSDiscovery(nil)
	if discovery == nil {
		t.Fatal("expected discovery")
	}
	if _, ok := discovery.sd.(*ZeroconfServiceDiscovery); !ok {
		t.Fatalf("service discovery = %T", discovery.sd)
	}
}

func TestMIPSDiscoveryTracksServiceUpdates(t *testing.T) {
	sd := &stubServiceDiscovery{}
	discovery := NewMIPSDiscovery(sd)

	var states []MIPSServiceState
	sub := discovery.SubscribeServiceChange("", func(event MIPSServiceEvent) {
		states = append(states, event.State)
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	if err := discovery.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	record := ServiceRecord{
		Name:      "xiaomi-hub._miot-central._tcp.local.",
		Type:      MIPSMDNSType,
		Server:    "xiaomi-hub.local.",
		Addresses: []string{"192.168.1.20"},
		Port:      8883,
		Profile:   encodeProfile(123456789, []byte{0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01}, 1, true),
	}
	sd.emit(ServiceEvent{Type: ServiceEventAdded, Record: record})
	record.Addresses = []string{"192.168.1.30"}
	sd.emit(ServiceEvent{Type: ServiceEventUpdated, Record: record})
	sd.emit(ServiceEvent{Type: ServiceEventRemoved, Record: record})

	services := discovery.Services()
	if len(services) != 0 {
		t.Fatalf("services = %#v", services)
	}
	if len(states) != 3 || states[0] != MIPSServiceStateAdded || states[1] != MIPSServiceStateUpdated || states[2] != MIPSServiceStateRemoved {
		t.Fatalf("states = %#v", states)
	}
}

type stubServiceDiscovery struct {
	handler func(ServiceEvent)
}

func (s *stubServiceDiscovery) Browse(_ context.Context, _ string, handler func(ServiceEvent)) (Subscription, error) {
	s.handler = handler
	return subscriptionFunc(func() error { return nil }), nil
}

func (s *stubServiceDiscovery) emit(event ServiceEvent) {
	if s.handler != nil {
		s.handler(event)
	}
}

func encodeProfile(did uint64, groupID []byte, role int, mqtt bool) string {
	profile := make([]byte, 23)
	binary.BigEndian.PutUint64(profile[1:9], did)
	copy(profile[9:17], groupID)
	profile[20] = byte(role << 4)
	if mqtt {
		profile[22] = 0x02
	}
	return base64.StdEncoding.EncodeToString(profile)
}
