package dec

import "github.com/tphakala/go-mp3/internal/bits"

// scfLongTable mirrors g_scf_long (tools/oracle/minimp3.h:486-495):
// long-block scalefactor band-width tables, one row per
// hdrGetMySampleRate index.
var scfLongTable = [8][23]uint8{
	{6, 6, 6, 6, 6, 6, 8, 10, 12, 14, 16, 20, 24, 28, 32, 38, 46, 52, 60, 68, 58, 54, 0},
	{12, 12, 12, 12, 12, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64, 76, 90, 2, 2, 2, 2, 2, 0},
	{6, 6, 6, 6, 6, 6, 8, 10, 12, 14, 16, 20, 24, 28, 32, 38, 46, 52, 60, 68, 58, 54, 0},
	{6, 6, 6, 6, 6, 6, 8, 10, 12, 14, 16, 18, 22, 26, 32, 38, 46, 54, 62, 70, 76, 36, 0},
	{6, 6, 6, 6, 6, 6, 8, 10, 12, 14, 16, 20, 24, 28, 32, 38, 46, 52, 60, 68, 58, 54, 0},
	{4, 4, 4, 4, 4, 4, 6, 6, 8, 8, 10, 12, 16, 20, 24, 28, 34, 42, 50, 54, 76, 158, 0},
	{4, 4, 4, 4, 4, 4, 6, 6, 6, 8, 10, 12, 16, 18, 22, 28, 34, 40, 46, 54, 54, 192, 0},
	{4, 4, 4, 4, 4, 4, 6, 6, 8, 10, 12, 16, 20, 24, 30, 38, 46, 56, 68, 84, 102, 26, 0},
}

// scfShortTable mirrors g_scf_short (tools/oracle/minimp3.h:496-505).
var scfShortTable = [8][40]uint8{
	{4, 4, 4, 4, 4, 4, 4, 4, 4, 6, 6, 6, 8, 8, 8, 10, 10, 10, 12, 12, 12, 14, 14, 14, 18, 18, 18, 24, 24, 24, 30, 30, 30, 40, 40, 40, 18, 18, 18, 0},
	{8, 8, 8, 8, 8, 8, 8, 8, 8, 12, 12, 12, 16, 16, 16, 20, 20, 20, 24, 24, 24, 28, 28, 28, 36, 36, 36, 2, 2, 2, 2, 2, 2, 2, 2, 2, 26, 26, 26, 0},
	{4, 4, 4, 4, 4, 4, 4, 4, 4, 6, 6, 6, 6, 6, 6, 8, 8, 8, 10, 10, 10, 14, 14, 14, 18, 18, 18, 26, 26, 26, 32, 32, 32, 42, 42, 42, 18, 18, 18, 0},
	{4, 4, 4, 4, 4, 4, 4, 4, 4, 6, 6, 6, 8, 8, 8, 10, 10, 10, 12, 12, 12, 14, 14, 14, 18, 18, 18, 24, 24, 24, 32, 32, 32, 44, 44, 44, 12, 12, 12, 0},
	{4, 4, 4, 4, 4, 4, 4, 4, 4, 6, 6, 6, 8, 8, 8, 10, 10, 10, 12, 12, 12, 14, 14, 14, 18, 18, 18, 24, 24, 24, 30, 30, 30, 40, 40, 40, 18, 18, 18, 0},
	{4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 6, 6, 6, 8, 8, 8, 10, 10, 10, 12, 12, 12, 14, 14, 14, 18, 18, 18, 22, 22, 22, 30, 30, 30, 56, 56, 56, 0},
	{4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 6, 6, 6, 6, 6, 6, 10, 10, 10, 12, 12, 12, 14, 14, 14, 16, 16, 16, 20, 20, 20, 26, 26, 26, 66, 66, 66, 0},
	{4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 6, 6, 6, 8, 8, 8, 12, 12, 12, 16, 16, 16, 20, 20, 20, 26, 26, 26, 34, 34, 34, 42, 42, 42, 12, 12, 12, 0},
}

