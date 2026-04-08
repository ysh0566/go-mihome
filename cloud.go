package miot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	cloudBizID                  = "haapi"
	cloudHomeLimit              = 150
	cloudDeviceLimit            = 200
	cloudDeviceChunkSize        = 150
	cloudAppVersion             = 9
	cloudPropertySource         = 1
	sharedDeviceRoomID          = "shared_device"
	notSupportGroupID           = "NotSupport"
	openAccountProfileURL       = "https://open.account.xiaomi.com/user/profile"
	cameraVendorSecurityPath    = "/app/v2/device/miss_get_vendor_security"
	cameraVendorPath            = "/v2/device/miss_get_vendor"
	cameraPincodeGenericPath    = "/app/v2/pincode/check_generic"
	cameraPincodeStandalonePath = "/app/pincode/check_alone"
	cameraPerfDataPath          = "/app/v2/device/camera_perf_data"
	cameraPerfDataTypeEvent     = "EventData"
)

var unsupportedCloudModels = map[string]struct{}{
	"chuangmi.ir.v2":        {},
	"era.airp.cwb03":        {},
	"hmpace.motion.v6nfc":   {},
	"k0918.toothbrush.t700": {},
}

var subDeviceSuffixPattern = regexp.MustCompile(`\.s\d+$`)

// CloudConfig configures the Xiaomi Home cloud client.
type CloudConfig struct {
	ClientID    string
	CloudServer string
}

// TokenProvider supplies an access token for Xiaomi cloud requests.
type TokenProvider interface {
	AccessToken(ctx context.Context) (string, error)
}

// CloudClientOption configures a CloudClient.
type CloudClientOption func(*CloudClient)

// CloudClient drives Xiaomi Home cloud read APIs and domain normalization.
type CloudClient struct {
	mu     sync.RWMutex
	http   HTTPDoer
	tokens TokenProvider
	clock  Clock
	cfg    CloudConfig
}

// WithCloudHTTPClient injects a custom HTTP client into CloudClient.
func WithCloudHTTPClient(client HTTPDoer) CloudClientOption {
	return func(c *CloudClient) {
		if client != nil {
			c.http = client
		}
	}
}

// WithCloudTokenProvider injects a custom token provider into CloudClient.
func WithCloudTokenProvider(tokens TokenProvider) CloudClientOption {
	return func(c *CloudClient) {
		if tokens != nil {
			c.tokens = tokens
		}
	}
}

// WithCloudClock injects a custom clock into CloudClient.
func WithCloudClock(clock Clock) CloudClientOption {
	return func(c *CloudClient) {
		if clock != nil {
			c.clock = clock
		}
	}
}

// NewCloudClient creates a Xiaomi Home cloud client.
func NewCloudClient(cfg CloudConfig, opts ...CloudClientOption) (*CloudClient, error) {
	if cfg.ClientID == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new cloud client", Msg: "client id is empty"}
	}
	if cfg.CloudServer == "" {
		cfg.CloudServer = "cn"
	}
	client := &CloudClient{
		http:  &http.Client{},
		clock: realClock{},
		cfg:   cfg,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	if client.http == nil {
		client.http = &http.Client{}
	}
	if client.clock == nil {
		client.clock = realClock{}
	}
	return client, nil
}

type fixedTokenProvider struct {
	token string
}

func (p fixedTokenProvider) AccessToken(context.Context) (string, error) {
	return p.token, nil
}

