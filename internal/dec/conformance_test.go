package dec

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vectorPaths returns every ISO conformance vector under testdata/vectors,
// resolved relative to the package directory the same way fixturePaths
// (dumps_test.go) resolves testdata/fixtures. Fetched by
// scripts/fetch-vectors.sh and gitignored (ISO-copyrighted material is
// never committed).
func vectorPaths(t *testing.T) []string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join("..", "..", "testdata", "vectors", "*.bit"))
	if err != nil {
		t.Fatalf("globbing vectors: %v", err)
	}
	return matches
}

// layer1And2Vectors lists every ISO vector whose bitstream carries Layer I
// and/or Layer II content only (confirmed by decoding every vector with
// this package's own DecodeFrame and recording info.Layer per frame; none
// of these ever produced a Layer III frame). This is a static, filename-keyed
// map, but it is not trusted blindly: validateLayer1And2Skip re-decodes each
// listed vector on every test run and fails loudly (t.Fatalf) if it now
// produces a Layer III frame, so a future vectors-corpus pin bump that
// changes a vector's content cannot silently widen the skip list.
// DecodeFrame's declared scope is Layer III only (see its doc comment in
// decode.go): a non-Layer-III frame is recognized and sized but always
// yields 0 samples. The pinned
// oracle, built from tools/oracle/mp3dump.c which defines only
// MINIMP3_IMPLEMENTATION (not MINIMP3_ONLY_MP3), still decodes Layer I/II
// in full. So for every vector below, this decoder's PCM output is
// unconditionally empty while the oracle's is not: the two can never be
// bit-exact, and no PSNR comparison against the .pcm reference is
// meaningful either. This is a scope boundary carried over from Task 10's
// DecodeFrame, not a defect discovered here; Layer I/II decoding is not
// part of this project (see CLAUDE.md, PROVENANCE.md). The map value is
// the Layer number found (1 or 2), which doubles as the per-vector reason.
var layer1And2Vectors = map[string]int{
	// ISO/IEC 11172-4 Layer II conformance set ("ILL2_"/"ILL4_" prefixes
	// from the minimp3 vectors corpus). ILL2_layer1 and ILL2_layer3 are
	// deliberately absent here: despite the shared prefix their content is
	// Layer I and Layer III respectively, not Layer II.
	"ILL2_center2":       2,
	"ILL2_dual":          2,
	"ILL2_dynx22":        2,
	"ILL2_dynx31":        2,
	"ILL2_dynx32":        2,
	"ILL2_ext_switching": 2,
	"ILL2_mono":          2,
	"ILL2_multilingual":  2,
	"ILL2_overalloc1":    2,
	"ILL2_prediction":    2,
	"ILL2_samples":       2,
	"ILL2_scf63":         2,
	"ILL2_tca21":         2,
	"ILL2_tca30":         2,
	"ILL2_tca30_PC":      2,
	"ILL2_tca31_PC":      2,
	"ILL2_tca31_mtx0":    2,
	"ILL2_tca31_mtx2":    2,
	"ILL2_tca32_PC":      2,
	"ILL2_wrongcrc":      2,
	"ILL4_ext_id1":       2,
	"ILL4_sync":          2,
	"ILL4_wrong_length1": 2,
	"ILL4_wrong_length2": 2,
	"ILL4_wrongcrc":      2,

	// Layer I conformance set.
	"ILL2_layer1": 1,
	"l1-fl1":      1,
	"l1-fl2":      1,
	"l1-fl3":      1,
	"l1-fl4":      1,
	"l1-fl5":      1,
	"l1-fl6":      1,
	"l1-fl7":      1,
	"l1-fl8":      1,

	// Layer II "free format" / bitrate-index conformance set.
	"l2-fl10":                    2,
	"l2-fl11":                    2,
	"l2-fl12":                    2,
	"l2-fl13":                    2,
	"l2-fl14":                    2,
	"l2-fl15":                    2,
	"l2-fl16":                    2,
	"l2-nonstandard-free_format": 2,
	"l2-nonstandard-test32-size": 2,
	"l2-test32":                  2,

	// Named "l2-" but its content, per the header scan, is Layer I.
	"l2-nonstandard-fl1_fl2_ff": 1,
}

