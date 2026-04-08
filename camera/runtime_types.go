package camera

import "context"

// VideoQuality identifies one Xiaomi camera video quality preset.
type VideoQuality int

const (
	// VideoQualityLow requests the low-quality stream.
	VideoQualityLow VideoQuality = 1
	// VideoQualityHigh requests the high-quality stream.
	VideoQualityHigh VideoQuality = 3
)

// Status identifies the runtime connection state of one camera instance.
type Status int

const (
	// StatusDisconnected reports that the camera is stopped or not connected.
	StatusDisconnected Status = 1
	// StatusConnecting reports that the camera start sequence is in progress.
	StatusConnecting Status = 2
	// StatusReconnecting reports that the camera runtime is reconnecting.
	StatusReconnecting Status = 3
	// StatusConnected reports that the camera is connected and emitting frames.
	StatusConnected Status = 4
	// StatusError reports that the camera runtime encountered an unrecoverable error.
	StatusError Status = 5
)

// Codec identifies one runtime raw-frame codec.
type Codec int

const (
	// CodecVideoH264 reports one H264 raw video frame.
	CodecVideoH264 Codec = 4
	// CodecVideoH265 reports one H265 raw video frame.
	CodecVideoH265 Codec = 5
	// CodecAudioPCM reports one PCM raw audio frame.
	CodecAudioPCM Codec = 1024
	// CodecAudioG711U reports one G.711 u-law raw audio frame.
	CodecAudioG711U Codec = 1026
	// CodecAudioG711A reports one G.711 a-law raw audio frame.
	CodecAudioG711A Codec = 1027
	// CodecAudioOpus reports one Opus raw audio frame.
	CodecAudioOpus Codec = 1032
)

// FrameType identifies one raw video frame type.
type FrameType int

const (
	// FrameTypeP reports one non-keyframe video frame.
	FrameTypeP FrameType = 0
	// FrameTypeI reports one keyframe video frame.
	FrameTypeI FrameType = 1
)

// Info describes the runtime-facing camera configuration.
type Info struct {
	CameraID        string
	Model           string
	ChannelCount    int
	Token           string
	PincodeRequired bool
	PincodeType     int
}

// StartOptions describes one camera runtime start request.
type StartOptions struct {
	VideoQualities  []VideoQuality
	PinCode         string
	EnableAudio     bool
	EnableReconnect bool
}

// RuntimeOptions configures the camera runtime.
type RuntimeOptions struct {
	AccessToken string
	Factory     DriverFactory
}

// DriverFactory constructs a driver for one camera instance.
type DriverFactory interface {
	New(Info) Driver
}

// Driver is the transport-specific runtime worker for one camera instance.
type Driver interface {
	Start(context.Context, StartOptions, EventSink) error
	Stop() error
}

// Frame is one runtime raw audio or video frame.
type Frame struct {
	Codec     Codec
	Length    uint32
	Timestamp uint64
	Sequence  uint32
	FrameType FrameType
	Channel   int
	Data      []byte
}

// DecodedFrame is one runtime decoded media frame payload.
type DecodedFrame struct {
	Timestamp uint64
	Channel   int
	Data      []byte
}

// EventSink receives runtime events from a driver.
type EventSink interface {
	UpdateStatus(Status)
	EmitRawVideo(Frame)
	EmitRawAudio(Frame)
	EmitJPEG(JPEGFrame)
	EmitPCM(DecodedFrame)
}

// Compatibility aliases for the external Xiaomi camera runtime API.
type CameraVideoQuality = VideoQuality
type CameraStatus = Status
type CameraCodec = Codec
type CameraFrameType = FrameType
type CameraInfo = Info
type CameraStartOptions = StartOptions
type CameraRuntimeOptions = RuntimeOptions
type CameraDriverFactory = DriverFactory
type CameraDriver = Driver
type CameraEventSink = EventSink
type CameraFrame = Frame
type CameraDecodedFrame = DecodedFrame

const (
	CameraVideoQualityLow  = VideoQualityLow
	CameraVideoQualityHigh = VideoQualityHigh

	CameraStatusDisconnected = StatusDisconnected
	CameraStatusConnecting   = StatusConnecting
	CameraStatusReconnecting = StatusReconnecting
	CameraStatusConnected    = StatusConnected
	CameraStatusError        = StatusError

	CameraCodecVideoH264  = CodecVideoH264
	CameraCodecVideoH265  = CodecVideoH265
	CameraCodecAudioPCM   = CodecAudioPCM
	CameraCodecAudioG711U = CodecAudioG711U
	CameraCodecAudioG711A = CodecAudioG711A
	CameraCodecAudioOpus  = CodecAudioOpus

	CameraFrameTypeP = FrameTypeP
	CameraFrameTypeI = FrameTypeI
)
