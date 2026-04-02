package miot

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	miotClientRefreshPropBatch    = 150
	miotClientCloudRefreshDelay   = 6 * time.Second
	miotClientCloudRefreshRetry   = 60 * time.Second
	miotClientGatewayRefreshDelay = 3 * time.Second
	miotClientRefreshPropDelay    = 200 * time.Millisecond
	miotClientRefreshPropRetry    = 3 * time.Second
	miotClientRefreshPropMaxRetry = 3
	miotClientOAuthRefreshMargin  = time.Minute
	miotClientCertRefreshMargin   = 72 * time.Hour
	miotClientPushSourceCloud     = "cloud"
	miotClientPushSourceLAN       = "lan"
)

var miotClientLANConnectTypes = map[int]struct{}{
	0:  {},
	8:  {},
	12: {},
	23: {},
}

// MIoTCloudBackend is the cloud dependency used by MIoTClient.
type MIoTCloudBackend interface {
	GetDevices(ctx context.Context, homeIDs []string) (DeviceSnapshot, error)
	GetDevicesByDID(ctx context.Context, dids []string) ([]DeviceInfo, error)
	GetProps(ctx context.Context, req GetPropsRequest) ([]PropertyResult, error)
	GetProp(ctx context.Context, query PropertyQuery) (PropertyResult, error)
	SetProps(ctx context.Context, req SetPropsRequest) ([]SetPropertyResult, error)
	InvokeAction(ctx context.Context, req ActionRequest) (ActionResult, error)
	UpdateAuth(cloudServer, clientID, accessToken string) error
	GetCentralCert(ctx context.Context, csr string) (string, error)
}

// MIoTCloudPushBackend is the cloud-MIPS dependency used by MIoTClient.
type MIoTCloudPushBackend interface {
	Start(ctx context.Context) error
	Close() error
	RefreshAccessToken(ctx context.Context, token string) error
	SubscribeProperty(ctx context.Context, req PropertySubscription, fn PropertyEventHandler) (Subscription, error)
	SubscribeEvent(ctx context.Context, req EventSubscription, fn EventHandler) (Subscription, error)
	SubscribeDeviceState(ctx context.Context, did string, fn DeviceStateHandler) (Subscription, error)
}

// MIoTLocalBackend is the local-gateway dependency used by MIoTClient.
type MIoTLocalBackend interface {
	Start(ctx context.Context) error
	Close() error
	GroupID() string
	GetDeviceList(ctx context.Context) ([]LocalDeviceSummary, error)
	GetPropSafe(ctx context.Context, req PropertyQuery) (PropertyResult, error)
	SetProp(ctx context.Context, req SetPropertyRequest) (SetPropertyResult, error)
	InvokeAction(ctx context.Context, req ActionRequest) (ActionResult, error)
	SubscribeProperty(ctx context.Context, req PropertySubscription, fn PropertyEventHandler) (Subscription, error)
	SubscribeEvent(ctx context.Context, req EventSubscription, fn EventHandler) (Subscription, error)
}

// MIoTLANBackend is the direct-LAN dependency used by MIoTClient.
type MIoTLANBackend interface {
	Start(ctx context.Context) error
	Close() error
	GetDeviceList() []LANDeviceSummary
	GetProp(ctx context.Context, req PropertyQuery) (PropertyResult, error)
	SetProp(ctx context.Context, req SetPropertyRequest) (SetPropertyResult, error)
	InvokeAction(ctx context.Context, req ActionRequest) (ActionResult, error)
	SubscribeProperty(req PropertySubscription, fn PropertyEventHandler) Subscription
	SubscribeEvent(req EventSubscription, fn EventHandler) Subscription
	SubscribeDeviceState(fn DeviceStateHandler) Subscription
	SubscribeLANState(fn func(bool)) Subscription
	UpdateDevices(devices []LANDeviceConfig) error
	VoteForLANControl(key string, vote bool)
}

// MIoTOAuthBackend refreshes OAuth tokens for MIoTClient.
type MIoTOAuthBackend interface {
	RefreshToken(ctx context.Context, refreshToken string) (OAuthToken, error)
}

// MIoTAuthStore loads and saves OAuth tokens for MIoTClient.
type MIoTAuthStore interface {
	LoadOAuthToken(ctx context.Context) (OAuthToken, error)
	SaveOAuthToken(ctx context.Context, token OAuthToken) error
}

// MIoTCertBackend manages local cert material for MIoTClient.
type MIoTCertBackend interface {
	VerifyCACert(ctx context.Context) error
	UserCertRemaining(ctx context.Context, certPEM []byte, did string) (time.Duration, error)
	LoadUserKey(ctx context.Context) ([]byte, error)
	GenerateUserKey() ([]byte, error)
	UpdateUserKey(ctx context.Context, keyPEM []byte) error
	GenerateUserCSR(keyPEM []byte, did string) ([]byte, error)
	UpdateUserCert(ctx context.Context, certPEM []byte) error
}

// MIoTNetworkBackend exposes network reachability state to MIoTClient.
type MIoTNetworkBackend interface {
	Status() bool
	SubscribeStatus(fn func(bool)) Subscription
}

type miotClientConnectionStateSource interface {
	SubscribeConnectionState(fn func(bool)) Subscription
}

type miotClientDeviceListChangeSource interface {
	SubscribeDeviceListChanged(fn func([]string)) Subscription
}

// MIoTControlMode configures the read/write routing behavior of MIoTClient.
type MIoTControlMode string

const (
	// MIoTControlModeAuto prefers local routes and falls back to cloud.
	MIoTControlModeAuto MIoTControlMode = "auto"
	// MIoTControlModeCloud forces cloud-only command routing.
	MIoTControlModeCloud MIoTControlMode = "cloud"
)

// MIoTClientHome describes one selected home for device refresh and gateway routing.
type MIoTClientHome struct {
	HomeID   string
	HomeName string
	GroupID  string
}

// MIoTClientConfig configures the platform-neutral MIoT coordinator.
type MIoTClientConfig struct {
	UID         string
	CloudServer string
	ControlMode MIoTControlMode
	VirtualDID  string
	Homes       []MIoTClientHome

	Cloud       MIoTCloudBackend
	CloudPush   MIoTCloudPushBackend
	LocalRoutes map[string]MIoTLocalBackend
	LAN         MIoTLANBackend
	Network     MIoTNetworkBackend
	OAuth       MIoTOAuthBackend
	AuthStore   MIoTAuthStore
	Cert        MIoTCertBackend
	Clock       Clock
}

// MIoTClientDevice is the public aggregated device state returned by MIoTClient.
type MIoTClientDevice struct {
	Info                 DeviceInfo
	State                DeviceState
	CloudPresent         bool
	CloudOnline          bool
	GatewayOnline        bool
	GatewayPushAvailable bool
	GatewaySpecV2Access  bool
	LANOnline            bool
	LANPushAvailable     bool
	PushSource           string
}

type miotClientDeviceEntry struct {
	info                 DeviceInfo
	state                DeviceState
	cloudPresent         bool
	cloudOnline          bool
	gatewayOnline        bool
	gatewayPushAvailable bool
	gatewaySpecV2Access  bool
	lanOnline            bool
	lanPushAvailable     bool
	pushSource           string
}

type miotClientPropertySub struct {
	req     PropertySubscription
	handler PropertyEventHandler
	source  string
	active  Subscription
}

type miotClientEventSub struct {
	req     EventSubscription
	handler EventHandler
	source  string
	active  Subscription
}

type miotClientStateSub struct {
	did     string
	handler DeviceStateHandler
}

type miotClientChange struct {
	did           string
	stateChanged  bool
	sourceChanged bool
	newState      DeviceState
}

