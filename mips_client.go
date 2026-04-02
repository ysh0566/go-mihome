package miot

import (
	"context"
	"strings"
	"sync"
)

type mqttConnectionStateSource interface {
	SubscribeConnectionState(func(bool)) Subscription
}

// MIPSBaseConfig configures the shared behavior used by cloud and local MIPS clients.
type MIPSBaseConfig struct {
	ReplyTopic     string
	StripPrefix    string
	DecodeEnvelope bool
}

// baseMIPSClient provides shared request tracking and broadcast handling.
type baseMIPSClient struct {
	mqtt MQTTConn
	cfg  MIPSBaseConfig

	mu       sync.Mutex
	nextID   uint32
	pending  map[uint32]chan MIPSMessage
	replySub Subscription
	subs     []Subscription
}

func newBaseMIPSClient(mqtt MQTTConn, cfg MIPSBaseConfig) *baseMIPSClient {
	return &baseMIPSClient{
		mqtt:    mqtt,
		cfg:     cfg,
		nextID:  1,
		pending: make(map[uint32]chan MIPSMessage),
	}
}

func (c *baseMIPSClient) Start(ctx context.Context) error {
	if c.mqtt == nil {
		return &Error{Code: ErrInvalidArgument, Op: "mips start", Msg: "mqtt is nil"}
	}
	return c.mqtt.Connect(ctx)
}

func (c *baseMIPSClient) Close() error {
	c.mu.Lock()
	replySub := c.replySub
	c.replySub = nil
	subs := append([]Subscription(nil), c.subs...)
	c.subs = nil
	c.pending = make(map[uint32]chan MIPSMessage)
	c.mu.Unlock()

	if replySub != nil {
		_ = replySub.Close()
	}
	for _, sub := range subs {
		if sub != nil {
			_ = sub.Close()
		}
	}
	if c.mqtt != nil {
		return c.mqtt.Close()
	}
	return nil
}

func (c *baseMIPSClient) SubscribeBroadcast(ctx context.Context, topic string, handler func(string, []byte)) (Subscription, error) {
	if c.mqtt == nil {
		return nil, &Error{Code: ErrInvalidArgument, Op: "mips subscribe", Msg: "mqtt is nil"}
	}
	sub, err := c.mqtt.Subscribe(ctx, topic, func(msgTopic string, payload []byte) {
		c.dispatchBroadcast(msgTopic, payload, handler)
	})
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.subs = append(c.subs, sub)
	c.mu.Unlock()
	return sub, nil
}

func (c *baseMIPSClient) Request(ctx context.Context, topic string, msg MIPSMessage) (MIPSMessage, error) {
	if c.mqtt == nil {
		return MIPSMessage{}, &Error{Code: ErrInvalidArgument, Op: "mips request", Msg: "mqtt is nil"}
	}
	if c.cfg.ReplyTopic == "" {
		return MIPSMessage{}, &Error{Code: ErrInvalidArgument, Op: "mips request", Msg: "reply topic is empty"}
	}
	if err := c.ensureReplySubscription(ctx); err != nil {
		return MIPSMessage{}, err
	}

	replyCh := make(chan MIPSMessage, 1)
	msg.ID = c.nextMessageID()
	if msg.ReplyTopic == "" {
		msg.ReplyTopic = c.cfg.ReplyTopic
	}

	c.mu.Lock()
	c.pending[msg.ID] = replyCh
	c.mu.Unlock()

	wire := append([]byte(nil), msg.Payload...)
	if c.cfg.DecodeEnvelope {
		packed, err := msg.Pack()
		if err != nil {
			c.deletePending(msg.ID)
			return MIPSMessage{}, err
		}
		wire = packed
	}
	if err := c.mqtt.Publish(ctx, topic, wire); err != nil {
		c.deletePending(msg.ID)
		return MIPSMessage{}, err
	}

	select {
	case reply := <-replyCh:
		return reply, nil
	case <-ctx.Done():
		c.deletePending(msg.ID)
		return MIPSMessage{}, ctx.Err()
	}
}

func (c *baseMIPSClient) SubscribeConnectionState(fn func(bool)) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}
	source, ok := c.mqtt.(mqttConnectionStateSource)
	if !ok {
		return subscriptionFunc(nil)
	}
	return source.SubscribeConnectionState(fn)
}

func (c *baseMIPSClient) ensureReplySubscription(ctx context.Context) error {
	c.mu.Lock()
	if c.replySub != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	sub, err := c.mqtt.Subscribe(ctx, c.cfg.ReplyTopic, func(topic string, payload []byte) {
		c.handleReply(topic, payload)
	})
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.replySub == nil {
		c.replySub = sub
		return nil
	}
	return sub.Close()
}

func (c *baseMIPSClient) handleReply(topic string, payload []byte) {
	msg, err := c.decodeMessage(payload)
	if err != nil {
		return
	}

	c.mu.Lock()
	replyCh := c.pending[msg.ID]
	delete(c.pending, msg.ID)
	c.mu.Unlock()

	if replyCh != nil {
		replyCh <- msg
	}
}

func (c *baseMIPSClient) dispatchBroadcast(topic string, payload []byte, handler func(string, []byte)) {
	if handler == nil {
		return
	}

	msg, err := c.decodeMessage(payload)
	if err != nil {
		return
	}
	handler(c.normalizeTopic(topic), msg.Payload)
}

func (c *baseMIPSClient) decodeMessage(payload []byte) (MIPSMessage, error) {
	if !c.cfg.DecodeEnvelope {
		return MIPSMessage{Payload: append([]byte(nil), payload...)}, nil
	}
	return UnpackMIPSMessage(payload)
}

func (c *baseMIPSClient) normalizeTopic(topic string) string {
	if c.cfg.StripPrefix != "" && strings.HasPrefix(topic, c.cfg.StripPrefix) {
		return strings.TrimPrefix(topic, c.cfg.StripPrefix)
	}
	return topic
}

func (c *baseMIPSClient) nextMessageID() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	return id
}

func (c *baseMIPSClient) deletePending(id uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, id)
}
