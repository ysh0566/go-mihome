package camera

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// FFmpegFrameDecoder is an optional FFmpeg-backed ProbeFrameDecoder implementation.
type FFmpegFrameDecoder struct {
	ffmpegPath     string
	commandBuilder func(context.Context, string, string) *exec.Cmd
}

type ffmpegProbeDecoderSession struct {
	ctx      context.Context
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	errCh    chan error
	waitDone chan struct{}
	stopped  chan struct{}
	stateMu  sync.Mutex
	stopping bool
	closeMu  sync.Once
}

// NewFFmpegFrameDecoder constructs a new FFmpeg-backed probe frame decoder.
func NewFFmpegFrameDecoder(ffmpegPath string) *FFmpegFrameDecoder {
	ffmpegPath = strings.TrimSpace(ffmpegPath)
	if ffmpegPath == "" {
		ffmpegPath = defaultFFmpegPath()
	}
	return &FFmpegFrameDecoder{ffmpegPath: ffmpegPath}
}

func decodeFirstJPEGFrame(ctx context.Context, ffmpegPath string, codec string, payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: decode payload unavailable", ErrProbeDecoderUnavailable)
	}
	inputFormat, err := ffmpegInputFormat(codec)
	if err != nil {
		return nil, err
	}
	ffmpegPath = strings.TrimSpace(ffmpegPath)
	if ffmpegPath == "" {
		ffmpegPath = defaultFFmpegPath()
	}
	if ffmpegPath == "" {
		return nil, fmt.Errorf("%w: ffmpeg path unavailable", ErrProbeDecoderUnavailable)
	}

	args := append([]string{
		"-loglevel", "fatal",
		"-f", inputFormat,
		"-i", "pipe:0",
		"-frames:v", "1",
	}, ffmpegMJPEGOutputArgs()...)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: open ffmpeg stdin: %w", ErrProbeDecoderUnavailable, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: open ffmpeg stdout: %w", ErrProbeDecoderUnavailable, err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: start ffmpeg: %w", ErrProbeDecoderUnavailable, err)
	}
	if _, err := stdin.Write(payload); err != nil {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		return nil, fmt.Errorf("%w: write ffmpeg stdin: %w", ErrProbeDecoderUnavailable, err)
	}
	_ = stdin.Close()
	output, readErr := io.ReadAll(stdout)
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("%w: read ffmpeg output: %w", ErrProbeDecoderUnavailable, readErr)
	}
	if waitErr != nil {
		return nil, fmt.Errorf("%w: ffmpeg exited: %w", ErrProbeDecoderUnavailable, waitErr)
	}
	frame := firstJPEGFrame(output)
	if len(frame) == 0 {
		return nil, fmt.Errorf("%w: ffmpeg output ended before JPEG frame", ErrProbeDecoderUnavailable)
	}
	return frame, nil
}

// Start starts an FFmpeg process and decodes JPEG frames from probe access units.
func (decoder *FFmpegFrameDecoder) Start(ctx context.Context, config ProbeDecoderConfig, onFrame func(JPEGFrame)) (ProbeDecoderSession, error) {
	if decoder == nil {
		return nil, fmt.Errorf("%w: decoder unavailable", ErrProbeDecoderUnavailable)
	}
	inputFormat, err := ffmpegInputFormat(config.Codec)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(decoder.ffmpegPath) == "" {
		return nil, fmt.Errorf("%w: ffmpeg path unavailable", ErrProbeDecoderUnavailable)
	}

	builder := decoder.commandBuilder
	if builder == nil {
		builder = defaultFFmpegCommandBuilder
	}
	cmd := builder(ctx, decoder.ffmpegPath, inputFormat)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: open ffmpeg stdin: %w", ErrProbeDecoderUnavailable, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: open ffmpeg stdout: %w", ErrProbeDecoderUnavailable, err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: start ffmpeg: %w", ErrProbeDecoderUnavailable, err)
	}

	session := &ffmpegProbeDecoderSession{
		ctx:      ctx,
		cmd:      cmd,
		stdin:    stdin,
		errCh:    make(chan error, 1),
		waitDone: make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	go session.readJPEGFrames(stdout, onFrame)
	go session.waitForExit()
	return session, nil
}

func defaultFFmpegCommandBuilder(ctx context.Context, ffmpegPath string, inputFormat string) *exec.Cmd {
	args := append([]string{
		"-loglevel", "fatal",
		"-f", inputFormat,
		"-i", "pipe:0",
	}, ffmpegMJPEGOutputArgs()...)
	return exec.CommandContext(ctx, ffmpegPath, args...)
}

func ffmpegMJPEGOutputArgs() []string {
	return []string{
		"-an",
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-q:v", "5",
		"pipe:1",
	}
}

