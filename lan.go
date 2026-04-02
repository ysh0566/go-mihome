package miot

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	stderrors "errors"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

// LANTransport executes direct LAN request/reply traffic.
type LANTransport interface {
	Request(ctx context.Context, ifName, ip string, packet []byte) ([]byte, error)
	Ping(ctx context.Context, ifName, ip string) error
}

// LANPacketListener optionally exposes inbound packet callbacks to the LAN runtime.
type LANPacketListener interface {
	Listen(ctx context.Context, handler func(ifName, ip string, packet []byte)) (Subscription, error)
}

// LANBroadcaster optionally exposes broadcast probe delivery per interface.
type LANBroadcaster interface {
	Broadcast(ctx context.Context, ifName string, packet []byte) error
}

// LANInterfaceUpdater optionally receives the current interface allow-list.
type LANInterfaceUpdater interface {
	UpdateInterfaces(ifNames []string) error
}

// LANPacketResponder optionally sends reply packets back to the source route.
type LANPacketResponder interface {
	Reply(ctx context.Context, ifName, ip string, packet []byte) error
}

// LANDeviceSummary is the public registry view returned by LANClient.
type LANDeviceSummary struct {
	DID               string
	IP                string
	Interface         string
	Online            bool
	PushAvailable     bool
	WildcardSubscribe bool
}

// LANRuntimeConfig controls background probing and offline detection.
type LANRuntimeConfig struct {
	ProbeInterval               time.Duration
	FastRetryInterval           time.Duration
	KeepaliveIntervalMin        time.Duration
	KeepaliveIntervalMax        time.Duration
	BroadcastIntervalMin        time.Duration
	BroadcastIntervalMax        time.Duration
	UnstableWindow              time.Duration
	UnstableResumeDelay         time.Duration
	UnstableTransitionThreshold int
	OfflineThreshold            int
	RequestTimeout              time.Duration
}

// LANOption configures a LAN client instance.
type LANOption func(*LANClient)

// WithLANRuntimeConfig overrides the default probing behavior.
func WithLANRuntimeConfig(cfg LANRuntimeConfig) LANOption {
	return func(c *LANClient) {
		if cfg.ProbeInterval > 0 {
			c.runtime.ProbeInterval = cfg.ProbeInterval
		}
		if cfg.FastRetryInterval > 0 {
			c.runtime.FastRetryInterval = cfg.FastRetryInterval
		}
		if cfg.KeepaliveIntervalMin > 0 {
			c.runtime.KeepaliveIntervalMin = cfg.KeepaliveIntervalMin
		}
		if cfg.KeepaliveIntervalMax > 0 {
			c.runtime.KeepaliveIntervalMax = cfg.KeepaliveIntervalMax
		}
		if cfg.BroadcastIntervalMin > 0 {
			c.runtime.BroadcastIntervalMin = cfg.BroadcastIntervalMin
		}
		if cfg.BroadcastIntervalMax > 0 {
			c.runtime.BroadcastIntervalMax = cfg.BroadcastIntervalMax
		}
		if cfg.UnstableWindow > 0 {
			c.runtime.UnstableWindow = cfg.UnstableWindow
		}
		if cfg.UnstableResumeDelay > 0 {
			c.runtime.UnstableResumeDelay = cfg.UnstableResumeDelay
		}
		if cfg.UnstableTransitionThreshold > 0 {
			c.runtime.UnstableTransitionThreshold = cfg.UnstableTransitionThreshold
		}
		if cfg.OfflineThreshold > 0 {
			c.runtime.OfflineThreshold = cfg.OfflineThreshold
		}
		if cfg.RequestTimeout > 0 {
			c.runtime.RequestTimeout = cfg.RequestTimeout
		}
	}
}

// LANClient manages packet codecs, direct request/reply calls, and LAN keepalive scans.
type LANClient struct {
	transport LANTransport

	mu                    sync.RWMutex
	devices               map[string]*LANDevice
	deviceState           map[string]*lanDeviceState
	virtualDID            string
	nextID                uint32
	propSubs              map[int]lanPropertySubscriber
	eventSubs             map[int]lanEventSubscriber
	stateSubs             map[int]DeviceStateHandler
	lanStateSubs          map[int]func(bool)
	lanVotes              map[string]bool
	enabled               bool
	centralGatewayPresent bool
	subscribeEnabled      bool
	broadcastIfs          []string
	nextBroadcast         time.Time
	broadcastBackoff      time.Duration
	recentUplink          map[string]time.Time
	nextSubID             int
	runtime               LANRuntimeConfig
	runtimeCancel         context.CancelFunc
	runtimeWG             sync.WaitGroup
	listenSub             Subscription
}

type lanPropertySubscriber struct {
	req     PropertySubscription
	handler PropertyEventHandler
}

type lanEventSubscriber struct {
	req     EventSubscription
	handler EventHandler
}

type lanDeviceState struct {
	online              bool
	pushAvailable       bool
	wildcardSupported   bool
	missedPings         int
	retryCount          int
	lastSeen            time.Time
	lastProbe           time.Time
	nextProbe           time.Time
	keepaliveInterval   time.Duration
	suppressOnlineUntil time.Time
	transitionHistory   []time.Time
	lastProbeSubTS      uint32
	lastSubscribeTS     uint32
}

type lanProbeInfo struct {
	UpdateTS          uint32
	SubType           byte
	WildcardSupported bool
	HasSubscription   bool
}

func defaultLANRuntimeConfig() LANRuntimeConfig {
	return LANRuntimeConfig{
		ProbeInterval:               30 * time.Second,
		BroadcastIntervalMin:        5 * time.Second,
		BroadcastIntervalMax:        45 * time.Second,
		UnstableWindow:              120 * time.Second,
		UnstableResumeDelay:         300 * time.Second,
		UnstableTransitionThreshold: 10,
		OfflineThreshold:            3,
		RequestTimeout:              2 * time.Second,
	}
}

