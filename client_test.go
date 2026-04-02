package miot

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestNewMIoTClientRequiresUIDAndCloudServer(t *testing.T) {
	_, err := NewMIoTClient(MIoTClientConfig{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestMIoTClientGetPropertyUsesCloudFirst(t *testing.T) {
	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {
					DID:         "dev1",
					Name:        "Lamp",
					Model:       "yeelink.light",
					Token:       "00112233445566778899aabbccddeeff",
					ConnectType: 0,
					Online:      true,
				},
			},
		},
		propResult: PropertyResult{
			DID:   "dev1",
			SIID:  2,
			PIID:  1,
			Value: NewSpecValueBool(true),
			Code:  0,
		},
	}
	cloudPush := newStubMIoTCloudPushBackend()
	local := newStubMIoTLocalBackend("group-1")
	local.deviceList = []LocalDeviceSummary{{
		DID:           "dev1",
		Online:        true,
		SpecV2Access:  true,
		PushAvailable: true,
	}}
	lan := newStubMIoTLANBackend()
	lan.deviceList = []LANDeviceSummary{{
		DID:           "dev1",
		Online:        true,
		PushAvailable: true,
	}}

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		ControlMode: MIoTControlModeAuto,
		Homes: []MIoTClientHome{{
			HomeID:   "home-1",
			HomeName: "Home",
			GroupID:  "group-1",
		}},
		Cloud:     cloud,
		CloudPush: cloudPush,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		LAN: lan,
	})

	if err := client.RefreshCloudDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.RefreshGatewayDevices(context.Background(), "group-1"); err != nil {
		t.Fatal(err)
	}
	if err := client.RefreshLANDevices(); err != nil {
		t.Fatal(err)
	}

	got, err := client.GetProperty(context.Background(), PropertyQuery{
		DID:  "dev1",
		SIID: 2,
		PIID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DID != "dev1" {
		t.Fatalf("did = %q", got.DID)
	}
	if cloud.getPropCalls != 1 {
		t.Fatalf("cloud get calls = %d", cloud.getPropCalls)
	}
	if local.getPropCalls != 0 {
		t.Fatalf("local get calls = %d", local.getPropCalls)
	}
	if lan.getPropCalls != 0 {
		t.Fatalf("lan get calls = %d", lan.getPropCalls)
	}
}

func TestMIoTClientSetPropertyPrefersGatewayThenLANThenCloud(t *testing.T) {
	t.Run("gateway first", func(t *testing.T) {
		client, _, cloud, local, lan := newRoutingTestClient(t)
		local.deviceList = []LocalDeviceSummary{{
			DID:           "dev1",
			Online:        true,
			SpecV2Access:  true,
			PushAvailable: true,
		}}
		if err := client.RefreshGatewayDevices(context.Background(), "group-1"); err != nil {
			t.Fatal(err)
		}

		got, err := client.SetProperty(context.Background(), SetPropertyRequest{
			DID:   "dev1",
			SIID:  2,
			PIID:  1,
			Value: NewSpecValueBool(true),
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Code != 0 {
			t.Fatalf("code = %d", got.Code)
		}
		if local.setPropCalls != 1 {
			t.Fatalf("local set calls = %d", local.setPropCalls)
		}
		if lan.setPropCalls != 0 {
			t.Fatalf("lan set calls = %d", lan.setPropCalls)
		}
		if cloud.setPropsCalls != 0 {
			t.Fatalf("cloud set calls = %d", cloud.setPropsCalls)
		}
	})

	t.Run("lan fallback", func(t *testing.T) {
		client, _, cloud, local, lan := newRoutingTestClient(t)
		lan.deviceList = []LANDeviceSummary{{
			DID:           "dev1",
			Online:        true,
			PushAvailable: true,
		}}
		if err := client.RefreshLANDevices(); err != nil {
			t.Fatal(err)
		}

		_, err := client.SetProperty(context.Background(), SetPropertyRequest{
			DID:   "dev1",
			SIID:  2,
			PIID:  1,
			Value: NewSpecValueBool(true),
		})
		if err != nil {
			t.Fatal(err)
		}
		if local.setPropCalls != 0 {
			t.Fatalf("local set calls = %d", local.setPropCalls)
		}
		if lan.setPropCalls != 1 {
			t.Fatalf("lan set calls = %d", lan.setPropCalls)
		}
		if cloud.setPropsCalls != 0 {
			t.Fatalf("cloud set calls = %d", cloud.setPropsCalls)
		}
	})

	t.Run("cloud fallback", func(t *testing.T) {
		client, _, cloud, local, lan := newRoutingTestClient(t)

		_, err := client.SetProperty(context.Background(), SetPropertyRequest{
			DID:   "dev1",
			SIID:  2,
			PIID:  1,
			Value: NewSpecValueBool(true),
		})
		if err != nil {
			t.Fatal(err)
		}
		if local.setPropCalls != 0 {
			t.Fatalf("local set calls = %d", local.setPropCalls)
		}
		if lan.setPropCalls != 0 {
			t.Fatalf("lan set calls = %d", lan.setPropCalls)
		}
		if cloud.setPropsCalls != 1 {
			t.Fatalf("cloud set calls = %d", cloud.setPropsCalls)
		}
	})
}

func TestMIoTClientInvokeActionPrefersGatewayThenLANThenCloud(t *testing.T) {
	t.Run("gateway first", func(t *testing.T) {
		client, _, cloud, local, lan := newRoutingTestClient(t)
		local.deviceList = []LocalDeviceSummary{{
			DID:           "dev1",
			Online:        true,
			SpecV2Access:  true,
			PushAvailable: true,
		}}
		if err := client.RefreshGatewayDevices(context.Background(), "group-1"); err != nil {
			t.Fatal(err)
		}

		got, err := client.InvokeAction(context.Background(), ActionRequest{
			DID:   "dev1",
			SIID:  2,
			AIID:  1,
			Input: []SpecValue{NewSpecValueBool(true)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Code != 0 {
			t.Fatalf("code = %d", got.Code)
		}
		if local.actionCalls != 1 {
			t.Fatalf("local action calls = %d", local.actionCalls)
		}
		if lan.actionCalls != 0 {
			t.Fatalf("lan action calls = %d", lan.actionCalls)
		}
		if cloud.invokeActionCalls != 0 {
			t.Fatalf("cloud action calls = %d", cloud.invokeActionCalls)
		}
	})

	t.Run("lan fallback", func(t *testing.T) {
		client, _, cloud, local, lan := newRoutingTestClient(t)
		lan.deviceList = []LANDeviceSummary{{
			DID:           "dev1",
			Online:        true,
			PushAvailable: true,
		}}
		if err := client.RefreshLANDevices(); err != nil {
			t.Fatal(err)
		}

		_, err := client.InvokeAction(context.Background(), ActionRequest{
			DID:   "dev1",
			SIID:  2,
			AIID:  1,
			Input: []SpecValue{NewSpecValueBool(true)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if local.actionCalls != 0 {
			t.Fatalf("local action calls = %d", local.actionCalls)
		}
		if lan.actionCalls != 1 {
			t.Fatalf("lan action calls = %d", lan.actionCalls)
		}
		if cloud.invokeActionCalls != 0 {
			t.Fatalf("cloud action calls = %d", cloud.invokeActionCalls)
		}
	})

	t.Run("cloud fallback", func(t *testing.T) {
		client, _, cloud, local, lan := newRoutingTestClient(t)

		_, err := client.InvokeAction(context.Background(), ActionRequest{
			DID:   "dev1",
			SIID:  2,
			AIID:  1,
			Input: []SpecValue{NewSpecValueBool(true)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if local.actionCalls != 0 {
			t.Fatalf("local action calls = %d", local.actionCalls)
		}
		if lan.actionCalls != 0 {
			t.Fatalf("lan action calls = %d", lan.actionCalls)
		}
		if cloud.invokeActionCalls != 1 {
			t.Fatalf("cloud action calls = %d", cloud.invokeActionCalls)
		}
	})
}

func TestMIoTClientRefreshCloudDevicesMergesCache(t *testing.T) {
	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {
					DID:         "dev1",
					Name:        "Desk Lamp",
					Model:       "yeelink.light",
					HomeID:      "home-1",
					HomeName:    "Home",
					RoomID:      "room-1",
					RoomName:    "Demo Room",
					GroupID:     "group-1",
					Token:       "00112233445566778899aabbccddeeff",
					ConnectType: 0,
					Online:      true,
				},
			},
		},
	}
	lan := newStubMIoTLANBackend()
	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Homes: []MIoTClientHome{{
			HomeID:   "home-1",
			HomeName: "Home",
			GroupID:  "group-1",
		}},
		Cloud: cloud,
		LAN:   lan,
	})

	if err := client.RefreshCloudDevices(context.Background()); err != nil {
		t.Fatal(err)
	}

	devices := client.Devices()
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d", len(devices))
	}
	got := devices["dev1"]
	if got.Info.HomeName != "Home" || got.Info.RoomName != "Demo Room" {
		t.Fatalf("device = %#v", got)
	}
	if got.State != DeviceStateOnline {
		t.Fatalf("state = %q", got.State)
	}
	if len(lan.updateDevicesCalls) != 1 {
		t.Fatalf("lan update calls = %d", len(lan.updateDevicesCalls))
	}
	if len(lan.updateDevicesCalls[0]) != 1 || lan.updateDevicesCalls[0][0].DID != "dev1" {
		t.Fatalf("lan update payload = %#v", lan.updateDevicesCalls[0])
	}
}

func TestMIoTClientAggregateDeviceStateTracksCloudGatewayAndLAN(t *testing.T) {
	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {
					DID:    "dev1",
					Name:   "Lamp",
					Model:  "yeelink.light",
					Online: false,
				},
			},
		},
	}
	cloudPush := newStubMIoTCloudPushBackend()
	local := newStubMIoTLocalBackend("group-1")
	lan := newStubMIoTLANBackend()
	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		ControlMode: MIoTControlModeAuto,
		Homes: []MIoTClientHome{{
			HomeID:   "home-1",
			HomeName: "Home",
			GroupID:  "group-1",
		}},
		Cloud:     cloud,
		CloudPush: cloudPush,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		LAN: lan,
	})

	if err := client.RefreshCloudDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state := client.Devices()["dev1"].State; state != DeviceStateOffline {
		t.Fatalf("initial state = %q", state)
	}

	local.deviceList = []LocalDeviceSummary{{
		DID:           "dev1",
		Online:        true,
		SpecV2Access:  true,
		PushAvailable: true,
	}}
	if err := client.RefreshGatewayDevices(context.Background(), "group-1"); err != nil {
		t.Fatal(err)
	}
	if state := client.Devices()["dev1"].State; state != DeviceStateOnline {
		t.Fatalf("gateway state = %q", state)
	}

	local.deviceList = nil
	if err := client.RefreshGatewayDevices(context.Background(), "group-1"); err != nil {
		t.Fatal(err)
	}
	if state := client.Devices()["dev1"].State; state != DeviceStateOffline {
		t.Fatalf("post-gateway state = %q", state)
	}

	lan.deviceList = []LANDeviceSummary{{
		DID:           "dev1",
		Online:        true,
		PushAvailable: true,
	}}
	if err := client.RefreshLANDevices(); err != nil {
		t.Fatal(err)
	}
	if state := client.Devices()["dev1"].State; state != DeviceStateOnline {
		t.Fatalf("lan state = %q", state)
	}

	lan.deviceList = nil
	if err := client.RefreshLANDevices(); err != nil {
		t.Fatal(err)
	}
	if state := client.Devices()["dev1"].State; state != DeviceStateOffline {
		t.Fatalf("final state = %q", state)
	}
}

func TestMIoTClientSubscriptionSourceSwitchesByAvailability(t *testing.T) {
	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {DID: "dev1", Online: true},
			},
		},
	}
	cloudPush := newStubMIoTCloudPushBackend()
	local := newStubMIoTLocalBackend("group-1")
	lan := newStubMIoTLANBackend()
	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		ControlMode: MIoTControlModeAuto,
		Homes: []MIoTClientHome{{
			HomeID:   "home-1",
			HomeName: "Home",
			GroupID:  "group-1",
		}},
		Cloud:     cloud,
		CloudPush: cloudPush,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		LAN: lan,
	})

	if err := client.RefreshCloudDevices(context.Background()); err != nil {
		t.Fatal(err)
	}

	sub, err := client.SubscribeProperty(context.Background(), PropertySubscription{
		DID:  "dev1",
		SIID: 2,
		PIID: 1,
	}, func(PropertyResult) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	if cloudPush.propertySubCount("dev1|2|1") != 1 {
		t.Fatalf("cloud property subs = %d", cloudPush.propertySubCount("dev1|2|1"))
	}

	local.deviceList = []LocalDeviceSummary{{
		DID:           "dev1",
		Online:        true,
		SpecV2Access:  true,
		PushAvailable: true,
	}}
	if err := client.RefreshGatewayDevices(context.Background(), "group-1"); err != nil {
		t.Fatal(err)
	}
	if local.propertySubCount("dev1|2|1") != 1 {
		t.Fatalf("gateway property subs = %d", local.propertySubCount("dev1|2|1"))
	}
	if cloudPush.propertyCloseCount != 1 {
		t.Fatalf("cloud property closes = %d", cloudPush.propertyCloseCount)
	}

	local.deviceList = nil
	if err := client.RefreshGatewayDevices(context.Background(), "group-1"); err != nil {
		t.Fatal(err)
	}
	lan.deviceList = []LANDeviceSummary{{
		DID:           "dev1",
		Online:        true,
		PushAvailable: true,
	}}
	if err := client.RefreshLANDevices(); err != nil {
		t.Fatal(err)
	}
	if lan.propertySubCount("dev1|2|1") != 1 {
		t.Fatalf("lan property subs = %d", lan.propertySubCount("dev1|2|1"))
	}
	if local.propertyCloseCount != 1 {
		t.Fatalf("gateway property closes = %d", local.propertyCloseCount)
	}

	lan.deviceList = nil
	if err := client.RefreshLANDevices(); err != nil {
		t.Fatal(err)
	}
	if cloudPush.propertySubCount("dev1|2|1") != 3 {
		t.Fatalf("cloud property subs after fallback = %d", cloudPush.propertySubCount("dev1|2|1"))
	}
	if lan.propertyCloseCount != 1 {
		t.Fatalf("lan property closes = %d", lan.propertyCloseCount)
	}
}

