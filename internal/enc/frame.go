package enc

import "github.com/tphakala/go-mp3/internal/bits"

// granuleCoding is one coded granule-channel, ready for side info + main
// data: the scalefactor state and its side-info encoding, the exact
// Huffman bit count, the global gain that produced it, the spectrum
// partition and region layout codeGranule settled on, and the quantized
// spectrum itself.
type granuleCoding struct {
	sf          scfState // per-band scalefactor state (zero value: Phase 3 behavior)
	part2Bits   int      // scalefactor side-info bits (0 until the outer loop, Task 4/5, sets sf)
	scfCompress int      // scalefac_compress side-info value (0 until Task 4/5)
	scfsi       int      // scfsi bits, granule 1 only (0 until Task 4/5)

	// peBits is this granule-channel's part23 bit demand estimate
	// (granuleDemandBits applied to the psymodel's PE), set at analysis
	// time (Task 3, codeFrame's pass 1) before the reservoir's planFrame
	// has seen the frame's other granule-channels. It is not itself the
	// budget the outer loop codes against (planFrame's budgets are), just
	// the input planFrame splits: kept on granuleCoding rather than a
	// separate Encoder-owned array so a caller inspecting one
	// granule-channel's final coding can also see what it asked for.
	peBits int

	part23Length int // part2Bits + Huffman bits (part3); part2Bits is 0 in PR A
	globalGain   int
	part         spectrumPartition
	ri           regionInfo
	ix           [576]int32

	// blockType is this granule's window shape (blockLong/Start/Short/Stop,
	// blocktypes.go). The live encoder path picks it per granule-channel via
	// decideBlockTypes (attack-driven short blocks and their start/stop
	// brackets); the test-only AppendFrameShortPin/AppendFrameShortPinSG pin
	// the non-long values directly. regionsFor dispatches on it (chooseRegions
	// for blockLong, chooseRegionsWS otherwise), writeSideInfo emits the
	// window-switching branch for any non-long value, and detectScfsi's
	// decision-6 guard reads it.
	blockType int

	// lay is the coding-order band geometry this granule-channel was coded
	// against (codeGranule's own lay parameter, cached here): renderMainData
	// needs one per granule-channel to call writeSpectrum, since the render
	// pair no longer threads srIndex through a single frame-level call the
	// way the Phase 3 appendFrame did. Every long granule-channel in one
	// frame is coded against the same pointer in practice (srIndex is fixed
	// for an Encoder's lifetime), so this is redundant within a frame but
	// keeps renderMainData self-contained.
	lay *bandLayout
}

// maxPart23Length is the largest value part_2_3_length's 12-bit side-info
// field can hold. bits.Writer.WriteBits masks silently, so a granule coded
// past this bound would have its part_2_3_length truncated on write while
// the Huffman bits actually emitted stayed the true (larger) count: the
// decoder would then walk a garbage main-data length and desync. codeGranule
// caps its effective budget at this value so that can never happen.
const maxPart23Length = 4095

// regionsFor dispatches to chooseRegions for a long granule or
// chooseRegionsWS for a window-switching one (start/short/stop,
// gc.blockType != blockLong): the single seam recode and codeGranule's
// truncation fallback both call, so a window-switching granule always
// gets the fixed two-table region split the decoder actually implies
// (ISO 2.4.2.7, design decision 5) rather than the long-block exhaustive
// search, which searches a region-count encoding a window-switching
// granule's side info never carries.
func regionsFor(ix *[576]int32, part spectrumPartition, lay *bandLayout, blockType int) regionInfo {
	if blockType == blockLong {
		return chooseRegions(ix, part, lay)
	}
	return chooseRegionsWS(ix, part, lay, blockType)
}

// recode quantizes xr at gg under gc's scalefactor state, partitions the
// result, and chooses region boundaries, writing straight into gc. A small
// shared step so codeGranule's two rate-loop phases (raise gain, then
// spectral truncation) do not duplicate the quantize/partition/choose
// sequence.
func recode(xr *[576]float64, gg int, lay *bandLayout, gc *granuleCoding) {
	quantizeGranule(xr, gg, &gc.sf, lay, &gc.ix)
	gc.part = partitionSpectrum(&gc.ix)
	gc.ri = regionsFor(&gc.ix, gc.part, lay, gc.blockType)
}