// psnrSkipVectors lists ISO vectors, all Layer III and therefore included
// in the bit-exact-vs-oracle gate below, whose .pcm reference is not
// usable for a meaningful PSNR comparison against this decoder's output,
// each for a specific, individually verified reason. Vectors are NOT
// listed here merely because one side has zero samples: TestConformanceVectors
// already skips the PSNR step generically whenever the got/reference
// overlap is empty (that covers, e.g., l3-nonstandard-apetag/id3v1/
// id3v1-apetag/id3v2, whose oracle output is 0 samples because the lone
// audio frame has no confirming second frame for mp3d_match_frame to sync
// on, while the .pcm reference has 1152; and l3-nonstandard-id3v2-only/
// small/vbrtag-corrupted/sideinfo-size, where both sides are 0). This map
// is only for vectors that need an explicit, human-verified reason beyond
// that generic rule.
var psnrSkipVectors = map[string]string{
	// Contains a LAME/Xing VBR tag frame followed by real 1kHz-tone audio.
	// Neither this decoder nor the pinned oracle special-cases Xing/LAME
	// tag detection (out of scope; the tag frame is decoded as if it were
	// audio, same as the tag-only l3-nonstandard-vbrtag-* vectors), so the
	// oracle's sample count (730368) doesn't match the reference's
	// (725760, LAME's gapless-trim length): a difference of 4608 samples,
	// 2 stereo frames. Unlike the l3-he_*/l3-si*/l3-hecommon vectors below,
	// this is not a simple trailing-frame truncation: no whole-frame shift
	// (checked -5..+5 frames of 2304 samples each) brought PSNR anywhere
	// near the 96 dB bar (best was 11.9 dB at +1 frame). This is LAME's
	// encoder-delay/padding trim, which only a Xing-tag-aware decoder
	// applies; bit-exact-vs-oracle is still required and holds.
	"l3-nonstandard-sin1k0db_lame_vbrtag": "LAME gapless trim not applied here or by the oracle; not a whole-frame shift",

	// No .pcm reference ships for this vector in the fetched corpus
	// (verified: no testdata/vectors/l3-nonstandard-big-iscf.pcm; the
	// corpus instead contains an orphan l3-sin1k0db_ofs633.pcm with no
	// matching .bit). Nothing to compare against.
	"l3-nonstandard-big-iscf": "no .pcm reference file present in the fetched vector corpus",
}

// decodeFullStream drives a fresh stateful Decoder across data the same way
// TestFullStreamMatchesOracle (decode_test.go) does: advance by
// info.FrameBytes, append only when the decoder returned samples, and stop
// when no further progress is possible. It also reports whether any
// decoded frame used intensity stereo or a mixed block, so
// TestConformanceVectors can assert that the ISO corpus actually exercises
// those two faithfully-ported paths the LAME fixture corpus never reaches
// (see CLAUDE.md's Task 11 carry-forward note), and whether any frame was
// Layer III at all, so callers can self-validate a layer1And2Vectors skip
// (see validateLayer1And2Skip) instead of trusting the static map blindly.
func decodeFullStream(data []byte) (pcm []float32, sawIStereo, sawMixed, sawLayer3 bool) {
	d := NewDecoder()
	buf := make([]float32, maxSamplesPerFrame)
	var info FrameInfo

	pos := 0
	for pos < len(data) {
		n := d.DecodeFrame(data[pos:], buf, &info)
		if n > 0 {
			pcm = append(pcm, buf[:n*info.Channels]...)
		}
		if info.Layer == 3 {
			sawLayer3 = true
			if hdrTestIStereo(d.header[:]) {
				sawIStereo = true
			}
			for _, gi := range d.scratch.grInfo {
				if gi.mixedBlockFlag != 0 {
					sawMixed = true
				}
			}
		}
		if info.FrameBytes <= 0 {
			break
		}
		pos += info.FrameBytes
	}
	return pcm, sawIStereo, sawMixed, sawLayer3
}