func TestMIoTClientDeviceStateSubscriberOnlyFiresOnAggregateChange(t *testing.T) {
	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {DID: "dev1", Online: false},
			},
		},
	}
	local := newStubMIoTLocalBackend("group-1")
	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		ControlMode: MIoTControlModeAuto,
		Homes: []MIoTClientHome{{
			HomeID:   "home-1",
			HomeName: "Home",
			GroupID:  "group-1",
		}},
		Cloud: cloud,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
	})

	if err := client.RefreshCloudDevices(context.Background()); err != nil {
		t.Fatal(err)
	}

	var states []DeviceState
	sub, err := client.SubscribeDeviceState(context.Background(), "dev1", func(_ string, state DeviceState) {
		states = append(states, state)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	local.deviceList = []LocalDeviceSummary{{
		DID:           "dev1",
		Online:        true,
		SpecV2Access:  true,
		PushAvailable: true,
	}}
	if err := client.RefreshGatewayDevices(context.Background(), "group-1"); err != nil {
		t.Fatal(err)
	}
	if got := len(states); got != 1 {
		t.Fatalf("len(states) = %d", got)
	}
	if states[0] != DeviceStateOnline {
		t.Fatalf("first state = %q", states[0])
	}

	local.deviceList = []LocalDeviceSummary{{
		DID:           "dev1",
		Online:        true,
		SpecV2Access:  true,
		PushAvailable: false,
	}}
	if err := client.RefreshGatewayDevices(context.Background(), "group-1"); err != nil {
		t.Fatal(err)
	}
	if got := len(states); got != 1 {
		t.Fatalf("len(states) after push-only change = %d", got)
	}

	local.deviceList = nil
	if err := client.RefreshGatewayDevices(context.Background(), "group-1"); err != nil {
		t.Fatal(err)
	}
	if got := len(states); got != 2 {
		t.Fatalf("len(states) after offline = %d", got)
	}
	if states[1] != DeviceStateOffline {
		t.Fatalf("second state = %q", states[1])
	}
}

func TestMIoTClientRefreshOAuthUpdatesBackends(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	store := &stubMIoTAuthStore{
		token: OAuthToken{
			AccessToken:  "old-access",
			RefreshToken: "refresh-token",
			ExpiresAt:    clock.now.Add(30 * time.Second),
		},
	}
	oauth := &stubMIoTOAuthBackend{
		token: OAuthToken{
			AccessToken:  "new-access",
			RefreshToken: "new-refresh",
			ExpiresAt:    clock.now.Add(2 * time.Hour),
		},
	}
	cloud := &stubMIoTCloudBackend{}
	cloudPush := newStubMIoTCloudPushBackend()

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Cloud:       cloud,
		CloudPush:   cloudPush,
		OAuth:       oauth,
		AuthStore:   store,
		Clock:       clock,
	})

	if err := client.RefreshOAuthInfo(context.Background()); err != nil {
		t.Fatal(err)
	}
	if oauth.refreshCalls != 1 {
		t.Fatalf("oauth refresh calls = %d", oauth.refreshCalls)
	}
	if cloud.lastAccessToken != "new-access" {
		t.Fatalf("cloud token = %q", cloud.lastAccessToken)
	}
	if !reflect.DeepEqual(cloudPush.refreshTokens, []string{"new-access"}) {
		t.Fatalf("cloud push tokens = %#v", cloudPush.refreshTokens)
	}
	if store.saved.AccessToken != "new-access" {
		t.Fatalf("saved token = %#v", store.saved)
	}
}