// NewLANClient creates a new direct LAN client.
func NewLANClient(transport LANTransport, opts ...LANOption) *LANClient {
	if transport == nil {
		transport = newUDPLANTransport()
	}
	client := &LANClient{
		transport:        transport,
		devices:          make(map[string]*LANDevice),
		deviceState:      make(map[string]*lanDeviceState),
		virtualDID:       newLANVirtualDID(),
		nextID:           1,
		propSubs:         make(map[int]lanPropertySubscriber),
		eventSubs:        make(map[int]lanEventSubscriber),
		stateSubs:        make(map[int]DeviceStateHandler),
		lanStateSubs:     make(map[int]func(bool)),
		lanVotes:         make(map[string]bool),
		enabled:          true,
		subscribeEnabled: true,
		recentUplink:     make(map[string]time.Time),
		runtime:          defaultLANRuntimeConfig(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	return client
}

// UpdateInterfaces replaces the interface allow-list used for LAN broadcast scans.
func (c *LANClient) UpdateInterfaces(ifNames []string) error {
	normalized := normalizeInterfaceNames(ifNames)
	c.mu.Lock()
	c.broadcastIfs = normalized
	c.nextBroadcast = time.Time{}
	c.broadcastBackoff = 0
	c.mu.Unlock()

	if updater, ok := c.transport.(LANInterfaceUpdater); ok {
		return updater.UpdateInterfaces(normalized)
	}
	return nil
}

// BindNetworkMonitor keeps the LAN broadcast interface list synchronized with a NetworkMonitor.
func (c *LANClient) BindNetworkMonitor(monitor *NetworkMonitor) (Subscription, error) {
	if monitor == nil {
		return nil, &Error{Code: ErrInvalidArgument, Op: "lan bind network monitor", Msg: "monitor is nil"}
	}
	apply := func() {
		_ = c.UpdateInterfaces(interfaceNamesFromNetworkInfos(monitor.Interfaces()))
	}
	apply()
	sub := monitor.SubscribeInterfaces(func(InterfaceStatus, NetworkInfo) {
		apply()
	})
	return subscriptionFunc(func() error {
		if sub != nil {
			return sub.Close()
		}
		return nil
	}), nil
}

// BindMIPSDiscovery disables local LAN control while a central MIPS gateway is available.
func (c *LANClient) BindMIPSDiscovery(discovery *MIPSDiscovery) (Subscription, error) {
	if discovery == nil {
		return nil, &Error{Code: ErrInvalidArgument, Op: "lan bind mips discovery", Msg: "discovery is nil"}
	}
	apply := func() {
		c.setCentralGatewayPresent(len(discovery.Services()) > 0)
	}
	apply()
	sub := discovery.SubscribeServiceChange("", func(MIPSServiceEvent) {
		apply()
	})
	return subscriptionFunc(func() error {
		c.setCentralGatewayPresent(false)
		if sub != nil {
			return sub.Close()
		}
		return nil
	}), nil
}

// Start enables the LAN client and starts background probing.
func (c *LANClient) Start(context.Context) error {
	c.setEnabled(true)
	c.mu.RLock()
	blocked := c.centralGatewayPresent
	c.mu.RUnlock()
	if blocked {
		return nil
	}
	return c.startRuntime()
}

// Close disables the LAN client and stops background probing.
func (c *LANClient) Close() error {
	c.stopRuntime()
	c.markAllDevicesOffline()
	c.setEnabled(false)
	return nil
}

// AddDevice registers or updates one direct LAN device.
func (c *LANClient) AddDevice(cfg LANDeviceConfig) error {
	device, err := NewLANDevice(cfg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.devices[cfg.DID] = device
	if c.deviceState[cfg.DID] == nil {
		c.deviceState[cfg.DID] = &lanDeviceState{}
	}
	return nil
}

// UpdateDevices replaces or inserts a batch of LAN devices.
func (c *LANClient) UpdateDevices(devices []LANDeviceConfig) error {
	for _, cfg := range devices {
		if err := c.AddDevice(cfg); err != nil {
			return err
		}
	}
	return nil
}

// DeleteDevices removes devices from the registry.
func (c *LANClient) DeleteDevices(dids []string) {
	c.mu.Lock()
	stateSubs := make([]DeviceStateHandler, 0, len(c.stateSubs))
	for _, sub := range c.stateSubs {
		stateSubs = append(stateSubs, sub)
	}
	for _, did := range dids {
		delete(c.devices, did)
		delete(c.deviceState, did)
	}
	c.mu.Unlock()

	for _, did := range dids {
		for _, fn := range stateSubs {
			fn(did, DeviceStateDisable)
		}
	}
}

// GetDeviceList returns the registered LAN devices with current runtime state.
func (c *LANClient) GetDeviceList() []LANDeviceSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()

	items := make([]LANDeviceSummary, 0, len(c.devices))
	for _, device := range c.devices {
		state := c.deviceState[device.DID()]
		items = append(items, LANDeviceSummary{
			DID:               device.DID(),
			IP:                device.IP(),
			Interface:         device.Interface(),
			Online:            state != nil && state.online,
			PushAvailable:     state != nil && state.pushAvailable,
			WildcardSubscribe: state != nil && state.wildcardSupported,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].DID < items[j].DID
	})
	return items
}

// Ping sends a probe to one registered device route.
func (c *LANClient) Ping(ctx context.Context, did string) error {
	if err := c.ensureEnabled("lan ping"); err != nil {
		return err
	}
	device, err := c.device(did)
	if err != nil {
		return err
	}
	if err := c.transport.Ping(ctx, device.Interface(), device.IP()); err != nil {
		c.recordPingFailure(did)
		return err
	}
	c.recordPingSuccess(did)
	return nil
}

// GetProp reads one property directly over LAN.
func (c *LANClient) GetProp(ctx context.Context, req PropertyQuery) (PropertyResult, error) {
	if err := c.ensureEnabled("lan get prop"); err != nil {
		return PropertyResult{}, err
	}
	payload, err := json.Marshal([]PropertyQuery{req})
	if err != nil {
		return PropertyResult{}, err
	}
	msg, err := c.call(ctx, req.DID, LANRequest{
		ID:     c.nextMessageID(),
		Method: "get_properties",
		Params: payload,
	})
	if err != nil {
		return PropertyResult{}, err
	}
	var results []PropertyResult
	if err := json.Unmarshal(msg.Result, &results); err != nil {
		return PropertyResult{}, err
	}
	if len(results) != 1 {
		return PropertyResult{}, &Error{Code: ErrInvalidResponse, Op: "lan get prop", Msg: "invalid result length"}
	}
	c.recordPingSuccess(req.DID)
	return results[0], nil
}

// SetProp writes one property directly over LAN.
func (c *LANClient) SetProp(ctx context.Context, req SetPropertyRequest) (SetPropertyResult, error) {
	if err := c.ensureEnabled("lan set prop"); err != nil {
		return SetPropertyResult{}, err
	}
	payload, err := json.Marshal([]SetPropertyRequest{req})
	if err != nil {
		return SetPropertyResult{}, err
	}
	msg, err := c.call(ctx, req.DID, LANRequest{
		ID:     c.nextMessageID(),
		Method: "set_properties",
		Params: payload,
	})
	if err != nil {
		return SetPropertyResult{}, err
	}
	var results []SetPropertyResult
	if err := json.Unmarshal(msg.Result, &results); err != nil {
		return SetPropertyResult{}, err
	}
	if len(results) != 1 {
		return SetPropertyResult{}, &Error{Code: ErrInvalidResponse, Op: "lan set prop", Msg: "invalid result length"}
	}
	c.recordPingSuccess(req.DID)
	return results[0], nil
}

// InvokeAction invokes one MIoT action directly over LAN.
func (c *LANClient) InvokeAction(ctx context.Context, req ActionRequest) (ActionResult, error) {
	if err := c.ensureEnabled("lan invoke action"); err != nil {
		return ActionResult{}, err
	}
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
	payload, err := json.Marshal(params)
	if err != nil {
		return ActionResult{}, err
	}
	msg, err := c.call(ctx, req.DID, LANRequest{
		ID:     c.nextMessageID(),
		Method: "action",
		Params: payload,
	})
	if err != nil {
		return ActionResult{}, err
	}
	var result ActionResult
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return ActionResult{}, err
	}
	c.recordPingSuccess(req.DID)
	return result, nil
}

// SubscribeProperty registers a direct-LAN property change subscription.
func (c *LANClient) SubscribeProperty(req PropertySubscription, fn PropertyEventHandler) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextSubID
	c.nextSubID++
	c.propSubs[id] = lanPropertySubscriber{req: req, handler: fn}
	return subscriptionFunc(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.propSubs, id)
		return nil
	})
}

