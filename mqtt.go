package miot

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

const (
	defaultMQTTKeepAlive           = 60
	defaultMQTTConnectTimeout      = 10 * time.Second
	defaultMQTTQoS            byte = 2
)

var newAutopahoConnection = autopaho.NewConnection

// MQTTConnectionAwaiter optionally blocks until the MQTT transport reaches its first connected state.
type MQTTConnectionAwaiter interface {
	AwaitConnection(ctx context.Context) error
}

// MQTTConnectionErrorSource optionally exposes the most recent MQTT connection error.
type MQTTConnectionErrorSource interface {
	LastConnectionError() error
}

// PahoMQTTConfig configures the default MQTT transport used by MIPS clients.
type PahoMQTTConfig struct {
	BrokerURL             string
	ClientID              string
	Username              string
	Password              string
	TLSConfig             *tls.Config
	KeepAlive             uint16
	ConnectTimeout        time.Duration
	CleanStart            bool
	SessionExpiryInterval uint32
	PublishQoS            byte
	SubscribeQoS          byte
}

// PahoMQTTConn is the default MQTTConn implementation backed by eclipse-paho/autopaho.
type PahoMQTTConn struct {
	cfg PahoMQTTConfig

	mu         sync.Mutex
	router     *paho.StandardRouter
	manager    *autopaho.ConnectionManager
	cancel     context.CancelFunc
	done       <-chan struct{}
	connecting bool
	subs       map[string]map[int]MQTTMessageHandler
	nextID     int

	connected      bool
	stateSubs      map[int]func(bool)
	nextStateSubID int
	lastConnectErr error
}

// NewPahoMQTTConn creates a default MQTT transport backed by eclipse-paho/autopaho.
func NewPahoMQTTConn(cfg PahoMQTTConfig) (*PahoMQTTConn, error) {
	if cfg.BrokerURL == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new paho mqtt conn", Msg: "broker url is empty"}
	}
	if cfg.ClientID == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new paho mqtt conn", Msg: "client id is empty"}
	}
	if cfg.KeepAlive == 0 {
		cfg.KeepAlive = defaultMQTTKeepAlive
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = defaultMQTTConnectTimeout
	}
	if cfg.PublishQoS == 0 {
		cfg.PublishQoS = defaultMQTTQoS
	}
	if cfg.SubscribeQoS == 0 {
		cfg.SubscribeQoS = defaultMQTTQoS
	}
	cfg.BrokerURL = normalizeBrokerURL(cfg.BrokerURL)
	return &PahoMQTTConn{
		cfg:       cfg,
		subs:      make(map[string]map[int]MQTTMessageHandler),
		stateSubs: make(map[int]func(bool)),
	}, nil
}

// Connect opens the MQTT connection and returns after the background connection loop starts.
func (c *PahoMQTTConn) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.manager != nil || c.connecting {
		c.mu.Unlock()
		return nil
	}
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return err
	}
	c.connecting = true
	cfg := c.cfg
	c.mu.Unlock()

	brokerURL, err := url.Parse(cfg.BrokerURL)
	if err != nil {
		c.mu.Lock()
		c.connecting = false
		c.mu.Unlock()
		return Wrap(ErrInvalidArgument, "parse mqtt broker url", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()

	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        cloneTLSConfig(cfg.TLSConfig),
		KeepAlive:                     cfg.KeepAlive,
		ConnectTimeout:                cfg.ConnectTimeout,
		CleanStartOnInitialConnection: cfg.CleanStart,
		SessionExpiryInterval:         cfg.SessionExpiryInterval,
		ReconnectBackoff:              autopaho.DefaultExponentialBackoff(),
		OnConnectionUp: func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
			c.setLastConnectionError(nil)
			c.setConnectionState(true)
			go c.resubscribeAll(cm)
		},
		OnConnectionDown: func() bool {
			c.setConnectionState(false)
			return true
		},
		OnConnectError: func(err error) {
			c.setLastConnectionError(err)
		},
		ConnectUsername: cfg.Username,
		ConnectPassword: []byte(cfg.Password),
		ClientConfig: paho.ClientConfig{
			ClientID: cfg.ClientID,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(pr paho.PublishReceived) (bool, error) {
					c.mu.Lock()
					router := c.router
					c.mu.Unlock()
					if router == nil {
						return true, nil
					}
					router.Route(pr.Packet.Packet())
					return true, nil
				},
			},
		},
	}

	manager, err := newAutopahoConnection(runCtx, clientConfig)
	if err != nil {
		cancel()
		c.mu.Lock()
		if c.cancel != nil {
			c.cancel = nil
		}
		c.connecting = false
		c.lastConnectErr = err
		c.mu.Unlock()
		return Wrap(ErrTransportFailure, "new mqtt connection", err)
	}

	c.mu.Lock()
	if !c.connecting {
		c.mu.Unlock()
		cancel()
		<-manager.Done()
		return &Error{Code: ErrProtocolFailure, Op: "mqtt connect", Msg: "connection closed while starting"}
	}
	router := paho.NewStandardRouter()
	for topic := range c.subs {
		c.registerRouteLocked(router, topic)
	}
	c.router = router
	c.manager = manager
	c.done = manager.Done()
	c.connecting = false
	c.mu.Unlock()
	return nil
}