func TestMIoTClientStartTreatsNilNetworkAsOnline(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {DID: "dev1", Online: true},
			},
		},
	}
	cloudPush := newStubMIoTCloudPushBackend()

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Homes: []MIoTClientHome{{
			HomeID: "home-1",
		}},
		Cloud:     cloud,
		CloudPush: cloudPush,
		Clock:     clock,
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cloud.snapshotCalls != 0 {
		t.Fatalf("cloud refresh calls before timer = %d, want 0", cloud.snapshotCalls)
	}

	clock.Advance(6 * time.Second)

	waitForCondition(t, func() bool { return cloud.snapshotCalls == 1 })
	if cloud.snapshotCalls != 1 {
		t.Fatalf("cloud refresh calls after timer = %d, want 1", cloud.snapshotCalls)
	}
	if cloudPush.startCalls != 1 {
		t.Fatalf("cloud push start calls = %d, want 1", cloudPush.startCalls)
	}
}

func TestMIoTClientStartObservesCallerOwnedNetwork(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	network := newStubMIoTNetworkBackend(false)
	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {DID: "dev1", Online: true},
			},
		},
	}
	cloudPush := newStubMIoTCloudPushBackend()

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Homes: []MIoTClientHome{{
			HomeID: "home-1",
		}},
		Cloud:     cloud,
		CloudPush: cloudPush,
		Clock:     clock,
		Network:   network,
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if network.startCalls != 0 {
		t.Fatalf("network start calls = %d, want 0", network.startCalls)
	}
	if network.closeCalls != 0 {
		t.Fatalf("network close calls = %d, want 0", network.closeCalls)
	}
	if cloud.snapshotCalls != 0 {
		t.Fatalf("cloud refresh calls before network online = %d, want 0", cloud.snapshotCalls)
	}

	network.Emit(true)
	clock.Advance(6 * time.Second)

	waitForCondition(t, func() bool { return cloud.snapshotCalls == 1 })
	if cloud.snapshotCalls != 1 {
		t.Fatalf("cloud refresh calls after network online = %d, want 1", cloud.snapshotCalls)
	}
}

func TestMIoTClientStartDoesNotMissGatewayConnectionEmittedDuringStart(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	local := newStubMIoTLocalBackend("group-1")
	local.onStart = func(context.Context) error {
		local.EmitConnection(true)
		return nil
	}

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Clock:       clock,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		Homes: []MIoTClientHome{{
			HomeID:  "home-1",
			GroupID: "group-1",
		}},
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if local.getDeviceListCalls != 0 {
		t.Fatalf("gateway refresh calls during start = %d, want 0", local.getDeviceListCalls)
	}

	clock.Advance(0)
	waitForCondition(t, func() bool { return local.getDeviceListCalls == 1 })
}

func TestMIoTClientStartDefersInitialGatewayRefresh(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	local := newStubMIoTLocalBackend("group-1")

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Clock:       clock,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		Homes: []MIoTClientHome{{
			HomeID:  "home-1",
			GroupID: "group-1",
		}},
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if local.getDeviceListCalls != 0 {
		t.Fatalf("gateway refresh calls during start = %d, want 0", local.getDeviceListCalls)
	}

	clock.Advance(0)
	waitForCondition(t, func() bool { return local.getDeviceListCalls == 1 })
}

func TestMIoTClientStartReturnsWithoutSynchronousGatewayReadiness(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	local := newStubMIoTLocalBackend("group-1")
	local.getDeviceListErr = errors.New("not ready")

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Clock:       clock,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		Homes: []MIoTClientHome{{
			HomeID:  "home-1",
			GroupID: "group-1",
		}},
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if local.getDeviceListCalls != 0 {
		t.Fatalf("gateway refresh calls during start = %d, want 0", local.getDeviceListCalls)
	}
}

