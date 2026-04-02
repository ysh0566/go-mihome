package miot

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
)

const defaultMIPSPort = 8883

// MIPSCloudConfig configures a cloud MIPS client.
type MIPSCloudConfig struct {
	UUID        string
	CloudServer string
	AppID       string
	Token       string
	Port        int
	TLSConfig   *tls.Config
	MQTT        MQTTConn
}

// CloudMIPSClient is the typed MIoT cloud MQTT client.
type CloudMIPSClient struct {
	cfg  MIPSCloudConfig
	base *baseMIPSClient
}

// NewCloudMIPSClient creates a cloud MIPS client configuration wrapper.
func NewCloudMIPSClient(cfg MIPSCloudConfig) (*CloudMIPSClient, error) {
	if cfg.UUID == "" || cfg.CloudServer == "" || cfg.AppID == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new cloud mips client", Msg: "uuid, cloud server, and app id are required"}
	}
	if cfg.Port == 0 {
		cfg.Port = defaultMIPSPort
	}
	if cfg.MQTT == nil {
		mqtt, err := newCloudMQTTConn(cfg)
		if err != nil {
			return nil, err
		}
		cfg.MQTT = mqtt
	}
	return &CloudMIPSClient{
		cfg:  cfg,
		base: newBaseMIPSClient(cfg.MQTT, MIPSBaseConfig{}),
	}, nil
}

// Host returns the cloud MQTT broker host.
func (c *CloudMIPSClient) Host() string {
	return fmt.Sprintf("%s-ha.mqtt.io.mi.com", c.cfg.CloudServer)
}

// ClientID returns the MQTT client identifier.
func (c *CloudMIPSClient) ClientID() string {
	return "ha." + c.cfg.UUID
}

// Port returns the broker port.
func (c *CloudMIPSClient) Port() int {
	return c.cfg.Port
}

// Start connects the underlying MQTT transport.
func (c *CloudMIPSClient) Start(ctx context.Context) error {
	return c.base.Start(ctx)
}

// AwaitConnection waits for the underlying MQTT transport to reach its first connected state.
func (c *CloudMIPSClient) AwaitConnection(ctx context.Context) error {
	return awaitMQTTConnection(ctx, c.base.mqtt, "await cloud mips connection")
}

// LastConnectionError returns the most recent MQTT connection-attempt error, when the transport supports it.
func (c *CloudMIPSClient) LastConnectionError() error {
	return lastMQTTConnectionError(c.base.mqtt)
}

// Close disconnects the underlying MQTT transport.
func (c *CloudMIPSClient) Close() error {
	return c.base.Close()
}

// SubscribeConnectionState subscribes to cloud MQTT connection up/down transitions.
func (c *CloudMIPSClient) SubscribeConnectionState(fn func(bool)) Subscription {
	return c.base.SubscribeConnectionState(fn)
}

// UpdateAccessToken updates the stored cloud access token.
func (c *CloudMIPSClient) UpdateAccessToken(token string) {
	_ = c.RefreshAccessToken(context.Background(), token)
}

// RefreshAccessToken updates the stored cloud access token and refreshes the underlying MQTT credentials when supported.
func (c *CloudMIPSClient) RefreshAccessToken(ctx context.Context, token string) error {
	c.cfg.Token = token
	updater, ok := c.base.mqtt.(MQTTCredentialUpdater)
	if !ok {
		return nil
	}
	return updater.UpdateCredentials(ctx, c.cfg.AppID, token)
}

// SubscribeProperty subscribes to cloud property change broadcasts.
func (c *CloudMIPSClient) SubscribeProperty(ctx context.Context, req PropertySubscription, fn PropertyEventHandler) (Subscription, error) {
	if fn == nil {
		return subscriptionFunc(nil), nil
	}
	return c.base.SubscribeBroadcast(ctx, cloudPropertyTopic(req), func(_ string, payload []byte) {
		var msg cloudPropertyEnvelope
		if err := json.Unmarshal(payload, &msg); err != nil {
			return
		}
		fn(PropertyResult{
			DID:   req.DID,
			SIID:  msg.Params.SIID,
			PIID:  msg.Params.PIID,
			Value: msg.Params.Value,
		})
	})
}

// SubscribeEvent subscribes to cloud event broadcasts.
func (c *CloudMIPSClient) SubscribeEvent(ctx context.Context, req EventSubscription, fn EventHandler) (Subscription, error) {
	if fn == nil {
		return subscriptionFunc(nil), nil
	}
	return c.base.SubscribeBroadcast(ctx, cloudEventTopic(req), func(_ string, payload []byte) {
		var msg cloudEventEnvelope
		if err := json.Unmarshal(payload, &msg); err != nil {
			return
		}
		fn(EventOccurrence{
			DID:       req.DID,
			SIID:      msg.Params.SIID,
			EIID:      msg.Params.EIID,
			Arguments: msg.Params.Arguments,
			From:      "cloud",
		})
	})
}

// SubscribeDeviceState subscribes to cloud online/offline device-state updates.
func (c *CloudMIPSClient) SubscribeDeviceState(ctx context.Context, did string, fn DeviceStateHandler) (Subscription, error) {
	if fn == nil {
		return subscriptionFunc(nil), nil
	}
	topic := fmt.Sprintf("device/%s/state/#", did)
	return c.base.SubscribeBroadcast(ctx, topic, func(_ string, payload []byte) {
		var msg cloudDeviceStateEnvelope
		if err := json.Unmarshal(payload, &msg); err != nil {
			return
		}
		state := DeviceStateOffline
		if msg.Event == "online" {
			state = DeviceStateOnline
		}
		fn(did, state)
	})
}

func cloudPropertyTopic(req PropertySubscription) string {
	if req.SIID > 0 && req.PIID > 0 {
		return fmt.Sprintf("device/%s/up/properties_changed/%d/%d", req.DID, req.SIID, req.PIID)
	}
	return fmt.Sprintf("device/%s/up/properties_changed/#", req.DID)
}

func cloudEventTopic(req EventSubscription) string {
	if req.SIID > 0 && req.EIID > 0 {
		return fmt.Sprintf("device/%s/up/event_occured/%d/%d", req.DID, req.SIID, req.EIID)
	}
	return fmt.Sprintf("device/%s/up/event_occured/#", req.DID)
}
