package miot

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"slices"
	"strconv"
	"sync"
)

// MIPSMDNSType is the MIoT central gateway service type advertised over mDNS.
const MIPSMDNSType = "_miot-central._tcp.local."

// MIPSServiceState identifies the lifecycle state of one discovered MIPS service.
type MIPSServiceState string

const (
	// MIPSServiceStateAdded reports a newly discovered service.
	MIPSServiceStateAdded MIPSServiceState = "added"
	// MIPSServiceStateUpdated reports a changed service.
	MIPSServiceStateUpdated MIPSServiceState = "updated"
	// MIPSServiceStateRemoved reports a removed service.
	MIPSServiceStateRemoved MIPSServiceState = "removed"
)

// MIPSServiceInfo is the typed representation of one MIoT central gateway record.
type MIPSServiceInfo struct {
	Profile      string
	Name         string
	Addresses    []string
	Port         int
	Type         string
	Server       string
	DID          string
	GroupID      string
	Role         int
	SupportsMQTT bool
}

// Valid reports whether the service is a primary gateway with MQTT support.
func (s MIPSServiceInfo) Valid() bool {
	return s.Role == 1 && s.SupportsMQTT
}

// MIPSServiceEvent describes one typed discovery update.
type MIPSServiceEvent struct {
	GroupID string
	State   MIPSServiceState
	Service MIPSServiceInfo
}

// MIPSDiscovery tracks current MIoT central gateway services from mDNS.
type MIPSDiscovery struct {
	sd ServiceDiscovery

	mu       sync.RWMutex
	services map[string]MIPSServiceInfo
	subs     map[int]mipsServiceSubscriber
	nextID   int
	browse   Subscription
}

type mipsServiceSubscriber struct {
	groupID string
	handler func(MIPSServiceEvent)
}

// NewMIPSDiscovery creates a new discovery tracker around a typed discovery backend.
func NewMIPSDiscovery(sd ServiceDiscovery) *MIPSDiscovery {
	if sd == nil {
		sd = NewZeroconfServiceDiscovery()
	}
	return &MIPSDiscovery{
		sd:       sd,
		services: make(map[string]MIPSServiceInfo),
		subs:     make(map[int]mipsServiceSubscriber),
	}
}

// Start begins browsing MIoT central gateway records.
func (d *MIPSDiscovery) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.browse != nil {
		d.mu.Unlock()
		return nil
	}
	sd := d.sd
	d.mu.Unlock()

	if sd == nil {
		return &Error{Code: ErrInvalidArgument, Op: "mips discovery start", Msg: "service discovery is nil"}
	}

	sub, err := sd.Browse(ctx, MIPSMDNSType, d.handleEvent)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.browse = sub
	return nil
}

// Close stops the underlying discovery browse subscription.
func (d *MIPSDiscovery) Close() error {
	d.mu.Lock()
	sub := d.browse
	d.browse = nil
	d.mu.Unlock()

	if sub != nil {
		return sub.Close()
	}
	return nil
}

// Services returns a deep copy of all currently known services keyed by group ID.
func (d *MIPSDiscovery) Services() map[string]MIPSServiceInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	items := make(map[string]MIPSServiceInfo, len(d.services))
	for key, info := range d.services {
		copyInfo := info
		copyInfo.Addresses = append([]string(nil), info.Addresses...)
		items[key] = copyInfo
	}
	return items
}

// SubscribeServiceChange registers a typed service change callback.
func (d *MIPSDiscovery) SubscribeServiceChange(groupID string, fn func(MIPSServiceEvent)) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	id := d.nextID
	d.nextID++
	d.subs[id] = mipsServiceSubscriber{groupID: groupID, handler: fn}
	return subscriptionFunc(func() error {
		d.mu.Lock()
		defer d.mu.Unlock()
		delete(d.subs, id)
		return nil
	})
}