func TestMIoTClientStartCleansUpAfterGatewayStartFailureAndCanRetry(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	local := newStubMIoTLocalBackend("group-1")
	local.onStart = func(context.Context) error {
		local.startCalls++
		if local.startCalls == 1 {
			return errors.New("boom")
		}
		return nil
	}

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Clock:       clock,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		Homes: []MIoTClientHome{{
			HomeID:  "home-1",
			GroupID: "group-1",
		}},
	})

	if err := client.Start(context.Background()); err == nil {
		t.Fatal("expected first start to fail")
	}
	if client.started {
		t.Fatal("expected started to be reset after failed start")
	}
	if client.runtimeCtx != nil {
		t.Fatal("expected runtime context to be cleared after failed start")
	}
	if client.runtimeCancel != nil {
		t.Fatal("expected runtime cancel to be cleared after failed start")
	}
	if len(local.connSubs) != 0 {
		t.Fatalf("gateway connection subscriptions = %d, want 0", len(local.connSubs))
	}
	if len(local.deviceListSubs) != 0 {
		t.Fatalf("gateway device-list subscriptions = %d, want 0", len(local.deviceListSubs))
	}

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("second start failed: %v", err)
	}
	if local.startCalls != 2 {
		t.Fatalf("gateway start calls = %d, want 2", local.startCalls)
	}
	if len(local.connSubs) != 1 {
		t.Fatalf("gateway connection subscriptions after retry = %d, want 1", len(local.connSubs))
	}
	if len(local.deviceListSubs) != 1 {
		t.Fatalf("gateway device-list subscriptions after retry = %d, want 1", len(local.deviceListSubs))
	}
}

func TestMIoTClientStartRollsBackStartedBackendsAndLANVoteOnFailure(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	local := newStubMIoTLocalBackend("group-1")
	lan := newStubMIoTLANBackend()
	lan.startErr = errors.New("lan start failed")

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		ControlMode: MIoTControlModeAuto,
		Clock:       clock,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		LAN: lan,
		Homes: []MIoTClientHome{{
			HomeID:  "home-1",
			GroupID: "group-1",
		}},
	})

	if err := client.Start(context.Background()); err == nil {
		t.Fatal("expected first start to fail")
	}
	if local.startCalls != 1 {
		t.Fatalf("local start calls = %d, want 1", local.startCalls)
	}
	if local.closeCalls != 1 {
		t.Fatalf("local close calls = %d, want 1", local.closeCalls)
	}
	if lan.startCalls != 1 {
		t.Fatalf("lan start calls = %d, want 1", lan.startCalls)
	}
	if lan.closeCalls != 0 {
		t.Fatalf("lan close calls = %d, want 0", lan.closeCalls)
	}
	if !reflect.DeepEqual(lan.voteCalls, []bool{true, false}) {
		t.Fatalf("lan votes = %#v, want [true false]", lan.voteCalls)
	}
	if client.started {
		t.Fatal("expected started to be reset after failed start")
	}

	lan.startErr = nil
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("second start failed: %v", err)
	}
	if local.startCalls != 2 {
		t.Fatalf("local start calls after retry = %d, want 2", local.startCalls)
	}
	if local.closeCalls != 1 {
		t.Fatalf("local close calls after retry = %d, want 1", local.closeCalls)
	}
	if lan.startCalls != 2 {
		t.Fatalf("lan start calls after retry = %d, want 2", lan.startCalls)
	}
	if lan.closeCalls != 0 {
		t.Fatalf("lan close calls after retry = %d, want 0", lan.closeCalls)
	}
	if !reflect.DeepEqual(lan.voteCalls, []bool{true, false, true}) {
		t.Fatalf("lan votes after retry = %#v, want [true false true]", lan.voteCalls)
	}
}

func TestMIoTClientNetworkOfflineCancelsCloudRefresh(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	network := newStubMIoTNetworkBackend(true)
	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {DID: "dev1", Online: true},
			},
		},
	}
	cloudPush := newStubMIoTCloudPushBackend()

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Homes: []MIoTClientHome{{
			HomeID: "home-1",
		}},
		Cloud:     cloud,
		CloudPush: cloudPush,
		Clock:     clock,
		Network:   network,
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	network.Emit(false)
	clock.Advance(10 * time.Second)

	if cloud.snapshotCalls != 0 {
		t.Fatalf("cloud refresh calls after offline = %d, want 0", cloud.snapshotCalls)
	}
	if cloudPush.closeCalls != 1 {
		t.Fatalf("cloud push close calls = %d, want 1", cloudPush.closeCalls)
	}
}

func TestMIoTClientCloudDisconnectDowngradesState(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {DID: "dev1", Online: true},
			},
		},
	}
	cloudPush := newStubMIoTCloudPushBackend()

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Homes: []MIoTClientHome{{
			HomeID: "home-1",
		}},
		Cloud:     cloud,
		CloudPush: cloudPush,
		Clock:     clock,
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(6 * time.Second)
	waitForCondition(t, func() bool { return cloud.snapshotCalls == 1 })

	sub, err := client.SubscribeProperty(context.Background(), PropertySubscription{
		DID: "dev1",
	}, func(PropertyResult) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	cloudPush.EmitConnection(false)

	waitForCondition(t, func() bool {
		device := client.Devices()["dev1"]
		return !device.CloudOnline && device.State == DeviceStateOffline
	})
	if cloudPush.propertyCloseCount == 0 {
		t.Fatal("expected cloud property subscription to be closed on disconnect")
	}
}

func TestMIoTClientGatewayConnectSchedulesGroupRefresh(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	local := newStubMIoTLocalBackend("group-1")
	cloudPush := newStubMIoTCloudPushBackend()

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Clock:       clock,
		CloudPush:   cloudPush,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		Homes: []MIoTClientHome{{
			HomeID:  "home-1",
			GroupID: "group-1",
		}},
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	initialCalls := local.getDeviceListCalls
	local.deviceList = []LocalDeviceSummary{{
		DID:           "dev1",
		Online:        true,
		SpecV2Access:  true,
		PushAvailable: true,
	}}

	local.EmitConnection(true)
	clock.Advance(2 * time.Second)
	if local.getDeviceListCalls != initialCalls {
		t.Fatalf("gateway refresh calls before delay = %d, want %d", local.getDeviceListCalls, initialCalls)
	}

	clock.Advance(time.Second)
	waitForCondition(t, func() bool { return local.getDeviceListCalls == initialCalls+1 })
}

func TestMIoTClientGatewayDisconnectDowngradesAggregateState(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	local := newStubMIoTLocalBackend("group-1")
	local.deviceList = []LocalDeviceSummary{{
		DID:           "dev1",
		Online:        true,
		SpecV2Access:  true,
		PushAvailable: true,
	}}

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Clock:       clock,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		Homes: []MIoTClientHome{{
			HomeID:  "home-1",
			GroupID: "group-1",
		}},
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(0)
	waitForCondition(t, func() bool {
		device := client.Devices()["dev1"]
		return device.GatewayOnline
	})
	device := client.Devices()["dev1"]
	if !device.GatewayOnline {
		t.Fatal("expected gateway to be online after initial refresh")
	}

	local.EmitConnection(false)

	waitForCondition(t, func() bool {
		device := client.Devices()["dev1"]
		return !device.GatewayOnline && device.State == DeviceStateDisable
	})
}

func TestMIoTClientGatewayRefreshFailureRequeuesWhileConnected(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	local := newStubMIoTLocalBackend("group-1")

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Clock:       clock,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		Homes: []MIoTClientHome{{
			HomeID:  "home-1",
			GroupID: "group-1",
		}},
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	initialCalls := local.getDeviceListCalls
	local.getDeviceListErr = errors.New("boom")

	local.EmitConnection(true)
	clock.Advance(3 * time.Second)
	waitForCondition(t, func() bool { return local.getDeviceListCalls == initialCalls+1 })

	local.getDeviceListErr = nil
	local.deviceList = []LocalDeviceSummary{{
		DID:           "dev1",
		Online:        true,
		SpecV2Access:  true,
		PushAvailable: true,
	}}

	clock.Advance(3 * time.Second)
	waitForCondition(t, func() bool { return local.getDeviceListCalls == initialCalls+2 })
}

