package exampleutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	miot "github.com/ysh0566/go-mihome"
)

func TestLoadRuntimeExampleConfigPrefersEnvironment(t *testing.T) {
	t.Setenv("MIOT_STORAGE_DIR", "/tmp/miot-runtime-cache")
	t.Setenv("MIOT_CLIENT_ID", "env-client")
	t.Setenv("MIOT_CLOUD_SERVER", "de")
	t.Setenv("MIOT_RUNTIME_SNAPSHOT_INTERVAL", "45s")
	t.Setenv("MIOT_MDNS_BOOTSTRAP_TIMEOUT", "12s")
	t.Setenv("MIOT_ACCESS_TOKEN", "env-access")
	t.Setenv("MIOT_REFRESH_TOKEN", "env-refresh")

	cfg, err := LoadRuntimeExampleConfig(RuntimeExampleConfig{
		ClientID:             "default-client",
		CloudServer:          "cn",
		StorageDir:           filepath.Join(".", ".miot-cache"),
		SnapshotInterval:     30 * time.Second,
		MDNSBootstrapTimeout: 5 * time.Second,
		AccessToken:          "default-access",
		RefreshToken:         "default-refresh",
	})
	if err != nil {
		t.Fatalf("LoadRuntimeExampleConfig returned error: %v", err)
	}

	if cfg.StorageDir != "/tmp/miot-runtime-cache" {
		t.Fatalf("StorageDir = %q, want /tmp/miot-runtime-cache", cfg.StorageDir)
	}
	if cfg.ClientID != "env-client" {
		t.Fatalf("ClientID = %q, want env-client", cfg.ClientID)
	}
	if cfg.CloudServer != "de" {
		t.Fatalf("CloudServer = %q, want de", cfg.CloudServer)
	}
	if cfg.SnapshotInterval != 45*time.Second {
		t.Fatalf("SnapshotInterval = %s, want 45s", cfg.SnapshotInterval)
	}
	if cfg.MDNSBootstrapTimeout != 12*time.Second {
		t.Fatalf("MDNSBootstrapTimeout = %s, want 12s", cfg.MDNSBootstrapTimeout)
	}
	if cfg.AccessToken != "env-access" {
		t.Fatalf("AccessToken = %q, want env-access", cfg.AccessToken)
	}
	if cfg.RefreshToken != "env-refresh" {
		t.Fatalf("RefreshToken = %q, want env-refresh", cfg.RefreshToken)
	}
}

func TestLoadRuntimeExampleConfigRejectsNonPositiveSnapshotInterval(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "zero", value: "0s"},
		{name: "negative", value: "-1s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MIOT_RUNTIME_SNAPSHOT_INTERVAL", tc.value)

			_, err := LoadRuntimeExampleConfig(RuntimeExampleConfig{
				SnapshotInterval: 30 * time.Second,
			})
			if err == nil {
				t.Fatalf("LoadRuntimeExampleConfig should reject snapshot interval %q", tc.value)
			}
			if !strings.Contains(err.Error(), "MIOT_RUNTIME_SNAPSHOT_INTERVAL") {
				t.Fatalf("error = %v, want snapshot interval config error", err)
			}
			if !strings.Contains(err.Error(), "positive") {
				t.Fatalf("error = %v, want non-positive interval detail", err)
			}
		})
	}
}