// scfMixedTable mirrors g_scf_mixed (tools/oracle/minimp3.h:506-515).
var scfMixedTable = [8][40]uint8{
	{6, 6, 6, 6, 6, 6, 6, 6, 6, 8, 8, 8, 10, 10, 10, 12, 12, 12, 14, 14, 14, 18, 18, 18, 24, 24, 24, 30, 30, 30, 40, 40, 40, 18, 18, 18, 0},
	{12, 12, 12, 4, 4, 4, 8, 8, 8, 12, 12, 12, 16, 16, 16, 20, 20, 20, 24, 24, 24, 28, 28, 28, 36, 36, 36, 2, 2, 2, 2, 2, 2, 2, 2, 2, 26, 26, 26, 0},
	{6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 8, 8, 8, 10, 10, 10, 14, 14, 14, 18, 18, 18, 26, 26, 26, 32, 32, 32, 42, 42, 42, 18, 18, 18, 0},
	{6, 6, 6, 6, 6, 6, 6, 6, 6, 8, 8, 8, 10, 10, 10, 12, 12, 12, 14, 14, 14, 18, 18, 18, 24, 24, 24, 32, 32, 32, 44, 44, 44, 12, 12, 12, 0},
	{6, 6, 6, 6, 6, 6, 6, 6, 6, 8, 8, 8, 10, 10, 10, 12, 12, 12, 14, 14, 14, 18, 18, 18, 24, 24, 24, 30, 30, 30, 40, 40, 40, 18, 18, 18, 0},
	{4, 4, 4, 4, 4, 4, 6, 6, 4, 4, 4, 6, 6, 6, 8, 8, 8, 10, 10, 10, 12, 12, 12, 14, 14, 14, 18, 18, 18, 22, 22, 22, 30, 30, 30, 56, 56, 56, 0},
	{4, 4, 4, 4, 4, 4, 6, 6, 4, 4, 4, 6, 6, 6, 6, 6, 6, 10, 10, 10, 12, 12, 12, 14, 14, 14, 16, 16, 16, 20, 20, 20, 26, 26, 26, 66, 66, 66, 0},
	{4, 4, 4, 4, 4, 4, 6, 6, 4, 4, 4, 6, 6, 6, 8, 8, 8, 12, 12, 12, 16, 16, 16, 20, 20, 20, 26, 26, 26, 34, 34, 34, 42, 42, 42, 12, 12, 12, 0},
}

// hdrIsMono mirrors upstream HDR_IS_MONO (tools/oracle/minimp3.h:62).
// Defined here rather than header.go because this task's commit is scoped
// to types.go/sideinfo.go/scalefactors.go; header.go is unmodified.
func hdrIsMono(hdr []byte) bool { return hdr[3]&0xC0 == 0xC0 }

// hdrGetMySampleRate mirrors upstream HDR_GET_MY_SAMPLE_RATE
// (tools/oracle/minimp3.h:76). See hdrIsMono for why it lives here instead
// of header.go.
func hdrGetMySampleRate(hdr []byte) int {
	return hdrGetSampleRate(hdr) + (int((hdr[1]>>3)&1)+int((hdr[1]>>4)&1))*3
}