// AwaitConnection waits for the MQTT transport to reach its first connected state.
func (c *PahoMQTTConn) AwaitConnection(ctx context.Context) error {
	manager := c.connectionManager()
	if manager == nil {
		return &Error{Code: ErrProtocolFailure, Op: "await mqtt connection", Msg: "connection is not started"}
	}
	err := manager.AwaitConnection(ctx)
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
		return err
	}
	return Wrap(ErrTransportFailure, "await mqtt connection", err)
}

func awaitMQTTConnection(ctx context.Context, mqtt MQTTConn, op string) error {
	if mqtt == nil {
		return &Error{Code: ErrInvalidArgument, Op: op, Msg: "mqtt is nil"}
	}
	awaiter, ok := mqtt.(MQTTConnectionAwaiter)
	if !ok {
		return &Error{Code: ErrProtocolFailure, Op: op, Msg: "mqtt transport does not support explicit connection waiting"}
	}
	if err := awaiter.AwaitConnection(ctx); err != nil {
		return err
	}
	return nil
}

func lastMQTTConnectionError(mqtt MQTTConn) error {
	if mqtt == nil {
		return nil
	}
	source, ok := mqtt.(MQTTConnectionErrorSource)
	if !ok {
		return nil
	}
	return source.LastConnectionError()
}

// Close disconnects the MQTT connection and releases background resources.
func (c *PahoMQTTConn) Close() error {
	c.mu.Lock()
	manager := c.manager
	cancel := c.cancel
	done := c.done
	wasConnected := c.connected
	c.manager = nil
	c.cancel = nil
	c.done = nil
	c.router = nil
	c.connected = false
	c.connecting = false
	c.mu.Unlock()

	var closeErr error
	if manager != nil {
		ctx, cancelDisconnect := context.WithTimeout(context.Background(), 5*time.Second)
		closeErr = manager.Disconnect(ctx)
		cancelDisconnect()
	}
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	if wasConnected {
		c.notifyConnectionState(false)
	}
	return closeErr
}

// LastConnectionError returns the most recent connection-attempt error observed by the MQTT transport.
func (c *PahoMQTTConn) LastConnectionError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastConnectErr
}

// UpdateCredentials swaps the MQTT username/password and reconnects when the transport is already running.
func (c *PahoMQTTConn) UpdateCredentials(ctx context.Context, username, password string) error {
	c.mu.Lock()
	c.cfg.Username = username
	c.cfg.Password = password
	connected := c.manager != nil
	c.mu.Unlock()

	if !connected {
		return nil
	}
	if err := c.Close(); err != nil {
		return err
	}
	return c.Connect(ctx)
}

// Publish sends one MQTT payload to the specified topic.
func (c *PahoMQTTConn) Publish(ctx context.Context, topic string, payload []byte) error {
	if topic == "" {
		return &Error{Code: ErrInvalidArgument, Op: "mqtt publish", Msg: "topic is empty"}
	}
	manager := c.connectionManager()
	if manager == nil {
		return &Error{Code: ErrProtocolFailure, Op: "mqtt publish", Msg: "connection is not started"}
	}
	_, err := manager.Publish(ctx, &paho.Publish{
		QoS:     c.cfg.PublishQoS,
		Topic:   topic,
		Payload: append([]byte(nil), payload...),
	})
	if err != nil {
		return Wrap(ErrTransportFailure, "mqtt publish", err)
	}
	return nil
}