// MIoTClient coordinates cloud, local-gateway, and direct-LAN MIoT behavior.
type MIoTClient struct {
	cfg   MIoTClientConfig
	clock Clock

	cloud       MIoTCloudBackend
	cloudPush   MIoTCloudPushBackend
	localRoutes map[string]MIoTLocalBackend
	lan         MIoTLANBackend
	network     MIoTNetworkBackend
	oauth       MIoTOAuthBackend
	authStore   MIoTAuthStore
	cert        MIoTCertBackend

	mu            sync.RWMutex
	devices       map[string]*miotClientDeviceEntry
	propSubs      map[int]*miotClientPropertySub
	eventSubs     map[int]*miotClientEventSub
	stateSubs     map[int]*miotClientStateSub
	cloudStateSub map[string]Subscription
	queuedProps   map[string]PropertyQuery
	nextSubID     int
	started       bool
	runtimeCtx    context.Context
	runtimeCancel context.CancelFunc
	networkOnline bool
	cloudPushOn   bool
	cloudTimer    Timer
	cloudStop     chan struct{}
	oauthTimer    Timer
	oauthStop     chan struct{}
	certTimer     Timer
	certStop      chan struct{}
	propTimer     Timer
	propStop      chan struct{}
	propRetry     int
	groupOnline   map[string]bool
	groupTimers   map[string]Timer
	groupStops    map[string]chan struct{}
	backgroundSub []Subscription
}

var _ EntityBackend = (*MIoTClient)(nil)

// NewMIoTClient creates a new platform-neutral MIoT coordinator.
func NewMIoTClient(cfg MIoTClientConfig) (*MIoTClient, error) {
	if cfg.UID == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new miot client", Msg: "uid is empty"}
	}
	if cfg.CloudServer == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new miot client", Msg: "cloud server is empty"}
	}
	if cfg.ControlMode == "" {
		cfg.ControlMode = MIoTControlModeAuto
	}
	client := &MIoTClient{
		cfg:           cfg,
		clock:         cfg.Clock,
		cloud:         cfg.Cloud,
		cloudPush:     cfg.CloudPush,
		localRoutes:   make(map[string]MIoTLocalBackend),
		lan:           cfg.LAN,
		network:       cfg.Network,
		oauth:         cfg.OAuth,
		authStore:     cfg.AuthStore,
		cert:          cfg.Cert,
		devices:       make(map[string]*miotClientDeviceEntry),
		propSubs:      make(map[int]*miotClientPropertySub),
		eventSubs:     make(map[int]*miotClientEventSub),
		stateSubs:     make(map[int]*miotClientStateSub),
		cloudStateSub: make(map[string]Subscription),
		queuedProps:   make(map[string]PropertyQuery),
		groupOnline:   make(map[string]bool),
		groupTimers:   make(map[string]Timer),
		groupStops:    make(map[string]chan struct{}),
	}
	if client.clock == nil {
		client.clock = realClock{}
	}
	for key, route := range cfg.LocalRoutes {
		if route != nil {
			client.localRoutes[key] = route
		}
	}
	return client, nil
}

// Start starts the configured push and LAN backends and performs an initial refresh.
func (c *MIoTClient) Start(ctx context.Context) (err error) {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	c.runtimeCtx, c.runtimeCancel = context.WithCancel(ctx)
	c.mu.Unlock()
	var startedRoutes []MIoTLocalBackend
	lanStarted := false
	lanVoteApplied := false
	defer func() {
		if err != nil {
			c.rollbackStart(startedRoutes, lanStarted, lanVoteApplied)
		}
	}()

	groupIDs := c.localGroupIDs()
	if c.network != nil {
		networkSub := c.network.SubscribeStatus(func(status bool) {
			c.handleNetworkStatusChanged(status)
		})
		c.mu.Lock()
		c.backgroundSub = append(c.backgroundSub, networkSub)
		c.mu.Unlock()
	}
	if source, ok := c.cloudPush.(miotClientConnectionStateSource); ok {
		sub := source.SubscribeConnectionState(func(connected bool) {
			c.handleCloudConnectionState(connected)
		})
		c.mu.Lock()
		c.backgroundSub = append(c.backgroundSub, sub)
		c.mu.Unlock()
	}
	for _, groupID := range groupIDs {
		route := c.localRoutes[groupID]
		if route == nil {
			continue
		}
		if source, ok := route.(miotClientConnectionStateSource); ok {
			sub := source.SubscribeConnectionState(func(connected bool) {
				c.handleGatewayConnectionState(groupID, connected)
			})
			c.mu.Lock()
			c.backgroundSub = append(c.backgroundSub, sub)
			c.mu.Unlock()
		}
		if source, ok := route.(miotClientDeviceListChangeSource); ok {
			sub := source.SubscribeDeviceListChanged(func([]string) {
				c.scheduleGatewayRefresh(groupID, 0)
			})
			c.mu.Lock()
			c.backgroundSub = append(c.backgroundSub, sub)
			c.mu.Unlock()
		}
	}

	for _, groupID := range groupIDs {
		if route := c.localRoutes[groupID]; route != nil {
			if err := route.Start(ctx); err != nil {
				return err
			}
			startedRoutes = append(startedRoutes, route)
		}
	}
	if c.lan != nil {
		if c.cfg.ControlMode == MIoTControlModeAuto {
			c.lan.VoteForLANControl(c.clientKey(), true)
		} else {
			c.lan.VoteForLANControl(c.clientKey(), false)
		}
		lanVoteApplied = true
		if err := c.lan.Start(ctx); err != nil {
			return err
		}
		lanStarted = true
		c.installLANSubscriptions()
	}
	for _, groupID := range groupIDs {
		if c.localRoutes[groupID] != nil {
			c.scheduleGatewayRefresh(groupID, 0)
		}
	}
	if c.lan != nil {
		if err := c.RefreshLANDevices(); err != nil {
			return err
		}
	}
	initialNetwork := true
	if c.network != nil {
		initialNetwork = c.network.Status()
	}
	c.handleNetworkStatusChanged(initialNetwork)
	return nil
}

func (c *MIoTClient) rollbackStart(startedRoutes []MIoTLocalBackend, lanStarted bool, lanVoteApplied bool) {
	c.mu.Lock()
	runtimeCancel := c.runtimeCancel
	c.runtimeCancel = nil
	c.runtimeCtx = nil
	backgroundSubs := append([]Subscription(nil), c.backgroundSub...)
	c.backgroundSub = nil
	groupStops := make(map[string]chan struct{}, len(c.groupStops))
	for key, stop := range c.groupStops {
		groupStops[key] = stop
	}
	groupTimers := make(map[string]Timer, len(c.groupTimers))
	for key, timer := range c.groupTimers {
		groupTimers[key] = timer
	}
	c.groupStops = make(map[string]chan struct{})
	c.groupTimers = make(map[string]Timer)
	c.started = false
	c.mu.Unlock()

	if runtimeCancel != nil {
		runtimeCancel()
	}
	for _, route := range startedRoutes {
		if route != nil {
			_ = route.Close()
		}
	}
	if lanVoteApplied && c.lan != nil {
		c.lan.VoteForLANControl(c.clientKey(), false)
	}
	if lanStarted && c.lan != nil {
		_ = c.lan.Close()
	}
	for _, stop := range groupStops {
		if stop != nil {
			close(stop)
		}
	}
	for _, timer := range groupTimers {
		if timer != nil {
			timer.Stop()
		}
	}
	for _, sub := range backgroundSubs {
		if sub != nil {
			_ = sub.Close()
		}
	}
}

