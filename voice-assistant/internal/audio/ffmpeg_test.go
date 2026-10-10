package audio_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/audio"
	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/turn"
)

// fakeFFmpeg writes a shell script standing in for ffmpeg, so these tests
// run where ffmpeg is not installed.
func fakeFFmpeg(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

type wavHeader struct {
	Riff          [4]byte
	RiffSize      uint32
	Wave          [4]byte
	Fmt           [4]byte
	FmtSize       uint32
	Format        uint16
	Channels      uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
	Data          [4]byte
	DataSize      uint32
}

func parseHeader(t *testing.T, wav []byte) wavHeader {
	t.Helper()
	var h wavHeader
	if err := binary.Read(bytes.NewReader(wav), binary.LittleEndian, &h); err != nil {
		t.Fatalf("read WAV header: %v", err)
	}
	return h
}

func checkHeader(t *testing.T, wav []byte) {
	t.Helper()
	h := parseHeader(t, wav)
	pcm := uint32(len(wav) - 44)
	if string(h.Riff[:]) != "RIFF" || string(h.Wave[:]) != "WAVE" || string(h.Fmt[:]) != "fmt " ||
		string(h.Data[:]) != "data" || h.Format != 1 || h.Channels != 1 || h.SampleRate != 16000 ||
		h.BitsPerSample != 16 || h.BlockAlign != 2 || h.ByteRate != 32000 ||
		h.DataSize != pcm || h.RiffSize != 36+pcm {
		t.Errorf("header = %+v for %d PCM bytes", h, pcm)
	}
}

func TestDecodeWrapsPCMInWAVHeader(t *testing.T) {
	// The fake checks it is asked for raw 16 kHz mono s16le on stdout and
	// echoes stdin back as the "PCM".
	bin := fakeFFmpeg(t, `
case "$*" in
  *"-i pipe:0"*"-ac 1"*"-ar 16000"*"-f s16le pipe:1"*) exec cat ;;
  *) echo "unexpected args: $*" >&2; exit 2 ;;
esac`)
	wav, err := audio.NewFFmpeg(bin).Decode(context.Background(), []byte("abcdef"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	checkHeader(t, wav)
	if string(wav[44:]) != "abcdef" {
		t.Errorf("PCM = %q", wav[44:])
	}
}

func TestDecodeReportsUndecodableInput(t *testing.T) {
	bin := fakeFFmpeg(t, `cat >/dev/null; echo "pipe:0: Invalid data found when processing input" >&2; exit 183`)
	_, err := audio.NewFFmpeg(bin).Decode(context.Background(), []byte("garbage"))
	if !errors.Is(err, turn.ErrUndecodable) {
		t.Fatalf("err = %v, want ErrUndecodable", err)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("Invalid data found")) {
		t.Errorf("err = %v, want ffmpeg's message", err)
	}
}

func TestDecodeTreatsEmptyOutputAsUndecodable(t *testing.T) {
	bin := fakeFFmpeg(t, `cat >/dev/null; exit 0`)
	if _, err := audio.NewFFmpeg(bin).Decode(context.Background(), []byte("x")); !errors.Is(err, turn.ErrUndecodable) {
		t.Fatalf("err = %v, want ErrUndecodable", err)
	}
}

func TestDecodeMissingBinaryIsNotAClientError(t *testing.T) {
	_, err := audio.NewFFmpeg(filepath.Join(t.TempDir(), "missing")).Decode(context.Background(), []byte("x"))
	if err == nil || errors.Is(err, turn.ErrUndecodable) {
		t.Fatalf("err = %v, want a non-client error", err)
	}
}

func TestDecodeStopsOnCancel(t *testing.T) {
	bin := fakeFFmpeg(t, `exec sleep 30`)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := audio.NewFFmpeg(bin).Decode(ctx, []byte("x"))
	if err == nil || errors.Is(err, turn.ErrUndecodable) {
		t.Errorf("err = %v, want a cancellation error", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("Decode took %v after cancellation", time.Since(start))
	}
}

// The tests below use the real ffmpeg and are skipped where it is missing.
func realFFmpeg(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	return path
}

func TestRealFFmpegDecodesOpus(t *testing.T) {
	bin := realFFmpeg(t)
	// One second of a 48 kHz stereo tone as Ogg/Opus, like a browser upload.
	var ogg bytes.Buffer
	gen := exec.Command(bin, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=1",
		"-ac", "2", "-c:a", "libopus", "-f", "ogg", "pipe:1")
	gen.Stdout = &ogg
	if err := gen.Run(); err != nil {
		t.Skipf("ffmpeg cannot encode opus here: %v", err)
	}

	wav, err := audio.NewFFmpeg(bin).Decode(context.Background(), ogg.Bytes())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	checkHeader(t, wav)
	if seconds := float64(len(wav)-44) / 32000; seconds < 0.9 || seconds > 1.1 {
		t.Errorf("decoded %.2f s, want about 1 s", seconds)
	}
}

func TestRealFFmpegRejectsGarbage(t *testing.T) {
	bin := realFFmpeg(t)
	_, err := audio.NewFFmpeg(bin).Decode(context.Background(), []byte("this is not audio at all"))
	if !errors.Is(err, turn.ErrUndecodable) {
		t.Fatalf("err = %v, want ErrUndecodable", err)
	}
}
