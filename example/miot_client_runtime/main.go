package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"time"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

const (
	defaultClientID              = "2882303761520251711"
	defaultCloudServer           = "cn"
	defaultStorageDir            = ".miot-example-cache"
	defaultSnapshotInterval      = 30 * time.Second
	defaultMDNSBootstrapTimeout  = 5 * time.Second
	runtimeUserCertRefreshSkew   = 72 * time.Hour
	runtimeLocalPreflightTimeout = 10 * time.Second
)

type staticTokenProvider struct {
	accessToken string
}

func (p staticTokenProvider) AccessToken(context.Context) (string, error) {
	token := strings.TrimSpace(p.accessToken)
	if token == "" {
		return "", fmt.Errorf("runtime token provider: access token is empty")
	}
	return token, nil
}

type runtimeCertMaterial struct {
	caPEM   []byte
	keyPEM  []byte
	certPEM []byte
}

type runtimeLocalRouteState struct {
	GroupID   string `json:"group_id"`
	HomeID    string `json:"home_id,omitempty"`
	HomeName  string `json:"home_name,omitempty"`
	Host      string `json:"host,omitempty"`
	Port      int    `json:"port,omitempty"`
	Admitted  bool   `json:"admitted"`
	Connected bool   `json:"connected"`
	LastError string `json:"last_error,omitempty"`
}

type runtimeDeviceSourceCounts struct {
	CloudOnline       int `json:"cloud_online"`
	GatewayOnline     int `json:"gateway_online"`
	LANOnline         int `json:"lan_online"`
	PushSourceCloud   int `json:"push_source_cloud"`
	PushSourceGateway int `json:"push_source_gateway"`
	PushSourceLAN     int `json:"push_source_lan"`
}

type runtimeHealthSnapshot struct {
	Warnings                 []string
	NetworkOnline            bool
	CloudPushConnected       bool
	CloudPushLastError       string
	LANControlEnabled        bool
	DiscoveryServiceCount    int
	LocalRouteCandidateCount int
	LocalRoutes              []runtimeLocalRouteState
}

type runtimeSnapshotMeta struct {
	Type              string
	UID               string
	StorageDir        string
	HomeCount         int
	LocalRouteCount   int
	CloudPushHost     string
	CloudPushPort     int
	CloudPushClientID string
	Timestamp         string
}

type runtimeHealth struct {
	mu                       sync.RWMutex
	warnings                 []string
	networkOnline            bool
	cloudPushConnected       bool
	cloudPushLastError       string
	lanControlEnabled        bool
	discoveryServiceCount    int
	localRouteCandidateCount int
	localRoutes              map[string]runtimeLocalRouteState
}

type runtimeLocalRoutePreflightClient interface {
	Start(ctx context.Context) error
	AwaitConnection(ctx context.Context) error
	GetDeviceList(ctx context.Context) ([]miot.LocalDeviceSummary, error)
	Close() error
}

func defaultRuntimeExampleConfig() exampleutil.RuntimeExampleConfig {
	return exampleutil.RuntimeExampleConfig{
		ClientID:             defaultClientID,
		CloudServer:          defaultCloudServer,
		StorageDir:           defaultStorageDir,
		SnapshotInterval:     defaultSnapshotInterval,
		MDNSBootstrapTimeout: defaultMDNSBootstrapTimeout,
	}
}

func runtimeOAuthTokenSource(cfg exampleutil.RuntimeExampleConfig, bootstrap exampleutil.RuntimeBootstrapState) string {
	envAccess := strings.TrimSpace(cfg.AccessToken)
	envRefresh := strings.TrimSpace(cfg.RefreshToken)

	switch {
	case envAccess != "" && envRefresh != "":
		return "env_pair"
	case envAccess != "" || envRefresh != "":
		return "partial_env_pair"
	case strings.TrimSpace(bootstrap.UID) != "":
		return "storage_auth_info"
	default:
		return "missing"
	}
}

func newRuntimeHealth() *runtimeHealth {
	return &runtimeHealth{
		localRoutes: make(map[string]runtimeLocalRouteState),
	}
}

func (h *runtimeHealth) SetWarnings(warnings []string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.warnings = append([]string(nil), warnings...)
}

func (h *runtimeHealth) SetNetworkOnline(online bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.networkOnline = online
}

func (h *runtimeHealth) SetCloudPushConnected(connected bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cloudPushConnected = connected
}

func (h *runtimeHealth) SetCloudPushLastError(message string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cloudPushLastError = strings.TrimSpace(message)
}

func (h *runtimeHealth) SetLANControlEnabled(enabled bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lanControlEnabled = enabled
}

func (h *runtimeHealth) SetDiscoveryServiceCount(count int) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.discoveryServiceCount = count
}

func (h *runtimeHealth) SetLocalRouteCandidateCount(count int) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.localRouteCandidateCount = count
}

func (h *runtimeHealth) UpsertLocalRoute(state runtimeLocalRouteState) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.localRoutes[state.GroupID] = state
}

func (h *runtimeHealth) SetLocalRouteConnected(groupID string, connected bool) {
	if h == nil || strings.TrimSpace(groupID) == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.localRoutes[groupID]
	state.GroupID = groupID
	state.Connected = connected
	h.localRoutes[groupID] = state
}

