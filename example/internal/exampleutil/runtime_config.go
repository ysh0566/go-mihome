package exampleutil

import (
	"context"
	"fmt"
	"strings"
	"time"

	miot "github.com/ysh0566/go-mihome"
)

// RuntimeExampleConfig stores runtime example configuration and bootstrap tokens.
type RuntimeExampleConfig struct {
	ClientID             string
	CloudServer          string
	StorageDir           string
	SnapshotInterval     time.Duration
	MDNSBootstrapTimeout time.Duration
	AccessToken          string
	RefreshToken         string
}

const firstRunRuntimeTokenError = "missing MIOT_ACCESS_TOKEN and MIOT_REFRESH_TOKEN for first run"

// LoadRuntimeExampleConfig reads runtime example config from environment with fallback defaults.
func LoadRuntimeExampleConfig(defaults RuntimeExampleConfig) (RuntimeExampleConfig, error) {
	cfg := defaults
	cfg.StorageDir = LookupString("MIOT_STORAGE_DIR", cfg.StorageDir)
	cfg.ClientID = LookupString("MIOT_CLIENT_ID", cfg.ClientID)
	cfg.CloudServer = LookupString("MIOT_CLOUD_SERVER", cfg.CloudServer)
	cfg.AccessToken = LookupString("MIOT_ACCESS_TOKEN", cfg.AccessToken)
	cfg.RefreshToken = LookupString("MIOT_REFRESH_TOKEN", cfg.RefreshToken)

	var err error
	if cfg.SnapshotInterval, err = lookupRuntimeDuration("MIOT_RUNTIME_SNAPSHOT_INTERVAL", cfg.SnapshotInterval); err != nil {
		return RuntimeExampleConfig{}, err
	}
	if cfg.MDNSBootstrapTimeout, err = lookupRuntimeDuration("MIOT_MDNS_BOOTSTRAP_TIMEOUT", cfg.MDNSBootstrapTimeout); err != nil {
		return RuntimeExampleConfig{}, err
	}
	if cfg.SnapshotInterval <= 0 {
		return RuntimeExampleConfig{}, fmt.Errorf("MIOT_RUNTIME_SNAPSHOT_INTERVAL must be positive")
	}
	return cfg, nil
}

// ResolveRuntimeOAuthToken resolves the runtime bootstrap token from environment or stored auth_info.
func ResolveRuntimeOAuthToken(ctx context.Context, cfg RuntimeExampleConfig, storage *miot.Storage, bootstrap RuntimeBootstrapState) (miot.OAuthToken, error) {
	envAccess := strings.TrimSpace(cfg.AccessToken)
	envRefresh := strings.TrimSpace(cfg.RefreshToken)

	switch {
	case envAccess != "" && envRefresh != "":
		return miot.OAuthToken{
			AccessToken:  envAccess,
			RefreshToken: envRefresh,
		}, nil
	case envAccess != "" || envRefresh != "":
		return miot.OAuthToken{}, fmt.Errorf("partial MIOT_ACCESS_TOKEN/MIOT_REFRESH_TOKEN env pair: both tokens are required together")
	case bootstrap.UID == "":
		return miot.OAuthToken{}, fmt.Errorf(firstRunRuntimeTokenError)
	case storage == nil:
		return miot.OAuthToken{}, fmt.Errorf("load stored runtime auth_info: storage is nil")
	}

	key := runtimeAuthInfoKey(cfg.ClientID)
	doc, err := storage.LoadUserConfig(ctx, bootstrap.UID, cfg.CloudServer, key)
	if err != nil {
		return miot.OAuthToken{}, fmt.Errorf("load stored runtime auth_info: %w", err)
	}

	entry, ok := runtimeAuthInfoEntry(doc, key)
	if !ok {
		return miot.OAuthToken{}, fmt.Errorf(firstRunRuntimeTokenError)
	}

	token, err := miot.DecodeUserConfigEntry[miot.OAuthToken](entry)
	if err != nil {
		return miot.OAuthToken{}, fmt.Errorf("decode stored runtime auth_info: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" {
		return miot.OAuthToken{}, fmt.Errorf("stored auth_info is invalid: access token and refresh token are required")
	}
	return token, nil
}

// PersistRuntimeOAuthToken saves the runtime OAuth token under the app-specific auth_info key.
func PersistRuntimeOAuthToken(ctx context.Context, storage *miot.Storage, uid, cloudServer, clientID string, token miot.OAuthToken) error {
	return NewRuntimeOAuthTokenStore(storage, uid, cloudServer, clientID).SaveOAuthToken(ctx, token)
}

func lookupRuntimeDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(LookupString(key, ""))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return parsed, nil
}

func runtimeAuthInfoEntry(doc miot.UserConfigDocument, key string) (miot.UserConfigEntry, bool) {
	for _, entry := range doc.Entries {
		if entry.Key == key {
			return entry, true
		}
	}
	return miot.UserConfigEntry{}, false
}
