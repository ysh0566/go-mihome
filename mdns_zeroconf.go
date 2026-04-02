package miot

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/zeroconf/v2"
)

const zeroconfSweepInterval = time.Second

type zeroconfRecordState struct {
	record ServiceRecord
	expiry time.Time
}

// ZeroconfServiceDiscovery is the default ServiceDiscovery backend backed by libp2p/zeroconf.
type ZeroconfServiceDiscovery struct{}

// NewZeroconfServiceDiscovery creates the default mDNS discovery backend.
func NewZeroconfServiceDiscovery() *ZeroconfServiceDiscovery {
	return &ZeroconfServiceDiscovery{}
}

// Browse streams typed service events from mDNS browse results.
func (d *ZeroconfServiceDiscovery) Browse(ctx context.Context, serviceType string, handler func(ServiceEvent)) (Subscription, error) {
	if handler == nil {
		return subscriptionFunc(nil), nil
	}
	service, domain := splitBrowseServiceType(serviceType)
	browseCtx, cancel := context.WithCancel(ctx)
	entries := make(chan *zeroconf.ServiceEntry)

	var mu sync.Mutex
	known := make(map[string]zeroconfRecordState)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(zeroconfSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-browseCtx.Done():
				return
			case entry, ok := <-entries:
				if !ok {
					return
				}
				record, err := zeroconfServiceRecord(entry)
				if err != nil {
					continue
				}
				key := record.Name

				mu.Lock()
				prev, existed := known[key]
				known[key] = zeroconfRecordState{record: record, expiry: entry.Expiry}
				mu.Unlock()

				eventType := ServiceEventAdded
				if existed {
					if serviceRecordEqual(prev.record, record) && prev.expiry.Equal(entry.Expiry) {
						continue
					}
					eventType = ServiceEventUpdated
				}
				handler(ServiceEvent{Type: eventType, Record: record})
			case <-ticker.C:
				now := time.Now()
				var expired []ServiceRecord

				mu.Lock()
				for key, state := range known {
					if state.expiry.IsZero() || state.expiry.After(now) {
						continue
					}
					expired = append(expired, state.record)
					delete(known, key)
				}
				mu.Unlock()

				for _, record := range expired {
					handler(ServiceEvent{Type: ServiceEventRemoved, Record: record})
				}
			}
		}
	}()

	go func() {
		_ = zeroconf.Browse(browseCtx, service, domain, entries)
	}()

	return subscriptionFunc(func() error {
		cancel()
		<-done
		return nil
	}), nil
}

func splitBrowseServiceType(serviceType string) (string, string) {
	trimmed := strings.TrimSuffix(serviceType, ".")
	if strings.HasSuffix(trimmed, ".local") {
		return strings.TrimSuffix(trimmed, ".local"), "local."
	}
	return trimmed, "local."
}

func zeroconfServiceRecord(entry *zeroconf.ServiceEntry) (ServiceRecord, error) {
	if entry == nil {
		return ServiceRecord{}, &Error{Code: ErrInvalidArgument, Op: "zeroconf service record", Msg: "entry is nil"}
	}
	profile := ""
	for _, item := range entry.Text {
		if value, ok := strings.CutPrefix(item, "profile="); ok {
			profile = value
			break
		}
	}
	if profile == "" {
		return ServiceRecord{}, &Error{Code: ErrInvalidResponse, Op: "zeroconf service record", Msg: "missing profile txt"}
	}

	addresses := make([]string, 0, len(entry.AddrIPv4)+len(entry.AddrIPv6))
	for _, ip := range entry.AddrIPv4 {
		if ip != nil {
			addresses = append(addresses, ip.String())
		}
	}
	for _, ip := range entry.AddrIPv6 {
		if ip != nil {
			addresses = append(addresses, ip.String())
		}
	}

	return ServiceRecord{
		Name:      entry.ServiceInstanceName(),
		Type:      entry.ServiceName(),
		Server:    entry.HostName,
		Addresses: addresses,
		Port:      entry.Port,
		Profile:   profile,
	}, nil
}

func serviceRecordEqual(a, b ServiceRecord) bool {
	if a.Name != b.Name || a.Type != b.Type || a.Server != b.Server || a.Port != b.Port || a.Profile != b.Profile {
		return false
	}
	if len(a.Addresses) != len(b.Addresses) {
		return false
	}
	for i := range a.Addresses {
		if a.Addresses[i] != b.Addresses[i] {
			return false
		}
	}
	return true
}
