package miot

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const testLANToken = "0123456789abcdef0123456789abcdef"

func TestLANPacketEncryptDecryptRoundTrip(t *testing.T) {
	dev := newTestLANDevice(t)
	params, err := json.Marshal([]PropertyQuery{{DID: dev.DID(), SIID: 2, PIID: 1}})
	if err != nil {
		t.Fatal(err)
	}

	packet, err := dev.BuildPacket(LANRequest{
		ID:     1,
		Method: "get_properties",
		Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := dev.ParsePacket(packet)
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "get_properties" {
		t.Fatalf("method = %q", got.Method)
	}
}

func TestLANClientGetPropUsesTransport(t *testing.T) {
	transport := &stubLANTransport{}
	client := NewLANClient(transport)
	cfg := LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "192.168.1.20",
		Interface: "en0",
	}
	if err := client.AddDevice(cfg); err != nil {
		t.Fatal(err)
	}

	transport.responder = func(packet []byte, ifName, ip string) ([]byte, error) {
		if ifName != "en0" || ip != "192.168.1.20" {
			t.Fatalf("request route = %s %s", ifName, ip)
		}
		dev, err := NewLANDevice(cfg)
		if err != nil {
			t.Fatal(err)
		}
		req, err := dev.ParsePacket(packet)
		if err != nil {
			t.Fatal(err)
		}
		respPayload, err := json.Marshal([]PropertyResult{{
			DID:   "123456789",
			SIID:  2,
			PIID:  1,
			Value: NewSpecValueBool(true),
			Code:  0,
		}})
		if err != nil {
			t.Fatal(err)
		}
		return dev.BuildResponsePacket(LANResponse{
			ID:     req.ID,
			Result: respPayload,
		})
	}

	result, err := client.GetProp(context.Background(), PropertyQuery{
		DID:  "123456789",
		SIID: 2,
		PIID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DID != "123456789" || result.SIID != 2 || result.PIID != 1 {
		t.Fatalf("result = %#v", result)
	}
	value, ok := result.Value.Bool()
	if !ok || !value {
		t.Fatalf("value = %#v", result.Value)
	}
}

func TestLANClientHandlePacketDispatchesPropertyEvent(t *testing.T) {
	transport := &stubLANTransport{}
	client := NewLANClient(transport)
	cfg := LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "192.168.1.20",
		Interface: "en0",
	}
	if err := client.AddDevice(cfg); err != nil {
		t.Fatal(err)
	}

	eventCh := make(chan PropertyResult, 1)
	sub := client.SubscribeProperty(PropertySubscription{
		DID:  "123456789",
		SIID: 2,
		PIID: 1,
	}, func(result PropertyResult) {
		eventCh <- result
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	dev := newTestLANDevice(t)
	params, err := json.Marshal([]PropertyResult{{
		DID:   "123456789",
		SIID:  2,
		PIID:  1,
		Value: NewSpecValueBool(true),
	}})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := dev.BuildPacket(LANRequest{
		ID:     7,
		Method: "properties_changed",
		Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.HandlePacket("123456789", packet); err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-eventCh:
		if result.DID != "123456789" || result.SIID != 2 || result.PIID != 1 {
			t.Fatalf("result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("property event not delivered")
	}
}

func TestLANClientHandlePacketDeduplicatesUplinkMessages(t *testing.T) {
	transport := &stubLANTransport{}
	client := NewLANClient(transport)
	cfg := LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "192.168.1.20",
		Interface: "en0",
	}
	if err := client.AddDevice(cfg); err != nil {
		t.Fatal(err)
	}

	eventCh := make(chan PropertyResult, 2)
	sub := client.SubscribeProperty(PropertySubscription{
		DID:  "123456789",
		SIID: 2,
		PIID: 1,
	}, func(result PropertyResult) {
		eventCh <- result
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	dev := newTestLANDevice(t)
	params, err := json.Marshal([]PropertyResult{{
		DID:   "123456789",
		SIID:  2,
		PIID:  1,
		Value: NewSpecValueBool(true),
	}})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := dev.BuildPacket(LANRequest{
		ID:     17,
		Method: "properties_changed",
		Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := client.HandlePacket("123456789", packet); err != nil {
		t.Fatal(err)
	}
	if err := client.HandlePacket("123456789", packet); err != nil {
		t.Fatal(err)
	}

	select {
	case <-eventCh:
	case <-time.After(time.Second):
		t.Fatal("property event not delivered")
	}

	select {
	case result := <-eventCh:
		t.Fatalf("duplicate event delivered = %#v", result)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLANClientSetSubscribeOptionBlocksUnsolicitedDispatch(t *testing.T) {
	client := NewLANClient(&stubLANTransport{})
	if err := client.AddDevice(LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "192.168.1.20",
		Interface: "en0",
	}); err != nil {
		t.Fatal(err)
	}

	eventCh := make(chan PropertyResult, 1)
	sub := client.SubscribeProperty(PropertySubscription{
		DID:  "123456789",
		SIID: 2,
		PIID: 1,
	}, func(result PropertyResult) {
		eventCh <- result
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	client.SetSubscribeOption(false)

	dev := newTestLANDevice(t)
	params, err := json.Marshal([]PropertyResult{{
		DID:   "123456789",
		SIID:  2,
		PIID:  1,
		Value: NewSpecValueBool(true),
	}})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := dev.BuildPacket(LANRequest{
		ID:     7,
		Method: "properties_changed",
		Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.HandlePacket("123456789", packet); err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-eventCh:
		t.Fatalf("unexpected result = %#v", result)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLANClientVoteForLANControlEmitsLANState(t *testing.T) {
	client := NewLANClient(&stubLANTransport{})
	stateCh := make(chan bool, 2)
	sub := client.SubscribeLANState(func(enabled bool) {
		stateCh <- enabled
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	client.VoteForLANControl("worker-a", true)
	client.VoteForLANControl("worker-a", false)

	first := <-stateCh
	second := <-stateCh
	if !first || second {
		t.Fatalf("states = %v, %v", first, second)
	}
}

func TestLANClientStartScansDevicesAndTracksOnlineOffline(t *testing.T) {
	transport := &stubLANTransport{}
	client := NewLANClient(transport, WithLANRuntimeConfig(LANRuntimeConfig{
		ProbeInterval:    10 * time.Millisecond,
		OfflineThreshold: 2,
		RequestTimeout:   20 * time.Millisecond,
	}))
	if err := client.AddDevice(LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "192.168.1.20",
		Interface: "en0",
	}); err != nil {
		t.Fatal(err)
	}

	stateCh := make(chan DeviceState, 4)
	sub := client.SubscribeDeviceState(func(did string, state DeviceState) {
		if did == "123456789" {
			stateCh <- state
		}
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	if err := client.Start(context.Background()); err != nil {
		if strings.Contains(err.Error(), "address already in use") {
			t.Skipf("skipping due to occupied LAN UDP port: %v", err)
		}
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	if state := waitForDeviceState(t, stateCh); state != DeviceStateOnline {
		t.Fatalf("online state = %q", state)
	}

	transport.setPingError(errors.New("timeout"))
	if state := waitForDeviceState(t, stateCh); state != DeviceStateOffline {
		t.Fatalf("offline state = %q", state)
	}

	list := client.GetDeviceList()
	if len(list) != 1 || list[0].Online {
		t.Fatalf("device list = %#v", list)
	}
}

func TestLANClientGetDeviceListTracksPushAndWildcardState(t *testing.T) {
	transport := &stubLANTransport{}
	client := NewLANClient(transport, WithLANRuntimeConfig(LANRuntimeConfig{
		ProbeInterval:    time.Hour,
		OfflineThreshold: 2,
		RequestTimeout:   20 * time.Millisecond,
	}))
	cfg := LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "192.168.1.20",
		Interface: "en0",
	}
	if err := client.AddDevice(cfg); err != nil {
		t.Fatal(err)
	}

	methodCh := make(chan string, 2)
	transport.responder = func(packet []byte, ifName, ip string) ([]byte, error) {
		dev, err := NewLANDevice(cfg)
		if err != nil {
			t.Fatal(err)
		}
		req, err := dev.ParsePacket(packet)
		if err != nil {
			t.Fatal(err)
		}
		methodCh <- req.Method
		return dev.BuildResponsePacket(LANResponse{
			ID:     req.ID,
			Result: []byte(`{"code":0}`),
		})
	}

	sub := client.SubscribeProperty(PropertySubscription{
		DID: "123456789",
	}, func(PropertyResult) {})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	list := client.GetDeviceList()
	if len(list) != 1 {
		t.Fatalf("device list len = %d", len(list))
	}
	if list[0].PushAvailable {
		t.Fatalf("push available = %#v", list[0])
	}
	if list[0].WildcardSubscribe {
		t.Fatalf("wildcard subscribe = %#v", list[0])
	}

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	transport.emitPacket("en1", "192.168.1.30", buildLANProbePacket(t, "123456789", 33, true, 0))
	if method := waitForString(t, methodCh); method != "miIO.sub" {
		t.Fatalf("method = %q", method)
	}

	list = client.GetDeviceList()
	if !list[0].PushAvailable {
		t.Fatalf("push available after sub = %#v", list[0])
	}
	if !list[0].WildcardSubscribe {
		t.Fatalf("wildcard subscribe after probe = %#v", list[0])
	}

	client.SetSubscribeOption(false)
	if method := waitForString(t, methodCh); method != "miIO.unsub" {
		t.Fatalf("method = %q", method)
	}
	list = client.GetDeviceList()
	if list[0].PushAvailable {
		t.Fatalf("push available after disable = %#v", list[0])
	}
	if !list[0].WildcardSubscribe {
		t.Fatalf("wildcard subscribe after disable = %#v", list[0])
	}
}

func TestLANClientStartUsesTransportListenerForInboundPackets(t *testing.T) {
	transport := &stubLANTransport{}
	client := NewLANClient(transport, WithLANRuntimeConfig(LANRuntimeConfig{
		ProbeInterval:    time.Hour,
		OfflineThreshold: 2,
		RequestTimeout:   20 * time.Millisecond,
	}))
	if err := client.AddDevice(LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "192.168.1.20",
		Interface: "en0",
	}); err != nil {
		t.Fatal(err)
	}

	eventCh := make(chan PropertyResult, 1)
	sub := client.SubscribeProperty(PropertySubscription{
		DID:  "123456789",
		SIID: 2,
		PIID: 1,
	}, func(result PropertyResult) {
		eventCh <- result
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	if err := client.Start(context.Background()); err != nil {
		if strings.Contains(err.Error(), "address already in use") {
			t.Skipf("skipping due to occupied LAN UDP port: %v", err)
		}
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	dev := newTestLANDevice(t)
	params, err := json.Marshal([]PropertyResult{{
		DID:   "123456789",
		SIID:  2,
		PIID:  1,
		Value: NewSpecValueBool(true),
	}})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := dev.BuildPacket(LANRequest{
		ID:     7,
		Method: "properties_changed",
		Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}

	transport.emitPacket("en1", "192.168.1.30", packet)

	select {
	case result := <-eventCh:
		if result.DID != "123456789" || result.SIID != 2 || result.PIID != 1 {
			t.Fatalf("result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("property event not delivered")
	}

	list := client.GetDeviceList()
	if len(list) != 1 {
		t.Fatalf("device list len = %d", len(list))
	}
	if list[0].Interface != "en1" || list[0].IP != "192.168.1.30" {
		t.Fatalf("device route = %#v", list[0])
	}
	if !list[0].Online {
		t.Fatalf("device online = %#v", list[0])
	}
}

func TestLANClientDefaultUDPTransportListensForInboundPackets(t *testing.T) {
	client := NewLANClient(nil, WithLANRuntimeConfig(LANRuntimeConfig{
		ProbeInterval:    time.Hour,
		OfflineThreshold: 2,
		RequestTimeout:   20 * time.Millisecond,
	}))
	if err := client.AddDevice(LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "127.0.0.1",
		Interface: "",
	}); err != nil {
		t.Fatal(err)
	}

	eventCh := make(chan PropertyResult, 1)
	sub := client.SubscribeProperty(PropertySubscription{
		DID:  "123456789",
		SIID: 2,
		PIID: 1,
	}, func(result PropertyResult) {
		eventCh <- result
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	if err := client.Start(context.Background()); err != nil {
		if strings.Contains(err.Error(), "address already in use") {
			t.Skipf("skipping due to occupied LAN UDP port: %v", err)
		}
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	dev := newTestLANDevice(t)
	params, err := json.Marshal([]PropertyResult{{
		DID:   "123456789",
		SIID:  2,
		PIID:  1,
		Value: NewSpecValueBool(true),
	}})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := dev.BuildPacket(LANRequest{
		ID:     7,
		Method: "properties_changed",
		Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}

	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: lanPort})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write(packet); err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-eventCh:
		if result.DID != "123456789" || result.SIID != 2 || result.PIID != 1 {
			t.Fatalf("result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("property event not delivered")
	}
}

func TestLANClientRepliesToUplinkMessagesWhenTransportSupportsResponder(t *testing.T) {
	transport := &stubLANTransport{}
	client := NewLANClient(transport, WithLANRuntimeConfig(LANRuntimeConfig{
		ProbeInterval:    time.Hour,
		OfflineThreshold: 2,
		RequestTimeout:   20 * time.Millisecond,
	}))
	cfg := LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "192.168.1.20",
		Interface: "en0",
	}
	if err := client.AddDevice(cfg); err != nil {
		t.Fatal(err)
	}

	replyCh := make(chan []byte, 1)
	transport.reply = func(ifName, ip string, packet []byte) error {
		if ifName != "en1" || ip != "192.168.1.30" {
			t.Fatalf("reply route = %s %s", ifName, ip)
		}
		replyCh <- append([]byte(nil), packet...)
		return nil
	}

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	dev := newTestLANDevice(t)
	params, err := json.Marshal([]PropertyResult{{
		DID:   "123456789",
		SIID:  2,
		PIID:  1,
		Value: NewSpecValueBool(true),
	}})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := dev.BuildPacket(LANRequest{
		ID:     29,
		Method: "properties_changed",
		Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}

	transport.emitPacket("en1", "192.168.1.30", packet)

	var replyPacket []byte
	select {
	case replyPacket = <-replyCh:
	case <-time.After(time.Second):
		t.Fatal("reply packet not delivered")
	}

	reply, err := dev.ParsePacket(replyPacket)
	if err != nil {
		t.Fatal(err)
	}
	if reply.ID != 29 {
		t.Fatalf("reply id = %d", reply.ID)
	}
	var result struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 {
		t.Fatalf("reply result = %#v", result)
	}
}

func TestLANClientRuntimeUsesAdaptiveKeepaliveAndFastRetryRecovery(t *testing.T) {
	transport := &stubLANTransport{}
	client := NewLANClient(transport, WithLANRuntimeConfig(LANRuntimeConfig{
		ProbeInterval:        5 * time.Millisecond,
		FastRetryInterval:    5 * time.Millisecond,
		KeepaliveIntervalMin: 25 * time.Millisecond,
		KeepaliveIntervalMax: 25 * time.Millisecond,
		OfflineThreshold:     3,
		RequestTimeout:       20 * time.Millisecond,
	}))
	if err := client.AddDevice(LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "192.168.1.20",
		Interface: "en0",
	}); err != nil {
		t.Fatal(err)
	}

	stateCh := make(chan DeviceState, 4)
	sub := client.SubscribeDeviceState(func(did string, state DeviceState) {
		if did == "123456789" {
			stateCh <- state
		}
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	if state := waitForDeviceState(t, stateCh); state != DeviceStateOnline {
		t.Fatalf("online state = %q", state)
	}
	if got := transport.pingCountValue(); got != 1 {
		t.Fatalf("ping count after initial online = %d", got)
	}

	time.Sleep(12 * time.Millisecond)
	if got := transport.pingCountValue(); got != 1 {
		t.Fatalf("ping count before keepalive due = %d", got)
	}

	transport.setPingError(errors.New("timeout"))
	if state := waitForDeviceState(t, stateCh); state != DeviceStateOffline {
		t.Fatalf("offline state = %q", state)
	}
	if got := transport.pingCountValue(); got != 4 {
		t.Fatalf("ping count after fast retries = %d", got)
	}

	transport.setPingError(nil)
	if state := waitForDeviceState(t, stateCh); state != DeviceStateOnline {
		t.Fatalf("recovered online state = %q", state)
	}
}

func TestLANClientBroadcastScanUpdatesInterfacesAndLearnsRoute(t *testing.T) {
	transport := &stubLANTransport{}
	client := NewLANClient(transport, WithLANRuntimeConfig(LANRuntimeConfig{
		ProbeInterval:        5 * time.Millisecond,
		FastRetryInterval:    5 * time.Millisecond,
		KeepaliveIntervalMin: 25 * time.Millisecond,
		KeepaliveIntervalMax: 25 * time.Millisecond,
		BroadcastIntervalMin: 5 * time.Millisecond,
		BroadcastIntervalMax: 5 * time.Millisecond,
		OfflineThreshold:     3,
		RequestTimeout:       20 * time.Millisecond,
	}))
	if err := client.AddDevice(LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "",
		Interface: "",
	}); err != nil {
		t.Fatal(err)
	}
	client.UpdateInterfaces([]string{"en0", "en1"})

	broadcastCh := make(chan string, 4)
	transport.broadcast = func(ifName string, packet []byte) error {
		broadcastCh <- ifName
		if ifName == "en1" {
			transport.emitPacket("en1", "192.168.1.30", buildLANProbePacket(t, "123456789", 41, true, 0))
		}
		return nil
	}

	stateCh := make(chan DeviceState, 2)
	sub := client.SubscribeDeviceState(func(did string, state DeviceState) {
		if did == "123456789" {
			stateCh <- state
		}
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	if got := waitForString(t, broadcastCh); got != "en0" && got != "en1" {
		t.Fatalf("unexpected broadcast interface = %q", got)
	}
	if got := waitForString(t, broadcastCh); got != "en0" && got != "en1" {
		t.Fatalf("unexpected broadcast interface = %q", got)
	}
	if state := waitForDeviceState(t, stateCh); state != DeviceStateOnline {
		t.Fatalf("online state = %q", state)
	}

	list := client.GetDeviceList()
	if len(list) != 1 {
		t.Fatalf("device list len = %d", len(list))
	}
	if list[0].Interface != "en1" || list[0].IP != "192.168.1.30" {
		t.Fatalf("device route = %#v", list[0])
	}
	if updated := transport.interfaceSnapshot(); len(updated) != 2 || updated[0] != "en0" || updated[1] != "en1" {
		t.Fatalf("updated interfaces = %#v", updated)
	}
}

func TestLANClientSuppressesRapidOnlineRecoveryAfterFlapping(t *testing.T) {
	client := NewLANClient(&stubLANTransport{}, WithLANRuntimeConfig(LANRuntimeConfig{
		OfflineThreshold:            1,
		UnstableTransitionThreshold: 3,
		UnstableWindow:              100 * time.Millisecond,
		UnstableResumeDelay:         60 * time.Millisecond,
	}))
	if err := client.AddDevice(LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "192.168.1.20",
		Interface: "en0",
	}); err != nil {
		t.Fatal(err)
	}

	stateCh := make(chan DeviceState, 4)
	sub := client.SubscribeDeviceState(func(did string, state DeviceState) {
		if did == "123456789" {
			stateCh <- state
		}
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	client.recordPingSuccess("123456789")
	if state := waitForDeviceState(t, stateCh); state != DeviceStateOnline {
		t.Fatalf("initial online state = %q", state)
	}

	client.recordPingFailure("123456789")
	if state := waitForDeviceState(t, stateCh); state != DeviceStateOffline {
		t.Fatalf("offline state = %q", state)
	}

	client.recordPingSuccess("123456789")
	select {
	case state := <-stateCh:
		t.Fatalf("unexpected recovery state = %q", state)
	case <-time.After(20 * time.Millisecond):
	}

	list := client.GetDeviceList()
	if len(list) != 1 || list[0].Online {
		t.Fatalf("device list during suppression = %#v", list)
	}

	time.Sleep(20 * time.Millisecond)
	client.recordPingSuccess("123456789")
	select {
	case state := <-stateCh:
		t.Fatalf("unexpected early recovery state = %q", state)
	case <-time.After(20 * time.Millisecond):
	}

	time.Sleep(50 * time.Millisecond)
	client.recordPingSuccess("123456789")
	if state := waitForDeviceState(t, stateCh); state != DeviceStateOnline {
		t.Fatalf("recovered online state = %q", state)
	}
}

func TestLANClientBindNetworkMonitorSyncsInterfaces(t *testing.T) {
	transport := &stubLANTransport{}
	client := NewLANClient(transport)

	inspector := &stubNetworkInspector{
		snapshots: [][]NetworkInfo{
			{
				{Name: "en0", IP: "192.168.1.2", Netmask: "255.255.255.0", NetworkSegment: "192.168.1.0"},
				{Name: "en1", IP: "10.0.0.2", Netmask: "255.255.255.0", NetworkSegment: "10.0.0.0"},
			},
			{
				{Name: "en1", IP: "10.0.0.3", Netmask: "255.255.255.0", NetworkSegment: "10.0.0.0"},
			},
		},
	}
	checker := &stubReachabilityChecker{
		results: [][]ReachabilityResult{
			{{Kind: ReachabilityTargetIP, Address: "8.8.8.8", Reachable: true}},
			{{Kind: ReachabilityTargetIP, Address: "8.8.8.8", Reachable: true}},
		},
	}
	monitor := NewNetworkMonitor(inspector, checker)
	monitor.UpdateTargets([]string{"8.8.8.8"}, nil)
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	sub, err := client.BindNetworkMonitor(monitor)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	if got := transport.interfaceSnapshot(); len(got) != 2 || got[0] != "en0" || got[1] != "en1" {
		t.Fatalf("initial interfaces = %#v", got)
	}

	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := transport.interfaceSnapshot(); len(got) != 1 || got[0] != "en1" {
		t.Fatalf("updated interfaces = %#v", got)
	}
}

func TestLANClientBindMIPSDiscoverySuppresssAndRestoresLANControl(t *testing.T) {
	transport := &stubLANTransport{}
	client := NewLANClient(transport, WithLANRuntimeConfig(LANRuntimeConfig{
		ProbeInterval:    10 * time.Millisecond,
		OfflineThreshold: 1,
		RequestTimeout:   20 * time.Millisecond,
	}))
	if err := client.AddDevice(LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "192.168.1.20",
		Interface: "en0",
	}); err != nil {
		t.Fatal(err)
	}

	sd := &stubServiceDiscovery{}
	discovery := NewMIPSDiscovery(sd)
	if err := discovery.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := discovery.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	sub, err := client.BindMIPSDiscovery(discovery)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	lanStateCh := make(chan bool, 4)
	lanSub := client.SubscribeLANState(func(enabled bool) {
		lanStateCh <- enabled
	})
	defer func() {
		if err := lanSub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	record := ServiceRecord{
		Name:      "xiaomi-hub._miot-central._tcp.local.",
		Type:      MIPSMDNSType,
		Server:    "xiaomi-hub.local.",
		Addresses: []string{"192.168.1.20"},
		Port:      8883,
		Profile:   encodeProfile(123456789, []byte{0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01}, 1, true),
	}
	sd.emit(ServiceEvent{Type: ServiceEventAdded, Record: record})
	if enabled := waitForBool(t, lanStateCh); enabled {
		t.Fatalf("lan state after service add = %v", enabled)
	}
	if _, err := client.GetProp(context.Background(), PropertyQuery{DID: "123456789", SIID: 2, PIID: 1}); err == nil {
		t.Fatal("expected lan control to be suppressed")
	}

	sd.emit(ServiceEvent{Type: ServiceEventRemoved, Record: record})
	if enabled := waitForBool(t, lanStateCh); !enabled {
		t.Fatalf("lan state after service remove = %v", enabled)
	}
}

type stubLANTransport struct {
	mu         sync.Mutex
	pingErr    error
	pingCount  int
	responder  func(packet []byte, ifName, ip string) ([]byte, error)
	listener   func(ifName, ip string, packet []byte)
	broadcast  func(ifName string, packet []byte) error
	reply      func(ifName, ip string, packet []byte) error
	interfaces []string
}

func (s *stubLANTransport) Request(_ context.Context, ifName, ip string, packet []byte) ([]byte, error) {
	if s.responder == nil {
		return nil, nil
	}
	return s.responder(packet, ifName, ip)
}

func (s *stubLANTransport) Ping(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pingCount++
	return s.pingErr
}

func (s *stubLANTransport) setPingError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pingErr = err
}

func (s *stubLANTransport) pingCountValue() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pingCount
}

func (s *stubLANTransport) Listen(_ context.Context, handler func(ifName, ip string, packet []byte)) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listener = handler
	return subscriptionFunc(func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.listener = nil
		return nil
	}), nil
}

func (s *stubLANTransport) Broadcast(_ context.Context, ifName string, packet []byte) error {
	s.mu.Lock()
	broadcast := s.broadcast
	s.mu.Unlock()
	if broadcast == nil {
		return nil
	}
	return broadcast(ifName, packet)
}

func (s *stubLANTransport) Reply(_ context.Context, ifName, ip string, packet []byte) error {
	s.mu.Lock()
	reply := s.reply
	s.mu.Unlock()
	if reply == nil {
		return nil
	}
	return reply(ifName, ip, packet)
}

func (s *stubLANTransport) UpdateInterfaces(ifNames []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interfaces = append([]string(nil), ifNames...)
	return nil
}

func (s *stubLANTransport) emitPacket(ifName, ip string, packet []byte) {
	s.mu.Lock()
	listener := s.listener
	s.mu.Unlock()
	if listener != nil {
		listener(ifName, ip, packet)
	}
}

func (s *stubLANTransport) interfaceSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.interfaces...)
}

func newTestLANDevice(t *testing.T) *LANDevice {
	t.Helper()

	dev, err := NewLANDevice(LANDeviceConfig{
		DID:       "123456789",
		Token:     testLANToken,
		IP:        "192.168.1.20",
		Interface: "en0",
	})
	if err != nil {
		t.Fatal(err)
	}
	return dev
}

func waitForDeviceState(t *testing.T, ch <-chan DeviceState) DeviceState {
	t.Helper()
	select {
	case state := <-ch:
		return state
	case <-time.After(time.Second):
		t.Fatal("device state not delivered")
	}
	return ""
}

func waitForString(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case item := <-ch:
		return item
	case <-time.After(time.Second):
		t.Fatal("string event not delivered")
	}
	return ""
}

func waitForBool(t *testing.T, ch <-chan bool) bool {
	t.Helper()
	select {
	case item := <-ch:
		return item
	case <-time.After(time.Second):
		t.Fatal("bool event not delivered")
	}
	return false
}

func buildLANProbePacket(t *testing.T, did string, updateTS uint32, wildcard bool, subType byte) []byte {
	t.Helper()

	packet := make([]byte, lanHeaderLength)
	binary.BigEndian.PutUint16(packet[0:2], lanPacketMagic)
	binary.BigEndian.PutUint16(packet[2:4], lanHeaderLength)
	if didUint, err := strconv.ParseUint(did, 10, 64); err != nil {
		t.Fatal(err)
	} else {
		binary.BigEndian.PutUint64(packet[4:12], didUint)
	}
	packet[16] = 'M'
	packet[17] = 'S'
	packet[18] = 'U'
	packet[19] = 'B'
	binary.BigEndian.PutUint32(packet[20:24], updateTS)
	packet[24] = 'P'
	packet[25] = 'U'
	packet[26] = 'B'
	packet[27] = subType
	if wildcard {
		packet[28] = 1
	}
	return packet
}