// SubscribeEvent registers a direct-LAN event subscription.
func (c *LANClient) SubscribeEvent(req EventSubscription, fn EventHandler) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextSubID
	c.nextSubID++
	c.eventSubs[id] = lanEventSubscriber{req: req, handler: fn}
	return subscriptionFunc(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.eventSubs, id)
		return nil
	})
}

// SubscribeDeviceState registers a LAN device-state subscription.
func (c *LANClient) SubscribeDeviceState(fn DeviceStateHandler) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextSubID
	c.nextSubID++
	c.stateSubs[id] = fn
	return subscriptionFunc(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.stateSubs, id)
		return nil
	})
}

// SubscribeLANState registers a callback for LAN-control enabled state changes.
func (c *LANClient) SubscribeLANState(fn func(bool)) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextSubID
	c.nextSubID++
	c.lanStateSubs[id] = fn
	return subscriptionFunc(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.lanStateSubs, id)
		return nil
	})
}

// VoteForLANControl records one voter preference and toggles LAN control accordingly.
func (c *LANClient) VoteForLANControl(key string, vote bool) {
	c.mu.Lock()
	if key != "" {
		c.lanVotes[key] = vote
	}
	enabled := false
	for _, current := range c.lanVotes {
		if current {
			enabled = true
			break
		}
	}
	blocked := c.centralGatewayPresent
	c.enabled = enabled
	subs := make([]func(bool), 0, len(c.lanStateSubs))
	for _, fn := range c.lanStateSubs {
		subs = append(subs, fn)
	}
	c.mu.Unlock()

	if enabled && !blocked {
		_ = c.startRuntime()
	} else {
		c.stopRuntime()
		c.markAllDevicesOffline()
	}
	for _, fn := range subs {
		fn(enabled && !blocked)
	}
}

// SetSubscribeOption enables or disables unsolicited property/event dispatch.
func (c *LANClient) SetSubscribeOption(enable bool) {
	c.mu.Lock()
	dids := make([]string, 0, len(c.deviceState))
	for did, state := range c.deviceState {
		if state != nil && state.pushAvailable {
			dids = append(dids, did)
		}
	}
	c.subscribeEnabled = enable
	c.mu.Unlock()

	if !enable {
		for _, did := range dids {
			c.unsubscribeDevice(context.Background(), did)
		}
	}
}

// HandlePacket parses one inbound packet and dispatches unsolicited property/event messages.
func (c *LANClient) HandlePacket(did string, packet []byte) error {
	return c.handlePacket(context.Background(), did, "", "", packet)
}

func (c *LANClient) handlePacket(ctx context.Context, did, ifName, ip string, packet []byte) error {
	if err := c.ensureEnabled("lan handle packet"); err != nil {
		return err
	}
	device, err := c.device(did)
	if err != nil {
		return err
	}
	msg, err := device.ParsePacket(packet)
	if err != nil {
		return err
	}
	c.recordPingSuccess(did)

	needsAck := msg.Method == "properties_changed" || msg.Method == "event_occured"
	if needsAck && c.recordRecentUplink(did, msg.ID) {
		c.replyToUplink(ctx, device, ifName, ip, msg.ID)
		return nil
	}
	if !c.subscriptionEnabled() {
		if needsAck {
			c.replyToUplink(ctx, device, ifName, ip, msg.ID)
		}
		return nil
	}

	switch msg.Method {
	case "properties_changed":
		var results []PropertyResult
		if err := json.Unmarshal(msg.Params, &results); err != nil {
			return err
		}
		c.dispatchProperty(results)
	case "event_occured":
		var event localEventPayload
		if err := json.Unmarshal(msg.Params, &event); err != nil {
			return err
		}
		c.dispatchEvent(EventOccurrence{
			DID:       event.DID,
			SIID:      event.SIID,
			EIID:      event.EIID,
			Arguments: event.Arguments,
			From:      "lan",
		})
	}
	if needsAck {
		c.replyToUplink(ctx, device, ifName, ip, msg.ID)
	}
	return nil
}

