package mp3_test

import (
	"os"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// sine48m128Fixture is a 48 kHz mono, 128 kbps CBR fixture: 86 frames of
// 1152 samples/channel each (verified against the internal/dec decoder),
// for a total of 99072 samples per channel.
const sine48m128Fixture = "testdata/fixtures/sine48m_128.mp3"

// decodeAll drives d over the whole file with the public API, mirroring
// internal/dec's TestFullStreamMatchesOracle loop: advance by
// info.FrameBytes (which already includes FrameOffset), stop when no
// further progress is possible.
func decodeAll(t *testing.T, d *mp3.Decoder, data []byte) (samples []float32, info mp3.FrameInfo) {
	t.Helper()

	pcm := make([]float32, 1152*2)
	pos := 0
	for pos < len(data) {
		n, fi, err := d.DecodeFrame(data[pos:], pcm)
		if err != nil {
			t.Fatalf("DecodeFrame at pos %d: %v", pos, err)
		}
		info = fi
		if n > 0 {
			samples = append(samples, pcm[:n*fi.Channels]...)
		}
		if fi.FrameBytes == 0 {
			break
		}
		pos += fi.FrameBytes
	}
	return samples, info
}

func TestDecodeFrameFullStream(t *testing.T) {
	data, err := os.ReadFile(sine48m128Fixture)
	if err != nil {
		t.Fatal(err)
	}

	d := mp3.NewDecoder()
	pcm := make([]float32, 1152*2)
	n, info, err := d.DecodeFrame(data, pcm)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if n != 1152 {
		t.Errorf("first frame samples/channel = %d, want 1152", n)
	}
	if info.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", info.SampleRate)
	}
	if info.Channels != 1 {
		t.Errorf("Channels = %d, want 1", info.Channels)
	}
	if info.Layer != 3 {
		t.Errorf("Layer = %d, want 3", info.Layer)
	}
	if info.FrameBytes == 0 {
		t.Fatal("FrameBytes = 0 on the first frame of a valid stream")
	}

	// Continue over the rest of the file and check the total sample count.
	// The fixture's 86 frames divide the file exactly, so this loop's last
	// call decodes the 86th frame and then stops via decodeAll's `pos <
	// len(data)` condition; it never needs an explicit FrameBytes == 0
	// call, which TestDecodeFrameEmptyInput covers separately.
	rest, _ := decodeAll(t, d, data[info.FrameBytes:])
	total := n*info.Channels + len(rest)
	const wantSamplesPerChannel = 99072 // 86 frames * 1152 samples/channel, mono
	if total != wantSamplesPerChannel {
		t.Errorf("total samples = %d, want %d", total, wantSamplesPerChannel)
	}
}

// TestDecodeFrameGarbageInput feeds bytes with no recognizable MP3 header.
// The whole input is reported as skippable (FrameBytes == len(garbage),
// Layer == 0), and this is not an error: see DecodeFrame's doc comment for
// why Layer == 0 is distinct from a recognized-but-unsupported layer.
func TestDecodeFrameGarbageInput(t *testing.T) {
	d := mp3.NewDecoder()
	pcm := make([]float32, 1152*2)
	garbage := []byte{0x00, 0x01, 0x02}
	n, info, err := d.DecodeFrame(garbage, pcm)
	if err != nil {
		t.Errorf("err = %v, want nil (no recognized header is not an error)", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
	if info.Layer != 0 {
		t.Errorf("Layer = %d, want 0 (no header recognized)", info.Layer)
	}
	if info.FrameBytes != len(garbage) {
		t.Errorf("FrameBytes = %d, want %d (whole input treated as skippable)", info.FrameBytes, len(garbage))
	}
}

func TestDecodeFrameEmptyInput(t *testing.T) {
	d := mp3.NewDecoder()
	pcm := make([]float32, 1152*2)
	n, info, err := d.DecodeFrame(nil, pcm)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if n != 0 || info.FrameBytes != 0 {
		t.Errorf("n=%d info.FrameBytes=%d, want 0, 0 on empty input", n, info.FrameBytes)
	}
}

// TestDecodeFrameSteadyStateAllocs mirrors internal/dec's
// TestDecodeSteadyStateAllocs at the public surface: DecodeFrame must not
// allocate per call in steady state, since Decoder wraps a dec.Decoder that
// keeps all persistent state as caller-owned fields (see decoder.go). A
// warmup call primes the fast-path header cache so the measured runs take
// the steady-state path.
func TestDecodeFrameSteadyStateAllocs(t *testing.T) {
	data, err := os.ReadFile("testdata/fixtures/sine44s_128.mp3")
	if err != nil {
		t.Fatal(err)
	}

	d := mp3.NewDecoder()
	pcm := make([]float32, 1152*2)

	if _, _, err := d.DecodeFrame(data, pcm); err != nil { // warmup: prime the fast-path header cache
		t.Fatalf("warmup DecodeFrame: %v", err)
	}

	avg := testing.AllocsPerRun(50, func() {
		if _, _, err := d.DecodeFrame(data, pcm); err != nil {
			t.Fatalf("DecodeFrame: %v", err)
		}
	})
	if avg != 0 {
		t.Fatalf("steady-state allocs = %v, want 0", avg)
	}
}

// TestDecoderResetByteIdentity: Reset between decoding two files must
// produce output identical to using two fresh Decoders, mirroring go-aac's
// TestEncoderResetByteIdentity convention.
//
// The free-format case exercises the Reset()-clears-freeFormatBytes path
// airtight: a free-format stream latches a sticky per-stream frame size in
// Decoder.freeFormatBytes (see internal/dec), so a Reset that failed to
// clear it would carry the first stream's size into the second decode and
// desync it.
func TestDecoderResetByteIdentity(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"cbr", sine48m128Fixture},
		{"freeFormat", "testdata/fixtures/sine44s_free168.mp3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}

			fresh := mp3.NewDecoder()
			want, _ := decodeAll(t, fresh, data)

			// Prime reused with a full decode so it carries stream state, then
			// reset it and decode the same file again.
			reused := mp3.NewDecoder()
			decodeAll(t, reused, data)
			reused.Reset()

			got, _ := decodeAll(t, reused, data)

			if len(got) != len(want) {
				t.Fatalf("post-Reset decode length = %d, want %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("post-Reset decode differs at sample %d: got %v, want %v", i, got[i], want[i])
				}
			}
		})
	}
}
