package miot

import (
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
		fixtureResponse{path: "/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList", fixture: "testdata/cloud/get_manual_scene_list_home_1.json"},
		fixtureResponse{path: "/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList", fixture: "testdata/cloud/get_manual_scene_list_shared_home_1.json"},
	)

	scenes, err := client.GetScenes(context.Background(), []string{"home-1", "share-home-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 2 {
		t.Fatalf("len(scenes) = %d, want 2", len(scenes))
	}
	if scenes[0].SceneType != 2 || scenes[0].TemplateID == "" {
		t.Fatalf("scene = %#v, want normalized extra fields", scenes[0])
	}
}

type fixtureResponse struct {
	path    string
	fixture string
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
	responses map[string][][]byte
	counts    map[string]int
	batches   []int
}

func newFixtureDoer(t *testing.T, responses ...fixtureResponse) *fixtureDoer {
	t.Helper()

	doer := &fixtureDoer{
		t:         t,
		responses: make(map[string][][]byte),
		counts:    make(map[string]int),
	}
	for _, resp := range responses {
		data, err := os.ReadFile(filepath.Clean(resp.fixture))
		if err != nil {
			t.Fatal(err)
		}
		doer.responses[resp.path] = append(doer.responses[resp.path], data)
	}
	return doer
}

func (d *fixtureDoer) Do(req *http.Request) (*http.Response, error) {
	d.counts[req.URL.Path]++
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
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			return nil, err
		}
		d.batches = append(d.batches, len(payload.DIDs))
	}
	bodies, ok := d.responses[req.URL.Path]
	if !ok || len(bodies) == 0 {
		return nil, &Error{Code: ErrInvalidArgument, Op: "fixture doer", Msg: "missing fixture for " + req.URL.Path}
	}
	index := d.counts[req.URL.Path] - 1
	if index >= len(bodies) {
		index = len(bodies) - 1
	}
	body := bodies[index]
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(body))),
	}, nil
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