// Close closes active subscriptions and owned backends.
func (c *MIoTClient) Close() error {
	c.mu.Lock()
	runtimeCancel := c.runtimeCancel
	c.runtimeCancel = nil
	c.runtimeCtx = nil
	cloudPushOn := c.cloudPushOn
	c.cloudPushOn = false
	cloudStop := c.cloudStop
	cloudTimer := c.cloudTimer
	c.cloudStop = nil
	c.cloudTimer = nil
	oauthStop := c.oauthStop
	oauthTimer := c.oauthTimer
	c.oauthStop = nil
	c.oauthTimer = nil
	certStop := c.certStop
	certTimer := c.certTimer
	c.certStop = nil
	c.certTimer = nil
	propStop := c.propStop
	propTimer := c.propTimer
	c.propStop = nil
	c.propTimer = nil
	c.propRetry = 0
	groupStops := make(map[string]chan struct{}, len(c.groupStops))
	for key, stop := range c.groupStops {
		groupStops[key] = stop
	}
	groupTimers := make(map[string]Timer, len(c.groupTimers))
	for key, timer := range c.groupTimers {
		groupTimers[key] = timer
	}
	c.groupStops = make(map[string]chan struct{})
	c.groupTimers = make(map[string]Timer)
	propSubs := make([]*miotClientPropertySub, 0, len(c.propSubs))
	for _, sub := range c.propSubs {
		propSubs = append(propSubs, sub)
	}
	eventSubs := make([]*miotClientEventSub, 0, len(c.eventSubs))
	for _, sub := range c.eventSubs {
		eventSubs = append(eventSubs, sub)
	}
	cloudStateSubs := make([]Subscription, 0, len(c.cloudStateSub))
	for _, sub := range c.cloudStateSub {
		cloudStateSubs = append(cloudStateSubs, sub)
	}
	backgroundSubs := append([]Subscription(nil), c.backgroundSub...)
	c.backgroundSub = nil
	c.cloudStateSub = make(map[string]Subscription)
	c.propSubs = make(map[int]*miotClientPropertySub)
	c.eventSubs = make(map[int]*miotClientEventSub)
	c.stateSubs = make(map[int]*miotClientStateSub)
	c.started = false
	c.mu.Unlock()

	if runtimeCancel != nil {
		runtimeCancel()
	}
	if cloudStop != nil {
		close(cloudStop)
	}
	if cloudTimer != nil {
		cloudTimer.Stop()
	}
	if oauthStop != nil {
		close(oauthStop)
	}
	if oauthTimer != nil {
		oauthTimer.Stop()
	}
	if certStop != nil {
		close(certStop)
	}
	if certTimer != nil {
		certTimer.Stop()
	}
	if propStop != nil {
		close(propStop)
	}
	if propTimer != nil {
		propTimer.Stop()
	}
	for _, stop := range groupStops {
		if stop != nil {
			close(stop)
		}
	}
	for _, timer := range groupTimers {
		if timer != nil {
			timer.Stop()
		}
	}
	for _, sub := range propSubs {
		if sub != nil && sub.active != nil {
			_ = sub.active.Close()
		}
	}
	for _, sub := range eventSubs {
		if sub != nil && sub.active != nil {
			_ = sub.active.Close()
		}
	}
	for _, sub := range cloudStateSubs {
		if sub != nil {
			_ = sub.Close()
		}
	}
	for _, sub := range backgroundSubs {
		if sub != nil {
			_ = sub.Close()
		}
	}
	for _, route := range c.localRoutes {
		if route != nil {
			_ = route.Close()
		}
	}
	if c.cloudPush != nil && cloudPushOn {
		_ = c.cloudPush.Close()
	}
	if c.lan != nil {
		c.lan.VoteForLANControl(c.clientKey(), false)
		_ = c.lan.Close()
	}
	return nil
}

func (c *MIoTClient) handleNetworkStatusChanged(status bool) {
	c.mu.Lock()
	c.networkOnline = status
	c.mu.Unlock()

	if status {
		oauthOK := true
		if err := c.RefreshOAuthInfo(c.runtimeContext()); err != nil {
			oauthOK = false
		}
		if oauthOK {
			if err := c.ensureCloudPushStarted(c.runtimeContext()); err == nil {
				c.scheduleCloudRefresh(miotClientCloudRefreshDelay)
			}
		}
		_ = c.RefreshUserCert(c.runtimeContext())
		return
	}

	c.cancelCloudRefresh()
	c.stopCloudPush()
	c.handleCloudConnectionState(false)
}

func (c *MIoTClient) handleCloudConnectionState(connected bool) {
	c.mu.Lock()
	c.cloudPushOn = connected
	c.mu.Unlock()
	if connected {
		c.scheduleCloudRefresh(0)
		return
	}

	c.mu.Lock()
	changes := make([]miotClientChange, 0, len(c.devices))
	for did, entry := range c.devices {
		if entry == nil || !entry.cloudPresent || !entry.cloudOnline {
			continue
		}
		entry.cloudOnline = false
		changes = append(changes, c.recomputeEntryLocked(did, entry))
	}
	c.mu.Unlock()
	c.handleChanges(changes)
}

func (c *MIoTClient) handleGatewayConnectionState(groupID string, connected bool) {
	c.mu.Lock()
	c.groupOnline[groupID] = connected
	c.mu.Unlock()
	if connected {
		c.scheduleGatewayRefresh(groupID, miotClientGatewayRefreshDelay)
		return
	}

	c.cancelGatewayRefresh(groupID)
	_ = c.updateGatewayDevices(groupID, nil)
}

func (c *MIoTClient) ensureCloudPushStarted(ctx context.Context) error {
	if c.cloudPush == nil {
		return nil
	}

	c.mu.Lock()
	if c.cloudPushOn {
		c.mu.Unlock()
		return nil
	}
	c.cloudPushOn = true
	c.mu.Unlock()

	if err := c.cloudPush.Start(ctx); err != nil {
		c.mu.Lock()
		c.cloudPushOn = false
		c.mu.Unlock()
		return err
	}
	return nil
}

func (c *MIoTClient) stopCloudPush() {
	if c.cloudPush == nil {
		return
	}

	c.mu.Lock()
	if !c.cloudPushOn {
		c.mu.Unlock()
		return
	}
	c.cloudPushOn = false
	c.mu.Unlock()

	_ = c.cloudPush.Close()
}

func (c *MIoTClient) scheduleCloudRefresh(delay time.Duration) {
	if c.cloud == nil || len(c.cfg.Homes) == 0 {
		return
	}
	if delay < 0 {
		delay = 0
	}

	timer := c.clock.NewTimer(delay)
	stop := make(chan struct{})

	c.mu.Lock()
	if c.cloudStop != nil {
		close(c.cloudStop)
	}
	if c.cloudTimer != nil {
		c.cloudTimer.Stop()
	}
	c.cloudTimer = timer
	c.cloudStop = stop
	ctx := c.runtimeCtx
	c.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-timer.C():
		}
		c.mu.Lock()
		if c.cloudStop == stop {
			c.cloudStop = nil
			c.cloudTimer = nil
		}
		c.mu.Unlock()
		if err := c.RefreshCloudDevices(c.runtimeContext()); err != nil {
			c.mu.RLock()
			online := c.networkOnline
			c.mu.RUnlock()
			if online {
				c.scheduleCloudRefresh(miotClientCloudRefreshRetry)
			}
		}
	}()
}

func (c *MIoTClient) cancelCloudRefresh() {
	c.mu.Lock()
	stop := c.cloudStop
	timer := c.cloudTimer
	c.cloudStop = nil
	c.cloudTimer = nil
	c.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	if timer != nil {
		timer.Stop()
	}
}

func (c *MIoTClient) scheduleOAuthRefresh(delay time.Duration) {
	c.scheduleSingleTimer(delay, &c.oauthTimer, &c.oauthStop, func() {
		_ = c.RefreshOAuthInfo(c.runtimeContext())
	})
}

func (c *MIoTClient) scheduleUserCertRefresh(delay time.Duration) {
	c.scheduleSingleTimer(delay, &c.certTimer, &c.certStop, func() {
		_ = c.RefreshUserCert(c.runtimeContext())
	})
}

func (c *MIoTClient) schedulePropRefresh(delay time.Duration) {
	c.scheduleSingleTimer(delay, &c.propTimer, &c.propStop, func() {
		c.handleQueuedPropertyRefresh()
	})
}

