package miot

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestCloudClientRunSceneUsesTypedRequest(t *testing.T) {
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/app/appgateway/miot/appsceneservice/AppSceneService/NewRunScene" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		var body RunSceneRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.SceneID != "scene-1" || body.OwnerUID != "100000001" || body.SceneType != 2 {
			t.Fatalf("body = %#v", body)
		}
		return jsonResponse(`{"code":0,"message":"ok","result":true}`), nil
	})

	client, err := NewCloudClient(
		CloudConfig{ClientID: "2882303761520431603", CloudServer: "cn"},
		WithCloudHTTPClient(doer),
		WithCloudTokenProvider(staticTokenProvider{token: "access-token"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RunScene(context.Background(), SceneRunRequest{SceneID: "scene-1", OwnerUID: "100000001", HomeID: "home-1"}); err != nil {
		t.Fatal(err)
	}
}

func TestCloudClientRunSceneRejectsUnacceptedScene(t *testing.T) {
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(`{"code":0,"message":"ok","result":false}`), nil
	})

	client, err := NewCloudClient(
		CloudConfig{ClientID: "2882303761520431603", CloudServer: "cn"},
		WithCloudHTTPClient(doer),
		WithCloudTokenProvider(staticTokenProvider{token: "access-token"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = client.RunScene(context.Background(), SceneRunRequest{SceneID: "scene-1", OwnerUID: "100000001"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := err.Error(), "scene scene-1 was not accepted"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestCloudClientGetScenesUsesTypedRequest(t *testing.T) {
	call := 0
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		call++
		switch call {
		case 1:
			if req.URL.Path != "/app/v2/homeroom/gethome" {
				t.Fatalf("path = %q", req.URL.Path)
			}
			return jsonResponse(`{"code":0,"message":"ok","result":{"homelist":[{"id":"home-1","uid":100000001,"name":"Demo Home"}],"share_home_list":[],"has_more":false,"max_id":""}}`), nil
		case 2:
			if req.URL.Path != "/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList" {
				t.Fatalf("path = %q", req.URL.Path)
			}
			var body GetManualSceneListRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.HomeID != "home-1" || body.OwnerUID != "100000001" || body.Source != "zkp" || body.GetType != 2 {
				t.Fatalf("body = %#v", body)
			}
			return jsonResponse(`{"code":0,"message":"ok","result":[]}`), nil
		default:
			t.Fatalf("unexpected call %d", call)
			return nil, nil
		}
	})

	client, err := NewCloudClient(
		CloudConfig{ClientID: "2882303761520431603", CloudServer: "cn"},
		WithCloudHTTPClient(doer),
		WithCloudTokenProvider(staticTokenProvider{token: "access-token"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.GetScenes(context.Background(), []string{"home-1"})
	if err != nil {
		t.Fatal(err)
	}
}