func (h *runtimeHealth) Snapshot() runtimeHealthSnapshot {
	if h == nil {
		return runtimeHealthSnapshot{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()

	routes := make([]runtimeLocalRouteState, 0, len(h.localRoutes))
	for _, route := range h.localRoutes {
		routes = append(routes, route)
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].GroupID < routes[j].GroupID
	})

	return runtimeHealthSnapshot{
		Warnings:                 append([]string(nil), h.warnings...),
		NetworkOnline:            h.networkOnline,
		CloudPushConnected:       h.cloudPushConnected,
		CloudPushLastError:       h.cloudPushLastError,
		LANControlEnabled:        h.lanControlEnabled,
		DiscoveryServiceCount:    h.discoveryServiceCount,
		LocalRouteCandidateCount: h.localRouteCandidateCount,
		LocalRoutes:              routes,
	}
}

type runtimeStartupSummary struct {
	Type                     string                    `json:"type"`
	UID                      string                    `json:"uid"`
	CloudServer              string                    `json:"cloud_server"`
	StorageDir               string                    `json:"storage_dir"`
	DeviceCount              int                       `json:"device_count"`
	OnlineCount              int                       `json:"online_count"`
	HomeCount                int                       `json:"home_count"`
	LocalRouteCount          int                       `json:"local_route_count"`
	DiscoveryServiceCount    int                       `json:"discovery_service_count"`
	LocalRouteCandidateCount int                       `json:"local_route_candidate_count"`
	CloudPushEnabled         bool                      `json:"cloud_push_enabled"`
	CloudPushHost            string                    `json:"cloud_push_host,omitempty"`
	CloudPushPort            int                       `json:"cloud_push_port,omitempty"`
	CloudPushClientID        string                    `json:"cloud_push_client_id,omitempty"`
	LANEnabled               bool                      `json:"lan_enabled"`
	NetworkOnline            bool                      `json:"network_online"`
	CloudPushConnected       bool                      `json:"cloud_push_connected"`
	CloudPushLastError       string                    `json:"cloud_push_last_error,omitempty"`
	LANControlEnabled        bool                      `json:"lan_control_enabled"`
	Warnings                 []string                  `json:"warnings,omitempty"`
	LocalRoutes              []runtimeLocalRouteState  `json:"local_routes"`
	DeviceSourceCounts       runtimeDeviceSourceCounts `json:"device_source_counts"`
	Timestamp                string                    `json:"timestamp"`
}

type runtimeSnapshotEvent struct {
	Type                     string                    `json:"type"`
	UID                      string                    `json:"uid"`
	StorageDir               string                    `json:"storage_dir"`
	DeviceCount              int                       `json:"device_count"`
	OnlineCount              int                       `json:"online_count"`
	HomeCount                int                       `json:"home_count"`
	LocalRouteCount          int                       `json:"local_route_count"`
	DiscoveryServiceCount    int                       `json:"discovery_service_count"`
	LocalRouteCandidateCount int                       `json:"local_route_candidate_count"`
	CloudPushEnabled         bool                      `json:"cloud_push_enabled"`
	CloudPushHost            string                    `json:"cloud_push_host,omitempty"`
	CloudPushPort            int                       `json:"cloud_push_port,omitempty"`
	CloudPushClientID        string                    `json:"cloud_push_client_id,omitempty"`
	LANEnabled               bool                      `json:"lan_enabled"`
	NetworkOnline            bool                      `json:"network_online"`
	CloudPushConnected       bool                      `json:"cloud_push_connected"`
	CloudPushLastError       string                    `json:"cloud_push_last_error,omitempty"`
	LANControlEnabled        bool                      `json:"lan_control_enabled"`
	Warnings                 []string                  `json:"warnings,omitempty"`
	LocalRoutes              []runtimeLocalRouteState  `json:"local_routes"`
	DeviceSourceCounts       runtimeDeviceSourceCounts `json:"device_source_counts"`
	Timestamp                string                    `json:"timestamp"`
}

type runtimeShutdownEvent struct {
	Type      string `json:"type"`
	UID       string `json:"uid"`
	Timestamp string `json:"timestamp"`
	Reason    string `json:"reason"`
}

type runtimeLogger struct {
	logger *log.Logger
}

func newRuntimeLogger(w io.Writer) *runtimeLogger {
	return &runtimeLogger{logger: log.New(w, "", 0)}
}

func (l *runtimeLogger) StartStep(step, detail string) func(error) {
	if l == nil || l.logger == nil {
		return func(error) {}
	}
	trimmedDetail := strings.TrimSpace(detail)
	if trimmedDetail == "" {
		l.logger.Printf("runtime step=%s status=start", step)
	} else {
		l.logger.Printf("runtime step=%s status=start %s", step, trimmedDetail)
	}
	startedAt := time.Now()
	return func(err error) {
		if err != nil {
			l.logger.Printf("runtime step=%s status=error duration=%s err=%q", step, time.Since(startedAt).Round(time.Millisecond), err.Error())
			return
		}
		l.logger.Printf("runtime step=%s status=ok duration=%s", step, time.Since(startedAt).Round(time.Millisecond))
	}
}

func (l *runtimeLogger) Infof(step, format string, args ...any) {
	if l == nil || l.logger == nil {
		return
	}
	message := strings.TrimSpace(fmt.Sprintf(format, args...))
	if message == "" {
		l.logger.Printf("runtime step=%s status=info", step)
		return
	}
	l.logger.Printf("runtime step=%s status=info msg=%q", step, message)
}

