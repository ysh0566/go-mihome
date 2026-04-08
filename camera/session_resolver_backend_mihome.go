package camera

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	miot "github.com/ysh0566/go-mihome"
)

type miHomeSessionLoader func(context.Context) (miHomeSession, error)
type miHomeCloudFactory func(miHomeSession) (miHomeCameraCloud, error)
type miHomeMediaFactory func(string, miot.CameraVendorInfo, string, string, []byte, []byte) (miHomeCameraMediaClient, error)
type miHomeHostDiscovery func(context.Context, string) ([]string, error)
type miHomeSnapshotDecoder func(context.Context, string, []byte) ([]byte, error)

// TokenCameraCloudClient describes the root-cloud operations required by the token-only camera backend.
type TokenCameraCloudClient interface {
	GetDevice(context.Context, string) (miot.DeviceInfo, error)
	GetCameraVendor(context.Context, string, []byte, string, string) (miot.CameraVendorInfo, error)
	GetCameraVendorSecurity(context.Context, string) (miot.CameraVendorSecurity, error)
}

type tokenCameraVendorBootstrapCloud interface {
	GetCameraVendorBootstrap(context.Context, string, []byte, []byte, string, string) (miot.CameraVendorInfo, error)
}

// TokenCameraSessionResolverBackendOptions configures the explicit token-only resolver backend.
type TokenCameraSessionResolverBackendOptions struct {
	Cloud          TokenCameraCloudClient
	SupportVendors string
	CallerUUID     string
	MaxAttempts    int
	DisableScan    bool
	RetryDelay     time.Duration
	Logger         any
}

type miHomeCameraSessionResolverBackend struct {
	loadSession   miHomeSessionLoader
	newCloud      miHomeCloudFactory
	newMedia      miHomeMediaFactory
	discoverHosts miHomeHostDiscovery
	firstTimeout  time.Duration
	logger        any
}

type miHomeCameraSessionResolverStream struct {
	client miHomeCameraMediaClient
	codec  string
	state  miHomeVideoBootstrapState
}

type tokenCameraSessionResolverBackend struct {
	cloud          TokenCameraCloudClient
	supportVendors string
	callerUUID     string
	newMedia       miHomeMediaFactory
	discoverHosts  miHomeHostDiscovery
	firstTimeout   time.Duration
	maxAttempts    int
	logger         any
}

func NewMiHomeCameraSessionResolverBackend(logger any) SessionResolverBackend {
	return &miHomeCameraSessionResolverBackend{
		loadSession: func(ctx context.Context) (miHomeSession, error) {
			return loadLocalMiHomeSession(ctx)
		},
		newCloud: func(session miHomeSession) (miHomeCameraCloud, error) {
			return newMiHomeCloudClient(session, nil)
		},
		newMedia: func(host string, vendor miot.CameraVendorInfo, model string, callerUUID string, clientPublic []byte, clientPrivate []byte) (miHomeCameraMediaClient, error) {
			return newMiHomeMissClient(host, vendor, model, callerUUID, clientPublic, clientPrivate)
		},
		discoverHosts: discoverMiHomeCS2Hosts,
		firstTimeout:  10 * time.Second,
		logger:        logger,
	}
}

// NewTokenCameraSessionResolverBackend constructs a token-only camera backend that bypasses local Mi Home session loading.
func NewTokenCameraSessionResolverBackend(options TokenCameraSessionResolverBackendOptions) SessionResolverBackend {
	discoverHosts := discoverMiHomeCS2Hosts
	if options.DisableScan {
		discoverHosts = nil
	}
	return &tokenCameraSessionResolverBackend{
		cloud:          options.Cloud,
		supportVendors: firstNonEmpty(options.SupportVendors, miHomeSupportVendors),
		callerUUID:     firstNonEmpty(options.CallerUUID, randomSessionSecret(16)),
		newMedia: func(host string, vendor miot.CameraVendorInfo, model string, callerUUID string, clientPublic []byte, clientPrivate []byte) (miHomeCameraMediaClient, error) {
			return newMiHomeMissClient(host, vendor, model, callerUUID, clientPublic, clientPrivate)
		},
		discoverHosts: discoverHosts,
		firstTimeout:  firstPositiveDuration(options.RetryDelay, 10*time.Second),
		maxAttempts:   firstPositiveInt(options.MaxAttempts, 3),
		logger:        options.Logger,
	}
}