// l3ReadSideInfo mirrors upstream L3_read_side_info
// (tools/oracle/minimp3.h:484-614): parses the per-granule-channel side
// info directly following the header (and optional CRC, already skipped
// by the caller) into gr[0:grCount], where
// grCount = (mono?1:2)*(MPEG1?2:1). Returns main_data_begin, or -1 on
// malformed side info (mirrors upstream's two early -1 returns).
//
// frameBytes is an addition to the brief's sketched signature
// (bs, gr, hdr): upstream's final validation
// (part_23_sum+bs->pos > bs->limit+main_data_begin*8) needs bs->limit,
// which bits.Reader does not expose (and this task's commit is not
// scoped to internal/bits/reader.go). frameBytes is len() of the byte
// slice bs was constructed over (bs_frame->buf's byte count upstream),
// giving limitBits = 8*frameBytes; see task-6-report.md.
func l3ReadSideInfo(bs *bits.Reader, gr []grInfo, hdr []byte, frameBytes int) int {
	var scfsi uint32
	var mainDataBegin int
	partSum := 0
	srIdx := hdrGetMySampleRate(hdr)
	if srIdx != 0 {
		srIdx--
	}
	grCount := 1
	if !hdrIsMono(hdr) {
		grCount = 2
	}

	if hdrTestMPEG1(hdr) {
		grCount *= 2
		mainDataBegin = int(bs.Bits(9))
		scfsi = bs.Bits(7 + grCount)
	} else {
		mainDataBegin = int(bs.Bits(8+grCount)) >> grCount
	}

	idx := 0
	for {
		g := &gr[idx]
		if hdrIsMono(hdr) {
			scfsi <<= 4
		}
		g.part23Length = uint16(bs.Bits(12))
		partSum += int(g.part23Length)
		g.bigValues = uint16(bs.Bits(9))
		if g.bigValues > 288 {
			return -1
		}
		g.globalGain = uint8(bs.Bits(8))
		if hdrTestMPEG1(hdr) {
			g.scalefacCompress = uint16(bs.Bits(4))
		} else {
			g.scalefacCompress = uint16(bs.Bits(9))
		}
		g.sfbTab = scfLongTable[srIdx][:]
		g.nLongSfb = 22
		g.nShortSfb = 0

		var tables uint32
		if bs.Bits(1) != 0 {
			g.blockType = uint8(bs.Bits(2))
			if g.blockType == 0 {
				return -1
			}
			g.mixedBlockFlag = uint8(bs.Bits(1))
			g.regionCount[0] = 7
			g.regionCount[1] = 255
			if g.blockType == shortBlockType {
				scfsi &= 0x0F0F
				if g.mixedBlockFlag == 0 {
					g.regionCount[0] = 8
					g.sfbTab = scfShortTable[srIdx][:]
					g.nLongSfb = 0
					g.nShortSfb = 39
				} else {
					g.sfbTab = scfMixedTable[srIdx][:]
					if hdrTestMPEG1(hdr) {
						g.nLongSfb = 8
					} else {
						g.nLongSfb = 6
					}
					g.nShortSfb = 30
				}
			}
			tables = bs.Bits(10)
			tables <<= 5
			g.subblockGain[0] = uint8(bs.Bits(3))
			g.subblockGain[1] = uint8(bs.Bits(3))
			g.subblockGain[2] = uint8(bs.Bits(3))
		} else {
			g.blockType = 0
			g.mixedBlockFlag = 0
			tables = bs.Bits(15)
			g.regionCount[0] = uint8(bs.Bits(4))
			g.regionCount[1] = uint8(bs.Bits(3))
			g.regionCount[2] = 255
		}
		g.tableSelect[0] = uint8(tables >> 10)
		g.tableSelect[1] = uint8((tables >> 5) & 31)
		g.tableSelect[2] = uint8(tables & 31)
		switch {
		case hdrTestMPEG1(hdr):
			g.preflag = uint8(bs.Bits(1))
		case g.scalefacCompress >= 500:
			g.preflag = 1
		default:
			g.preflag = 0
		}
		g.scalefacScale = uint8(bs.Bits(1))
		g.count1Table = uint8(bs.Bits(1))
		g.scfsi = uint8((scfsi >> 12) & 15)
		scfsi <<= 4

		idx++
		grCount--
		if grCount == 0 {
			break
		}
	}

	if partSum+bs.Pos() > 8*frameBytes+mainDataBegin*8 {
		return -1
	}

	return mainDataBegin
}

// l3RestoreReservoir mirrors upstream L3_restore_reservoir
// (tools/oracle/minimp3.h:1228-1236). frameData is the byte slice bsFrame
// was constructed over (bs_frame->buf upstream: the current frame's bytes
// after the header and optional CRC); bsFrame's position must already be
// wherever l3ReadSideInfo left it (bs_frame->pos upstream). It assembles
// maindata (must be at least maxBitreservoirBytes+len(frameData) bytes,
// mirroring mp3dec_scratch_t.maindata) as reserv-tail || current-frame-tail
// and returns a fresh Reader over the used prefix, that same prefix (for
// l3SaveReservoir, which needs its length), and whether the reservoir held
// enough bytes for mainDataBegin (upstream's return value).
func l3RestoreReservoir(res *reservoir, bsFrame *bits.Reader, frameData []byte, mainDataBegin int, maindata []byte) (mainBS bits.Reader, mainData []byte, ok bool) {
	frameBytes := len(frameData) - bsFrame.Pos()/8
	bytesHave := min(res.n, mainDataBegin)
	copy(maindata, res.buf[max(0, res.n-mainDataBegin):res.n])
	copy(maindata[bytesHave:], frameData[bsFrame.Pos()/8:bsFrame.Pos()/8+frameBytes])
	mainData = maindata[:bytesHave+frameBytes]
	return bits.NewReader(mainData), mainData, res.n >= mainDataBegin
}

// l3SaveReservoir mirrors upstream L3_save_reservoir
// (tools/oracle/minimp3.h:1212-1226). mainData is the exact slice mainBS
// was constructed over (the mainData returned by l3RestoreReservoir, i.e.
// len(mainData) gives bs->limit/8 upstream); mainBS's position reflects
// everything consumed by this frame's scalefactor (and, once Task 7 ports
// it, Huffman) decode.
func l3SaveReservoir(res *reservoir, mainBS bits.Reader, mainData []byte) {
	pos := (mainBS.Pos() + 7) / 8
	remains := len(mainData) - pos
	if remains > maxBitreservoirBytes {
		pos += remains - maxBitreservoirBytes
		remains = maxBitreservoirBytes
	}
	if remains > 0 {
		copy(res.buf[:], mainData[pos:pos+remains])
	}
	res.n = remains
}