func (l *runtimeLogger) Warnf(step, format string, args ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Printf("runtime step=%s status=warn msg=%q", step, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func (l *runtimeLogger) Statef(step, format string, args ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Printf("runtime step=%s status=state msg=%q", step, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func main() {
	log.SetFlags(0)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	diag := newRuntimeLogger(os.Stderr)
	loadConfigDone := diag.StartStep("load_config", "")
	cfg, err := exampleutil.LoadRuntimeExampleConfig(defaultRuntimeExampleConfig())
	loadConfigDone(err)
	if err != nil {
		log.Fatal(err)
	}
	diag.Infof(
		"load_config",
		"cloud_server=%s storage_dir=%s snapshot_interval=%s mdns_bootstrap_timeout=%s",
		cfg.CloudServer,
		cfg.StorageDir,
		cfg.SnapshotInterval,
		cfg.MDNSBootstrapTimeout,
	)

	if err := runRuntime(ctx, cfg, diag); err != nil {
		log.Fatal(err)
	}
}

func runRuntime(ctx context.Context, cfg exampleutil.RuntimeExampleConfig, diag *runtimeLogger) error {
	if diag == nil {
		diag = newRuntimeLogger(io.Discard)
	}

	storageDone := diag.StartStep("storage_init", "storage_dir="+cfg.StorageDir)
	storage, err := miot.NewStorage(cfg.StorageDir)
	storageDone(err)
	if err != nil {
		return fmt.Errorf("create storage: %w", err)
	}

	var (
		bootstrapWarnings []string
		cloudPush         *miot.CloudMIPSClient
		monitor           *miot.NetworkMonitor
		lan               *miot.LANClient
		discovery         *miot.MIPSDiscovery
		networkBinding    miot.Subscription
		discoveryBinding  miot.Subscription
		client            *miot.MIoTClient
		localRoutes       = make(map[string]miot.MIoTLocalBackend)
		stateSubs         []miot.Subscription
		health            = newRuntimeHealth()
	)
	defer func() {
		for _, sub := range stateSubs {
			if sub != nil {
				_ = sub.Close()
			}
		}
		if client != nil {
			_ = client.Close()
		} else {
			_ = closeLocalRoutes(localRoutes)
			if lan != nil {
				_ = lan.Close()
			}
			if cloudPush != nil {
				_ = cloudPush.Close()
			}
		}
		if discoveryBinding != nil {
			_ = discoveryBinding.Close()
		}
		if networkBinding != nil {
			_ = networkBinding.Close()
		}
		if discovery != nil {
			_ = discovery.Close()
		}
		if monitor != nil {
			_ = monitor.Close()
		}
	}()

	bootstrapDone := diag.StartStep("bootstrap_state_load", "")
	bootstrap, err := exampleutil.LoadRuntimeBootstrapState(ctx, storage)
	bootstrapDone(err)
	if err != nil {
		return fmt.Errorf("load runtime bootstrap state: %w", err)
	}
	diag.Infof(
		"bootstrap_state_load",
		"uid_present=%t cloud_mips_uuid_present=%t runtime_did_present=%t",
		strings.TrimSpace(bootstrap.UID) != "",
		strings.TrimSpace(bootstrap.CloudMIPSUUID) != "",
		strings.TrimSpace(bootstrap.RuntimeDID) != "",
	)

	tokenDone := diag.StartStep("oauth_token_resolve", "")
	tokenSource := runtimeOAuthTokenSource(cfg, bootstrap)
	token, err := exampleutil.ResolveRuntimeOAuthToken(ctx, cfg, storage, bootstrap)
	tokenDone(err)
	if err != nil {
		return fmt.Errorf("resolve runtime oauth token: %w", err)
	}
	diag.Infof(
		"oauth_token_resolve",
		"source=%s access_token_len=%d refresh_token_present=%t",
		tokenSource,
		len(strings.TrimSpace(token.AccessToken)),
		strings.TrimSpace(token.RefreshToken) != "",
	)

	cloudClientDone := diag.StartStep("cloud_client_init", "cloud_server="+cfg.CloudServer)
	cloud, err := miot.NewCloudClient(
		miot.CloudConfig{
			ClientID:    cfg.ClientID,
			CloudServer: cfg.CloudServer,
		},
		miot.WithCloudTokenProvider(staticTokenProvider{accessToken: token.AccessToken}),
	)
	cloudClientDone(err)
	if err != nil {
		return fmt.Errorf("create cloud client: %w", err)
	}

	uid := strings.TrimSpace(bootstrap.UID)
	if uid == "" {
		resolveUIDDone := diag.StartStep("resolve_uid", "source=cloud_profile")
		uid, err = cloud.GetUID(ctx)
		resolveUIDDone(err)
		if err != nil {
			return fmt.Errorf("resolve uid: %w", err)
		}
	} else {
		diag.Infof("resolve_uid", "source=bootstrap_state uid=%s", uid)
	}
	if uid == "" {
		return fmt.Errorf("resolve uid: empty result")
	}

	runtimeDIDDone := diag.StartStep("runtime_did_resolve", "")
	runtimeDID, err := ensureRuntimeDID(bootstrap.RuntimeDID)
	runtimeDIDDone(err)
	if err != nil {
		return fmt.Errorf("resolve runtime did: %w", err)
	}
	diag.Infof("runtime_did_resolve", "runtime_did=%s", runtimeDID)
	cloudUUIDDone := diag.StartStep("cloud_push_uuid_resolve", "")
	legacyCloudUUID := strings.TrimSpace(bootstrap.CloudMIPSUUID)
	cloudMIPSUUID, changedCloudUUID, err := exampleutil.NormalizeRuntimeCloudMIPSUUID(bootstrap.CloudMIPSUUID)
	cloudUUIDDone(err)
	if err != nil {
		return fmt.Errorf("resolve cloud push uuid: %w", err)
	}
	if changedCloudUUID && legacyCloudUUID != "" && legacyCloudUUID != cloudMIPSUUID {
		diag.Warnf("cloud_push_uuid_resolve", "rotated legacy uuid old=%s new=%s", legacyCloudUUID, cloudMIPSUUID)
	}
	diag.Infof("cloud_push_uuid_resolve", "uuid=%s", cloudMIPSUUID)

	bootstrap.UID = uid
	bootstrap.RuntimeDID = runtimeDID
	bootstrap.CloudMIPSUUID = cloudMIPSUUID
	saveBootstrapDone := diag.StartStep("bootstrap_state_save", "")
	if err := exampleutil.SaveRuntimeBootstrapState(ctx, storage, bootstrap); err != nil {
		saveBootstrapDone(err)
		return fmt.Errorf("save runtime bootstrap state: %w", err)
	}
	saveBootstrapDone(nil)

	if strings.TrimSpace(cfg.AccessToken) != "" || strings.TrimSpace(cfg.RefreshToken) != "" {
		persistTokenDone := diag.StartStep("oauth_token_persist", "")
		if err := exampleutil.PersistRuntimeOAuthToken(ctx, storage, uid, cfg.CloudServer, cfg.ClientID, token); err != nil {
			persistTokenDone(err)
			return fmt.Errorf("persist runtime oauth token: %w", err)
		}
		persistTokenDone(nil)
	}

	loadHomesDone := diag.StartStep("home_infos_load", "")
	infos, err := cloud.GetHomeInfos(ctx)
	loadHomesDone(err)
	if err != nil {
		return fmt.Errorf("load home infos: %w", err)
	}
	homes := flattenRuntimeHomes(infos)
	diag.Infof("home_infos_load", "homes=%d shared_homes=%d", len(infos.HomeList), len(infos.ShareHomeList))

	cloudPushDone := diag.StartStep("cloud_push_init", "")
	cloudPush, err = miot.NewCloudMIPSClient(miot.MIPSCloudConfig{
		UUID:        cloudMIPSUUID,
		CloudServer: cfg.CloudServer,
		AppID:       cfg.ClientID,
		Token:       token.AccessToken,
	})
	cloudPushDone(err)
	if err != nil {
		return fmt.Errorf("create cloud push client: %w", err)
	}
	health.SetCloudPushConnected(false)
	health.SetCloudPushLastError("")
	stateSubs = append(stateSubs, cloudPush.SubscribeConnectionState(func(connected bool) {
		health.SetCloudPushConnected(connected)
		if connected {
			health.SetCloudPushLastError("")
		}
		diag.Statef("cloud_push_state", "connected=%t host=%s port=%d client_id=%s", connected, cloudPush.Host(), cloudPush.Port(), cloudPush.ClientID())
	}))
	diag.Statef("cloud_push_state", "connected=%t host=%s port=%d client_id=%s", false, cloudPush.Host(), cloudPush.Port(), cloudPush.ClientID())

	networkDone := diag.StartStep("network_monitor_start", "")
	monitor = miot.NewNetworkMonitor(nil, nil)
	if err := monitor.Start(ctx); err != nil {
		networkDone(err)
		return fmt.Errorf("start network monitor: %w", err)
	}
	networkDone(nil)
	health.SetNetworkOnline(monitor.Status())
	diag.Statef("network_state", "online=%t", monitor.Status())
	stateSubs = append(stateSubs, monitor.SubscribeStatus(func(online bool) {
		health.SetNetworkOnline(online)
		diag.Statef("network_state", "online=%t", online)
	}))

	lanInitDone := diag.StartStep("lan_init", "")
	lan = miot.NewLANClient(nil)
	lanInitDone(nil)
	health.SetLANControlEnabled(false)
	stateSubs = append(stateSubs, lan.SubscribeLANState(func(enabled bool) {
		health.SetLANControlEnabled(enabled)
		diag.Statef("lan_control_state", "enabled=%t", enabled)
	}))
	diag.Statef("lan_control_state", "enabled=%t", false)
	lanBindDone := diag.StartStep("lan_bind_network_monitor", "")
	networkBinding, err = lan.BindNetworkMonitor(monitor)
	lanBindDone(err)
	if err != nil {
		bootstrapWarnings = appendBootstrapWarning(bootstrapWarnings, diag, "lan_bind_network_monitor", err)
		health.SetWarnings(bootstrapWarnings)
	}

	discovery = miot.NewMIPSDiscovery(nil)
	discoveryStarted := false
	mdnsStartDone := diag.StartStep("mdns_start", "")
	if err := discovery.Start(ctx); err != nil {
		mdnsStartDone(err)
		bootstrapWarnings = appendBootstrapWarning(bootstrapWarnings, diag, "mdns_start", err)
		health.SetWarnings(bootstrapWarnings)
	} else {
		mdnsStartDone(nil)
		discoveryStarted = true
		diag.Infof("mdns_bootstrap_wait", "timeout=%s", cfg.MDNSBootstrapTimeout)
		waitForDiscoveryBootstrap(ctx, cfg.MDNSBootstrapTimeout)
		discoveryServiceCount := len(discovery.Services())
		health.SetDiscoveryServiceCount(discoveryServiceCount)
		if discoveryServiceCount == 0 {
			bootstrapWarnings = appendBootstrapWarningMessage(bootstrapWarnings, diag, "mdns_bootstrap", "no MIoT central gateways discovered during bootstrap window")
			health.SetWarnings(bootstrapWarnings)
		} else {
			diag.Infof("mdns_bootstrap", "services=%d", discoveryServiceCount)
		}
	}

	if discoveryStarted {
		bindDiscoveryDone := diag.StartStep("lan_bind_mips_discovery", "")
		discoveryBinding, err = lan.BindMIPSDiscovery(discovery)
		bindDiscoveryDone(err)
		if err != nil {
			bootstrapWarnings = appendBootstrapWarning(bootstrapWarnings, diag, "lan_bind_mips_discovery", err)
			health.SetWarnings(bootstrapWarnings)
		}
	}

	certManagerDone := diag.StartStep("cert_manager_init", "")
	certManager, err := miot.NewCertManager(storage, uid, cfg.CloudServer)
	certManagerDone(err)
	if err != nil {
		return fmt.Errorf("create cert manager: %w", err)
	}

	if discoveryStarted {
		certMaterial, certWarnings, certErr := ensureRuntimeCertMaterial(ctx, cloud, certManager, runtimeDID, diag)
		bootstrapWarnings = append(bootstrapWarnings, certWarnings...)
		health.SetWarnings(bootstrapWarnings)
		if certErr != nil {
			bootstrapWarnings = appendBootstrapWarning(bootstrapWarnings, diag, "cert_bootstrap", certErr)
			health.SetWarnings(bootstrapWarnings)
		} else {
			candidates := exampleutil.BuildRuntimeLocalRouteCandidates(homes, discovery.Services(), runtimeDID)
			health.SetLocalRouteCandidateCount(len(candidates))
			if len(candidates) == 0 && len(discovery.Services()) > 0 {
				bootstrapWarnings = appendBootstrapWarningMessage(bootstrapWarnings, diag, "local_route_candidates", "discovered MIoT gateways did not match any selected home group")
				health.SetWarnings(bootstrapWarnings)
			}
			for _, candidate := range candidates {
				route, routeErr := admitRuntimeLocalRoute(ctx, candidate, certMaterial, diag)
				if routeErr != nil {
					health.UpsertLocalRoute(runtimeLocalRouteState{
						GroupID:   candidate.GroupID,
						HomeID:    candidate.HomeID,
						HomeName:  candidate.HomeName,
						Host:      candidate.Host,
						Port:      candidate.Port,
						Admitted:  false,
						Connected: false,
						LastError: routeErr.Error(),
					})
					bootstrapWarnings = appendBootstrapWarning(bootstrapWarnings, diag, "local_route_"+candidate.GroupID, routeErr)
					health.SetWarnings(bootstrapWarnings)
					continue
				}
				localRoutes[candidate.GroupID] = route
				health.UpsertLocalRoute(runtimeLocalRouteState{
					GroupID:   candidate.GroupID,
					HomeID:    candidate.HomeID,
					HomeName:  candidate.HomeName,
					Host:      candidate.Host,
					Port:      candidate.Port,
					Admitted:  true,
					Connected: false,
				})
				diag.Infof("local_route_admitted", "group_id=%s home_name=%s host=%s port=%d", candidate.GroupID, candidate.HomeName, candidate.Host, candidate.Port)
				if source, ok := route.(interface {
					SubscribeConnectionState(func(bool)) miot.Subscription
				}); ok {
					groupID := candidate.GroupID
					homeName := candidate.HomeName
					host := candidate.Host
					port := candidate.Port
					stateSubs = append(stateSubs, source.SubscribeConnectionState(func(connected bool) {
						health.SetLocalRouteConnected(groupID, connected)
						diag.Statef("local_route_state", "group_id=%s home_name=%s host=%s port=%d connected=%t", groupID, homeName, host, port, connected)
					}))
				}
			}
		}
	}

	authStoreDone := diag.StartStep("auth_store_init", "")
	authStore := exampleutil.NewRuntimeOAuthTokenStore(storage, uid, cfg.CloudServer, cfg.ClientID)
	authStoreDone(nil)

	clientInitDone := diag.StartStep("miot_client_init", "")
	client, err = miot.NewMIoTClient(miot.MIoTClientConfig{
		UID:         uid,
		CloudServer: cfg.CloudServer,
		ControlMode: miot.MIoTControlModeAuto,
		VirtualDID:  runtimeDID,
		Homes:       homes,
		Cloud:       cloud,
		CloudPush:   cloudPush,
		LocalRoutes: localRoutes,
		LAN:         lan,
		Network:     monitor,
		AuthStore:   authStore,
		Cert:        certManager,
	})
	clientInitDone(err)
	if err != nil {
		return fmt.Errorf("create miot client: %w", err)
	}

	clientStartDone := diag.StartStep("miot_client_start", "")
	if err := client.Start(ctx); err != nil {
		clientStartDone(err)
		return fmt.Errorf("start miot client: %w", err)
	}
	clientStartDone(nil)
	syncRuntimeCloudPushError(health, cloudPush, diag)
	devices := client.Devices()
	startup := buildRuntimeStartupSummary(devices, cfg.CloudServer, runtimeSnapshotMeta{
		Type:              "startup_summary",
		UID:               uid,
		StorageDir:        storage.RootPath(),
		HomeCount:         len(homes),
		LocalRouteCount:   len(localRoutes),
		CloudPushHost:     cloudPush.Host(),
		CloudPushPort:     cloudPush.Port(),
		CloudPushClientID: cloudPush.ClientID(),
		Timestamp:         nowTimestamp(),
	}, health.Snapshot())
	diag.Infof(
		"miot_client_start",
		"uid=%s homes=%d devices=%d online=%d local_routes=%d/%d network_online=%t cloud_push_connected=%t lan_control_enabled=%t",
		startup.UID,
		startup.HomeCount,
		startup.DeviceCount,
		startup.OnlineCount,
		startup.LocalRouteCount,
		startup.LocalRouteCandidateCount,
		startup.NetworkOnline,
		startup.CloudPushConnected,
		startup.LANControlEnabled,
	)

	if err := exampleutil.PrintJSONStdout(startup); err != nil {
		return fmt.Errorf("emit startup summary: %w", err)
	}

	ticker := time.NewTicker(time.Second * 3)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			diag.Infof("shutdown", "reason=interrupt")
			if err := exampleutil.PrintJSONStdout(runtimeShutdownEvent{
				Type:      "shutdown",
				UID:       uid,
				Timestamp: nowTimestamp(),
				Reason:    "interrupt",
			}); err != nil {
				log.Printf("emit shutdown summary: %v", err)
			}
			return nil
		case <-ticker.C:
			syncRuntimeCloudPushError(health, cloudPush, diag)
			snapshot, err := emitRuntimeSnapshot(
				client,
				uid,
				storage.RootPath(),
				len(homes),
				len(localRoutes),
				cloudPush.Host(),
				cloudPush.Port(),
				cloudPush.ClientID(),
				health.Snapshot(),
			)
			if err != nil {
				log.Printf("emit runtime snapshot: %v", err)
			} else {
				diag.Infof(
					"snapshot",
					"devices=%d online=%d homes=%d local_routes=%d/%d network_online=%t cloud_push_connected=%t lan_control_enabled=%t warnings=%d",
					snapshot.DeviceCount,
					snapshot.OnlineCount,
					snapshot.HomeCount,
					snapshot.LocalRouteCount,
					snapshot.LocalRouteCandidateCount,
					snapshot.NetworkOnline,
					snapshot.CloudPushConnected,
					snapshot.LANControlEnabled,
					len(snapshot.Warnings),
				)
			}
			for _, v := range client.Devices() {
				fmt.Println(v.Info.Name, v.State)
			}
			client.GetProperty()
		}
	}
}

func emitRuntimeSnapshot(
	client *miot.MIoTClient,
	uid,
	storageDir string,
	homeCount,
	localRouteCount int,
	cloudPushHost string,
	cloudPushPort int,
	cloudPushClientID string,
	health runtimeHealthSnapshot,
) (runtimeSnapshotEvent, error) {
	event := buildRuntimeSnapshotEvent(client.Devices(), runtimeSnapshotMeta{
		Type:              "snapshot",
		UID:               uid,
		StorageDir:        storageDir,
		HomeCount:         homeCount,
		LocalRouteCount:   localRouteCount,
		CloudPushHost:     cloudPushHost,
		CloudPushPort:     cloudPushPort,
		CloudPushClientID: cloudPushClientID,
		Timestamp:         nowTimestamp(),
	}, health)
	if err := exampleutil.PrintJSONStdout(event); err != nil {
		return runtimeSnapshotEvent{}, err
	}
	return event, nil
}

func buildRuntimeStartupSummary(devices map[string]miot.MIoTClientDevice, cloudServer string, meta runtimeSnapshotMeta, health runtimeHealthSnapshot) runtimeStartupSummary {
	sourceCounts := countRuntimeDeviceSources(devices)
	return runtimeStartupSummary{
		Type:                     meta.Type,
		UID:                      meta.UID,
		CloudServer:              cloudServer,
		StorageDir:               meta.StorageDir,
		DeviceCount:              len(devices),
		OnlineCount:              countOnlineDevices(devices),
		HomeCount:                meta.HomeCount,
		LocalRouteCount:          meta.LocalRouteCount,
		DiscoveryServiceCount:    health.DiscoveryServiceCount,
		LocalRouteCandidateCount: health.LocalRouteCandidateCount,
		CloudPushEnabled:         true,
		CloudPushHost:            meta.CloudPushHost,
		CloudPushPort:            meta.CloudPushPort,
		CloudPushClientID:        meta.CloudPushClientID,
		LANEnabled:               true,
		NetworkOnline:            health.NetworkOnline,
		CloudPushConnected:       health.CloudPushConnected,
		CloudPushLastError:       health.CloudPushLastError,
		LANControlEnabled:        health.LANControlEnabled,
		Warnings:                 append([]string(nil), health.Warnings...),
		LocalRoutes:              append([]runtimeLocalRouteState(nil), health.LocalRoutes...),
		DeviceSourceCounts:       sourceCounts,
		Timestamp:                meta.Timestamp,
	}
}

func buildRuntimeSnapshotEvent(devices map[string]miot.MIoTClientDevice, meta runtimeSnapshotMeta, health runtimeHealthSnapshot) runtimeSnapshotEvent {
	sourceCounts := countRuntimeDeviceSources(devices)
	return runtimeSnapshotEvent{
		Type:                     meta.Type,
		UID:                      meta.UID,
		StorageDir:               meta.StorageDir,
		DeviceCount:              len(devices),
		OnlineCount:              countOnlineDevices(devices),
		HomeCount:                meta.HomeCount,
		LocalRouteCount:          meta.LocalRouteCount,
		DiscoveryServiceCount:    health.DiscoveryServiceCount,
		LocalRouteCandidateCount: health.LocalRouteCandidateCount,
		CloudPushEnabled:         true,
		CloudPushHost:            meta.CloudPushHost,
		CloudPushPort:            meta.CloudPushPort,
		CloudPushClientID:        meta.CloudPushClientID,
		LANEnabled:               true,
		NetworkOnline:            health.NetworkOnline,
		CloudPushConnected:       health.CloudPushConnected,
		CloudPushLastError:       health.CloudPushLastError,
		LANControlEnabled:        health.LANControlEnabled,
		Warnings:                 append([]string(nil), health.Warnings...),
		LocalRoutes:              append([]runtimeLocalRouteState(nil), health.LocalRoutes...),
		DeviceSourceCounts:       sourceCounts,
		Timestamp:                meta.Timestamp,
	}
}

func countOnlineDevices(devices map[string]miot.MIoTClientDevice) int {
	count := 0
	for _, device := range devices {
		if device.State == miot.DeviceStateOnline {
			count++
		}
	}
	return count
}

func countRuntimeDeviceSources(devices map[string]miot.MIoTClientDevice) runtimeDeviceSourceCounts {
	var counts runtimeDeviceSourceCounts
	for _, device := range devices {
		if device.CloudOnline {
			counts.CloudOnline++
		}
		if device.GatewayOnline {
			counts.GatewayOnline++
		}
		if device.LANOnline {
			counts.LANOnline++
		}
		switch device.PushSource {
		case "cloud":
			counts.PushSourceCloud++
		case "gateway":
			counts.PushSourceGateway++
		case "lan":
			counts.PushSourceLAN++
		case "":
			// no active push source
		default:
			counts.PushSourceGateway++
		}
	}
	return counts
}

func syncRuntimeCloudPushError(health *runtimeHealth, cloudPush *miot.CloudMIPSClient, diag *runtimeLogger) {
	if health == nil || cloudPush == nil {
		return
	}
	message := ""
	if err := cloudPush.LastConnectionError(); err != nil {
		message = strings.TrimSpace(err.Error())
	}
	before := health.Snapshot().CloudPushLastError
	if before == message {
		return
	}
	health.SetCloudPushLastError(message)
	if message == "" {
		diag.Infof("cloud_push_error", "cleared host=%s port=%d client_id=%s", cloudPush.Host(), cloudPush.Port(), cloudPush.ClientID())
		return
	}
	diag.Warnf("cloud_push_error", "host=%s port=%d client_id=%s err=%s", cloudPush.Host(), cloudPush.Port(), cloudPush.ClientID(), message)
}

func flattenRuntimeHomes(infos miot.HomeInfos) []miot.MIoTClientHome {
	homeIDs := make([]string, 0, len(infos.HomeList)+len(infos.ShareHomeList))
	homesByID := make(map[string]miot.HomeInfo, len(infos.HomeList)+len(infos.ShareHomeList))
	for homeID, home := range infos.HomeList {
		homeIDs = append(homeIDs, homeID)
		homesByID[homeID] = home
	}
	for homeID, home := range infos.ShareHomeList {
		if _, ok := homesByID[homeID]; ok {
			continue
		}
		homeIDs = append(homeIDs, homeID)
		homesByID[homeID] = home
	}
	sort.Strings(homeIDs)

	homes := make([]miot.MIoTClientHome, 0, len(homeIDs))
	for _, homeID := range homeIDs {
		home := homesByID[homeID]
		homes = append(homes, miot.MIoTClientHome{
			HomeID:   home.HomeID,
			HomeName: home.HomeName,
			GroupID:  home.GroupID,
		})
	}
	return homes
}

func ensureRuntimeCertMaterial(ctx context.Context, cloud *miot.CloudClient, certManager *miot.CertManager, runtimeDID string, diag *runtimeLogger) (runtimeCertMaterial, []string, error) {
	var warnings []string

	verifyCADone := diag.StartStep("cert_verify_ca", "")
	if err := certManager.VerifyCACert(ctx); err != nil {
		verifyCADone(err)
		return runtimeCertMaterial{}, warnings, fmt.Errorf("verify ca cert: %w", err)
	}
	verifyCADone(nil)

	loadCAFileDone := diag.StartStep("cert_load_ca_file", "")
	caPEM, err := os.ReadFile(certManager.CAPath())
	if err != nil {
		loadCAFileDone(err)
		return runtimeCertMaterial{}, warnings, fmt.Errorf("load ca cert: %w", err)
	}
	loadCAFileDone(nil)

	userKeyDone := diag.StartStep("cert_load_or_generate_key", "")
	keyPEM, err := certManager.LoadUserKey(ctx)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			userKeyDone(err)
			return runtimeCertMaterial{}, warnings, fmt.Errorf("load user key: %w", err)
		}
		keyPEM, err = certManager.GenerateUserKey()
		if err != nil {
			userKeyDone(err)
			return runtimeCertMaterial{}, warnings, fmt.Errorf("generate user key: %w", err)
		}
		if err := certManager.UpdateUserKey(ctx, keyPEM); err != nil {
			userKeyDone(err)
			return runtimeCertMaterial{}, warnings, fmt.Errorf("persist user key: %w", err)
		}
	}
	userKeyDone(nil)

	userCertDone := diag.StartStep("cert_load_or_refresh_user_cert", "")
	certPEM, err := certManager.LoadUserCert(ctx)
	if err == nil {
		remaining, remainingErr := certManager.UserCertRemaining(ctx, certPEM, runtimeDID)
		if remainingErr == nil && remaining > runtimeUserCertRefreshSkew {
			userCertDone(nil)
			return runtimeCertMaterial{
				caPEM:   caPEM,
				keyPEM:  keyPEM,
				certPEM: certPEM,
			}, warnings, nil
		}
		if remainingErr != nil {
			warnings = appendBootstrapWarning(warnings, diag, "existing_user_cert_validation", remainingErr)
		}
	}

	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		warnings = appendBootstrapWarning(warnings, diag, "existing_user_cert_validation", err)
	}

	csrPEM, err := certManager.GenerateUserCSR(keyPEM, runtimeDID)
	if err != nil {
		userCertDone(err)
		return runtimeCertMaterial{}, warnings, fmt.Errorf("generate user csr: %w", err)
	}

	freshCertPEM, err := cloud.GetCentralCert(ctx, string(csrPEM))
	if err != nil {
		userCertDone(err)
		return runtimeCertMaterial{}, warnings, fmt.Errorf("request central cert: %w", err)
	}
	certPEM = []byte(freshCertPEM)
	if err := certManager.UpdateUserCert(ctx, certPEM); err != nil {
		userCertDone(err)
		return runtimeCertMaterial{}, warnings, fmt.Errorf("persist user cert: %w", err)
	}

	if _, err := certManager.UserCertRemaining(ctx, certPEM, runtimeDID); err != nil {
		userCertDone(err)
		return runtimeCertMaterial{}, warnings, fmt.Errorf("validate user cert: %w", err)
	}
	userCertDone(nil)

	return runtimeCertMaterial{
		caPEM:   caPEM,
		keyPEM:  keyPEM,
		certPEM: certPEM,
	}, warnings, nil
}