// UpdateAuth updates the active cloud region, app id, and access token.
func (c *CloudClient) UpdateAuth(cloudServer, clientID, accessToken string) error {
	if c == nil {
		return &Error{Code: ErrInvalidArgument, Op: "update cloud auth", Msg: "client is nil"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if cloudServer != "" {
		c.cfg.CloudServer = cloudServer
	}
	if clientID != "" {
		c.cfg.ClientID = clientID
	}
	if accessToken != "" {
		c.tokens = fixedTokenProvider{token: accessToken}
	}
	return nil
}

// GetUserInfo fetches the Xiaomi account profile for the current access token.
func (c *CloudClient) GetUserInfo(ctx context.Context) (AccountProfile, error) {
	if c == nil {
		return AccountProfile{}, &Error{Code: ErrInvalidArgument, Op: "get user info", Msg: "client is nil"}
	}
	if c.http == nil {
		return AccountProfile{}, &Error{Code: ErrInvalidArgument, Op: "get user info", Msg: "http client is nil"}
	}
	if c.tokens == nil {
		return AccountProfile{}, &Error{Code: ErrInvalidArgument, Op: "get user info", Msg: "token provider is nil"}
	}

	token, err := c.tokens.AccessToken(ctx)
	if err != nil {
		return AccountProfile{}, err
	}
	clientID := c.clientID()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAccountProfileURL, nil)
	if err != nil {
		return AccountProfile{}, err
	}
	query := req.URL.Query()
	query.Set("clientId", clientID)
	query.Set("token", token)
	req.URL.RawQuery = query.Encode()
	req.Header.Set("content-type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return AccountProfile{}, Wrap(ErrTransportFailure, "get user info", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return AccountProfile{}, &Error{Code: ErrInvalidAccessToken, Op: "get user info", Msg: "unauthorized"}
	}
	if resp.StatusCode != http.StatusOK {
		return AccountProfile{}, &Error{Code: ErrTransportFailure, Op: "get user info", Msg: fmt.Sprintf("status %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return AccountProfile{}, err
	}
	var profileResp AccountProfileResponse
	if err := json.Unmarshal(body, &profileResp); err != nil {
		return AccountProfile{}, Wrap(ErrDecodeResponse, "decode user profile response", err)
	}
	if profileResp.Code != 0 {
		return AccountProfile{}, fmt.Errorf("user profile response code %d", profileResp.Code)
	}
	if profileResp.Data.MiliaoNick == "" {
		return AccountProfile{}, &Error{Code: ErrInvalidResponse, Op: "get user info", Msg: "missing miliaoNick"}
	}
	return profileResp.Data, nil
}

// GetCentralCert requests a user central certificate for the provided PEM CSR.
func (c *CloudClient) GetCentralCert(ctx context.Context, csr string) (string, error) {
	if csr == "" {
		return "", &Error{Code: ErrInvalidArgument, Op: "get central cert", Msg: "csr is empty"}
	}
	var resp GetCentralCertResponse
	if err := c.postJSON(ctx, "/app/v2/ha/oauth/get_central_crt", GetCentralCertRequest{
		CSR: base64.StdEncoding.EncodeToString([]byte(csr)),
	}, &resp); err != nil {
		return "", err
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("get central cert failed: code %d", resp.Code)
	}
	if resp.Result == nil || resp.Result.Cert == "" {
		return "", &Error{Code: ErrInvalidResponse, Op: "get central cert", Msg: "missing cert"}
	}
	return resp.Result.Cert, nil
}

// GetUID loads the Xiaomi user ID from the normalized home listing.
func (c *CloudClient) GetUID(ctx context.Context) (string, error) {
	infos, err := c.GetHomeInfos(ctx)
	if err != nil {
		return "", err
	}
	return infos.UID, nil
}

// GetHomeInfos fetches and normalizes the Xiaomi home listing.
func (c *CloudClient) GetHomeInfos(ctx context.Context) (HomeInfos, error) {
	var resp GetHomeResponse
	if err := c.postJSON(ctx, "/app/v2/homeroom/gethome", GetHomeRequest{
		Limit:         cloudHomeLimit,
		FetchShare:    true,
		FetchShareDev: true,
		PlatForm:      0,
		AppVer:        cloudAppVersion,
	}, &resp); err != nil {
		return HomeInfos{}, err
	}
	if resp.Result == nil {
		return HomeInfos{}, &Error{Code: ErrInvalidResponse, Op: "get home infos", Msg: "missing result"}
	}
	if resp.Code != 0 {
		return HomeInfos{}, fmt.Errorf("get home infos failed: code %d", resp.Code)
	}

	infos, err := normalizeHomeInfos(resp.Result)
	if err != nil {
		return HomeInfos{}, err
	}
	if resp.Result.HasMore && resp.Result.MaxID != "" {
		extras, err := c.fetchAllDevRoomPages(ctx, resp.Result.MaxID)
		if err != nil {
			return HomeInfos{}, err
		}
		mergeHomeExtras(&infos, extras)
	}
	return infos, nil
}

// GetScenes fetches and normalizes manual scenes for the selected homes.
func (c *CloudClient) GetScenes(ctx context.Context, homeIDs []string) ([]SceneInfo, error) {
	infos, err := c.GetHomeInfos(ctx)
	if err != nil {
		return nil, err
	}

	selected := selectSceneHomes(infos, homeIDs)
	scenes := make([]SceneInfo, 0)
	for _, home := range selected {
		items, err := c.fetchManualScenes(ctx, home)
		if err != nil {
			return nil, err
		}
		scenes = append(scenes, items...)
	}

	sort.SliceStable(scenes, func(i, j int) bool {
		if scenes[i].HomeID != scenes[j].HomeID {
			return scenes[i].HomeID < scenes[j].HomeID
		}
		if scenes[i].Name != scenes[j].Name {
			return scenes[i].Name < scenes[j].Name
		}
		return scenes[i].ID < scenes[j].ID
	})
	return scenes, nil
}

// RunScene triggers one manual Xiaomi scene.
func (c *CloudClient) RunScene(ctx context.Context, req SceneRunRequest) error {
	sceneID := strings.TrimSpace(req.SceneID)
	if sceneID == "" {
		return &Error{Code: ErrInvalidArgument, Op: "run scene", Msg: "scene id is empty"}
	}
	ownerUID := strings.TrimSpace(req.OwnerUID)
	if ownerUID == "" {
		return &Error{Code: ErrInvalidArgument, Op: "run scene", Msg: "owner uid is empty"}
	}

	var resp RunSceneResponse
	if err := c.postJSON(ctx, "/app/appgateway/miot/appsceneservice/AppSceneService/NewRunScene", RunSceneRequest{
		SceneID:   sceneID,
		OwnerUID:  ownerUID,
		SceneType: 2,
		HomeID:    strings.TrimSpace(req.HomeID),
		RoomID:    strings.TrimSpace(req.RoomID),
	}, &resp); err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("run scene failed: code %d", resp.Code)
	}
	if !resp.Result {
		return fmt.Errorf("scene %s was not accepted", sceneID)
	}
	return nil
}

// GetDevices fetches devices for the selected homes and normalizes the result.
func (c *CloudClient) GetDevices(ctx context.Context, homeIDs []string) (DeviceSnapshot, error) {
	infos, homes, baseDIDs, err := c.buildHomeDeviceBase(ctx, homeIDs)
	if err != nil {
		return DeviceSnapshot{}, err
	}

	devicesByDID := make(map[string]DeviceInfo, len(baseDIDs))
	detailed, err := c.GetDevicesByDID(ctx, baseDIDs)
	if err != nil {
		return DeviceSnapshot{}, err
	}
	for _, device := range detailed {
		devicesByDID[device.DID] = device
	}

	devices := make(map[string]DeviceInfo, len(devicesByDID))
	for _, did := range sortedKeys(devicesByDID) {
		detail := devicesByDID[did]
		base, ok := baseDeviceByDID(homes, did)
		if !ok {
			continue
		}
		detail.HomeID = base.HomeID
		detail.HomeName = base.HomeName
		detail.RoomID = base.RoomID
		detail.RoomName = base.RoomName
		detail.GroupID = base.GroupID
		devices[did] = detail
	}

	sharedDevices, sharedHomes, err := c.discoverSeparatedSharedDevices(ctx)
	if err != nil {
		return DeviceSnapshot{}, err
	}
	mergeSharedDevices(homes, devices, sharedHomes, sharedDevices)
	nestSubDevices(devices)

	return DeviceSnapshot{
		UID:     infos.UID,
		Homes:   homes,
		Devices: devices,
	}, nil
}

// GetDevicesByDID fetches and normalizes devices for the provided device IDs.
func (c *CloudClient) GetDevicesByDID(ctx context.Context, dids []string) ([]DeviceInfo, error) {
	if len(dids) == 0 {
		return nil, nil
	}
	unique := uniqueStrings(dids)
	var out []DeviceInfo
	for start := 0; start < len(unique); start += cloudDeviceChunkSize {
		end := start + cloudDeviceChunkSize
		if end > len(unique) {
			end = len(unique)
		}
		chunk := unique[start:end]
		cursor := ""
		for {
			page, hasMore, nextStartDID, err := c.fetchDeviceListPage(ctx, chunk, cursor)
			if err != nil {
				return nil, err
			}
			out = append(out, page...)
			if !hasMore || nextStartDID == "" {
				break
			}
			cursor = nextStartDID
		}
	}
	return out, nil
}

// GetDeviceByDID fetches and normalizes one device by DID.
func (c *CloudClient) GetDeviceByDID(ctx context.Context, did string) (DeviceInfo, error) {
	did = strings.TrimSpace(did)
	if did == "" {
		return DeviceInfo{}, &Error{Code: ErrInvalidArgument, Op: "get device by did", Msg: "did is empty"}
	}
	devices, err := c.GetDevicesByDID(ctx, []string{did})
	if err != nil {
		return DeviceInfo{}, err
	}
	for _, device := range devices {
		if device.DID == did {
			return device, nil
		}
	}
	return DeviceInfo{}, fmt.Errorf("device %s not found", did)
}

// GetDevice fetches and normalizes one device by DID.
func (c *CloudClient) GetDevice(ctx context.Context, did string) (DeviceInfo, error) {
	return c.GetDeviceByDID(ctx, did)
}

// CheckGenericCameraPincode validates a generic Xiaomi camera pincode.
func (c *CloudClient) CheckGenericCameraPincode(ctx context.Context, did string, pincode string) (bool, error) {
	return c.checkCameraPincode(ctx, cameraPincodeGenericPath, did, pincode)
}

// CheckStandaloneCameraPincode validates a standalone Xiaomi camera pincode.
func (c *CloudClient) CheckStandaloneCameraPincode(ctx context.Context, did string, pincode string) (bool, error) {
	return c.checkCameraPincode(ctx, cameraPincodeStandalonePath, did, pincode)
}

// GetCameraVendorSecurity fetches the Xiaomi camera vendor security payload.
func (c *CloudClient) GetCameraVendorSecurity(ctx context.Context, did string) (CameraVendorSecurity, error) {
	did = strings.TrimSpace(did)
	if did == "" {
		return CameraVendorSecurity{}, &Error{Code: ErrInvalidArgument, Op: "get camera vendor security", Msg: "did is empty"}
	}
	var resp struct {
		Code    int                  `json:"code"`
		Message string               `json:"message,omitempty"`
		Result  CameraVendorSecurity `json:"result"`
	}
	if err := c.postJSON(ctx, cameraVendorSecurityPath, struct {
		DID string `json:"did"`
	}{DID: did}, &resp); err != nil {
		return CameraVendorSecurity{}, err
	}
	if resp.Code != 0 {
		return CameraVendorSecurity{}, fmt.Errorf("get camera vendor security failed: code %d", resp.Code)
	}
	return resp.Result, nil
}

// GetCameraVendor fetches the Xiaomi camera vendor bootstrap payload.
func (c *CloudClient) GetCameraVendor(ctx context.Context, did string, appPublicKey []byte, supportVendors string, callerUUID string) (CameraVendorInfo, error) {
	did = strings.TrimSpace(did)
	if did == "" {
		return CameraVendorInfo{}, &Error{Code: ErrInvalidArgument, Op: "get camera vendor", Msg: "did is empty"}
	}
	if len(appPublicKey) == 0 {
		return CameraVendorInfo{}, &Error{Code: ErrInvalidArgument, Op: "get camera vendor", Msg: "app public key is empty"}
	}
	supportVendors = strings.TrimSpace(supportVendors)
	if supportVendors == "" {
		supportVendors = "TUTK_CS2_MTP"
	}
	var resp struct {
		Code    int             `json:"code"`
		Message string          `json:"message,omitempty"`
		Result  json.RawMessage `json:"result"`
	}
	if err := c.postJSON(ctx, cameraVendorPath, struct {
		AppPublicKey string `json:"app_pubkey"`
		DID          string `json:"did"`
		Support      string `json:"support_vendors"`
		CallerUUID   string `json:"caller_uuid"`
	}{
		AppPublicKey: hex.EncodeToString(appPublicKey),
		DID:          did,
		Support:      supportVendors,
		CallerUUID:   strings.TrimSpace(callerUUID),
	}, &resp); err != nil {
		return CameraVendorInfo{}, err
	}
	if resp.Code != 0 {
		return CameraVendorInfo{}, fmt.Errorf("get camera vendor failed: code %d", resp.Code)
	}
	return parseCameraVendorInfo(resp.Result)
}

// ReportCameraPerfEvent posts one Xiaomi camera performance event.
func (c *CloudClient) ReportCameraPerfEvent(ctx context.Context, did string, head map[string]any, data any) error {
	did = strings.TrimSpace(did)
	if did == "" {
		return &Error{Code: ErrInvalidArgument, Op: "report camera perf event", Msg: "did is empty"}
	}
	payload := CameraPerfEnvelope{
		Head:     cloneStringAnyMap(head),
		DID:      did,
		DataType: cameraPerfDataTypeEvent,
		Data:     data,
	}
	if payload.Head == nil {
		payload.Head = map[string]any{}
	}
	var resp struct {
		Code    int             `json:"code"`
		Message string          `json:"message,omitempty"`
		Result  json.RawMessage `json:"result"`
	}
	if err := c.postJSON(ctx, cameraPerfDataPath, payload, &resp); err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("report camera perf event failed: code %d", resp.Code)
	}
	return nil
}

// GetProps fetches typed property values from the Xiaomi cloud API.
func (c *CloudClient) GetProps(ctx context.Context, req GetPropsRequest) ([]PropertyResult, error) {
	if len(req.Params) == 0 {
		return nil, &Error{Code: ErrInvalidArgument, Op: "get props", Msg: "params are empty"}
	}
	if req.DataSource == 0 {
		req.DataSource = cloudPropertySource
	}
	var resp GetPropsResponse
	if err := c.postJSON(ctx, "/app/v2/miotspec/prop/get", req, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("get props failed: code %d", resp.Code)
	}
	return resp.Result, nil
}

// GetProp fetches one typed property value from the Xiaomi cloud API.
func (c *CloudClient) GetProp(ctx context.Context, query PropertyQuery) (PropertyResult, error) {
	results, err := c.GetProps(ctx, GetPropsRequest{
		DataSource: cloudPropertySource,
		Params:     []PropertyQuery{query},
	})
	if err != nil {
		return PropertyResult{}, err
	}
	if len(results) == 0 {
		return PropertyResult{}, &Error{Code: ErrInvalidResponse, Op: "get prop", Msg: "missing result"}
	}
	return results[0], nil
}

// SetProps sends typed property writes to the Xiaomi cloud API.
func (c *CloudClient) SetProps(ctx context.Context, req SetPropsRequest) ([]SetPropertyResult, error) {
	if len(req.Params) == 0 {
		return nil, &Error{Code: ErrInvalidArgument, Op: "set props", Msg: "params are empty"}
	}
	var resp SetPropsResponse
	if err := c.postJSON(ctx, "/app/v2/miotspec/prop/set", req, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("set props failed: code %d", resp.Code)
	}
	return resp.Result, nil
}

// InvokeAction sends a typed MIoT action invocation to the Xiaomi cloud API.
func (c *CloudClient) InvokeAction(ctx context.Context, req ActionRequest) (ActionResult, error) {
	var resp ActionResponse
	if err := c.postJSON(ctx, "/app/v2/miotspec/action", actionRequestEnvelope{Params: req}, &resp); err != nil {
		return ActionResult{}, err
	}
	if resp.Code != 0 {
		return ActionResult{}, fmt.Errorf("invoke action failed: code %d", resp.Code)
	}
	return resp.Result, nil
}

func (c *CloudClient) discoverSeparatedSharedDevices(ctx context.Context) (map[string]DeviceInfo, map[string]HomeInfo, error) {
	page, hasMore, nextStartDID, err := c.fetchDeviceListPage(ctx, nil, "")
	if err != nil {
		return nil, nil, err
	}
	for hasMore && nextStartDID != "" {
		more, moreHasMore, moreNext, err := c.fetchDeviceListPage(ctx, nil, nextStartDID)
		if err != nil {
			return nil, nil, err
		}
		page = append(page, more...)
		hasMore = moreHasMore
		nextStartDID = moreNext
	}

	devices := make(map[string]DeviceInfo)
	homes := make(map[string]HomeInfo)
	for _, device := range page {
		if device.Owner == nil || device.Owner.UserID == "" || device.Owner.Nickname == "" {
			continue
		}
		ownerID := device.Owner.UserID
		home, ok := homes[ownerID]
		if !ok {
			home = HomeInfo{
				HomeID:   ownerID,
				HomeName: device.Owner.Nickname,
				UID:      ownerID,
				GroupID:  notSupportGroupID,
				Rooms:    map[string]RoomInfo{},
			}
		}
		if home.Rooms == nil {
			home.Rooms = map[string]RoomInfo{}
		}
		room := home.Rooms[sharedDeviceRoomID]
		if room.RoomID == "" {
			room = RoomInfo{RoomID: sharedDeviceRoomID, RoomName: sharedDeviceRoomID}
		}
		room.DIDs = appendUnique(room.DIDs, device.DID)
		home.Rooms[sharedDeviceRoomID] = room
		home.DIDs = appendUnique(home.DIDs, device.DID)
		homes[ownerID] = home

		device.HomeID = ownerID
		device.HomeName = home.HomeName
		device.RoomID = sharedDeviceRoomID
		device.RoomName = sharedDeviceRoomID
		device.GroupID = notSupportGroupID
		devices[device.DID] = device
	}
	return devices, homes, nil
}

func (c *CloudClient) fetchDeviceListPage(ctx context.Context, dids []string, startDID string) ([]DeviceInfo, bool, string, error) {
	if dids == nil {
		dids = []string{}
	}
	req := GetDeviceListPageRequest{
		Limit:          cloudDeviceLimit,
		GetSplitDevice: true,
		GetThirdDevice: true,
		DIDs:           dids,
		StartDID:       startDID,
	}
	var resp GetDeviceListPageResponse
	if err := c.postJSON(ctx, "/app/v2/home/device_list_page", req, &resp); err != nil {
		return nil, false, "", err
	}
	if resp.Result == nil {
		return nil, false, "", &Error{Code: ErrInvalidResponse, Op: "get device list page", Msg: "missing result"}
	}
	if resp.Code != 0 {
		return nil, false, "", fmt.Errorf("get device list page failed: code %d", resp.Code)
	}

	devices := make([]DeviceInfo, 0, len(resp.Result.List))
	for _, raw := range resp.Result.List {
		device, ok := normalizeDeviceRecord(raw)
		if !ok {
			continue
		}
		devices = append(devices, device)
	}
	return devices, resp.Result.HasMore, resp.Result.NextStartDID, nil
}

func (c *CloudClient) fetchAllDevRoomPages(ctx context.Context, startID string) ([]GetHomeCloudHome, error) {
	cursor := startID
	var all []GetHomeCloudHome
	for cursor != "" {
		page, hasMore, nextCursor, err := c.fetchDevRoomPage(ctx, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if !hasMore || nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return all, nil
}

func (c *CloudClient) fetchDevRoomPage(ctx context.Context, startID string) ([]GetHomeCloudHome, bool, string, error) {
	req := GetDevRoomPageRequest{
		StartID: startID,
		Limit:   cloudHomeLimit,
	}
	var resp GetDevRoomPageResponse
	if err := c.postJSON(ctx, "/app/v2/homeroom/get_dev_room_page", req, &resp); err != nil {
		return nil, false, "", err
	}
	if resp.Result == nil {
		return nil, false, "", &Error{Code: ErrInvalidResponse, Op: "get dev room page", Msg: "missing result"}
	}
	if resp.Code != 0 {
		return nil, false, "", fmt.Errorf("get dev room page failed: code %d", resp.Code)
	}
	return resp.Result.Info, resp.Result.HasMore, resp.Result.MaxID, nil
}

func (c *CloudClient) fetchManualScenes(ctx context.Context, home HomeInfo) ([]SceneInfo, error) {
	var resp GetManualSceneListResponse
	if err := c.postJSON(ctx, "/app/appgateway/miot/appsceneservice/AppSceneService/GetManualSceneList", GetManualSceneListRequest{
		HomeID:   home.HomeID,
		OwnerUID: home.UID,
		Source:   "zkp",
		GetType:  2,
	}, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("get scenes failed: code %d", resp.Code)
	}
	if resp.Result == nil {
		return nil, &Error{Code: ErrInvalidResponse, Op: "get scenes", Msg: "missing result"}
	}

	scenes := make([]SceneInfo, 0, len(resp.Result))
	for _, raw := range resp.Result {
		scene, ok := normalizeSceneRecord(home, raw)
		if !ok {
			continue
		}
		scenes = append(scenes, scene)
	}
	return scenes, nil
}

func (c *CloudClient) buildHomeDeviceBase(ctx context.Context, homeIDs []string) (HomeInfos, map[string]map[string]HomeInfo, []string, error) {
	infos, err := c.GetHomeInfos(ctx)
	if err != nil {
		return HomeInfos{}, nil, nil, err
	}

	selected := make(map[string]struct{}, len(homeIDs))
	filterHomes := homeIDs != nil
	if filterHomes {
		for _, homeID := range homeIDs {
			selected[homeID] = struct{}{}
		}
	}

	homes := map[string]map[string]HomeInfo{
		"home_list":             {},
		"share_home_list":       {},
		"separated_shared_list": {},
	}
	didSet := make(map[string]struct{})

	addHome := func(bucket string, home HomeInfo) {
		if filterHomes {
			if _, ok := selected[home.HomeID]; !ok {
				return
			}
		}
		homes[bucket][home.HomeID] = cloneHomeInfo(home)
		collectHomeDIDs(home, didSet)
	}

	for id, home := range infos.HomeList {
		_ = id
		addHome("home_list", home)
	}
	for id, home := range infos.ShareHomeList {
		_ = id
		addHome("share_home_list", home)
	}

	baseDIDs := make([]string, 0, len(didSet))
	for did := range didSet {
		baseDIDs = append(baseDIDs, did)
	}
	sort.Strings(baseDIDs)
	return infos, homes, baseDIDs, nil
}

func collectHomeDIDs(home HomeInfo, didSet map[string]struct{}) {
	for _, did := range home.DIDs {
		didSet[did] = struct{}{}
	}
	for _, room := range home.Rooms {
		for _, did := range room.DIDs {
			didSet[did] = struct{}{}
		}
	}
}

func baseDeviceByDID(homes map[string]map[string]HomeInfo, did string) (DeviceInfo, bool) {
	for _, bucket := range homes {
		for _, home := range bucket {
			for roomID, room := range home.Rooms {
				for _, roomDid := range room.DIDs {
					if roomDid == did {
						return DeviceInfo{
							DID:      did,
							UID:      home.UID,
							HomeID:   home.HomeID,
							HomeName: home.HomeName,
							RoomID:   roomID,
							RoomName: room.RoomName,
							GroupID:  home.GroupID,
						}, true
					}
				}
			}
			for _, homeDid := range home.DIDs {
				if homeDid == did {
					return DeviceInfo{
						DID:      did,
						UID:      home.UID,
						HomeID:   home.HomeID,
						HomeName: home.HomeName,
						RoomID:   home.HomeID,
						RoomName: home.HomeName,
						GroupID:  home.GroupID,
					}, true
				}
			}
		}
	}
	return DeviceInfo{}, false
}

func mergeSharedDevices(homes map[string]map[string]HomeInfo, devices map[string]DeviceInfo, sharedHomes map[string]HomeInfo, sharedDevices map[string]DeviceInfo) {
	if len(sharedHomes) > 0 {
		bucket := homes["separated_shared_list"]
		for ownerID, home := range sharedHomes {
			bucket[ownerID] = cloneHomeInfo(home)
		}
		homes["separated_shared_list"] = bucket
	}
	for did, device := range sharedDevices {
		devices[did] = device
	}
}

func nestSubDevices(devices map[string]DeviceInfo) {
	parentChildren := make(map[string]map[string]DeviceInfo)
	for did, device := range devices {
		if match := subDeviceSuffixPattern.FindString(did); match != "" {
			parentDID := strings.TrimSuffix(did, match)
			if parentDID == "" {
				continue
			}
			if _, ok := devices[parentDID]; !ok {
				continue
			}
			suffix := strings.TrimPrefix(match, ".")
			if parentChildren[parentDID] == nil {
				parentChildren[parentDID] = make(map[string]DeviceInfo)
			}
			parentChildren[parentDID][suffix] = device
			delete(devices, did)
		}
	}
	for parentDID, children := range parentChildren {
		parent := devices[parentDID]
		if parent.SubDevices == nil {
			parent.SubDevices = make(map[string]DeviceInfo, len(children))
		}
		for key, child := range children {
			parent.SubDevices[key] = child
		}
		devices[parentDID] = parent
	}
}

func normalizeHomeInfos(resp *GetHomeResponseData) (HomeInfos, error) {
	infos := HomeInfos{
		HomeList:      make(map[string]HomeInfo, len(resp.HomeList)),
		ShareHomeList: make(map[string]HomeInfo, len(resp.ShareHomeList)),
	}
	for _, home := range resp.HomeList {
		normalized, ok := normalizeHomeRecord(home)
		if !ok {
			continue
		}
		if infos.UID == "" {
			infos.UID = normalized.UID
		}
		infos.HomeList[normalized.HomeID] = normalized
	}
	for _, home := range resp.ShareHomeList {
		normalized, ok := normalizeHomeRecord(home)
		if !ok {
			continue
		}
		infos.ShareHomeList[normalized.HomeID] = normalized
	}
	return infos, nil
}

func mergeHomeExtras(infos *HomeInfos, extras []GetHomeCloudHome) {
	for _, home := range extras {
		homeID := home.ID
		for key, existing := range infos.HomeList {
			if key != homeID {
				continue
			}
			mergeHomeRecord(&existing, home)
			infos.HomeList[key] = existing
		}
		for key, existing := range infos.ShareHomeList {
			if key != homeID {
				continue
			}
			mergeHomeRecord(&existing, home)
			infos.ShareHomeList[key] = existing
		}
	}
}

func selectSceneHomes(infos HomeInfos, homeIDs []string) []HomeInfo {
	selected := make(map[string]HomeInfo)
	filterHomes := homeIDs != nil
	wanted := make(map[string]struct{}, len(homeIDs))
	for _, homeID := range homeIDs {
		if homeID == "" {
			continue
		}
		wanted[homeID] = struct{}{}
	}

	addHome := func(home HomeInfo) {
		if filterHomes {
			if _, ok := wanted[home.HomeID]; !ok {
				return
			}
		}
		selected[home.HomeID] = cloneHomeInfo(home)
	}

	for _, home := range infos.HomeList {
		addHome(home)
	}
	for _, home := range infos.ShareHomeList {
		addHome(home)
	}

	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]HomeInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, selected[id])
	}
	return out
}

