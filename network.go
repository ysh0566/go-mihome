package miot

import (
	"context"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultNetworkRefreshInterval = 30 * time.Second
	defaultReachabilityTimeout    = 6 * time.Second
)

var (
	defaultReachabilityIPs  = []string{"1.2.4.8", "8.8.8.8", "9.9.9.9"}
	defaultReachabilityURLs = []string{
		"https://www.bing.com",
		"https://www.google.com",
		"https://www.baidu.com",
	}
)

// InterfaceStatus reports how one network interface changed.
type InterfaceStatus string

const (
	// InterfaceStatusAdd reports a newly seen interface.
	InterfaceStatusAdd InterfaceStatus = "add"
	// InterfaceStatusUpdate reports an existing interface with changed addressing.
	InterfaceStatusUpdate InterfaceStatus = "update"
	// InterfaceStatusRemove reports a removed interface.
	InterfaceStatusRemove InterfaceStatus = "remove"
)

// NetworkInfo describes one local IPv4 interface.
type NetworkInfo struct {
	Name           string
	IP             string
	Netmask        string
	NetworkSegment string
}

// NetworkMonitorOption customizes a NetworkMonitor.
type NetworkMonitorOption func(*NetworkMonitor)

// WithNetworkClock overrides the clock used by the monitor loop.
func WithNetworkClock(clock Clock) NetworkMonitorOption {
	return func(m *NetworkMonitor) {
		if clock != nil {
			m.clock = clock
		}
	}
}

// WithNetworkRefreshInterval overrides the background refresh interval.
func WithNetworkRefreshInterval(interval time.Duration) NetworkMonitorOption {
	return func(m *NetworkMonitor) {
		if interval > 0 {
			m.refreshInterval = interval
		}
	}
}

// WithNetworkTimeout overrides the reachability timeout per probe.
func WithNetworkTimeout(timeout time.Duration) NetworkMonitorOption {
	return func(m *NetworkMonitor) {
		if timeout > 0 {
			m.timeout = timeout
		}
	}
}

// NetworkMonitor tracks current interface state and internet reachability.
type NetworkMonitor struct {
	inspector       NetworkInspector
	checker         ReachabilityChecker
	clock           Clock
	refreshInterval time.Duration
	timeout         time.Duration

	mu         sync.RWMutex
	status     bool
	interfaces map[string]NetworkInfo
	ipTargets  []string
	urlTargets []string

	statusSubs map[int]func(bool)
	infoSubs   map[int]func(InterfaceStatus, NetworkInfo)
	nextSubID  int

	cancel context.CancelFunc
	done   chan struct{}
	ticker Ticker
}

// NewNetworkMonitor creates a new monitor with injected network dependencies.
func NewNetworkMonitor(inspector NetworkInspector, checker ReachabilityChecker, opts ...NetworkMonitorOption) *NetworkMonitor {
	if inspector == nil {
		inspector = systemNetworkInspector{}
	}
	if checker == nil {
		checker = defaultReachabilityChecker{client: &http.Client{}}
	}

	monitor := &NetworkMonitor{
		inspector:       inspector,
		checker:         checker,
		clock:           realClock{},
		refreshInterval: defaultNetworkRefreshInterval,
		timeout:         defaultReachabilityTimeout,
		interfaces:      make(map[string]NetworkInfo),
		ipTargets:       append([]string(nil), defaultReachabilityIPs...),
		urlTargets:      append([]string(nil), defaultReachabilityURLs...),
		statusSubs:      make(map[int]func(bool)),
		infoSubs:        make(map[int]func(InterfaceStatus, NetworkInfo)),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(monitor)
		}
	}
	return monitor
}

// Start begins periodic refreshes until the context is canceled or Close is called.
func (m *NetworkMonitor) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	ticker := m.clock.NewTicker(m.refreshInterval)
	done := make(chan struct{})
	m.cancel = cancel
	m.ticker = ticker
	m.done = done
	m.mu.Unlock()

	go func() {
		defer close(done)
		defer ticker.Stop()
		_ = m.Refresh(runCtx)
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C():
				_ = m.Refresh(runCtx)
			}
		}
	}()
	return nil
}

// Close stops the background refresh loop.
func (m *NetworkMonitor) Close() error {
	m.mu.Lock()
	cancel := m.cancel
	done := m.done
	m.cancel = nil
	m.done = nil
	m.ticker = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return nil
}

// UpdateTargets replaces the IP and URL targets used for reachability checks.
func (m *NetworkMonitor) UpdateTargets(ipTargets, urlTargets []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ipTargets == nil {
		m.ipTargets = append([]string(nil), defaultReachabilityIPs...)
	} else {
		m.ipTargets = append([]string(nil), ipTargets...)
	}
	if urlTargets == nil {
		m.urlTargets = append([]string(nil), defaultReachabilityURLs...)
	} else {
		m.urlTargets = append([]string(nil), urlTargets...)
	}
}

