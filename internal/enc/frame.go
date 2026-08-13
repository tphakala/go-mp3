package enc

import "github.com/tphakala/go-mp3/internal/bits"

// granuleCoding is one coded granule-channel, ready for side info + main
// data: the exact Huffman bit count (part2 is 0, Phase 3 carries no
// scalefactors), the global gain that produced it, the spectrum partition
// and region layout codeGranule settled on, and the quantized spectrum
// itself.
type granuleCoding struct {
	part23Length int // exact Huffman bits (part2 is 0: no scalefactors in Phase 3)
	globalGain   int
	part         spectrumPartition
	ri           regionInfo
	ix           [576]int32
}

// maxPart23Length is the largest value part_2_3_length's 12-bit side-info
// field can hold. bits.Writer.WriteBits masks silently, so a granule coded
// past this bound would have its part_2_3_length truncated on write while
// the Huffman bits actually emitted stayed the true (larger) count: the
// decoder would then walk a garbage main-data length and desync. codeGranule
// caps its effective budget at this value so that can never happen.
const maxPart23Length = 4095

// recode quantizes xr at gg, partitions the result, and chooses region
// boundaries, writing straight into gc. A small shared step so codeGranule's
// two rate-loop phases (raise gain, then spectral truncation) do not
// duplicate the quantize/partition/choose sequence.
func recode(xr *[576]float64, gg int, sfbWidths *[22]int, gc *granuleCoding) {
	quantizeGranule(xr, gg, &gc.ix)
	gc.part = partitionSpectrum(&gc.ix)
	gc.ri = chooseRegions(&gc.ix, gc.part, sfbWidths)
}

// codeGranule runs the inner rate loop for one granule-channel: from
// minGlobalGain upward, quantize + partition + chooseRegions until
// ri.bits <= budgetBits (or gg reaches 255, then the truncation fallback
// zeroes lines from the top in sfb steps until it fits; bits reach 0 on an
// empty spectrum, so it terminates).
//
// The rate loop targets min(budgetBits, maxPart23Length): see
// maxPart23Length's doc comment for why. Any unused remainder of budgetBits
// (whether from the cap or simply an easy-to-code granule) becomes zero
// stuffing at the frame tail, same as any other unused main-data budget;
// rolling it to another granule via a bit reservoir is deferred to Phase 4.
func codeGranule(xr *[576]float64, budgetBits int, sfbWidths *[22]int, gc *granuleCoding) {
	effBudget := min(budgetBits, maxPart23Length)

	gg := minGlobalGain(xr)
	recode(xr, gg, sfbWidths, gc)

	for gc.ri.bits > effBudget && gg < 255 {
		gg++
		recode(xr, gg, sfbWidths, gc)
	}

	for gc.ri.bits > effBudget && zeroTopSfb(&gc.ix, sfbWidths) {
		gc.part = partitionSpectrum(&gc.ix)
		gc.ri = chooseRegions(&gc.ix, gc.part, sfbWidths)
	}

	gc.globalGain = gg
	gc.part23Length = gc.ri.bits
}

// zeroTopSfb zeros the highest-indexed scalefactor band of ix that has any
// nonzero line, per sfbWidths' cumulative band edges, and reports whether it
// found one to zero. It is codeGranule's global_gain=255 truncation
// fallback: the deterministic last resort when even the maximum gain still
// leaves a granule over budget. Repeated calls converge on the all-zero
// spectrum (0 Huffman bits, within any nonnegative budget), so
// codeGranule's fallback loop always terminates.
func zeroTopSfb(ix *[576]int32, sfbWidths *[22]int) bool {
	var edge [23]int
	for i := range 22 {
		edge[i+1] = edge[i] + sfbWidths[i]
	}

	for sfb := 21; sfb >= 0; sfb-- {
		lo, hi := edge[sfb], edge[sfb+1]
		nonzero := false
		for i := lo; i < hi; i++ {
			if ix[i] != 0 {
				nonzero = true
				break
			}
		}
		if !nonzero {
			continue
		}
		for i := lo; i < hi; i++ {
			ix[i] = 0
		}
		return true
	}
	return false
}

// bitrateKbpsTable maps bitrateIndex (1..14) to kbps for MPEG-1 Layer III,
// ISO/IEC 11172-3 Table B.1 (index 0 is free format, out of Phase 3's CBR
// scope and never used here).
var bitrateKbpsTable = [15]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}

// sampleRateHzTable maps srIndex (0=44100, 1=48000, 2=32000) to Hz, the same
// three MPEG-1 rates and ordering as sfbWidthsLong's rows.
var sampleRateHzTable = [3]int{44100, 48000, 32000}

// frameLength returns the exact byte length of an MPEG-1 Layer III CBR
// frame: 144000*kbps/sr + padding (1152 samples/frame * 125 bytes-per-kbps-
// per-second, ISO/IEC 11172-3 2.4.3.1).
func frameLength(bitrateIndex, srIndex, padding int) int {
	return 144000*bitrateKbpsTable[bitrateIndex]/sampleRateHzTable[srIndex] + padding
}

// sideInfoBits returns the exact packed size, in bits, of one frame's
// side-info block for nch channels: main_data_begin(9) + private_bits(5
// mono/3 stereo) + scfsi(4 per channel) + 2 granules * nch channels * 59
// bits/granule-channel (writeSideInfo's per-granule-channel field layout).
// 136 bits mono, 256 stereo.
func sideInfoBits(nch int) int {
	privateBits := 5
	if nch == 2 {
		privateBits = 3
	}
	return 9 + privateBits + nch*4 + 2*nch*59
}

