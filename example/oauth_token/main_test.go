package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

func TestLoadOAuthTokenConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := loadOAuthTokenConfig(oauthTokenConfig{
		ClientID:    defaultClientID,
		CloudServer: defaultCloudServer,
		RedirectURL: defaultRedirectURL,
		UUID:        defaultOAuthUUID,
		StorageDir:  defaultStorageDir,
	})
	if err != nil {
		t.Fatalf("loadOAuthTokenConfig returned error: %v", err)
	}

	if cfg.ClientID != "2882303761520251711" {
		t.Fatalf("ClientID = %q, want 2882303761520251711", cfg.ClientID)
	}
	if cfg.CloudServer != "cn" {
		t.Fatalf("CloudServer = %q, want cn", cfg.CloudServer)
	}
	if cfg.RedirectURL != defaultRedirectURL {
		t.Fatalf("RedirectURL = %q, want %s", cfg.RedirectURL, defaultRedirectURL)
	}
	if cfg.UUID != "" {
		t.Fatalf("UUID = %q, want empty default", cfg.UUID)
	}
	if cfg.StorageDir != defaultStorageDir {
		t.Fatalf("StorageDir = %q, want %q", cfg.StorageDir, defaultStorageDir)
	}
	if cfg.Code != "" {
		t.Fatalf("Code = %q, want empty", cfg.Code)
	}
}

func TestLoadOAuthTokenConfigPrefersEnvironment(t *testing.T) {
	t.Setenv("MIOT_CLIENT_ID", "env-client")
	t.Setenv("MIOT_CLOUD_SERVER", "us")
	t.Setenv("MIOT_OAUTH_REDIRECT_URL", "http://127.0.0.1:8123/callback")
	t.Setenv("MIOT_OAUTH_UUID", "env-uuid")
	t.Setenv("MIOT_OAUTH_CODE", "env-code")

	cfg, err := loadOAuthTokenConfig(oauthTokenConfig{
		ClientID:    defaultClientID,
		CloudServer: defaultCloudServer,
		RedirectURL: defaultRedirectURL,
		UUID:        defaultOAuthUUID,
		StorageDir:  defaultStorageDir,
	})
	if err != nil {
		t.Fatalf("loadOAuthTokenConfig returned error: %v", err)
	}

	if cfg.ClientID != "env-client" {
		t.Fatalf("ClientID = %q, want env-client", cfg.ClientID)
	}
	if cfg.CloudServer != "us" {
		t.Fatalf("CloudServer = %q, want us", cfg.CloudServer)
	}
	if cfg.RedirectURL != "http://127.0.0.1:8123/callback" {
		t.Fatalf("RedirectURL = %q, want env override", cfg.RedirectURL)
	}
	if cfg.UUID != "env-uuid" {
		t.Fatalf("UUID = %q, want env-uuid", cfg.UUID)
	}
	if cfg.StorageDir != defaultStorageDir {
		t.Fatalf("StorageDir = %q, want default storage dir", cfg.StorageDir)
	}
	if cfg.Code != "env-code" {
		t.Fatalf("Code = %q, want env-code", cfg.Code)
	}
}