func TestMIoTClientRefreshOAuthSchedulesNextRun(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	network := newStubMIoTNetworkBackend(true)
	store := &stubMIoTAuthStore{
		token: OAuthToken{
			AccessToken:  "old-access",
			RefreshToken: "refresh-token",
			ExpiresAt:    clock.now.Add(30 * time.Second),
		},
	}
	oauth := &stubMIoTOAuthBackend{
		token: OAuthToken{
			AccessToken:  "new-access",
			RefreshToken: "new-refresh",
			ExpiresAt:    clock.now.Add(2 * time.Hour),
		},
	}
	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		CloudPush:   newStubMIoTCloudPushBackend(),
		OAuth:       oauth,
		AuthStore:   store,
		Clock:       clock,
		Network:     network,
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if oauth.refreshCalls != 1 {
		t.Fatalf("oauth refresh calls after start = %d, want 1", oauth.refreshCalls)
	}

	clock.Advance(2*time.Hour - miotClientOAuthRefreshMargin - time.Second)
	if oauth.refreshCalls != 1 {
		t.Fatalf("oauth refresh calls before next deadline = %d, want 1", oauth.refreshCalls)
	}

	clock.Advance(time.Second)
	waitForCondition(t, func() bool { return oauth.refreshCalls == 2 })
}

func TestMIoTClientOAuthFailureSkipsCloudReconnect(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	network := newStubMIoTNetworkBackend(true)
	oauth := &stubMIoTOAuthBackend{err: errors.New("boom")}
	store := &stubMIoTAuthStore{
		token: OAuthToken{
			AccessToken:  "old-access",
			RefreshToken: "refresh-token",
			ExpiresAt:    clock.now.Add(30 * time.Second),
		},
	}
	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {DID: "dev1", Online: true},
			},
		},
	}
	cloudPush := newStubMIoTCloudPushBackend()
	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Homes: []MIoTClientHome{{
			HomeID: "home-1",
		}},
		Cloud:     cloud,
		CloudPush: cloudPush,
		OAuth:     oauth,
		AuthStore: store,
		Clock:     clock,
		Network:   network,
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cloudPush.startCalls != 0 {
		t.Fatalf("cloud push start calls = %d, want 0", cloudPush.startCalls)
	}

	clock.Advance(6 * time.Second)
	if cloud.snapshotCalls != 0 {
		t.Fatalf("cloud refresh calls after oauth failure = %d, want 0", cloud.snapshotCalls)
	}
}

func TestMIoTClientRefreshUserCertSchedulesNextRun(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	network := newStubMIoTNetworkBackend(true)
	cloud := &stubMIoTCloudBackend{centralCert: "central-cert"}
	cert := &stubMIoTCertBackend{
		remaining:            30 * time.Second,
		remainingAfterUpdate: 10 * 24 * time.Hour,
	}
	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		VirtualDID:  "virtual-did",
		Cloud:       cloud,
		Cert:        cert,
		Clock:       clock,
		Network:     network,
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cert.verifyCACalls != 1 {
		t.Fatalf("verify ca calls after start = %d, want 1", cert.verifyCACalls)
	}

	clock.Advance(cert.remainingAfterUpdate - miotClientCertRefreshMargin - time.Second)
	if cert.verifyCACalls != 1 {
		t.Fatalf("verify ca calls before next deadline = %d, want 1", cert.verifyCACalls)
	}

	clock.Advance(time.Second)
	waitForCondition(t, func() bool { return cert.verifyCACalls == 2 })
}

func TestMIoTClientCloudRefreshRetriesAfterFailure(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	network := newStubMIoTNetworkBackend(true)
	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {DID: "dev1", Online: true},
			},
		},
		snapshotErrs: []error{errors.New("boom"), nil},
	}
	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Homes: []MIoTClientHome{{
			HomeID: "home-1",
		}},
		Cloud:     cloud,
		CloudPush: newStubMIoTCloudPushBackend(),
		Clock:     clock,
		Network:   network,
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	clock.Advance(6 * time.Second)
	waitForCondition(t, func() bool { return cloud.snapshotCalls == 1 })

	clock.Advance(59 * time.Second)
	if cloud.snapshotCalls != 1 {
		t.Fatalf("cloud refresh calls before retry delay = %d, want 1", cloud.snapshotCalls)
	}

	clock.Advance(time.Second)
	waitForCondition(t, func() bool { return cloud.snapshotCalls == 2 })
}

func TestMIoTClientPropertyRefreshQueueRetriesThreeTimesThenDrops(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	cloud := &stubMIoTCloudBackend{getPropsErr: errors.New("boom")}
	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Cloud:       cloud,
		Clock:       clock,
	})
	seedClientDevice(client, "dev1")

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	client.RequestRefreshProperty("dev1", 2, 1)

	clock.Advance(200 * time.Millisecond)
	waitForCondition(t, func() bool { return cloud.getPropsCalls == 1 })
	clock.Advance(3 * time.Second)
	waitForCondition(t, func() bool { return cloud.getPropsCalls == 2 })
	clock.Advance(3 * time.Second)
	waitForCondition(t, func() bool { return cloud.getPropsCalls == 3 })
	clock.Advance(3 * time.Second)
	waitForCondition(t, func() bool { return queuedPropertyCount(client) == 0 })
}

func TestMIoTClientPropertyRefreshQueueCloudChunking(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	cloud := &stubMIoTCloudBackend{getPropsErr: errors.New("boom")}
	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Cloud:       cloud,
		Clock:       clock,
	})
	seedClientDevice(client, "dev1")

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for piid := 1; piid <= 151; piid++ {
		client.RequestRefreshProperty("dev1", 2, piid)
	}

	clock.Advance(200 * time.Millisecond)
	waitForCondition(t, func() bool { return cloud.getPropsCalls == 1 })
	if got := len(cloud.getPropsRequests[0].Params); got != 150 {
		t.Fatalf("first cloud batch size = %d, want 150", got)
	}
}

func TestMIoTClientRuntimeTransitionsMaintainPushSourceAndState(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	network := newStubMIoTNetworkBackend(false)
	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {
					DID:    "dev1",
					Online: true,
				},
			},
		},
	}
	cloudPush := newStubMIoTCloudPushBackend()
	local := newStubMIoTLocalBackend("group-1")

	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		Clock:       clock,
		Network:     network,
		Cloud:       cloud,
		CloudPush:   cloudPush,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		Homes: []MIoTClientHome{{
			HomeID:  "home-1",
			GroupID: "group-1",
		}},
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	network.Emit(true)
	clock.Advance(6 * time.Second)
	waitForCondition(t, func() bool {
		device := client.Devices()["dev1"]
		return device.PushSource == miotClientPushSourceCloud && device.State == DeviceStateOnline
	})

	local.deviceList = []LocalDeviceSummary{{
		DID:           "dev1",
		Online:        true,
		SpecV2Access:  true,
		PushAvailable: true,
	}}
	local.EmitConnection(true)
	clock.Advance(3 * time.Second)
	waitForCondition(t, func() bool {
		device := client.Devices()["dev1"]
		return device.PushSource == "group-1"
	})

	local.EmitConnection(false)
	waitForCondition(t, func() bool {
		device := client.Devices()["dev1"]
		return device.PushSource == miotClientPushSourceCloud
	})

	network.Emit(false)
	waitForCondition(t, func() bool {
		device := client.Devices()["dev1"]
		return device.PushSource == "" && device.State == DeviceStateOffline
	})
}