// codeGranule runs the inner rate loop for one granule-channel: starting at
// minGlobalGain, it searches for the smallest global_gain whose
// quantize + partition + chooseRegions coding fits ri.bits <= budgetBits,
// falling back to gg 255 when none does (then the truncation fallback zeroes
// lines from the top in sfb steps until it fits; bits reach 0 on an empty
// spectrum, so it terminates).
//
// fast selects the search strategy. With fast false (RateControlExact, the
// default) searchGlobalGainExact scans gg upward one step at a time and always
// finds the exact smallest fitting gg, the finest quantization within budget,
// at up to ~255 recodes on frames that start far below their fitting gain.
// With fast true (RateControlFast) searchGlobalGainFast binary-searches
// instead, about log2(255) recodes, an order-of-magnitude speedup on those
// escalation-heavy frames; because bits(gg) is not strictly monotone it can
// settle a step or two above the exact choice, so its output is not
// bit-identical to the exact scan. See each helper for the details.
//
// The rate loop targets min(budgetBits, maxPart23Length): see
// maxPart23Length's doc comment for why. Any unused remainder of budgetBits
// (whether from the cap or simply an easy-to-code granule) becomes zero
// stuffing at the frame tail, same as any other unused main-data budget;
// rolling it to another granule via a bit reservoir is deferred to Phase 4.
func codeGranule(xr *[576]float64, budgetBits int, lay *bandLayout, gc *granuleCoding, fast bool) {
	gc.lay = lay
	effBudget := min(budgetBits, maxPart23Length)

	gg := minGlobalGain(xr, &gc.sf, lay)
	recode(xr, gg, lay, gc)

	if fast {
		gg = searchGlobalGainFast(xr, effBudget, gg, lay, gc)
	} else {
		gg = searchGlobalGainExact(xr, effBudget, gg, lay, gc)
	}

	for gc.ri.bits > effBudget && zeroTopSfb(&gc.ix, lay) {
		gc.part = partitionSpectrum(&gc.ix)
		gc.ri = regionsFor(&gc.ix, gc.part, lay, gc.blockType)
	}

	gc.globalGain = gg
	gc.part23Length = gc.part2Bits + gc.ri.bits
}

// searchGlobalGainExact scans global_gain upward from gg (already recoded into
// gc) one step at a time, recoding at each step, until gc's coding fits
// effBudget or gg reaches 255. It returns the smallest gg in [gg, 255] whose
// coding fits, else 255, and leaves gc holding that gg's coding. This is the
// exact rate-control behavior (RateControlExact): it always lands on the finest
// quantization within budget, at the cost of up to ~255 recodes per call on a
// granule that starts far below its fitting gain.
func searchGlobalGainExact(xr *[576]float64, effBudget, gg int, lay *bandLayout, gc *granuleCoding) int {
	for gc.ri.bits > effBudget && gg < 255 {
		gg++
		recode(xr, gg, lay, gc)
	}
	return gg
}

