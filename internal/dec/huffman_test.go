package dec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
)

// TestHuffmanTablesMatchOracle asserts the six ported const tables
// (pow43Table, huffTabs, tab32Table, tab33Table, huffTabIndex,
// linbitsTable, all in tables.go) are byte-identical to the pin's
// g_pow43, tabs, tab32, tab33, tabindex and g_linbits.
// tools/oracle/tables.c is a standalone C program (not linked against
// build/minimp3.h: these tables are function-local statics inside
// L3_pow_43/L3_huffman, not externally-linkable symbols) that prints the
// same six tables, transcribed from tools/oracle/minimp3.h with
// line-number citations, in this same order, appended after the four
// tables Task 6 already checksums. This golden hash was produced once
// with:
//
//	cc -O2 -o /tmp/mp3tables tools/oracle/tables.c && /tmp/mp3tables | sha256sum
//
// Re-run that whenever tools/oracle/tables.c's literals change to refresh
// wantHex. float32 and int16 entries are serialized little-endian to
// match mp3dump.c's own stated little-endian assumption (the only build
// targets are amd64 and arm64), the same convention readDump's payload
// parsing already relies on.
func TestHuffmanTablesMatchOracle(t *testing.T) {
	const wantHex = "ac1a3503e47f132c0fdce11af36c65d1c665741739c07c60220bb9d00d86b0f8"

	h := sha256.New()
	var buf4 [4]byte
	for _, f := range pow43Table {
		binary.LittleEndian.PutUint32(buf4[:], math.Float32bits(f))
		h.Write(buf4[:])
	}
	for _, v := range huffTabs {
		binary.LittleEndian.PutUint16(buf4[:2], uint16(v))
		h.Write(buf4[:2])
	}
	h.Write(tab32Table[:])
	h.Write(tab33Table[:])
	for _, v := range huffTabIndex {
		binary.LittleEndian.PutUint16(buf4[:2], uint16(v))
		h.Write(buf4[:2])
	}
	h.Write(linbitsTable[:])

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		t.Fatalf("huffman table checksum = %s, want %s (see tools/oracle/tables.c)", got, wantHex)
	}
}

// TestL3HuffmanMatchesOracle is the differential test: it walks every
// replayFixtures fixture frame by frame, threading a fresh reservoir per
// fixture through l3ReadSideInfo, l3RestoreReservoir, l3ReadScalefactors
// and l3Huffman exactly as mp3dec_decode_frame/L3_decode do
// (tools/oracle/minimp3.h:1759-1777), and compares the resulting 576
// dequantized grbuf floats per granule-channel against the "grbuf_huff"
// stage of the oracle's dump.
//
// dst is zeroed before every l3Huffman call, mirroring upstream's
// memset(scratch.grbuf[0], 0, 576*2*sizeof(float)) once per granule
// (tools/oracle/minimp3.h:1772, run before L3_decode's per-channel loop):
// without it, entries l3Huffman itself never writes would compare this
// port's leftover values from an earlier call against the oracle's
// equally-arbitrary pre-zeroed state, which happens to be all zero
// because upstream zeroes it and never doesn't.
func TestL3HuffmanMatchesOracle(t *testing.T) {
	for _, fx := range replayFixtures(t) {
		frameRecs := readDump(t, fx, "frames")
		scfRecs := readDump(t, fx, "scf")
		huffRecs := readDump(t, fx, "grbuf_huff")
		data := readFile(t, fx)

		pos, free := 0, 0
		ci := 0
		var res reservoir
		maindata := make([]byte, maxBitreservoirBytes+maxFreeFormatFrameSize)
		var istPos [2][39]uint8

		for range frameRecs {
			var fb int
			off := findFrame(data[pos:], &free, &fb)
			if fb == 0 || pos+off+4 > len(data) {
				t.Fatalf("%s: no frame found at pos %d", fx, pos)
			}
			hdr := data[pos+off : pos+off+4]
			bsData := data[pos+off+4 : pos+off+fb]
			bs := bits.NewReader(bsData)
			if hdrIsCRC(hdr) {
				bs.Bits(16)
			}

			nch, _, grCount := grCountForHeader(hdr)

			var grBuf [4]grInfo
			mainDataBegin := l3ReadSideInfo(&bs, grBuf[:], hdr, len(bsData))

			if mainDataBegin >= 0 && !bs.Overrun() {
				mainBS, mainData, ok := l3RestoreReservoir(&res, &bs, bsData, mainDataBegin, maindata)
				if ok {
					for g := 0; g < grCount; g++ {
						ch := g % nch
						gi := &grBuf[g]
						layer3grLimit := mainBS.Pos() + int(gi.part23Length)

						var scf [40]float32
						l3ReadScalefactors(hdr, scf[:], istPos[ch][:], gi, &mainBS, ch)

						if ci >= len(scfRecs) {
							t.Fatalf("%s: frame at %d granule-ch %d: ran out of scf records", fx, pos, g)
						}
						if ci >= len(huffRecs) {
							t.Fatalf("%s: frame at %d granule-ch %d: ran out of grbuf_huff records", fx, pos, g)
						}

						var dst [576]float32
						l3Huffman(dst[:], &mainBS, gi, scf[:], layer3grLimit)

						rec := huffRecs[ci]
						ci++
						for i := range 576 {
							if math.Float32bits(dst[i]) != math.Float32bits(rec.F32[i]) {
								t.Fatalf("%s: frame at %d granule-ch %d dst[%d] = %08x, want %08x",
									fx, pos, g, i, math.Float32bits(dst[i]), math.Float32bits(rec.F32[i]))
							}
						}
					}
				}
				l3SaveReservoir(&res, mainBS, mainData)
			}

			pos += off + fb
		}
		if ci != len(huffRecs) {
			t.Fatalf("%s: consumed %d of %d grbuf_huff records", fx, ci, len(huffRecs))
		}
	}
}
