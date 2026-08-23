package enc

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestEncodeGoldenForcedLR is the durable executable form of the
// byte-identity claim in TestEncodeGolden's doc comment: with the DECIDE
// phase pinned to L/R (forceLRForTest), the encoder must reproduce the
// exact pre-M/S golden hashes on the same inputs. This proves the L/R
// coding path (and mono, which never consults msDecide but shares the whole
// granule coding path) is byte-identical to the pre-M/S encoder, so the
// delta in the live goldens reflects only the M/S decision.
//
// The hook is a plain package-level bool with no synchronization: this test
// must NOT call t.Parallel(), and restores the hook via t.Cleanup so it
// cannot leak into other tests (including repeated runs under -count>1,
// where each run sets and restores it independently).
func TestEncodeGoldenForcedLR(t *testing.T) {
	forceLRForTest = true
	t.Cleanup(func() { forceLRForTest = false })

	// The three non-correlated TestEncodeGolden cases with their pre-M/S
	// hashes (frozen before Phase 4 increment 6 Task 2; see TestEncodeGolden's
	// doc comment, which documents these exact values).
	cases := []struct {
		name                 string
		sampleRate, ch, kbps int
		wantHex              string
	}{
		{"44100_2ch_128kbps", 44100, 2, 128, "c734a1491e179a2bf6386ef3d465c2177817660b901226d8ab0523ee7930ebda"},
		{"48000_1ch_320kbps", 48000, 1, 320, "d1d7d99887552f2b2ddc4dde49e74be60fa14988a6732d0f02424a0d1f60da19"},
		{"32000_2ch_32kbps", 32000, 2, 32, "11996294f75b9b529296cae97507e6e84f329ad43462fc387acfaa7687fc1a23"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Config{SampleRate: c.sampleRate, Channels: c.ch, BitrateKbps: c.kbps}
			e, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			// Identical input construction to TestEncodeGolden's
			// non-correlated cases: same seed derivation, 4 frames of amp-1.0
			// planar LCG samples.
			seed := uint64(c.sampleRate)<<32 | uint64(c.kbps)<<8 | uint64(c.ch)
			var stream []byte
			for f := range 4 {
				samples := planarSamples(&seed, c.ch, 1.0)
				stream, err = e.EncodeFrame(stream, samples)
				if err != nil {
					t.Fatalf("frame %d: EncodeFrame: %v", f, err)
				}
			}

			sum := sha256.Sum256(stream)
			got := hex.EncodeToString(sum[:])
			if got != c.wantHex {
				t.Fatalf("forced-L/R sha256 = %s, want pre-M/S golden %s", got, c.wantHex)
			}
		})
	}
}
