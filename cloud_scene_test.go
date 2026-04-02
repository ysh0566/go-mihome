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
		if body.HomeID != "home-1" || body.RoomID != "room-1" {
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
	if err := client.RunScene(context.Background(), SceneRunRequest{SceneID: "scene-1", OwnerUID: "100000001", HomeID: "home-1", RoomID: "room-1"}); err != nil {
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

func TestCloudClientGetScenesHonorsNilAndEmptyHomeFilters(t *testing.T) {
	makeClient := func(t *testing.T) (*CloudClient, *fixtureDoer) {
		t.Helper()
		return newFixtureCloudClient(t,
			fixtureResponse{
				path: "/app/v2/homeroom/gethome",
				body: `{
  "code": 0,
  "message": "ok",
  "result": {
    "homelist": [
      {
        "id": "home-1",
        "uid": 100000001,
        "name": "Home One"
      },
      {
        "id": "home-2",
        "uid": 100000002,
        "name": "Home Two"
      }
    ],
    "share_home_list": [],
    "has_more": false,
    "max_id": ""
  }
}`,
			},
			fixtureResponse{
				path:         "/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList",
				body:         `{"code":0,"message":"ok","result":[{"scene_id":"scene-home-1","scene_name":"Scene Home 1","update_time":"1","home_id":"home-1"}]}`,
				wantHomeID:   "home-1",
				wantOwnerUID: "100000001",
			},
			fixtureResponse{
				path:         "/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList",
				body:         `{"code":0,"message":"ok","result":[{"scene_id":"scene-home-2","scene_name":"Scene Home 2","update_time":"2","home_id":"home-2"}]}`,
				wantHomeID:   "home-2",
				wantOwnerUID: "100000002",
			},
		)
	}

	allClient, allDoer := makeClient(t)
	allScenes, err := allClient.GetScenes(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(allScenes) != 2 {
		t.Fatalf("len(allScenes) = %d", len(allScenes))
	}
	if got := allDoer.calls("/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList"); got != 2 {
		t.Fatalf("scene request count = %d", got)
	}

	emptyClient, emptyDoer := makeClient(t)
	emptyScenes, err := emptyClient.GetScenes(context.Background(), []string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyScenes) != 0 {
		t.Fatalf("len(emptyScenes) = %d", len(emptyScenes))
	}
	if got := emptyDoer.calls("/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList"); got != 0 {
		t.Fatalf("scene request count = %d", got)
	}
}

func TestCloudClientGetScenesPrefersCloudFailureWhenResultMissing(t *testing.T) {
	client, _ := newFixtureCloudClient(t,
		fixtureResponse{
			path: "/app/v2/homeroom/gethome",
			body: `{
  "code": 0,
  "message": "ok",
  "result": {
    "homelist": [
      {
        "id": "home-1",
        "uid": 100000001,
        "name": "Home One"
      }
    ],
    "share_home_list": [],
    "has_more": false,
    "max_id": ""
  }
}`,
		},
		fixtureResponse{
			path:         "/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList",
			body:         `{"code":5,"message":"boom"}`,
			wantHomeID:   "home-1",
			wantOwnerUID: "100000001",
		},
	)

	_, err := client.GetScenes(context.Background(), []string{"home-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := err.Error(), "get scenes failed: code 5"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestCloudClientGetScenesSortsByHomeNameAndID(t *testing.T) {
	client, _ := newFixtureCloudClient(t,
		fixtureResponse{
			path: "/app/v2/homeroom/gethome",
			body: `{
  "code": 0,
  "message": "ok",
  "result": {
    "homelist": [
      {
        "id": "home-2",
        "uid": 100000002,
        "name": "Home Two"
      },
      {
        "id": "home-1",
        "uid": 100000001,
        "name": "Home One"
      }
    ],
    "share_home_list": [],
    "has_more": false,
    "max_id": ""
  }
}`,
		},
		fixtureResponse{
			path:         "/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList",
			body:         `{"code":0,"message":"ok","result":[{"scene_id":"scene-z","scene_name":"Zed","update_time":"4","home_id":"home-1"},{"scene_id":"scene-b","scene_name":"Alpha","update_time":"2","home_id":"home-1"},{"scene_id":"scene-a","scene_name":"Alpha","update_time":"1","home_id":"home-1"}]}`,
			wantHomeID:   "home-1",
			wantOwnerUID: "100000001",
		},
		fixtureResponse{
			path:         "/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList",
			body:         `{"code":0,"message":"ok","result":[{"scene_id":"scene-y","scene_name":"Beta","update_time":"3","home_id":"home-2"}]}`,
			wantHomeID:   "home-2",
			wantOwnerUID: "100000002",
		},
	)

	scenes, err := client.GetScenes(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 4 {
		t.Fatalf("len(scenes) = %d", len(scenes))
	}
	got := []struct {
		homeID string
		name   string
		id     string
	}{
		{scenes[0].HomeID, scenes[0].Name, scenes[0].ID},
		{scenes[1].HomeID, scenes[1].Name, scenes[1].ID},
		{scenes[2].HomeID, scenes[2].Name, scenes[2].ID},
		{scenes[3].HomeID, scenes[3].Name, scenes[3].ID},
	}
	want := []struct {
		homeID string
		name   string
		id     string
	}{
		{"home-1", "Alpha", "scene-a"},
		{"home-1", "Alpha", "scene-b"},
		{"home-1", "Zed", "scene-z"},
		{"home-2", "Beta", "scene-y"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scene[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}