func normalizeSceneRecord(home HomeInfo, raw GetManualSceneListItem) (SceneInfo, bool) {
	if raw.SceneID == "" || raw.SceneName == "" {
		return SceneInfo{}, false
	}
	updateTime, err := strconv.ParseInt(strings.TrimSpace(raw.UpdateTime), 10, 64)
	if err != nil {
		return SceneInfo{}, false
	}
	scene := SceneInfo{
		ID:         raw.SceneID,
		Name:       raw.SceneName,
		UID:        home.UID,
		HomeID:     raw.HomeID,
		HomeName:   home.HomeName,
		RoomID:     raw.RoomID,
		SceneType:  raw.SceneType,
		Icon:       raw.Icon,
		TemplateID: raw.TemplateID,
		UpdateTime: updateTime,
		RType:      raw.RType,
		Enabled:    raw.Enable,
		DeviceIDs:  append([]string(nil), raw.DIDs...),
		ProductIDs: append([]string(nil), raw.ProductIDs...),
	}
	if scene.HomeID == "" {
		scene.HomeID = home.HomeID
	}
	return scene, true
}

func normalizeHomeRecord(home GetHomeCloudHome) (HomeInfo, bool) {
	if home.ID == "" || home.Name == "" {
		return HomeInfo{}, false
	}
	info := HomeInfo{
		HomeID:    home.ID,
		HomeName:  home.Name,
		CityID:    strconv.FormatInt(home.CityID, 10),
		Longitude: home.Longitude,
		Latitude:  home.Latitude,
		Address:   home.Address,
		DIDs:      append([]string(nil), home.DIDs...),
		Rooms:     make(map[string]RoomInfo, len(home.RoomList)),
		UID:       strconv.FormatInt(home.UID, 10),
	}
	info.GroupID = CalcGroupID(info.UID, info.HomeID)
	for _, room := range home.RoomList {
		if room.ID == "" {
			continue
		}
		info.Rooms[room.ID] = RoomInfo{
			RoomID:   room.ID,
			RoomName: room.Name,
			DIDs:     append([]string(nil), room.DIDs...),
		}
	}
	return info, true
}

