package enc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

// TestFBMatrixKnownAnswer recomputes every fbMatrix[b][j] with math.Cos and
// requires agreement with the committed literal within 1 ULP. This is a
// test-side tolerance for the test's own math.Cos call; the committed
// literal (generated once by a throwaway program, see fbtables.go) is the
// runtime truth and carries no math.Cos call itself.
func TestFBMatrixKnownAnswer(t *testing.T) {
	for b := range 32 {
		for j := range 64 {
			want := math.Cos(float64(2*b+1) * float64(j-16) * math.Pi / 64)
			got := fbMatrix[b][j]
			if diff := ulpDistance(got, want); diff > 1 {
				t.Fatalf("fbMatrix[%d][%d] = %v (bits %x), want %v (bits %x), diff %d ULP",
					b, j, got, math.Float64bits(got), want, math.Float64bits(want), diff)
			}
		}
	}
}

// ulpDistance walks the float64 sequence from a towards b with
// math.Nextafter and returns the number of representable steps between
// them, capped at maxULP+1 so a badly wrong value fails fast instead of
// looping. This sidesteps the bit-pattern sign-boundary pitfalls of a
// direct integer-bits subtraction.
const maxULP = 4

func ulpDistance(a, b float64) int {
	if a == b {
		return 0
	}
	for steps := 1; steps <= maxULP; steps++ {
		a = math.Nextafter(a, b)
		if a == b {
			return steps
		}
	}
	return maxULP + 1
}

// TestFBWindowChecksum guards fbWindow's 512 committed literals against
// accidental edits with a golden sha256 over their float64 bit patterns,
// same pattern as TestHuffmanTablesMatchOracle
// (internal/dec/huffman_test.go:31). Frozen on first run.
func TestFBWindowChecksum(t *testing.T) {
	const wantHex = "7995d3fca1baed4209c9d0d609a8c8db3a024967498685731ed78e782e43178b"

	h := sha256.New()
	var buf8 [8]byte
	for _, v := range fbWindow {
		binary.LittleEndian.PutUint64(buf8[:], math.Float64bits(v))
		h.Write(buf8[:])
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		t.Fatalf("fbWindow checksum = %s, want %s", got, wantHex)
	}
}

// TestFBWindowStructure documents fbWindow's shape with cheap, independent
// sentinel checks so a wrong-sized or grossly mistransribed table fails
// fast without needing the exact checksum: 512 entries, C[0] == 0, the peak
// magnitude sits near the center, and the coefficient sum is within 10% of
// a value frozen on first run (a transcription tripwire: a single digit
// dropped or a sign flipped on a large coefficient blows this by far more
// than 10%).
func TestFBWindowStructure(t *testing.T) {
	if len(fbWindow) != 512 {
		t.Fatalf("len(fbWindow) = %d, want 512", len(fbWindow))
	}
	if fbWindow[0] != 0 {
		t.Fatalf("fbWindow[0] = %v, want 0", fbWindow[0])
	}

	peakIdx := 0
	for i, v := range fbWindow {
		if math.Abs(v) > math.Abs(fbWindow[peakIdx]) {
			peakIdx = i
		}
	}
	if peakIdx < 200 || peakIdx > 312 {
		t.Fatalf("peak magnitude at index %d, want within [200, 312]", peakIdx)
	}

	sum := 0.0
	for _, v := range fbWindow {
		sum += v
	}
	const wantSum = 0.04419612813949585
	if math.Abs(sum-wantSum) > math.Abs(wantSum)*0.10 {
		t.Fatalf("fbWindow sum = %v, want within 10%% of %v", sum, wantSum)
	}
}

// TestFBSineConcentration feeds a 3-granule sine at each test band's center
// frequency and checks the analysis output concentrates there: at least 99%
// of the output energy in granules after the first lands in bands b-1..b+1,
// and every band at distance >= 3 from b sits at least 80 dB below b's own
// level. A single wrong window digit of meaningful magnitude breaks the
// stopband and fails this test long before TestReconstructionGate would
// catch it.
func TestFBSineConcentration(t *testing.T) {
	const sr = 44100.0
	const granules = 3
	const samplesPerGranule = 18 * 32

	for _, b := range []int{1, 5, 15, 30} {
		freq := (float64(b) + 0.5) * sr / 64
		var fb Filterbank
		var outs [granules][18][32]float64
		in := make([]float64, samplesPerGranule)

		for g := range granules {
			for i := range samplesPerGranule {
				n := float64(g*samplesPerGranule + i)
				in[i] = math.Sin(2 * math.Pi * freq * n / sr)
			}
			fb.AnalyzeGranule(in, &outs[g])
		}

		// Per-band energy over granules after the first (steady state).
		var energy [32]float64
		for g := 1; g < granules; g++ {
			for tt := range 18 {
				for band := range 32 {
					v := outs[g][tt][band]
					energy[band] += v * v
				}
			}
		}

		total := 0.0
		for _, e := range energy {
			total += e
		}
		near := 0.0
		for band := b - 1; band <= b+1; band++ {
			if band >= 0 && band < 32 {
				near += energy[band]
			}
		}
		if total <= 0 {
			t.Fatalf("band %d: zero total energy", b)
		}
		if frac := near / total; frac < 0.99 {
			t.Fatalf("band %d: bands %d..%d hold %.4f%% of energy, want >= 99%%", b, b-1, b+1, frac*100)
		}

		peak := energy[b]
		for i := range 32 {
			dist := i - b
			if dist < 0 {
				dist = -dist
			}
			if dist < 3 {
				continue
			}
			if energy[i] <= 0 {
				continue
			}
			db := 10 * math.Log10(energy[i]/peak)
			if db > -80 {
				t.Fatalf("band %d: band %d (distance %d) at %.1f dB, want <= -80 dB", b, i, dist, db)
			}
		}
	}
}

// TestFBGolden analyzes 4 granules of LCG-generated pseudo-noise and checks
// a golden sha256 over the output float64 bit patterns. CI's arm64 leg
// failing this test (while amd64 stays green) means an FMA leak in
// AnalyzeGranule; fix the fusion site (an unwrapped product feeding a
// following +/-), never the golden.
func TestFBGolden(t *testing.T) {
	const granules = 4
	const samplesPerGranule = 18 * 32

	var seed uint64 = 1
	next := func() float64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return (float64(seed>>11)/float64(1<<53))*2 - 1
	}

	var fb Filterbank
	h := sha256.New()
	var buf8 [8]byte
	var out [18][32]float64
	in := make([]float64, samplesPerGranule)

	for range granules {
		for i := range in {
			in[i] = next()
		}
		fb.AnalyzeGranule(in, &out)
		for _, row := range out {
			for _, v := range row {
				binary.LittleEndian.PutUint64(buf8[:], math.Float64bits(v))
				h.Write(buf8[:])
			}
		}
	}

	const wantHex = "593b5f4c950fa16ba359b1c241d51ffb2ccd2abc6a3a3520bec91dd4f4f1a311"
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		t.Fatalf("TestFBGolden checksum = %s, want %s", got, wantHex)
	}
}