func (b *miHomeCameraSessionResolverBackend) Probe(ctx context.Context, target Target, _ Profile) (SessionResolverProbe, error) {
	cameraID := strings.TrimSpace(target.CameraID)
	model := strings.TrimSpace(target.Model)
	client, resolvedModel, err := b.openClient(ctx, cameraID, model, VideoQualityHigh)
	if err != nil {
		return SessionResolverProbe{}, err
	}
	defer func() {
		_ = client.StopMedia()
		_ = client.Close()
	}()

	codec, err := probeCodecFromClient(ctx, client)
	if err != nil {
		return SessionResolverProbe{}, err
	}
	return SessionResolverProbe{
		CameraID: cameraID,
		Model:    resolvedModel,
		Codec:    codec,
	}, nil
}

func (b *miHomeCameraSessionResolverBackend) Open(ctx context.Context, req SessionResolverStreamRequest) (SessionResolverStream, error) {
	cameraID := strings.TrimSpace(req.CameraID)
	model := strings.TrimSpace(req.Model)
	client, _, err := b.openClient(ctx, cameraID, model, req.VideoQuality)
	if err != nil {
		return nil, err
	}
	return &miHomeCameraSessionResolverStream{
		client: client,
		codec:  normalizeCodec(req.Codec),
	}, nil
}

func (b *tokenCameraSessionResolverBackend) Probe(ctx context.Context, target Target, _ Profile) (SessionResolverProbe, error) {
	cameraID := strings.TrimSpace(target.CameraID)
	model := strings.TrimSpace(target.Model)
	client, resolvedModel, err := b.openClient(ctx, cameraID, model, VideoQualityHigh)
	if err != nil {
		return SessionResolverProbe{}, err
	}
	defer func() {
		_ = client.StopMedia()
		_ = client.Close()
	}()

	codec, err := probeCodecFromClient(ctx, client)
	if err != nil {
		return SessionResolverProbe{}, err
	}
	return SessionResolverProbe{
		CameraID: cameraID,
		Model:    resolvedModel,
		Codec:    codec,
	}, nil
}

func (b *tokenCameraSessionResolverBackend) Open(ctx context.Context, req SessionResolverStreamRequest) (SessionResolverStream, error) {
	cameraID := strings.TrimSpace(req.CameraID)
	model := strings.TrimSpace(req.Model)
	client, _, err := b.openClient(ctx, cameraID, model, req.VideoQuality)
	if err != nil {
		return nil, err
	}
	return &miHomeCameraSessionResolverStream{
		client: client,
		codec:  normalizeCodec(req.Codec),
	}, nil
}

func (b *miHomeCameraSessionResolverBackend) openClient(ctx context.Context, cameraID string, model string, quality VideoQuality) (miHomeCameraMediaClient, string, error) {
	if b == nil || b.loadSession == nil || b.newCloud == nil || b.newMedia == nil {
		return nil, "", fmt.Errorf("%w: mihome resolver backend unavailable", ErrRuntimeUnavailable)
	}

	session, err := b.loadSession(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("load mihome session: %w", err)
	}
	cloud, err := b.newCloud(session)
	if err != nil {
		return nil, "", fmt.Errorf("create mihome cloud: %w", err)
	}
	if strings.Contains(model, ".cateye.") {
		_ = cloud.WakeupCamera(ctx, cameraID)
	}
	device, err := cloud.GetDevice(ctx, cameraID)
	if err != nil {
		return nil, "", fmt.Errorf("get mihome device: %w", err)
	}

	resolvedModel := firstNonEmpty(model, strings.TrimSpace(device.Model))
	publicKey, privateKey, err := miHomeCameraGenerateKey()
	if err != nil {
		return nil, "", fmt.Errorf("generate camera key: %w", err)
	}
	vendor, err := cloud.GetCameraVendor(ctx, cameraID, publicKey, session.DeviceID)
	if err != nil {
		return nil, "", fmt.Errorf("get camera vendor: %w", err)
	}

	factory := &miHomeCameraPipelineFactory{
		newMedia:      b.newMedia,
		discoverHosts: b.discoverHosts,
		firstTimeout:  b.firstTimeout,
		logger:        b.logger,
	}
	client, _, err := factory.startMediaClientWithRetries(ctx, cloud, CameraStreamDescriptor{
		CameraID:     cameraID,
		Model:        resolvedModel,
		VideoQuality: quality,
	}, device, vendor, resolvedModel, session.DeviceID, publicKey, privateKey)
	if err != nil {
		return nil, "", err
	}
	return client, resolvedModel, nil
}

