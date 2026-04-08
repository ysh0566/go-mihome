package camera

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	// ErrRuntimeUnavailable reports that the camera runtime is missing required pieces.
	ErrRuntimeUnavailable = errors.New("miot camera runtime unavailable")
)

// Runtime owns camera instances and their lifecycle.
type Runtime struct {
	mu      sync.Mutex
	token   string
	factory DriverFactory
	cameras map[string]*Instance
}

// Instance is one managed camera runtime instance.
type Instance struct {
	info   Info
	driver Driver

	mu            sync.Mutex
	status        Status
	nextCallback  int
	statusChanged map[int]func(string, Status)
	rawVideo      map[int]frameCallback
	rawAudio      map[int]frameCallback
	jpegChanged   map[int]jpegCallback
	decodedJPEG   map[int]decodedCallback
	decodedPCM    map[int]decodedCallback
}

type jpegCallback struct {
	channel  int
	callback func(string, JPEGFrame)
}

type frameCallback struct {
	channel  int
	callback func(string, Frame)
}

type decodedCallback struct {
	channel  int
	callback func(string, DecodedFrame)
}

type noopDriverFactory struct{}

func (noopDriverFactory) New(Info) Driver {
	return noopDriver{}
}

type noopDriver struct{}

func (noopDriver) Start(context.Context, StartOptions, EventSink) error {
	return ErrRuntimeUnavailable
}

func (noopDriver) Stop() error {
	return nil
}

// NewRuntime constructs a new camera runtime.
func NewRuntime(options RuntimeOptions) *Runtime {
	factory := options.Factory
	if factory == nil {
		factory = noopDriverFactory{}
	}
	return &Runtime{
		token:   strings.TrimSpace(options.AccessToken),
		factory: factory,
		cameras: map[string]*Instance{},
	}
}

// NewCameraRuntime constructs the compatibility runtime wrapper.
func NewCameraRuntime(options RuntimeOptions) *Runtime {
	return NewRuntime(options)
}

// UpdateAccessToken updates the runtime access token tracked by the caller.
func (r *Runtime) UpdateAccessToken(accessToken string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.token = strings.TrimSpace(accessToken)
	r.mu.Unlock()
}