func (c *LANClient) call(ctx context.Context, did string, req LANRequest) (LANMessage, error) {
	device, err := c.device(did)
	if err != nil {
		return LANMessage{}, err
	}
	packet, err := device.BuildPacket(req)
	if err != nil {
		return LANMessage{}, err
	}
	reply, err := c.transport.Request(ctx, device.Interface(), device.IP(), packet)
	if err != nil {
		return LANMessage{}, err
	}
	if len(reply) == 0 {
		return LANMessage{}, &Error{Code: ErrInvalidResponse, Op: "lan call", Msg: "empty reply"}
	}
	return device.ParsePacket(reply)
}

func (c *LANClient) ensureEnabled(op string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.enabled && !c.centralGatewayPresent {
		return nil
	}
	return &Error{Code: ErrProtocolFailure, Op: op, Msg: "lan control is disabled"}
}

func (c *LANClient) subscriptionEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.subscribeEnabled
}

func (c *LANClient) setEnabled(enabled bool) {
	c.mu.Lock()
	before := c.enabled && !c.centralGatewayPresent
	if c.enabled == enabled {
		c.mu.Unlock()
		return
	}
	c.enabled = enabled
	after := c.enabled && !c.centralGatewayPresent
	subs := make([]func(bool), 0, len(c.lanStateSubs))
	for _, fn := range c.lanStateSubs {
		subs = append(subs, fn)
	}
	c.mu.Unlock()

	if before == after {
		return
	}
	for _, fn := range subs {
		fn(after)
	}
}

func (c *LANClient) setCentralGatewayPresent(present bool) {
	c.mu.Lock()
	before := c.enabled && !c.centralGatewayPresent
	if c.centralGatewayPresent == present {
		c.mu.Unlock()
		return
	}
	c.centralGatewayPresent = present
	after := c.enabled && !c.centralGatewayPresent
	subs := make([]func(bool), 0, len(c.lanStateSubs))
	for _, fn := range c.lanStateSubs {
		subs = append(subs, fn)
	}
	c.mu.Unlock()

	if after {
		_ = c.startRuntime()
	} else {
		c.stopRuntime()
		c.markAllDevicesOffline()
	}
	if before == after {
		return
	}
	for _, fn := range subs {
		fn(after)
	}
}

func (c *LANClient) startRuntime() error {
	c.mu.Lock()
	if c.runtimeCancel != nil {
		c.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.runtimeCancel = cancel
	interval := c.scanTickInterval()
	if listener, ok := c.transport.(LANPacketListener); ok {
		sub, err := listener.Listen(ctx, c.handleRuntimePacket)
		if err != nil {
			c.runtimeCancel = nil
			c.mu.Unlock()
			cancel()
			return err
		}
		c.listenSub = sub
	}
	c.runtimeWG.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.runtimeWG.Done()
		c.scanDevices(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.scanDevices(ctx)
			}
		}
	}()
	return nil
}

func (c *LANClient) stopRuntime() {
	c.mu.Lock()
	cancel := c.runtimeCancel
	c.runtimeCancel = nil
	listenSub := c.listenSub
	c.listenSub = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		c.runtimeWG.Wait()
	}
	if listenSub != nil {
		_ = listenSub.Close()
	}
}

func (c *LANClient) device(did string) (*LANDevice, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	device := c.devices[did]
	if device == nil {
		return nil, &Error{Code: ErrInvalidArgument, Op: "lan device", Msg: "device not found"}
	}
	return device, nil
}

func (c *LANClient) nextMessageID() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	return id
}