func TestResolveRuntimeOAuthTokenUsesEnvironmentFirst(t *testing.T) {
	ctx := context.Background()
	storage := mustTestRuntimeStorage(t)
	bootstrap := RuntimeBootstrapState{UID: "10001"}

	storedToken := miot.OAuthToken{
		AccessToken:  "stored-access",
		RefreshToken: "stored-refresh",
		MacKey:       "stored-mac",
	}
	if err := PersistRuntimeOAuthToken(ctx, storage, bootstrap.UID, "cn", "test-client", storedToken); err != nil {
		t.Fatalf("PersistRuntimeOAuthToken returned error: %v", err)
	}

	t.Run("environment wins over stored auth", func(t *testing.T) {
		t.Setenv("MIOT_ACCESS_TOKEN", "env-access")
		t.Setenv("MIOT_REFRESH_TOKEN", "env-refresh")

		cfg, err := LoadRuntimeExampleConfig(RuntimeExampleConfig{
			ClientID:         "test-client",
			CloudServer:      "cn",
			SnapshotInterval: 30 * time.Second,
		})
		if err != nil {
			t.Fatalf("LoadRuntimeExampleConfig returned error: %v", err)
		}

		token, err := ResolveRuntimeOAuthToken(ctx, cfg, storage, bootstrap)
		if err != nil {
			t.Fatalf("ResolveRuntimeOAuthToken returned error: %v", err)
		}
		if token.AccessToken != "env-access" {
			t.Fatalf("AccessToken = %q, want env-access", token.AccessToken)
		}
		if token.RefreshToken != "env-refresh" {
			t.Fatalf("RefreshToken = %q, want env-refresh", token.RefreshToken)
		}
	})

	t.Run("partial environment token pair is rejected", func(t *testing.T) {
		t.Setenv("MIOT_ACCESS_TOKEN", "env-access")
		t.Setenv("MIOT_REFRESH_TOKEN", "")

		cfg, err := LoadRuntimeExampleConfig(RuntimeExampleConfig{
			ClientID:         "test-client",
			CloudServer:      "cn",
			SnapshotInterval: 30 * time.Second,
		})
		if err != nil {
			t.Fatalf("LoadRuntimeExampleConfig returned error: %v", err)
		}

		_, err = ResolveRuntimeOAuthToken(ctx, cfg, storage, bootstrap)
		if err == nil {
			t.Fatal("ResolveRuntimeOAuthToken should reject a partial env token pair")
		}
		if !strings.Contains(err.Error(), "partial") {
			t.Fatalf("error = %v, want partial env token pair error", err)
		}
	})
}

func TestResolveRuntimeOAuthTokenLoadsStoredAuthWhenBootstrapUIDIsPresent(t *testing.T) {
	ctx := context.Background()

	t.Run("missing bootstrap uid does not consult stored auth", func(t *testing.T) {
		storage := mustTestRuntimeStorage(t)
		if err := PersistRuntimeOAuthToken(ctx, storage, "10001", "cn", "test-client", miot.OAuthToken{
			AccessToken:  "stored-access",
			RefreshToken: "stored-refresh",
		}); err != nil {
			t.Fatalf("PersistRuntimeOAuthToken returned error: %v", err)
		}

		_, err := ResolveRuntimeOAuthToken(ctx, RuntimeExampleConfig{
			ClientID:    "test-client",
			CloudServer: "cn",
		}, storage, RuntimeBootstrapState{})
		if err == nil {
			t.Fatal("ResolveRuntimeOAuthToken should fail on first run without env tokens")
		}
		if !strings.Contains(err.Error(), "first run") {
			t.Fatalf("error = %v, want direct first-run error", err)
		}
	})

	t.Run("bootstrap uid loads stored auth_info", func(t *testing.T) {
		storage := mustTestRuntimeStorage(t)
		want := miot.OAuthToken{
			AccessToken:  "stored-access",
			RefreshToken: "stored-refresh",
			MacKey:       "stored-mac",
			ExpiresIn:    7200,
		}
		if err := PersistRuntimeOAuthToken(ctx, storage, "10001", "cn", "test-client", want); err != nil {
			t.Fatalf("PersistRuntimeOAuthToken returned error: %v", err)
		}

		got, err := ResolveRuntimeOAuthToken(ctx, RuntimeExampleConfig{
			ClientID:    "test-client",
			CloudServer: "cn",
		}, storage, RuntimeBootstrapState{UID: "10001"})
		if err != nil {
			t.Fatalf("ResolveRuntimeOAuthToken returned error: %v", err)
		}
		if got.AccessToken != want.AccessToken {
			t.Fatalf("AccessToken = %q, want %q", got.AccessToken, want.AccessToken)
		}
		if got.RefreshToken != want.RefreshToken {
			t.Fatalf("RefreshToken = %q, want %q", got.RefreshToken, want.RefreshToken)
		}
		if got.MacKey != want.MacKey {
			t.Fatalf("MacKey = %q, want %q", got.MacKey, want.MacKey)
		}
	})

	t.Run("missing stored auth returns direct first-run error", func(t *testing.T) {
		storage := mustTestRuntimeStorage(t)

		_, err := ResolveRuntimeOAuthToken(ctx, RuntimeExampleConfig{
			ClientID:    "test-client",
			CloudServer: "cn",
		}, storage, RuntimeBootstrapState{UID: "10001"})
		if err == nil {
			t.Fatal("ResolveRuntimeOAuthToken should fail when stored auth_info is missing")
		}
		if !strings.Contains(err.Error(), "first run") {
			t.Fatalf("error = %v, want direct first-run error", err)
		}
	})

	t.Run("corrupt stored auth preserves underlying error", func(t *testing.T) {
		storage := mustTestRuntimeStorage(t)
		if err := PersistRuntimeOAuthToken(ctx, storage, "10001", "cn", "test-client", miot.OAuthToken{
			AccessToken:  "stored-access",
			RefreshToken: "stored-refresh",
		}); err != nil {
			t.Fatalf("PersistRuntimeOAuthToken returned error: %v", err)
		}

		path := storage.Path("miot_config", "10001_cn", miot.StorageFormatJSON)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile returned error: %v", err)
		}
		data[len(data)-1] ^= 0xff
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}

		_, err = ResolveRuntimeOAuthToken(ctx, RuntimeExampleConfig{
			ClientID:    "test-client",
			CloudServer: "cn",
		}, storage, RuntimeBootstrapState{UID: "10001"})
		if err == nil {
			t.Fatal("ResolveRuntimeOAuthToken should fail when stored auth_info is corrupt")
		}
		if strings.Contains(err.Error(), firstRunRuntimeTokenError) {
			t.Fatalf("error = %v, want underlying stored-auth failure instead of first-run error", err)
		}

		var miotErr *miot.Error
		if !errors.As(err, &miotErr) {
			t.Fatalf("error = %T %v, want wrapped *miot.Error", err, err)
		}
		if miotErr.Code != miot.ErrInvalidResponse {
			t.Fatalf("miot error code = %q, want %q", miotErr.Code, miot.ErrInvalidResponse)
		}
	})

	t.Run("stored auth missing token fields reports invalid cached auth", func(t *testing.T) {
		storage := mustTestRuntimeStorage(t)
		if err := PersistRuntimeOAuthToken(ctx, storage, "10001", "cn", "test-client", miot.OAuthToken{
			AccessToken:  "",
			RefreshToken: "stored-refresh",
		}); err != nil {
			t.Fatalf("PersistRuntimeOAuthToken returned error: %v", err)
		}

		_, err := ResolveRuntimeOAuthToken(ctx, RuntimeExampleConfig{
			ClientID:    "test-client",
			CloudServer: "cn",
		}, storage, RuntimeBootstrapState{UID: "10001"})
		if err == nil {
			t.Fatal("ResolveRuntimeOAuthToken should reject stored auth_info with empty token fields")
		}
		if strings.Contains(err.Error(), firstRunRuntimeTokenError) {
			t.Fatalf("error = %v, want invalid stored-auth error instead of first-run error", err)
		}
		if !strings.Contains(err.Error(), "stored auth_info is invalid") {
			t.Fatalf("error = %v, want invalid stored-auth detail", err)
		}
	})
}

