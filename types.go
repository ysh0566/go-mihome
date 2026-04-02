package miot

// HomeInfos is the normalized cloud home listing returned by GetHomeInfos.
type HomeInfos struct {
	UID           string
	HomeList      map[string]HomeInfo
	ShareHomeList map[string]HomeInfo
}

// HomeInfo is the normalized representation of a Xiaomi home or shared home.
type HomeInfo struct {
	HomeID    string
	HomeName  string
	CityID    string
	Longitude float64
	Latitude  float64
	Address   string
	DIDs      []string
	Rooms     map[string]RoomInfo
	GroupID   string
	UID       string
}

// RoomInfo is the normalized representation of a Xiaomi room.
type RoomInfo struct {
	RoomID   string
	RoomName string
	DIDs     []string
}

// DeviceSnapshot is the normalized cloud device listing returned by GetDevices.
type DeviceSnapshot struct {
	UID     string
	Homes   map[string]map[string]HomeInfo
	Devices map[string]DeviceInfo
}

// DeviceInfo is the normalized representation of a Xiaomi device.
type DeviceInfo struct {
	DID          string
	UID          string
	Name         string
	URN          string
	Model        string
	ConnectType  int
	Token        string
	Online       bool
	Icon         string
	ParentID     string
	Manufacturer string
	VoiceCtrl    int
	RSSI         int
	Owner        *DeviceOwner
	PID          int
	LocalIP      string
	SSID         string
	BSSID        string
	OrderTime    int64
	FWVersion    string
	HomeID       string
	HomeName     string
	RoomID       string
	RoomName     string
	GroupID      string
	SubDevices   map[string]DeviceInfo
}

// DeviceOwner is the normalized representation of a shared-device owner.
type DeviceOwner struct {
	UserID   string
	Nickname string
}

// PropertyQuery identifies one MIoT property by device and property coordinates.
type PropertyQuery struct {
	DID  string `json:"did"`
	SIID int    `json:"siid"`
	PIID int    `json:"piid"`
}

// PropertySubscription identifies one property broadcast stream.
type PropertySubscription struct {
	DID  string
	SIID int
	PIID int
}

// EventSubscription identifies one event broadcast stream.
type EventSubscription struct {
	DID  string
	SIID int
	EIID int
}

// GetPropsRequest batches one or more property queries.
type GetPropsRequest struct {
	DataSource int             `json:"datasource,omitempty"`
	Params     []PropertyQuery `json:"params"`
}

// PropertyResult is the typed property payload returned by the Xiaomi cloud API.
type PropertyResult struct {
	DID        string    `json:"did"`
	IID        string    `json:"iid,omitempty"`
	SIID       int       `json:"siid"`
	PIID       int       `json:"piid"`
	Value      SpecValue `json:"value"`
	Code       int       `json:"code"`
	UpdateTime int64     `json:"updateTime,omitempty"`
	ExecTime   int64     `json:"exe_time,omitempty"`
}

// PropertyEventHandler handles one typed property change notification.
type PropertyEventHandler func(PropertyResult)

// SetPropertyRequest describes one cloud property write request.
type SetPropertyRequest struct {
	DID   string    `json:"did"`
	SIID  int       `json:"siid"`
	PIID  int       `json:"piid"`
	Value SpecValue `json:"value"`
}

// SetPropsRequest batches one or more property write requests.
type SetPropsRequest struct {
	Params []SetPropertyRequest `json:"params"`
}

// SetPropertyResult describes one cloud property write result.
type SetPropertyResult struct {
	DID  string `json:"did"`
	SIID int    `json:"siid"`
	PIID int    `json:"piid"`
	Code int    `json:"code"`
}

// ActionRequest describes one MIoT action invocation.
type ActionRequest struct {
	DID   string      `json:"did"`
	SIID  int         `json:"siid"`
	AIID  int         `json:"aiid"`
	Input []SpecValue `json:"in"`
}

// ActionResult is the typed MIoT action response payload.
type ActionResult struct {
	Code   int         `json:"code"`
	Output []SpecValue `json:"out,omitempty"`
}

// EventOccurrence is one typed MIoT event notification.
type EventOccurrence struct {
	DID       string
	SIID      int
	EIID      int
	Arguments []SpecValue
	From      string
}

// EventHandler handles one typed MIoT event notification.
type EventHandler func(EventOccurrence)

// DeviceState identifies a device connectivity state.
type DeviceState string

const (
	// DeviceStateDisable reports that the device is disabled.
	DeviceStateDisable DeviceState = "disable"
	// DeviceStateOffline reports that the device is offline.
	DeviceStateOffline DeviceState = "offline"
	// DeviceStateOnline reports that the device is online.
	DeviceStateOnline DeviceState = "online"
)

// DeviceStateHandler handles one typed device state update.
type DeviceStateHandler func(did string, state DeviceState)
