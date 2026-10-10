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

// box builds one ISO-BMFF box: 32-bit size, 4-byte type, payload.
func box(kind, payload string) string {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(8+len(payload)))
	b.WriteString(kind)
	b.WriteString(payload)
	return b.String()
}

// inputFromFileOnly fails when asked to read stdin and echoes the named
// input file as the "PCM", so a test can tell how the input was passed.
const inputFromFileOnly = `
in=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-i" ]; then in="$2"; fi
  shift
done
if [ "$in" = "pipe:0" ]; then echo "input came through the pipe" >&2; exit 9; fi
exec cat "$in"`

func emptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestDecodeMP4WithIndexAtEndUsesTempFile(t *testing.T) {
	// moov after mdat: ffmpeg would need to seek back, which a pipe cannot.
	input := []byte(box("ftyp", "M4A isom") + box("mdat", "audio-samples") + box("moov", "index"))
	dir := t.TempDir()
	dec := audio.NewFFmpeg(fakeFFmpeg(t, inputFromFileOnly))
	dec.TempDir = dir

	wav, err := dec.Decode(context.Background(), input)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	checkHeader(t, wav)
	if !bytes.Equal(wav[44:], input) {
		t.Errorf("ffmpeg read %q, want the uploaded bytes", wav[44:])
	}
	emptyDir(t, dir)
}

func TestDecodeRemovesTempFileOnFailure(t *testing.T) {
	input := []byte(box("ftyp", "M4A ") + box("mdat", "x") + box("moov", "y"))
	dir := t.TempDir()
	dec := audio.NewFFmpeg(fakeFFmpeg(t, `echo "moov atom not found" >&2; exit 1`))
	dec.TempDir = dir

	if _, err := dec.Decode(context.Background(), input); !errors.Is(err, turn.ErrUndecodable) {
		t.Fatalf("err = %v, want ErrUndecodable", err)
	}
	emptyDir(t, dir)
}

func TestDecodeStreamableMP4UsesPipe(t *testing.T) {
	pipeOnly := `
case "$*" in
  *"-i pipe:0"*) exec cat ;;
  *) echo "unexpected args: $*" >&2; exit 2 ;;
esac`
	for name, input := range map[string]string{
		"index first":        box("ftyp", "isom") + box("moov", "index") + box("mdat", "samples"),
		"fragmented":         box("ftyp", "iso5") + box("moov", "mvex") + box("moof", "f") + box("mdat", "s"),
		"truncated box":      box("ftyp", "isom") + "\x00\x00\xff\xffmdat",
		"not mp4":            "OggS-not-an-mp4-at-all",
		"bad size":           "\x00\x00\x00\x02ftypxxxxxxxx",
		"huge 64-bit size":   box("ftyp", "isom") + "\x00\x00\x00\x01mdat\xff\xff\xff\xff\xff\xff\xff\xff",
		"size zero to end":   box("ftyp", "isom") + "\x00\x00\x00\x00mdat rest of file",
		"moov before mdat64": box("ftyp", "isom") + box("moov", "i") + "\x00\x00\x00\x01mdat\x00\x00\x00\x00\x00\x00\x00\x11s",
	} {
		t.Run(name, func(t *testing.T) {
			wav, err := audio.NewFFmpeg(fakeFFmpeg(t, pipeOnly)).Decode(context.Background(), []byte(input))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if string(wav[44:]) != input {
				t.Errorf("PCM = %q", wav[44:])
			}
		})
	}
}

func TestDecodeMP4WithLargeMdatBeforeMoovUsesTempFile(t *testing.T) {
	// A 64-bit mdat size, as large recordings use, still counts as index at end.
	mdat := "\x00\x00\x00\x01mdat\x00\x00\x00\x00\x00\x00\x00\x14samp"
	input := []byte(box("ftyp", "M4A ") + mdat + box("moov", "index"))
	dec := audio.NewFFmpeg(fakeFFmpeg(t, inputFromFileOnly))
	dec.TempDir = t.TempDir()
	if _, err := dec.Decode(context.Background(), input); err != nil {
		t.Fatalf("Decode: %v", err)
	}
}

func TestRealFFmpegDecodesMP4WithIndexAtEnd(t *testing.T) {
	bin := realFFmpeg(t)
	// 60 s of AAC in a plain (non-fragmented) MP4, moov written last, as a
	// recorder that finalises the file does. Through a pipe this decodes
	// to nothing; it must go through a seekable file.
	src := filepath.Join(t.TempDir(), "speech.m4a")
	gen := exec.Command(bin, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=60",
		"-c:a", "aac", "-b:a", "128k", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot encode AAC here: %v: %s", err, out)
	}
	input, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	dec := audio.NewFFmpeg(bin)
	dec.TempDir = dir
	wav, err := dec.Decode(context.Background(), input)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	checkHeader(t, wav)
	if seconds := float64(len(wav)-44) / 32000; seconds < 59 || seconds > 61 {
		t.Errorf("decoded %.2f s, want about 60 s", seconds)
	}
	emptyDir(t, dir)
}
