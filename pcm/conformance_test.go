package pcm

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mp3 "github.com/tphakala/go-mp3"
)

// vectorPaths returns every ISO conformance vector under testdata/vectors,
// resolved relative to the pcm package directory the same way
// internal/dec's vectorPaths (conformance_test.go there) resolves
// testdata/fixtures. Fetched by scripts/fetch-vectors.sh and gitignored
// (ISO-copyrighted material is never committed).
func vectorPaths(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "testdata", "vectors", "*.bit"))
	if err != nil {
		t.Fatalf("globbing vectors: %v", err)
	}
	return matches
}

// requireVectors skips the test when no vectors are present locally, and
// fails loudly instead under MP3_REQUIRE_DUMPS: the same convention
// dumps_test.go and internal/dec's conformance_test.go already use, so a
// plain checkout (nothing fetched) is a quiet skip while oracle.yml (which
// fetches the corpus before running tests with MP3_REQUIRE_DUMPS=1) proves
// this coverage actually ran rather than silently reporting green on
// nothing.
func requireVectors(t *testing.T, vectors []string) {
	t.Helper()
	if len(vectors) > 0 {
		return
	}
	if os.Getenv("MP3_REQUIRE_DUMPS") != "" {
		t.Fatal("conformance vectors required but none found (run scripts/fetch-vectors.sh first)")
	}
	t.Skip("no conformance vectors found (run scripts/fetch-vectors.sh first)")
}

// vectorLayer decodes a vector's first frame with the low-level frame API
// and returns its Layer (1, 2, or 3, or 0 if no frame header was found at
// all). This is a runtime check rather than a duplicated static skip-list:
// internal/dec's layer1And2Vectors is the authoritative record of which
// vectors are Layer I/II only, built by inspection there, but re-deriving a
// second copy of that list here would drift the moment one side changed;
// asking the frame API directly for THIS vector's own bytes cannot drift.
func vectorLayer(t *testing.T, data []byte) int {
	t.Helper()
	d := mp3.NewDecoder()
	scratch := make([]float32, 1152*2)
	_, fi, err := d.DecodeFrame(data, scratch)
	if err != nil && !errors.Is(err, mp3.ErrUnsupported) {
		t.Fatalf("decoding vector's first frame: %v", err)
	}
	return fi.Layer
}

// streamingTruncatedTailOK lists ISO vectors whose raw bytes end in a
// syntactically valid MPEG frame header (correct sync, version, layer,
// bitrate, and sample-rate fields) whose declared length overruns the
// remaining bytes: a genuinely truncated final frame. mp3.DecodeFrame,
// called with the whole remaining buffer in one shot, treats this
// tolerantly (it reports no samples for it and stops, exactly as it does
// for any other unconfirmable tail); pcm.Decoder's streaming layer is
// deliberately stricter here (Task 6) and reports mp3.ErrCorruptStream,
// because at true end of stream there is no way to tell a legitimately
// short final frame from data lost in transit. This is the same "the Go
// behavior is the correct, safer one" principle CLAUDE.md's Task 12
// carry-forward note already establishes for reservoir-boundary
// truncation; TestStreamingConformance still requires that every sample
// pcm.Decoder DID emit before hitting that tail is bit-exact against the
// frame-API ground truth, so this only relaxes "must reach a clean EOF",
// never "must not corrupt anything." Verified against this repo's fetched
// vector corpus: every ISO vector in this set is a whole number of real
// audio frames (its Info().TotalSamples divides evenly by 1152/576) plus a
// short, non-contributing trailing header, so the emitted sample count is
// unaffected either way.
var streamingTruncatedTailOK = map[string]bool{
	"l3-compl":    true,
	"l3-sin1k0db": true,
	"l3-nonstandard-compl-sideinfo-bigvalues": true,
	"l3-nonstandard-compl-sideinfo-blocktype": true,
	"l3-nonstandard-compl-sideinfo-size":      true,
}

