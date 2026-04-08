package camera

import "time"

// SupportInfo describes Xiaomi camera support metadata for one model.
type SupportInfo struct {
	ChannelCount int
	Name         string
	Vendor       string
}

// Target is the normalized camera descriptor used by probe/session helpers.
type Target struct {
	CameraID    string
	Name        string
	Model       string
	HomeID      string
	Home        string
	RoomID      string
	Room        string
	Online      bool
	SupportInfo SupportInfo
}

// Profile selects a camera resolver family for one model.
type Profile struct {
	Name        string
	ExactModels []string
	Prefixes    []string
}

// Session is the normalized result of resolving one camera stream session.
type Session struct {
	CameraID     string
	Model        string
	ProfileName  string
	ResolverName string
	Transport    string
	SessionID    string
	StreamURL    string
	Codec        string
	VideoQuality VideoQuality
	Token        string
}

// AccessUnit is one elementary media access unit from a probe transport.
type AccessUnit struct {
	Codec            string
	Payload          []byte
	PresentationTime time.Duration
}

// JPEGFrame is one decoded JPEG snapshot frame.
type JPEGFrame struct {
	Channel    int
	Payload    []byte
	CapturedAt time.Time
	Width      int
	Height     int
}
