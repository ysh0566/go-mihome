package miot

import "time"

// AccountProfileResponse is the typed response from Xiaomi account profile API.
type AccountProfileResponse struct {
	Code        int            `json:"code"`
	Result      string         `json:"result"`
	Description string         `json:"description"`
	TraceID     string         `json:"traceId"`
	Data        AccountProfile `json:"data"`
}

// AccountProfile describes one Xiaomi account profile payload.
type AccountProfile struct {
	UnionID        string `json:"unionId"`
	MiliaoNick     string `json:"miliaoNick"`
	MiliaoIcon     string `json:"miliaoIcon"`
	MiliaoIcon75   string `json:"miliaoIcon_75"`
	MiliaoIcon90   string `json:"miliaoIcon_90"`
	MiliaoIcon120  string `json:"miliaoIcon_120"`
	MiliaoIcon320  string `json:"miliaoIcon_320"`
	MiliaoIconOrig string `json:"miliaoIcon_orig"`
}

// OAuthTokenResponse is the typed MIoT OAuth token envelope.
type OAuthTokenResponse struct {
	Code        int              `json:"code"`
	Description string           `json:"description,omitempty"`
	Result      OAuthTokenResult `json:"result"`
}

// OAuthTokenResult is the transport payload returned by the token endpoint.
type OAuthTokenResult struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	MacKey       string `json:"mac_key,omitempty"`
}

// OAuthToken is the normalized OAuth token model used by the Go library.
type OAuthToken struct {
	AccessToken  string
	RefreshToken string
	MacKey       string
	ExpiresIn    int
	ExpiresAt    time.Time
}

// GetCentralCertRequest is the typed request body for the Xiaomi central certificate API.
type GetCentralCertRequest struct {
	CSR string `json:"csr"`
}

// GetCentralCertResponse is the typed Xiaomi central certificate response envelope.
type GetCentralCertResponse struct {
	Code    int                   `json:"code"`
	Message string                `json:"message,omitempty"`
	Result  *GetCentralCertResult `json:"result,omitempty"`
}

// GetCentralCertResult is the typed central certificate payload.
type GetCentralCertResult struct {
	Cert string `json:"cert"`
}

// GetHomeRequest is the typed request body for the Xiaomi gethome API.
type GetHomeRequest struct {
	Limit         int  `json:"limit"`
	FetchShare    bool `json:"fetch_share"`
	FetchShareDev bool `json:"fetch_share_dev"`
	PlatForm      int  `json:"plat_form"`
	AppVer        int  `json:"app_ver"`
}

// GetHomeResponse is the typed Xiaomi gethome API response envelope.
type GetHomeResponse struct {
	Code    int                  `json:"code"`
	Message string               `json:"message,omitempty"`
	Result  *GetHomeResponseData `json:"result,omitempty"`
}

// GetHomeResponseData is the typed payload from the Xiaomi gethome API.
type GetHomeResponseData struct {
	HomeList      []GetHomeCloudHome `json:"homelist"`
	ShareHomeList []GetHomeCloudHome `json:"share_home_list"`
	HasMore       bool               `json:"has_more"`
	MaxID         string             `json:"max_id"`
}

// GetHomeCloudHome is the raw Xiaomi home record from the cloud API.
type GetHomeCloudHome struct {
	ID        string             `json:"id"`
	UID       int64              `json:"uid"`
	Name      string             `json:"name"`
	CityID    int64              `json:"city_id"`
	Longitude float64            `json:"longitude"`
	Latitude  float64            `json:"latitude"`
	Address   string             `json:"address"`
	DIDs      []string           `json:"dids"`
	RoomList  []GetHomeCloudRoom `json:"roomlist"`
}

// GetHomeCloudRoom is the raw Xiaomi room record from the cloud API.
type GetHomeCloudRoom struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	DIDs []string `json:"dids"`
}

// GetDevRoomPageRequest is the typed request body for the Xiaomi get_dev_room_page API.
type GetDevRoomPageRequest struct {
	StartID string `json:"start_id,omitempty"`
	Limit   int    `json:"limit"`
}

// GetDevRoomPageResponse is the typed Xiaomi get_dev_room_page API response envelope.
type GetDevRoomPageResponse struct {
	Code    int                         `json:"code"`
	Message string                      `json:"message,omitempty"`
	Result  *GetDevRoomPageResponseData `json:"result,omitempty"`
}

// GetDevRoomPageResponseData is the typed payload from get_dev_room_page.
type GetDevRoomPageResponseData struct {
	Info    []GetHomeCloudHome `json:"info"`
	HasMore bool               `json:"has_more"`
	MaxID   string             `json:"max_id"`
}

// GetDeviceListPageRequest is the typed request body for the Xiaomi device_list_page API.
type GetDeviceListPageRequest struct {
	Limit          int      `json:"limit"`
	GetSplitDevice bool     `json:"get_split_device"`
	GetThirdDevice bool     `json:"get_third_device"`
	DIDs           []string `json:"dids"`
	StartDID       string   `json:"start_did,omitempty"`
}

