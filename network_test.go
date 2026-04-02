package miot

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestNetworkMonitorDetectsInterfaceChanges(t *testing.T) {
	inspector := &stubNetworkInspector{
		snapshots: [][]NetworkInfo{
			{
				{Name: "en0", IP: "192.168.1.2", Netmask: "255.255.255.0", NetworkSegment: "192.168.1.0"},
			},
			{
				{Name: "en0", IP: "192.168.1.3", Netmask: "255.255.255.0", NetworkSegment: "192.168.1.0"},
				{Name: "en1", IP: "10.0.0.2", Netmask: "255.255.255.0", NetworkSegment: "10.0.0.0"},
			},
			{
				{Name: "en1", IP: "10.0.0.2", Netmask: "255.255.255.0", NetworkSegment: "10.0.0.0"},
			},
		},
	}
	checker := &stubReachabilityChecker{
		results: [][]ReachabilityResult{
			{{Kind: ReachabilityTargetIP, Address: "8.8.8.8", Reachable: true, Latency: 20 * time.Millisecond}},
			{{Kind: ReachabilityTargetIP, Address: "8.8.8.8", Reachable: true, Latency: 15 * time.Millisecond}},
			{{Kind: ReachabilityTargetIP, Address: "8.8.8.8", Reachable: false}},
		},
	}
	monitor := NewNetworkMonitor(inspector, checker)
	monitor.UpdateTargets([]string{"8.8.8.8"}, nil)

	var statusEvents []bool
	statusSub := monitor.SubscribeStatus(func(online bool) {
		statusEvents = append(statusEvents, online)
	})
	defer func() {
		if err := statusSub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	var infoEvents []string
	infoSub := monitor.SubscribeInterfaces(func(status InterfaceStatus, info NetworkInfo) {
		infoEvents = append(infoEvents, fmt.Sprintf("%s:%s", status, info.Name))
	})
	defer func() {
		if err := infoSub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	for i := 0; i < 3; i++ {
		if err := monitor.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	if got := monitor.Status(); got {
		t.Fatalf("status = %v, want false", got)
	}
	if got := monitor.Interfaces(); len(got) != 1 || got[0].Name != "en1" {
		t.Fatalf("interfaces = %#v", got)
	}
	if !reflect.DeepEqual(statusEvents, []bool{true, false}) {
		t.Fatalf("status events = %#v", statusEvents)
	}
	wantInfo := []string{"add:en0", "update:en0", "add:en1", "remove:en0"}
	if !reflect.DeepEqual(infoEvents, wantInfo) {
		t.Fatalf("info events = %#v, want %#v", infoEvents, wantInfo)
	}
}

type stubNetworkInspector struct {
	snapshots [][]NetworkInfo
	index     int
}

func (s *stubNetworkInspector) Interfaces(context.Context) ([]NetworkInfo, error) {
	if s.index >= len(s.snapshots) {
		return nil, nil
	}
	items := append([]NetworkInfo(nil), s.snapshots[s.index]...)
	s.index++
	return items, nil
}

type stubReachabilityChecker struct {
	results [][]ReachabilityResult
	index   int
}

func (s *stubReachabilityChecker) Check(context.Context, []ReachabilityTarget, time.Duration) ([]ReachabilityResult, error) {
	if s.index >= len(s.results) {
		return nil, nil
	}
	items := append([]ReachabilityResult(nil), s.results[s.index]...)
	s.index++
	return items, nil
}
