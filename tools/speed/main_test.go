package main

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tphakala/go-mp3"
	"github.com/tphakala/go-mp3/internal/quality"
)

// Literals the tests repeat.
const (
	flagBitrates = "-bitrates"
	flagCheck    = "-check"
	flagRecord   = "-record"
	flagCorpus   = "-corpus"
	flagList     = "-list"
	flagSecs     = "-secs"
	fileA        = "a.wav"
)

// wavHeaderBytes is the canonical header WriteWAV16 emits (internal/quality/wav.go:18-20).
const wavHeaderBytes = 44

// writeTestWAV writes planar channels to dir/name as a 16-bit WAV and returns the path.
func writeTestWAV(t *testing.T, dir, name string, rate int, ch [][]float64) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := quality.WriteWAV16(f, rate, ch); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// sineWAV writes a mono sine of the given length, frequency and amplitude.
func sineWAV(t *testing.T, dir, name string, rate, frames int, hz, amp float64) string {
	t.Helper()
	x := make([]float64, frames)
	for i := range x {
		x[i] = amp * math.Sin(2*math.Pi*hz*float64(i)/float64(rate))
	}
	return writeTestWAV(t, dir, name, rate, [][]float64{x})
}

// independentHash encodes the first frames sample frames of a WAV file's
// int16 payload with the frame API, reading the samples straight from the file
// bytes and feeding every returned chunk, drain included, to a running hash.
func independentHash(t *testing.T, path string, rate, channels, frames, kbps int) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data := b[wavHeaderBytes:]
	pcm := make([][]float32, channels)
	for c := range pcm {
		pcm[c] = make([]float32, frames)
		for i := range frames {
			off := (i*channels + c) * 2
			v := int16(uint16(data[off]) | uint16(data[off+1])<<8)
			pcm[c][i] = float32(v) / 32768
		}
	}
	e, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: rate, Channels: channels, Bitrate: kbps * 1000})
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	var dst []byte
	views := make([][]float32, channels)
	for pos := 0; pos < frames; pos += mp3.FrameSize {
		end := min(pos+mp3.FrameSize, frames)
		for c := range views {
			views[c] = pcm[c][pos:end]
		}
		if dst, err = e.EncodeFrame(dst[:0], views); err != nil {
			t.Fatal(err)
		}
		h.Write(dst)
	}
	if dst, err = e.EncodeFrame(dst[:0], nil); err != nil {
		t.Fatal(err)
	}
	h.Write(dst)
	return hex.EncodeToString(h.Sum(nil))
}

// TestSpeedHashMatchesFrameEncode pins the tool's load, cut and encode path
// to an independent frame-API encode of the same file bytes: a dropped drain,
// a wrong cut length, a padded or rejected short file, kbps passed as bits
// per second, swapped channels or a changed sample conversion all change the
// hash.
func TestSpeedHashMatchesFrameEncode(t *testing.T) {
	const rate, kbps, total = 48000, 128, 72000
	prog, ok := quality.ProgramByName("stereo-wide")
	if !ok {
		t.Fatal("program stereo-wide missing")
	}
	path := writeTestWAV(t, t.TempDir(), "wide.wav", rate, prog.Gen(rate, total))

	tests := []struct {
		name       string
		secs       int
		wantFrames int
	}{
		{"cut at secs", 1, 48000},            // 41 full frames and a 768-sample tail
		{"file shorter than secs", 3, 72000}, // whole file, 62 full frames and a 576-sample tail
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := loadWAV(path)
			if err != nil {
				t.Fatal(err)
			}
			pcm, _ := w.cut(tt.secs)
			if len(pcm) != 2 || len(pcm[0]) != tt.wantFrames {
				t.Fatalf("cut(%d) gave %d channels of %d frames, want 2 of %d", tt.secs, len(pcm), len(pcm[0]), tt.wantFrames)
			}
			stream, _, err := encodeStream(pcm, w.rate, kbps, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := hashHex(stream)
			want := independentHash(t, path, rate, 2, tt.wantFrames, kbps)
			if got != want {
				t.Errorf("hash %s, want %s", got, want)
			}
		})
	}
}
