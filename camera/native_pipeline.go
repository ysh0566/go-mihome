package camera

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	miot "github.com/ysh0566/go-mihome"
)

type miHomeCameraPipelineFactory struct {
	loadSession    miHomeSessionLoader
	newCloud       miHomeCloudFactory
	newMedia       miHomeMediaFactory
	discoverHosts  miHomeHostDiscovery
	decodeSnapshot miHomeSnapshotDecoder
	decoder        ProbeFrameDecoder
	fallback       cameraPipelineFactory
	firstTimeout   time.Duration
	maxAttempts    int
	logger         any
}

type miHomeCameraPipeline struct {
	cancel         context.CancelFunc
	client         miHomeCameraMediaClient
	decoderSession ProbeDecoderSession
	closeOnce      sync.Once
	mu             sync.Mutex

	videoState miHomeVideoBootstrapState
}

func newMiHomeCameraPipelineFactory(ffmpegPath string, logger any, fallback cameraPipelineFactory) cameraPipelineFactory {
	return &miHomeCameraPipelineFactory{
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
		decodeSnapshot: func(ctx context.Context, codec string, payload []byte) ([]byte, error) {
			return decodeFirstJPEGFrame(ctx, ffmpegPath, codec, payload)
		},
		decoder:      NewFFmpegFrameDecoder(ffmpegPath),
		fallback:     fallback,
		firstTimeout: 10 * time.Second,
		logger:       logger,
	}
}

func (f *miHomeCameraPipelineFactory) Available() bool {
	return f != nil && f.decoder != nil && f.loadSession != nil && f.newCloud != nil && f.newMedia != nil
}

func (f *miHomeCameraPipelineFactory) Start(desc CameraStreamDescriptor, onJPEG func(JPEGFrame)) (io.Closer, error) {
	if f == nil || f.decoder == nil || f.loadSession == nil || f.newCloud == nil || f.newMedia == nil {
		return f.tryFallback(desc, onJPEG, fmt.Errorf("%w: mihome pipeline unavailable", ErrCameraBridgeUnavailable))
	}

	ctx, cancel := context.WithCancel(context.Background())
	session, err := f.loadSession(ctx)
	if err != nil {
		cancel()
		return f.tryFallback(desc, onJPEG, fmt.Errorf("%w: load mihome session: %w", ErrCameraBridgeUnavailable, err))
	}
	cloud, err := f.newCloud(session)
	if err != nil {
		cancel()
		return f.tryFallback(desc, onJPEG, fmt.Errorf("%w: create mihome cloud: %w", ErrCameraBridgeUnavailable, err))
	}
	if strings.Contains(strings.TrimSpace(desc.Model), ".cateye.") {
		_ = cloud.WakeupCamera(ctx, desc.CameraID)
	}
	device, err := cloud.GetDevice(ctx, desc.CameraID)
	if err != nil {
		cancel()
		return f.tryFallback(desc, onJPEG, fmt.Errorf("%w: get mihome device: %w", ErrCameraBridgeUnavailable, err))
	}
	if strings.TrimSpace(device.LocalIP) == "" {
		cancel()
		return f.tryFallback(desc, onJPEG, fmt.Errorf("%w: camera local ip unavailable", ErrCameraBridgeUnavailable))
	}
	publicKey, privateKey, err := miHomeCameraGenerateKey()
	if err != nil {
		cancel()
		return f.tryFallback(desc, onJPEG, fmt.Errorf("%w: generate camera key: %w", ErrCameraBridgeUnavailable, err))
	}
	vendor, err := cloud.GetCameraVendor(ctx, desc.CameraID, publicKey, session.DeviceID)
	if err != nil {
		cancel()
		return f.tryFallback(desc, onJPEG, fmt.Errorf("%w: get camera vendor: %w", ErrCameraBridgeUnavailable, err))
	}
	client, _, err := f.startMediaClientWithRetries(ctx, cloud, desc, device, vendor, firstNonEmpty(desc.Model, device.Model), session.DeviceID, publicKey, privateKey)
	if err != nil {
		cancel()
		return f.tryFallback(desc, onJPEG, fmt.Errorf("%w: %w", ErrCameraBridgeUnavailable, err))
	}

	pipeline := &miHomeCameraPipeline{
		cancel: cancel,
		client: client,
	}
	go pipeline.run(ctx, f.decoder, f.decodeSnapshot, onJPEG)
	return pipeline, nil
}