func (c *MIoTClient) cancelPropRefresh() {
	c.mu.Lock()
	stop := c.propStop
	timer := c.propTimer
	c.propStop = nil
	c.propTimer = nil
	c.propRetry = 0
	c.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	if timer != nil {
		timer.Stop()
	}
}

func (c *MIoTClient) scheduleSingleTimer(delay time.Duration, timerSlot *Timer, stopSlot *chan struct{}, fn func()) {
	c.mu.RLock()
	ctx := c.runtimeCtx
	c.mu.RUnlock()
	if ctx == nil {
		return
	}
	if delay < 0 {
		delay = 0
	}
	timer := c.clock.NewTimer(delay)
	stop := make(chan struct{})

	c.mu.Lock()
	if *stopSlot != nil {
		close(*stopSlot)
	}
	if *timerSlot != nil {
		(*timerSlot).Stop()
	}
	*timerSlot = timer
	*stopSlot = stop
	c.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-timer.C():
		}

		c.mu.Lock()
		if *stopSlot == stop {
			*stopSlot = nil
			*timerSlot = nil
		}
		c.mu.Unlock()
		fn()
	}()
}

func (c *MIoTClient) scheduleGatewayRefresh(groupID string, delay time.Duration) {
	if groupID == "" {
		return
	}
	timer := c.clock.NewTimer(delay)
	stop := make(chan struct{})

	c.mu.Lock()
	if oldStop := c.groupStops[groupID]; oldStop != nil {
		close(oldStop)
	}
	if oldTimer := c.groupTimers[groupID]; oldTimer != nil {
		oldTimer.Stop()
	}
	c.groupStops[groupID] = stop
	c.groupTimers[groupID] = timer
	ctx := c.runtimeCtx
	c.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-timer.C():
		}
		c.mu.Lock()
		if c.groupStops[groupID] == stop {
			delete(c.groupStops, groupID)
			delete(c.groupTimers, groupID)
		}
		connected := c.groupOnline[groupID]
		c.mu.Unlock()

		if err := c.RefreshGatewayDevices(c.runtimeContext(), groupID); err != nil && connected {
			c.scheduleGatewayRefresh(groupID, miotClientGatewayRefreshDelay)
		}
	}()
}

func (c *MIoTClient) cancelGatewayRefresh(groupID string) {
	c.mu.Lock()
	stop := c.groupStops[groupID]
	timer := c.groupTimers[groupID]
	delete(c.groupStops, groupID)
	delete(c.groupTimers, groupID)
	c.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	if timer != nil {
		timer.Stop()
	}
}

func (c *MIoTClient) runtimeContext() context.Context {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.runtimeCtx != nil {
		return c.runtimeCtx
	}
	return context.Background()
}

// Devices returns a deep copy of the aggregated device registry.
func (c *MIoTClient) Devices() map[string]MIoTClientDevice {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[string]MIoTClientDevice, len(c.devices))
	for did, entry := range c.devices {
		if entry == nil {
			continue
		}
		out[did] = MIoTClientDevice{
			Info:                 cloneDeviceInfo(entry.info),
			State:                entry.state,
			CloudPresent:         entry.cloudPresent,
			CloudOnline:          entry.cloudOnline,
			GatewayOnline:        entry.gatewayOnline,
			GatewayPushAvailable: entry.gatewayPushAvailable,
			GatewaySpecV2Access:  entry.gatewaySpecV2Access,
			LANOnline:            entry.lanOnline,
			LANPushAvailable:     entry.lanPushAvailable,
			PushSource:           entry.pushSource,
		}
	}
	return out
}

// RefreshCloudDevices refreshes all cloud devices for the selected homes.
func (c *MIoTClient) RefreshCloudDevices(ctx context.Context) error {
	if c.cloud == nil {
		return nil
	}
	snapshot, err := c.cloud.GetDevices(ctx, c.homeIDs())
	if err != nil {
		return err
	}

	newDIDs := make([]string, 0, len(snapshot.Devices))
	c.mu.Lock()
	changes := make([]miotClientChange, 0, len(c.devices)+len(snapshot.Devices))
	for did, entry := range c.devices {
		if entry != nil && entry.cloudPresent {
			entry.cloudPresent = false
			change := c.recomputeEntryLocked(did, entry)
			changes = append(changes, change)
		}
	}
	for did, info := range snapshot.Devices {
		entry := c.ensureDeviceEntryLocked(did)
		entry.info = cloneDeviceInfo(info)
		entry.cloudPresent = true
		entry.cloudOnline = info.Online
		changes = append(changes, c.recomputeEntryLocked(did, entry))
		newDIDs = append(newDIDs, did)
	}
	c.mu.Unlock()

	if c.lan != nil {
		_ = c.lan.UpdateDevices(c.buildLANDeviceConfigs())
	}
	for _, did := range newDIDs {
		_ = c.ensureCloudStateSubscription(did)
	}
	c.handleChanges(changes)
	return nil
}

// RefreshCloudDevicesByDID refreshes selected cloud devices by DID.
func (c *MIoTClient) RefreshCloudDevicesByDID(ctx context.Context, dids []string) error {
	if c.cloud == nil || len(dids) == 0 {
		return nil
	}
	details, err := c.cloud.GetDevicesByDID(ctx, dids)
	if err != nil {
		return err
	}
	detailByDID := make(map[string]DeviceInfo, len(details))
	for _, info := range details {
		detailByDID[info.DID] = info
	}

	c.mu.Lock()
	changes := make([]miotClientChange, 0, len(dids))
	for _, did := range uniqueStrings(dids) {
		entry := c.ensureDeviceEntryLocked(did)
		if info, ok := detailByDID[did]; ok {
			entry.info = mergeDeviceInfo(entry.info, info)
			entry.cloudPresent = true
			entry.cloudOnline = info.Online
		} else {
			entry.cloudPresent = false
			entry.cloudOnline = false
		}
		change := c.recomputeEntryLocked(did, entry)
		changes = append(changes, change)
	}
	c.mu.Unlock()

	if c.lan != nil {
		_ = c.lan.UpdateDevices(c.buildLANDeviceConfigs())
	}
	for _, did := range uniqueStrings(dids) {
		_ = c.ensureCloudStateSubscription(did)
	}
	c.handleChanges(changes)
	return nil
}

// RefreshGatewayDevices refreshes one local-gateway device view.
func (c *MIoTClient) RefreshGatewayDevices(ctx context.Context, groupID string) error {
	route := c.localRoutes[groupID]
	if route == nil {
		return nil
	}
	items, err := route.GetDeviceList(ctx)
	if err != nil {
		return err
	}
	return c.updateGatewayDevices(groupID, items)
}

// RefreshLANDevices refreshes the current direct-LAN device view.
func (c *MIoTClient) RefreshLANDevices() error {
	if c.lan == nil {
		return nil
	}
	return c.updateLANDevices(c.lan.GetDeviceList())
}

// RefreshOAuthInfo refreshes OAuth credentials when they are near expiry.
func (c *MIoTClient) RefreshOAuthInfo(ctx context.Context) error {
	if c.oauth == nil || c.authStore == nil {
		return nil
	}
	token, err := c.authStore.LoadOAuthToken(ctx)
	if err != nil {
		return err
	}
	if token.ExpiresAt.After(c.clock.Now().Add(miotClientOAuthRefreshMargin)) {
		return nil
	}
	if token.RefreshToken == "" {
		return &Error{Code: ErrInvalidArgument, Op: "miot client refresh oauth", Msg: "refresh token is empty"}
	}
	refreshed, err := c.oauth.RefreshToken(ctx, token.RefreshToken)
	if err != nil {
		return err
	}
	if c.cloud != nil {
		if err := c.cloud.UpdateAuth("", "", refreshed.AccessToken); err != nil {
			return err
		}
	}
	if c.cloudPush != nil {
		if err := c.cloudPush.RefreshAccessToken(ctx, refreshed.AccessToken); err != nil {
			return err
		}
	}
	if err := c.authStore.SaveOAuthToken(ctx, refreshed); err != nil {
		return err
	}
	token = refreshed
	delay := token.ExpiresAt.Sub(c.clock.Now()) - miotClientOAuthRefreshMargin
	c.scheduleOAuthRefresh(delay)
	return nil
}

