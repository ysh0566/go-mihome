package miot

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"sort"
)

// MIPSLocalConfig configures a local gateway MIPS client.
type MIPSLocalConfig struct {
	ClientDID     string
	GroupID       string
	HomeName      string
	Host          string
	Port          int
	TLSConfig     *tls.Config
	CACertPEM     []byte
	ClientCertPEM []byte
	ClientKeyPEM  []byte
	MQTT          MQTTConn
}

// LocalMIPSClient is the typed MIoT local gateway MQTT client.
type LocalMIPSClient struct {
	cfg  MIPSLocalConfig
	base *baseMIPSClient
}

// NewLocalMIPSClient creates a local MIPS client configuration wrapper.
func NewLocalMIPSClient(cfg MIPSLocalConfig) (*LocalMIPSClient, error) {
	if cfg.ClientDID == "" || cfg.GroupID == "" || cfg.Host == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new local mips client", Msg: "client did, group id, and host are required"}
	}
	if cfg.Port == 0 {
		cfg.Port = defaultMIPSPort
	}
	if cfg.MQTT == nil {
		mqtt, err := newLocalMQTTConn(cfg)
		if err != nil {
			return nil, err
		}
		cfg.MQTT = mqtt
	}
	return &LocalMIPSClient{
		cfg: cfg,
		base: newBaseMIPSClient(cfg.MQTT, MIPSBaseConfig{
			ReplyTopic:     cfg.ClientDID + "/reply",
			StripPrefix:    cfg.ClientDID + "/",
			DecodeEnvelope: true,
		}),
	}, nil
}

// Host returns the gateway host.
func (c *LocalMIPSClient) Host() string {
	return c.cfg.Host
}

// Port returns the gateway port.
func (c *LocalMIPSClient) Port() int {
	return c.cfg.Port
}

// ClientID returns the MQTT client identifier.
func (c *LocalMIPSClient) ClientID() string {
	return c.cfg.ClientDID
}

// GroupID returns the bound home group identifier.
func (c *LocalMIPSClient) GroupID() string {
	return c.cfg.GroupID
}

// ReplyTopic returns the reply topic used for local requests.
func (c *LocalMIPSClient) ReplyTopic() string {
	return c.cfg.ClientDID + "/reply"
}

// Start connects the underlying MQTT transport.
func (c *LocalMIPSClient) Start(ctx context.Context) error {
	return c.base.Start(ctx)
}

// AwaitConnection waits for the underlying MQTT transport to reach its first connected state.
func (c *LocalMIPSClient) AwaitConnection(ctx context.Context) error {
	return awaitMQTTConnection(ctx, c.base.mqtt, "await local mips connection")
}

// LastConnectionError returns the most recent MQTT connection-attempt error, when the transport supports it.
func (c *LocalMIPSClient) LastConnectionError() error {
	return lastMQTTConnectionError(c.base.mqtt)
}

// Close disconnects the underlying MQTT transport.
func (c *LocalMIPSClient) Close() error {
	return c.base.Close()
}

// SubscribeConnectionState subscribes to local MQTT connection up/down transitions.
func (c *LocalMIPSClient) SubscribeConnectionState(fn func(bool)) Subscription {
	return c.base.SubscribeConnectionState(fn)
}

// SubscribeProperty subscribes to local property change broadcasts.
func (c *LocalMIPSClient) SubscribeProperty(ctx context.Context, req PropertySubscription, fn PropertyEventHandler) (Subscription, error) {
	if fn == nil {
		return subscriptionFunc(nil), nil
	}
	return c.base.SubscribeBroadcast(ctx, c.localPropertyTopic(req), func(_ string, payload []byte) {
		var msg localPropertyPayload
		if err := json.Unmarshal(payload, &msg); err != nil {
			return
		}
		fn(PropertyResult{
			DID:   msg.DID,
			SIID:  msg.SIID,
			PIID:  msg.PIID,
			Value: msg.Value,
		})
	})
}

