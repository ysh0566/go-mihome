package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strings"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

const (
	defaultClientID    = "2882303761520251711"
	defaultCloudServer = "cn"
	defaultRedirectURL = "https://mico.api.mijia.tech/login_redirect"
	defaultOAuthUUID   = ""
	defaultStorageDir  = ".miot-example-cache"
)

type oauthTokenConfig struct {
	ClientID    string
	CloudServer string
	RedirectURL string
	UUID        string
	StorageDir  string
	Code        string
}

type oauthTokenClient interface {
	AuthURL(options *miot.AuthURLOptions) string
	ExchangeCode(ctx context.Context, code string) (miot.OAuthToken, error)
}

type authURLEvent struct {
	Type             string `json:"type"`
	ClientID         string `json:"client_id"`
	CloudServer      string `json:"cloud_server"`
	RedirectURL      string `json:"redirect_url"`
	UUID             string `json:"uuid"`
	AuthorizationURL string `json:"authorization_url"`
	AuthURL          string `json:"auth_url"`
}

type tokenEvent struct {
	Type        string          `json:"type"`
	ClientID    string          `json:"client_id"`
	CloudServer string          `json:"cloud_server"`
	RedirectURL string          `json:"redirect_url"`
	UUID        string          `json:"uuid"`
	Token       miot.OAuthToken `json:"token"`
}

func main() {
	log.SetFlags(0)

	cfg, err := loadOAuthTokenConfig(oauthTokenConfig{
		ClientID:    defaultClientID,
		CloudServer: defaultCloudServer,
		RedirectURL: defaultRedirectURL,
		UUID:        defaultOAuthUUID,
		StorageDir:  defaultStorageDir,
	})
	if err != nil {
		log.Fatal(err)
	}
	cfg, err = resolveOAuthTokenConfig(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}

	if err := runOAuthToken(context.Background(), os.Stdout, cfg, newOAuthTokenClient); err != nil {
		log.Fatal(err)
	}
}

func loadOAuthTokenConfig(defaults oauthTokenConfig) (oauthTokenConfig, error) {
	cfg := defaults
	cfg.ClientID = exampleutil.LookupString("MIOT_CLIENT_ID", cfg.ClientID)
	cfg.CloudServer = exampleutil.LookupString("MIOT_CLOUD_SERVER", cfg.CloudServer)
	cfg.RedirectURL = exampleutil.LookupString("MIOT_OAUTH_REDIRECT_URL", cfg.RedirectURL)
	cfg.UUID = exampleutil.LookupString("MIOT_OAUTH_UUID", cfg.UUID)
	cfg.StorageDir = exampleutil.LookupString("MIOT_STORAGE_DIR", cfg.StorageDir)
	cfg.Code = exampleutil.LookupString("MIOT_OAUTH_CODE", cfg.Code)

	if strings.TrimSpace(cfg.ClientID) == "" {
		return oauthTokenConfig{}, fmt.Errorf("missing MIOT_CLIENT_ID or default client id")
	}
	if strings.TrimSpace(cfg.CloudServer) == "" {
		return oauthTokenConfig{}, fmt.Errorf("missing MIOT_CLOUD_SERVER or default cloud server")
	}
	if strings.TrimSpace(cfg.RedirectURL) == "" {
		return oauthTokenConfig{}, fmt.Errorf("missing MIOT_OAUTH_REDIRECT_URL or default redirect url")
	}
	if strings.TrimSpace(cfg.UUID) == "" {
		cfg.UUID = ""
	}
	if strings.TrimSpace(cfg.StorageDir) == "" {
		return oauthTokenConfig{}, fmt.Errorf("missing MIOT_STORAGE_DIR or default storage dir")
	}
	cfg.Code = normalizeOAuthCode(cfg.Code)
	return cfg, nil
}

func resolveOAuthTokenConfig(ctx context.Context, cfg oauthTokenConfig) (oauthTokenConfig, error) {
	if strings.TrimSpace(cfg.UUID) != "" {
		return cfg, nil
	}
	storage, err := miot.NewStorage(cfg.StorageDir)
	if err != nil {
		return oauthTokenConfig{}, fmt.Errorf("create oauth token storage: %w", err)
	}
	bootstrap, err := exampleutil.LoadRuntimeBootstrapState(ctx, storage)
	if err != nil {
		return oauthTokenConfig{}, fmt.Errorf("load runtime bootstrap state: %w", err)
	}
	uuid, changed, err := exampleutil.NormalizeRuntimeCloudMIPSUUID(bootstrap.CloudMIPSUUID)
	if err != nil {
		return oauthTokenConfig{}, fmt.Errorf("resolve runtime cloud uuid: %w", err)
	}
	cfg.UUID = uuid
	if changed || bootstrap.CloudMIPSUUID != uuid {
		bootstrap.CloudMIPSUUID = uuid
		if err := exampleutil.SaveRuntimeBootstrapState(ctx, storage, bootstrap); err != nil {
			return oauthTokenConfig{}, fmt.Errorf("save runtime bootstrap state: %w", err)
		}
	}
	return cfg, nil
}

func runOAuthToken(ctx context.Context, w io.Writer, cfg oauthTokenConfig, factory func(oauthTokenConfig) (oauthTokenClient, error)) error {
	if factory == nil {
		factory = newOAuthTokenClient
	}
	client, err := factory(cfg)
	if err != nil {
		return err
	}

	if cfg.Code == "" {
		authURL := client.AuthURL(nil)
		return exampleutil.PrintJSON(w, authURLEvent{
			Type:             "auth_url",
			ClientID:         cfg.ClientID,
			CloudServer:      cfg.CloudServer,
			RedirectURL:      cfg.RedirectURL,
			UUID:             cfg.UUID,
			AuthorizationURL: authURL,
			AuthURL:          authURL,
		})
	}

	token, err := client.ExchangeCode(ctx, cfg.Code)
	if err != nil {
		return err
	}
	return exampleutil.PrintJSON(w, tokenEvent{
		Type:        "token",
		ClientID:    cfg.ClientID,
		CloudServer: cfg.CloudServer,
		RedirectURL: cfg.RedirectURL,
		UUID:        cfg.UUID,
		Token:       token,
	})
}

func newOAuthTokenClient(cfg oauthTokenConfig) (oauthTokenClient, error) {
	return miot.NewOAuthClient(miot.OAuthConfig{
		ClientID:    cfg.ClientID,
		RedirectURL: cfg.RedirectURL,
		CloudServer: cfg.CloudServer,
		UUID:        cfg.UUID,
	})
}

func normalizeOAuthCode(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	code := strings.TrimSpace(parsed.Query().Get("code"))
	if code == "" {
		return raw
	}
	return code
}