// Subscribe registers a topic handler and ensures the broker subscription exists.
func (c *PahoMQTTConn) Subscribe(ctx context.Context, topic string, handler MQTTMessageHandler) (Subscription, error) {
	if topic == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "mqtt subscribe", Msg: "topic is empty"}
	}
	if handler == nil {
		return subscriptionFunc(nil), nil
	}

	c.mu.Lock()
	if c.subs[topic] == nil {
		c.subs[topic] = make(map[int]MQTTMessageHandler)
		if c.router != nil {
			c.registerRouteLocked(c.router, topic)
		}
	}
	id := c.nextID
	c.nextID++
	c.subs[topic][id] = handler
	manager := c.manager
	needsBrokerSub := len(c.subs[topic]) == 1
	c.mu.Unlock()

	if needsBrokerSub && manager != nil {
		if _, err := manager.Subscribe(ctx, &paho.Subscribe{
			Subscriptions: []paho.SubscribeOptions{{
				Topic: topic,
				QoS:   c.cfg.SubscribeQoS,
			}},
		}); err != nil {
			_ = c.removeSubscription(context.Background(), topic, id)
			return nil, Wrap(ErrTransportFailure, "mqtt subscribe", err)
		}
	}

	return subscriptionFunc(func() error {
		return c.removeSubscription(context.Background(), topic, id)
	}), nil
}

func (c *PahoMQTTConn) removeSubscription(ctx context.Context, topic string, id int) error {
	c.mu.Lock()
	handlers := c.subs[topic]
	if handlers == nil {
		c.mu.Unlock()
		return nil
	}
	delete(handlers, id)
	manager := c.manager
	lastHandler := len(handlers) == 0
	if lastHandler {
		delete(c.subs, topic)
		if c.router != nil {
			c.router.UnregisterHandler(topic)
		}
	}
	c.mu.Unlock()

	if lastHandler && manager != nil {
		if _, err := manager.Unsubscribe(ctx, &paho.Unsubscribe{Topics: []string{topic}}); err != nil {
			return Wrap(ErrTransportFailure, "mqtt unsubscribe", err)
		}
	}
	return nil
}

func (c *PahoMQTTConn) registerRouteLocked(router *paho.StandardRouter, topic string) {
	router.RegisterHandler(topic, func(msg *paho.Publish) {
		c.dispatch(topic, msg.Topic, msg.Payload)
	})
}

func (c *PahoMQTTConn) dispatch(subscriptionTopic, actualTopic string, payload []byte) {
	c.mu.Lock()
	handlers := c.subs[subscriptionTopic]
	fns := make([]MQTTMessageHandler, 0, len(handlers))
	for _, fn := range handlers {
		if fn != nil {
			fns = append(fns, fn)
		}
	}
	c.mu.Unlock()

	for _, fn := range fns {
		fn(actualTopic, append([]byte(nil), payload...))
	}
}

func (c *PahoMQTTConn) resubscribeAll(cm *autopaho.ConnectionManager) {
	topics := c.subscriptionTopics()
	if len(topics) == 0 {
		return
	}
	subscriptions := make([]paho.SubscribeOptions, 0, len(topics))
	for _, topic := range topics {
		subscriptions = append(subscriptions, paho.SubscribeOptions{
			Topic: topic,
			QoS:   c.cfg.SubscribeQoS,
		})
	}
	_, _ = cm.Subscribe(context.Background(), &paho.Subscribe{Subscriptions: subscriptions})
}

func (c *PahoMQTTConn) subscriptionTopics() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	topics := make([]string, 0, len(c.subs))
	for topic := range c.subs {
		topics = append(topics, topic)
	}
	return topics
}

// SubscribeConnectionState registers a callback for MQTT connection up/down transitions.
func (c *PahoMQTTConn) SubscribeConnectionState(fn func(bool)) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}

	c.mu.Lock()
	id := c.nextStateSubID
	c.nextStateSubID++
	c.stateSubs[id] = fn
	c.mu.Unlock()

	return subscriptionFunc(func() error {
		c.mu.Lock()
		delete(c.stateSubs, id)
		c.mu.Unlock()
		return nil
	})
}