func (c *LANClient) scanDevices(ctx context.Context) {
	c.mu.Lock()
	if !c.enabled {
		c.mu.Unlock()
		return
	}
	type route struct {
		did    string
		ifName string
		ip     string
	}
	routes := make([]route, 0, len(c.devices))
	now := time.Now()
	for did, device := range c.devices {
		state := c.deviceState[did]
		if state == nil {
			state = &lanDeviceState{}
			c.deviceState[did] = state
		}
		if device.IP() == "" {
			continue
		}
		if !state.nextProbe.IsZero() && now.Before(state.nextProbe) {
			continue
		}
		state.lastProbe = now
		routes = append(routes, route{
			did:    did,
			ifName: device.Interface(),
			ip:     device.IP(),
		})
	}
	timeout := c.runtime.RequestTimeout
	broadcastIfs := c.broadcastTargetsLocked(now)
	virtualDID := c.virtualDID
	c.mu.Unlock()

	for _, route := range routes {
		pingCtx := ctx
		cancel := func() {}
		if timeout > 0 {
			pingCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		err := c.transport.Ping(pingCtx, route.ifName, route.ip)
		cancel()
		if err != nil {
			c.recordPingFailure(route.did)
			continue
		}
		c.recordPingSuccess(route.did)
	}

	c.broadcastScan(ctx, virtualDID, broadcastIfs)
}

func (c *LANClient) handleRuntimePacket(ifName, ip string, packet []byte) {
	did, err := didFromLANPacket(packet)
	if err != nil {
		return
	}

	c.mu.Lock()
	device := c.devices[did]
	if device != nil {
		device.UpdateRoute(ip, ifName)
	}
	c.mu.Unlock()
	if device == nil {
		return
	}

	if len(packet) == lanHeaderLength {
		c.recordPingSuccess(did)
		if probe, err := parseLANProbePacket(packet); err == nil {
			c.updateProbeState(did, probe)
			c.syncDeviceSubscription(context.Background(), did, probe)
		}
		return
	}
	_ = c.handlePacket(context.Background(), did, ifName, ip, packet)
}

func (c *LANClient) recordRecentUplink(did string, msgID uint32) bool {
	if msgID == 0 {
		return false
	}
	now := time.Now()
	key := did + "." + strconv.FormatUint(uint64(msgID), 10)

	c.mu.Lock()
	defer c.mu.Unlock()
	for item, expiry := range c.recentUplink {
		if !expiry.After(now) {
			delete(c.recentUplink, item)
		}
	}
	if expiry, ok := c.recentUplink[key]; ok && expiry.After(now) {
		return true
	}
	c.recentUplink[key] = now.Add(5 * time.Second)
	return false
}

func (c *LANClient) replyToUplink(ctx context.Context, device *LANDevice, ifName, ip string, msgID uint32) {
	if device == nil || ifName == "" || ip == "" || msgID == 0 {
		return
	}
	responder, ok := c.transport.(LANPacketResponder)
	if !ok {
		return
	}
	result, err := json.Marshal(struct {
		Code int `json:"code"`
	}{Code: 0})
	if err != nil {
		return
	}
	packet, err := device.BuildResponsePacket(LANResponse{
		ID:     msgID,
		Result: result,
	})
	if err != nil {
		return
	}
	_ = responder.Reply(ctx, ifName, ip, packet)
}

func (c *LANClient) dispatchProperty(results []PropertyResult) {
	c.mu.RLock()
	subs := make([]lanPropertySubscriber, 0, len(c.propSubs))
	for _, sub := range c.propSubs {
		subs = append(subs, sub)
	}
	c.mu.RUnlock()

	for _, result := range results {
		for _, sub := range subs {
			if sub.req.DID != result.DID && sub.req.DID != "" {
				continue
			}
			if sub.req.SIID != 0 && sub.req.SIID != result.SIID {
				continue
			}
			if sub.req.PIID != 0 && sub.req.PIID != result.PIID {
				continue
			}
			sub.handler(result)
		}
	}
}

func (c *LANClient) dispatchEvent(event EventOccurrence) {
	c.mu.RLock()
	subs := make([]lanEventSubscriber, 0, len(c.eventSubs))
	for _, sub := range c.eventSubs {
		subs = append(subs, sub)
	}
	c.mu.RUnlock()

	for _, sub := range subs {
		if sub.req.DID != event.DID && sub.req.DID != "" {
			continue
		}
		if sub.req.SIID != 0 && sub.req.SIID != event.SIID {
			continue
		}
		if sub.req.EIID != 0 && sub.req.EIID != event.EIID {
			continue
		}
		sub.handler(event)
	}
}

func (c *LANClient) emitState(did string, state DeviceState) {
	c.mu.RLock()
	subs := make([]DeviceStateHandler, 0, len(c.stateSubs))
	for _, fn := range c.stateSubs {
		subs = append(subs, fn)
	}
	c.mu.RUnlock()
	for _, fn := range subs {
		fn(did, state)
	}
}

func (c *LANClient) recordPingSuccess(did string) {
	c.mu.Lock()
	state := c.deviceState[did]
	if state == nil {
		state = &lanDeviceState{}
		c.deviceState[did] = state
	}
	now := time.Now()
	state.lastSeen = now
	state.lastProbe = state.lastSeen
	state.missedPings = 0
	state.retryCount = 0
	if state.keepaliveInterval <= 0 || !state.online {
		state.keepaliveInterval = c.keepaliveIntervalMin()
	} else {
		state.keepaliveInterval *= 2
		if state.keepaliveInterval < c.keepaliveIntervalMin() {
			state.keepaliveInterval = c.keepaliveIntervalMin()
		}
		if max := c.keepaliveIntervalMax(); state.keepaliveInterval > max {
			state.keepaliveInterval = max
		}
	}
	state.nextProbe = now.Add(state.keepaliveInterval)
	alreadyOnline := state.online
	if !alreadyOnline {
		if state.suppressOnlineUntil.After(now) {
			c.mu.Unlock()
			return
		}
		state.transitionHistory = c.pruneTransitionHistory(state.transitionHistory, now)
		state.transitionHistory = append(state.transitionHistory, now)
		if c.isUnstableOnlineRecovery(state.transitionHistory, now) {
			state.suppressOnlineUntil = now.Add(c.unstableResumeDelay())
			c.mu.Unlock()
			return
		}
		state.suppressOnlineUntil = time.Time{}
	}
	state.online = true
	c.mu.Unlock()
	if !alreadyOnline {
		c.emitState(did, DeviceStateOnline)
	}
}

func (c *LANClient) recordPingFailure(did string) {
	c.mu.Lock()
	state := c.deviceState[did]
	if state == nil {
		state = &lanDeviceState{}
		c.deviceState[did] = state
	}
	now := time.Now()
	state.lastProbe = now
	state.missedPings++
	state.retryCount++
	threshold := c.runtime.OfflineThreshold
	shouldEmit := state.online && threshold > 0 && state.retryCount >= threshold
	if shouldEmit {
		state.transitionHistory = c.pruneTransitionHistory(state.transitionHistory, now)
		state.transitionHistory = append(state.transitionHistory, now)
		state.online = false
		state.missedPings = 0
		state.retryCount = 0
		state.keepaliveInterval = c.keepaliveIntervalMin()
		state.nextProbe = now.Add(state.keepaliveInterval)
	} else {
		state.nextProbe = now.Add(c.fastRetryInterval())
	}
	c.mu.Unlock()
	if shouldEmit {
		c.emitState(did, DeviceStateOffline)
	}
}

func (c *LANClient) scanTickInterval() time.Duration {
	return minPositiveDuration(c.runtime.ProbeInterval, c.fastRetryInterval(), c.broadcastIntervalMin())
}

func (c *LANClient) fastRetryInterval() time.Duration {
	if c.runtime.FastRetryInterval > 0 {
		return c.runtime.FastRetryInterval
	}
	if c.runtime.ProbeInterval > 0 && c.runtime.ProbeInterval < 5*time.Second {
		return c.runtime.ProbeInterval
	}
	return 5 * time.Second
}

func (c *LANClient) keepaliveIntervalMin() time.Duration {
	if c.runtime.KeepaliveIntervalMin > 0 {
		return c.runtime.KeepaliveIntervalMin
	}
	if c.runtime.ProbeInterval > 0 {
		return c.runtime.ProbeInterval
	}
	return 10 * time.Second
}

func (c *LANClient) keepaliveIntervalMax() time.Duration {
	if c.runtime.KeepaliveIntervalMax > 0 {
		if c.runtime.KeepaliveIntervalMax < c.keepaliveIntervalMin() {
			return c.keepaliveIntervalMin()
		}
		return c.runtime.KeepaliveIntervalMax
	}
	return c.keepaliveIntervalMin() * 4
}

func (c *LANClient) broadcastIntervalMin() time.Duration {
	if c.runtime.BroadcastIntervalMin > 0 {
		return c.runtime.BroadcastIntervalMin
	}
	return 5 * time.Second
}

func (c *LANClient) broadcastIntervalMax() time.Duration {
	if c.runtime.BroadcastIntervalMax > 0 {
		if c.runtime.BroadcastIntervalMax < c.broadcastIntervalMin() {
			return c.broadcastIntervalMin()
		}
		return c.runtime.BroadcastIntervalMax
	}
	return 45 * time.Second
}

func (c *LANClient) unstableWindow() time.Duration {
	if c.runtime.UnstableWindow > 0 {
		return c.runtime.UnstableWindow
	}
	return 120 * time.Second
}

func (c *LANClient) unstableResumeDelay() time.Duration {
	if c.runtime.UnstableResumeDelay > 0 {
		return c.runtime.UnstableResumeDelay
	}
	return 300 * time.Second
}

func (c *LANClient) unstableTransitionThreshold() int {
	if c.runtime.UnstableTransitionThreshold > 0 {
		return c.runtime.UnstableTransitionThreshold
	}
	return 10
}

func (c *LANClient) broadcastTargetsLocked(now time.Time) []string {
	if len(c.broadcastIfs) == 0 {
		return nil
	}
	if !c.nextBroadcast.IsZero() && now.Before(c.nextBroadcast) {
		return nil
	}
	targets := append([]string(nil), c.broadcastIfs...)
	if c.broadcastBackoff <= 0 {
		c.broadcastBackoff = c.broadcastIntervalMin()
	} else {
		c.broadcastBackoff *= 2
		if c.broadcastBackoff < c.broadcastIntervalMin() {
			c.broadcastBackoff = c.broadcastIntervalMin()
		}
		if max := c.broadcastIntervalMax(); c.broadcastBackoff > max {
			c.broadcastBackoff = max
		}
	}
	c.nextBroadcast = now.Add(c.broadcastBackoff)
	return targets
}

func (c *LANClient) pruneTransitionHistory(history []time.Time, now time.Time) []time.Time {
	window := c.unstableWindow()
	if window <= 0 || len(history) == 0 {
		return history[:0]
	}
	items := history[:0]
	cutoff := now.Add(-window)
	for _, item := range history {
		if item.After(cutoff) {
			items = append(items, item)
		}
	}
	return items
}

func (c *LANClient) isUnstableOnlineRecovery(history []time.Time, now time.Time) bool {
	threshold := c.unstableTransitionThreshold()
	if threshold <= 0 {
		return false
	}
	if len(history) < threshold {
		return false
	}
	window := c.unstableWindow()
	if window <= 0 {
		return false
	}
	return now.Sub(history[0]) <= window
}

func (c *LANClient) broadcastScan(ctx context.Context, virtualDID string, ifNames []string) {
	if len(ifNames) == 0 {
		return
	}
	broadcaster, ok := c.transport.(LANBroadcaster)
	if !ok {
		return
	}
	packet := buildLANBroadcastProbePacket(virtualDID)
	for _, ifName := range ifNames {
		_ = broadcaster.Broadcast(ctx, ifName, packet)
	}
}

func minPositiveDuration(values ...time.Duration) time.Duration {
	best := time.Duration(0)
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if best == 0 || value < best {
			best = value
		}
	}
	if best <= 0 {
		return time.Second
	}
	return best
}