func mergeHomeRecord(dst *HomeInfo, home GetHomeCloudHome) {
	if dst == nil {
		return
	}
	dst.DIDs = appendUnique(dst.DIDs, home.DIDs...)
	for _, room := range home.RoomList {
		if room.ID == "" {
			continue
		}
		current := dst.Rooms[room.ID]
		if current.RoomID == "" {
			current.RoomID = room.ID
		}
		if current.RoomName == "" {
			current.RoomName = room.Name
		}
		current.DIDs = appendUnique(current.DIDs, room.DIDs...)
		dst.Rooms[room.ID] = current
	}
}

func normalizeDeviceRecord(raw GetDeviceListPageDevice) (DeviceInfo, bool) {
	if raw.DID == "" || raw.Name == "" || raw.SpecType == "" || raw.Model == "" {
		return DeviceInfo{}, false
	}
	if strings.HasPrefix(raw.DID, "miwifi.") {
		return DeviceInfo{}, false
	}
	if _, unsupported := unsupportedCloudModels[raw.Model]; unsupported {
		return DeviceInfo{}, false
	}

	owner := raw.Owner
	var normalizedOwner *DeviceOwner
	if owner != nil {
		userID := strconv.FormatInt(owner.UserID, 10)
		if userID != "" || owner.Nickname != "" {
			normalizedOwner = &DeviceOwner{
				UserID:   userID,
				Nickname: owner.Nickname,
			}
		}
	}

	localIP := raw.LocalIP
	if localIP == "" {
		localIP = raw.LocalIPAlt
	}

	device := DeviceInfo{
		DID:             raw.DID,
		UID:             strconv.FormatInt(raw.UID, 10),
		Name:            raw.Name,
		URN:             raw.SpecType,
		Model:           raw.Model,
		ConnectType:     raw.PID,
		Token:           raw.Token,
		Online:          raw.IsOnline,
		Icon:            raw.Icon,
		ParentID:        raw.ParentID,
		Manufacturer:    manufacturerFromModel(raw.Model),
		VoiceCtrl:       raw.VoiceCtrl,
		RSSI:            raw.RSSI,
		Owner:           normalizedOwner,
		PID:             raw.PID,
		LocalIP:         localIP,
		SSID:            raw.SSID,
		BSSID:           raw.BSSID,
		OrderTime:       raw.OrderTime,
		FWVersion:       raw.Extra.FWVersion,
		PincodeRequired: raw.Extra.IsSetPincode != 0,
		PincodeType:     raw.Extra.PincodeType,
	}
	return device, true
}