// granuleBudgetBits returns the per-granule-channel Huffman bit budget for a
// frame: the frame's total byte length converted to bits, minus the 32-bit
// header and the side-info block, split evenly across the frame's 2*nch
// granule-channels.
func granuleBudgetBits(bitrateIndex, srIndex, padding, nch int) int {
	mainBits := frameLength(bitrateIndex, srIndex, padding)*8 - 32 - sideInfoBits(nch)
	return mainBits / (2 * nch)
}

// frameHeader packs the 32-bit MPEG-1 Layer III header: sync(11)=0x7FF,
// ID(2)=3 (MPEG-1), layer(2)=1 (Layer III), protection_bit(1)=1 (no CRC),
// bitrate_index(4), sampling_frequency(2), padding_bit(1), private_bit(1)=0,
// mode(2), mode_extension(2)=0, copyright(1)=0, original(1)=1,
// emphasis(2)=0. Byte 1 (sync tail + ID + layer + protection) is therefore
// always 0xFB for this scope, regardless of the arguments.
//
// bitrateIndex: 1..14 for {32,40,48,56,64,80,96,112,128,160,192,224,256,320}
// kbps. srIndex: 0=44100, 1=48000, 2=32000. mode: 0 (stereo) or 3
// (single_channel).
func frameHeader(bitrateIndex, srIndex, padding, mode int) [4]byte {
	return [4]byte{
		0xFF,
		0xFB,
		byte(bitrateIndex<<4 | srIndex<<2 | padding<<1),
		byte(mode<<6 | 1<<2), // mode_extension=0, copyright=0, original=1, emphasis=0
	}
}

// paddingState implements the CBR padding accumulator: exact mean bitrate
// via acc += (144000*kbps) % sr per frame; padding=1 and acc -= sr when
// acc >= sr. 48 kHz rates divide exactly (acc stays 0, never padded).
type paddingState struct{ acc int }

// next returns this frame's padding bit (0 or 1) and advances p's
// accumulator. bitrateKbps and sampleRate are the frame's actual values (not
// table indices), matching frameLength's own units.
func (p *paddingState) next(bitrateKbps, sampleRate int) (padding int) {
	p.acc += (144000 * bitrateKbps) % sampleRate
	if p.acc >= sampleRate {
		p.acc -= sampleRate
		return 1
	}
	return 0
}

// appendFrame assembles one complete frame: header, side info (per the
// exact MPEG-1 field layout l3ReadSideInfo consumes,
// internal/dec/sideinfo.go:69), main data (the granule-channel Huffman bits
// in granule-major channel-minor order), and zero stuffing to the exact
// frame length. main_data_begin is 0.
//
// sfbWidths is derived once from srIndex, &sfbWidthsLong[srIndex], and that
// same pointer is passed to every writeSpectrum call in the frame: the
// production source of truth for which sfb-width row a frame was coded
// against is always srIndex, never a value threaded separately. appendFrame
// panics if a writeSpectrum call's returned bit count disagrees with the
// granule's own recorded ri.bits: that can only happen if the caller coded
// the granule against a different sfbWidths row than srIndex names here, an
// invariant violation in the caller, not a recoverable runtime condition
// (same rationale as bits.Writer's n-range panic).
func appendFrame(dst []byte, bitrateIndex, srIndex, padding, mode int, gr *[2][2]granuleCoding, nch int) []byte {
	frameStart := len(dst)
	header := frameHeader(bitrateIndex, srIndex, padding, mode)
	dst = append(dst, header[:]...)

	sfb := &sfbWidthsLong[srIndex]
	w := bits.NewWriter(dst)
	writeSideInfo(&w, gr, nch)
	for g := range 2 {
		for ch := range nch {
			gc := &gr[g][ch]
			got := writeSpectrum(&w, &gc.ix, gc.part, gc.ri, sfb)
			if got != gc.ri.bits {
				panic("enc: appendFrame: writeSpectrum bit count diverged from codeGranule's recorded part23Length")
			}
		}
	}
	dst = w.Flush()

	want := frameStart + frameLength(bitrateIndex, srIndex, padding)
	if len(dst) > want {
		panic("enc: appendFrame: main data overflowed the frame's byte budget")
	}
	for len(dst) < want {
		dst = append(dst, 0)
	}
	return dst
}

// --- Test-only cross-package surface ---
//
// codeGranule and appendFrame stay unexported: internal encoder plumbing,
// not this package's public surface (a later task defines that). internal/
// dec's structural validator (encx_frame_test.go) is a white-box test living
// in package dec, needed there because it drives the decoder's unexported
// hdrValid/l3ReadSideInfo/grInfo directly against a real emitted stream; Go
// visibility rules mean it can only reach this package's EXPORTED names,
// even though "internal/dec importing internal/enc in a _test.go file" is
// itself the sanctioned exception to "enc must never import dec" (see
// PROVENANCE.md). AppendFramePin is the minimal exported surface that gate
// needs, mirroring huffman.go's EncodeHuffmanPin precedent for the same
// cross-package white-box reason.

// AppendFramePin codes xr[g][ch] through the production codeGranule (real
// budget arithmetic, including the maxPart23Length cap) and assembles one
// frame via the production appendFrame. Test-only.
func AppendFramePin(dst []byte, bitrateIndex, srIndex, padding, mode int, xr *[2][2][576]float64, nch int) []byte {
	sfb := &sfbWidthsLong[srIndex]
	budget := granuleBudgetBits(bitrateIndex, srIndex, padding, nch)

	var gr [2][2]granuleCoding
	for g := range 2 {
		for ch := range nch {
			codeGranule(&xr[g][ch], budget, sfb, &gr[g][ch])
		}
	}
	return appendFrame(dst, bitrateIndex, srIndex, padding, mode, &gr, nch)
}