func (b *tokenCameraSessionResolverBackend) openClient(ctx context.Context, cameraID string, model string, quality VideoQuality) (miHomeCameraMediaClient, string, error) {
	if b == nil || b.cloud == nil || b.newMedia == nil {
		return nil, "", fmt.Errorf("%w: token resolver backend unavailable", ErrRuntimeUnavailable)
	}

	if wakeup, ok := b.cloud.(interface {
		WakeupCamera(context.Context, string) error
	}); ok && strings.Contains(model, ".cateye.") {
		_ = wakeup.WakeupCamera(ctx, cameraID)
	}
	device, err := b.cloud.GetDevice(ctx, cameraID)
	logPrintf(b.logger, "camera_id=%s stage=token_get_device", cameraID)
	if err != nil {
		return nil, "", fmt.Errorf("get token camera device: %w", err)
	}
	if strings.TrimSpace(device.LocalIP) == "" {
		return nil, "", fmt.Errorf("get token camera device: strict token-local preview requires a LAN local_ip for %s", cameraID)
	}

	resolvedModel := firstNonEmpty(model, strings.TrimSpace(device.Model))
	logPrintf(b.logger, "camera_id=%s stage=token_device_resolved model=%s local_ip=%s", cameraID, resolvedModel, strings.TrimSpace(device.LocalIP))
	publicKey, privateKey, err := miHomeCameraGenerateKey()
	if err != nil {
		return nil, "", fmt.Errorf("generate camera key: %w", err)
	}
	logPrintf(b.logger, "camera_id=%s stage=token_get_vendor_bootstrap support_vendors=%s", cameraID, b.supportVendors)
	vendor, err := b.getVendor(ctx, cameraID, publicKey, privateKey)
	if err != nil {
		return nil, "", fmt.Errorf("get token camera vendor: %w", explainTokenCameraVendorError(err))
	}
	logPrintf(b.logger, "camera_id=%s stage=token_vendor_resolved support_vendor=%s vendor_id=%d has_p2p_id=%t has_init_string=%t",
		cameraID,
		strings.TrimSpace(vendor.SupportVendor),
		vendor.VendorID,
		strings.TrimSpace(vendor.P2PID) != "",
		strings.TrimSpace(vendor.InitString) != "",
	)

	factory := &miHomeCameraPipelineFactory{
		newMedia:      b.newMedia,
		discoverHosts: b.discoverHosts,
		firstTimeout:  b.firstTimeout,
		maxAttempts:   b.maxAttempts,
		logger:        b.logger,
	}
	client, _, err := factory.startMediaClientWithRetries(ctx, tokenCameraCloudBridge{
		cloud:          b.cloud,
		supportVendors: b.supportVendors,
		callerUUID:     b.callerUUID,
	}, CameraStreamDescriptor{
		CameraID:     cameraID,
		Model:        resolvedModel,
		VideoQuality: quality,
	}, device, vendor, resolvedModel, b.callerUUID, publicKey, privateKey)
	if err != nil {
		return nil, "", explainTokenCameraMediaError(err, device, vendor)
	}
	return client, resolvedModel, nil
}

func (b *tokenCameraSessionResolverBackend) getVendor(ctx context.Context, cameraID string, publicKey []byte, privateKey []byte) (miot.CameraVendorInfo, error) {
	if advanced, ok := b.cloud.(tokenCameraVendorBootstrapCloud); ok {
		return advanced.GetCameraVendorBootstrap(ctx, cameraID, publicKey, privateKey, b.supportVendors, b.callerUUID)
	}
	return b.cloud.GetCameraVendor(ctx, cameraID, publicKey, b.supportVendors, b.callerUUID)
}

func explainTokenCameraVendorError(err error) error {
	if err == nil {
		return nil
	}
	var miotErr *miot.Error
	if !errors.As(err, &miotErr) {
		return err
	}
	switch miotErr.Code {
	case miot.ErrInvalidAccessToken:
		return fmt.Errorf("camera preview token rejected during native live bootstrap; verify MIOT_CLIENT_ID and MIOT_CLOUD_SERVER match the token: %w", err)
	case miot.ErrTransportFailure:
		if strings.Contains(miotErr.Msg, "status 204") {
			return fmt.Errorf("camera bootstrap endpoint rejected the token-only native live request; verify MIOT_CLIENT_ID and MIOT_CLOUD_SERVER for this account region: %w", err)
		}
		if strings.Contains(miotErr.Msg, "status 404") {
			return fmt.Errorf("camera bootstrap endpoint was not found on the selected micoapi host; verify MIOT_CLOUD_SERVER matches the account region: %w", err)
		}
	}
	return err
}