// streamingNoAudioSkip lists ISO vectors this decoder's streaming layer
// correctly refuses to construct a Decoder for (mp3.ErrCorruptStream at
// NewDecoder, before any Read), each for a specific, verified reason
// distinct from streamingTruncatedTailOK above: these never produce any
// usable audio in the first place, on either side.
//
// "l3-nonstandard-big-iscf" is the one exception: the reason is not that
// this decoder is being appropriately strict, but that this vector ships
// no .pcm reference in the fetched corpus (see internal/dec's
// psnrSkipVectors, which documents the same gap at the frame-API layer)
// and its raw frame-API sample count comes from a stream this decoder's
// own Task 6 hardening does not consider safely decodable; there is
// nothing here to hold either side to.
var streamingNoAudioSkip = map[string]string{
	"l3-nonstandard-big-iscf": "no .pcm reference in the fetched corpus; not a validated ground truth on either side (see internal/dec's psnrSkipVectors)",
}

// TestStreamingConformance decodes every Layer III ISO conformance vector
// through pcm.Decoder end-to-end (WithF32, so the comparison is exact
// float32 bits, not S16 quantization) and requires it to reproduce the
// low-level frame API's own decode of the same bytes: this is what proves
// the streaming/packing layer built on top of the verified frame API (see
// internal/dec's TestConformanceVectors) adds nothing of its own. Layer I/II
// vectors are skipped (outside this decoder's scope, like everywhere else
// in this repo). For the one tag-bearing (LAME) vector in the corpus, it
// additionally requires the emitted length to match the ISO .pcm reference
// exactly: the gapless-trim conformance gate TestGaplessTrim already checks,
// now proven at the streaming layer specifically.
func TestStreamingConformance(t *testing.T) {
	vectors := vectorPaths(t)
	requireVectors(t, vectors)

	for _, fx := range vectors {
		name := strings.TrimSuffix(filepath.Base(fx), ".bit")
		t.Run(name, func(t *testing.T) {
			checkStreamingConformance(t, fx, name)
		})
	}
}

// checkStreamingConformance is TestStreamingConformance's per-vector body,
// split out so each concern (construction-failure classification, the
// read-outcome check, the gapless window, the bit-exact comparison) is its
// own small, separately readable function.
func checkStreamingConformance(t *testing.T, fx, name string) {
	t.Helper()
	raw, err := os.ReadFile(fx)
	if err != nil {
		t.Fatalf("reading %s: %v", fx, err)
	}

	if layer := vectorLayer(t, raw); layer != 3 {
		t.Skipf("Layer %d content, outside this decoder's Layer III scope", layer)
	}

	// Independent ground truth: the frame-API decode of the same raw bytes,
	// tag frame excluded (decodeAllFloat32ViaFrameAPI, options_test.go), with
	// no assumed gapless trim (delay=padding=0 here). The real trim, when one
	// applies, is intersected in below from the streaming Decoder's own
	// gaplessStart/gaplessEnd rather than reconstructed from
	// Info().EncoderDelay/Padding, so it stays correct even for a
	// hypothetical vector whose total is unknown (where the tail is never
	// trimmed despite a nonzero EncoderPadding; see applyLAME in decoder.go).
	all := decodeAllFloat32ViaFrameAPI(t, raw, 0, 0)

	sd, cerr := NewDecoder(bytes.NewReader(raw), WithF32())
	if cerr != nil {
		skipOrFailConstructionError(t, name, cerr, len(all))
		return
	}

	gotBytes, rerr := io.ReadAll(sd)
	requireAcceptableReadOutcome(t, name, rerr)
	got := float32BytesFromLE(t, gotBytes)

	want := gaplessTrim(all, sd)
	requireBitExact(t, got, want)

	if sd.info.EncoderDelay == 0 && sd.info.EncoderPadding == 0 {
		return // no gapless trim on this vector: nothing further to check
	}
	checkPCMReferenceLength(t, name, sd.info.Channels, len(want)/sd.info.Channels)
}

// skipOrFailConstructionError classifies a NewDecoder failure: either a
// documented exception (streamingNoAudioSkip), a case where the frame API
// finds no audio either (an acceptable skip), or an unexpected failure the
// frame API disagrees with (a hard failure).
func skipOrFailConstructionError(t *testing.T, name string, cerr error, frameAPISamples int) {
	t.Helper()
	if reason, ok := streamingNoAudioSkip[name]; ok {
		t.Skipf("streaming construction declined (%v): %s", cerr, reason)
	}
	if !errors.Is(cerr, mp3.ErrCorruptStream) || frameAPISamples != 0 {
		t.Fatalf("NewDecoder: %v (frame API found %d samples in the same bytes)", cerr, frameAPISamples)
	}
	t.Skipf("streaming construction declined (%v); the frame API finds no audio here either", cerr)
}