func admitRuntimeLocalRoute(ctx context.Context, candidate exampleutil.RuntimeLocalRouteCandidate, certMaterial runtimeCertMaterial, diag *runtimeLogger) (miot.MIoTLocalBackend, error) {
	routeDone := diag.StartStep(
		"local_route_preflight",
		fmt.Sprintf("group_id=%s home_name=%s host=%s port=%d", candidate.GroupID, candidate.HomeName, candidate.Host, candidate.Port),
	)
	if strings.TrimSpace(candidate.Host) == "" {
		err := fmt.Errorf("build local route for group %s: empty host", candidate.GroupID)
		routeDone(err)
		return nil, err
	}

	preflight, err := miot.NewLocalMIPSClient(miot.MIPSLocalConfig{
		ClientDID:     candidate.ClientDID,
		GroupID:       candidate.GroupID,
		HomeName:      candidate.HomeName,
		Host:          candidate.Host,
		Port:          candidate.Port,
		CACertPEM:     certMaterial.caPEM,
		ClientCertPEM: certMaterial.certPEM,
		ClientKeyPEM:  certMaterial.keyPEM,
	})
	if err != nil {
		err = fmt.Errorf("build local route for group %s: %w", candidate.GroupID, err)
		routeDone(err)
		return nil, err
	}

	preflightCtx, cancel := context.WithTimeout(ctx, runtimeLocalPreflightTimeout)
	defer cancel()

	if err := runRuntimeLocalRoutePreflight(preflightCtx, preflight); err != nil {
		err = fmt.Errorf("preflight local route for group %s: %w", candidate.GroupID, err)
		routeDone(err)
		return nil, err
	}

	route, err := miot.NewLocalMIPSClient(miot.MIPSLocalConfig{
		ClientDID:     candidate.ClientDID,
		GroupID:       candidate.GroupID,
		HomeName:      candidate.HomeName,
		Host:          candidate.Host,
		Port:          candidate.Port,
		CACertPEM:     certMaterial.caPEM,
		ClientCertPEM: certMaterial.certPEM,
		ClientKeyPEM:  certMaterial.keyPEM,
	})
	if err != nil {
		err = fmt.Errorf("rebuild local route for group %s: %w", candidate.GroupID, err)
		routeDone(err)
		return nil, err
	}
	routeDone(nil)
	return route, nil
}