// RefreshUserCert refreshes the user certificate when it is close to expiry.
func (c *MIoTClient) RefreshUserCert(ctx context.Context) error {
	if c.cert == nil || c.cloud == nil || c.cfg.VirtualDID == "" {
		return nil
	}
	if err := c.cert.VerifyCACert(ctx); err != nil {
		return err
	}
	remaining, err := c.cert.UserCertRemaining(ctx, nil, c.cfg.VirtualDID)
	if err == nil && remaining > miotClientCertRefreshMargin {
		c.scheduleUserCertRefresh(remaining - miotClientCertRefreshMargin)
		return nil
	}
	keyPEM, err := c.cert.LoadUserKey(ctx)
	if err != nil || len(keyPEM) == 0 {
		keyPEM, err = c.cert.GenerateUserKey()
		if err != nil {
			return err
		}
		if err := c.cert.UpdateUserKey(ctx, keyPEM); err != nil {
			return err
		}
	}
	csrPEM, err := c.cert.GenerateUserCSR(keyPEM, c.cfg.VirtualDID)
	if err != nil {
		return err
	}
	certPEM, err := c.cloud.GetCentralCert(ctx, string(csrPEM))
	if err != nil {
		return err
	}
	if err := c.cert.UpdateUserCert(ctx, []byte(certPEM)); err != nil {
		return err
	}
	remaining, err = c.cert.UserCertRemaining(ctx, nil, c.cfg.VirtualDID)
	if err == nil {
		c.scheduleUserCertRefresh(remaining - miotClientCertRefreshMargin)
	}
	return nil
}

// RequestRefreshProperty queues one property for later refresh.
func (c *MIoTClient) RequestRefreshProperty(did string, siid, piid int) {
	c.mu.Lock()
	if _, ok := c.devices[did]; !ok {
		c.mu.Unlock()
		return
	}
	key := c.propertyKey(did, siid, piid)
	c.queuedProps[key] = PropertyQuery{DID: did, SIID: siid, PIID: piid}
	shouldSchedule := c.propTimer == nil
	c.mu.Unlock()
	if shouldSchedule {
		c.schedulePropRefresh(miotClientRefreshPropDelay)
	}
}

// RefreshQueuedProperties fetches queued properties and dispatches them to matching subscribers.
func (c *MIoTClient) RefreshQueuedProperties(ctx context.Context) error {
	progress, remaining := c.refreshQueuedPropertiesCycle(ctx)
	if progress {
		return nil
	}
	if remaining == 0 {
		return nil
	}
	return &Error{Code: ErrProtocolFailure, Op: "miot client refresh queued properties", Msg: "some properties could not be refreshed"}
}

func (c *MIoTClient) handleQueuedPropertyRefresh() {
	progress, remaining := c.refreshQueuedPropertiesCycle(c.runtimeContext())
	if progress {
		c.mu.Lock()
		c.propRetry = 0
		c.mu.Unlock()
		if remaining > 0 {
			c.schedulePropRefresh(miotClientRefreshPropDelay)
		}
		return
	}
	if remaining == 0 {
		c.cancelPropRefresh()
		return
	}

	c.mu.Lock()
	c.propRetry++
	retry := c.propRetry
	c.mu.Unlock()
	if retry >= miotClientRefreshPropMaxRetry {
		c.mu.Lock()
		c.queuedProps = make(map[string]PropertyQuery)
		c.propRetry = 0
		c.mu.Unlock()
		c.cancelPropRefresh()
		return
	}
	c.schedulePropRefresh(miotClientRefreshPropRetry)
}

func (c *MIoTClient) refreshQueuedPropertiesCycle(ctx context.Context) (bool, int) {
	c.mu.Lock()
	if len(c.queuedProps) == 0 {
		c.mu.Unlock()
		return false, 0
	}

	active := make(map[string]PropertyQuery, min(len(c.queuedProps), miotClientRefreshPropBatch))
	overflow := make(map[string]PropertyQuery)
	count := 0
	for key, query := range c.queuedProps {
		if count < miotClientRefreshPropBatch {
			active[key] = query
			count++
			continue
		}
		overflow[key] = query
	}
	c.queuedProps = overflow
	c.mu.Unlock()

	progress := false

	if c.cloud != nil {
		results, err := c.cloud.GetProps(ctx, GetPropsRequest{Params: propertyQueriesFromMap(active)})
		if err == nil {
			for _, result := range results {
				delete(active, c.propertyKey(result.DID, result.SIID, result.PIID))
				c.dispatchPropertyResult(result)
				progress = true
			}
		}
	}

	if !progress && c.cfg.ControlMode == MIoTControlModeAuto {
		for key, query := range firstPropertyPerDevice(active) {
			route := c.gatewayRouteForQuery(query)
			if route == nil {
				continue
			}
			result, err := route.GetPropSafe(ctx, query)
			if err == nil {
				delete(active, key)
				c.dispatchPropertyResult(result)
				progress = true
			}
		}
	}
	if !progress && c.cfg.ControlMode == MIoTControlModeAuto {
		for key, query := range firstPropertyPerDevice(active) {
			if c.lan == nil || !c.isLANOnline(query.DID) {
				continue
			}
			result, err := c.lan.GetProp(ctx, query)
			if err == nil {
				delete(active, key)
				c.dispatchPropertyResult(result)
				progress = true
			}
		}
	}

	c.mu.Lock()
	for key, query := range active {
		c.queuedProps[key] = query
	}
	remaining := len(c.queuedProps)
	c.mu.Unlock()
	return progress, remaining
}

// DeviceOnline reports the aggregate online state of one device.
func (c *MIoTClient) DeviceOnline(_ context.Context, did string) (bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry := c.devices[did]
	if entry == nil {
		return false, &Error{Code: ErrInvalidArgument, Op: "miot client device online", Msg: "device not found"}
	}
	return entry.state == DeviceStateOnline, nil
}

// GetProperty reads a property using the coordinator routing rules.
func (c *MIoTClient) GetProperty(ctx context.Context, query PropertyQuery) (PropertyResult, error) {
	entry, err := c.deviceEntry(query.DID)
	if err != nil {
		return PropertyResult{}, err
	}
	var firstErr error
	if c.cloud != nil {
		result, err := c.cloud.GetProp(ctx, query)
		if err == nil {
			return result, nil
		}
		firstErr = err
	}
	if c.cfg.ControlMode == MIoTControlModeAuto {
		if entry.gatewayOnline && entry.gatewaySpecV2Access {
			if route := c.localRoutes[entry.info.GroupID]; route != nil {
				return route.GetPropSafe(ctx, query)
			}
		}
		if entry.lanOnline && c.lan != nil {
			return c.lan.GetProp(ctx, query)
		}
	}
	if firstErr != nil {
		return PropertyResult{}, firstErr
	}
	return PropertyResult{}, &Error{Code: ErrProtocolFailure, Op: "miot client get property", Msg: "no available route"}
}

// GetProperty is the EntityBackend alias for GetProperty.
func (c *MIoTClient) GetProp(ctx context.Context, query PropertyQuery) (PropertyResult, error) {
	return c.GetProperty(ctx, query)
}

