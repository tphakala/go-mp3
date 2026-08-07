package dec

import "github.com/tphakala/go-mp3/internal/bits"

// scfPartitionsTable mirrors g_scf_partitions (tools/oracle/minimp3.h:656-660).
var scfPartitionsTable = [3][28]uint8{
	{6, 5, 5, 5, 6, 5, 5, 5, 6, 5, 7, 3, 11, 10, 0, 0, 7, 7, 7, 0, 6, 6, 6, 3, 8, 8, 5, 0},
	{8, 9, 6, 12, 6, 9, 9, 9, 6, 9, 12, 6, 15, 18, 0, 0, 6, 15, 12, 0, 6, 12, 9, 6, 6, 18, 9, 0},
	{9, 9, 6, 12, 9, 9, 9, 9, 9, 9, 12, 6, 18, 18, 0, 0, 12, 12, 12, 0, 12, 9, 9, 6, 15, 12, 9, 0},
}

// scfModTable mirrors g_mod (tools/oracle/minimp3.h:674).
var scfModTable = [24]uint8{5, 5, 4, 4, 5, 5, 4, 1, 4, 3, 1, 1, 5, 6, 6, 1, 4, 4, 4, 1, 4, 3, 1, 1}

// preampTable mirrors g_preamp (tools/oracle/minimp3.h:701).
var preampTable = [10]uint8{1, 1, 1, 1, 2, 2, 3, 3, 3, 2}

// scfcDecodeTable mirrors g_scfc_decode (tools/oracle/minimp3.h:668).
var scfcDecodeTable = [16]uint8{0, 1, 2, 3, 12, 5, 6, 7, 9, 10, 11, 13, 14, 15, 18, 19}

// expfracTable mirrors g_expfrac (tools/oracle/minimp3.h:644).
var expfracTable = [4]float32{9.31322575e-10, 7.83145814e-10, 6.58544508e-10, 5.53767716e-10}

// bitsDequantizerOut, maxScf and maxScfi mirror upstream's
// BITS_DEQUANTIZER_OUT, MAX_SCF and MAX_SCFI (tools/oracle/minimp3.h:80-82).
const (
	bitsDequantizerOut = -1
	maxScf             = 255 + bitsDequantizerOut*4 - 210
	maxScfi            = (maxScf + 3) &^ 3
)

// hdrTestIStereo mirrors upstream HDR_TEST_I_STEREO
// (tools/oracle/minimp3.h:69). See hdrIsMono (sideinfo.go) for why it
// lives here instead of header.go.
func hdrTestIStereo(hdr []byte) bool { return hdr[3]&0x10 != 0 }

// hdrIsMsStereo mirrors upstream HDR_IS_MS_STEREO
// (tools/oracle/minimp3.h:63).
func hdrIsMsStereo(hdr []byte) bool { return hdr[3]&0xE0 == 0x60 }

// l3LdexpQ2 mirrors upstream L3_ldexp_q2 (tools/oracle/minimp3.h:642-652).
// The int shift result converts to float32 at the exact point upstream's
// usual-arithmetic-conversion rules would (no double promotion: every
// g_expfrac entry is an f-suffixed float32 literal, so the int operand
// converts directly to float32 to match it), and this is a chain of two
// multiplications (a*(b*c)), not a multiply-add, so there is no FMA-fusion
// site to block.
func l3LdexpQ2(y float32, expQ2 int) float32 {
	for {
		e := min(120, expQ2)
		shifted := int32(1) << 30 >> uint(e>>2)
		y *= expfracTable[e&3] * float32(shifted)
		expQ2 -= e
		if expQ2 <= 0 {
			break
		}
	}
	return y
}

