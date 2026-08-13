package dec

import (
	"fmt"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
	"github.com/tphakala/go-mp3/internal/enc"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// validateFrames is a white-box test helper (package dec, not dec_test): it
// imports internal/enc, the sanctioned exception to "enc must never import
// dec" for _test.go files only (see PROVENANCE.md and internal/enc/doc.go).
//
// It re-parses a Phase 3 encoder byte stream with the decoder's own header
// and side-info parsers and asserts every emitted frame's structural
// invariants, then feeds the whole stream through the internal Decoder's
// frame loop (the same engine the public mp3.Decoder delegates to; a
// package dec test file cannot import the root mp3 package without an
// import cycle, since mp3 itself imports internal/dec) and requires every
// frame decodes cleanly. nFrames is the expected frame count.
func validateFrames(t *testing.T, stream []byte, wantSampleRate, wantKbps, wantNch, nFrames int) {
	t.Helper()
	validateFrameHeaders(t, stream, wantSampleRate, wantKbps, wantNch, nFrames)
	validateFrameDecode(t, stream, nFrames)
}

// validateFrameHeaders walks stream frame by frame using only the decoder's
// header and side-info parsers (no decode), checking every header field,
// exact frame length, and every granule-channel's side-info invariants.
func validateFrameHeaders(t *testing.T, stream []byte, wantSampleRate, wantKbps, wantNch, nFrames int) {
	t.Helper()

	pos := 0
	frames := 0
	for pos < len(stream) {
		h := stream[pos:]
		if !hdrValid(h) {
			t.Fatalf("frame %d at byte %d: hdrValid = false", frames, pos)
		}
		if got := int(hdrBitrateKbps(h)); got != wantKbps {
			t.Fatalf("frame %d: hdrBitrateKbps = %d, want %d", frames, got, wantKbps)
		}
		if got := int(hdrSampleRateHz(h)); got != wantSampleRate {
			t.Fatalf("frame %d: hdrSampleRateHz = %d, want %d", frames, got, wantSampleRate)
		}

		frameBytes := hdrFrameBytes(h, 0) + hdrPadding(h)
		if pos+frameBytes > len(stream) {
			t.Fatalf("frame %d: frame length %d overruns the remaining %d stream bytes", frames, frameBytes, len(stream)-pos)
		}
		frame := h[:frameBytes]

		rd := bits.NewReader(frame[4:])
		gr := make([]grInfo, 2*wantNch)
		if mdb := l3ReadSideInfo(&rd, gr, frame[:4], len(frame)-4); mdb != 0 {
			t.Fatalf("frame %d: l3ReadSideInfo = %d, want 0 (main_data_begin=0, no malformed signal)", frames, mdb)
		}

		frameMainBits := frameBytes*8 - 32 - sideInfoBitsFor(wantNch)
		validateGranules(t, frames, gr, frameMainBits)

		pos += frameBytes
		frames++
	}
	if pos != len(stream) {
		t.Fatalf("stream not fully consumed by header walk: pos = %d, len(stream) = %d", pos, len(stream))
	}
	if frames != nFrames {
		t.Fatalf("frame count = %d, want %d", frames, nFrames)
	}
}

// validateGranules checks every granule-channel's side-info invariants
// (long blocks only, no scalefactor amplification/reuse fields set, valid
// codebook numbers) and that their combined part2_3 length fits the frame's
// main-data budget.
func validateGranules(t *testing.T, frameIdx int, gr []grInfo, frameMainBits int) {
	t.Helper()

	sumPart23 := 0
	for i := range gr {
		g := &gr[i]
		if g.bigValues > 288 {
			t.Fatalf("frame %d gr %d: bigValues = %d, want <= 288", frameIdx, i, g.bigValues)
		}
		if g.blockType != 0 {
			t.Fatalf("frame %d gr %d: blockType = %d, want 0 (long blocks only)", frameIdx, i, g.blockType)
		}
		if g.mixedBlockFlag != 0 {
			t.Fatalf("frame %d gr %d: mixedBlockFlag = %d, want 0", frameIdx, i, g.mixedBlockFlag)
		}
		if g.scalefacCompress != 0 {
			t.Fatalf("frame %d gr %d: scalefacCompress = %d, want 0", frameIdx, i, g.scalefacCompress)
		}
		if g.preflag != 0 {
			t.Fatalf("frame %d gr %d: preflag = %d, want 0", frameIdx, i, g.preflag)
		}
		if g.scalefacScale != 0 {
			t.Fatalf("frame %d gr %d: scalefacScale = %d, want 0", frameIdx, i, g.scalefacScale)
		}
		for r, ts := range g.tableSelect {
			if ts == 4 || ts == 14 {
				t.Fatalf("frame %d gr %d region %d: tableSelect = %d, invalid codebook number", frameIdx, i, r, ts)
			}
		}
		sumPart23 += int(g.part23Length)
	}
	if sumPart23 > frameMainBits {
		t.Fatalf("frame %d: sum(part23Length) = %d, exceeds mainBits = %d", frameIdx, sumPart23, frameMainBits)
	}
}

// validateFrameDecode feeds stream through the internal decoder, not the
// public mp3.Decoder (see validateFrames' doc comment for the import-cycle
// reason). A plain advance-by-FrameBytes loop needs no sentinel header:
// decode.go's fast path accepts an exact-fit final frame
// (frameSize == mp3Bytes, decode.go:98) and findFrame's resync path accepts
// the same (i == 0 && frameAndPadding == mp3Bytes, header.go:172-173), the
// same behavior TestFullStreamMatchesOracle already relies on.
func validateFrameDecode(t *testing.T, stream []byte, nFrames int) {
	t.Helper()

	d := NewDecoder()
	pcm := make([]float32, maxSamplesPerFrame)
	var fi FrameInfo
	pos := 0
	decoded := 0
	for pos < len(stream) {
		n := d.DecodeFrame(stream[pos:], pcm, &fi)
		if n != 1152 {
			t.Fatalf("decode frame %d at byte %d: n = %d, want 1152", decoded, pos, n)
		}
		if fi.FrameOffset != 0 {
			t.Fatalf("decode frame %d: FrameOffset = %d, want 0", decoded, fi.FrameOffset)
		}
		if fi.Layer != 3 {
			t.Fatalf("decode frame %d: Layer = %d, want 3", decoded, fi.Layer)
		}
		if fi.FrameBytes <= 0 {
			t.Fatalf("decode frame %d: FrameBytes = %d, want > 0", decoded, fi.FrameBytes)
		}
		pos += fi.FrameBytes
		decoded++
	}
	if pos != len(stream) {
		t.Fatalf("stream not fully consumed by decode loop: pos = %d, len(stream) = %d", pos, len(stream))
	}
	if decoded != nFrames {
		t.Fatalf("decoded frame count = %d, want %d", decoded, nFrames)
	}
}

// sideInfoBitsFor returns the exact packed side-info size in bits for nch
// channels: 136 mono, 256 stereo. Independently derived here (not calling
// into internal/enc) from the same field widths l3ReadSideInfo reads
// (internal/dec/sideinfo.go:69): main_data_begin(9) + private_bits(5 mono/3
// stereo) + scfsi(4/channel) + 2 granules * nch channels * 59
// bits/granule-channel.
func sideInfoBitsFor(nch int) int {
	privateBits := 5
	if nch == 2 {
		privateBits = 3
	}
	return 9 + privateBits + nch*4 + 2*nch*59
}

// grid amplitudes for the LCG-driven synthetic spectra: silence, and three
// scaled levels reaching well up toward enc.maxQuant, so the rate loop
// exercises easy, moderate, and heavily-compressed granules across a
// 20-frame run.
var structuralGridAmplitudes = [4]float64{0, 50, 2000, 8000}

// runStructuralGrid builds nFrames synthetic frames at (srIndex,
// bitrateIndex, mode, nch), amplitude-cycling through
// structuralGridAmplitudes, encodes
// each through enc.AppendFramePin (the production codeGranule + appendFrame
// pair), and validates the resulting stream with validateFrames.
func runStructuralGrid(t *testing.T, srIndex, bitrateIndex, wantKbps, mode, nch, nFrames int) {
	t.Helper()
	seed := uint64(srIndex)<<32 | uint64(bitrateIndex)<<8 | uint64(mode)

	var stream []byte
	for f := range nFrames {
		amp := structuralGridAmplitudes[f%len(structuralGridAmplitudes)]
		var xr [2][2][576]float64
		for g := range 2 {
			for ch := range nch {
				for i := range 576 {
					v := testsignal.LCG(&seed) * amp
					if testsignal.LCG(&seed) < 0.5 {
						v = -v
					}
					xr[g][ch][i] = v
				}
			}
		}
		stream = enc.AppendFramePin(stream, bitrateIndex, srIndex, 0, mode, &xr, nch)
	}

	validateFrames(t, stream, int(hdrHz[srIndex]), wantKbps, nch, nFrames)
}

// kbpsToIndex maps every MPEG-1 Layer III CBR bitrate to its side-info
// bitrate_index, ISO/IEC 11172-3 Table B.1 (hand-specified: the standard
// MPEG-1 Layer III bitrate list).
var kbpsToIndex = map[int]int{
	32: 1, 40: 2, 48: 3, 56: 4, 64: 5, 80: 6, 96: 7,
	112: 8, 128: 9, 160: 10, 192: 11, 224: 12, 256: 13, 320: 14,
}

// gridMode pairs a side-info channel-mode index (mode) with its channel
// count (nch): the (stereo, mono) combinations TestEncFrameStructuralGrid
// and TestEncoderStructuralGrid both grid over.
type gridMode struct {
	mode int
	nch  int
}

// gridSampleRates, gridBitratesKbps and gridModes are the shared coverage
// TestEncFrameStructuralGrid and TestEncoderStructuralGrid both sweep via
// forEachGridCase: every MPEG-1 sample rate x bitrate in {32,128,320} x
// (stereo, mono).
var gridSampleRates = [3]int{44100, 48000, 32000}

var gridBitratesKbps = []int{32, 128, 320}

var gridModes = []gridMode{
	{0, 2}, // stereo
	{3, 1}, // single_channel
}

// forEachGridCase drives the sample-rate x bitrate x mode grid shared by
// TestEncFrameStructuralGrid and TestEncoderStructuralGrid, invoking run as
// its own subtest for every case, named "sr<Hz>_kbps<kbps>_nch<nch>" (same
// naming and iteration order both tests used before this helper existed).
// sr is the MPEG-1 sample-rate index (0=44100, 1=48000, 2=32000, matching
// srIndex elsewhere in this package); m is the (mode, nch) pair.
func forEachGridCase(t *testing.T, run func(t *testing.T, sr, kbps int, m gridMode)) {
	t.Helper()
	for sr := range 3 {
		for _, kbps := range gridBitratesKbps {
			for _, m := range gridModes {
				t.Run(fmt.Sprintf("sr%d_kbps%d_nch%d", gridSampleRates[sr], kbps, m.nch), func(t *testing.T) {
					t.Parallel()
					run(t, sr, kbps, m)
				})
			}
		}
	}
}

// TestEncFrameStructuralGrid is the no-oracle centerpiece for Task 6: every
// (sample rate) x (bitrate in {32,128,320}) x (mono,stereo) combination,
// 20 frames each, run through the production codeGranule/appendFrame pair
// and validated structurally and by real decode. 32kHz/320kbps/mono
// (budget 5676, effBudget 4095) is included, as required, in this grid's
// bitrate-320 x mono coverage.
//
// Note on that specific coverage: a missing maxPart23Length cap would mask
// part_2_3_length on write (WriteBits truncates silently to the low 12
// bits) but, because main_data_begin is always 0 here, the resulting
// under-declared granule length never pushes the decoder's per-frame
// bits.Reader past its limit (that reader spans the whole frame's main-data
// area, sized to the frame's true, uncorrupted mainBits budget, and a
// 12-bit-masked declared length is by construction always < 4096, far
// inside it) - so this grid's decode leg does not, on its own, turn a
// missing cap into an observable n != 1152 or Overrun failure. Verified by
// temporarily removing the cap: TestCodeGranuleBudgetCap (frame_test.go)
// fails immediately, while this grid still passes. TestCodeGranuleBudgetCap
// is therefore the load-bearing regression guard for the cap itself; this
// grid's value for that requirement is the literal coverage the addendum
// asks for (32kHz/320kbps/mono exercised end to end) plus its broader job
// of decoder-proving region boundaries and table selects (see this file's
// validateGranules and the addendum's CF3 note) across real, varied
// content. A full 14-bitrate sweep at 44.1 kHz stereo is included
// separately.
func TestEncFrameStructuralGrid(t *testing.T) {
	forEachGridCase(t, func(t *testing.T, sr, kbps int, m gridMode) {
		t.Helper()
		runStructuralGrid(t, sr, kbpsToIndex[kbps], kbps, m.mode, m.nch, 20)
	})

	for kbps, idx := range kbpsToIndex {
		t.Run(fmt.Sprintf("sweep_44100_stereo_kbps%d", kbps), func(t *testing.T) {
			t.Parallel()
			runStructuralGrid(t, 0, idx, kbps, 0, 2, 20)
		})
	}
}

// TestEncHeaderSampleRateRowMapping closes the gap TestEncSfbWidthsMatchDec
// leaves open: that test hard-codes decoder rows {5,6,7} per encoder index,
// so a 44.1<->32kHz label swap consistently applied to BOTH
// enc.sfbWidthsLong's row order AND the encoder's header sampling_frequency
// packing would still pass it. This test instead builds a real header via
// enc.AppendFramePin, decodes the sample rate the decoder's own hdr_*
// accessors extract from it, and confirms the sfb-width row
// l3ReadSideInfo actually selects at runtime for that header (via
// hdrGetMySampleRate -> scfLongTable) equals enc.SfbWidthsLongRow at the
// SAME decoded rate (not the srIndex used to build the header): header
// bytes -> hdrGetMySampleRate -> scfLongTable row selection, so a label
// swap on either side breaks either the sample-rate check or the sfbTab
// check.
func TestEncHeaderSampleRateRowMapping(t *testing.T) {
	wantRate := [3]int{44100, 48000, 32000}
	indexOf := map[int]int{44100: 0, 48000: 1, 32000: 2}

	for srIndex := range 3 {
		var xr [2][2][576]float64 // zero spectra
		frame := enc.AppendFramePin(nil, 9, srIndex, 0, 3, &xr, 1)
		h := frame[:4]

		if !hdrValid(h) {
			t.Fatalf("srIndex %d: hdrValid = false", srIndex)
		}
		if got := int(hdrSampleRateHz(h)); got != wantRate[srIndex] {
			t.Fatalf("srIndex %d: hdrSampleRateHz = %d, want %d", srIndex, got, wantRate[srIndex])
		}

		rd := bits.NewReader(frame[4:])
		gr := make([]grInfo, 2)
		if got := l3ReadSideInfo(&rd, gr, h, len(frame)-4); got != 0 {
			t.Fatalf("srIndex %d: l3ReadSideInfo = %d, want 0", srIndex, got)
		}

		encRow := enc.SfbWidthsLongRow(indexOf[wantRate[srIndex]])
		for i := range 22 {
			if gr[0].sfbTab[i] != uint8(encRow[i]) {
				t.Fatalf("srIndex %d: gr[0].sfbTab[%d] = %d, want %d (enc.SfbWidthsLongRow at the decoded rate)",
					srIndex, i, gr[0].sfbTab[i], encRow[i])
			}
		}
	}
}

// encoderStructuralGridAmplitudes are the LCG-cycled amplitudes
// runEncoderStructuralGrid drives the real Encoder with, scaled into
// [-1,1] (the Encoder's documented input domain): silence, quiet, loud,
// and near-full-scale, so the rate loop exercises easy, moderate, and
// heavily-compressed granules exactly as structuralGridAmplitudes does for
// the synthetic-spectrum grid above.
var encoderStructuralGridAmplitudes = [4]float64{0, 0.02, 0.4, 0.95}

// runEncoderStructuralGrid drives the production enc.Encoder (Task 7)
// through nFrames real PCM frames plus one drain frame and validates the
// resulting stream with validateFrames, the same validator
// TestEncFrameStructuralGrid runs over the synthetic-spectrum
// AppendFramePin path above. This is the addendum's CF3 requirement
// (section d): the structural invariants must hold for the real end-to-end
// PCM-in/MP3-out pipeline, not only for hand-built spectra.
func runEncoderStructuralGrid(t *testing.T, sampleRate, kbps, nch, nFrames int) {
	t.Helper()

	e, err := enc.New(enc.Config{SampleRate: sampleRate, Channels: nch, BitrateKbps: kbps})
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}

	seed := uint64(sampleRate)<<32 | uint64(kbps)<<8 | uint64(nch)

	var stream []byte
	for f := range nFrames {
		amp := encoderStructuralGridAmplitudes[f%len(encoderStructuralGridAmplitudes)]
		samples := make([][]float32, nch)
		for ch := range nch {
			samples[ch] = make([]float32, 1152)
			for i := range 1152 {
				v := testsignal.LCG(&seed)*2 - 1
				samples[ch][i] = float32(v * amp)
			}
		}
		stream, err = e.EncodeFrame(stream, samples)
		if err != nil {
			t.Fatalf("frame %d: EncodeFrame: %v", f, err)
		}
	}
	stream, err = e.EncodeFrame(stream, nil) // drain: one extra frame
	if err != nil {
		t.Fatalf("drain: EncodeFrame: %v", err)
	}

	validateFrames(t, stream, sampleRate, kbps, nch, nFrames+1)
}

// TestEncoderStructuralGrid reruns TestEncFrameStructuralGrid's grid (every
// sample rate x bitrate in {32,128,320} x mono/stereo) through the real
// Task 7 Encoder instead of the synthetic AppendFramePin spectra, closing
// the addendum's CF3 gap: the structural validator must also see real
// PCM-in/MP3-out output, not just hand-built granule spectra.
func TestEncoderStructuralGrid(t *testing.T) {
	forEachGridCase(t, func(t *testing.T, sr, kbps int, m gridMode) {
		t.Helper()
		runEncoderStructuralGrid(t, gridSampleRates[sr], kbps, m.nch, 20)
	})
}
