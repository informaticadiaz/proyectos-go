// Package audio decodes recorded audio with ffmpeg.
package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/turn"
)

// Output format expected by whisper.cpp.
const (
	sampleRate    = 16000
	channels      = 1
	bitsPerSample = 16
)

// FFmpeg decodes any format ffmpeg understands into 16 kHz mono 16-bit PCM
// WAV. Audio goes through stdin and stdout, so no temporary files are made.
type FFmpeg struct {
	path string
}

// NewFFmpeg returns a decoder running the ffmpeg binary at path (a bare
// name is looked up in PATH).
func NewFFmpeg(path string) *FFmpeg {
	return &FFmpeg{path: path}
}

// Decode converts audio to WAV. Input ffmpeg rejects is reported as
// turn.ErrUndecodable; a missing binary or a cancelled context is not.
//
// Reading from a pipe means ffmpeg cannot seek, so containers that keep
// their index at the end (non-fragmented MP4/M4A) fail; WebM, Ogg, WAV,
// MP3 and fragmented MP4 work.
func (f *FFmpeg) Decode(ctx context.Context, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, f.path,
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-vn", "-ac", "1", "-ar", "16000",
		"-f", "s16le", "pipe:1")
	var pcm, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = &pcm
	cmd.Stderr = &stderr
	// Do not wait forever for pipes held open by a killed child.
	cmd.WaitDelay = 2 * time.Second

	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("ffmpeg: %w", ctxErr)
	}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		return nil, fmt.Errorf("%w: ffmpeg exited with %d: %s",
			turn.ErrUndecodable, exitErr.ExitCode(), lastLine(stderr.String()))
	case err != nil:
		return nil, fmt.Errorf("run ffmpeg: %w", err)
	case pcm.Len() == 0:
		return nil, fmt.Errorf("%w: ffmpeg produced no audio", turn.ErrUndecodable)
	}
	return wrapWAV(pcm.Bytes()), nil
}

// wrapWAV prepends a canonical 44-byte WAV header. ffmpeg's own WAV muxer
// cannot fill in the sizes when writing to a pipe, so the header is built
// here, where the length is known.
func wrapWAV(pcm []byte) []byte {
	const blockAlign = channels * bitsPerSample / 8
	header := struct {
		Riff          [4]byte
		RiffSize      uint32
		WaveFmt       [8]byte
		FmtSize       uint32
		Format        uint16
		Channels      uint16
		SampleRate    uint32
		ByteRate      uint32
		BlockAlign    uint16
		BitsPerSample uint16
		Data          [4]byte
		DataSize      uint32
	}{
		Riff:          [4]byte{'R', 'I', 'F', 'F'},
		RiffSize:      uint32(36 + len(pcm)),
		WaveFmt:       [8]byte{'W', 'A', 'V', 'E', 'f', 'm', 't', ' '},
		FmtSize:       16,
		Format:        1, // integer PCM
		Channels:      channels,
		SampleRate:    sampleRate,
		ByteRate:      sampleRate * blockAlign,
		BlockAlign:    blockAlign,
		BitsPerSample: bitsPerSample,
		Data:          [4]byte{'d', 'a', 't', 'a'},
		DataSize:      uint32(len(pcm)),
	}
	var buf bytes.Buffer
	buf.Grow(44 + len(pcm))
	binary.Write(&buf, binary.LittleEndian, &header)
	buf.Write(pcm)
	return buf.Bytes()
}

// lastLine keeps ffmpeg's final error line, which names the problem.
func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return "no error output"
	}
	return s
}
