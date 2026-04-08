package camera

import (
	"context"
	"errors"
)

var (
	// ErrProbeDecoderUnavailable reports that the configured probe decoder cannot be used.
	ErrProbeDecoderUnavailable = errors.New("miot camera probe decoder unavailable")
)

// DecodeFirstJPEGFrame decodes the first JPEG frame from a probe payload with FFmpeg.
func DecodeFirstJPEGFrame(ctx context.Context, ffmpegPath string, codec string, payload []byte) ([]byte, error) {
	return decodeFirstJPEGFrame(ctx, ffmpegPath, codec, payload)
}