// validateLayer1And2Skip fails the test loudly if a vector listed in
// layer1And2Vectors as Layer I/II-only actually produced a Layer III frame
// on this decode. layer1And2Vectors is a static, filename-keyed map built
// once by inspection; this is what stops it from drifting silently (e.g. a
// vectors-corpus pin bump that changes a vector's content) into quietly
// excluding a vector that should be gated by TestConformanceVectors.
func validateLayer1And2Skip(t *testing.T, name string, layer int, sawLayer3 bool) {
	t.Helper()
	if sawLayer3 {
		t.Fatalf("layer1And2Vectors marks %q as Layer %d only, but decoding it produced a Layer III frame; "+
			"the skip list is stale (corpus pin bump?) and must be updated", name, layer)
	}
}

// f32ToS16 mirrors upstream mp3dec_f32_to_s16's scalar path exactly
// (tools/oracle/minimp3.h:1849-1862): scale by 32768, saturate at the
// int16 extremes, otherwise truncate-toward-zero after adding 0.5 and then
// step one further away from zero for negative values ("to be compliant",
// per the upstream comment). This is the quantization the .pcm reference
// files were produced through, so comparing against them requires
// replaying the same rounding, not a plain cast.
func f32ToS16(sample float32) int16 {
	s := sample * 32768.0
	switch {
	case s >= 32766.5:
		return 32767
	case s <= -32767.5:
		return -32768
	default:
		v := int16(s + 0.5)
		if v < 0 {
			v--
		}
		return v
	}
}

// readPCM16Reference reads an ISO conformance vector's little-endian int16
// reference PCM (testdata/vectors/<name>.pcm). It returns nil, without
// failing the test, when the file does not exist: a small number of
// vectors in the fetched corpus ship no reference (see psnrSkipVectors),
// and TestConformanceVectors' generic empty-overlap rule handles a nil
// result the same way it handles a present-but-empty one.
func readPCM16Reference(t *testing.T, path string) []int16 {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading reference PCM %s: %v", path, err)
	}
	if len(data)%2 != 0 {
		t.Fatalf("reference PCM %s: length %d is not a multiple of 2", path, len(data))
	}

	out := make([]int16, len(data)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
	}
	return out
}

// psnrVsReference computes the PSNR, in dB, of got (Go's float32 PCM)
// against ref (the ISO reference's raw int16 samples), quantizing got to
// int16 via f32ToS16 first so both sides are compared in the same domain
// the reference was generated in. It uses the standard formula
// 20*log10(peak) - 10*log10(mse) with peak fixed at the full int16 range
// (32767), matching a 16-bit reference signal; got and ref must already be
// the same length (callers truncate to their common prefix, see
// TestConformanceVectors).
func psnrVsReference(got []float32, ref []int16) float64 {
	var se float64
	for i, r := range ref {
		d := float64(f32ToS16(got[i])) - float64(r)
		se += d * d
	}
	mse := se / float64(len(ref))
	if mse == 0 {
		return math.Inf(1)
	}
	return 20*math.Log10(32767.0) - 10*math.Log10(mse)
}