// SubscribeEvent subscribes to local event broadcasts.
func (c *LocalMIPSClient) SubscribeEvent(ctx context.Context, req EventSubscription, fn EventHandler) (Subscription, error) {
	if fn == nil {
		return subscriptionFunc(nil), nil
	}
	return c.base.SubscribeBroadcast(ctx, c.localEventTopic(req), func(_ string, payload []byte) {
		var msg localEventPayload
		if err := json.Unmarshal(payload, &msg); err != nil {
			return
		}
		fn(EventOccurrence{
			DID:       msg.DID,
			SIID:      msg.SIID,
			EIID:      msg.EIID,
			Arguments: msg.Arguments,
			From:      "local",
		})
	})
}

// SubscribeDeviceListChanged subscribes to gateway device-list change broadcasts.
func (c *LocalMIPSClient) SubscribeDeviceListChanged(ctx context.Context, fn func([]string)) (Subscription, error) {
	if fn == nil {
		return subscriptionFunc(nil), nil
	}
	topic := c.cfg.ClientDID + "/appMsg/devListChange"
	return c.base.SubscribeBroadcast(ctx, topic, func(_ string, payload []byte) {
		var msg struct {
			DevList []string `json:"devList"`
		}
		if err := json.Unmarshal(payload, &msg); err != nil {
			return
		}
		if len(msg.DevList) == 0 {
			return
		}
		fn(append([]string(nil), msg.DevList...))
	})
}

// GetProp requests one property value through the local gateway.
func (c *LocalMIPSClient) GetProp(ctx context.Context, req PropertyQuery) (PropertyResult, error) {
	payload, err := json.Marshal(localGetPropRequest(req))
	if err != nil {
		return PropertyResult{}, err
	}
	reply, err := c.base.Request(ctx, "master/proxy/get", MIPSMessage{Payload: payload})
	if err != nil {
		return PropertyResult{}, err
	}
	var result localGetPropResponse
	if err := json.Unmarshal(reply.Payload, &result); err != nil {
		return PropertyResult{}, err
	}
	if result.Error != nil {
		return PropertyResult{}, &Error{Code: ErrInvalidResponse, Op: "local mips get prop", Msg: result.Error.Message}
	}
	return PropertyResult{
		DID:   req.DID,
		SIID:  req.SIID,
		PIID:  req.PIID,
		Value: result.Value,
	}, nil
}

// GetPropSafe is the serialized-safe property getter exposed for parity with the Python local MIPS client.
func (c *LocalMIPSClient) GetPropSafe(ctx context.Context, req PropertyQuery) (PropertyResult, error) {
	return c.GetProp(ctx, req)
}

// SetProp writes one property through the local gateway.
func (c *LocalMIPSClient) SetProp(ctx context.Context, req SetPropertyRequest) (SetPropertyResult, error) {
	rpc := localRPCEnvelope[[]SetPropertyRequest]{
		DID: req.DID,
		RPC: localRPC[[]SetPropertyRequest]{
			ID:     c.base.nextMessageID(),
			Method: "set_properties",
			Params: []SetPropertyRequest{req},
		},
	}
	payload, err := json.Marshal(rpc)
	if err != nil {
		return SetPropertyResult{}, err
	}
	reply, err := c.base.Request(ctx, "master/proxy/rpcReq", MIPSMessage{Payload: payload})
	if err != nil {
		return SetPropertyResult{}, err
	}
	var result localSetPropResponse
	if err := json.Unmarshal(reply.Payload, &result); err != nil {
		return SetPropertyResult{}, err
	}
	if result.Error != nil {
		return SetPropertyResult{}, &Error{Code: ErrInvalidResponse, Op: "local mips set prop", Msg: result.Error.Message}
	}
	if len(result.Result) != 1 {
		return SetPropertyResult{}, &Error{Code: ErrInvalidResponse, Op: "local mips set prop", Msg: "invalid result length"}
	}
	return result.Result[0], nil
}

// InvokeAction invokes one local MIoT action through the gateway.
func (c *LocalMIPSClient) InvokeAction(ctx context.Context, req ActionRequest) (ActionResult, error) {
	params := struct {
		DID   string      `json:"did"`
		SIID  int         `json:"siid"`
		AIID  int         `json:"aiid"`
		Input []SpecValue `json:"in"`
	}{
		DID:   req.DID,
		SIID:  req.SIID,
		AIID:  req.AIID,
		Input: req.Input,
	}
	rpc := localRPCEnvelope[any]{
		DID: req.DID,
		RPC: localRPC[any]{
			ID:     c.base.nextMessageID(),
			Method: "action",
			Params: params,
		},
	}
	payload, err := json.Marshal(rpc)
	if err != nil {
		return ActionResult{}, err
	}
	reply, err := c.base.Request(ctx, "master/proxy/rpcReq", MIPSMessage{Payload: payload})
	if err != nil {
		return ActionResult{}, err
	}
	var result localActionResponse
	if err := json.Unmarshal(reply.Payload, &result); err != nil {
		return ActionResult{}, err
	}
	if result.Error != nil {
		return ActionResult{}, &Error{Code: ErrInvalidResponse, Op: "local mips action", Msg: result.Error.Message}
	}
	return result.Result, nil
}