// l3ReadScalefactorsRaw mirrors upstream L3_read_scalefactors
// (tools/oracle/minimp3.h:609-640), reading the raw (pre-ldexp) integer
// scalefactors into iscf. Upstream names this function's output parameter
// "scf" too (it is called with the local iscf[40] array from
// L3_decode_scalefactors, not the final float scf array); this port calls
// it iscf throughout to keep the two arrays textually distinct.
func l3ReadScalefactorsRaw(iscf, istPos []uint8, scfSize, scfCount []uint8, bitbuf *bits.Reader, scfsi int) {
	pos := 0
	for i := 0; i < 4 && scfCount[i] != 0; i, scfsi = i+1, scfsi*2 {
		cnt := int(scfCount[i])
		if scfsi&8 != 0 {
			copy(iscf[pos:pos+cnt], istPos[pos:pos+cnt])
		} else {
			bitsN := int(scfSize[i])
			if bitsN == 0 {
				for k := 0; k < cnt; k++ {
					iscf[pos+k] = 0
					istPos[pos+k] = 0
				}
			} else {
				maxScf := -1
				if scfsi < 0 {
					maxScf = (1 << bitsN) - 1
				}
				for k := 0; k < cnt; k++ {
					s := int(bitbuf.Bits(bitsN))
					if s == maxScf {
						istPos[pos+k] = 0xFF
					} else {
						istPos[pos+k] = uint8(s)
					}
					iscf[pos+k] = uint8(s)
				}
			}
		}
		pos += cnt
	}
	iscf[pos], iscf[pos+1], iscf[pos+2] = 0, 0, 0
}

// l3ReadScalefactors mirrors upstream L3_decode_scalefactors
// (tools/oracle/minimp3.h:654-714): it computes the raw integer
// scalefactors via l3ReadScalefactorsRaw, then floats them via l3LdexpQ2
// into scf[0 : gr.nLongSfb+gr.nShortSfb] (scf's remaining entries, up to
// index 39, are left untouched, exactly mirroring upstream leaving the
// tail of mp3dec_scratch_t.scf's 40 floats unwritten; see
// task-6-report.md for why the differential test only compares that
// prefix).
//
// hdr is not part of the brief's originally sketched signature
// (scf, istPos, gr, bs, ch); it is required because upstream reads
// HDR_TEST_MPEG1, HDR_TEST_I_STEREO and HDR_IS_MS_STEREO from it, so it
// is added as the first parameter (matching L3_decode_scalefactors's own
// leading hdr parameter); see task-6-report.md.
func l3ReadScalefactors(hdr []byte, scf []float32, istPos []uint8, gr *grInfo, bs *bits.Reader, ch int) {
	row := 0
	if gr.nShortSfb != 0 {
		row++
	}
	if gr.nLongSfb == 0 {
		row++
	}
	scfPartition := scfPartitionsTable[row][:]

	var scfSize [4]uint8
	var iscf [40]uint8
	scfShift := int(gr.scalefacScale) + 1
	scfsi := int(gr.scfsi)

	if hdrTestMPEG1(hdr) {
		part := int(scfcDecodeTable[gr.scalefacCompress])
		scfSize[0] = uint8(part >> 2)
		scfSize[1] = scfSize[0]
		scfSize[2] = uint8(part & 3)
		scfSize[3] = scfSize[2]
	} else {
		ist := 0
		if hdrTestIStereo(hdr) && ch != 0 {
			ist = 1
		}
		sfc := int(gr.scalefacCompress) >> ist
		k := ist * 3 * 4
		for sfc >= 0 {
			modprod := 1
			for i := 3; i >= 0; i-- {
				scfSize[i] = uint8(sfc / modprod % int(scfModTable[k+i]))
				modprod *= int(scfModTable[k+i])
			}
			sfc -= modprod
			k += 4
		}
		scfPartition = scfPartition[k:]
		scfsi = -16
	}
	l3ReadScalefactorsRaw(iscf[:], istPos, scfSize[:], scfPartition, bs, scfsi)

	if gr.nShortSfb != 0 {
		sh := uint(3 - scfShift)
		for i := 0; i < int(gr.nShortSfb); i += 3 {
			base := int(gr.nLongSfb) + i
			iscf[base+0] += gr.subblockGain[0] << sh
			iscf[base+1] += gr.subblockGain[1] << sh
			iscf[base+2] += gr.subblockGain[2] << sh
		}
	} else if gr.preflag != 0 {
		for i := 0; i < 10; i++ {
			iscf[11+i] += preampTable[i]
		}
	}

	msAdj := 0
	if hdrIsMsStereo(hdr) {
		msAdj = 2
	}
	gainExp := int(gr.globalGain) + bitsDequantizerOut*4 - 210 - msAdj
	gain := l3LdexpQ2(float32(1<<(maxScfi/4)), maxScfi-gainExp)
	n := int(gr.nLongSfb) + int(gr.nShortSfb)
	for i := 0; i < n; i++ {
		scf[i] = l3LdexpQ2(gain, int(iscf[i])<<scfShift)
	}
}
