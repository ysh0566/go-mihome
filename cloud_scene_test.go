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