func TestRunOAuthTokenEmitsAuthURLWhenCodeMissing(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	factoryCalls := 0
	err := runOAuthToken(context.Background(), &buf, oauthTokenConfig{
		ClientID:    "2882303761520251711",
		CloudServer: "cn",
		RedirectURL: defaultRedirectURL,
		UUID:        "go-mihome-oauth",
	}, func(cfg oauthTokenConfig) (oauthTokenClient, error) {
		factoryCalls++
		if cfg.ClientID != "2882303761520251711" || cfg.RedirectURL != defaultRedirectURL || cfg.UUID != "go-mihome-oauth" {
			t.Fatalf("factory cfg = %#v", cfg)
		}
		return stubOAuthTokenClient{
			authURL: "https://account.xiaomi.com/oauth2/authorize?client_id=2882303761520251711",
		}, nil
	})
	if err != nil {
		t.Fatalf("runOAuthToken returned error: %v", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("factoryCalls = %d, want 1", factoryCalls)
	}

	var out authURLEvent
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if out.Type != "auth_url" {
		t.Fatalf("Type = %q, want auth_url", out.Type)
	}
	if out.AuthURL == "" {
		t.Fatal("AuthURL is empty")
	}
	if out.ClientID != "2882303761520251711" {
		t.Fatalf("ClientID = %q, want 2882303761520251711", out.ClientID)
	}
	if out.RedirectURL != defaultRedirectURL {
		t.Fatalf("RedirectURL = %q", out.RedirectURL)
	}
}

func TestRunOAuthTokenExchangesCodeWhenProvided(t *testing.T) {
	t.Parallel()

	wantToken := miot.OAuthToken{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresIn:    3600,
		ExpiresAt:    time.Unix(1_700_000_000, 0),
	}

	var buf bytes.Buffer
	err := runOAuthToken(context.Background(), &buf, oauthTokenConfig{
		ClientID:    "2882303761520251711",
		CloudServer: "cn",
		RedirectURL: defaultRedirectURL,
		UUID:        "go-mihome-oauth",
		Code:        "oauth-code",
	}, func(cfg oauthTokenConfig) (oauthTokenClient, error) {
		return stubOAuthTokenClient{
			exchangeCode: func(ctx context.Context, code string) (miot.OAuthToken, error) {
				if code != "oauth-code" {
					t.Fatalf("code = %q, want oauth-code", code)
				}
				return wantToken, nil
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("runOAuthToken returned error: %v", err)
	}

	var out tokenEvent
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if out.Type != "token" {
		t.Fatalf("Type = %q, want token", out.Type)
	}
	if out.Token != wantToken {
		t.Fatalf("Token = %#v, want %#v", out.Token, wantToken)
	}
}

func TestRunOAuthTokenPropagatesExchangeError(t *testing.T) {
	t.Parallel()

	err := runOAuthToken(context.Background(), &bytes.Buffer{}, oauthTokenConfig{
		ClientID:    "2882303761520251711",
		CloudServer: "cn",
		RedirectURL: defaultRedirectURL,
		UUID:        "go-mihome-oauth",
		Code:        "oauth-code",
	}, func(oauthTokenConfig) (oauthTokenClient, error) {
		return stubOAuthTokenClient{
			exchangeCode: func(context.Context, string) (miot.OAuthToken, error) {
				return miot.OAuthToken{}, errors.New("exchange failed")
			},
		}, nil
	})
	if err == nil {
		t.Fatal("runOAuthToken returned nil error, want exchange failure")
	}
}

func TestLoadOAuthTokenConfigExtractsCodeFromRedirectURL(t *testing.T) {
	t.Setenv("MIOT_OAUTH_CODE", "http://homeassistant.local:8123/?code=oauth-code&state=abc")

	cfg, err := loadOAuthTokenConfig(oauthTokenConfig{
		ClientID:    defaultClientID,
		CloudServer: defaultCloudServer,
		RedirectURL: defaultRedirectURL,
		UUID:        defaultOAuthUUID,
		StorageDir:  defaultStorageDir,
	})
	if err != nil {
		t.Fatalf("loadOAuthTokenConfig returned error: %v", err)
	}
	if cfg.Code != "oauth-code" {
		t.Fatalf("Code = %q, want oauth-code", cfg.Code)
	}
}

func TestResolveOAuthTokenConfigUsesRuntimeBootstrapUUID(t *testing.T) {
	t.Parallel()

	storage, err := miot.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage returned error: %v", err)
	}
	if err := exampleutil.SaveRuntimeBootstrapState(context.Background(), storage, exampleutil.RuntimeBootstrapState{
		CloudMIPSUUID: "0123456789abcdef0123456789abcdef",
	}); err != nil {
		t.Fatalf("SaveRuntimeBootstrapState returned error: %v", err)
	}

	cfg, err := resolveOAuthTokenConfig(context.Background(), oauthTokenConfig{
		ClientID:    defaultClientID,
		CloudServer: defaultCloudServer,
		RedirectURL: defaultRedirectURL,
		StorageDir:  storage.RootPath(),
	})
	if err != nil {
		t.Fatalf("resolveOAuthTokenConfig returned error: %v", err)
	}
	if cfg.UUID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("UUID = %q, want bootstrap cloud uuid", cfg.UUID)
	}
}

func TestResolveOAuthTokenConfigMigratesLegacyRuntimeUUID(t *testing.T) {
	t.Parallel()

	storage, err := miot.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage returned error: %v", err)
	}
	if err := exampleutil.SaveRuntimeBootstrapState(context.Background(), storage, exampleutil.RuntimeBootstrapState{
		UID:           "10001",
		CloudMIPSUUID: "go-mihome-runtime-75a6779fd702b23d",
		RuntimeDID:    "runtime-did",
	}); err != nil {
		t.Fatalf("SaveRuntimeBootstrapState returned error: %v", err)
	}

	cfg, err := resolveOAuthTokenConfig(context.Background(), oauthTokenConfig{
		ClientID:    defaultClientID,
		CloudServer: defaultCloudServer,
		RedirectURL: defaultRedirectURL,
		StorageDir:  storage.RootPath(),
	})
	if err != nil {
		t.Fatalf("resolveOAuthTokenConfig returned error: %v", err)
	}
	if cfg.UUID == "go-mihome-runtime-75a6779fd702b23d" {
		t.Fatal("resolveOAuthTokenConfig kept legacy runtime uuid unchanged")
	}
	if len(cfg.UUID) != 32 {
		t.Fatalf("UUID = %q, want 32-char hex", cfg.UUID)
	}

	bootstrap, err := exampleutil.LoadRuntimeBootstrapState(context.Background(), storage)
	if err != nil {
		t.Fatalf("LoadRuntimeBootstrapState returned error: %v", err)
	}
	if bootstrap.CloudMIPSUUID != cfg.UUID {
		t.Fatalf("bootstrap uuid = %q, want migrated uuid %q", bootstrap.CloudMIPSUUID, cfg.UUID)
	}
	if bootstrap.UID != "10001" || bootstrap.RuntimeDID != "runtime-did" {
		t.Fatalf("bootstrap = %#v, want other fields preserved", bootstrap)
	}
}

type stubOAuthTokenClient struct {
	authURL      string
	exchangeCode func(context.Context, string) (miot.OAuthToken, error)
}

func (s stubOAuthTokenClient) AuthURL(*miot.AuthURLOptions) string {
	return s.authURL
}

func (s stubOAuthTokenClient) ExchangeCode(ctx context.Context, code string) (miot.OAuthToken, error) {
	if s.exchangeCode != nil {
		return s.exchangeCode(ctx, code)
	}
	return miot.OAuthToken{}, nil
}
