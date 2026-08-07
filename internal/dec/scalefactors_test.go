package dec

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
)

// TestL3LdexpQ2 hand-computes a couple of cases: one within a single loop
// pass (expQ2 <= 120) and one spanning two passes (expQ2 > 120, exercising
// the do-while's multi-iteration branch), verified with float64 arithmetic
// and truncated to float32 exactly where tools/oracle/minimp3.h:642-652
// would (see l3LdexpQ2's doc comment: no double promotion occurs in the
// real function, this is just an independent way to hand-check it).
func TestL3LdexpQ2(t *testing.T) {
	expfrac := [4]float64{9.31322575e-10, 7.83145814e-10, 6.58544508e-10, 5.53767716e-10}
	want := func(y float64, expQ2 int) float32 {
		for {
			e := min(120, expQ2)
			y *= expfrac[e&3] * float64(int64(1)<<30>>uint(e>>2))
			expQ2 -= e
			if expQ2 <= 0 {
				break
			}
		}
		return float32(y)
	}

	tests := []struct {
		y      float32
		expQ2  int
		wantAt float64
	}{
		{1 << 11, 44 - 39, 44 - 39}, // single pass, small exp_q2
		{1 << 11, 260, 260},         // spans two passes (>120)
	}
	for _, tt := range tests {
		got := l3LdexpQ2(tt.y, tt.expQ2)
		w := want(float64(tt.y), tt.expQ2)
		if math.Float32bits(got) != math.Float32bits(w) {
			t.Errorf("l3LdexpQ2(%v, %d) = %v (%08x), want %v (%08x)",
				tt.y, tt.expQ2, got, math.Float32bits(got), w, math.Float32bits(w))
		}
	}
}

// TestScalefactorTablesMatchOracle asserts the four ported const tables
// (scfLongTable, scfShortTable and scfMixedTable in sideinfo.go;
// scfcDecodeTable here) are byte-identical to the pin's g_scf_long,
// g_scf_short, g_scf_mixed and g_scfc_decode. tools/oracle/tables.c is a
// standalone C program (not linked against build/minimp3.h: those tables
// are function-local statics, not externally linkable symbols) that
// prints the same four tables, transcribed from tools/oracle/minimp3.h
// with line-number citations, in this same order, concatenated. This
// golden hash was produced once with:
//
//	cc -O2 -o /tmp/mp3tables tools/oracle/tables.c && /tmp/mp3tables | sha256sum
//
// Re-run that whenever tools/oracle/tables.c's literals change to refresh
// wantHex.
func TestScalefactorTablesMatchOracle(t *testing.T) {
	const wantHex = "6021d92cb4e7c8131a01174d2261fad27c382726320f98eb07f98b56e4d6c5ba"

	h := sha256.New()
	for _, row := range scfLongTable {
		h.Write(row[:])
	}
	for _, row := range scfShortTable {
		h.Write(row[:])
	}
	for _, row := range scfMixedTable {
		h.Write(row[:])
	}
	h.Write(scfcDecodeTable[:])

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		t.Fatalf("scalefactor table checksum = %s, want %s (see tools/oracle/tables.c)", got, wantHex)
	}
}

// TestL3ReadScalefactorsMatchesOracle is the differential test: it walks
// every replayFixtures fixture frame by frame, threading a fresh
// reservoir per fixture through l3ReadSideInfo, l3RestoreReservoir and
// l3ReadScalefactors exactly as mp3dec_decode_frame/L3_decode do
// (tools/oracle/minimp3.h:1759-1777), and compares the resulting
// scalefactors' float32 bit patterns against the "scf" stage of the
// oracle's dump.
//
// Huffman decode (L3_huffman) is Task 7's job, not this one's, but
// L3_huffman's last statement unconditionally sets bs->pos = layer3gr_limit
// regardless of how the actual Huffman decode went (tools/oracle/minimp3.h:876),
// so this test reproduces that exact effect on the bit position via
// mainBS.SetPos(layer3grLimit) without decoding any Huffman data itself.
// That keeps the reservoir's bit-position bookkeeping bit-exact across
// frames (needed for every later frame's scf to line up with the oracle)
// without pulling Huffman decode into this task's scope.
func TestL3ReadScalefactorsMatchesOracle(t *testing.T) {
	for _, fx := range replayFixtures(t) {
		frameRecs := readDump(t, fx, "frames")
		scfRecs := readDump(t, fx, "scf")
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

			// Mirrors mp3dec_decode_frame's outer gate
			// (tools/oracle/minimp3.h:1762): main_data_begin<0 or side
			// info's own reads overran the frame means the decoder resets
			// without ever calling L3_restore_reservoir/L3_save_reservoir.
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
						rec := scfRecs[ci]
						ci++

						// Only [0:n) is ever written by l3ReadScalefactors
						// (mirrors upstream leaving mp3dec_scratch_t.scf's
						// tail untouched); comparing beyond n would compare
						// the oracle's uninitialized stack garbage there.
						n := int(gi.nLongSfb) + int(gi.nShortSfb)
						for i := 0; i < n; i++ {
							if math.Float32bits(scf[i]) != math.Float32bits(rec.F32[i]) {
								t.Fatalf("%s: frame at %d granule-ch %d scf[%d] = %08x, want %08x",
									fx, pos, g, i, math.Float32bits(scf[i]), math.Float32bits(rec.F32[i]))
							}
						}

						mainBS.SetPos(layer3grLimit)
					}
				}
				l3SaveReservoir(&res, mainBS, mainData)
			}

			pos += off + fb
		}
		if ci != len(scfRecs) {
			t.Fatalf("%s: consumed %d of %d scf records", fx, ci, len(scfRecs))
		}
	}
}
