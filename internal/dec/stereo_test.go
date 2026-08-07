package dec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
)

// TestStereoTablesMatchOracle asserts the two ported const tables (gPan,
// gAA, both in tables.go) are byte-identical to the pin's g_pan and g_aa.
// tools/oracle/tables.c appends these two tables (transcribed from
// tools/oracle/minimp3.h with line-number citations) after the tables
// Tasks 6/7 already checksum. This golden hash covers only the new
// trailing 120 bytes (56 for g_pan, 64 for g_aa), independent of the
// rest of tables.c's output, produced once with:
//
//	cc -O2 -o /tmp/mp3tables tools/oracle/tables.c && \
//	  /tmp/mp3tables | tail -c 120 | sha256sum
//
// Re-run that whenever gPan/gAA's literals change to refresh wantHex.
func TestStereoTablesMatchOracle(t *testing.T) {
	const wantHex = "f5023f80d212db3a66f40bb8a2ce5fe8aa36f9152ea8df80320e950c9823937b"

	h := sha256.New()
	var buf4 [4]byte
	for _, f := range gPan {
		binary.LittleEndian.PutUint32(buf4[:], math.Float32bits(f))
		h.Write(buf4[:])
	}
	for _, row := range gAA {
		for _, f := range row {
			binary.LittleEndian.PutUint32(buf4[:], math.Float32bits(f))
			h.Write(buf4[:])
		}
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		t.Fatalf("stereo table checksum = %s, want %s (see tools/oracle/tables.c)", got, wantHex)
	}
}

// compareGrbuf576 fails t if got and want disagree anywhere in their
// first 576 float32 bit patterns, identifying the mismatch by fixture,
// frame position, granule, channel and dump stage.
func compareGrbuf576(t *testing.T, fx string, pos, gr, ch int, stage string, got, want []float32) {
	t.Helper()
	for i := range 576 {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s: frame at %d granule %d ch %d %s[%d] = %08x, want %08x",
				fx, pos, gr, ch, stage, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

// decodeHuffmanGranule runs l3ReadScalefactors and l3Huffman for each of
// the granule's nch channels (mirroring upstream L3_decode's first
// per-channel loop, tools/oracle/minimp3.h:1242-1247), comparing each
// channel's decoded 576 floats against huffRecs[ciBase+ch], and returns
// the granule's channel-0/channel-1 buffers for the caller to run this
// task's stage against.
func decodeHuffmanGranule(t *testing.T, fx string, pos, gr, nch int, hdr []byte, grBuf []grInfo, istPos *[2][39]uint8, mainBS *bits.Reader, huffRecs []dumpRecord, ciBase int) [2][576]float32 {
	t.Helper()
	var grbuf [2][576]float32

	for ch := range nch {
		gi := &grBuf[gr*nch+ch]
		layer3grLimit := mainBS.Pos() + int(gi.part23Length)

		var scf [40]float32
		l3ReadScalefactors(hdr, scf[:], istPos[ch][:], gi, mainBS, ch)

		idx := ciBase + ch
		if idx >= len(huffRecs) {
			t.Fatalf("%s: frame at %d granule %d ch %d: ran out of grbuf_huff records", fx, pos, gr, ch)
		}

		l3Huffman(grbuf[ch][:], mainBS, gi, scf[:], layer3grLimit)
		compareGrbuf576(t, fx, pos, gr, ch, "grbuf_huff", grbuf[ch][:], huffRecs[idx].F32)
	}

	return grbuf
}

// stereoReorderAntialiasGranule applies this task's stage to one
// already-Huffman-decoded granule: l3StereoProcess once across both
// channels, then per channel l3Reorder (when gr.nShortSfb != 0) and
// l3Antialias, mirroring upstream L3_decode's stereo dispatch and second
// per-channel loop (tools/oracle/minimp3.h:1249-1271). Each channel's
// result is compared against preRecs[ciBase+ch] ("grbuf_pre_imdct").
func stereoReorderAntialiasGranule(t *testing.T, fx string, pos, gr, nch int, hdr []byte, grBuf []grInfo, istPos *[2][39]uint8, grbuf *[2][576]float32, preRecs []dumpRecord, ciBase int) {
	t.Helper()

	// grPair's second entry is only read by l3IntensityStereo when the
	// header claims I_STEREO, which no fixture's mono stream ever sets
	// (mode-extension bits are only meaningful for joint stereo); grBuf's
	// 4-entry capacity always covers gr*nch+2 for every grCount reachable
	// here (mono MPEG1's worst case, nGranules=2 nch=1, needs index 3).
	// This mirrors upstream's own harmless read of adjacent gr_info
	// storage in that same unreachable-for-mono combination.
	grPair := grBuf[gr*nch : gr*nch+2]
	l3StereoProcess(grbuf[0][:], grbuf[1][:], hdr, istPos[1][:], grPair)

	for ch := range nch {
		gi := &grBuf[gr*nch+ch]

		nLongBands := 0
		if gi.mixedBlockFlag != 0 {
			nLongBands = 2
		}
		if hdrGetMySampleRate(hdr) == 2 {
			nLongBands <<= 1
		}
		aaBands := 31
		if gi.nShortSfb != 0 {
			aaBands = nLongBands - 1
			var scratch [576]float32
			l3Reorder(grbuf[ch][nLongBands*18:], scratch[:], gi)
		}
		l3Antialias(grbuf[ch][:], aaBands)

		idx := ciBase + ch
		if idx >= len(preRecs) {
			t.Fatalf("%s: frame at %d granule %d ch %d: ran out of grbuf_pre_imdct records", fx, pos, gr, ch)
		}
		compareGrbuf576(t, fx, pos, gr, ch, "grbuf_pre_imdct", grbuf[ch][:], preRecs[idx].F32)
	}
}

// TestStereoReorderAntialiasMatchesOracle is the differential test: it
// walks every replayFixtures fixture frame by frame, threading a fresh
// reservoir per fixture through l3ReadSideInfo and l3RestoreReservoir
// exactly as the earlier tasks' tests do, then per granule runs
// decodeHuffmanGranule followed by stereoReorderAntialiasGranule,
// mirroring upstream L3_decode (tools/oracle/minimp3.h:1238-1272) in
// full: both channels' Huffman decode complete before stereo processing
// runs once per granule, then reorder/antialias run per channel. The
// result is compared against the "grbuf_pre_imdct" oracle dump stage
// (added this task, immediately before L3_imdct_gr) 576 floats at a
// time, by bit pattern.
func TestStereoReorderAntialiasMatchesOracle(t *testing.T) {
	for _, fx := range replayFixtures(t) {
		frameRecs := readDump(t, fx, "frames")
		huffRecs := readDump(t, fx, "grbuf_huff")
		preRecs := readDump(t, fx, "grbuf_pre_imdct")
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
						ci = ciBase + nch
					}
				}
				l3SaveReservoir(&res, mainBS, mainData)
			}

			pos += off + fb
		}
		if ci != len(preRecs) {
			t.Fatalf("%s: consumed %d of %d grbuf_pre_imdct records", fx, ci, len(preRecs))
		}
	}
}
