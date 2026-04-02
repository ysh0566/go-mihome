package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

func TestRuntimeDefaultClientIDMatchesHAOAuthApp(t *testing.T) {
	t.Parallel()

	if defaultClientID != "2882303761520251711" {
		t.Fatalf("defaultClientID = %q, want HA oauth app 2882303761520251711", defaultClientID)
	}
}

func TestDefaultRuntimeExampleConfigDoesNotEmbedOAuthTokens(t *testing.T) {
	t.Parallel()

	cfg := defaultRuntimeExampleConfig()
	if cfg.ClientID != "2882303761520251711" {
		t.Fatalf("ClientID = %q, want 2882303761520251711", cfg.ClientID)
	}
	if cfg.AccessToken != "" {
		t.Fatalf("AccessToken = %q, want empty default", cfg.AccessToken)
	}
	if cfg.RefreshToken != "" {
		t.Fatalf("RefreshToken = %q, want empty default", cfg.RefreshToken)
	}
}

func TestRuntimeOAuthTokenSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cfg       exampleutil.RuntimeExampleConfig
		bootstrap exampleutil.RuntimeBootstrapState
		want      string
	}{
		{
			name: "env pair",
			cfg: exampleutil.RuntimeExampleConfig{
				AccessToken:  "env-access",
				RefreshToken: "env-refresh",
			},
			want: "env_pair",
		},
		{
			name: "partial env",
			cfg: exampleutil.RuntimeExampleConfig{
				AccessToken: "env-access",
			},
			want: "partial_env_pair",
		},
		{
			name:      "storage auth",
			bootstrap: exampleutil.RuntimeBootstrapState{UID: "10001"},
			want:      "storage_auth_info",
		},
		{
			name: "first run missing",
			want: "missing",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := runtimeOAuthTokenSource(tc.cfg, tc.bootstrap); got != tc.want {
				t.Fatalf("runtimeOAuthTokenSource() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRuntimeLoggerStartAndSuccess(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newRuntimeLogger(&buf)

	finish := logger.StartStep("storage_init", "storage_dir=/tmp/miot-cache")
	finish(nil)

	output := buf.String()
	if !strings.Contains(output, "runtime step=storage_init status=start storage_dir=/tmp/miot-cache") {
		t.Fatalf("start log missing details, got %q", output)
	}
	if !strings.Contains(output, "runtime step=storage_init status=ok duration=") {
		t.Fatalf("success log missing duration, got %q", output)
	}
}

func TestRuntimeLoggerFailureAndWarning(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newRuntimeLogger(&buf)

	finish := logger.StartStep("resolve_uid", "")
	finish(errors.New("request failed"))
	logger.Warnf("mdns_bootstrap", "no MIoT central gateways discovered during bootstrap window")

	output := buf.String()
	if !strings.Contains(output, "runtime step=resolve_uid status=error duration=") {
		t.Fatalf("failure log missing duration, got %q", output)
	}
	if !strings.Contains(output, "err=\"request failed\"") {
		t.Fatalf("failure log missing error, got %q", output)
	}
	if !strings.Contains(output, "runtime step=mdns_bootstrap status=warn msg=\"no MIoT central gateways discovered during bootstrap window\"") {
		t.Fatalf("warning log missing message, got %q", output)
	}
}

func TestRuntimeLoggerStateChange(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newRuntimeLogger(&buf)

	logger.Statef("cloud_push_state", "connected=%t", true)

	output := buf.String()
	if !strings.Contains(output, "runtime step=cloud_push_state status=state msg=\"connected=true\"") {
		t.Fatalf("state-change log missing message, got %q", output)
	}
}

func TestBuildRuntimeSnapshotIncludesDiagnostics(t *testing.T) {
	t.Parallel()

	devices := map[string]miot.MIoTClientDevice{
		"dev-cloud": {
			State:       miot.DeviceStateOnline,
			CloudOnline: true,
			PushSource:  "cloud",
		},
		"dev-gateway": {
			State:         miot.DeviceStateOnline,
			GatewayOnline: true,
			PushSource:    "group-1",
		},
		"dev-lan": {
			State:      miot.DeviceStateOnline,
			LANOnline:  true,
			PushSource: "lan",
		},
		"dev-offline": {
			State: miot.DeviceStateDisable,
		},
	}

	diag := newRuntimeHealth()
	diag.SetNetworkOnline(true)
	diag.SetCloudPushConnected(true)
	diag.SetCloudPushLastError("auth denied")
	diag.SetLANControlEnabled(false)
	diag.SetWarnings([]string{"mdns bootstrap timeout", "local route group-2 preflight failed"})
	diag.SetDiscoveryServiceCount(2)
	diag.SetLocalRouteCandidateCount(2)
	diag.UpsertLocalRoute(runtimeLocalRouteState{
		GroupID:   "group-2",
		HomeID:    "home-2",
		HomeName:  "Guest Home",
		Host:      "192.168.1.2",
		Port:      8883,
		Admitted:  false,
		Connected: false,
		LastError: "preflight failed",
	})
	diag.UpsertLocalRoute(runtimeLocalRouteState{
		GroupID:   "group-1",
		HomeID:    "home-1",
		HomeName:  "Primary Home",
		Host:      "192.168.1.1",
		Port:      8883,
		Admitted:  true,
		Connected: true,
	})

	got := buildRuntimeSnapshotEvent(devices, runtimeSnapshotMeta{
		Type:              "snapshot",
		UID:               "123",
		StorageDir:        "/tmp/cache",
		HomeCount:         2,
		LocalRouteCount:   1,
		CloudPushHost:     "cn-ha.mqtt.io.mi.com",
		CloudPushPort:     8883,
		CloudPushClientID: "runtime-client",
		Timestamp:         "2026-04-02T00:00:00Z",
	}, diag.Snapshot())

	if got.DeviceCount != 4 {
		t.Fatalf("DeviceCount = %d, want 4", got.DeviceCount)
	}
	if got.OnlineCount != 3 {
		t.Fatalf("OnlineCount = %d, want 3", got.OnlineCount)
	}
	if !got.NetworkOnline || !got.CloudPushConnected || got.LANControlEnabled {
		t.Fatalf("connectivity = %#v, want network/cloud push true and lan control false", got)
	}
	if got.CloudPushLastError != "auth denied" {
		t.Fatalf("CloudPushLastError = %q, want auth denied", got.CloudPushLastError)
	}
	if got.DiscoveryServiceCount != 2 {
		t.Fatalf("DiscoveryServiceCount = %d, want 2", got.DiscoveryServiceCount)
	}
	if got.LocalRouteCandidateCount != 2 {
		t.Fatalf("LocalRouteCandidateCount = %d, want 2", got.LocalRouteCandidateCount)
	}
	if got.CloudPushHost != "cn-ha.mqtt.io.mi.com" || got.CloudPushPort != 8883 || got.CloudPushClientID != "runtime-client" {
		t.Fatalf("cloud push target = %#v", got)
	}
	if !reflect.DeepEqual(got.Warnings, []string{"mdns bootstrap timeout", "local route group-2 preflight failed"}) {
		t.Fatalf("Warnings = %v", got.Warnings)
	}
	wantRoutes := []runtimeLocalRouteState{
		{
			GroupID:   "group-1",
			HomeID:    "home-1",
			HomeName:  "Primary Home",
			Host:      "192.168.1.1",
			Port:      8883,
			Admitted:  true,
			Connected: true,
		},
		{
			GroupID:   "group-2",
			HomeID:    "home-2",
			HomeName:  "Guest Home",
			Host:      "192.168.1.2",
			Port:      8883,
			Admitted:  false,
			Connected: false,
			LastError: "preflight failed",
		},
	}
	if !reflect.DeepEqual(got.LocalRoutes, wantRoutes) {
		t.Fatalf("LocalRoutes = %#v, want %#v", got.LocalRoutes, wantRoutes)
	}
	if got.DeviceSourceCounts.CloudOnline != 1 || got.DeviceSourceCounts.GatewayOnline != 1 || got.DeviceSourceCounts.LANOnline != 1 {
		t.Fatalf("online source counts = %#v", got.DeviceSourceCounts)
	}
	if got.DeviceSourceCounts.PushSourceCloud != 1 || got.DeviceSourceCounts.PushSourceGateway != 1 || got.DeviceSourceCounts.PushSourceLAN != 1 {
		t.Fatalf("push source counts = %#v", got.DeviceSourceCounts)
	}
}

func TestRunRuntimeLocalRoutePreflightAwaitsBeforeDeviceList(t *testing.T) {
	t.Parallel()

	var steps []string
	client := &fakeRuntimeLocalRoutePreflightClient{
		onStart: func(context.Context) error {
			steps = append(steps, "start")
			return nil
		},
		onAwaitConnection: func(context.Context) error {
			steps = append(steps, "await")
			return nil
		},
		onGetDeviceList: func(context.Context) ([]miot.LocalDeviceSummary, error) {
			steps = append(steps, "get_device_list")
			return nil, nil
		},
		onClose: func() error {
			steps = append(steps, "close")
			return nil
		},
	}

	if err := runRuntimeLocalRoutePreflight(context.Background(), client); err != nil {
		t.Fatalf("runRuntimeLocalRoutePreflight returned error: %v", err)
	}

	want := []string{"start", "await", "get_device_list", "close"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
}

func TestRunRuntimeLocalRoutePreflightClosesClientWhenAwaitFails(t *testing.T) {
	t.Parallel()

	client := &fakeRuntimeLocalRoutePreflightClient{
		onStart: func(context.Context) error { return nil },
		onAwaitConnection: func(context.Context) error {
			return errors.New("await failed")
		},
	}

	err := runRuntimeLocalRoutePreflight(context.Background(), client)
	if err == nil {
		t.Fatal("runRuntimeLocalRoutePreflight returned nil error, want await failure")
	}
	if !client.closed {
		t.Fatal("preflight client was not closed after await failure")
	}
}

type fakeRuntimeLocalRoutePreflightClient struct {
	onStart           func(context.Context) error
	onAwaitConnection func(context.Context) error
	onGetDeviceList   func(context.Context) ([]miot.LocalDeviceSummary, error)
	onClose           func() error
	closed            bool
}

func (f *fakeRuntimeLocalRoutePreflightClient) Start(ctx context.Context) error {
	if f.onStart != nil {
		return f.onStart(ctx)
	}
	return nil
}

func (f *fakeRuntimeLocalRoutePreflightClient) AwaitConnection(ctx context.Context) error {
	if f.onAwaitConnection != nil {
		return f.onAwaitConnection(ctx)
	}
	return nil
}

func (f *fakeRuntimeLocalRoutePreflightClient) GetDeviceList(ctx context.Context) ([]miot.LocalDeviceSummary, error) {
	if f.onGetDeviceList != nil {
		return f.onGetDeviceList(ctx)
	}
	return nil, nil
}

func (f *fakeRuntimeLocalRoutePreflightClient) Close() error {
	f.closed = true
	if f.onClose != nil {
		return f.onClose()
	}
	return nil
}
