package miot

import (
	"context"
	"crypto/tls"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
)

func TestBaseMIPSClientRequestAndBroadcast(t *testing.T) {
	mqtt := newStubMQTTConn()
	client := newBaseMIPSClient(mqtt, MIPSBaseConfig{
		ReplyTopic:     "controller/reply",
		StripPrefix:    "controller/",
		DecodeEnvelope: true,
	})

	var broadcastTopic string
	var broadcastPayload string
	sub, err := client.SubscribeBroadcast(context.Background(), "controller/appMsg/notify/iot/123/property/#", func(topic string, payload []byte) {
		broadcastTopic = topic
		broadcastPayload = string(payload)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	replyCh := make(chan MIPSMessage, 1)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		reply, err := client.Request(ctx, "master/proxy/get", MIPSMessage{
			Payload: []byte(`{"did":"123","siid":2,"piid":1}`),
		})
		if err != nil {
			errCh <- err
			return
		}
		replyCh <- reply
	}()

	deadline := time.Now().Add(time.Second)
	for len(mqtt.published) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(mqtt.published) != 1 {
		t.Fatalf("published = %#v", mqtt.published)
	}
	wire := mqtt.published[0]
	if wire.Topic != "master/proxy/get" {
		t.Fatalf("publish topic = %q", wire.Topic)
	}

	req, err := UnpackMIPSMessage(wire.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if req.ReplyTopic != "controller/reply" {
		t.Fatalf("reply topic = %q", req.ReplyTopic)
	}

	mqtt.emit("controller/reply", mustPackMIPSMessage(t, MIPSMessage{
		ID:      req.ID,
		Payload: []byte(`{"result":true}`),
	}))
	mqtt.emit("controller/appMsg/notify/iot/123/property/2.1", mustPackMIPSMessage(t, MIPSMessage{
		ID:      99,
		Payload: []byte(`{"params":{"siid":2,"piid":1,"value":true}}`),
	}))

	select {
	case err := <-errCh:
		t.Fatal(err)
	case reply := <-replyCh:
		if string(reply.Payload) != `{"result":true}` {
			t.Fatalf("reply payload = %q", reply.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("request timed out")
	}

	if broadcastTopic != "appMsg/notify/iot/123/property/2.1" {
		t.Fatalf("broadcast topic = %q", broadcastTopic)
	}
	if broadcastPayload != `{"params":{"siid":2,"piid":1,"value":true}}` {
		t.Fatalf("broadcast payload = %q", broadcastPayload)
	}
}

func TestCloudMIPSClientBuildsBrokerIdentity(t *testing.T) {
	client, err := NewCloudMIPSClient(MIPSCloudConfig{
		UUID:        "abc123",
		CloudServer: "cn",
		AppID:       "2882303761520431603",
		Token:       "token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.Host() != "cn-ha.mqtt.io.mi.com" {
		t.Fatalf("host = %q", client.Host())
	}
	if client.ClientID() != "ha.abc123" {
		t.Fatalf("client id = %q", client.ClientID())
	}
	if client.Port() != 8883 {
		t.Fatalf("port = %d", client.Port())
	}
}

func TestCloudMIPSClientBuildsDefaultMQTTConn(t *testing.T) {
	client, err := NewCloudMIPSClient(MIPSCloudConfig{
		UUID:        "abc123",
		CloudServer: "cn",
		AppID:       "2882303761520431603",
		Token:       "access-token",
	})
	if err != nil {
		t.Fatal(err)
	}

	conn, ok := client.base.mqtt.(*PahoMQTTConn)
	if !ok {
		t.Fatalf("mqtt = %T", client.base.mqtt)
	}
	if conn.cfg.BrokerURL != "tls://cn-ha.mqtt.io.mi.com:8883" {
		t.Fatalf("broker url = %q", conn.cfg.BrokerURL)
	}
	if conn.cfg.ClientID != "ha.abc123" {
		t.Fatalf("client id = %q", conn.cfg.ClientID)
	}
	if conn.cfg.Username != "2882303761520431603" {
		t.Fatalf("username = %q", conn.cfg.Username)
	}
	if conn.cfg.Password != "access-token" {
		t.Fatalf("password = %q", conn.cfg.Password)
	}
}

func TestCloudMIPSClientUpdateAccessTokenRefreshesMQTTCredentials(t *testing.T) {
	mqtt := newStubMQTTConn()
	client, err := NewCloudMIPSClient(MIPSCloudConfig{
		UUID:        "abc123",
		CloudServer: "cn",
		AppID:       "2882303761520431603",
		Token:       "access-token",
		MQTT:        mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	client.UpdateAccessToken("new-access-token")

	if got := mqtt.username; got != "2882303761520431603" {
		t.Fatalf("username = %q", got)
	}
	if got := mqtt.password; got != "new-access-token" {
		t.Fatalf("password = %q", got)
	}
}

func TestCloudMIPSClientSubscribeConnectionState(t *testing.T) {
	mqtt := newStubMQTTConn()
	client, err := NewCloudMIPSClient(MIPSCloudConfig{
		UUID:        "abc123",
		CloudServer: "cn",
		AppID:       "2882303761520431603",
		Token:       "token",
		MQTT:        mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	var states []bool
	sub := client.SubscribeConnectionState(func(connected bool) {
		states = append(states, connected)
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	mqtt.emitConnectionState(true)
	mqtt.emitConnectionState(false)

	if !reflect.DeepEqual(states, []bool{true, false}) {
		t.Fatalf("states = %v, want [true false]", states)
	}
}

func TestLocalMIPSClientSubscribeConnectionState(t *testing.T) {
	mqtt := newStubMQTTConn()
	client, err := NewLocalMIPSClient(MIPSLocalConfig{
		ClientDID: "controller",
		GroupID:   "0102030405060708",
		Host:      "192.168.1.20",
		MQTT:      mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	var states []bool
	sub := client.SubscribeConnectionState(func(connected bool) {
		states = append(states, connected)
	})
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	mqtt.emitConnectionState(true)
	mqtt.emitConnectionState(false)

	if !reflect.DeepEqual(states, []bool{true, false}) {
		t.Fatalf("states = %v, want [true false]", states)
	}
}

func TestLocalMIPSClientSubscribeDeviceListChangedParsesChangedDIDs(t *testing.T) {
	mqtt := newStubMQTTConn()
	client, err := NewLocalMIPSClient(MIPSLocalConfig{
		ClientDID: "controller",
		GroupID:   "0102030405060708",
		Host:      "192.168.1.20",
		MQTT:      mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	sub, err := client.SubscribeDeviceListChanged(context.Background(), func(dids []string) {
		got = append([]string(nil), dids...)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	mqtt.emit("controller/appMsg/devListChange", mustPackMIPSMessage(t, MIPSMessage{
		Payload: []byte(`{"devList":["123","456"]}`),
	}))

	if !reflect.DeepEqual(got, []string{"123", "456"}) {
		t.Fatalf("got %v, want [123 456]", got)
	}
}

func TestLocalMIPSClientBuildsDefaultMQTTConnWithTLSConfig(t *testing.T) {
	client, err := NewLocalMIPSClient(MIPSLocalConfig{
		ClientDID: "controller",
		GroupID:   "0102030405060708",
		Host:      "192.168.1.20",
		Port:      8883,
		TLSConfig: testTLSConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}

	conn, ok := client.base.mqtt.(*PahoMQTTConn)
	if !ok {
		t.Fatalf("mqtt = %T", client.base.mqtt)
	}
	if conn.cfg.BrokerURL != "tls://192.168.1.20:8883" {
		t.Fatalf("broker url = %q", conn.cfg.BrokerURL)
	}
	if conn.cfg.ClientID != "controller" {
		t.Fatalf("client id = %q", conn.cfg.ClientID)
	}
	if conn.cfg.TLSConfig == nil {
		t.Fatal("expected tls config")
	}
}

func TestCloudMIPSClientBuildsDefaultMQTTConnWithCleanStart(t *testing.T) {
	client, err := NewCloudMIPSClient(MIPSCloudConfig{
		UUID:        "runtime-uuid",
		CloudServer: "cn",
		AppID:       "2882303761520251711",
		Token:       "access-token",
	})
	if err != nil {
		t.Fatal(err)
	}

	conn, ok := client.base.mqtt.(*PahoMQTTConn)
	if !ok {
		t.Fatalf("mqtt = %T", client.base.mqtt)
	}
	if conn.cfg.BrokerURL != "tls://cn-ha.mqtt.io.mi.com:8883" {
		t.Fatalf("broker url = %q", conn.cfg.BrokerURL)
	}
	if conn.cfg.ClientID != "ha.runtime-uuid" {
		t.Fatalf("client id = %q", conn.cfg.ClientID)
	}
	if conn.cfg.Username != "2882303761520251711" {
		t.Fatalf("username = %q", conn.cfg.Username)
	}
	if conn.cfg.Password != "access-token" {
		t.Fatalf("password = %q", conn.cfg.Password)
	}
	if !conn.cfg.CleanStart {
		t.Fatal("expected cloud mqtt clean start to be enabled")
	}
}

func TestPahoMQTTConnConnectRespectsCanceledContext(t *testing.T) {
	conn, err := NewPahoMQTTConn(PahoMQTTConfig{
		BrokerURL:    "tcp://example.invalid:1883",
		ClientID:     "test-client",
		PublishQoS:   defaultMQTTQoS,
		SubscribeQoS: defaultMQTTQoS,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer cancel()

	err = conn.Connect(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("connect error = %v, want context canceled", err)
	}
	if got := conn.connectionManager(); got != nil {
		t.Fatalf("connection manager = %#v, want nil", got)
	}
}

func TestPahoMQTTConnAwaitConnectionHonorsCancellation(t *testing.T) {
	conn, err := NewPahoMQTTConn(PahoMQTTConfig{
		BrokerURL:    "tcp://example.invalid:1883",
		ClientID:     "test-client",
		PublishQoS:   defaultMQTTQoS,
		SubscribeQoS: defaultMQTTQoS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer cancel()

	err = conn.AwaitConnection(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("await connection error = %v, want context canceled", err)
	}
}

func TestPahoMQTTConnAwaitConnectionWrapsTransportFailure(t *testing.T) {
	conn, err := NewPahoMQTTConn(PahoMQTTConfig{
		BrokerURL:    "tcp://example.invalid:1883",
		ClientID:     "test-client",
		PublishQoS:   defaultMQTTQoS,
		SubscribeQoS: defaultMQTTQoS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager := conn.connectionManager()
	if manager == nil {
		t.Fatal("expected connection manager")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	conn.mu.Lock()
	conn.manager = manager
	conn.mu.Unlock()

	err = conn.AwaitConnection(context.Background())
	var miotErr *Error
	if !errors.As(err, &miotErr) {
		t.Fatalf("await connection error = %v, want structured MIoT error", err)
	}
	if miotErr.Code != ErrTransportFailure {
		t.Fatalf("await connection code = %s, want %s", miotErr.Code, ErrTransportFailure)
	}
	if miotErr.Msg == "" {
		t.Fatalf("await connection message = %#v, want transport failure message", miotErr)
	}
}

func TestPahoMQTTConnCloseCancelsStartupBeforeConnectionManagerExists(t *testing.T) {
	oldFactory := newAutopahoConnection
	t.Cleanup(func() {
		newAutopahoConnection = oldFactory
	})

	started := make(chan struct{})
	newAutopahoConnection = func(ctx context.Context, cfg autopaho.ClientConfig) (*autopaho.ConnectionManager, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	conn, err := NewPahoMQTTConn(PahoMQTTConfig{
		BrokerURL:    "tcp://example.invalid:1883",
		ClientID:     "test-client",
		PublishQoS:   defaultMQTTQoS,
		SubscribeQoS: defaultMQTTQoS,
	})
	if err != nil {
		t.Fatal(err)
	}

	connectDone := make(chan error, 1)
	go func() {
		connectDone <- conn.Connect(context.Background())
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("connect did not reach autopaho.NewConnection")
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("close returned error: %v", err)
	}

	select {
	case err := <-connectDone:
		if err == nil {
			t.Fatal("connect returned nil, want startup cancellation error")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not unblock connect")
	}

	if got := conn.connectionManager(); got != nil {
		t.Fatalf("connection manager = %#v, want nil", got)
	}
}

func TestCloudMIPSClientAwaitConnectionDelegatesToMQTTAwaiter(t *testing.T) {
	mqtt := newStubAwaitMQTTConn()
	client, err := NewCloudMIPSClient(MIPSCloudConfig{
		UUID:        "abc123",
		CloudServer: "cn",
		AppID:       "2882303761520431603",
		Token:       "token",
		MQTT:        mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := client.AwaitConnection(context.Background()); err != nil {
		t.Fatalf("await connection returned error: %v", err)
	}
	if got := atomic.LoadInt32(&mqtt.awaitCalls); got != 1 {
		t.Fatalf("await calls = %d, want 1", got)
	}
}

func TestLocalMIPSClientAwaitConnectionDelegatesToMQTTAwaiter(t *testing.T) {
	mqtt := newStubAwaitMQTTConn()
	client, err := NewLocalMIPSClient(MIPSLocalConfig{
		ClientDID: "controller",
		GroupID:   "0102030405060708",
		Host:      "192.168.1.20",
		MQTT:      mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := client.AwaitConnection(context.Background()); err != nil {
		t.Fatalf("await connection returned error: %v", err)
	}
	if got := atomic.LoadInt32(&mqtt.awaitCalls); got != 1 {
		t.Fatalf("await calls = %d, want 1", got)
	}
}

func TestLocalMIPSClientAwaitConnectionReturnsUnsupportedWithoutAwaiter(t *testing.T) {
	mqtt := newStubMQTTConn()
	client, err := NewLocalMIPSClient(MIPSLocalConfig{
		ClientDID: "controller",
		GroupID:   "0102030405060708",
		Host:      "192.168.1.20",
		MQTT:      mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = client.AwaitConnection(context.Background())
	var miotErr *Error
	if !errors.As(err, &miotErr) {
		t.Fatalf("await connection error = %v, want structured MIoT error", err)
	}
	if miotErr.Code != ErrProtocolFailure {
		t.Fatalf("await connection code = %s, want %s", miotErr.Code, ErrProtocolFailure)
	}
	if miotErr == nil || miotErr.Msg == "" {
		t.Fatalf("await connection error message = %#v, want unsupported message", miotErr)
	}
}

func TestCloudMIPSClientLastConnectionErrorDelegatesToMQTTDiagnostics(t *testing.T) {
	mqtt := newStubMQTTConn()
	mqtt.lastConnectionErr = errors.New("cloud auth failed")
	client, err := NewCloudMIPSClient(MIPSCloudConfig{
		UUID:        "abc123",
		CloudServer: "cn",
		AppID:       "2882303761520431603",
		Token:       "token",
		MQTT:        mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = client.LastConnectionError()
	if err == nil || err.Error() != "cloud auth failed" {
		t.Fatalf("LastConnectionError() = %v, want cloud auth failed", err)
	}
}

func TestLocalMIPSClientLastConnectionErrorDelegatesToMQTTDiagnostics(t *testing.T) {
	mqtt := newStubMQTTConn()
	mqtt.lastConnectionErr = errors.New("gateway tls failed")
	client, err := NewLocalMIPSClient(MIPSLocalConfig{
		ClientDID: "controller",
		GroupID:   "0102030405060708",
		Host:      "192.168.1.20",
		MQTT:      mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = client.LastConnectionError()
	if err == nil || err.Error() != "gateway tls failed" {
		t.Fatalf("LastConnectionError() = %v, want gateway tls failed", err)
	}
}

func TestCloudMIPSClientSubscribePropertyParsesPayload(t *testing.T) {
	mqtt := newStubMQTTConn()
	client, err := NewCloudMIPSClient(MIPSCloudConfig{
		UUID:        "abc123",
		CloudServer: "cn",
		AppID:       "2882303761520431603",
		Token:       "token",
		MQTT:        mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	var got PropertyResult
	sub, err := client.SubscribeProperty(context.Background(), PropertySubscription{
		DID:  "123",
		SIID: 2,
		PIID: 1,
	}, func(result PropertyResult) {
		got = result
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	mqtt.emit("device/123/up/properties_changed/2/1", []byte(`{"params":{"siid":2,"piid":1,"value":true}}`))
	if got.DID != "123" || got.SIID != 2 || got.PIID != 1 {
		t.Fatalf("result = %#v", got)
	}
	value, ok := got.Value.Bool()
	if !ok || !value {
		t.Fatalf("value = %#v", got.Value)
	}
}

func TestLocalMIPSClientGetPropUsesTypedRequest(t *testing.T) {
	mqtt := newStubMQTTConn()
	client, err := NewLocalMIPSClient(MIPSLocalConfig{
		ClientDID: "controller",
		GroupID:   "0102030405060708",
		Host:      "192.168.1.20",
		Port:      8883,
		MQTT:      mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	replyCh := make(chan PropertyResult, 1)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		result, err := client.GetProp(ctx, PropertyQuery{DID: "123", SIID: 2, PIID: 1})
		if err != nil {
			errCh <- err
			return
		}
		replyCh <- result
	}()

	deadline := time.Now().Add(time.Second)
	for len(mqtt.published) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(mqtt.published) != 1 {
		t.Fatalf("published = %#v", mqtt.published)
	}
	pub := mqtt.published[0]
	if pub.Topic != "master/proxy/get" {
		t.Fatalf("topic = %q", pub.Topic)
	}
	request, err := UnpackMIPSMessage(pub.Payload)
	if err != nil {
		t.Fatal(err)
	}
	mqtt.emit("controller/reply", mustPackMIPSMessage(t, MIPSMessage{
		ID:      request.ID,
		Payload: []byte(`{"value":true}`),
	}))

	select {
	case err := <-errCh:
		t.Fatal(err)
	case result := <-replyCh:
		if result.DID != "123" || result.SIID != 2 || result.PIID != 1 {
			t.Fatalf("result = %#v", result)
		}
		value, ok := result.Value.Bool()
		if !ok || !value {
			t.Fatalf("value = %#v", result.Value)
		}
	case <-time.After(time.Second):
		t.Fatal("get prop timed out")
	}
}

func TestLocalMIPSClientGetPropSafeDelegatesToTypedRequest(t *testing.T) {
	mqtt := newStubMQTTConn()
	client, err := NewLocalMIPSClient(MIPSLocalConfig{
		ClientDID: "controller",
		GroupID:   "0102030405060708",
		Host:      "192.168.1.20",
		Port:      8883,
		MQTT:      mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	replyCh := make(chan PropertyResult, 1)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		result, err := client.GetPropSafe(ctx, PropertyQuery{DID: "123", SIID: 2, PIID: 1})
		if err != nil {
			errCh <- err
			return
		}
		replyCh <- result
	}()

	deadline := time.Now().Add(time.Second)
	for len(mqtt.published) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(mqtt.published) != 1 {
		t.Fatalf("published = %#v", mqtt.published)
	}
	request, err := UnpackMIPSMessage(mqtt.published[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	mqtt.emit("controller/reply", mustPackMIPSMessage(t, MIPSMessage{
		ID:      request.ID,
		Payload: []byte(`{"value":true}`),
	}))

	select {
	case err := <-errCh:
		t.Fatal(err)
	case result := <-replyCh:
		value, ok := result.Value.Bool()
		if !ok || !value {
			t.Fatalf("value = %#v", result.Value)
		}
	case <-time.After(time.Second):
		t.Fatal("get prop safe timed out")
	}
}

func TestLocalMIPSClientGetActionGroupListUsesTypedResponse(t *testing.T) {
	mqtt := newStubMQTTConn()
	client, err := NewLocalMIPSClient(MIPSLocalConfig{
		ClientDID: "controller",
		GroupID:   "0102030405060708",
		Host:      "192.168.1.20",
		Port:      8883,
		MQTT:      mqtt,
	})
	if err != nil {
		t.Fatal(err)
	}

	replyCh := make(chan []string, 1)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		items, err := client.GetActionGroupList(ctx)
		if err != nil {
			errCh <- err
			return
		}
		replyCh <- items
	}()

	deadline := time.Now().Add(time.Second)
	for len(mqtt.published) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(mqtt.published) != 1 {
		t.Fatalf("published = %#v", mqtt.published)
	}
	if mqtt.published[0].Topic != "master/proxy/getMijiaActionGroupList" {
		t.Fatalf("topic = %q", mqtt.published[0].Topic)
	}
	request, err := UnpackMIPSMessage(mqtt.published[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	mqtt.emit("controller/reply", mustPackMIPSMessage(t, MIPSMessage{
		ID:      request.ID,
		Payload: []byte(`{"result":["scene-a","scene-b"]}`),
	}))

	select {
	case err := <-errCh:
		t.Fatal(err)
	case items := <-replyCh:
		if len(items) != 2 || items[0] != "scene-a" || items[1] != "scene-b" {
			t.Fatalf("items = %#v", items)
		}
	case <-time.After(time.Second):
		t.Fatal("get action groups timed out")
	}
}

type stubAwaitMQTTConn struct {
	*stubMQTTConn
	awaitCalls int32
}

func newStubAwaitMQTTConn() *stubAwaitMQTTConn {
	return &stubAwaitMQTTConn{
		stubMQTTConn: newStubMQTTConn(),
	}
}

func (s *stubAwaitMQTTConn) AwaitConnection(context.Context) error {
	atomic.AddInt32(&s.awaitCalls, 1)
	return nil
}

type stubMQTTConn struct {
	published         []mqttPublication
	subs              []mqttSubscription
	username          string
	password          string
	stateSubs         map[int]func(bool)
	nextID            int
	lastConnectionErr error
}

type mqttPublication struct {
	Topic   string
	Payload []byte
}

type mqttSubscription struct {
	topic   string
	handler MQTTMessageHandler
}

func newStubMQTTConn() *stubMQTTConn {
	return &stubMQTTConn{
		stateSubs: make(map[int]func(bool)),
	}
}

func testTLSConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true}
}

func (s *stubMQTTConn) Connect(context.Context) error {
	return nil
}

func (s *stubMQTTConn) Close() error {
	return nil
}

func (s *stubMQTTConn) Publish(_ context.Context, topic string, payload []byte) error {
	s.published = append(s.published, mqttPublication{
		Topic:   topic,
		Payload: append([]byte(nil), payload...),
	})
	return nil
}

func (s *stubMQTTConn) Subscribe(_ context.Context, topic string, handler MQTTMessageHandler) (Subscription, error) {
	index := len(s.subs)
	s.subs = append(s.subs, mqttSubscription{topic: topic, handler: handler})
	return subscriptionFunc(func() error {
		s.subs[index].handler = nil
		return nil
	}), nil
}

func (s *stubMQTTConn) UpdateCredentials(_ context.Context, username, password string) error {
	s.username = username
	s.password = password
	return nil
}

func (s *stubMQTTConn) SubscribeConnectionState(fn func(bool)) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}
	id := s.nextID
	s.nextID++
	s.stateSubs[id] = fn
	return subscriptionFunc(func() error {
		delete(s.stateSubs, id)
		return nil
	})
}

func (s *stubMQTTConn) LastConnectionError() error {
	return s.lastConnectionErr
}

func (s *stubMQTTConn) emit(topic string, payload []byte) {
	for _, sub := range s.subs {
		if sub.handler != nil && MatchMQTTTopic(sub.topic, topic) {
			sub.handler(topic, payload)
		}
	}
}

func (s *stubMQTTConn) emitConnectionState(connected bool) {
	for _, fn := range s.stateSubs {
		if fn != nil {
			fn(connected)
		}
	}
}

func mustPackMIPSMessage(t *testing.T, msg MIPSMessage) []byte {
	t.Helper()

	wire, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return wire
}