// TestConformanceVectors decodes every ISO conformance vector
// (testdata/vectors/*.bit) with the Go decoder and requires:
//
//  1. Bit-exact equality against the pinned oracle's PCM dump
//     (tools/oracle/dumps/<name>.bit/pcm.f32le), the same differential gate
//     Tasks 5-10 apply to the LAME fixture corpus, extended here to the ISO
//     corpus's intensity-stereo and mixed-block content that corpus never
//     exercises.
//  2. PSNR of that output against the vector's own .pcm reference, in the
//     int16 domain the reference was generated in, >= 96 dB: the ISO
//     conformance bar, not merely oracle agreement.
//
// Layer I/II vectors are skipped (layer1And2Vectors, out of this decoder's
// declared scope); a handful of Layer III vectors skip only the PSNR step
// (psnrSkipVectors, plus the generic empty-overlap rule below), each for a
// documented reason. Every skip is a t.Skip, visible in -v output, not a
// silent omission.
func TestConformanceVectors(t *testing.T) {
	const minPSNRdB = 96.0

	for _, fx := range vectorPaths(t) {
		name := strings.TrimSuffix(filepath.Base(fx), ".bit")

		t.Run(name, func(t *testing.T) {
			data := readFile(t, fx)
			got, sawIStereo, sawMixed, sawLayer3 := decodeFullStream(data)

			if layer, skip := layer1And2Vectors[name]; skip {
				validateLayer1And2Skip(t, name, layer, sawLayer3)
				t.Skipf("Layer %d content, outside this decoder's Layer III scope", layer)
			}

			want := readF32File(t, dumpPath(fx, "pcm.f32le"))
			compareBitExact(t, fx, got, want)
			if sawIStereo {
				t.Log("exercised intensity stereo")
			}
			if sawMixed {
				t.Log("exercised a mixed block")
			}

			if reason, skip := psnrSkipVectors[name]; skip {
				t.Skipf("PSNR-vs-reference skipped: %s", reason)
			}

			refPath := filepath.Join("..", "..", "testdata", "vectors", name+".pcm")
			ref := readPCM16Reference(t, refPath)
			n := len(got)
			if len(ref) < n {
				n = len(ref)
			}
			if n == 0 {
				t.Skipf("nothing to compare: got %d samples, reference has %d", len(got), len(ref))
			}

			psnr := psnrVsReference(got[:n], ref[:n])
			if psnr < minPSNRdB {
				t.Fatalf("PSNR = %.2f dB over %d overlapping samples (got %d, reference %d), want >= %.1f dB",
					psnr, n, len(got), len(ref), minPSNRdB)
			}
			t.Logf("PSNR = %.2f dB over %d samples (got %d, reference %d)", psnr, n, len(got), len(ref))
		})
	}
}

// TestConformanceVectorsExerciseIntensityStereoAndMixedBlocks decodes every
// non-Layer-I/II vector once more and asserts that, across the ISO corpus
// as a whole, at least one vector used intensity stereo and at least one
// used a mixed block. Task 10's carry-forward note (CLAUDE.md) flagged
// these two faithfully-ported paths as untested by the LAME fixture
// corpus; this is the coverage assertion that closes that gap, so a future
// change that silently dropped ISO vectors from the corpus (rather than
// merely a single test's skip) would fail loudly here instead of quietly
// losing the coverage TestConformanceVectors above depends on. It also
// self-validates every layer1And2Vectors skip it encounters, the same as
// TestConformanceVectors, so this loop cannot quietly widen its Layer I/II
// exclusion either.
func TestConformanceVectorsExerciseIntensityStereoAndMixedBlocks(t *testing.T) {
	vectors := vectorPaths(t)
	// An absent corpus is a skip, not a failure, mirroring the dump-absence
	// convention in dumps_test.go / decode_test.go: a plain checkout (or the
	// ci.yml Test job, which does not fetch the ISO vectors) has nothing to
	// exercise, while MP3_REQUIRE_DUMPS (set by oracle.yml, where the vectors
	// are present) turns the absence into a hard failure so CI proves the
	// coverage assertion actually ran.
	if len(vectors) == 0 {
		if os.Getenv("MP3_REQUIRE_DUMPS") != "" {
			t.Fatal("conformance vectors required but none found (run scripts/fetch-vectors.sh first)")
		}
		t.Skip("no conformance vectors found (run scripts/fetch-vectors.sh first)")
	}

	anyIStereo, anyMixed := false, false
	var iStereoVectors, mixedVectors []string

	for _, fx := range vectors {
		name := strings.TrimSuffix(filepath.Base(fx), ".bit")
		data := readFile(t, fx)
		_, sawIStereo, sawMixed, sawLayer3 := decodeFullStream(data)

		if layer, skip := layer1And2Vectors[name]; skip {
			validateLayer1And2Skip(t, name, layer, sawLayer3)
			continue
		}

		if sawIStereo {
			anyIStereo = true
			iStereoVectors = append(iStereoVectors, name)
		}
		if sawMixed {
			anyMixed = true
			mixedVectors = append(mixedVectors, name)
		}
	}

	t.Logf("intensity stereo exercised by: %v", iStereoVectors)
	t.Logf("mixed blocks exercised by: %v", mixedVectors)

	if !anyIStereo {
		t.Error("no ISO vector exercised intensity stereo; l3IntensityStereo is untested")
	}
	if !anyMixed {
		t.Error("no ISO vector exercised a mixed block; the mixed-block reorder/antialias path is untested")
	}
}