// GetDeviceList requests the current local device summary list.
func (c *LocalMIPSClient) GetDeviceList(ctx context.Context) ([]LocalDeviceSummary, error) {
	reply, err := c.base.Request(ctx, "master/proxy/getDevList", MIPSMessage{Payload: []byte(`{}`)})
	if err != nil {
		return nil, err
	}
	var result localDeviceListResponse
	if err := json.Unmarshal(reply.Payload, &result); err != nil {
		return nil, err
	}
	if result.Error != nil {
		return nil, &Error{Code: ErrInvalidResponse, Op: "local mips get device list", Msg: result.Error.Message}
	}

	items := make([]LocalDeviceSummary, 0, len(result.DevList))
	for did, entry := range result.DevList {
		if entry.Name == "" || entry.URN == "" || entry.Model == "" {
			continue
		}
		items = append(items, LocalDeviceSummary{
			DID:           did,
			Name:          entry.Name,
			URN:           entry.URN,
			Model:         entry.Model,
			Online:        entry.Online,
			SpecV2Access:  entry.SpecV2Access,
			PushAvailable: entry.PushAvailable,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].DID < items[j].DID
	})
	return items, nil
}

// GetActionGroupList requests the available local action-group identifiers.
func (c *LocalMIPSClient) GetActionGroupList(ctx context.Context) ([]string, error) {
	reply, err := c.base.Request(ctx, "master/proxy/getMijiaActionGroupList", MIPSMessage{Payload: []byte(`{}`)})
	if err != nil {
		return nil, err
	}
	var result localActionGroupListResponse
	if err := json.Unmarshal(reply.Payload, &result); err != nil {
		return nil, err
	}
	if result.Error != nil {
		return nil, &Error{Code: ErrInvalidResponse, Op: "local mips get action group list", Msg: result.Error.Message}
	}
	return append([]string(nil), result.Result...), nil
}

// ExecActionGroup executes one local action-group identifier through the gateway.
func (c *LocalMIPSClient) ExecActionGroup(ctx context.Context, id string) (ActionGroupExecResult, error) {
	if id == "" {
		return ActionGroupExecResult{}, &Error{Code: ErrInvalidArgument, Op: "local mips exec action group", Msg: "id is empty"}
	}
	reply, err := c.base.Request(ctx, "master/proxy/execMijiaActionGroup", MIPSMessage{
		Payload: []byte(fmt.Sprintf(`{"id":%q}`, id)),
	})
	if err != nil {
		return ActionGroupExecResult{}, err
	}
	var result localActionGroupExecResponse
	if err := json.Unmarshal(reply.Payload, &result); err != nil {
		return ActionGroupExecResult{}, err
	}
	if result.Error != nil {
		return ActionGroupExecResult{}, &Error{Code: ErrInvalidResponse, Op: "local mips exec action group", Msg: result.Error.Message}
	}
	return result.Result, nil
}

func (c *LocalMIPSClient) localPropertyTopic(req PropertySubscription) string {
	suffix := "#"
	if req.SIID > 0 && req.PIID > 0 {
		suffix = fmt.Sprintf("%d.%d", req.SIID, req.PIID)
	}
	return fmt.Sprintf("%s/appMsg/notify/iot/%s/property/%s", c.cfg.ClientDID, req.DID, suffix)
}

func (c *LocalMIPSClient) localEventTopic(req EventSubscription) string {
	suffix := "#"
	if req.SIID > 0 && req.EIID > 0 {
		suffix = fmt.Sprintf("%d.%d", req.SIID, req.EIID)
	}
	return fmt.Sprintf("%s/appMsg/notify/iot/%s/event/%s", c.cfg.ClientDID, req.DID, suffix)
}