func (f *miHomeCameraPipelineFactory) tryFallback(desc CameraStreamDescriptor, onJPEG func(JPEGFrame), cause error) (io.Closer, error) {
	if f != nil && f.fallback != nil {
		if availability, ok := f.fallback.(interface{ Available() bool }); ok && !availability.Available() {
			return nil, cause
		}
		return f.fallback.Start(desc, onJPEG)
	}
	return nil, cause
}

func (f *miHomeCameraPipelineFactory) openMediaClient(ctx context.Context, cameraID string, host string, vendor miot.CameraVendorInfo, model string, callerUUID string, publicKey []byte, privateKey []byte) (miHomeCameraMediaClient, string, error) {
	if f == nil || f.newMedia == nil {
		return nil, "", fmt.Errorf("%w: native media factory unavailable", ErrCameraBridgeUnavailable)
	}
	host = strings.TrimSpace(host)
	client, err := f.newMedia(host, vendor, model, callerUUID, publicKey, privateKey)
	if err == nil {
		return client, host, nil
	}
	baseErr := err
	logPrintf(f.logger, "camera_id=%s stage=local_media_connect_failed host=%s error=%q", cameraID, host, baseErr)
	if f.discoverHosts == nil {
		return nil, host, baseErr
	}
	hosts, discoverErr := f.discoverHosts(ctx, host)
	if discoverErr != nil {
		return nil, host, baseErr
	}
	for _, candidate := range hosts {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || candidate == host {
			continue
		}
		logPrintf(f.logger, "camera_id=%s stage=local_media_candidate host=%s", cameraID, candidate)
		client, err = f.newMedia(candidate, vendor, model, callerUUID, publicKey, privateKey)
		if err == nil {
			return client, candidate, nil
		}
		logPrintf(f.logger, "camera_id=%s stage=local_media_connect_failed host=%s error=%q", cameraID, candidate, err)
	}
	return nil, host, baseErr
}

func (f *miHomeCameraPipelineFactory) startMediaClientWithRetries(ctx context.Context, cloud miHomeCameraCloud, desc CameraStreamDescriptor, device miot.DeviceInfo, vendor miot.CameraVendorInfo, model string, callerUUID string, publicKey []byte, privateKey []byte) (miHomeCameraMediaClient, string, error) {
	maxAttempts := firstPositiveInt(f.maxAttempts, 3)
	startQuality := miHomeMediaQualityOption(desc.VideoQuality)

	currentDevice := device
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		host := strings.TrimSpace(currentDevice.LocalIP)
		logPrintf(f.logger, "camera_id=%s stage=local_media_connect attempt=%d host=%s", desc.CameraID, attempt, host)
		client, mediaHost, err := f.openMediaClient(ctx, desc.CameraID, host, vendor, model, callerUUID, publicKey, privateKey)
		if err == nil {
			startErr := client.StartMedia("0", startQuality, "0")
			if startErr == nil {
				logPrintf(f.logger, "camera_id=%s stage=local_media_started attempt=%d host=%s quality=%s", desc.CameraID, attempt, mediaHost, startQuality)
				return client, mediaHost, nil
			}
			_ = client.Close()
			lastErr = fmt.Errorf("start native camera media: %w", startErr)
			logPrintf(f.logger, "camera_id=%s stage=local_media_start_failed attempt=%d host=%s error=%q", desc.CameraID, attempt, mediaHost, startErr)
		} else {
			lastErr = fmt.Errorf("open native camera media: %w", err)
		}
		if attempt == maxAttempts {
			break
		}

		if cloud != nil {
			_ = cloud.WakeupCamera(ctx, desc.CameraID)
			if refreshed, refreshErr := cloud.GetDevice(ctx, desc.CameraID); refreshErr == nil && strings.TrimSpace(refreshed.LocalIP) != "" {
				currentDevice = refreshed
				logPrintf(f.logger, "camera_id=%s stage=token_device_refreshed attempt=%d local_ip=%s", desc.CameraID, attempt, strings.TrimSpace(refreshed.LocalIP))
			}
		}

		backoff := time.Second
		if f != nil && f.firstTimeout > 0 && f.firstTimeout < backoff {
			backoff = f.firstTimeout
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(backoff):
		}
	}

	return nil, strings.TrimSpace(currentDevice.LocalIP), lastErr
}