func (c *PahoMQTTConn) setConnectionState(connected bool) {
	c.mu.Lock()
	if c.connected == connected {
		c.mu.Unlock()
		return
	}
	c.connected = connected
	subs := make([]func(bool), 0, len(c.stateSubs))
	for _, fn := range c.stateSubs {
		if fn != nil {
			subs = append(subs, fn)
		}
	}
	c.mu.Unlock()

	for _, fn := range subs {
		fn(connected)
	}
}

func (c *PahoMQTTConn) notifyConnectionState(connected bool) {
	c.mu.Lock()
	subs := make([]func(bool), 0, len(c.stateSubs))
	for _, fn := range c.stateSubs {
		if fn != nil {
			subs = append(subs, fn)
		}
	}
	c.mu.Unlock()

	for _, fn := range subs {
		fn(connected)
	}
}

func (c *PahoMQTTConn) setLastConnectionError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastConnectErr = err
}

func (c *PahoMQTTConn) connectionManager() *autopaho.ConnectionManager {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.manager
}

func normalizeBrokerURL(raw string) string {
	switch {
	case strings.HasPrefix(raw, "ssl://"):
		return "tls://" + strings.TrimPrefix(raw, "ssl://")
	case strings.Contains(raw, "://"):
		return raw
	default:
		return "tls://" + raw
	}
}

func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return nil
	}
	return cfg.Clone()
}

func buildTLSConfig(base *tls.Config, caPEM, certPEM, keyPEM []byte, insecureSkipVerify bool) (*tls.Config, error) {
	if base != nil {
		return base.Clone(), nil
	}
	if len(caPEM) == 0 && len(certPEM) == 0 && len(keyPEM) == 0 {
		return nil, nil
	}

	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecureSkipVerify,
	}
	if len(caPEM) > 0 {
		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM(caPEM); !ok {
			return nil, &Error{Code: ErrCertificateInvalid, Op: "build tls config", Msg: "invalid ca pem"}
		}
		cfg.RootCAs = pool
	}
	if len(certPEM) > 0 || len(keyPEM) > 0 {
		if len(certPEM) == 0 || len(keyPEM) == 0 {
			return nil, &Error{Code: ErrCertificateInvalid, Op: "build tls config", Msg: "client certificate and key must both be provided"}
		}
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, Wrap(ErrCertificateInvalid, "build tls config", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func newCloudMQTTConn(cfg MIPSCloudConfig) (MQTTConn, error) {
	tlsConfig, err := buildTLSConfig(cfg.TLSConfig, nil, nil, nil, false)
	if err != nil {
		return nil, err
	}
	return NewPahoMQTTConn(PahoMQTTConfig{
		BrokerURL:      fmt.Sprintf("tls://%s-ha.mqtt.io.mi.com:%d", cfg.CloudServer, cfg.Port),
		ClientID:       "ha." + cfg.UUID,
		Username:       cfg.AppID,
		Password:       cfg.Token,
		TLSConfig:      tlsConfig,
		CleanStart:     true,
		PublishQoS:     defaultMQTTQoS,
		SubscribeQoS:   defaultMQTTQoS,
		ConnectTimeout: defaultMQTTConnectTimeout,
		KeepAlive:      defaultMQTTKeepAlive,
	})
}

func newLocalMQTTConn(cfg MIPSLocalConfig) (MQTTConn, error) {
	tlsConfig, err := buildTLSConfig(cfg.TLSConfig, cfg.CACertPEM, cfg.ClientCertPEM, cfg.ClientKeyPEM, true)
	if err != nil {
		return nil, err
	}
	if tlsConfig == nil {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new local mqtt conn", Msg: "tls config or certificate material is required"}
	}
	return NewPahoMQTTConn(PahoMQTTConfig{
		BrokerURL:      fmt.Sprintf("tls://%s:%d", cfg.Host, cfg.Port),
		ClientID:       cfg.ClientDID,
		TLSConfig:      tlsConfig,
		PublishQoS:     defaultMQTTQoS,
		SubscribeQoS:   defaultMQTTQoS,
		ConnectTimeout: defaultMQTTConnectTimeout,
		KeepAlive:      defaultMQTTKeepAlive,
	})
}
