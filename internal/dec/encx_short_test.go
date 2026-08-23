package dec

import (
	"fmt"
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
	"github.com/tphakala/go-mp3/internal/enc"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// Block-type values as ISO/IEC 11172-3 2.4.1.7 encodes them in side info's
// block_type field, matching enc's unexported blockLong/blockStart/
// blockShort/blockStop (internal/enc/blocktypes.go) and this package's own
// shortBlockType (types.go): 0 long, 1 start, 2 short, 3 stop. Named here
// (rather than imported) because they are enc-package-internal constants
// this white-box test cannot reach directly.
const (
	blkLong  = 0
	blkStart = 1
	blkShort = 2
	blkStop  = 3
)

// shortPattern names one (granule 0, granule 1) block_type pair this
// task's gates drive enc.AppendFrameShortPin with.
type shortPattern struct {
	name     string
	id       int // distinct per pattern, seeds the LCG (unlike len(name), which collides: "short_short" and "start_short" are both 11 bytes)
	gr0, gr1 int
}

// shortPatterns lists every block-type pattern the brief requires: the
// legal decision-10 transitions (docs/superpowers/plans/
// 2026-08-23-go-mp3-phase4-inc7-short-blocks.md) this task's
// window-switching plumbing must support end to end, even though PR B's
// real attack-driven state machine (which actually picks these sequences
// from PCM content) is not wired into codeFrame yet.
var shortPatterns = []shortPattern{
	{"short_short", 0, blkShort, blkShort},
	{"start_short", 1, blkStart, blkShort},
	{"short_stop", 2, blkShort, blkStop},
	{"long_start", 3, blkLong, blkStart},
}

// shortRates pairs an enc srIndex (0=44100, 1=48000, 2=32000) with the
// sample rate the decoded header must report.
var shortRates = [3]int{44100, 48000, 32000}

// buildShortXr fills a 576-line spectrum with a bounded, bit-portable LCG
// signal (dyadic scale: amp is a power of two, and testsignal.LCG's own
// construction is dyadic-exact) directly in coding order: this is
// synthetic test content, not derived from a real MDCT, so there is no
// separate "MDCT order" to reorder from (see AppendFrameShortPin's doc
// comment on what "coding order" means for a real caller feeding it a
// short granule). Tapered toward the tail (frac, the internal/enc
// fullScaleSpectrum precedent) rather than full-scale at every line: a
// granule this loud across the WHOLE spectrum needs far more Huffman
// escape bits than even the highest CBR bitrate's per-granule budget can
// hold, which would trip codeGranule's zeroTopSfb truncation fallback
// (existing, already-tested behavior, but not what this task's readback
// gate wants to exercise) and zero out high bands regardless of any
// window-switching logic.
func buildShortXr(seed *uint64, amp float64) [576]float64 {
	var xr [576]float64
	for i := range xr {
		frac := float64(576-i) / 576
		v := testsignal.LCG(seed) * amp * frac
		if testsignal.LCG(seed) < 0.5 {
			v = -v
		}
		xr[i] = v
	}
	return xr
}

// looseShortXmin is generous enough (far above any noise a real granule
// could produce) that outerLoop's very first pass satisfies every band
// (over == 0 immediately): the granule keeps global_gain's own choice
// from minGlobalGain and an all-zero scalefactor state (scf,
// subblock_gain, scalefac_scale, preflag all 0). This makes the
// encoder's per-band amplification intent (bandExtraQuarters) exactly 0
// for every band of every granule-channel, a deterministic baseline the
// readback gates below check against without needing to introspect the
// encoder's internal granuleCoding (AppendFrameShortPin returns only
// bytes).
func looseShortXmin() (x [2][2][39]float64) {
	for g := range 2 {
		for ch := range 2 {
			for b := range 39 {
				x[g][ch][b] = 1e18
			}
		}
	}
	return x
}

// buildShortFrame drives enc.AppendFrameShortPin for one (rate, nch,
// pattern) case with a fresh LCG spectrum and looseShortXmin, sharing the
// exact construction TestEncShortSideInfoReadback and
// TestEncShortSpectralReadback both need.
func buildShortFrame(rate, nch int, p shortPattern, ampSeed uint64) (frame []byte, xr [2][2][576]float64) {
	var bt [2][2]int
	for ch := range nch {
		bt[0][ch] = p.gr0
		bt[1][ch] = p.gr1
	}
	seed := ampSeed
	for g := range 2 {
		for ch := range nch {
			xr[g][ch] = buildShortXr(&seed, 4096)
		}
	}
	xmin := looseShortXmin()
	frame = enc.AppendFrameShortPin(nil, 9, rate, nch, &bt, &xr, &xmin)
	return frame, xr
}

// forEachShortCase drives the rate x {mono, stereo} x block-type-pattern
// grid the brief requires, invoking run as its own subtest.
func forEachShortCase(t *testing.T, run func(t *testing.T, rate, nch int, p shortPattern)) {
	t.Helper()
	for rate, hz := range shortRates {
		for _, nch := range []int{1, 2} {
			for _, p := range shortPatterns {
				t.Run(fmt.Sprintf("sr%d_nch%d_%s", hz, nch, p.name), func(t *testing.T) {
					run(t, rate, nch, p)
				})
			}
		}
	}
}

// TestEncShortSideInfoReadback is the structural half of Task A4's
// readback gate: for every rate x {mono, stereo} x block-type pattern, it
// parses a window-switching frame with l3ReadSideInfo alone (no
// scalefactor or Huffman decode) and checks block_type, mixed_block_flag,
// subblock_gain, and the derived nLongSfb/nShortSfb/regionCount[0] all
// match what the encoder intended: long/start/stop keep the full
// long-window geometry (22/0 sfb's, region0 = the first 8 long sfb's,
// regionCount[0]=7, design decision 5); short switches to the short
// geometry (0/39, region0 = the first 9 coding bands, regionCount[0]=8).
// checkShortSideInfoGranule checks one granule-channel's parsed side info
// against the block_type want the encoder was driven with. Split out of
// TestEncShortSideInfoReadback to keep that test's cognitive complexity
// down.
func checkShortSideInfoGranule(t *testing.T, gi, ch int, g *grInfo, want int) {
	t.Helper()
	if int(g.blockType) != want {
		t.Fatalf("gr %d ch %d: blockType = %d, want %d", gi, ch, g.blockType, want)
	}
	if g.mixedBlockFlag != 0 {
		t.Fatalf("gr %d ch %d: mixedBlockFlag = %d, want 0", gi, ch, g.mixedBlockFlag)
	}
	switch want {
	case blkLong, blkStart, blkStop:
		if g.nLongSfb != 22 || g.nShortSfb != 0 {
			t.Fatalf("gr %d ch %d: nLongSfb=%d nShortSfb=%d, want 22/0 (long geometry)", gi, ch, g.nLongSfb, g.nShortSfb)
		}
		if want != blkLong && g.regionCount[0] != 7 {
			t.Fatalf("gr %d ch %d: regionCount[0] = %d, want 7 (first 8 long sfb's, decision 5)", gi, ch, g.regionCount[0])
		}
	case blkShort:
		if g.nLongSfb != 0 || g.nShortSfb != 39 {
			t.Fatalf("gr %d ch %d: nLongSfb=%d nShortSfb=%d, want 0/39 (short geometry)", gi, ch, g.nLongSfb, g.nShortSfb)
		}
		if g.regionCount[0] != 8 {
			t.Fatalf("gr %d ch %d: regionCount[0] = %d, want 8 (first 9 coding bands, decision 5)", gi, ch, g.regionCount[0])
		}
	}
	if want != blkLong {
		for w, sg := range g.subblockGain {
			if sg > 7 {
				t.Fatalf("gr %d ch %d: subblockGain[%d] = %d, want 0..7", gi, ch, w, sg)
			}
			if sg != 0 {
				t.Fatalf("gr %d ch %d: subblockGain[%d] = %d, want 0 (looseShortXmin never escalates)", gi, ch, w, sg)
			}
		}
	}
	for r, ts := range g.tableSelect {
		if ts == 4 || ts == 14 {
			t.Fatalf("gr %d ch %d region %d: tableSelect = %d, invalid codebook number", gi, ch, r, ts)
		}
	}
	if g.scfsi != 0 {
		t.Fatalf("gr %d ch %d: scfsi = %04b, want 0 (AppendFrameShortPin never sets scfsi)", gi, ch, g.scfsi)
	}
}

func TestEncShortSideInfoReadback(t *testing.T) {
	forEachShortCase(t, func(t *testing.T, rate, nch int, p shortPattern) {
		t.Helper()
		seed := uint64(rate)<<40 | uint64(nch)<<24 | uint64(p.id)<<8 | 1
		frame, _ := buildShortFrame(rate, nch, p, seed)

		hdr := frame[:4]
		if !hdrValid(hdr) {
			t.Fatalf("hdrValid = false")
		}
		rd := bits.NewReader(frame[4:])
		gr := make([]grInfo, 2*nch)
		if mdb := l3ReadSideInfo(&rd, gr, hdr, len(frame)-4); mdb != 0 {
			t.Fatalf("l3ReadSideInfo = %d, want 0 (main_data_begin=0)", mdb)
		}

		want := [2]int{p.gr0, p.gr1}
		for gi := range 2 {
			for ch := range nch {
				checkShortSideInfoGranule(t, gi, ch, &gr[gi*nch+ch], want[gi])
			}
		}
	})
}

// TestEncShortFullDecodeReadback is the robustness half of Task A4's
// readback gate: the production decoder (the same DecodeFrame the public
// mp3.Decoder delegates to) must accept a window-switching stream cleanly
// for every rate x {mono, stereo} x block-type pattern, with no error and
// a full 1152-sample granule pair.
func TestEncShortFullDecodeReadback(t *testing.T) {
	forEachShortCase(t, func(t *testing.T, rate, nch int, p shortPattern) {
		t.Helper()
		seed := uint64(rate)<<40 | uint64(nch)<<24 | uint64(p.id)<<8 | 2
		frame, _ := buildShortFrame(rate, nch, p, seed)

		d := NewDecoder()
		pcm := make([]float32, maxSamplesPerFrame)
		var fi FrameInfo
		n := d.DecodeFrame(frame, pcm, &fi)
		if n != 1152 {
			t.Fatalf("DecodeFrame: n = %d, want 1152", n)
		}
		if fi.Layer != 3 {
			t.Fatalf("DecodeFrame: Layer = %d, want 3", fi.Layer)
		}
	})
}

// TestEncShortValidatorGrammar exercises Task A4's validateGranules
// extension (encx_frame_test.go) directly against a real window-switching
// stream: every single-frame case in the grid must pass the FULL
// structural validator, including the new block_type/mixed_block_flag/
// subblock_gain checks, the scfsi-disabled-for-any-non-long-granule check
// (design decision 6), and the decision-10 per-channel transition
// grammar (each single frame's granule 0 -> granule 1 step, since
// AppendFrameShortPin always builds one self-contained frame with no
// predecessor). This is what keeps validateFrames/validateGranules from
// being dead code for the window-switching branch: the pre-existing
// structural grids (runStructuralGrid, runEncoderStructuralGrid) never
// emit a non-zero block_type.
func TestEncShortValidatorGrammar(t *testing.T) {
	const wantKbps = 128 // bitrateIndex 9, matches buildShortFrame
	forEachShortCase(t, func(t *testing.T, rate, nch int, p shortPattern) {
		t.Helper()
		seed := uint64(rate)<<40 | uint64(nch)<<24 | uint64(p.id)<<8 | 4
		frame, _ := buildShortFrame(rate, nch, p, seed)
		validateFrames(t, frame, shortRates[rate], wantKbps, nch, 1, false)
	})
}

// TestLegalBlockTransition exhaustively checks legalBlockTransition
// (encx_frame_test.go, design decision 10) against every (from, to) pair
// in {long, start, short, stop}^2, proving the grammar table
// TestEncShortValidatorGrammar's positive cases exercise is not
// vacuously permissive: every one of the 4 legal steps
// (0->{0,1}, 1->{2}, 2->{2,3}, 3->{0,1}) is accepted and every one of the
// other 12 is rejected, start->stop (1->3) included (legal under TDAC but
// never emitted under this policy, decision 10's own doc comment).
func TestLegalBlockTransition(t *testing.T) {
	legal := map[[2]int]bool{
		{blkLong, blkLong}: true, {blkLong, blkStart}: true,
		{blkStart, blkShort}: true,
		{blkShort, blkShort}: true, {blkShort, blkStop}: true,
		{blkStop, blkLong}: true, {blkStop, blkStart}: true,
	}
	for from := blkLong; from <= blkStop; from++ {
		for to := blkLong; to <= blkStop; to++ {
			want := legal[[2]int{from, to}]
			if got := legalBlockTransition(from, to); got != want {
				t.Errorf("legalBlockTransition(%d, %d) = %v, want %v", from, to, got, want)
			}
		}
	}
}

// maxQuantPin mirrors enc's maxQuant (internal/enc/quantize.go): the
// largest magnitude any Huffman table encodes (15 + 2^13 - 1, the
// linbits-13 escape family's capacity). Reproduced here (a documented
// ISO/IEC 11172-3 Table B.7 constant, not proprietary decoder logic)
// because checkSpectralRoundTrip needs to replicate quantizeGranule's
// exact clamp to independently predict what index the encoder must have
// produced.
const maxQuantPin = 8206

// quantizeIxPin reproduces quantizeGranule's per-line forward mapping
// (internal/enc/quantize.go): ix = trunc(sqrt(t*sqrt(t)) + 0.4054),
// clamped to maxQuantPin, where t = |xr|*is and is is the SAME per-line
// multiplier the encoder used for this line. This is the documented ISO
// annex C.1.5.4 power-law quantizer (round-half-up on x^0.75 - 0.0946,
// per quantizeGranule's own doc comment); reproducing it here is
// independent verification, not a copy of encoder internals into a
// production path (this is _test.go code in a different package).
func quantizeIxPin(absXr, is float64) int32 {
	t := absXr * is
	v := math.Sqrt(t * math.Sqrt(t))
	if nint := v + 0.4054; nint <= maxQuantPin {
		return int32(nint)
	}
	return maxQuantPin
}

// checkSpectralRoundTrip compares one granule-channel's decoded spectrum
// (dst, from l3Huffman dequantizing against l3ReadScalefactors' floats)
// against the encoder's own xr, IN THE QUANTIZED-INDEX DOMAIN rather than
// the reconstructed-magnitude domain: the ISO power-law quantizer is
// deliberately compressive (fine steps at low magnitude, coarse steps at
// high magnitude in absolute terms, but the RELATIVE magnitude error for
// a one-index rounding difference varies hugely across the range, from
// ~130% at ix=1 down to ~0.02% near maxQuantPin), so a fixed magnitude
// tolerance is either far too loose at the top of the range or a
// guaranteed false failure at the bottom. Comparing indices sidesteps
// that: since looseShortXmin pins every granule-channel to an all-zero
// scalefactor state (bandExtraQuarters == 0 for every band), the decoded
// scf[] is UNIFORM across every band of a granule-channel (same
// l3LdexpQ2(gain, 0) call every time), so is = 1/scf[0] is the single
// per-line multiplier quantizeGranule used for the WHOLE granule, read
// back from the decoder's own output rather than re-derived from
// globalGain and the quantGainBase/l3LdexpQ2 fixed-point convention
// (avoiding a second, independent and bug-prone reimplementation of that
// machinery). Requiring the recovered index to be within 1 of the
// independently-computed expected index (allowing for the two
// half-up/truncation roundings landing on either side of an integer
// boundary) is a real, band-geometry-sensitive gate: a wrong sfbTab row or
// a misplaced window-switching region boundary would misalign which table
// decodes which line and produce indices far outside this tolerance, not
// just a marginal magnitude drift. It does NOT exercise subblock_gain:
// with looseShortXmin every granule-channel's subblock_gain is provably 0
// (its own doc comment), so a subblock_gain sign/shift/window-index bug in
// l3ReadScalefactors would go undetected here; TestEncShortSubblockGain-
// Readback below is the gate for that mechanism.
//
// Both xr and dst are in CODING order (l3Huffman decodes against
// gr.sfbTab, which for a window-switching granule is scfShortTable/
// scfLongTable in the same coding-band order enc's bandLayout uses,
// already cross-checked by TestEncSfbWidthsMatchDec/
// TestEncSfbWidthsShortMatchDec), so no reordering is needed for this
// direct, per-line comparison.
func checkSpectralRoundTrip(t *testing.T, gi, ch int, xr *[576]float64, dst []float32, scf0 float32) {
	t.Helper()
	is := 1.0 / float64(scf0)
	for i := range 576 {
		want := xr[i]
		wantIx := quantizeIxPin(math.Abs(want), is)
		if want < 0 {
			wantIx = -wantIx
		}

		var gotIx int32
		if dst[i] != 0 {
			mag := math.Round(math.Pow(math.Abs(float64(dst[i]))/float64(scf0), 0.75))
			gotIx = int32(mag)
			if dst[i] < 0 {
				gotIx = -gotIx
			}
		}

		if diff := gotIx - wantIx; diff < -1 || diff > 1 {
			t.Fatalf("gr %d ch %d line %d: recovered ix %d, want %d (+-1) (xr=%v dst=%v scf0=%v)",
				gi, ch, i, gotIx, wantIx, want, dst[i], scf0)
		}
	}
}

// TestEncShortSpectralReadback is the scalefactor/dequant half of Task
// A4's readback gate: for every rate x {mono, stereo} x block-type
// pattern, it decodes side info, scalefactors, and the Huffman spectrum
// directly (bypassing MDCT/synthesis) and requires the dequantized
// spectrum to match the encoder's own xr within the quantizer's noise
// bound. Because looseShortXmin pins every granule-channel to an
// all-zero scalefactor state (bandExtraQuarters == 0 for every band,
// looseShortXmin's own doc comment), a faithful round trip here directly
// proves the decoder's per-band effective dequant step agrees with the
// encoder's intent band-for-band: a wrong sfbTab row or a misplaced
// region boundary would show up as a localized reconstruction failure
// rather than a uniform, tolerable quantization residual. It does NOT
// cover the short-only subblock_gain term in that iscf arithmetic:
// subblock_gain is provably 0 in every case here, so a sign/shift/
// window-index bug in that term would produce byte-identical output and
// go undetected; TestEncShortSubblockGainReadback below forces a known
// nonzero subblock_gain to gate that mechanism instead.
func TestEncShortSpectralReadback(t *testing.T) {
	forEachShortCase(t, func(t *testing.T, rate, nch int, p shortPattern) {
		t.Helper()
		seed := uint64(rate)<<40 | uint64(nch)<<24 | uint64(p.id)<<8 | 3
		frame, xr := buildShortFrame(rate, nch, p, seed)

		hdr := frame[:4]
		rd := bits.NewReader(frame[4:])
		gr := make([]grInfo, 2*nch)
		if mdb := l3ReadSideInfo(&rd, gr, hdr, len(frame)-4); mdb != 0 {
			t.Fatalf("l3ReadSideInfo = %d, want 0", mdb)
		}

		mainData := frame[4+sideInfoBitsFor(nch)/8:]
		main := bits.NewReader(mainData)
		var istPos [2][40]uint8
		var scf [40]float32
		pos := 0
		for gi := range 2 {
			for ch := range nch {
				g := &gr[gi*nch+ch]
				layer3gr := pos + int(g.part23Length)

				main.SetPos(pos)
				l3ReadScalefactors(hdr, scf[:], istPos[ch][:], g, &main, ch)
				if scf[0] == 0 {
					t.Fatalf("gr %d ch %d: scf[0] = 0, want > 0 (looseShortXmin: bandExtraQuarters is 0 for every band, so scf must be a nonzero, band-uniform gain)", gi, ch)
				}

				var dst [576]float32
				l3Huffman(dst[:], &main, g, scf[:], layer3gr)
				if main.Overrun() {
					t.Fatalf("gr %d ch %d: bits.Reader overran main data", gi, ch)
				}

				checkSpectralRoundTrip(t, gi, ch, &xr[gi][ch], dst[:], scf[0])
				pos += int(g.part23Length)
			}
		}
	})
}

// forcedSubblockGain is the per-window subblock_gain
// TestEncShortSubblockGainReadback forces into every short granule-channel
// it builds: [1,2,3] straight from the fix brief's own worked example,
// nonzero on every window so a bug that misroutes a window's contribution
// to the wrong band cannot hide behind a window that would decode to 0
// regardless.
var forcedSubblockGain = [3]int{1, 2, 3}

// buildShortFrameSG is buildShortFrame's sibling for the nonzero
// subblock_gain readback gate below: it drives enc.AppendFrameShortPinSG
// instead of enc.AppendFrameShortPin, forcing every granule-channel's
// subblock_gain to forcedSubblockGain regardless of block type. A
// non-short granule ignores the value entirely (both bandExtraQuarters and
// l3ReadScalefactors gate the term on the granule actually being short:
// lay.short / gr.nShortSfb != 0), so forcing it uniformly is harmless
// there and keeps one helper shape for every pattern in shortPatterns.
func buildShortFrameSG(rate, nch int, p shortPattern, ampSeed uint64) (frame []byte, xr [2][2][576]float64) {
	var bt [2][2]int
	for ch := range nch {
		bt[0][ch] = p.gr0
		bt[1][ch] = p.gr1
	}
	seed := ampSeed
	for g := range 2 {
		for ch := range nch {
			xr[g][ch] = buildShortXr(&seed, 4096)
		}
	}
	var sg [2][2][3]int
	for g := range 2 {
		for ch := range nch {
			sg[g][ch] = forcedSubblockGain
		}
	}
	frame = enc.AppendFrameShortPinSG(nil, 9, rate, nch, &bt, &xr, &sg)
	return frame, xr
}

// ldexpQ2Pin reproduces l3LdexpQ2's exact mathematical behavior (scalefac-
// tors.go: y * 2^(-expQ2/4), computed there via an iterative shift-based
// construction to stay in int32 range) directly via math.Exp2, for the
// non-negative expQ2 domain every call below uses. This is the
// checkSpectralRoundTrip/quantizeIxPin precedent (an independent
// reimplementation of a decoder formula, not a call into the code under
// test) applied to the scalefactor/dequant side instead of the quantizer
// side.
func ldexpQ2Pin(y float64, expQ2 int) float64 {
	return y * math.Exp2(-float64(expQ2)/4)
}

// expectScfForSubblockGain independently predicts one short granule-
// channel's scf[i] for coding band i from the decoded global_gain alone,
// reproducing l3ReadScalefactors' own gain/scfShift arithmetic
// (scalefactors.go) but computing the subblock_gain contribution to iscf
// INDEPENDENTLY of that function's own `iscf[base+i] += gr.subblockGain[i]
// << sh` line: bandExtraQuarters (internal/enc/quantize.go) documents
// subblock_gain as contributing exactly 8 quarter-power-of-two steps per
// unit, so this reproduces that fact directly instead of reading it back
// off the line under test. A pure short granule-channel (nLongSfb == 0)
// packs its 39 bands as 13 groups of 3 windows, so band i belongs to
// window i%3 (l3ReadScalefactors' own base+0/base+1/base+2 grouping with
// nLongSfb == 0). scalefacScale, preflag, and the granule's own scf[] are
// all 0 here (buildShortFrameSG/AppendFrameShortPinSG never set them), so
// iscf's only nonzero contribution for a pure short granule-channel is the
// subblock_gain term itself: msAdj is also always 0 for these frames (mode
// is always single_channel or plain stereo, never joint stereo, so
// hdrIsMsStereo is always false).
func expectScfForSubblockGain(gg int, sg [3]int, i int) float32 {
	gainExp := gg + bitsDequantizerOut*4 - 210
	gain := ldexpQ2Pin(float64(1<<(maxScfi/4)), maxScfi-gainExp)
	extraQuarters := 8 * sg[i%3]
	return float32(ldexpQ2Pin(gain, extraQuarters))
}

// TestEncShortSubblockGainReadback is Task A4's fix-round-1 gate for the
// decoder's short-only subblock_gain dequant term (iscf[base+i] +=
// gr.subblockGain[i] << sh, internal/dec/scalefactors.go): TestEncShort-
// SpectralReadback's looseShortXmin fixture never drives outerLoop's
// escalateSubblockGain, so subblock_gain was 0 in every one of that test's
// 24 grid cases and this arithmetic went completely uncovered (a sign
// flip, a wrong shift amount, or a misrouted window index would have
// produced byte-identical output in every case). Rather than trying to
// make outerLoop's real escalation fire (a separate, already-flagged
// design concern: escalateSubblockGain is largely a no-op as integrated
// there; see enc.AppendFrameShortPinSG's doc comment), this bypasses the
// escalation POLICY entirely and forces forcedSubblockGain=[1,2,3]
// directly through that test-only pin, then decodes side info and
// scalefactors and checks the decoded scf[] against
// expectScfForSubblockGain's independent prediction for every band of
// every short granule-channel in the grid.
func TestEncShortSubblockGainReadback(t *testing.T) {
	forEachShortCase(t, func(t *testing.T, rate, nch int, p shortPattern) {
		t.Helper()
		want := [2]int{p.gr0, p.gr1}
		if want[0] != blkShort && want[1] != blkShort {
			t.Skip("no short granule in this pattern: subblock_gain has no functional effect on start/stop/long geometry")
		}

		seed := uint64(rate)<<40 | uint64(nch)<<24 | uint64(p.id)<<8 | 5
		frame, _ := buildShortFrameSG(rate, nch, p, seed)

		hdr := frame[:4]
		rd := bits.NewReader(frame[4:])
		gr := make([]grInfo, 2*nch)
		if mdb := l3ReadSideInfo(&rd, gr, hdr, len(frame)-4); mdb != 0 {
			t.Fatalf("l3ReadSideInfo = %d, want 0", mdb)
		}

		mainData := frame[4+sideInfoBitsFor(nch)/8:]
		main := bits.NewReader(mainData)
		var istPos [2][40]uint8
		var scf [40]float32
		pos := 0
		for gi := range 2 {
			for ch := range nch {
				g := &gr[gi*nch+ch]
				if want[gi] != blkShort {
					pos += int(g.part23Length)
					continue
				}

				main.SetPos(pos)
				l3ReadScalefactors(hdr, scf[:], istPos[ch][:], g, &main, ch)

				for i := range 39 {
					wantScf := expectScfForSubblockGain(int(g.globalGain), forcedSubblockGain, i)
					if wantScf == 0 {
						t.Fatalf("gr %d ch %d band %d: expectScfForSubblockGain = 0, test setup bug", gi, ch, i)
					}
					if relErr := math.Abs(float64(scf[i]-wantScf)) / float64(wantScf); relErr > 1e-4 {
						t.Fatalf("gr %d ch %d band %d (window %d, subblock_gain=%d): scf = %v, want %v (relative error %v)",
							gi, ch, i, i%3, forcedSubblockGain[i%3], scf[i], wantScf, relErr)
					}
				}
				pos += int(g.part23Length)
			}
		}
	})
}