// Refresh performs one synchronous network status and interface refresh.
func (m *NetworkMonitor) Refresh(ctx context.Context) error {
	targets := m.targets()
	results, err := m.checker.Check(ctx, targets, m.timeout)
	if err != nil {
		return err
	}
	infos, err := m.inspector.Interfaces(ctx)
	if err != nil {
		return err
	}

	nextStatus := false
	for _, result := range results {
		if result.Reachable {
			nextStatus = true
			break
		}
	}

	nextInterfaces := make(map[string]NetworkInfo, len(infos))
	for _, info := range infos {
		nextInterfaces[info.Name] = info
	}

	var statusSubs []func(bool)
	var infoSubs []func(InterfaceStatus, NetworkInfo)
	var statusChanged bool
	type interfaceEvent struct {
		status InterfaceStatus
		info   NetworkInfo
	}
	var events []interfaceEvent

	m.mu.Lock()
	if m.status != nextStatus {
		m.status = nextStatus
		statusChanged = true
		statusSubs = make([]func(bool), 0, len(m.statusSubs))
		for _, fn := range m.statusSubs {
			statusSubs = append(statusSubs, fn)
		}
	}

	infoSubs = make([]func(InterfaceStatus, NetworkInfo), 0, len(m.infoSubs))
	for _, fn := range m.infoSubs {
		infoSubs = append(infoSubs, fn)
	}

	for _, info := range infos {
		prev, ok := m.interfaces[info.Name]
		if !ok {
			events = append(events, interfaceEvent{status: InterfaceStatusAdd, info: info})
		} else if prev != info {
			events = append(events, interfaceEvent{status: InterfaceStatusUpdate, info: info})
		}
	}
	for name, prev := range m.interfaces {
		if _, ok := nextInterfaces[name]; !ok {
			events = append(events, interfaceEvent{status: InterfaceStatusRemove, info: prev})
		}
	}
	m.interfaces = nextInterfaces
	m.mu.Unlock()

	if statusChanged {
		for _, fn := range statusSubs {
			fn(nextStatus)
		}
	}
	for _, event := range events {
		for _, fn := range infoSubs {
			fn(event.status, event.info)
		}
	}
	return nil
}

// Status returns the last known reachability state.
func (m *NetworkMonitor) Status() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// Interfaces returns a stable copy of the current interfaces.
func (m *NetworkMonitor) Interfaces() []NetworkInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	items := make([]NetworkInfo, 0, len(m.interfaces))
	for _, info := range m.interfaces {
		items = append(items, info)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items
}

// SubscribeStatus registers a callback for reachability state changes.
func (m *NetworkMonitor) SubscribeStatus(fn func(bool)) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextSubID
	m.nextSubID++
	m.statusSubs[id] = fn
	return subscriptionFunc(func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.statusSubs, id)
		return nil
	})
}

// SubscribeInterfaces registers a callback for interface add, update, and remove events.
func (m *NetworkMonitor) SubscribeInterfaces(fn func(InterfaceStatus, NetworkInfo)) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextSubID
	m.nextSubID++
	m.infoSubs[id] = fn
	return subscriptionFunc(func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.infoSubs, id)
		return nil
	})
}

func (m *NetworkMonitor) targets() []ReachabilityTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()

	targets := make([]ReachabilityTarget, 0, len(m.ipTargets)+len(m.urlTargets))
	for _, ip := range m.ipTargets {
		targets = append(targets, ReachabilityTarget{Kind: ReachabilityTargetIP, Address: ip})
	}
	for _, url := range m.urlTargets {
		targets = append(targets, ReachabilityTarget{Kind: ReachabilityTargetURL, Address: url})
	}
	return targets
}

type systemNetworkInspector struct{}

func (systemNetworkInspector) Interfaces(context.Context) ([]NetworkInfo, error) {
	rawInterfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	results := make([]NetworkInfo, 0, len(rawInterfaces))
	for _, iface := range rawInterfaces {
		if iface.Name == "hassio" || strings.HasPrefix(iface.Name, "docker") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP == nil || ipNet.Mask == nil {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			mask := ipMaskString(ipNet.Mask)
			segment := ip.Mask(ipNet.Mask).String()
			results = append(results, NetworkInfo{
				Name:           iface.Name,
				IP:             ip.String(),
				Netmask:        mask,
				NetworkSegment: segment,
			})
			break
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results, nil
}

type defaultReachabilityChecker struct {
	client *http.Client
}

func (c defaultReachabilityChecker) Check(ctx context.Context, targets []ReachabilityTarget, timeout time.Duration) ([]ReachabilityResult, error) {
	results := make([]ReachabilityResult, 0, len(targets))
	for _, target := range targets {
		start := time.Now()
		reachable := false

		switch target.Kind {
		case ReachabilityTargetIP:
			dialer := net.Dialer{Timeout: timeout}
			conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.Address, "53"))
			if err == nil {
				reachable = true
				_ = conn.Close()
			}
		case ReachabilityTargetURL:
			client := c.client
			if client == nil {
				client = &http.Client{}
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.Address, nil)
			if err == nil {
				resp, err := client.Do(req)
				if err == nil {
					reachable = true
					_ = resp.Body.Close()
				}
			}
		}

		result := ReachabilityResult{
			Kind:      target.Kind,
			Address:   target.Address,
			Reachable: reachable,
		}
		if reachable {
			result.Latency = time.Since(start)
		}
		results = append(results, result)
	}
	return results, nil
}

func ipMaskString(mask net.IPMask) string {
	if mask == nil {
		return ""
	}
	parts := make([]string, 0, len(mask))
	for _, part := range mask {
		parts = append(parts, strconv.Itoa(int(part)))
	}
	return strings.Join(parts, ".")
}