// requireAcceptableReadOutcome fails the test on any ReadAll error that is
// not the documented truncated-tail policy (streamingTruncatedTailOK) for
// this vector, and logs (rather than silently accepting) the ones that are.
func requireAcceptableReadOutcome(t *testing.T, name string, rerr error) {
	t.Helper()
	if rerr == nil {
		return
	}
	if !errors.Is(rerr, mp3.ErrCorruptStream) || !streamingTruncatedTailOK[name] {
		t.Fatalf("ReadAll: %v", rerr)
	}
	t.Logf("streaming decode ended with %v (a stricter-than-the-frame-API truncated-final-frame policy, Task 6); "+
		"still requiring every sample decoded before that point to be bit-exact", rerr)
}

// gaplessTrim narrows all (the untrimmed frame-API ground truth) to
// sd's own [gaplessStart, gaplessEnd) window, in interleaved sample
// positions, so the comparison mirrors exactly the trim sd applied.
func gaplessTrim(all []float32, sd *Decoder) []float32 {
	channels := sd.info.Channels
	lo := int(sd.gaplessStart) * channels
	hi := len(all)
	if sd.gaplessEnd != math.MaxUint64 {
		if ge := int(sd.gaplessEnd) * channels; ge < hi {
			hi = ge
		}
	}
	if lo > hi {
		lo = hi
	}
	return all[lo:hi]
}

// requireBitExact fails the test at the first sample (or length) mismatch
// between got and want, comparing float32 bit patterns rather than values so
// a NaN or signed-zero divergence cannot slip through.
func requireBitExact(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("streaming decode produced %d samples, frame-API ground truth produced %d", len(got), len(want))
	}
	for i, g := range got {
		if math.Float32bits(g) != math.Float32bits(want[i]) {
			t.Fatalf("sample %d = %v (bits %#x), want %v (bits %#x)",
				i, g, math.Float32bits(g), want[i], math.Float32bits(want[i]))
		}
	}
}

// checkPCMReferenceLength requires a tag-bearing vector's emitted
// per-channel sample count to match its ISO .pcm reference exactly, when
// the reference was fetched. Only the length is checked (not sample
// values): the value-level check already ran, bit-exact, against the
// frame-API ground truth above; this additionally anchors that ground
// truth's gapless-trimmed length to the independent ISO reference, the
// same invariant TestGaplessTrim (gapless_test.go) checks for this vector
// through pcm.Decoder alone, now cross-checked at the streaming layer.
// Honors the MP3_REQUIRE_DUMPS convention: skip when the reference is
// absent, fail loudly when the corpus was supposed to be fetched.
func checkPCMReferenceLength(t *testing.T, name string, channels, gotPerChannel int) {
	t.Helper()
	refPath := filepath.Join("..", "testdata", "vectors", name+".pcm")
	fi, err := os.Stat(refPath)
	if err != nil {
		if os.IsNotExist(err) {
			if os.Getenv("MP3_REQUIRE_DUMPS") != "" {
				t.Fatalf("required .pcm reference missing for tag-bearing vector %s: %s", name, refPath)
			}
			t.Skipf(".pcm reference not found for tag-bearing vector %s (run scripts/fetch-vectors.sh first): %s", name, refPath)
		}
		t.Fatalf("stat %s: %v", refPath, err)
	}

	const bytesPerSample = 2 // ISO references are 16-bit PCM
	refBytes := fi.Size()
	if refBytes%(bytesPerSample*int64(channels)) != 0 {
		t.Fatalf("reference %s size %d is not a whole number of %d-channel samples", refPath, refBytes, channels)
	}
	refPerChannel := refBytes / (bytesPerSample * int64(channels))
	if int64(gotPerChannel) != refPerChannel {
		t.Errorf("streaming decode emitted %d samples/channel, want %d (ISO .pcm reference, gapless applied)",
			gotPerChannel, refPerChannel)
	}
}
