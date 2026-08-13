package dec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
)

// TestImdctTablesMatchOracle asserts the three ported const tables (gTwid9,
// gTwid3, gMdctWindow, all in tables.go) are byte-identical to the pin's
// g_twid9, g_twid3 and g_mdct_window. tools/oracle/tables.c appends these
// three tables (transcribed from tools/oracle/minimp3.h with line-number
// citations) after the tables Tasks 6/7/8 already checksum. This golden
// hash covers only the new trailing 240 bytes (18+6+36 float32 entries),
// independent of the rest of tables.c's output, produced once with:
//
//	cc -O2 -o /tmp/mp3tables tools/oracle/tables.c && \
//	  /tmp/mp3tables | tail -c 240 | sha256sum
//
// Re-run that whenever gTwid9/gTwid3/gMdctWindow's literals change to
// refresh wantHex.
func TestImdctTablesMatchOracle(t *testing.T) {
	const wantHex = "aac33f833c5043c18802830ab0db1a5bb0f7be2d2eba201356fa98afbedbf5d3"

	h := sha256.New()
	var buf4 [4]byte
	for _, f := range gTwid9 {
		binary.LittleEndian.PutUint32(buf4[:], math.Float32bits(f))
		h.Write(buf4[:])
	}
	for _, f := range gTwid3 {
		binary.LittleEndian.PutUint32(buf4[:], math.Float32bits(f))
		h.Write(buf4[:])
	}
	for _, row := range gMdctWindow {
		for _, f := range row {
			binary.LittleEndian.PutUint32(buf4[:], math.Float32bits(f))
			h.Write(buf4[:])
		}
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		t.Fatalf("imdct table checksum = %s, want %s (see tools/oracle/tables.c)", got, wantHex)
	}
}

// compareFloatsBitExact fails t if got[:n] and want[:n] disagree anywhere
// in their float32 bit patterns, identifying the mismatch by fixture,
// frame position, granule, channel and dump stage. The explicit n parameter
// handles both the 576-sample granule buffers and the 288-float overlap
// state, so callers across the dec tests share one comparator.
func compareFloatsBitExact(t *testing.T, fx string, pos, gr, ch int, stage string, got, want []float32, n int) {
	t.Helper()
	for i := range n {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s: frame at %d granule %d ch %d %s[%d] = %08x, want %08x",
				fx, pos, gr, ch, stage, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

// imdctGranule applies this task's stage to one granule already brought to
// "grbuf_pre_imdct" state: for each of nch channels, derives nLongBands
// exactly as upstream L3_decode's second per-channel loop does
// (tools/oracle/minimp3.h:1259-1260), runs l3ImdctGr against overlap[ch]
// (the fixture-lifetime, cross-frame accumulator threaded in by the
// caller), and compares the result against postRecs[ciBase+ch]
// ("grbuf_post_imdct", 576 floats) and overlapRecs[ciBase+ch] ("overlap",
// 288 floats).
func imdctGranule(t *testing.T, fx string, pos, gr, nch int, hdr []byte, grBuf []grInfo, overlap *[2][288]float32, grbuf *[2][576]float32, postRecs, overlapRecs []dumpRecord, ciBase int) {
	t.Helper()

	for ch := range nch {
		gi := &grBuf[gr*nch+ch]

		nLongBands := 0
		if gi.mixedBlockFlag != 0 {
			nLongBands = 2
		}
		if hdrGetMySampleRate(hdr) == 2 {
			nLongBands <<= 1
		}

		idx := ciBase + ch
		if idx >= len(postRecs) || idx >= len(overlapRecs) {
			t.Fatalf("%s: frame at %d granule %d ch %d: ran out of grbuf_post_imdct/overlap records", fx, pos, gr, ch)
		}

		l3ImdctGr(grbuf[ch][:], overlap[ch][:], gi.blockType, nLongBands)

		compareFloatsBitExact(t, fx, pos, gr, ch, "grbuf_post_imdct", grbuf[ch][:], postRecs[idx].F32, 576)
		compareFloatsBitExact(t, fx, pos, gr, ch, "overlap", overlap[ch][:], overlapRecs[idx].F32, 288)
	}
}

// TestImdctGrMatchesOracle is the differential test: it walks every
// replayFixtures fixture frame by frame exactly as
// TestStereoReorderAntialiasMatchesOracle does (Task 8), reusing
// decodeHuffmanGranule and stereoReorderAntialiasGranule to reach the
// "grbuf_pre_imdct" state, then runs imdctGranule against a per-fixture,
// per-channel overlap buffer that is threaded across every frame and
// granule of the fixture (fresh only once per fixture, never reset
// mid-stream), mirroring upstream mp3dec_t.mdct_overlap's lifetime. The
// result is compared against both the "grbuf_post_imdct" (576
// floats/channel) and "overlap" (288 floats/channel) oracle dump stages,
// added this task immediately after the L3_imdct_gr call and before
// L3_change_sign, by float bit pattern.
func TestImdctGrMatchesOracle(t *testing.T) {
	for _, fx := range replayFixtures(t) {
		frameRecs := readDump(t, fx, "frames")
		huffRecs := readDump(t, fx, "grbuf_huff")
		preRecs := readDump(t, fx, "grbuf_pre_imdct")
		postRecs := readDump(t, fx, "grbuf_post_imdct")
		overlapRecs := readDump(t, fx, "overlap")
		data := readFile(t, fx)

		pos, free := 0, 0
		ci := 0
		var res reservoir
		maindata := make([]byte, maxBitreservoirBytes+maxFreeFormatFrameSize)
		var istPos [2][39]uint8
		// overlap is state carried across every frame and granule of this
		// fixture, mirroring upstream mp3dec_t.mdct_overlap[2][288]: it is
		// declared once per fixture (zero-initialized, matching a freshly
		// constructed decoder) and never reset between frames.
		var overlap [2][288]float32

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

			nch, nGranules, _ := grCountForHeader(hdr)

			var grBuf [4]grInfo
			mainDataBegin := l3ReadSideInfo(&bs, grBuf[:], hdr, len(bsData))

			if mainDataBegin >= 0 && !bs.Overrun() {
				mainBS, mainData, ok := l3RestoreReservoir(&res, &bs, bsData, mainDataBegin, maindata)
				if ok {
					for gr := range nGranules {
						ciBase := ci
						grbuf := decodeHuffmanGranule(t, fx, pos, gr, nch, hdr, grBuf[:], &istPos, &mainBS, huffRecs, ciBase)
						stereoReorderAntialiasGranule(t, fx, pos, gr, nch, hdr, grBuf[:], &istPos, &grbuf, preRecs, ciBase)
						imdctGranule(t, fx, pos, gr, nch, hdr, grBuf[:], &overlap, &grbuf, postRecs, overlapRecs, ciBase)

						ci = ciBase + nch
					}
				}
				l3SaveReservoir(&res, mainBS, mainData)
			}

			pos += off + fb
		}
		if ci != len(postRecs) || ci != len(overlapRecs) {
			t.Fatalf("%s: consumed %d granule-channels, want %d (grbuf_post_imdct) / %d (overlap)", fx, ci, len(postRecs), len(overlapRecs))
		}
	}
}