// SetProperty writes a property using the coordinator routing rules.
func (c *MIoTClient) SetProperty(ctx context.Context, req SetPropertyRequest) (SetPropertyResult, error) {
	entry, err := c.deviceEntry(req.DID)
	if err != nil {
		return SetPropertyResult{}, err
	}
	if c.cfg.ControlMode == MIoTControlModeAuto && entry.gatewayOnline && entry.gatewaySpecV2Access {
		if route := c.localRoutes[entry.info.GroupID]; route != nil {
			result, err := route.SetProp(ctx, req)
			if err == nil && result.CodeOK() {
				return result, nil
			}
			if err != nil {
				return SetPropertyResult{}, err
			}
			return SetPropertyResult{}, c.execResultError("miot client set property", result.Code)
		}
	}
	if c.cfg.ControlMode == MIoTControlModeAuto && entry.lanOnline && c.lan != nil {
		result, err := c.lan.SetProp(ctx, req)
		if err == nil && result.CodeOK() {
			return result, nil
		}
		if err != nil {
			return SetPropertyResult{}, err
		}
		return SetPropertyResult{}, c.execResultError("miot client set property", result.Code)
	}
	if c.cloud != nil && entry.cloudPresent {
		results, err := c.cloud.SetProps(ctx, SetPropsRequest{Params: []SetPropertyRequest{req}})
		if err != nil {
			return SetPropertyResult{}, err
		}
		if len(results) != 1 {
			return SetPropertyResult{}, &Error{Code: ErrInvalidResponse, Op: "miot client set property", Msg: "invalid result length"}
		}
		if !results[0].CodeOK() {
			return SetPropertyResult{}, c.execResultError("miot client set property", results[0].Code)
		}
		return results[0], nil
	}
	return SetPropertyResult{}, &Error{Code: ErrProtocolFailure, Op: "miot client set property", Msg: "no available route"}
}

// SetProperty is the EntityBackend alias for SetProperty.
func (c *MIoTClient) SetProp(ctx context.Context, req SetPropertyRequest) (SetPropertyResult, error) {
	return c.SetProperty(ctx, req)
}

// InvokeAction executes an action using the coordinator routing rules.
func (c *MIoTClient) InvokeAction(ctx context.Context, req ActionRequest) (ActionResult, error) {
	entry, err := c.deviceEntry(req.DID)
	if err != nil {
		return ActionResult{}, err
	}
	if c.cfg.ControlMode == MIoTControlModeAuto && entry.gatewayOnline && entry.gatewaySpecV2Access {
		if route := c.localRoutes[entry.info.GroupID]; route != nil {
			result, err := route.InvokeAction(ctx, req)
			if err == nil && actionCodeOK(result.Code) {
				return result, nil
			}
			if err != nil {
				return ActionResult{}, err
			}
			return ActionResult{}, c.execResultError("miot client invoke action", result.Code)
		}
	}
	if c.cfg.ControlMode == MIoTControlModeAuto && entry.lanOnline && c.lan != nil {
		result, err := c.lan.InvokeAction(ctx, req)
		if err == nil && actionCodeOK(result.Code) {
			return result, nil
		}
		if err != nil {
			return ActionResult{}, err
		}
		return ActionResult{}, c.execResultError("miot client invoke action", result.Code)
	}
	if c.cloud != nil && entry.cloudPresent {
		result, err := c.cloud.InvokeAction(ctx, req)
		if err != nil {
			return ActionResult{}, err
		}
		if !actionCodeOK(result.Code) {
			return ActionResult{}, c.execResultError("miot client invoke action", result.Code)
		}
		return result, nil
	}
	return ActionResult{}, &Error{Code: ErrProtocolFailure, Op: "miot client invoke action", Msg: "no available route"}
}

// SubscribeProperty subscribes to property updates for one device.
func (c *MIoTClient) SubscribeProperty(ctx context.Context, req PropertySubscription, fn PropertyEventHandler) (Subscription, error) {
	if fn == nil {
		return subscriptionFunc(nil), nil
	}
	if _, err := c.deviceEntry(req.DID); err != nil {
		return nil, err
	}

	c.mu.Lock()
	id := c.nextSubID
	c.nextSubID++
	sub := &miotClientPropertySub{req: req, handler: fn}
	c.propSubs[id] = sub
	c.mu.Unlock()

	c.rebindPropertySubscription(ctx, sub)
	return subscriptionFunc(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.propSubs, id)
		if sub.active != nil {
			return sub.active.Close()
		}
		return nil
	}), nil
}

// SubscribeEvent subscribes to event updates for one device.
func (c *MIoTClient) SubscribeEvent(ctx context.Context, req EventSubscription, fn EventHandler) (Subscription, error) {
	if fn == nil {
		return subscriptionFunc(nil), nil
	}
	if _, err := c.deviceEntry(req.DID); err != nil {
		return nil, err
	}

	c.mu.Lock()
	id := c.nextSubID
	c.nextSubID++
	sub := &miotClientEventSub{req: req, handler: fn}
	c.eventSubs[id] = sub
	c.mu.Unlock()

	c.rebindEventSubscription(ctx, sub)
	return subscriptionFunc(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.eventSubs, id)
		if sub.active != nil {
			return sub.active.Close()
		}
		return nil
	}), nil
}

// SubscribeDeviceState subscribes to aggregate device-state changes.
func (c *MIoTClient) SubscribeDeviceState(_ context.Context, did string, fn DeviceStateHandler) (Subscription, error) {
	if fn == nil {
		return subscriptionFunc(nil), nil
	}
	if _, err := c.deviceEntry(did); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextSubID
	c.nextSubID++
	c.stateSubs[id] = &miotClientStateSub{did: did, handler: fn}
	return subscriptionFunc(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.stateSubs, id)
		return nil
	}), nil
}

