package miot

import (
	"context"
	"io/fs"
	"net/http"
	"time"
)

// Subscription represents a cancelable subscription handle.
type Subscription interface {
	Close() error
}

type subscriptionFunc func() error

func (fn subscriptionFunc) Close() error {
	if fn == nil {
		return nil
	}
	return fn()
}

// HTTPDoer abstracts HTTP request execution.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Clock abstracts time for expirations and tests.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	NewTicker(d time.Duration) Ticker
	NewTimer(d time.Duration) Timer
}

// Ticker abstracts a time.Ticker for testable timing behavior.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Timer abstracts a cancelable single-shot timer for testable delayed work.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// FileStore abstracts filesystem operations for storage-backed components.
type FileStore interface {
	MkdirAll(path string, perm fs.FileMode) error
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	Remove(name string) error
	RemoveAll(path string) error
	ReadDir(name string) ([]fs.DirEntry, error)
	Stat(name string) (fs.FileInfo, error)
}

// RootPathProvider exposes the backing root path for raw file consumers.
type RootPathProvider interface {
	RootPath() string
}

// RawFileBackend exposes a root path plus raw file operations.
type RawFileBackend interface {
	RootPathProvider
	RawFiles() FileStore
}

// ByteStore persists hashed byte payloads keyed by domain and name.
type ByteStore interface {
	SaveBytes(ctx context.Context, domain, name string, data []byte) error
	LoadBytes(ctx context.Context, domain, name string) ([]byte, error)
}

// ReachabilityTargetKind identifies the transport used for a reachability probe.
type ReachabilityTargetKind string

const (
	// ReachabilityTargetIP probes a raw IP address.
	ReachabilityTargetIP ReachabilityTargetKind = "ip"
	// ReachabilityTargetURL probes an HTTP(S) URL.
	ReachabilityTargetURL ReachabilityTargetKind = "url"
)

// ReachabilityTarget describes one network reachability probe target.
type ReachabilityTarget struct {
	Kind    ReachabilityTargetKind
	Address string
}

// ReachabilityResult reports the outcome of one network probe.
type ReachabilityResult struct {
	Kind      ReachabilityTargetKind
	Address   string
	Reachable bool
	Latency   time.Duration
}

// ReachabilityChecker checks one or more probe targets.
type ReachabilityChecker interface {
	Check(ctx context.Context, targets []ReachabilityTarget, timeout time.Duration) ([]ReachabilityResult, error)
}

// NetworkInspector enumerates current local interfaces.
type NetworkInspector interface {
	Interfaces(ctx context.Context) ([]NetworkInfo, error)
}

// ServiceEventType identifies the type of discovery lifecycle event.
type ServiceEventType string

const (
	// ServiceEventAdded reports a newly discovered service.
	ServiceEventAdded ServiceEventType = "added"
	// ServiceEventUpdated reports a changed discovered service.
	ServiceEventUpdated ServiceEventType = "updated"
	// ServiceEventRemoved reports a removed discovered service.
	ServiceEventRemoved ServiceEventType = "removed"
)

// ServiceRecord is the typed input emitted by a discovery backend.
type ServiceRecord struct {
	Name      string
	Type      string
	Server    string
	Addresses []string
	Port      int
	Profile   string
}

// ServiceEvent is one typed discovery callback payload.
type ServiceEvent struct {
	Type   ServiceEventType
	Record ServiceRecord
}

// ServiceDiscovery browses one service type and forwards typed service events.
type ServiceDiscovery interface {
	Browse(ctx context.Context, serviceType string, handler func(ServiceEvent)) (Subscription, error)
}

// MQTTMessageHandler handles one MQTT topic payload pair.
type MQTTMessageHandler func(topic string, payload []byte)

// MQTTConn abstracts the MQTT transport used by MIPS clients.
type MQTTConn interface {
	Connect(ctx context.Context) error
	Close() error
	Publish(ctx context.Context, topic string, payload []byte) error
	Subscribe(ctx context.Context, topic string, handler MQTTMessageHandler) (Subscription, error)
}

// MQTTCredentialUpdater optionally updates live MQTT credentials for transports that support it.
type MQTTCredentialUpdater interface {
	UpdateCredentials(ctx context.Context, username, password string) error
}

// EntityBackend executes property and action operations for generic entities.
type EntityBackend interface {
	DeviceOnline(ctx context.Context, did string) (bool, error)
	GetProperty(ctx context.Context, query PropertyQuery) (PropertyResult, error)
	SetProperty(ctx context.Context, req SetPropertyRequest) (SetPropertyResult, error)
	SubscribeProperty(ctx context.Context, req PropertySubscription, fn PropertyEventHandler) (Subscription, error)
	SubscribeEvent(ctx context.Context, req EventSubscription, fn EventHandler) (Subscription, error)
	SubscribeDeviceState(ctx context.Context, did string, fn DeviceStateHandler) (Subscription, error)
	InvokeAction(ctx context.Context, req ActionRequest) (ActionResult, error)
}
