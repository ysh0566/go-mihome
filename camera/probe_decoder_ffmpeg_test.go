package camera

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestFFmpegDecoderStartFailsWithoutBinary(t *testing.T) {
	t.Parallel()

	decoder := NewFFmpegFrameDecoder("/definitely/missing/ffmpeg")

	_, err := decoder.Start(context.Background(), ProbeDecoderConfig{Codec: "h264"}, func(JPEGFrame) {})
	if err == nil {
		t.Fatal("Start() error = nil, want missing ffmpeg failure")
	}
	if !errors.Is(err, ErrProbeDecoderUnavailable) {
		t.Fatalf("Start() error = %v, want ErrProbeDecoderUnavailable", err)
	}
}

func TestFFmpegDecoderEmitsJPEGFrameFromBackend(t *testing.T) {
	t.Parallel()

	decoder := NewFFmpegFrameDecoder("ffmpeg-helper")
	decoder.commandBuilder = func(ctx context.Context, ffmpegPath string, inputFormat string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestFFmpegDecoderHelperProcess", "--", inputFormat)
		cmd.Env = append(os.Environ(), "GO_WANT_FFMPEG_DECODER_HELPER=1")
		return cmd
	}

	frames := make(chan JPEGFrame, 1)
	session, err := decoder.Start(context.Background(), ProbeDecoderConfig{Codec: "h264"}, func(frame JPEGFrame) {
		frames <- frame
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if err := session.Decode(AccessUnit{
		Codec:            "h264",
		Payload:          []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84},
		PresentationTime: 100 * time.Millisecond,
	}); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	select {
	case frame := <-frames:
		if !bytes.Equal(frame.Payload, testJPEGFrame) {
			t.Fatalf("frame.Payload = %x, want %x", frame.Payload, testJPEGFrame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for decoded JPEG frame")
	}
}

func TestFFmpegDecoderCloseSuppressesIntentionalShutdownError(t *testing.T) {
	t.Parallel()

	decoder := NewFFmpegFrameDecoder("ffmpeg-helper")
	decoder.commandBuilder = func(ctx context.Context, ffmpegPath string, inputFormat string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestFFmpegDecoderHelperProcess", "--", inputFormat)
		cmd.Env = append(os.Environ(), "GO_WANT_FFMPEG_DECODER_HELPER=1")
		return cmd
	}

	session, err := decoder.Start(context.Background(), ProbeDecoderConfig{Codec: "h264"}, func(JPEGFrame) {})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-session.Err():
		t.Fatalf("Err() after Close() = %v, want no shutdown error", err)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestDefaultFFmpegCommandBuilderAvoidsFPSFilter(t *testing.T) {
	t.Parallel()

	cmd := defaultFFmpegCommandBuilder(context.Background(), "ffmpeg", "hevc")
	if cmd == nil {
		t.Fatal("defaultFFmpegCommandBuilder() = nil")
	}

	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "fps=2") {
		t.Fatalf("ffmpeg args = %q, want no fps filter", args)
	}
	if !strings.Contains(args, "-f hevc") {
		t.Fatalf("ffmpeg args = %q, want input format", args)
	}
	if !strings.Contains(args, "-f image2pipe") {
		t.Fatalf("ffmpeg args = %q, want image2pipe output", args)
	}
}

func TestDefaultFFmpegCommandBuilderAvoidsAggressiveLowLatencyFlags(t *testing.T) {
	t.Parallel()

	cmd := defaultFFmpegCommandBuilder(context.Background(), "ffmpeg", "hevc")
	if cmd == nil {
		t.Fatal("defaultFFmpegCommandBuilder() = nil")
	}

	args := strings.Join(cmd.Args, " ")
	for _, forbidden := range []string{
		"-fflags nobuffer",
		"-flags low_delay",
		"-analyzeduration 0",
		"-probesize 32",
	} {
		if strings.Contains(args, forbidden) {
			t.Fatalf("ffmpeg args = %q, want %q to be absent for stable HEVC pipe decoding", args, forbidden)
		}
	}
}

func TestDefaultFFmpegCommandBuilderUsesExplicitJPEGQuality(t *testing.T) {
	t.Parallel()

	cmd := defaultFFmpegCommandBuilder(context.Background(), "ffmpeg", "hevc")
	if cmd == nil {
		t.Fatal("defaultFFmpegCommandBuilder() = nil")
	}

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "-q:v 5") {
		t.Fatalf("ffmpeg args = %q, want explicit JPEG quality", args)
	}
}

func TestFFmpegDecoderHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_FFMPEG_DECODER_HELPER") != "1" {
		return
	}

	buf := make([]byte, 1024)
	_, _ = os.Stdin.Read(buf)
	_, _ = os.Stdout.Write(testJPEGFrame)
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}