func normalizeInterfaceNames(ifNames []string) []string {
	if len(ifNames) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ifNames))
	items := make([]string, 0, len(ifNames))
	for _, ifName := range ifNames {
		if ifName == "" {
			continue
		}
		if _, ok := seen[ifName]; ok {
			continue
		}
		seen[ifName] = struct{}{}
		items = append(items, ifName)
	}
	sort.Strings(items)
	return items
}

func interfaceNamesFromNetworkInfos(infos []NetworkInfo) []string {
	if len(infos) == 0 {
		return nil
	}
	items := make([]string, 0, len(infos))
	for _, info := range infos {
		items = append(items, info.Name)
	}
	return normalizeInterfaceNames(items)
}

func (c *LANClient) markAllDevicesOffline() {
	c.mu.Lock()
	dids := make([]string, 0, len(c.deviceState))
	for did, state := range c.deviceState {
		if state == nil || !state.online {
			continue
		}
		state.online = false
		state.missedPings = 0
		dids = append(dids, did)
	}
	c.mu.Unlock()
	for _, did := range dids {
		c.emitState(did, DeviceStateOffline)
	}
}

func (c *LANClient) updateProbeState(did string, probe lanProbeInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.deviceState[did]
	if state == nil {
		state = &lanDeviceState{}
		c.deviceState[did] = state
	}
	if probe.HasSubscription {
		state.wildcardSupported = probe.WildcardSupported
		state.lastProbeSubTS = probe.UpdateTS
		if state.lastSubscribeTS != 0 && state.lastSubscribeTS != probe.UpdateTS {
			state.pushAvailable = false
		}
	}
}