func TestMIoTClientRefreshUserCertRotatesCertificateWhenNearExpiry(t *testing.T) {
	cert := &stubMIoTCertBackend{
		remaining: 12 * time.Hour,
	}
	cloud := &stubMIoTCloudBackend{
		centralCert: "signed-cert",
	}
	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		VirtualDID:  "123456",
		Cloud:       cloud,
		Cert:        cert,
	})

	if err := client.RefreshUserCert(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cert.verifyCACalls != 1 {
		t.Fatalf("verify ca calls = %d", cert.verifyCACalls)
	}
	if cert.generateKeyCalls != 1 {
		t.Fatalf("generate key calls = %d", cert.generateKeyCalls)
	}
	if cert.generateCSRDid != "123456" {
		t.Fatalf("csr did = %q", cert.generateCSRDid)
	}
	if cloud.lastCSR != "csr-body" {
		t.Fatalf("csr body = %q", cloud.lastCSR)
	}
	if string(cert.updatedCert) != "signed-cert" {
		t.Fatalf("updated cert = %q", cert.updatedCert)
	}
}

func TestMIoTClientImplementsEntityBackend(t *testing.T) {
	var _ EntityBackend = (*MIoTClient)(nil)
}

func newRoutingTestClient(t *testing.T) (*MIoTClient, *stubMIoTCloudPushBackend, *stubMIoTCloudBackend, *stubMIoTLocalBackend, *stubMIoTLANBackend) {
	t.Helper()

	cloud := &stubMIoTCloudBackend{
		snapshot: DeviceSnapshot{
			Devices: map[string]DeviceInfo{
				"dev1": {
					DID:         "dev1",
					Name:        "Lamp",
					Model:       "yeelink.light",
					Online:      true,
					Token:       "00112233445566778899aabbccddeeff",
					ConnectType: 0,
				},
			},
		},
		setResults: []SetPropertyResult{{
			DID:  "dev1",
			SIID: 2,
			PIID: 1,
			Code: 0,
		}},
		actionResult: ActionResult{Code: 0},
	}
	cloudPush := newStubMIoTCloudPushBackend()
	local := newStubMIoTLocalBackend("group-1")
	lan := newStubMIoTLANBackend()
	client := mustNewTestMIoTClient(t, MIoTClientConfig{
		UID:         "user1",
		CloudServer: "cn",
		ControlMode: MIoTControlModeAuto,
		Homes: []MIoTClientHome{{
			HomeID:   "home-1",
			HomeName: "Home",
			GroupID:  "group-1",
		}},
		Cloud:     cloud,
		CloudPush: cloudPush,
		LocalRoutes: map[string]MIoTLocalBackend{
			"group-1": local,
		},
		LAN: lan,
	})
	if err := client.RefreshCloudDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	return client, cloudPush, cloud, local, lan
}

func mustNewTestMIoTClient(t *testing.T, cfg MIoTClientConfig) *MIoTClient {
	t.Helper()

	client, err := NewMIoTClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type stubMIoTCloudBackend struct {
	snapshot          DeviceSnapshot
	snapshotErrs      []error
	details           []DeviceInfo
	propResult        PropertyResult
	getPropsErr       error
	getPropsCalls     int
	getPropsRequests  []GetPropsRequest
	getPropsResults   []PropertyResult
	setResults        []SetPropertyResult
	actionResult      ActionResult
	snapshotCalls     int
	getPropCalls      int
	setPropsCalls     int
	invokeActionCalls int
	lastAccessToken   string
	lastCSR           string
	centralCert       string
}

func (s *stubMIoTCloudBackend) GetDevices(context.Context, []string) (DeviceSnapshot, error) {
	s.snapshotCalls++
	if len(s.snapshotErrs) > 0 {
		err := s.snapshotErrs[0]
		s.snapshotErrs = s.snapshotErrs[1:]
		if err != nil {
			return DeviceSnapshot{}, err
		}
	}
	return s.snapshot, nil
}

func (s *stubMIoTCloudBackend) GetDevicesByDID(context.Context, []string) ([]DeviceInfo, error) {
	return append([]DeviceInfo(nil), s.details...), nil
}

func (s *stubMIoTCloudBackend) GetProps(_ context.Context, req GetPropsRequest) ([]PropertyResult, error) {
	s.getPropsCalls++
	copied := GetPropsRequest{Params: append([]PropertyQuery(nil), req.Params...)}
	s.getPropsRequests = append(s.getPropsRequests, copied)
	if s.getPropsErr != nil {
		return nil, s.getPropsErr
	}
	if len(s.getPropsResults) > 0 {
		return append([]PropertyResult(nil), s.getPropsResults...), nil
	}
	return []PropertyResult{s.propResult}, nil
}

func (s *stubMIoTCloudBackend) GetProp(context.Context, PropertyQuery) (PropertyResult, error) {
	s.getPropCalls++
	return s.propResult, nil
}

func (s *stubMIoTCloudBackend) SetProps(context.Context, SetPropsRequest) ([]SetPropertyResult, error) {
	s.setPropsCalls++
	return append([]SetPropertyResult(nil), s.setResults...), nil
}

func (s *stubMIoTCloudBackend) InvokeAction(context.Context, ActionRequest) (ActionResult, error) {
	s.invokeActionCalls++
	return s.actionResult, nil
}

func (s *stubMIoTCloudBackend) UpdateAuth(_, _, accessToken string) error {
	if accessToken != "" {
		s.lastAccessToken = accessToken
	}
	return nil
}

func (s *stubMIoTCloudBackend) GetCentralCert(_ context.Context, csr string) (string, error) {
	s.lastCSR = csr
	return s.centralCert, nil
}

type stubMIoTCloudPushBackend struct {
	propertySubs       map[string]int
	propertyCloseCount int
	eventSubs          map[string]int
	stateSubs          map[string]DeviceStateHandler
	refreshTokens      []string
	startCalls         int
	closeCalls         int
	connSubs           map[int]func(bool)
	nextConnID         int
}

func newStubMIoTCloudPushBackend() *stubMIoTCloudPushBackend {
	return &stubMIoTCloudPushBackend{
		propertySubs: make(map[string]int),
		eventSubs:    make(map[string]int),
		stateSubs:    make(map[string]DeviceStateHandler),
		connSubs:     make(map[int]func(bool)),
	}
}

func (s *stubMIoTCloudPushBackend) Start(context.Context) error {
	s.startCalls++
	return nil
}

func (s *stubMIoTCloudPushBackend) Close() error {
	s.closeCalls++
	return nil
}

func (s *stubMIoTCloudPushBackend) RefreshAccessToken(_ context.Context, token string) error {
	s.refreshTokens = append(s.refreshTokens, token)
	return nil
}

func (s *stubMIoTCloudPushBackend) SubscribeProperty(_ context.Context, req PropertySubscription, fn PropertyEventHandler) (Subscription, error) {
	key := propertySubKey(req)
	s.propertySubs[key]++
	return subscriptionFunc(func() error {
		s.propertyCloseCount++
		return nil
	}), nil
}

func (s *stubMIoTCloudPushBackend) SubscribeEvent(_ context.Context, req EventSubscription, fn EventHandler) (Subscription, error) {
	key := eventSubKey(req)
	s.eventSubs[key]++
	return subscriptionFunc(func() error { return nil }), nil
}

func (s *stubMIoTCloudPushBackend) SubscribeDeviceState(_ context.Context, did string, fn DeviceStateHandler) (Subscription, error) {
	s.stateSubs[did] = fn
	return subscriptionFunc(func() error {
		delete(s.stateSubs, did)
		return nil
	}), nil
}

func (s *stubMIoTCloudPushBackend) SubscribeConnectionState(fn func(bool)) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}
	id := s.nextConnID
	s.nextConnID++
	s.connSubs[id] = fn
	return subscriptionFunc(func() error {
		delete(s.connSubs, id)
		return nil
	})
}