func (c *MIoTClient) updateGatewayDevices(groupID string, items []LocalDeviceSummary) error {
	current := make(map[string]LocalDeviceSummary, len(items))
	for _, item := range items {
		current[item.DID] = item
	}

	c.mu.Lock()
	changes := make([]miotClientChange, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for did, entry := range c.devices {
		if entry == nil || entry.info.GroupID != groupID {
			continue
		}
		summary, ok := current[did]
		if !ok {
			entry.gatewayOnline = false
			entry.gatewayPushAvailable = false
			entry.gatewaySpecV2Access = false
			change := c.recomputeEntryLocked(did, entry)
			changes = append(changes, change)
			continue
		}
		seen[did] = struct{}{}
		entry.gatewayOnline = summary.Online
		entry.gatewayPushAvailable = summary.PushAvailable
		entry.gatewaySpecV2Access = summary.SpecV2Access
		if summary.Name != "" {
			entry.info.Name = summary.Name
		}
		if summary.URN != "" {
			entry.info.URN = summary.URN
		}
		if summary.Model != "" {
			entry.info.Model = summary.Model
		}
		entry.info.GroupID = groupID
		change := c.recomputeEntryLocked(did, entry)
		changes = append(changes, change)
	}
	for did, summary := range current {
		if _, ok := seen[did]; ok {
			continue
		}
		entry := c.ensureDeviceEntryLocked(did)
		entry.info.DID = did
		entry.info.GroupID = groupID
		if summary.Name != "" {
			entry.info.Name = summary.Name
		}
		if summary.URN != "" {
			entry.info.URN = summary.URN
		}
		if summary.Model != "" {
			entry.info.Model = summary.Model
		}
		entry.gatewayOnline = summary.Online
		entry.gatewayPushAvailable = summary.PushAvailable
		entry.gatewaySpecV2Access = summary.SpecV2Access
		change := c.recomputeEntryLocked(did, entry)
		changes = append(changes, change)
	}
	c.mu.Unlock()

	c.handleChanges(changes)
	return nil
}

func (c *MIoTClient) updateLANDevices(items []LANDeviceSummary) error {
	current := make(map[string]LANDeviceSummary, len(items))
	for _, item := range items {
		current[item.DID] = item
	}

	c.mu.Lock()
	changes := make([]miotClientChange, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for did, entry := range c.devices {
		if entry == nil {
			continue
		}
		summary, ok := current[did]
		if !ok {
			entry.lanOnline = false
			entry.lanPushAvailable = false
			change := c.recomputeEntryLocked(did, entry)
			changes = append(changes, change)
			continue
		}
		seen[did] = struct{}{}
		entry.lanOnline = summary.Online
		entry.lanPushAvailable = summary.PushAvailable
		change := c.recomputeEntryLocked(did, entry)
		changes = append(changes, change)
	}
	for did, summary := range current {
		if _, ok := seen[did]; ok {
			continue
		}
		entry := c.ensureDeviceEntryLocked(did)
		entry.info.DID = did
		entry.lanOnline = summary.Online
		entry.lanPushAvailable = summary.PushAvailable
		change := c.recomputeEntryLocked(did, entry)
		changes = append(changes, change)
	}
	c.mu.Unlock()

	c.handleChanges(changes)
	return nil
}

func (c *MIoTClient) installLANSubscriptions() {
	if c.lan == nil {
		return
	}
	stateSub := c.lan.SubscribeDeviceState(func(string, DeviceState) {
		_ = c.RefreshLANDevices()
	})
	lanStateSub := c.lan.SubscribeLANState(func(enabled bool) {
		if enabled {
			_ = c.RefreshLANDevices()
			return
		}
		_ = c.updateLANDevices(nil)
	})
	c.mu.Lock()
	c.backgroundSub = append(c.backgroundSub, stateSub, lanStateSub)
	c.mu.Unlock()
}

func (c *MIoTClient) ensureCloudStateSubscription(did string) error {
	if c.cloudPush == nil || did == "" {
		return nil
	}

	c.mu.RLock()
	_, ok := c.cloudStateSub[did]
	c.mu.RUnlock()
	if ok {
		return nil
	}

	sub, err := c.cloudPush.SubscribeDeviceState(context.Background(), did, func(did string, state DeviceState) {
		c.handleCloudDeviceState(did, state)
	})
	if err != nil {
		return err
	}

	c.mu.Lock()
	if _, ok := c.cloudStateSub[did]; ok {
		c.mu.Unlock()
		return sub.Close()
	}
	c.cloudStateSub[did] = sub
	c.mu.Unlock()
	return nil
}

func (c *MIoTClient) handleCloudDeviceState(did string, state DeviceState) {
	c.mu.Lock()
	entry := c.devices[did]
	if entry == nil {
		c.mu.Unlock()
		return
	}
	entry.cloudPresent = true
	entry.cloudOnline = state == DeviceStateOnline
	change := c.recomputeEntryLocked(did, entry)
	c.mu.Unlock()

	c.handleChanges([]miotClientChange{change})
}

func (c *MIoTClient) rebindSubscriptionsForDevice(did string) {
	c.mu.RLock()
	propSubs := make([]*miotClientPropertySub, 0, len(c.propSubs))
	for _, sub := range c.propSubs {
		if sub != nil && sub.req.DID == did {
			propSubs = append(propSubs, sub)
		}
	}
	eventSubs := make([]*miotClientEventSub, 0, len(c.eventSubs))
	for _, sub := range c.eventSubs {
		if sub != nil && sub.req.DID == did {
			eventSubs = append(eventSubs, sub)
		}
	}
	c.mu.RUnlock()

	for _, sub := range propSubs {
		c.rebindPropertySubscription(context.Background(), sub)
	}
	for _, sub := range eventSubs {
		c.rebindEventSubscription(context.Background(), sub)
	}
}

func (c *MIoTClient) rebindPropertySubscription(ctx context.Context, sub *miotClientPropertySub) {
	if sub == nil {
		return
	}
	source := c.currentPushSource(sub.req.DID)
	if sub.source == source {
		return
	}
	if sub.active != nil {
		_ = sub.active.Close()
		sub.active = nil
	}
	active, err := c.subscribePropertyFromSource(ctx, source, sub.req, sub.handler)
	if err != nil {
		active = nil
	}
	c.mu.Lock()
	sub.source = source
	sub.active = active
	c.mu.Unlock()
}

func (c *MIoTClient) rebindEventSubscription(ctx context.Context, sub *miotClientEventSub) {
	if sub == nil {
		return
	}
	source := c.currentPushSource(sub.req.DID)
	if sub.source == source {
		return
	}
	if sub.active != nil {
		_ = sub.active.Close()
		sub.active = nil
	}
	active, err := c.subscribeEventFromSource(ctx, source, sub.req, sub.handler)
	if err != nil {
		active = nil
	}
	c.mu.Lock()
	sub.source = source
	sub.active = active
	c.mu.Unlock()
}

func (c *MIoTClient) subscribePropertyFromSource(ctx context.Context, source string, req PropertySubscription, fn PropertyEventHandler) (Subscription, error) {
	switch source {
	case "":
		return nil, nil
	case miotClientPushSourceCloud:
		if c.cloudPush == nil {
			return nil, nil
		}
		return c.cloudPush.SubscribeProperty(ctx, req, fn)
	case miotClientPushSourceLAN:
		if c.lan == nil {
			return nil, nil
		}
		return c.lan.SubscribeProperty(req, fn), nil
	default:
		route := c.localRoutes[source]
		if route == nil {
			return nil, nil
		}
		return route.SubscribeProperty(ctx, req, fn)
	}
}

func (c *MIoTClient) subscribeEventFromSource(ctx context.Context, source string, req EventSubscription, fn EventHandler) (Subscription, error) {
	switch source {
	case "":
		return nil, nil
	case miotClientPushSourceCloud:
		if c.cloudPush == nil {
			return nil, nil
		}
		return c.cloudPush.SubscribeEvent(ctx, req, fn)
	case miotClientPushSourceLAN:
		if c.lan == nil {
			return nil, nil
		}
		return c.lan.SubscribeEvent(req, fn), nil
	default:
		route := c.localRoutes[source]
		if route == nil {
			return nil, nil
		}
		return route.SubscribeEvent(ctx, req, fn)
	}
}

func (c *MIoTClient) dispatchPropertyResult(result PropertyResult) {
	c.mu.RLock()
	subs := make([]*miotClientPropertySub, 0, len(c.propSubs))
	for _, sub := range c.propSubs {
		if sub != nil && propertySubscriptionMatches(sub.req, result) {
			subs = append(subs, sub)
		}
	}
	c.mu.RUnlock()
	for _, sub := range subs {
		sub.handler(result)
	}
}

func (c *MIoTClient) handleChanges(changes []miotClientChange) {
	for _, change := range changes {
		if !change.stateChanged && !change.sourceChanged {
			continue
		}
		if change.sourceChanged {
			c.rebindSubscriptionsForDevice(change.did)
		}
		if change.stateChanged {
			c.notifyDeviceState(change.did, change.newState)
		}
	}
}

func (c *MIoTClient) notifyDeviceState(did string, state DeviceState) {
	c.mu.RLock()
	subs := make([]*miotClientStateSub, 0, len(c.stateSubs))
	for _, sub := range c.stateSubs {
		if sub != nil && sub.did == did {
			subs = append(subs, sub)
		}
	}
	c.mu.RUnlock()
	for _, sub := range subs {
		sub.handler(did, state)
	}
}

func (c *MIoTClient) deviceEntry(did string) (*miotClientDeviceEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry := c.devices[did]
	if entry == nil {
		return nil, &Error{Code: ErrInvalidArgument, Op: "miot client device", Msg: fmt.Sprintf("device %s not found", did)}
	}
	return cloneDeviceEntry(entry), nil
}

func (c *MIoTClient) currentPushSource(did string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry := c.devices[did]
	if entry == nil {
		return ""
	}
	return entry.pushSource
}

func (c *MIoTClient) gatewayRouteForQuery(query PropertyQuery) MIoTLocalBackend {
	c.mu.RLock()
	entry := c.devices[query.DID]
	c.mu.RUnlock()
	if entry == nil || !entry.gatewayOnline || !entry.gatewaySpecV2Access {
		return nil
	}
	return c.localRoutes[entry.info.GroupID]
}

func (c *MIoTClient) isLANOnline(did string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry := c.devices[did]
	return entry != nil && entry.lanOnline
}

func (c *MIoTClient) buildLANDeviceConfigs() []LANDeviceConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cfgs := make([]LANDeviceConfig, 0, len(c.devices))
	for _, entry := range c.devices {
		if entry == nil {
			continue
		}
		if entry.info.Token == "" {
			continue
		}
		if _, ok := miotClientLANConnectTypes[entry.info.ConnectType]; !ok {
			continue
		}
		cfgs = append(cfgs, LANDeviceConfig{
			DID:   entry.info.DID,
			Token: entry.info.Token,
		})
	}
	sort.Slice(cfgs, func(i, j int) bool {
		return cfgs[i].DID < cfgs[j].DID
	})
	return cfgs
}

func (c *MIoTClient) localGroupIDs() []string {
	keys := make([]string, 0, len(c.localRoutes))
	for groupID := range c.localRoutes {
		keys = append(keys, groupID)
	}
	sort.Strings(keys)
	return keys
}

func (c *MIoTClient) homeIDs() []string {
	ids := make([]string, 0, len(c.cfg.Homes))
	for _, home := range c.cfg.Homes {
		if home.HomeID != "" {
			ids = append(ids, home.HomeID)
		}
	}
	return ids
}

func (c *MIoTClient) clientKey() string {
	return c.cfg.UID + "-" + c.cfg.CloudServer
}

func (c *MIoTClient) propertyKey(did string, siid, piid int) string {
	return fmt.Sprintf("%s|%d|%d", did, siid, piid)
}

func (c *MIoTClient) ensureDeviceEntryLocked(did string) *miotClientDeviceEntry {
	entry := c.devices[did]
	if entry != nil {
		return entry
	}
	entry = &miotClientDeviceEntry{
		info:  DeviceInfo{DID: did},
		state: DeviceStateDisable,
	}
	c.devices[did] = entry
	return entry
}

func (c *MIoTClient) recomputeEntryLocked(did string, entry *miotClientDeviceEntry) miotClientChange {
	if entry == nil {
		return miotClientChange{}
	}
	oldState := entry.state
	oldSource := entry.pushSource
	entry.state = aggregateDeviceState(entry.cloudPresent, entry.cloudOnline, entry.gatewayOnline, entry.lanOnline)
	entry.pushSource = c.selectPushSource(entry)
	return miotClientChange{
		did:           did,
		stateChanged:  oldState != entry.state,
		sourceChanged: oldSource != entry.pushSource,
		newState:      entry.state,
	}
}

func (c *MIoTClient) selectPushSource(entry *miotClientDeviceEntry) string {
	if entry == nil {
		return ""
	}
	if c.cfg.ControlMode == MIoTControlModeAuto {
		if entry.gatewayOnline && entry.gatewayPushAvailable && entry.info.GroupID != "" {
			return entry.info.GroupID
		}
		if entry.lanOnline && entry.lanPushAvailable {
			return miotClientPushSourceLAN
		}
	}
	if entry.cloudPresent && entry.cloudOnline {
		return miotClientPushSourceCloud
	}
	return ""
}

func aggregateDeviceState(cloudPresent, cloudOnline, gatewayOnline, lanOnline bool) DeviceState {
	if !cloudPresent && !gatewayOnline && !lanOnline {
		return DeviceStateDisable
	}
	if cloudOnline || gatewayOnline || lanOnline {
		return DeviceStateOnline
	}
	return DeviceStateOffline
}

func propertySubscriptionMatches(req PropertySubscription, result PropertyResult) bool {
	if req.DID != result.DID {
		return false
	}
	if req.SIID > 0 && req.SIID != result.SIID {
		return false
	}
	if req.PIID > 0 && req.PIID != result.PIID {
		return false
	}
	return true
}

func propertyQueriesFromMap(items map[string]PropertyQuery) []PropertyQuery {
	queries := make([]PropertyQuery, 0, len(items))
	for _, query := range items {
		queries = append(queries, query)
	}
	return queries
}

func firstPropertyPerDevice(items map[string]PropertyQuery) map[string]PropertyQuery {
	selected := make(map[string]PropertyQuery)
	seen := make(map[string]struct{})
	for key, query := range items {
		if _, ok := seen[query.DID]; ok {
			continue
		}
		seen[query.DID] = struct{}{}
		selected[key] = query
	}
	return selected
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clonePropertyQueryMap(in map[string]PropertyQuery) map[string]PropertyQuery {
	out := make(map[string]PropertyQuery, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (c *MIoTClient) execResultError(op string, code int) error {
	return &Error{Code: ErrProtocolFailure, Op: op, Msg: fmt.Sprintf("result code %d", code)}
}

func actionCodeOK(code int) bool {
	return code == 0 || code == 1
}

func (r SetPropertyResult) CodeOK() bool {
	return actionCodeOK(r.Code)
}

func cloneDeviceEntry(entry *miotClientDeviceEntry) *miotClientDeviceEntry {
	if entry == nil {
		return nil
	}
	copyEntry := *entry
	copyEntry.info = cloneDeviceInfo(entry.info)
	return &copyEntry
}

func cloneDeviceInfo(info DeviceInfo) DeviceInfo {
	out := info
	if info.SubDevices != nil {
		out.SubDevices = make(map[string]DeviceInfo, len(info.SubDevices))
		for key, value := range info.SubDevices {
			out.SubDevices[key] = cloneDeviceInfo(value)
		}
	}
	return out
}

func mergeDeviceInfo(current, next DeviceInfo) DeviceInfo {
	merged := cloneDeviceInfo(next)
	if merged.HomeID == "" {
		merged.HomeID = current.HomeID
	}
	if merged.HomeName == "" {
		merged.HomeName = current.HomeName
	}
	if merged.RoomID == "" {
		merged.RoomID = current.RoomID
	}
	if merged.RoomName == "" {
		merged.RoomName = current.RoomName
	}
	if merged.GroupID == "" {
		merged.GroupID = current.GroupID
	}
	if merged.Token == "" {
		merged.Token = current.Token
	}
	if merged.ConnectType == 0 && current.ConnectType != 0 {
		merged.ConnectType = current.ConnectType
	}
	if merged.Name == "" {
		merged.Name = current.Name
	}
	if merged.Model == "" {
		merged.Model = current.Model
	}
	if merged.URN == "" {
		merged.URN = current.URN
	}
	return merged
}

// StorageOAuthTokenStore adapts Storage user-config entries into an MIoTAuthStore.
type StorageOAuthTokenStore struct {
	storage     *Storage
	uid         string
	cloudServer string
}

// NewStorageOAuthTokenStore creates an MIoTAuthStore backed by Storage user config.
func NewStorageOAuthTokenStore(storage *Storage, uid, cloudServer string) MIoTAuthStore {
	return StorageOAuthTokenStore{
		storage:     storage,
		uid:         uid,
		cloudServer: cloudServer,
	}
}

func (s StorageOAuthTokenStore) LoadOAuthToken(ctx context.Context) (OAuthToken, error) {
	if s.storage == nil {
		return OAuthToken{}, &Error{Code: ErrInvalidArgument, Op: "load oauth token", Msg: "storage is nil"}
	}
	doc, err := s.storage.LoadUserConfig(ctx, s.uid, s.cloudServer, "auth_info")
	if err != nil {
		return OAuthToken{}, err
	}
	for _, entry := range doc.Entries {
		if entry.Key == "auth_info" {
			return DecodeUserConfigEntry[OAuthToken](entry)
		}
	}
	return OAuthToken{}, &Error{Code: ErrInvalidArgument, Op: "load oauth token", Msg: "auth_info entry not found"}
}

func (s StorageOAuthTokenStore) SaveOAuthToken(ctx context.Context, token OAuthToken) error {
	if s.storage == nil {
		return &Error{Code: ErrInvalidArgument, Op: "save oauth token", Msg: "storage is nil"}
	}
	entry, err := NewUserConfigEntry("auth_info", token)
	if err != nil {
		return err
	}
	return s.storage.UpdateUserConfig(ctx, s.uid, s.cloudServer, &UserConfigDocument{
		Entries: []UserConfigEntry{entry},
	}, false)
}
