package miot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDecodeAccountProfileResponse(t *testing.T) {
	var resp AccountProfileResponse
	mustLoadJSONFixture(t, "testdata/cloud/account_profile.json", &resp)
	if resp.Data.MiliaoNick != "example-user" {
		t.Fatalf("nick = %q", resp.Data.MiliaoNick)
	}
	if resp.Data.UnionID == "" {
		t.Fatal("expected union id")
	}
}

func TestOAuthClientAuthURL(t *testing.T) {
	client, err := NewOAuthClient(OAuthConfig{
		ClientID:    "2882303761520431603",
		RedirectURL: "http://homeassistant.local:8123",
		CloudServer: "cn",
		UUID:        "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}

	rawURL := client.AuthURL(nil)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("client_id") != "2882303761520431603" {
		t.Fatalf("client_id = %q", query.Get("client_id"))
	}
	if query.Get("device_id") != "ha.abc123" {
		t.Fatalf("device_id = %q", query.Get("device_id"))
	}
	if query.Get("redirect_uri") != "http://homeassistant.local:8123" {
		t.Fatalf("redirect_uri = %q", query.Get("redirect_uri"))
	}
}

func TestOAuthClientExchangeCode(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", req.Method)
		}
		if req.URL.Host != "ha.api.io.mi.com" {
			t.Fatalf("host = %q", req.URL.Host)
		}
		if req.URL.Path != "/app/v2/ha/oauth/get_token" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		dataParam := req.URL.Query().Get("data")
		if !strings.Contains(dataParam, "\"code\":\"oauth-code\"") {
			t.Fatalf("data param = %q", dataParam)
		}
		return jsonResponse(`{"code":0,"result":{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600,"mac_key":"mac-key"}}`), nil
	})

	client, err := NewOAuthClient(
		OAuthConfig{
			ClientID:    "2882303761520431603",
			RedirectURL: "http://homeassistant.local:8123",
			CloudServer: "cn",
			UUID:        "abc123",
		},
		WithOAuthHTTPClient(doer),
		WithOAuthClock(fixedClock{now: now}),
	)
	if err != nil {
		t.Fatal(err)
	}

	token, err := client.ExchangeCode(context.Background(), "oauth-code")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access-token" || token.RefreshToken != "refresh-token" || token.MacKey != "mac-key" {
		t.Fatalf("token = %#v", token)
	}
	if !token.ExpiresAt.Equal(now.Add(2520 * time.Second)) {
		t.Fatalf("ExpiresAt = %s", token.ExpiresAt)
	}
}

func TestOAuthClientRefreshToken(t *testing.T) {
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		dataParam := req.URL.Query().Get("data")
		if !strings.Contains(dataParam, "\"refresh_token\":\"refresh-token\"") {
			t.Fatalf("data param = %q", dataParam)
		}
		return jsonResponse(`{"code":0,"result":{"access_token":"access-token-2","refresh_token":"refresh-token-2","expires_in":7200}}`), nil
	})

	client, err := NewOAuthClient(
		OAuthConfig{
			ClientID:    "2882303761520431603",
			RedirectURL: "http://homeassistant.local:8123",
			CloudServer: "cn",
			UUID:        "abc123",
		},
		WithOAuthHTTPClient(doer),
		WithOAuthClock(fixedClock{now: time.Unix(1_700_000_000, 0)}),
	)
	if err != nil {
		t.Fatal(err)
	}

	token, err := client.RefreshToken(context.Background(), "refresh-token")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access-token-2" || token.RefreshToken != "refresh-token-2" {
		t.Fatalf("token = %#v", token)
	}
}

func mustLoadJSONFixture(t *testing.T, relPath string, out any) {
	t.Helper()

	data, err := os.ReadFile(filepath.Clean(relPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (fn roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       ioNopCloser{reader: strings.NewReader(body)},
	}
}

type ioNopCloser struct {
	reader *strings.Reader
}

func (c ioNopCloser) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c ioNopCloser) Close() error {
	return nil
}