func (s *stubMIoTCloudPushBackend) EmitConnection(connected bool) {
	for _, fn := range s.connSubs {
		if fn != nil {
			fn(connected)
		}
	}
}

func (s *stubMIoTCloudPushBackend) propertySubCount(key string) int {
	return s.propertySubs[key]
}

type stubMIoTLocalBackend struct {
	groupID            string
	deviceList         []LocalDeviceSummary
	getDeviceListErr   error
	getDeviceListCalls int
	startCalls         int
	closeCalls         int
	getPropErr         error
	getPropCalls       int
	setPropCalls       int
	actionCalls        int
	propertySubs       map[string]int
	propertyCloseCount int
	connSubs           map[int]func(bool)
	deviceListSubs     map[int]func([]string)
	nextRuntimeID      int
	onStart            func(context.Context) error
}

func newStubMIoTLocalBackend(groupID string) *stubMIoTLocalBackend {
	return &stubMIoTLocalBackend{
		groupID:        groupID,
		propertySubs:   make(map[string]int),
		connSubs:       make(map[int]func(bool)),
		deviceListSubs: make(map[int]func([]string)),
	}
}

func (s *stubMIoTLocalBackend) Start(ctx context.Context) error {
	if s.onStart != nil {
		return s.onStart(ctx)
	}
	s.startCalls++
	return nil
}
func (s *stubMIoTLocalBackend) Close() error {
	s.closeCalls++
	return nil
}
func (s *stubMIoTLocalBackend) GroupID() string { return s.groupID }

func (s *stubMIoTLocalBackend) GetDeviceList(context.Context) ([]LocalDeviceSummary, error) {
	s.getDeviceListCalls++
	if s.getDeviceListErr != nil {
		return nil, s.getDeviceListErr
	}
	return append([]LocalDeviceSummary(nil), s.deviceList...), nil
}

func (s *stubMIoTLocalBackend) GetPropSafe(context.Context, PropertyQuery) (PropertyResult, error) {
	s.getPropCalls++
	if s.getPropErr != nil {
		return PropertyResult{}, s.getPropErr
	}
	return PropertyResult{
		DID:   "dev1",
		SIID:  2,
		PIID:  1,
		Value: NewSpecValueBool(true),
	}, nil
}

func (s *stubMIoTLocalBackend) SetProp(context.Context, SetPropertyRequest) (SetPropertyResult, error) {
	s.setPropCalls++
	return SetPropertyResult{DID: "dev1", SIID: 2, PIID: 1, Code: 0}, nil
}

func (s *stubMIoTLocalBackend) InvokeAction(context.Context, ActionRequest) (ActionResult, error) {
	s.actionCalls++
	return ActionResult{Code: 0}, nil
}

func (s *stubMIoTLocalBackend) SubscribeProperty(_ context.Context, req PropertySubscription, fn PropertyEventHandler) (Subscription, error) {
	key := propertySubKey(req)
	s.propertySubs[key]++
	return subscriptionFunc(func() error {
		s.propertyCloseCount++
		return nil
	}), nil
}

func (s *stubMIoTLocalBackend) SubscribeEvent(context.Context, EventSubscription, EventHandler) (Subscription, error) {
	return subscriptionFunc(func() error { return nil }), nil
}

func (s *stubMIoTLocalBackend) SubscribeConnectionState(fn func(bool)) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}
	id := s.nextRuntimeID
	s.nextRuntimeID++
	s.connSubs[id] = fn
	return subscriptionFunc(func() error {
		delete(s.connSubs, id)
		return nil
	})
}

func (s *stubMIoTLocalBackend) SubscribeDeviceListChanged(fn func([]string)) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}
	id := s.nextRuntimeID
	s.nextRuntimeID++
	s.deviceListSubs[id] = fn
	return subscriptionFunc(func() error {
		delete(s.deviceListSubs, id)
		return nil
	})
}

func (s *stubMIoTLocalBackend) EmitConnection(connected bool) {
	for _, fn := range s.connSubs {
		if fn != nil {
			fn(connected)
		}
	}
}

func (s *stubMIoTLocalBackend) EmitDeviceListChanged(dids []string) {
	for _, fn := range s.deviceListSubs {
		if fn != nil {
			fn(append([]string(nil), dids...))
		}
	}
}

func (s *stubMIoTLocalBackend) propertySubCount(key string) int {
	return s.propertySubs[key]
}

type stubMIoTLANBackend struct {
	deviceList         []LANDeviceSummary
	startCalls         int
	closeCalls         int
	startErr           error
	getPropErr         error
	getPropCalls       int
	setPropCalls       int
	actionCalls        int
	propertySubs       map[string]int
	propertyCloseCount int
	updateDevicesCalls [][]LANDeviceConfig
	voteCalls          []bool
}

func newStubMIoTLANBackend() *stubMIoTLANBackend {
	return &stubMIoTLANBackend{
		propertySubs: make(map[string]int),
	}
}

func (s *stubMIoTLANBackend) Start(context.Context) error {
	s.startCalls++
	return s.startErr
}
func (s *stubMIoTLANBackend) Close() error {
	s.closeCalls++
	return nil
}

func (s *stubMIoTLANBackend) GetDeviceList() []LANDeviceSummary {
	return append([]LANDeviceSummary(nil), s.deviceList...)
}

func (s *stubMIoTLANBackend) GetProp(context.Context, PropertyQuery) (PropertyResult, error) {
	s.getPropCalls++
	if s.getPropErr != nil {
		return PropertyResult{}, s.getPropErr
	}
	return PropertyResult{
		DID:   "dev1",
		SIID:  2,
		PIID:  1,
		Value: NewSpecValueBool(true),
	}, nil
}

func (s *stubMIoTLANBackend) SetProp(context.Context, SetPropertyRequest) (SetPropertyResult, error) {
	s.setPropCalls++
	return SetPropertyResult{DID: "dev1", SIID: 2, PIID: 1, Code: 0}, nil
}

func (s *stubMIoTLANBackend) InvokeAction(context.Context, ActionRequest) (ActionResult, error) {
	s.actionCalls++
	return ActionResult{Code: 0}, nil
}

func (s *stubMIoTLANBackend) SubscribeProperty(req PropertySubscription, fn PropertyEventHandler) Subscription {
	key := propertySubKey(req)
	s.propertySubs[key]++
	return subscriptionFunc(func() error {
		s.propertyCloseCount++
		return nil
	})
}