func (c *CloudClient) checkCameraPincode(ctx context.Context, path string, did string, pincode string) (bool, error) {
	did = strings.TrimSpace(did)
	pincode = strings.TrimSpace(pincode)
	if did == "" {
		return false, &Error{Code: ErrInvalidArgument, Op: "check camera pincode", Msg: "did is empty"}
	}
	if len(pincode) != 4 {
		return false, &Error{Code: ErrInvalidArgument, Op: "check camera pincode", Msg: "pincode must be 4 digits"}
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message,omitempty"`
		Result  bool   `json:"result"`
	}
	if err := c.postJSON(ctx, path, struct {
		DID     string `json:"did"`
		Pincode string `json:"pincode"`
	}{DID: did, Pincode: pincode}, &resp); err != nil {
		return false, err
	}
	if resp.Code != 0 {
		return false, fmt.Errorf("check camera pincode failed: code %d", resp.Code)
	}
	return resp.Result, nil
}

func parseCameraVendorInfo(payload json.RawMessage) (CameraVendorInfo, error) {
	if len(payload) == 0 {
		return CameraVendorInfo{}, &Error{Code: ErrInvalidResponse, Op: "parse camera vendor info", Msg: "empty payload"}
	}
	var raw struct {
		PublicKey     string          `json:"public_key"`
		Sign          string          `json:"sign"`
		P2PID         string          `json:"p2p_id"`
		InitString    string          `json:"init_string"`
		SupportVendor string          `json:"support_vendor"`
		VendorName    string          `json:"vendor_name"`
		Vendor        json.RawMessage `json:"vendor"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return CameraVendorInfo{}, Wrap(ErrInvalidResponse, "parse camera vendor info", err)
	}
	info := CameraVendorInfo{
		PublicKey:     raw.PublicKey,
		Sign:          raw.Sign,
		P2PID:         raw.P2PID,
		InitString:    raw.InitString,
		SupportVendor: firstNonEmpty(raw.SupportVendor, raw.VendorName),
	}
	if len(raw.Vendor) == 0 {
		return info, nil
	}

	var rawVendor map[string]any
	if err := json.Unmarshal(raw.Vendor, &rawVendor); err == nil {
		info.RawVendor = rawVendor
	}

	var vendor struct {
		Vendor       int             `json:"vendor"`
		VendorParams json.RawMessage `json:"vendor_params"`
	}
	if err := json.Unmarshal(raw.Vendor, &vendor); err != nil {
		return info, nil
	}
	info.VendorID = vendor.Vendor
	if info.SupportVendor == "" {
		info.SupportVendor = cameraVendorName(vendor.Vendor)
	}
	if len(vendor.VendorParams) == 0 {
		return info, nil
	}

	var rawParams map[string]any
	if err := json.Unmarshal(vendor.VendorParams, &rawParams); err == nil {
		info.RawVendorParam = rawParams
	}
	var params struct {
		P2PID      string `json:"p2p_id"`
		InitString string `json:"init_string"`
	}
	if err := json.Unmarshal(vendor.VendorParams, &params); err == nil {
		info.P2PID = firstNonEmpty(info.P2PID, params.P2PID)
		info.InitString = firstNonEmpty(info.InitString, params.InitString)
	}
	return info, nil
}

func cameraVendorName(id int) string {
	switch id {
	case 1:
		return "tutk"
	case 3:
		return "agora"
	case 4:
		return "cs2"
	case 6:
		return "mtp"
	default:
		return ""
	}
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func manufacturerFromModel(model string) string {
	if model == "" {
		return ""
	}
	if idx := strings.IndexByte(model, '.'); idx > 0 {
		return model[:idx]
	}
	return model
}

func cloneHomeInfo(info HomeInfo) HomeInfo {
	clone := info
	clone.DIDs = append([]string(nil), info.DIDs...)
	clone.Rooms = make(map[string]RoomInfo, len(info.Rooms))
	for roomID, room := range info.Rooms {
		clone.Rooms[roomID] = RoomInfo{
			RoomID:   room.RoomID,
			RoomName: room.RoomName,
			DIDs:     append([]string(nil), room.DIDs...),
		}
	}
	return clone
}

func (c *CloudClient) postJSON(ctx context.Context, path string, reqBody any, respBody any) error {
	if c == nil {
		return &Error{Code: ErrInvalidArgument, Op: "cloud request", Msg: "client is nil"}
	}
	if c.http == nil {
		return &Error{Code: ErrInvalidArgument, Op: "cloud request", Msg: "http client is nil"}
	}
	if c.tokens == nil {
		return &Error{Code: ErrInvalidArgument, Op: "cloud request", Msg: "token provider is nil"}
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return Wrap(ErrInvalidArgument, "marshal cloud request", err)
	}
	tokenProvider := c.tokenProvider()
	token, err := tokenProvider.AccessToken(ctx)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Host = c.cloudHost()
	req.Header.Set("Authorization", "Bearer"+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client-AppId", c.clientID())
	req.Header.Set("X-Client-BizId", cloudBizID)

	resp, err := c.http.Do(req)
	if err != nil {
		return Wrap(ErrTransportFailure, "cloud request", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return &Error{Code: ErrInvalidAccessToken, Op: "cloud request", Msg: "unauthorized"}
	}
	if resp.StatusCode != http.StatusOK {
		return &Error{Code: ErrTransportFailure, Op: "cloud request", Msg: fmt.Sprintf("status %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, respBody); err != nil {
		return Wrap(ErrDecodeResponse, "decode cloud response", err)
	}
	return nil
}

func (c *CloudClient) baseURL() string {
	return "https://" + c.cloudHost()
}

func (c *CloudClient) cloudHost() string {
	c.mu.RLock()
	server := c.cfg.CloudServer
	c.mu.RUnlock()
	if server == "" || server == "cn" {
		return defaultOAuthHost
	}
	return server + "." + defaultOAuthHost
}

func (c *CloudClient) clientID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.ClientID
}

func (c *CloudClient) tokenProvider() TokenProvider {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tokens
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func appendUnique(base []string, values ...string) []string {
	seen := make(map[string]struct{}, len(base))
	for _, value := range base {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		base = append(base, value)
	}
	return base
}

func sortedKeys(values map[string]DeviceInfo) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