func runRuntimeLocalRoutePreflight(ctx context.Context, client runtimeLocalRoutePreflightClient) error {
	if err := client.Start(ctx); err != nil {
		_ = client.Close()
		return fmt.Errorf("start failed: %w", err)
	}
	if err := client.AwaitConnection(ctx); err != nil {
		_ = client.Close()
		return fmt.Errorf("await connection failed: %w", err)
	}
	if _, err := client.GetDeviceList(ctx); err != nil {
		_ = client.Close()
		return fmt.Errorf("get device list failed: %w", err)
	}
	if err := client.Close(); err != nil {
		return fmt.Errorf("close failed: %w", err)
	}
	return nil
}

func waitForDiscoveryBootstrap(ctx context.Context, wait time.Duration) {
	if wait <= 0 {
		return
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func closeLocalRoutes(routes map[string]miot.MIoTLocalBackend) error {
	var closeErr error
	for groupID, route := range routes {
		if route == nil {
			continue
		}
		if err := route.Close(); err != nil && closeErr == nil {
			closeErr = fmt.Errorf("close local route %s: %w", groupID, err)
		}
	}
	return closeErr
}

func ensureRuntimeDID(existing string) (string, error) {
	if trimmed := strings.TrimSpace(existing); trimmed != "" {
		return trimmed, nil
	}

	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	id := binary.BigEndian.Uint64(buf[:])
	if id == 0 {
		id = 1
	}
	return fmt.Sprintf("%d", id), nil
}

func ensureCloudMIPSUUID(existing string) (string, error) {
	if trimmed := strings.TrimSpace(existing); trimmed != "" {
		return trimmed, nil
	}

	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "go-mihome-runtime-" + hex.EncodeToString(buf[:]), nil
}

func appendBootstrapWarning(warnings []string, diag *runtimeLogger, stage string, err error) []string {
	if err == nil {
		return warnings
	}
	return appendBootstrapWarningMessage(warnings, diag, stage, err.Error())
}

func appendBootstrapWarningMessage(warnings []string, diag *runtimeLogger, stage, message string) []string {
	if diag != nil {
		diag.Warnf(stage, "%s", message)
	}
	return append(warnings, stage+": "+message)
}

func nowTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