func defaultFFmpegPath() string {
	if value := os.Getenv("FFMPEG_PATH"); value != "" {
		return value
	}
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	return path
}

func ffmpegInputFormat(codec string) (string, error) {
	switch normalizeCodec(codec) {
	case "h264":
		return "h264", nil
	case "h265":
		return "hevc", nil
	default:
		return "", fmt.Errorf("%w: unsupported codec %q", ErrProbeDecoderUnavailable, strings.TrimSpace(codec))
	}
}

func normalizeCodec(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "h264", "avc":
		return "h264"
	case "h265", "hevc":
		return "h265"
	default:
		return strings.ToLower(strings.TrimSpace(codec))
	}
}

func (session *ffmpegProbeDecoderSession) Decode(unit AccessUnit) error {
	if session == nil {
		return fmt.Errorf("%w: decoder session unavailable", ErrProbeDecoderUnavailable)
	}
	if len(unit.Payload) == 0 {
		return nil
	}
	_, err := session.stdin.Write(unit.Payload)
	return err
}

func (session *ffmpegProbeDecoderSession) Close() error {
	if session == nil {
		return nil
	}
	session.closeMu.Do(func() {
		session.stateMu.Lock()
		session.stopping = true
		if session.stopped != nil {
			close(session.stopped)
		}
		session.stateMu.Unlock()
		if session.stdin != nil {
			_ = session.stdin.Close()
		}
		if session.cmd != nil && session.cmd.Process != nil {
			_ = session.cmd.Process.Kill()
		}
		if session.waitDone != nil {
			<-session.waitDone
		}
	})
	return nil
}

func (session *ffmpegProbeDecoderSession) Err() <-chan error {
	if session == nil {
		return nil
	}
	return session.errCh
}

func (session *ffmpegProbeDecoderSession) readJPEGFrames(reader io.Reader, onFrame func(JPEGFrame)) {
	if onFrame == nil {
		_, _ = io.Copy(io.Discard, reader)
		return
	}

	bufReader := bufio.NewReader(reader)
	buffer := make([]byte, 0, 256*1024)
	chunk := make([]byte, 32*1024)
	emittedFrame := false
	for {
		n, err := bufReader.Read(chunk)
		if n > 0 {
			buffer = append(buffer, chunk[:n]...)
			for {
				frame, rest, ok := nextJPEGFrame(buffer)
				if !ok {
					if len(buffer) > 1 {
						buffer = append([]byte(nil), buffer[len(buffer)-1:]...)
					}
					break
				}
				emittedFrame = true
				onFrame(JPEGFrame{
					Payload:    frame,
					CapturedAt: time.Now().UTC(),
				})
				buffer = append([]byte(nil), rest...)
			}
		}
		if err != nil {
			select {
			case <-session.stopped:
				return
			default:
			}
			switch {
			case errors.Is(err, io.EOF) && !emittedFrame:
				session.reportError(fmt.Errorf("%w: ffmpeg output ended before JPEG frame", ErrProbeDecoderUnavailable))
			case !errors.Is(err, io.EOF):
				session.reportError(fmt.Errorf("%w: read ffmpeg output: %w", ErrProbeDecoderUnavailable, err))
			}
			return
		}
	}
}

func firstJPEGFrame(buffer []byte) []byte {
	frame, _, ok := nextJPEGFrame(buffer)
	if !ok {
		return nil
	}
	return frame
}

func nextJPEGFrame(buffer []byte) ([]byte, []byte, bool) {
	start := bytes.Index(buffer, []byte{0xFF, 0xD8})
	if start < 0 {
		return nil, buffer, false
	}
	end := bytes.Index(buffer[start+2:], []byte{0xFF, 0xD9})
	if end < 0 {
		return nil, buffer[start:], false
	}
	end += start + 4
	frame := append([]byte(nil), buffer[start:end]...)
	return frame, buffer[end:], true
}

func (session *ffmpegProbeDecoderSession) waitForExit() {
	if session == nil || session.cmd == nil {
		return
	}
	err := session.cmd.Wait()
	if err != nil {
		select {
		case <-session.stopped:
		default:
			session.reportError(fmt.Errorf("%w: ffmpeg exited: %w", ErrProbeDecoderUnavailable, err))
		}
	}
	close(session.waitDone)
}

func (session *ffmpegProbeDecoderSession) reportError(err error) {
	if session == nil || err == nil {
		return
	}
	if session.ctx != nil && session.ctx.Err() != nil {
		return
	}
	session.stateMu.Lock()
	defer session.stateMu.Unlock()
	if session.stopping {
		return
	}
	select {
	case session.errCh <- err:
	default:
	}
}
