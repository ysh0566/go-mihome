package miot

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	oauthAuthorizeURL = "https://account.xiaomi.com/oauth2/authorize"
	defaultOAuthHost  = "ha.api.io.mi.com"
	tokenExpiryRatio  = 0.7
)

// OAuthClientOption configures an OAuthClient.
type OAuthClientOption func(*OAuthClient)

// OAuthConfig configures the MIoT OAuth flow.
type OAuthConfig struct {
	ClientID    string
	RedirectURL string
	CloudServer string
	UUID        string
}

// AuthURLOptions overrides optional fields in the authorization URL.
type AuthURLOptions struct {
	RedirectURL string
	State       string
	Scopes      []string
	SkipConfirm bool
}

// OAuthClient exchanges Xiaomi OAuth codes and refresh tokens.
type OAuthClient struct {
	http     HTTPDoer
	clock    Clock
	cfg      OAuthConfig
	host     string
	deviceID string
	state    string
}

type oauthTokenRequest struct {
	ClientID     string `json:"client_id"`
	RedirectURI  string `json:"redirect_uri"`
	Code         string `json:"code,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	DeviceID     string `json:"device_id,omitempty"`
}

// WithOAuthHTTPClient injects a custom HTTP transport into OAuthClient.
func WithOAuthHTTPClient(client HTTPDoer) OAuthClientOption {
	return func(c *OAuthClient) {
		if client != nil {
			c.http = client
		}
	}
}

// WithOAuthClock injects a test clock into OAuthClient.
func WithOAuthClock(clock Clock) OAuthClientOption {
	return func(c *OAuthClient) {
		if clock != nil {
			c.clock = clock
		}
	}
}

// NewOAuthClient creates a Xiaomi OAuth client.
func NewOAuthClient(cfg OAuthConfig, opts ...OAuthClientOption) (*OAuthClient, error) {
	if cfg.ClientID == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new oauth client", Msg: "client id is empty"}
	}
	if cfg.RedirectURL == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new oauth client", Msg: "redirect url is empty"}
	}
	if cfg.CloudServer == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new oauth client", Msg: "cloud server is empty"}
	}
	if cfg.UUID == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new oauth client", Msg: "uuid is empty"}
	}

	deviceID := "ha." + cfg.UUID
	host := defaultOAuthHost
	if cfg.CloudServer != "cn" {
		host = cfg.CloudServer + "." + defaultOAuthHost
	}
	sum := sha1.Sum([]byte("d=" + deviceID))
	client := &OAuthClient{
		http:     &http.Client{},
		clock:    realClock{},
		cfg:      cfg,
		host:     host,
		deviceID: deviceID,
		state:    hex.EncodeToString(sum[:]),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	return client, nil
}

// Close releases any held resources.
func (c *OAuthClient) Close() error {
	return nil
}

// SetRedirectURL updates the redirect URL used by the authorization flow.
func (c *OAuthClient) SetRedirectURL(redirectURL string) error {
	if redirectURL == "" {
		return &Error{Code: ErrInvalidArgument, Op: "set redirect url", Msg: "redirect url is empty"}
	}
	c.cfg.RedirectURL = redirectURL
	return nil
}

// AuthURL builds the Xiaomi OAuth authorization URL.
func (c *OAuthClient) AuthURL(options *AuthURLOptions) string {
	values := url.Values{}
	values.Set("redirect_uri", c.cfg.RedirectURL)
	values.Set("client_id", c.cfg.ClientID)
	values.Set("response_type", "code")
	values.Set("device_id", c.deviceID)
	values.Set("state", c.state)
	values.Set("skip_confirm", "false")
	if options != nil {
		if options.RedirectURL != "" {
			values.Set("redirect_uri", options.RedirectURL)
		}
		if options.State != "" {
			values.Set("state", options.State)
		}
		if len(options.Scopes) > 0 {
			values.Set("scope", strings.Join(options.Scopes, " "))
		}
		if options.SkipConfirm {
			values.Set("skip_confirm", "true")
		}
	}
	return oauthAuthorizeURL + "?" + values.Encode()
}

// ExchangeCode exchanges an authorization code for an OAuth token.
func (c *OAuthClient) ExchangeCode(ctx context.Context, code string) (OAuthToken, error) {
	if code == "" {
		return OAuthToken{}, &Error{Code: ErrInvalidArgument, Op: "exchange code", Msg: "code is empty"}
	}
	return c.requestToken(ctx, oauthTokenRequest{
		ClientID:    c.cfg.ClientID,
		RedirectURI: c.cfg.RedirectURL,
		Code:        code,
		DeviceID:    c.deviceID,
	})
}

// RefreshToken exchanges a refresh token for a fresh OAuth token.
func (c *OAuthClient) RefreshToken(ctx context.Context, refreshToken string) (OAuthToken, error) {
	if refreshToken == "" {
		return OAuthToken{}, &Error{Code: ErrInvalidArgument, Op: "refresh token", Msg: "refresh token is empty"}
	}
	return c.requestToken(ctx, oauthTokenRequest{
		ClientID:     c.cfg.ClientID,
		RedirectURI:  c.cfg.RedirectURL,
		RefreshToken: refreshToken,
	})
}

func (c *OAuthClient) requestToken(ctx context.Context, payload oauthTokenRequest) (OAuthToken, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return OAuthToken{}, Wrap(ErrInvalidArgument, "marshal oauth request", err)
	}
	reqURL := "https://" + c.host + "/app/v2/ha/oauth/get_token"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return OAuthToken{}, err
	}
	query := req.URL.Query()
	query.Set("data", string(data))
	req.URL.RawQuery = query.Encode()
	req.Header.Set("content-type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return OAuthToken{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return OAuthToken{}, fmt.Errorf("oauth token request failed: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return OAuthToken{}, err
	}
	var tokenResp OAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return OAuthToken{}, Wrap(ErrInvalidResponse, "decode oauth token response", err)
	}
	if tokenResp.Code != 0 {
		return OAuthToken{}, fmt.Errorf("oauth token response code %d, text: %s", tokenResp.Code, tokenResp.Description)
	}
	if tokenResp.Result.AccessToken == "" || tokenResp.Result.RefreshToken == "" || tokenResp.Result.ExpiresIn <= 0 {
		return OAuthToken{}, &Error{Code: ErrInvalidResponse, Op: "validate oauth token response", Msg: "missing token fields"}
	}

	validFor := int(float64(tokenResp.Result.ExpiresIn) * tokenExpiryRatio)
	return OAuthToken{
		AccessToken:  tokenResp.Result.AccessToken,
		RefreshToken: tokenResp.Result.RefreshToken,
		MacKey:       tokenResp.Result.MacKey,
		ExpiresIn:    tokenResp.Result.ExpiresIn,
		ExpiresAt:    c.clock.Now().Add(time.Duration(validFor) * time.Second),
	}, nil
}