// GetDeviceListPageResponse is the typed Xiaomi device_list_page API response envelope.
type GetDeviceListPageResponse struct {
	Code    int                            `json:"code"`
	Message string                         `json:"message,omitempty"`
	Result  *GetDeviceListPageResponseData `json:"result,omitempty"`
}

// GetDeviceListPageResponseData is the typed payload from the device list API.
type GetDeviceListPageResponseData struct {
	List         []GetDeviceListPageDevice `json:"list"`
	HasMore      bool                      `json:"has_more"`
	NextStartDID string                    `json:"next_start_did"`
}

// GetDeviceListPageDevice is the raw Xiaomi device record from the cloud API.
type GetDeviceListPageDevice struct {
	DID                string                  `json:"did"`
	UID                int64                   `json:"uid"`
	Name               string                  `json:"name"`
	SpecType           string                  `json:"spec_type"`
	Model              string                  `json:"model"`
	CityID             int64                   `json:"city_id,omitempty"`
	PermitLevel        int                     `json:"permitLevel,omitempty"`
	RemoteControllable bool                    `json:"remote_controllable,omitempty"`
	PID                int                     `json:"pid"`
	Token              string                  `json:"token"`
	IsOnline           bool                    `json:"isOnline"`
	Icon               string                  `json:"icon"`
	ParentID           string                  `json:"parent_id"`
	VoiceCtrl          int                     `json:"voice_ctrl"`
	RSSI               int                     `json:"rssi"`
	Owner              *GetDeviceListPageOwner `json:"owner"`
	LocalIP            string                  `json:"local_ip,omitempty"`
	LocalIPAlt         string                  `json:"localip,omitempty"`
	Longitude          string                  `json:"longitude,omitempty"`
	Latitude           string                  `json:"latitude,omitempty"`
	SSID               string                  `json:"ssid"`
	BSSID              string                  `json:"bssid"`
	OrderTime          int64                   `json:"orderTime"`
	Extra              GetDeviceListPageExtra  `json:"extra"`
}

// GetDeviceListPageOwner is the typed shared-device owner record.
type GetDeviceListPageOwner struct {
	UserID   int64  `json:"userid"`
	Nickname string `json:"nickname"`
}

// GetDeviceListPageExtra is the typed extra device record from the cloud API.
type GetDeviceListPageExtra struct {
	FWVersion    string `json:"fw_version"`
	IsSetPincode int    `json:"isSetPincode"`
	PincodeType  int    `json:"pincodeType"`
}

// GetManualSceneListRequest is the typed request body for the Xiaomi manual scene list API.
type GetManualSceneListRequest struct {
	HomeID   string `json:"home_id"`
	OwnerUID string `json:"owner_uid"`
	Source   string `json:"source"`
	GetType  int    `json:"get_type"`
}

// GetManualSceneListResponse is the typed Xiaomi manual scene list API response envelope.
type GetManualSceneListResponse struct {
	Code    int                      `json:"code"`
	Message string                   `json:"message,omitempty"`
	Result  []GetManualSceneListItem `json:"result"`
}

// GetManualSceneListItem is the raw Xiaomi scene record from the cloud API.
type GetManualSceneListItem struct {
	SceneID    string   `json:"scene_id"`
	SceneName  string   `json:"scene_name"`
	SceneType  int      `json:"scene_type"`
	Icon       string   `json:"icon"`
	UpdateTime string   `json:"update_time"`
	TemplateID string   `json:"template_id"`
	RoomID     string   `json:"room_id"`
	RType      int      `json:"r_type"`
	HomeID     string   `json:"home_id"`
	Enable     bool     `json:"enable"`
	DIDs       []string `json:"dids"`
	ProductIDs []string `json:"pd_ids"`
}

// RunSceneRequest is the typed Xiaomi cloud request envelope for manual scene execution.
type RunSceneRequest struct {
	SceneID   string `json:"scene_id"`
	OwnerUID  string `json:"owner_uid"`
	SceneType int    `json:"scene_type"`
	HomeID    string `json:"home_id,omitempty"`
	RoomID    string `json:"room_id,omitempty"`
}

// RunSceneResponse is the typed Xiaomi cloud response envelope for manual scene execution.
type RunSceneResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
	Result  bool   `json:"result"`
}

// GetPropsResponse is the typed Xiaomi get properties response envelope.
type GetPropsResponse struct {
	Code    int              `json:"code"`
	Message string           `json:"message,omitempty"`
	Result  []PropertyResult `json:"result"`
}

// SetPropsResponse is the typed Xiaomi set properties response envelope.
type SetPropsResponse struct {
	Code    int                 `json:"code"`
	Message string              `json:"message,omitempty"`
	Result  []SetPropertyResult `json:"result"`
}

// actionRequestEnvelope wraps one action request in the Xiaomi cloud payload format.
type actionRequestEnvelope struct {
	Params ActionRequest `json:"params"`
}

// ActionResponse is the typed Xiaomi action response envelope.
type ActionResponse struct {
	Code    int          `json:"code"`
	Message string       `json:"message,omitempty"`
	Result  ActionResult `json:"result"`
}
