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
//
// Re-frozen in Phase 4 increment 7 Task B2, in lockstep with
// TestEncodeGolden's own re-freeze: the psymodel window re-centering and
// the held-frame lookahead (design decisions 9-11) change every stream's
// bytes for the same two reasons TestEncodeGolden's doc comment names in
// full (the analysis window is no longer causal, and the 4-call/no-drain
// stream now holds 3 coded frames instead of 4). The mono case is
// byte-identical to TestEncodeGolden's own mono hash, as always (mono
// never consults msDecide, so forceLRForTest changes nothing for it).
func TestEncodeGoldenForcedLR(t *testing.T) {
	forceLRForTest = true
	t.Cleanup(func() { forceLRForTest = false })

	// The three non-correlated TestEncodeGolden cases with their pre-M/S
	// hashes.
	cases := []struct {
		name                 string
		sampleRate, ch, kbps int
		wantHex              string
	}{
		{"44100_2ch_128kbps", 44100, 2, 128, "0fcfefa621bc0fc0291506297a6ef9d377c2dc6df447e25cbb05036c6a9b2c57"},
		{"48000_1ch_320kbps", 48000, 1, 320, "cc1886c0c5e01dd9640b7df2b382fbb5045adf08c525efb914093728302ba712"},
		{"32000_2ch_32kbps", 32000, 2, 32, "fd1ea422b612fd34e38d95822edb62eb8145359f1f891d2183ea5479581277b3"},
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