func (s *stubMIoTLANBackend) SubscribeEvent(EventSubscription, EventHandler) Subscription {
	return subscriptionFunc(func() error { return nil })
}

func (s *stubMIoTLANBackend) SubscribeDeviceState(DeviceStateHandler) Subscription {
	return subscriptionFunc(func() error { return nil })
}

func (s *stubMIoTLANBackend) SubscribeLANState(func(bool)) Subscription {
	return subscriptionFunc(func() error { return nil })
}

func (s *stubMIoTLANBackend) UpdateDevices(devices []LANDeviceConfig) error {
	copied := append([]LANDeviceConfig(nil), devices...)
	s.updateDevicesCalls = append(s.updateDevicesCalls, copied)
	return nil
}

func (s *stubMIoTLANBackend) VoteForLANControl(_ string, vote bool) {
	s.voteCalls = append(s.voteCalls, vote)
}

func (s *stubMIoTLANBackend) propertySubCount(key string) int {
	return s.propertySubs[key]
}

type stubMIoTOAuthBackend struct {
	token        OAuthToken
	err          error
	refreshCalls int
}

func (s *stubMIoTOAuthBackend) RefreshToken(context.Context, string) (OAuthToken, error) {
	s.refreshCalls++
	if s.err != nil {
		return OAuthToken{}, s.err
	}
	return s.token, nil
}

type stubMIoTAuthStore struct {
	token OAuthToken
	saved OAuthToken
}

func (s *stubMIoTAuthStore) LoadOAuthToken(context.Context) (OAuthToken, error) {
	return s.token, nil
}

func (s *stubMIoTAuthStore) SaveOAuthToken(_ context.Context, token OAuthToken) error {
	s.token = token
	s.saved = token
	return nil
}

type stubMIoTCertBackend struct {
	remaining            time.Duration
	remainingAfterUpdate time.Duration
	verifyCACalls        int
	generateKeyCalls     int
	generateCSRDid       string
	updatedKey           []byte
	updatedCert          []byte
}

func (s *stubMIoTCertBackend) VerifyCACert(context.Context) error {
	s.verifyCACalls++
	return nil
}

func (s *stubMIoTCertBackend) UserCertRemaining(context.Context, []byte, string) (time.Duration, error) {
	if len(s.updatedCert) > 0 && s.remainingAfterUpdate > 0 {
		return s.remainingAfterUpdate, nil
	}
	return s.remaining, nil
}

func (s *stubMIoTCertBackend) LoadUserKey(context.Context) ([]byte, error) {
	return nil, errors.New("missing key")
}

func (s *stubMIoTCertBackend) GenerateUserKey() ([]byte, error) {
	s.generateKeyCalls++
	return []byte("private-key"), nil
}

func (s *stubMIoTCertBackend) UpdateUserKey(_ context.Context, keyPEM []byte) error {
	s.updatedKey = append([]byte(nil), keyPEM...)
	return nil
}

func (s *stubMIoTCertBackend) GenerateUserCSR(_ []byte, did string) ([]byte, error) {
	s.generateCSRDid = did
	return []byte("csr-body"), nil
}

func (s *stubMIoTCertBackend) UpdateUserCert(_ context.Context, certPEM []byte) error {
	s.updatedCert = append([]byte(nil), certPEM...)
	return nil
}

type stubClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*stubTimer
}

func TestStubClockNewTimerCanBeStoppedAndReset(t *testing.T) {
	clock := newStubClock(time.Unix(1000, 0))
	timer := clock.NewTimer(5 * time.Second)
	if !timer.Stop() {
		t.Fatal("expected active timer to stop")
	}
	if timer.Reset(3 * time.Second) {
		t.Fatal("expected reset on stopped timer to report false")
	}
}

func newStubClock(now time.Time) *stubClock {
	return &stubClock{now: now}
}

func (c *stubClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stubClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- c.Now().Add(d)
	return ch
}

func (c *stubClock) NewTicker(time.Duration) Ticker {
	return stubTicker{c: make(chan time.Time)}
}

func (c *stubClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &stubTimer{
		clock:    c,
		c:        make(chan time.Time, 1),
		active:   true,
		deadline: c.now.Add(d),
	}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *stubClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	timers := append([]*stubTimer(nil), c.timers...)
	c.mu.Unlock()

	for _, timer := range timers {
		timer.fireIfDue(now)
	}
}

type stubTicker struct {
	c chan time.Time
}

func (t stubTicker) C() <-chan time.Time {
	return t.c
}

func (t stubTicker) Stop() {}

type stubTimer struct {
	clock    *stubClock
	mu       sync.Mutex
	c        chan time.Time
	active   bool
	deadline time.Time
}

func (t *stubTimer) C() <-chan time.Time {
	return t.c
}

func (t *stubTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	wasActive := t.active
	t.active = false
	return wasActive
}

func (t *stubTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	now := t.clock.now
	t.clock.mu.Unlock()

	t.mu.Lock()
	defer t.mu.Unlock()
	wasActive := t.active
	t.active = true
	t.deadline = now.Add(d)
	for {
		select {
		case <-t.c:
		default:
			return wasActive
		}
	}
}

func (t *stubTimer) fireIfDue(now time.Time) {
	t.mu.Lock()
	if !t.active || t.deadline.After(now) {
		t.mu.Unlock()
		return
	}
	t.active = false
	deadline := t.deadline
	t.mu.Unlock()

	select {
	case t.c <- deadline:
	default:
	}
}

type stubMIoTNetworkBackend struct {
	mu         sync.Mutex
	status     bool
	subs       map[int]func(bool)
	nextID     int
	startCalls int
	closeCalls int
}

func newStubMIoTNetworkBackend(status bool) *stubMIoTNetworkBackend {
	return &stubMIoTNetworkBackend{
		status: status,
		subs:   make(map[int]func(bool)),
	}
}

func (s *stubMIoTNetworkBackend) Start(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startCalls++
	return nil
}

func (s *stubMIoTNetworkBackend) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCalls++
	return nil
}

func (s *stubMIoTNetworkBackend) Status() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *stubMIoTNetworkBackend) SubscribeStatus(fn func(bool)) Subscription {
	if fn == nil {
		return subscriptionFunc(nil)
	}
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.subs[id] = fn
	s.mu.Unlock()
	return subscriptionFunc(func() error {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
		return nil
	})
}

func (s *stubMIoTNetworkBackend) Emit(status bool) {
	s.mu.Lock()
	s.status = status
	subs := make([]func(bool), 0, len(s.subs))
	for _, fn := range s.subs {
		if fn != nil {
			subs = append(subs, fn)
		}
	}
	s.mu.Unlock()
	for _, fn := range subs {
		fn(status)
	}
}

func propertySubKey(req PropertySubscription) string {
	return req.DID + "|" + strconv.Itoa(req.SIID) + "|" + strconv.Itoa(req.PIID)
}

func eventSubKey(req EventSubscription) string {
	return req.DID + "|" + strconv.Itoa(req.SIID) + "|" + strconv.Itoa(req.EIID)
}

func waitForCondition(t *testing.T, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func seedClientDevice(client *MIoTClient, did string) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.devices[did] = &miotClientDeviceEntry{
		info:         DeviceInfo{DID: did},
		cloudPresent: true,
		cloudOnline:  true,
		state:        DeviceStateOnline,
	}
}

func queuedPropertyCount(client *MIoTClient) int {
	client.mu.RLock()
	defer client.mu.RUnlock()
	return len(client.queuedProps)
}