// ParseMIPSServiceRecord parses one discovery record into a typed MIPS gateway description.
func ParseMIPSServiceRecord(record ServiceRecord) (MIPSServiceInfo, error) {
	if record.Profile == "" {
		return MIPSServiceInfo{}, &Error{Code: ErrInvalidArgument, Op: "parse mips service record", Msg: "profile is empty"}
	}
	if len(record.Addresses) == 0 {
		return MIPSServiceInfo{}, &Error{Code: ErrInvalidArgument, Op: "parse mips service record", Msg: "addresses are empty"}
	}
	if record.Port <= 0 {
		return MIPSServiceInfo{}, &Error{Code: ErrInvalidArgument, Op: "parse mips service record", Msg: "port is invalid"}
	}

	profile, err := base64.StdEncoding.DecodeString(record.Profile)
	if err != nil {
		return MIPSServiceInfo{}, Wrap(ErrInvalidResponse, "decode mips profile", err)
	}
	if len(profile) < 23 {
		return MIPSServiceInfo{}, &Error{Code: ErrInvalidResponse, Op: "parse mips service record", Msg: "profile is too short"}
	}

	group := append([]byte(nil), profile[9:17]...)
	reverseBytes(group)

	return MIPSServiceInfo{
		Profile:      record.Profile,
		Name:         record.Name,
		Addresses:    append([]string(nil), record.Addresses...),
		Port:         record.Port,
		Type:         record.Type,
		Server:       record.Server,
		DID:          strconv.FormatUint(binary.BigEndian.Uint64(profile[1:9]), 10),
		GroupID:      hex.EncodeToString(group),
		Role:         int(profile[20] >> 4),
		SupportsMQTT: ((profile[22] >> 1) & 0x01) == 0x01,
	}, nil
}

func (d *MIPSDiscovery) handleEvent(event ServiceEvent) {
	service, err := ParseMIPSServiceRecord(event.Record)
	if err != nil || !service.Valid() {
		return
	}

	mappedState := MIPSServiceStateAdded
	d.mu.Lock()
	existing, existed := d.services[service.GroupID]
	switch event.Type {
	case ServiceEventRemoved:
		if existed {
			delete(d.services, service.GroupID)
			mappedState = MIPSServiceStateRemoved
		}
	case ServiceEventUpdated:
		d.services[service.GroupID] = service
		mappedState = MIPSServiceStateUpdated
	case ServiceEventAdded:
		if existed && mipsServiceChanged(existing, service) {
			d.services[service.GroupID] = service
			mappedState = MIPSServiceStateUpdated
		} else {
			d.services[service.GroupID] = service
			mappedState = MIPSServiceStateAdded
		}
	default:
		d.services[service.GroupID] = service
		if existed {
			mappedState = MIPSServiceStateUpdated
		}
	}

	subs := make([]mipsServiceSubscriber, 0, len(d.subs))
	for _, sub := range d.subs {
		subs = append(subs, sub)
	}
	d.mu.Unlock()

	if event.Type == ServiceEventRemoved && !existed {
		return
	}

	update := MIPSServiceEvent{
		GroupID: service.GroupID,
		State:   mappedState,
		Service: service,
	}
	for _, sub := range subs {
		if sub.groupID == "" || sub.groupID == service.GroupID {
			sub.handler(update)
		}
	}
}

func reverseBytes(data []byte) {
	for left, right := 0, len(data)-1; left < right; left, right = left+1, right-1 {
		data[left], data[right] = data[right], data[left]
	}
}

func mipsServiceChanged(left, right MIPSServiceInfo) bool {
	return left.Profile != right.Profile ||
		left.Name != right.Name ||
		left.Port != right.Port ||
		left.Type != right.Type ||
		left.Server != right.Server ||
		left.DID != right.DID ||
		left.GroupID != right.GroupID ||
		left.Role != right.Role ||
		left.SupportsMQTT != right.SupportsMQTT ||
		!slices.Equal(left.Addresses, right.Addresses)
}