func TestResolveRuntimeOAuthTokenScopesStoredAuthByClientID(t *testing.T) {
	ctx := context.Background()
	storage := mustTestRuntimeStorage(t)

	want := miot.OAuthToken{
		AccessToken:  "stored-access",
		RefreshToken: "stored-refresh",
	}
	if err := PersistRuntimeOAuthToken(ctx, storage, "10001", "cn", "client-a", want); err != nil {
		t.Fatalf("PersistRuntimeOAuthToken returned error: %v", err)
	}

	got, err := ResolveRuntimeOAuthToken(ctx, RuntimeExampleConfig{
		ClientID:    "client-a",
		CloudServer: "cn",
	}, storage, RuntimeBootstrapState{UID: "10001"})
	if err != nil {
		t.Fatalf("ResolveRuntimeOAuthToken returned error: %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Fatalf("token = %#v, want %#v", got, want)
	}

	_, err = ResolveRuntimeOAuthToken(ctx, RuntimeExampleConfig{
		ClientID:    "client-b",
		CloudServer: "cn",
	}, storage, RuntimeBootstrapState{UID: "10001"})
	if err == nil {
		t.Fatal("ResolveRuntimeOAuthToken should not reuse auth_info across client ids")
	}
	if !strings.Contains(err.Error(), firstRunRuntimeTokenError) {
		t.Fatalf("error = %v, want first-run error for missing client-specific auth_info", err)
	}
}

func mustTestRuntimeStorage(t *testing.T) *miot.Storage {
	t.Helper()
	storage, err := miot.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage returned error: %v", err)
	}
	return storage
}