func (c *LANClient) syncDeviceSubscription(ctx context.Context, did string, probe lanProbeInfo) {
	if !probe.HasSubscription || !probe.WildcardSupported {
		return
	}
	if probe.SubType != 0 && probe.SubType != 1 && probe.SubType != 4 {
		return
	}

	c.mu.RLock()
	enabled := c.subscribeEnabled
	needsPush := c.hasPushSubscriberLocked(did)
	state := c.deviceState[did]
	alreadySubscribed := state != nil && state.pushAvailable && state.lastSubscribeTS == probe.UpdateTS
	c.mu.RUnlock()

	if !enabled || !needsPush || alreadySubscribed {
		return
	}
	c.subscribeDevice(ctx, did, probe.UpdateTS)
}

func (c *LANClient) subscribeDevice(ctx context.Context, did string, probeTS uint32) {
	updateTS := uint32(time.Now().Unix())
	params := struct {
		Version   string `json:"version"`
		DID       string `json:"did"`
		UpdateTS  uint32 `json:"update_ts"`
		SubMethod string `json:"sub_method"`
	}{
		Version:   "2.0",
		DID:       c.virtualDID,
		UpdateTS:  updateTS,
		SubMethod: ".",
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return
	}
	msg, err := c.call(ctx, did, LANRequest{
		ID:     c.nextMessageID(),
		Method: "miIO.sub",
		Params: payload,
	})
	if err != nil {
		return
	}
	var result struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(msg.Result, &result); err != nil || result.Code != 0 {
		return
	}
	c.mu.Lock()
	state := c.deviceState[did]
	if state == nil {
		state = &lanDeviceState{}
		c.deviceState[did] = state
	}
	state.pushAvailable = true
	state.wildcardSupported = true
	if probeTS != 0 {
		state.lastProbeSubTS = probeTS
	}
	state.lastSubscribeTS = updateTS
	c.mu.Unlock()
}

func (c *LANClient) unsubscribeDevice(ctx context.Context, did string) {
	c.mu.RLock()
	state := c.deviceState[did]
	if state == nil || !state.pushAvailable {
		c.mu.RUnlock()
		return
	}
	updateTS := state.lastSubscribeTS
	c.mu.RUnlock()

	params := struct {
		Version   string `json:"version"`
		DID       string `json:"did"`
		UpdateTS  uint32 `json:"update_ts"`
		SubMethod string `json:"sub_method"`
	}{
		Version:   "2.0",
		DID:       c.virtualDID,
		UpdateTS:  updateTS,
		SubMethod: ".",
	}
	payload, err := json.Marshal(params)
	if err == nil {
		msg, err := c.call(ctx, did, LANRequest{
			ID:     c.nextMessageID(),
			Method: "miIO.unsub",
			Params: payload,
		})
		if err == nil {
			var result struct {
				Code int `json:"code"`
			}
			_ = json.Unmarshal(msg.Result, &result)
		}
	}

	c.mu.Lock()
	if state := c.deviceState[did]; state != nil {
		state.pushAvailable = false
	}
	c.mu.Unlock()
}

func (c *LANClient) hasPushSubscriberLocked(did string) bool {
	for _, sub := range c.propSubs {
		if sub.req.DID == did || sub.req.DID == "" {
			return true
		}
	}
	for _, sub := range c.eventSubs {
		if sub.req.DID == did || sub.req.DID == "" {
			return true
		}
	}
	return false
}

func (c *LANClient) hasWildcardSubscriberLocked(did string) bool {
	for _, sub := range c.propSubs {
		if sub.req.DID != did && sub.req.DID != "" {
			continue
		}
		if sub.req.SIID == 0 || sub.req.PIID == 0 {
			return true
		}
	}
	for _, sub := range c.eventSubs {
		if sub.req.DID != did && sub.req.DID != "" {
			continue
		}
		if sub.req.SIID == 0 || sub.req.EIID == 0 {
			return true
		}
	}
	return false
}

type udpLANTransport struct {
	mu             sync.Mutex
	handler        func(ifName, ip string, packet []byte)
	conn           *net.UDPConn
	connDone       chan struct{}
	broadcastIfs   []string
	broadcastConns map[string]*udpLANBroadcastConn
}

type udpLANBroadcastConn struct {
	ifName      string
	broadcastIP net.IP
	conn        *net.UDPConn
	done        chan struct{}
}

func newUDPLANTransport() *udpLANTransport {
	return &udpLANTransport{
		broadcastConns: make(map[string]*udpLANBroadcastConn),
	}
}

func newLANVirtualDID() string {
	var buf [8]byte
	if _, err := crand.Read(buf[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return strconv.FormatUint(binary.BigEndian.Uint64(buf[:]), 10)
}

func didFromLANPacket(packet []byte) (string, error) {
	if len(packet) < lanHeaderLength {
		return "", &Error{Code: ErrInvalidResponse, Op: "lan packet did", Msg: "packet too short"}
	}
	if binary.BigEndian.Uint16(packet[0:2]) != lanPacketMagic {
		return "", &Error{Code: ErrInvalidResponse, Op: "lan packet did", Msg: "invalid packet magic"}
	}
	return strconv.FormatUint(binary.BigEndian.Uint64(packet[4:12]), 10), nil
}

func buildLANBroadcastProbePacket(virtualDID string) []byte {
	probe := make([]byte, lanHeaderLength)
	copy(probe[:20], []byte{
		0x21, 0x31, 0x00, 0x20,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0x4D, 0x44, 0x49, 0x44,
	})
	if did, err := strconv.ParseUint(virtualDID, 10, 64); err == nil {
		binary.BigEndian.PutUint64(probe[20:28], did)
	}
	return probe
}

func parseLANProbePacket(packet []byte) (lanProbeInfo, error) {
	if len(packet) != lanHeaderLength {
		return lanProbeInfo{}, &Error{Code: ErrInvalidResponse, Op: "lan probe", Msg: "not a probe packet"}
	}
	info := lanProbeInfo{}
	if string(packet[16:20]) == "MSUB" && string(packet[24:27]) == "PUB" {
		info.HasSubscription = true
		info.UpdateTS = binary.BigEndian.Uint32(packet[20:24])
		info.SubType = packet[27]
		info.WildcardSupported = packet[28] == 1
	}
	return info, nil
}

func (udpLANTransport) Request(ctx context.Context, _ string, ip string, packet []byte) ([]byte, error) {
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(ip), Port: lanPort})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	deadline := time.Now().Add(5 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if _, err := conn.Write(packet); err != nil {
		return nil, err
	}
	buffer := make([]byte, 2048)
	n, err := conn.Read(buffer)
	if err != nil {
		return nil, err
	}
	return buffer[:n], nil
}

func (udpLANTransport) Ping(ctx context.Context, _ string, ip string) error {
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(ip), Port: lanPort})
	if err != nil {
		return err
	}
	defer conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	probe := make([]byte, lanHeaderLength)
	copy(probe[:20], []byte{
		0x21, 0x31, 0x00, 0x20,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0x4D, 0x44, 0x49, 0x44,
	})
	_, err = conn.Write(probe)
	return err
}