// Create returns a managed instance for the provided camera info.
func (r *Runtime) Create(info Info) (*Instance, error) {
	if r == nil {
		return nil, ErrRuntimeUnavailable
	}

	info = normalizeInfo(info)
	if info.CameraID == "" || info.Model == "" {
		return nil, fmt.Errorf("%w: camera id and model are required", ErrRuntimeUnavailable)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if current, ok := r.cameras[info.CameraID]; ok {
		return current, nil
	}

	camera := &Instance{
		info:          info,
		driver:        r.factory.New(info),
		status:        StatusDisconnected,
		statusChanged: map[int]func(string, Status){},
		rawVideo:      map[int]frameCallback{},
		rawAudio:      map[int]frameCallback{},
		jpegChanged:   map[int]jpegCallback{},
		decodedJPEG:   map[int]decodedCallback{},
		decodedPCM:    map[int]decodedCallback{},
	}
	r.cameras[info.CameraID] = camera
	return camera, nil
}

// CreateCamera is the compatibility wrapper for Create.
func (r *Runtime) CreateCamera(info Info) (*Instance, error) {
	return r.Create(info)
}

// Get returns the managed instance for one camera identifier.
func (r *Runtime) Get(cameraID string) *Instance {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cameras[strings.TrimSpace(cameraID)]
}

// GetCamera is the compatibility wrapper for Get.
func (r *Runtime) GetCamera(cameraID string) *Instance {
	return r.Get(cameraID)
}

// Destroy removes and stops the managed instance for one camera identifier.
func (r *Runtime) Destroy(cameraID string) error {
	if r == nil {
		return nil
	}
	cameraID = strings.TrimSpace(cameraID)

	r.mu.Lock()
	camera := r.cameras[cameraID]
	delete(r.cameras, cameraID)
	r.mu.Unlock()

	if camera == nil {
		return nil
	}
	return camera.Stop()
}

// DestroyCamera is the compatibility wrapper for Destroy.
func (r *Runtime) DestroyCamera(cameraID string) error {
	return r.Destroy(cameraID)
}

// Info returns the normalized instance metadata.
func (c *Instance) Info() Info {
	if c == nil {
		return Info{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// Status returns the current runtime status.
func (c *Instance) Status() Status {
	if c == nil {
		return StatusDisconnected
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Start validates options, updates state, and delegates to the driver.
func (c *Instance) Start(ctx context.Context, options StartOptions) error {
	if c == nil || c.driver == nil {
		return ErrRuntimeUnavailable
	}
	options = c.normalizeStartOptions(options)
	options.PinCode = strings.TrimSpace(options.PinCode)
	if c.Info().PincodeRequired && options.PinCode == "" {
		return fmt.Errorf("%w: pincode is required", ErrRuntimeUnavailable)
	}
	if options.PinCode != "" && !isValidPinCode(options.PinCode) {
		return fmt.Errorf("%w: pincode must be 4 digits", ErrRuntimeUnavailable)
	}

	c.UpdateStatus(StatusConnecting)
	if err := c.driver.Start(ctx, options, c); err != nil {
		c.UpdateStatus(StatusError)
		return err
	}
	return nil
}

// Stop stops the underlying driver and marks the instance disconnected.
func (c *Instance) Stop() error {
	if c == nil || c.driver == nil {
		return nil
	}
	err := c.driver.Stop()
	c.UpdateStatus(StatusDisconnected)
	return err
}

// RegisterStatusChanged registers one status callback and returns its id.
func (c *Instance) RegisterStatusChanged(callback func(string, Status)) int {
	if c == nil || callback == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextCallback
	c.nextCallback++
	c.statusChanged[id] = callback
	return id
}

// UnregisterStatusChanged removes one status callback by id.
func (c *Instance) UnregisterStatusChanged(id int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.statusChanged, id)
	c.mu.Unlock()
}

// RegisterRawVideo registers one raw-video callback for a specific channel.
func (c *Instance) RegisterRawVideo(channel int, callback func(string, Frame)) int {
	return c.registerFrameCallback(&c.rawVideo, channel, callback)
}

// UnregisterRawVideo removes one raw-video callback by id.
func (c *Instance) UnregisterRawVideo(id int) {
	c.unregisterFrameCallback(c.rawVideo, id)
}

// RegisterRawAudio registers one raw-audio callback for a specific channel.
func (c *Instance) RegisterRawAudio(channel int, callback func(string, Frame)) int {
	return c.registerFrameCallback(&c.rawAudio, channel, callback)
}

// UnregisterRawAudio removes one raw-audio callback by id.
func (c *Instance) UnregisterRawAudio(id int) {
	c.unregisterFrameCallback(c.rawAudio, id)
}

// RegisterJPEG registers one JPEG callback for a specific channel.
func (c *Instance) RegisterJPEG(channel int, callback func(string, JPEGFrame)) int {
	if c == nil || callback == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextCallback
	c.nextCallback++
	c.jpegChanged[id] = jpegCallback{channel: channel, callback: callback}
	return id
}

// UnregisterJPEG removes one JPEG callback by id.
func (c *Instance) UnregisterJPEG(id int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.jpegChanged, id)
	c.mu.Unlock()
}

// RegisterDecodedJPEG registers one decoded-JPEG callback for a specific channel.
func (c *Instance) RegisterDecodedJPEG(channel int, callback func(string, DecodedFrame)) int {
	return c.registerDecodedCallback(&c.decodedJPEG, channel, callback)
}

// UnregisterDecodedJPEG removes one decoded-JPEG callback by id.
func (c *Instance) UnregisterDecodedJPEG(id int) {
	c.unregisterDecodedCallback(c.decodedJPEG, id)
}

// RegisterDecodedPCM registers one decoded-PCM callback for a specific channel.
func (c *Instance) RegisterDecodedPCM(channel int, callback func(string, DecodedFrame)) int {
	return c.registerDecodedCallback(&c.decodedPCM, channel, callback)
}

// UnregisterDecodedPCM removes one decoded-PCM callback by id.
func (c *Instance) UnregisterDecodedPCM(id int) {
	c.unregisterDecodedCallback(c.decodedPCM, id)
}

// UpdateStatus updates the current status and dispatches callbacks.
func (c *Instance) UpdateStatus(status Status) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.status = status
	cameraID := c.info.CameraID
	callbacks := make([]func(string, Status), 0, len(c.statusChanged))
	for _, callback := range c.statusChanged {
		callbacks = append(callbacks, callback)
	}
	c.mu.Unlock()

	for _, callback := range callbacks {
		callback(cameraID, status)
	}
}

// EmitRawVideo dispatches one raw video frame to callbacks registered for its channel.
func (c *Instance) EmitRawVideo(frame Frame) {
	c.emitFrame(c.rawVideo, frame)
}

// EmitRawAudio dispatches one raw audio frame to callbacks registered for its channel.
func (c *Instance) EmitRawAudio(frame Frame) {
	c.emitFrame(c.rawAudio, frame)
}

// EmitJPEG dispatches one JPEG frame to callbacks registered for its channel.
func (c *Instance) EmitJPEG(frame JPEGFrame) {
	if c == nil {
		return
	}
	c.mu.Lock()
	cameraID := c.info.CameraID
	callbacks := make([]func(string, JPEGFrame), 0, len(c.jpegChanged))
	decodedCallbacks := make([]func(string, DecodedFrame), 0, len(c.decodedJPEG))
	for _, callback := range c.jpegChanged {
		if callback.callback == nil {
			continue
		}
		if callback.channel >= 0 && callback.channel != frame.Channel {
			continue
		}
		callbacks = append(callbacks, callback.callback)
	}
	for _, callback := range c.decodedJPEG {
		if callback.callback == nil {
			continue
		}
		if callback.channel >= 0 && callback.channel != frame.Channel {
			continue
		}
		decodedCallbacks = append(decodedCallbacks, callback.callback)
	}
	c.mu.Unlock()

	clonedFrame := cloneJPEGFrame(frame)
	for _, callback := range callbacks {
		callback(cameraID, cloneJPEGFrame(clonedFrame))
	}
	decoded := decodedFrameFromJPEG(clonedFrame)
	for _, callback := range decodedCallbacks {
		callback(cameraID, cloneDecodedFrame(decoded))
	}
}

// EmitPCM dispatches one decoded PCM frame to callbacks registered for its channel.
func (c *Instance) EmitPCM(frame DecodedFrame) {
	c.emitDecoded(c.decodedPCM, frame)
}

func (c *Instance) normalizeStartOptions(options StartOptions) StartOptions {
	count := c.info.ChannelCount
	if count <= 0 {
		count = 1
	}
	switch len(options.VideoQualities) {
	case 0:
		options.VideoQualities = make([]VideoQuality, count)
		for idx := range options.VideoQualities {
			options.VideoQualities[idx] = VideoQualityLow
		}
	case 1:
		quality := options.VideoQualities[0]
		options.VideoQualities = make([]VideoQuality, count)
		for idx := range options.VideoQualities {
			options.VideoQualities[idx] = quality
		}
	}
	options.PinCode = strings.TrimSpace(options.PinCode)
	return options
}

func (c *Instance) registerFrameCallback(target *map[int]frameCallback, channel int, callback func(string, Frame)) int {
	if c == nil || callback == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextCallback
	c.nextCallback++
	(*target)[id] = frameCallback{
		channel:  channel,
		callback: callback,
	}
	return id
}

func (c *Instance) unregisterFrameCallback(target map[int]frameCallback, id int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(target, id)
	c.mu.Unlock()
}

func (c *Instance) registerDecodedCallback(target *map[int]decodedCallback, channel int, callback func(string, DecodedFrame)) int {
	if c == nil || callback == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextCallback
	c.nextCallback++
	(*target)[id] = decodedCallback{
		channel:  channel,
		callback: callback,
	}
	return id
}

func (c *Instance) unregisterDecodedCallback(target map[int]decodedCallback, id int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(target, id)
	c.mu.Unlock()
}

func (c *Instance) emitFrame(callbacks map[int]frameCallback, frame Frame) {
	if c == nil {
		return
	}
	c.mu.Lock()
	list := make([]frameCallback, 0, len(callbacks))
	for _, callback := range callbacks {
		if callback.callback == nil {
			continue
		}
		if callback.channel >= 0 && callback.channel != frame.Channel {
			continue
		}
		list = append(list, callback)
	}
	cameraID := c.info.CameraID
	c.mu.Unlock()
	for _, callback := range list {
		callback.callback(cameraID, cloneFrame(frame))
	}
}

func (c *Instance) emitDecoded(callbacks map[int]decodedCallback, frame DecodedFrame) {
	if c == nil {
		return
	}
	c.mu.Lock()
	list := make([]decodedCallback, 0, len(callbacks))
	for _, callback := range callbacks {
		if callback.callback == nil {
			continue
		}
		if callback.channel >= 0 && callback.channel != frame.Channel {
			continue
		}
		list = append(list, callback)
	}
	cameraID := c.info.CameraID
	c.mu.Unlock()
	for _, callback := range list {
		callback.callback(cameraID, cloneDecodedFrame(frame))
	}
}

func normalizeInfo(info Info) Info {
	info.CameraID = strings.TrimSpace(info.CameraID)
	info.Model = strings.TrimSpace(info.Model)
	info.Token = strings.TrimSpace(info.Token)
	if info.ChannelCount <= 0 {
		info.ChannelCount = 1
	}
	return info
}

func isValidPinCode(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func decodedFrameFromJPEG(frame JPEGFrame) DecodedFrame {
	return DecodedFrame{
		Timestamp: uint64(frame.CapturedAt.UnixNano()),
		Channel:   frame.Channel,
		Data:      append([]byte(nil), frame.Payload...),
	}
}

func cloneFrame(frame Frame) Frame {
	frame.Data = append([]byte(nil), frame.Data...)
	return frame
}

func cloneDecodedFrame(frame DecodedFrame) DecodedFrame {
	frame.Data = append([]byte(nil), frame.Data...)
	return frame
}
