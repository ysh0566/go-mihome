package miot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
)

func TestDecodeGetPropsResponse(t *testing.T) {
	var resp GetPropsResponse
	mustLoadJSONFixture(t, "testdata/cloud/get_props.json", &resp)
	if len(resp.Result) != 1 {
		t.Fatalf("len(result) = %d", len(resp.Result))
	}
	if resp.Result[0].DID != "demo-prop-device-1" {
		t.Fatalf("did = %q", resp.Result[0].DID)
	}
	value, ok := resp.Result[0].Value.Bool()
	if !ok || !value {
		t.Fatalf("value = %#v", resp.Result[0].Value)
	}
}

func TestCloudClientGetPropUsesTypedResult(t *testing.T) {
	client, _ := newFixtureCloudClient(t, fixtureResponse{
		path:    "/app/v2/miotspec/prop/get",
		fixture: "testdata/cloud/get_props.json",
	})

	got, err := client.GetProp(context.Background(), PropertyQuery{
		DID:  "demo-prop-device-1",
		SIID: 2,
		PIID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DID != "demo-prop-device-1" || got.SIID != 2 || got.PIID != 1 {
		t.Fatalf("result = %#v", got)
	}
	value, ok := got.Value.Bool()
	if !ok || !value {
		t.Fatalf("value = %#v", got.Value)
	}
}

func TestCloudClientSetPropsUsesTypedRequest(t *testing.T) {
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/app/v2/miotspec/prop/set" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		var body SetPropsRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Params) != 1 {
			t.Fatalf("params = %#v", body.Params)
		}
		value, ok := body.Params[0].Value.Bool()
		if !ok || !value {
			t.Fatalf("request value = %#v", body.Params[0].Value)
		}
		return jsonResponse(`{"code":0,"message":"ok","result":[{"did":"demo-prop-device-1","siid":2,"piid":1,"code":0}]}`), nil
	})

	client, err := NewCloudClient(
		CloudConfig{ClientID: "2882303761520431603", CloudServer: "cn"},
		WithCloudHTTPClient(doer),
		WithCloudTokenProvider(staticTokenProvider{token: "access-token"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	results, err := client.SetProps(context.Background(), SetPropsRequest{
		Params: []SetPropertyRequest{{
			DID:   "demo-prop-device-1",
			SIID:  2,
			PIID:  1,
			Value: NewSpecValueBool(true),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Code != 0 {
		t.Fatalf("results = %#v", results)
	}
}

func TestCloudClientInvokeActionUsesTypedRequest(t *testing.T) {
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/app/v2/miotspec/action" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		var body struct {
			Params ActionRequest `json:"params"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Params.DID != "demo-prop-device-1" || body.Params.AIID != 1 {
			t.Fatalf("params = %#v", body.Params)
		}
		if len(body.Params.Input) != 1 {
			t.Fatalf("input = %#v", body.Params.Input)
		}
		value, ok := body.Params.Input[0].Bool()
		if !ok || !value {
			t.Fatalf("action input = %#v", body.Params.Input[0])
		}
		return jsonResponse(`{"code":0,"message":"ok","result":{"code":0,"out":[1]}}`), nil
	})

	client, err := NewCloudClient(
		CloudConfig{ClientID: "2882303761520431603", CloudServer: "cn"},
		WithCloudHTTPClient(doer),
		WithCloudTokenProvider(staticTokenProvider{token: "access-token"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.InvokeAction(context.Background(), ActionRequest{
		DID:   "demo-prop-device-1",
		SIID:  2,
		AIID:  1,
		Input: []SpecValue{NewSpecValueBool(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 {
		t.Fatalf("result code = %d", result.Code)
	}
	if len(result.Output) != 1 {
		t.Fatalf("result = %#v", result)
	}
	value, ok := result.Output[0].Int()
	if !ok || value != 1 {
		t.Fatalf("output = %#v", result.Output[0])
	}
}

func TestCloudClientGetUserInfoUsesOpenAccountProfile(t *testing.T) {
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "open.account.xiaomi.com" {
			t.Fatalf("host = %q", req.URL.Host)
		}
		if req.URL.Path != "/user/profile" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		query := req.URL.Query()
		if query.Get("clientId") != "2882303761520431603" {
			t.Fatalf("clientId = %q", query.Get("clientId"))
		}
		if query.Get("token") != "access-token" {
			t.Fatalf("token = %q", query.Get("token"))
		}
		return jsonResponse(`{"code":0,"result":"ok","description":"no error","traceId":"trace","data":{"unionId":"union","miliaoNick":"nick"}}`), nil
	})

	client, err := NewCloudClient(
		CloudConfig{ClientID: "2882303761520431603", CloudServer: "cn"},
		WithCloudHTTPClient(doer),
		WithCloudTokenProvider(staticTokenProvider{token: "access-token"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	got, err := client.GetUserInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.MiliaoNick != "nick" {
		t.Fatalf("nick = %q", got.MiliaoNick)
	}
}

func TestCloudClientUpdateAuthOverridesHeadersAndHost(t *testing.T) {
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "de.ha.api.io.mi.com" {
			t.Fatalf("host = %q", req.URL.Host)
		}
		if got := req.Header.Get("Authorization"); got != "Beareroverride-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := req.Header.Get("X-Client-AppId"); got != "new-client-id" {
			t.Fatalf("client app id = %q", got)
		}
		return jsonResponse(`{"code":0,"message":"ok","result":[{"did":"demo-prop-device-1","siid":2,"piid":1,"value":true,"code":0}]}`), nil
	})

	client, err := NewCloudClient(
		CloudConfig{ClientID: "old-client-id", CloudServer: "cn"},
		WithCloudHTTPClient(doer),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateAuth("de", "new-client-id", "override-token"); err != nil {
		t.Fatal(err)
	}

	_, err = client.GetProp(context.Background(), PropertyQuery{
		DID:  "demo-prop-device-1",
		SIID: 2,
		PIID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCloudClientGetCentralCertUsesTypedRequest(t *testing.T) {
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/app/v2/ha/oauth/get_central_crt" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if got := req.Header.Get("Authorization"); got != "Beareraccess-token" {
			t.Fatalf("authorization = %q", got)
		}
		var body struct {
			CSR string `json:"csr"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		want := base64.StdEncoding.EncodeToString([]byte("csr-body"))
		if body.CSR != want {
			t.Fatalf("csr = %q, want %q", body.CSR, want)
		}
		return jsonResponse(`{"code":0,"message":"ok","result":{"cert":"signed-cert"}}`), nil
	})

	client, err := NewCloudClient(
		CloudConfig{ClientID: "2882303761520431603", CloudServer: "cn"},
		WithCloudHTTPClient(doer),
		WithCloudTokenProvider(staticTokenProvider{token: "access-token"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	cert, err := client.GetCentralCert(context.Background(), "csr-body")
	if err != nil {
		t.Fatal(err)
	}
	if cert != "signed-cert" {
		t.Fatalf("cert = %q", cert)
	}
}