func explainTokenCameraMediaError(err error, device miot.DeviceInfo, vendor miot.CameraVendorInfo) error {
	if err == nil {
		return nil
	}

	host := strings.TrimSpace(device.LocalIP)
	if host == "" {
		return err
	}
	if strings.TrimSpace(vendor.SupportVendor) == "cs2" || vendor.VendorID == 4 {
		if strings.TrimSpace(vendor.P2PID) != "" || strings.TrimSpace(vendor.InitString) != "" {
			return fmt.Errorf("strict token-local preview reached %s and got CS2 vendor bootstrap (p2p_id/init_string), but the pure-Go local connector could not open media. This camera likely requires the PPCS-style connect phase used by libmiot_camera_lite, which is not implemented yet: %w", host, err)
		}
		return fmt.Errorf("strict token-local preview reached %s, but the pure-Go CS2 local media connect failed: %w", host, err)
	}
	return err
}

func probeCodecFromClient(ctx context.Context, client miHomeCameraMediaClient) (string, error) {
	if client == nil {
		return "", io.EOF
	}
	deadline := time.Now().Add(10 * time.Second)
	if deadlineErr := client.SetDeadline(deadline); deadlineErr != nil {
		return "", deadlineErr
	}
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		packet, err := client.ReadPacket()
		if err != nil {
			return "", err
		}
		codec := miHomeCameraCodec(packet.CodecID)
		if codec != "" {
			return codec, nil
		}
	}
}

type tokenCameraCloudBridge struct {
	cloud          TokenCameraCloudClient
	supportVendors string
	callerUUID     string
}

func (b tokenCameraCloudBridge) GetDevice(ctx context.Context, did string) (miot.DeviceInfo, error) {
	if b.cloud == nil {
		return miot.DeviceInfo{}, fmt.Errorf("token camera cloud unavailable")
	}
	return b.cloud.GetDevice(ctx, did)
}

func (b tokenCameraCloudBridge) GetCameraVendor(ctx context.Context, did string, appPublicKey []byte, callerUUID string) (miot.CameraVendorInfo, error) {
	if b.cloud == nil {
		return miot.CameraVendorInfo{}, fmt.Errorf("token camera cloud unavailable")
	}
	return b.cloud.GetCameraVendor(ctx, did, appPublicKey, b.supportVendors, firstNonEmpty(callerUUID, b.callerUUID))
}

func (b tokenCameraCloudBridge) GetCameraVendorSecurity(ctx context.Context, did string) (miot.CameraVendorSecurity, error) {
	if b.cloud == nil {
		return miot.CameraVendorSecurity{}, fmt.Errorf("token camera cloud unavailable")
	}
	return b.cloud.GetCameraVendorSecurity(ctx, did)
}

func (b tokenCameraCloudBridge) WakeupCamera(ctx context.Context, did string) error {
	if wakeup, ok := b.cloud.(interface {
		WakeupCamera(context.Context, string) error
	}); ok {
		return wakeup.WakeupCamera(ctx, did)
	}
	return nil
}

func (s *miHomeCameraSessionResolverStream) Recv(ctx context.Context) (AccessUnit, error) {
	if s == nil || s.client == nil {
		return AccessUnit{}, io.EOF
	}
	for {
		if ctx.Err() != nil {
			return AccessUnit{}, ctx.Err()
		}
		if err := s.client.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return AccessUnit{}, err
		}
		packet, err := s.client.ReadPacket()
		if err != nil {
			return AccessUnit{}, err
		}
		codec := miHomeCameraCodec(packet.CodecID)
		if codec == "" {
			continue
		}
		if s.codec == "" {
			s.codec = codec
		}
		ready, payload := s.state.prepare(s.codec, packet.Payload)
		if !ready {
			continue
		}
		return AccessUnit{
			Codec:            s.codec,
			Payload:          payload,
			PresentationTime: time.Duration(packet.Timestamp) * time.Millisecond,
		}, nil
	}
}

func (s *miHomeCameraSessionResolverStream) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return errors.Join(s.client.StopMedia(), s.client.Close())
}

func firstPositiveDuration(values ...time.Duration) time.Duration {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