func (t *udpLANTransport) Listen(ctx context.Context, handler func(ifName, ip string, packet []byte)) (Subscription, error) {
	if handler == nil {
		return subscriptionFunc(nil), nil
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: lanPort})
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.handler = handler
	t.conn = conn
	t.connDone = make(chan struct{})
	done := t.connDone
	t.startUDPLoopLocked(ctx, "", conn, done)
	if err := t.syncBroadcastConnsLocked(ctx); err != nil {
		t.mu.Unlock()
		_ = conn.Close()
		<-done
		return nil, err
	}
	t.mu.Unlock()
	return subscriptionFunc(func() error {
		t.mu.Lock()
		err := t.closeLocked()
		t.mu.Unlock()
		return err
	}), nil
}

func (t *udpLANTransport) UpdateInterfaces(ifNames []string) error {
	t.mu.Lock()
	t.broadcastIfs = normalizeInterfaceNames(ifNames)
	var err error
	if t.handler != nil {
		err = t.syncBroadcastConnsLocked(context.Background())
	}
	t.mu.Unlock()
	return err
}

func (t *udpLANTransport) Broadcast(ctx context.Context, ifName string, packet []byte) error {
	t.mu.Lock()
	conn := t.broadcastConns[ifName]
	t.mu.Unlock()
	if conn == nil {
		return &Error{Code: ErrInvalidArgument, Op: "lan broadcast", Msg: "interface not configured"}
	}
	deadline := time.Now().Add(2 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok {
		deadline = ctxDeadline
	}
	if err := conn.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	_, err := conn.conn.WriteToUDP(packet, &net.UDPAddr{IP: conn.broadcastIP, Port: lanPort})
	return err
}

func (*udpLANTransport) Reply(ctx context.Context, _ string, ip string, packet []byte) error {
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(ip), Port: lanPort})
	if err != nil {
		return err
	}
	defer conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok {
		deadline = ctxDeadline
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	_, err = conn.Write(packet)
	return err
}

func (t *udpLANTransport) closeLocked() error {
	var firstErr error
	for ifName, conn := range t.broadcastConns {
		if err := conn.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		<-conn.done
		delete(t.broadcastConns, ifName)
	}
	if t.conn != nil {
		if err := t.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		if t.connDone != nil {
			<-t.connDone
		}
	}
	t.handler = nil
	t.conn = nil
	t.connDone = nil
	return firstErr
}

func (t *udpLANTransport) syncBroadcastConnsLocked(ctx context.Context) error {
	desired := make(map[string]struct{}, len(t.broadcastIfs))
	for _, ifName := range t.broadcastIfs {
		desired[ifName] = struct{}{}
		if _, ok := t.broadcastConns[ifName]; ok {
			continue
		}
		conn, err := newUDPLANBroadcastConn(ifName)
		if err != nil {
			return err
		}
		t.broadcastConns[ifName] = conn
		if t.handler != nil {
			t.startUDPLoopLocked(ctx, ifName, conn.conn, conn.done)
		}
	}
	for ifName, conn := range t.broadcastConns {
		if _, ok := desired[ifName]; ok {
			continue
		}
		_ = conn.conn.Close()
		<-conn.done
		delete(t.broadcastConns, ifName)
	}
	return nil
}

func (t *udpLANTransport) startUDPLoopLocked(ctx context.Context, ifName string, conn *net.UDPConn, done chan struct{}) {
	handler := t.handler
	go func() {
		defer close(done)
		buffer := make([]byte, 2048)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, addr, err := conn.ReadFromUDP(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					select {
					case <-ctx.Done():
						return
					default:
						continue
					}
				}
				if stderrors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			handler(ifName, addr.IP.String(), append([]byte(nil), buffer[:n]...))
		}
	}()
}

func newUDPLANBroadcastConn(ifName string) (*udpLANBroadcastConn, error) {
	localIP, broadcastIP, err := interfaceBroadcastInfo(ifName)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: localIP})
	if err != nil {
		return nil, err
	}
	return &udpLANBroadcastConn{
		ifName:      ifName,
		broadcastIP: broadcastIP,
		conn:        conn,
		done:        make(chan struct{}),
	}, nil
}

func interfaceBroadcastInfo(ifName string) (net.IP, net.IP, error) {
	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return nil, nil, err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, nil, err
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet == nil {
			continue
		}
		ip4 := ipNet.IP.To4()
		mask := net.IP(ipNet.Mask).To4()
		if ip4 == nil || mask == nil {
			continue
		}
		broadcast := net.IPv4(
			ip4[0]|^mask[0],
			ip4[1]|^mask[1],
			ip4[2]|^mask[2],
			ip4[3]|^mask[3],
		)
		return append(net.IP(nil), ip4...), broadcast, nil
	}
	return nil, nil, &Error{Code: ErrInvalidArgument, Op: "lan interface broadcast", Msg: "no ipv4 address on interface"}
}