func miHomeMediaQualityOption(value VideoQuality) string {
	switch value {
	case VideoQualityLow:
		return "sd"
	case VideoQualityHigh:
		return "hd"
	default:
		return "hd"
	}
}

func (p *miHomeCameraPipeline) run(ctx context.Context, decoder ProbeFrameDecoder, decodeSnapshot miHomeSnapshotDecoder, onJPEG func(JPEGFrame)) {
	defer func() {
		_ = p.Close()
	}()

	var decoderCodec string
	emittedSnapshot := false
	firstSnapshotAttempted := false
	for {
		if err := p.client.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return
		}
		packet, err := p.client.ReadPacket()
		if err != nil {
			return
		}
		codec := miHomeCameraCodec(packet.CodecID)
		if codec == "" {
			continue
		}
		if decoderCodec == "" {
			decoderCodec = codec
			decoderSession, err := decoder.Start(ctx, ProbeDecoderConfig{Codec: decoderCodec}, func(frame JPEGFrame) {
				if len(frame.Payload) == 0 || onJPEG == nil {
					return
				}
				onJPEG(cloneJPEGFrame(frame))
			})
			if err != nil {
				return
			}
			p.mu.Lock()
			p.decoderSession = decoderSession
			p.mu.Unlock()
		}

		ready, decodePayload := p.videoState.prepare(decoderCodec, packet.Payload)
		if !ready || p.decoderSession == nil {
			if ready && !emittedSnapshot && !firstSnapshotAttempted && decodeSnapshot != nil {
				firstSnapshotAttempted = true
				frame, err := decodeSnapshot(ctx, decoderCodec, decodePayload)
				if err == nil && len(frame) > 0 {
					emittedSnapshot = true
					if onJPEG != nil {
						onJPEG(JPEGFrame{
							Payload:    append([]byte(nil), frame...),
							CapturedAt: time.Now().UTC(),
						})
					}
				}
			}
			continue
		}

		if !emittedSnapshot && !firstSnapshotAttempted && decodeSnapshot != nil {
			firstSnapshotAttempted = true
			frame, err := decodeSnapshot(ctx, decoderCodec, decodePayload)
			if err == nil && len(frame) > 0 {
				emittedSnapshot = true
				if onJPEG != nil {
					onJPEG(JPEGFrame{
						Payload:    append([]byte(nil), frame...),
						CapturedAt: time.Now().UTC(),
					})
				}
			}
		}

		if err := p.decoderSession.Decode(AccessUnit{
			Codec:            decoderCodec,
			Payload:          decodePayload,
			PresentationTime: time.Duration(packet.Timestamp) * time.Millisecond,
		}); err != nil {
			return
		}
	}
}

func (p *miHomeCameraPipeline) Close() error {
	if p == nil {
		return nil
	}
	var closeErr error
	p.closeOnce.Do(func() {
		if p.cancel != nil {
			p.cancel()
		}
		if p.client != nil {
			closeErr = errors.Join(closeErr, p.client.StopMedia(), p.client.Close())
		}
		p.mu.Lock()
		decoderSession := p.decoderSession
		p.decoderSession = nil
		p.mu.Unlock()
		if decoderSession != nil {
			closeErr = errors.Join(closeErr, decoderSession.Close())
		}
	})
	return closeErr
}

func miHomeCameraCodec(codecID uint32) string {
	switch codecID {
	case miHomeMissCodecH264:
		return "h264"
	case miHomeMissCodecH265:
		return "h265"
	default:
		return ""
	}
}