// searchGlobalGainFast binary-searches global_gain for the smallest fitting
// value instead of scanning (RateControlFast). gg is minGlobalGain, already
// recoded into gc; if that coding already fits, or gg is 255, it returns
// immediately, matching the exact scan. Otherwise it lower_bounds over
// (gg, 255], recoding at each probe, then a trailing recode restores gc to the
// chosen gg since the last probe need not have been at it. This is about
// log2(255) recodes rather than up to 255.
//
// It equals searchGlobalGainExact only where bits(gg) has no non-monotone bump
// straddling effBudget. bits(gg) does carry occasional small bumps (region
// granularity in chooseRegions/partitionSpectrum), so on a straddling bump the
// search can skip a lower fitting gg and settle one or a few steps higher: a
// slightly coarser quantization, never finer, hence a small bounded quality
// reduction traded for the speed. Its output is therefore not bit-identical to
// the exact scan, which is why RateControlFast is opt-in and the encoder
// defaults to RateControlExact.
func searchGlobalGainFast(xr *[576]float64, effBudget, gg int, lay *bandLayout, gc *granuleCoding) int {
	if gc.ri.bits <= effBudget || gg >= 255 {
		return gg
	}
	lo, hi := gg+1, 255
	for lo < hi {
		mid := (lo + hi) / 2
		recode(xr, mid, lay, gc)
		if gc.ri.bits <= effBudget {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	recode(xr, lo, lay, gc)
	return lo
}

// zeroTopSfb zeros the highest-indexed scalefactor band of ix that has any
// nonzero line, per lay's cumulative band edges, and reports whether it
// found one to zero. It is codeGranule's global_gain=255 truncation
// fallback: the deterministic last resort when even the maximum gain still
// leaves a granule over budget. Repeated calls converge on the all-zero
// spectrum (0 Huffman bits, within any nonnegative budget), so
// codeGranule's fallback loop always terminates.
func zeroTopSfb(ix *[576]int32, lay *bandLayout) bool {
	var edge [40]int
	for i := range lay.nBands {
		edge[i+1] = edge[i] + lay.width[i]
	}

	for sfb := lay.nBands - 1; sfb >= 0; sfb-- {
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
// mode(2), mode_extension(2), copyright(1)=0, original(1)=1,
// emphasis(2)=0. Byte 1 (sync tail + ID + layer + protection) is therefore
// always 0xFB for this scope, regardless of the arguments.
//
// bitrateIndex: 1..14 for {32,40,48,56,64,80,96,112,128,160,192,224,256,320}
// kbps. srIndex: 0=44100, 1=48000, 2=32000. mode: 0 (stereo) or 3
// (single_channel). modeExt: mode_extension, 0 unless mode is joint stereo
// (1); 2 selects M/S with intensity stereo off.
func frameHeader(bitrateIndex, srIndex, padding, mode, modeExt int) [4]byte {
	return [4]byte{
		0xFF,
		0xFB,
		byte(bitrateIndex<<4 | srIndex<<2 | padding<<1),
		byte(mode<<6 | modeExt<<4 | 1<<2), // copyright=0, original=1, emphasis=0
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

// maxFrameBytes is the largest CBR frame: 320kbps at 32kHz plus padding.
// (144000*320/32000 + 1 = 1441.)
const maxFrameBytes = 1441

// fifoSlots bounds the pending ring: with resCap <= 7 areas (design
// decision 2), a slot's area is complete after at most 8 later frames,
// plus the current frame and margin.
const fifoSlots = 12

// frameSlot is one pending container frame: its full byte image and how
// much of its main-data area has been placed so far.
type frameSlot struct {
	buf  [maxFrameBytes]byte
	n    int // total frame length
	base int // area start: 4 + side info bytes
	fill int // area bytes placed so far; complete when base+fill == n
}

// frameFIFO is the preallocated pending-frame ring: the physical
// realization of the bit reservoir, threading one continuous main-data
// byte stream across however many container frames it takes to drain.
type frameFIFO struct {
	slots       [fifoSlots]frameSlot
	head, count int
}

// push appends a new pending frame: header+side info already rendered in
// hdr (length base), total frame length n. Panics if the ring is full
// (structurally impossible under design decision 2; the panic guards the
// invariant).
func (f *frameFIFO) push(hdr []byte, n int) {
	if f.count == fifoSlots {
		panic("enc: frameFIFO: push into a full ring")
	}
	slot := &f.slots[(f.head+f.count)%fifoSlots]
	copy(slot.buf[:], hdr)
	slot.n = n
	slot.base = len(hdr)
	slot.fill = 0
	f.count++
}

// place threads main-data bytes through the pending slots' unfilled area
// bytes in order (the no-gap policy): the byte-stream threading itself.
// Panics if b holds more bytes than the pending slots have room for
// (structurally impossible under design decision 2: a caller always
// clamps its spend to the slots' total available room first).
func (f *frameFIFO) place(b []byte) {
	for i := range f.count {
		if len(b) == 0 {
			break
		}
		slot := &f.slots[(f.head+i)%fifoSlots]
		if take := min(len(b), slot.n-slot.base-slot.fill); take > 0 {
			copy(slot.buf[slot.base+slot.fill:], b[:take])
			slot.fill += take
			b = b[take:]
		}
	}
	if len(b) > 0 {
		panic("enc: frameFIFO: place: bytes exceed the pending slots' total room")
	}
}

// flushInto appends every COMPLETE leading slot to dst and pops it.
func (f *frameFIFO) flushInto(dst []byte) []byte {
	for f.count > 0 {
		slot := &f.slots[f.head]
		if slot.base+slot.fill != slot.n {
			break
		}
		dst = append(dst, slot.buf[:slot.n]...)
		f.head = (f.head + 1) % fifoSlots
		f.count--
	}
	return dst
}

// flushAll zero-fills every remaining area byte (unaddressed stuffing)
// and appends everything: the drain path.
func (f *frameFIFO) flushAll(dst []byte) []byte {
	for f.count > 0 {
		slot := &f.slots[f.head]
		for i := slot.base + slot.fill; i < slot.n; i++ {
			slot.buf[i] = 0
		}
		dst = append(dst, slot.buf[:slot.n]...)
		f.head = (f.head + 1) % fifoSlots
		f.count--
	}
	return dst
}

// unfilled returns the total unfilled area bytes over pending slots: the
// structural cross-check for reservoir.occ (they must always agree; the
// Encoder asserts it in debug tests).
func (f *frameFIFO) unfilled() int {
	total := 0
	for i := range f.count {
		slot := &f.slots[(f.head+i)%fifoSlots]
		total += slot.n - slot.base - slot.fill
	}
	return total
}

// renderFrameInto writes one frame's header and side info (per the exact
// MPEG-1 field layout l3ReadSideInfo consumes, internal/dec/sideinfo.go:69)
// into dst, and returns the extended slice along with base: the number of
// bytes just written (4-byte header plus the side-info block), which is
// where this frame's main-data area begins. mainDataBegin is written into
// the side info's main_data_begin field verbatim; codeFrame passes the
// reservoir's live occupancy (e.resv.occ), while only the test-only legacy
// pin helpers (the assembleFrame path) pass 0.
func renderFrameInto(dst []byte, bitrateIndex, srIndex, padding, mode, modeExt int, gr *[2][2]granuleCoding, nch, mainDataBegin int) (out []byte, base int) {
	frameStart := len(dst)
	header := frameHeader(bitrateIndex, srIndex, padding, mode, modeExt)
	dst = append(dst, header[:]...)

	w := bits.NewWriter(dst)
	writeSideInfo(&w, gr, nch, mainDataBegin)
	dst = w.Flush()

	return dst, len(dst) - frameStart
}

// renderMainData writes every granule-channel's main data into dst, in
// granule-major channel-minor order (scalefactors via writeScalefactors
// then the Huffman spectrum via writeSpectrum, the same ordering the
// Phase 3 appendFrame used), through one bits.Writer, flushes (byte-
// padding the final partial byte), then appends zero ancillary bytes
// until at least spendMin bytes have been added since dst's starting
// length (the unused remainder of a pinned physical spend, same role as
// appendFrame's old zero stuffing). Each granule-channel writes against
// gc.lay, the band layout codeGranule cached at coding time, so this
// function needs no srIndex of its own.
//
// Panics if a granule-channel's written part2 (writeScalefactors) or
// part3 (writeSpectrum) bit count disagrees with its own recorded
// part2Bits/ri.bits, or their sum disagrees with part23Length: that can
// only happen on an invariant violation in the caller (e.g. coding the
// granule against a different layout than gc.lay names here, or
// setting scfCompress/scfsi inconsistently with sf), not a recoverable
// runtime condition (same rationale as bits.Writer's n-range panic).
func renderMainData(dst []byte, gr *[2][2]granuleCoding, nch, spendMin int) []byte {
	start := len(dst)
	w := bits.NewWriter(dst)
	for g := range 2 {
		for ch := range nch {
			gc := &gr[g][ch]
			part2 := writeScalefactors(&w, gc)
			part3 := writeSpectrum(&w, &gc.ix, gc.part, gc.ri, gc.lay)
			if part2 != gc.part2Bits || part3 != gc.ri.bits || part2+part3 != gc.part23Length {
				panic("enc: renderMainData: part2+part3 bit count diverged from codeGranule's recorded part23Length")
			}
		}
	}
	dst = w.Flush()

	for len(dst)-start < spendMin {
		dst = append(dst, 0)
	}
	return dst
}

// assembleFrame renders one complete, self-contained frame: header and
// side info with mainDataBegin=0, then main data padded with ancillary
// zeros to the frame's exact byte length. This reproduces the Phase 3
// whole-frame layout through the new render primitives, for the callers
// in this package not yet wired to the reservoir (Task 3): AppendFramePin,
// AppendFrameScfPin, and the Encoder's codeFrame.
// TestZeroReservoirEquivalence proves the FIFO path this bypasses
// produces the identical bytes.
func assembleFrame(dst []byte, bitrateIndex, srIndex, padding, mode int, gr *[2][2]granuleCoding, nch int) []byte {
	frameStart := len(dst)
	dst, base := renderFrameInto(dst, bitrateIndex, srIndex, padding, mode, 0, gr, nch, 0)
	n := frameLength(bitrateIndex, srIndex, padding)
	dst = renderMainData(dst, gr, nch, n-base)
	if len(dst) != frameStart+n {
		panic("enc: assembleFrame: main data overflowed the frame's byte budget")
	}
	return dst
}

// --- Test-only cross-package surface ---
//
// codeGranule, renderFrameInto, renderMainData, and assembleFrame stay
// unexported: internal encoder plumbing, not this package's public
// surface (a later task defines that). internal/dec's structural
// validator (encx_frame_test.go) is a white-box test living in package
// dec, needed there because it drives the decoder's unexported
// hdrValid/l3ReadSideInfo/grInfo directly against a real emitted stream; Go
// visibility rules mean it can only reach this package's EXPORTED names,
// even though "internal/dec importing internal/enc in a _test.go file" is
// itself the sanctioned exception to "enc must never import dec" (see
// PROVENANCE.md). AppendFramePin is the minimal exported surface that gate
// needs, mirroring huffman.go's EncodeHuffmanPin precedent for the same
// cross-package white-box reason.

// AppendFramePin codes xr[g][ch] through the production codeGranule (real
// budget arithmetic, including the maxPart23Length cap) and assembles one
// frame via the production assembleFrame. Test-only.
func AppendFramePin(dst []byte, bitrateIndex, srIndex, padding, mode int, xr *[2][2][576]float64, nch int) []byte {
	lay := &layoutLong[srIndex]
	budget := granuleBudgetBits(bitrateIndex, srIndex, padding, nch)

	var gr [2][2]granuleCoding
	for g := range 2 {
		for ch := range nch {
			codeGranule(&xr[g][ch], budget, lay, &gr[g][ch], false)
		}
	}
	return assembleFrame(dst, bitrateIndex, srIndex, padding, mode, &gr, nch)
}

// ScfPin is one granule-channel's pinned scalefactor state for
// AppendFrameScfPin: the internal/dec encx_ readback tests drive the inner
// rate loop at these exact scalefactors instead of the implicit all-zero
// state codeGranule otherwise sees.
type ScfPin struct {
	Scf           [21]int
	ScalefacScale int
	Preflag       int
}

// AppendFrameScfPin codes xr[g][ch] through the production codeGranule with
// each granule-channel's scalefactor state PRESET from pins (the inner rate
// loop only; no outer loop chooses the scalefactors here, that is Task 4/5's
// job), picks each granule-channel's cheapest covering scalefac_compress via
// chooseScalefacCompress, optionally detects and applies scfsi across
// granule 0/1 per channel, and assembles one frame via the production
// assembleFrame. Test-only, the AppendFramePin precedent.
//
// scalefac_compress is chosen, and gc.part2Bits set, BEFORE codeGranule
// runs: chooseScalefacCompress depends only on the pinned sf, not on the
// quantized spectrum, so the granule-channel's part2 cost is already known.
// codeGranule is then budgeted at budget-part2 (not the raw per-granule
// share), so its Huffman rate loop leaves exactly enough room for the
// scalefactor bits assembleFrame will also have to write; without that
// reduction, part2 would land on top of an already-full Huffman budget and
// overflow the frame's fixed byte length.
func AppendFrameScfPin(dst []byte, bitrateIndex, srIndex, padding, mode int, xr *[2][2][576]float64, pins *[2][2]ScfPin, useScfsi bool, nch int) []byte {
	lay := &layoutLong[srIndex]
	budget := granuleBudgetBits(bitrateIndex, srIndex, padding, nch)

	var gr [2][2]granuleCoding
	for g := range 2 {
		for ch := range nch {
			gc := &gr[g][ch]
			pin := pins[g][ch]
			// ScfPin.Scf stays [21]int (the long-block pin surface
			// internal/dec's readback tests already build literals
			// against); copy it into the widened [36]int scfState.scf
			// rather than a direct array assignment.
			copy(gc.sf.scf[:len(pin.Scf)], pin.Scf[:])
			gc.sf.scalefacScale = pin.ScalefacScale
			gc.sf.preflag = pin.Preflag

			idx, part2, ok := chooseScalefacCompress(&gc.sf, 0, lay)
			if !ok {
				panic("enc: AppendFrameScfPin: pinned scalefactors not coverable by any scalefac_compress index")
			}
			gc.scfCompress = idx
			gc.part2Bits = part2

			codeGranule(&xr[g][ch], budget-part2, lay, gc, false)
		}
	}

	if useScfsi {
		for ch := range nch {
			mask := detectScfsi(&gr[0][ch], &gr[1][ch])
			applyScfsi(&gr[1][ch], mask)
		}
	}

	return assembleFrame(dst, bitrateIndex, srIndex, padding, mode, &gr, nch)
}

// AppendFrameShortPin codes one self-contained frame (main_data_begin 0,
// the AppendFramePin precedent) with each granule-channel's block type
// FORCED from blockTypes, through the real production pipeline: outerLoop
// (the masking-driven inner rate loop, unchanged by this task) picking
// scalefactors/subblock_gain/global_gain against xmin, codeGranule's
// regionsFor dispatch routing a window-switching granule to
// chooseRegionsWS, and the same render path (writeSideInfo,
// writeScalefactors, writeSpectrum) every other pin uses. xr must already
// be in CODING order for any granule-channel whose blockTypes entry is
// blockShort (callers run reorderShort first; see bandLayout's doc
// comment for what "coding order" means for a short granule).
//
// Padding is fixed at 0 and mode is derived from nch (single_channel for
// mono, L/R stereo for two channels): this pin exists to drive the
// window-switching side-info and readback gates, not to exercise every
// header permutation AppendFramePin/AppendFrameScfPin already cover.
func AppendFrameShortPin(dst []byte, bitrateIndex, srIndex, nch int, blockTypes *[2][2]int, xr *[2][2][576]float64, xmin *[2][2][39]float64) []byte {
	budget := granuleBudgetBits(bitrateIndex, srIndex, 0, nch)

	var gr [2][2]granuleCoding
	for g := range 2 {
		for ch := range nch {
			bt := blockTypes[g][ch]
			lay := layoutFor(bt, srIndex)
			gc := &gr[g][ch]
			gc.blockType = bt

			var best granuleCoding
			outerLoop(&xr[g][ch], &xmin[g][ch], budget, lay, gc, &best, false)
		}
	}

	mode := 0
	if nch == 1 {
		mode = 3
	}
	return assembleFrame(dst, bitrateIndex, srIndex, 0, mode, &gr, nch)
}

// AppendFrameShortPinSG is AppendFrameShortPin's sibling for driving
// subblock_gain directly: instead of letting outerLoop's masking escalation
// (escalateSubblockGain) pick subblock_gain, each granule-channel's value is
// FORCED from subblockGain and the inner rate loop alone (codeGranule) codes
// the granule against it, the AppendFrameScfPin precedent for scf/
// scalefacScale/preflag applied here to subblock_gain instead.
//
// This exists because escalateSubblockGain, as integrated into outerLoop, is
// largely a no-op in practice (a separate, already-flagged concern): driving
// a nonzero subblock_gain through the real outer loop is unreliable, but the
// decoder-readback gate for the iscf += subblockGain[i] << sh dequant term
// (internal/dec/scalefactors.go) needs a known nonzero value regardless of
// whether the escalation policy would ever choose one. blockTypes must pick
// blockShort/blockStart/blockStop for any granule-channel given a nonzero
// subblockGain; a long granule ignores it entirely (ISO 2.4.1.7, no
// subblock_gain field).
func AppendFrameShortPinSG(dst []byte, bitrateIndex, srIndex, nch int, blockTypes *[2][2]int, xr *[2][2][576]float64, subblockGain *[2][2][3]int) []byte {
	budget := granuleBudgetBits(bitrateIndex, srIndex, 0, nch)

	var gr [2][2]granuleCoding
	for g := range 2 {
		for ch := range nch {
			bt := blockTypes[g][ch]
			lay := layoutFor(bt, srIndex)
			gc := &gr[g][ch]
			gc.blockType = bt
			gc.sf.subblockGain = subblockGain[g][ch]

			idx, part2, ok := chooseScalefacCompress(&gc.sf, 0, lay)
			if !ok {
				panic("enc: AppendFrameShortPinSG: pinned scalefactors not coverable by any scalefac_compress index")
			}
			gc.scfCompress = idx
			gc.part2Bits = part2

			codeGranule(&xr[g][ch], budget-part2, lay, gc, false)
		}
	}

	mode := 0
	if nch == 1 {
		mode = 3
	}
	return assembleFrame(dst, bitrateIndex, srIndex, 0, mode, &gr, nch)
}
