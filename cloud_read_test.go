package miot

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestCloudClientGetHomeInfosNormalizesHomes(t *testing.T) {
	client, _ := newFixtureCloudClient(t, fixtureResponse{
		path:    "/app/v2/homeroom/gethome",
		fixture: "testdata/cloud/gethome.json",
	})

	got, err := client.GetHomeInfos(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if got.UID != "100000001" {
		t.Fatalf("uid = %q", got.UID)
	}
	if len(got.HomeList) != 1 {
		t.Fatalf("home list len = %d", len(got.HomeList))
	}
	home := got.HomeList["home-1"]
	if home.HomeName != "Demo Home" {
		t.Fatalf("home name = %q", home.HomeName)
	}
	if home.GroupID != CalcGroupID("100000001", "home-1") {
		t.Fatalf("group id = %q", home.GroupID)
	}
	if room := home.Rooms["room-1"]; room.RoomName != "Demo Room" {
		t.Fatalf("room name = %q", room.RoomName)
	}
	if len(got.ShareHomeList) != 1 {
		t.Fatalf("share home list len = %d", len(got.ShareHomeList))
	}
	if got.ShareHomeList["share-home-1"].HomeName != "Shared Demo Home" {
		t.Fatalf("share home name = %q", got.ShareHomeList["share-home-1"].HomeName)
	}
}

func TestCloudClientGetDevicesNormalizesSubDevices(t *testing.T) {
	client, _ := newFixtureCloudClient(t,
		fixtureResponse{path: "/app/v2/homeroom/gethome", fixture: "testdata/cloud/gethome.json"},
		fixtureResponse{path: "/app/v2/home/device_list_page", fixture: "testdata/cloud/device_list_page.json"},
	)

	got, err := client.GetDevices(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	if got.UID != "100000001" {
		t.Fatalf("uid = %q", got.UID)
	}
	if len(got.Devices) != 4 {
		t.Fatalf("device len = %d", len(got.Devices))
	}
	if _, ok := got.Devices["miwifi.router"]; ok {
		t.Fatal("expected miwifi device to be ignored")
	}
	if _, ok := got.Devices["era.airp.cwb03"]; ok {
		t.Fatal("expected unsupported model to be ignored")
	}
	parent := got.Devices["demo-hub-1"]
	if parent.Name != "Gateway Light" {
		t.Fatalf("parent name = %q", parent.Name)
	}
	if parent.SubDevices["s1"].DID != "demo-hub-1.s1" {
		t.Fatalf("sub device did = %q", parent.SubDevices["s1"].DID)
	}
	if parent.SubDevices["s1"].RoomID != "room-1" {
		t.Fatalf("sub device room = %q", parent.SubDevices["s1"].RoomID)
	}
	shared := got.Devices["shared-1"]
	if shared.GroupID != "NotSupport" {
		t.Fatalf("shared group = %q", shared.GroupID)
	}
	if shared.HomeID != "200000002" {
		t.Fatalf("shared owner id = %q", shared.HomeID)
	}
	if got.Homes["separated_shared_list"]["200000002"].HomeName != "Shared Demo User" {
		t.Fatalf("separated shared home = %q", got.Homes["separated_shared_list"]["200000002"].HomeName)
	}
}

func TestCloudClientGetDevicesByDIDChunksRequests(t *testing.T) {
	client, doer := newFixtureCloudClient(t, fixtureResponse{
		path:    "/app/v2/home/device_list_page",
		fixture: "testdata/cloud/device_list_page.json",
	})

	dids := make([]string, 151)
	for i := range dids {
		dids[i] = strings.TrimSpace(strings.Repeat("0", 3-len(strconv.Itoa(i))) + strconv.Itoa(i))
	}

	got, err := client.GetDevicesByDID(context.Background(), dids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected device details")
	}
	if n := doer.calls("/app/v2/home/device_list_page"); n != 2 {
		t.Fatalf("request count = %d", n)
	}
	if got := doer.batchSizes(); len(got) != 2 || got[0] != 1 || got[1] != 150 {
		t.Fatalf("batch sizes = %v", got)
	}
}

func TestCloudClientGetHomeInfosMergesPagedRoomAssignments(t *testing.T) {
	client, _ := newFixtureCloudClient(t,
		fixtureResponse{path: "/app/v2/homeroom/gethome", fixture: "testdata/cloud/gethome_paged.json"},
		fixtureResponse{path: "/app/v2/homeroom/get_dev_room_page", fixture: "testdata/cloud/get_dev_room_page.json"},
	)

	got, err := client.GetHomeInfos(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	home := got.HomeList["home-1"]
	if !containsString(home.DIDs, "extra-home-device") {
		t.Fatalf("home dids = %v", home.DIDs)
	}
	if !containsString(home.Rooms["room-1"].DIDs, "room-light-extra") {
		t.Fatalf("room dids = %v", home.Rooms["room-1"].DIDs)
	}
}

func TestCloudClientGetHomeInfosMergesAllDevRoomPages(t *testing.T) {
	client, doer := newFixtureCloudClient(t,
		fixtureResponse{path: "/app/v2/homeroom/gethome", fixture: "testdata/cloud/gethome_paged_multi.json"},
		fixtureResponse{path: "/app/v2/homeroom/get_dev_room_page", fixture: "testdata/cloud/get_dev_room_page_multi_1.json"},
		fixtureResponse{path: "/app/v2/homeroom/get_dev_room_page", fixture: "testdata/cloud/get_dev_room_page_multi_2.json"},
	)

	got, err := client.GetHomeInfos(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	home := got.HomeList["home-1"]
	if !containsString(home.DIDs, "extra-home-device-2") {
		t.Fatalf("home dids = %v", home.DIDs)
	}
	if !containsString(home.Rooms["room-1"].DIDs, "room-light-extra-2") {
		t.Fatalf("room dids = %v", home.Rooms["room-1"].DIDs)
	}
	if calls := doer.calls("/app/v2/homeroom/get_dev_room_page"); calls != 2 {
		t.Fatalf("get_dev_room_page calls = %d", calls)
	}
}

func TestCloudClientGetDevicesPrefersRoomAssignmentWhenDeviceAppearsInHomeAndRoom(t *testing.T) {
	client, _ := newFixtureCloudClient(t,
		fixtureResponse{path: "/app/v2/homeroom/gethome", fixture: "testdata/cloud/gethome_room_override.json"},
		fixtureResponse{path: "/app/v2/home/device_list_page", fixture: "testdata/cloud/device_list_page.json"},
	)

	got, err := client.GetDevices(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	device := got.Devices["room-light-1"]
	if device.RoomID != "room-1" {
		t.Fatalf("room-light-1 room id = %q", device.RoomID)
	}
	if device.RoomName != "Demo Room" {
		t.Fatalf("room-light-1 room name = %q", device.RoomName)
	}
}

func TestCloudClientGetScenesNormalizesRegularAndSharedHomes(t *testing.T) {
	client, _ := newFixtureCloudClient(t,
		fixtureResponse{path: "/app/v2/homeroom/gethome", fixture: "testdata/cloud/gethome.json"},
		fixtureResponse{
			path:         "/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList",
			fixture:      "testdata/cloud/get_manual_scene_list_home_1.json",
			wantHomeID:   "home-1",
			wantOwnerUID: "100000001",
		},
		fixtureResponse{
			path:         "/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList",
			fixture:      "testdata/cloud/get_manual_scene_list_shared_home_1.json",
			wantHomeID:   "share-home-1",
			wantOwnerUID: "200000001",
		},
	)

	scenes, err := client.GetScenes(context.Background(), []string{"home-1", "share-home-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 2 {
		t.Fatalf("len(scenes) = %d, want 2", len(scenes))
	}
	byHome := make(map[string]SceneInfo, len(scenes))
	for _, scene := range scenes {
		byHome[scene.HomeID] = scene
	}

	regular := byHome["home-1"]
	if regular.ID != "scene-home-1" || regular.Name != "Leave Home" || regular.UID != "100000001" {
		t.Fatalf("regular scene = %#v", regular)
	}
	if regular.HomeName != "Demo Home" || regular.RoomID != "room-1" || regular.SceneType != 2 {
		t.Fatalf("regular scene placement = %#v", regular)
	}
	if regular.TemplateID != "tpl-home-1" || regular.RType != 1 || !regular.Enabled {
		t.Fatalf("regular scene normalized fields = %#v", regular)
	}
	if regular.UpdateTime != 1775147234 {
		t.Fatalf("regular scene update time = %d", regular.UpdateTime)
	}
	if len(regular.DeviceIDs) != 2 || regular.DeviceIDs[0] != "device-1" || regular.DeviceIDs[1] != "device-2" {
		t.Fatalf("regular scene devices = %#v", regular.DeviceIDs)
	}
	if len(regular.ProductIDs) != 2 || regular.ProductIDs[0] != "101" || regular.ProductIDs[1] != "102" {
		t.Fatalf("regular scene products = %#v", regular.ProductIDs)
	}

	shared := byHome["share-home-1"]
	if shared.ID != "scene-shared-home-1" || shared.Name != "Guest Mode" || shared.UID != "200000001" {
		t.Fatalf("shared scene = %#v", shared)
	}
	if shared.HomeName != "Shared Demo Home" || shared.RoomID != "share-room-1" || shared.SceneType != 1 {
		t.Fatalf("shared scene placement = %#v", shared)
	}
	if shared.TemplateID != "tpl-shared-home-1" || shared.RType != 3 || !shared.Enabled {
		t.Fatalf("shared scene normalized fields = %#v", shared)
	}
	if shared.UpdateTime != 1775149234 {
		t.Fatalf("shared scene update time = %d", shared.UpdateTime)
	}
	if len(shared.DeviceIDs) != 1 || shared.DeviceIDs[0] != "shared-1" {
		t.Fatalf("shared scene devices = %#v", shared.DeviceIDs)
	}
	if len(shared.ProductIDs) != 1 || shared.ProductIDs[0] != "301" {
		t.Fatalf("shared scene products = %#v", shared.ProductIDs)
	}
}

func TestCloudClientGetScenesNormalizesSecondRegularHome(t *testing.T) {
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
        "name": "Second Demo Home",
        "city_id": 999001,
        "longitude": 10.0200,
        "latitude": 20.0200,
        "address": "Example City",
        "dids": [
          "second-device-1"
        ],
        "roomlist": [
          {
            "id": "room-2",
            "name": "Second Room",
            "dids": [
              "second-device-1"
            ]
          }
        ]
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
			fixture:      "testdata/cloud/get_manual_scene_list_home_2.json",
			wantHomeID:   "home-2",
			wantOwnerUID: "100000002",
		},
	)

	scenes, err := client.GetScenes(context.Background(), []string{"home-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 1 {
		t.Fatalf("len(scenes) = %d, want 1", len(scenes))
	}
	scene := scenes[0]
	if scene.ID != "scene-home-2" || scene.Name != "Arrive Home" || scene.UID != "100000002" {
		t.Fatalf("scene = %#v", scene)
	}
	if scene.HomeID != "home-2" || scene.HomeName != "Second Demo Home" || scene.RoomID != "room-2" {
		t.Fatalf("scene placement = %#v", scene)
	}
	if scene.SceneType != 1 || scene.TemplateID != "tpl-home-2" || scene.RType != 2 || scene.Enabled {
		t.Fatalf("scene normalized fields = %#v", scene)
	}
	if scene.UpdateTime != 1775148234 {
		t.Fatalf("scene update time = %d", scene.UpdateTime)
	}
	if len(scene.DeviceIDs) != 1 || scene.DeviceIDs[0] != "device-3" {
		t.Fatalf("scene devices = %#v", scene.DeviceIDs)
	}
	if len(scene.ProductIDs) != 1 || scene.ProductIDs[0] != "201" {
		t.Fatalf("scene products = %#v", scene.ProductIDs)
	}
}

type fixtureResponse struct {
	path         string
	fixture      string
	body         string
	wantHomeID   string
	wantOwnerUID string
}

func newFixtureCloudClient(t *testing.T, responses ...fixtureResponse) (*CloudClient, *fixtureDoer) {
	t.Helper()

	token := staticTokenProvider{token: "access-token"}
	doer := newFixtureDoer(t, responses...)
	return &CloudClient{
		http:   doer,
		tokens: token,
		cfg: CloudConfig{
			ClientID:    "2882303761520431603",
			CloudServer: "cn",
		},
	}, doer
}

type staticTokenProvider struct {
	token string
}

func (p staticTokenProvider) AccessToken(context.Context) (string, error) {
	return p.token, nil
}

type fixtureDoer struct {
	t         *testing.T
	responses map[string][]fixtureEntry
	counts    map[string]int
	batches   []int
}

type fixtureEntry struct {
	body         []byte
	wantHomeID   string
	wantOwnerUID string
}

func newFixtureDoer(t *testing.T, responses ...fixtureResponse) *fixtureDoer {
	t.Helper()

	doer := &fixtureDoer{
		t:         t,
		responses: make(map[string][]fixtureEntry),
		counts:    make(map[string]int),
	}
	for _, resp := range responses {
		var data []byte
		var err error
		if resp.body != "" {
			data = []byte(resp.body)
		} else {
			data, err = os.ReadFile(filepath.Clean(resp.fixture))
			if err != nil {
				t.Fatal(err)
			}
		}
		doer.responses[resp.path] = append(doer.responses[resp.path], fixtureEntry{
			body:         data,
			wantHomeID:   resp.wantHomeID,
			wantOwnerUID: resp.wantOwnerUID,
		})
	}
	return doer
}

func (d *fixtureDoer) Do(req *http.Request) (*http.Response, error) {
	d.counts[req.URL.Path]++
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if got := req.Header.Get("Authorization"); got != "Beareraccess-token" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "fixture doer", Msg: "authorization header = " + got}
	}
	if got := req.Header.Get("X-Client-AppId"); got != "2882303761520431603" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "fixture doer", Msg: "client app id = " + got}
	}
	if req.URL.Path == "/app/v2/home/device_list_page" {
		var payload struct {
			DIDs []string `json:"dids"`
		}
		if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&payload); err != nil {
			return nil, err
		}
		d.batches = append(d.batches, len(payload.DIDs))
	}
	entries, ok := d.responses[req.URL.Path]
	if !ok || len(entries) == 0 {
		return nil, &Error{Code: ErrInvalidArgument, Op: "fixture doer", Msg: "missing fixture for " + req.URL.Path}
	}
	var body []byte
	if hasSceneFixture(entries) {
		match := sceneFixtureMatch{
			homeID:   readJSONField(bodyBytes, "home_id"),
			ownerUID: readJSONField(bodyBytes, "owner_uid"),
		}
		for _, entry := range entries {
			if entry.wantHomeID == match.homeID && entry.wantOwnerUID == match.ownerUID {
				body = entry.body
				break
			}
		}
		if body == nil {
			return nil, &Error{Code: ErrInvalidArgument, Op: "fixture doer", Msg: "missing scene fixture for " + req.URL.Path + " home_id=" + match.homeID + " owner_uid=" + match.ownerUID}
		}
	} else {
		index := d.counts[req.URL.Path] - 1
		if index >= len(entries) {
			index = len(entries) - 1
		}
		body = entries[index].body
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}, nil
}

type sceneFixtureMatch struct {
	homeID   string
	ownerUID string
}

func hasSceneFixture(entries []fixtureEntry) bool {
	for _, entry := range entries {
		if entry.wantHomeID != "" || entry.wantOwnerUID != "" {
			return true
		}
	}
	return false
}

func readJSONField(body []byte, field string) string {
	if len(body) == 0 {
		return ""
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	raw, ok := payload[field]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func (d *fixtureDoer) calls(path string) int {
	return d.counts[path]
}

func (d *fixtureDoer) batchSizes() []int {
	out := append([]int(nil), d.batches...)
	sort.Ints(out)
	return out
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
