package dec

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
)

// TestHdrGetMySampleRate is a small hand-computed unit test driving the
// helper l3ReadSideInfo depends on for its scalefactor-band table row
// selection, verified against tools/oracle/minimp3.h:76's bit layout.
func TestHdrGetMySampleRate(t *testing.T) {
	tests := []struct {
		name string
		h    []byte
		want int
	}{
		// MPEG-1 (bit3=1,bit4=1): HDR_GET_SAMPLE_RATE=0 (44100) + (1+1)*3 = 6.
		{"MPEG-1 44100/128", mpeg1L3_44100_128, 6},
		// MPEG-2 (bit3=0,bit4=1): HDR_GET_SAMPLE_RATE=1 (48000 row, halved
		// to 24000Hz by hdr_sample_rate_hz's MPEG1 shift) + (0+1)*3 = 4.
		{"MPEG-2 24000/32", mpeg2L3_24000_32, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hdrGetMySampleRate(tt.h); got != tt.want {
				t.Errorf("hdrGetMySampleRate(%08b) = %d, want %d", tt.h, got, tt.want)
			}
		})
	}
}

// grCountForHeader mirrors l3ReadSideInfo's own grCount derivation
// (mono/stereo times MPEG1/MPEG2), used by the differential tests here and
// in scalefactors_test.go to size and slice grInfo buffers without
// depending on the oracle's own "sideinfo" dump for that count.
func grCountForHeader(hdr []byte) (nch, nGranules, grCount int) {
	nch = 2
	if hdrIsMono(hdr) {
		nch = 1
	}
	nGranules = 1
	if hdrTestMPEG1(hdr) {
		nGranules = 2
	}
	return nch, nGranules, nch * nGranules
}

// sideInfoFields packs one granule-channel's parsed integer fields in the
// exact order the "sideinfo" oracle hook dumps them (see
// tools/oracle/hooks.patch).
func sideInfoFields(gi *grInfo) [21]int32 {
	return [21]int32{
		int32(gi.part23Length), int32(gi.bigValues), int32(gi.scalefacCompress),
		int32(gi.globalGain), int32(gi.blockType), int32(gi.mixedBlockFlag),
		int32(gi.nLongSfb), int32(gi.nShortSfb),
		int32(gi.tableSelect[0]), int32(gi.tableSelect[1]), int32(gi.tableSelect[2]),
		int32(gi.regionCount[0]), int32(gi.regionCount[1]), int32(gi.regionCount[2]),
		int32(gi.subblockGain[0]), int32(gi.subblockGain[1]), int32(gi.subblockGain[2]),
		int32(gi.preflag), int32(gi.scalefacScale), int32(gi.count1Table), int32(gi.scfsi),
	}
}

// sideInfoFieldDefined reports whether field index i of sideInfoFields is
// meaningfully set by upstream L3_read_side_info for a granule-channel
// with this blockType, i.e. whether it is safe to compare against the
// oracle's dump at all. Index 13 (region_count[2]) is set only in the
// non-window-switched branch (tools/oracle/minimp3.h:597: block_type==0);
// indices 14-16 (subblock_gain[0..2]) are set only in the window-switched
// branch (block_type!=0). Whichever branch does NOT run leaves the field
// as whatever was already in mp3dec_scratch_t.gr_info, which the oracle
// harness never initializes (a fresh, uninitialized stack struct on every
// mp3dec_decode_frame call) -- confirmed empirically: the oracle's own
// dump shows a live, non-255 region_count[2] on window-switched granules.
// That is stack garbage, not a value either implementation can reproduce
// or is meant to, so it is excluded from the gate rather than compared.
func sideInfoFieldDefined(i int, blockType uint8) bool {
	switch i {
	case 13:
		return blockType == 0
	case 14, 15, 16:
		return blockType != 0
	default:
		return true
	}
}

// TestL3ReadSideInfoMatchesOracle is the differential test: it walks every
// replayFixtures fixture frame by frame (using Task 5's findFrame), reads
// side info from each frame's post-header bytes, and compares main_data_begin
// plus every granule-channel's fields (see sideInfoFields) against the
// "sideinfo" stage of the oracle's dump. Bounded by the pre-existing
// "frames" dump (Task 5) so a mismatch in frame count is caught rather than
// silently under/over-iterating.
func TestL3ReadSideInfoMatchesOracle(t *testing.T) {
	for _, fx := range replayFixtures(t) {
		frameRecs := readDump(t, fx, "frames")
		sideRecs := readDump(t, fx, "sideinfo")
		data := readFile(t, fx)

		pos, free := 0, 0
		si := 0
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

			_, _, grCount := grCountForHeader(hdr)

			var grBuf [4]grInfo
			got := l3ReadSideInfo(&bs, grBuf[:], hdr, len(bsData))
			if got < 0 {
				// Mirrors upstream: L3_read_side_info's two early -1
				// returns skip the dump hook entirely, so there is no
				// oracle record to compare against for this frame.
				pos += off + fb
				continue
			}

			if si >= len(sideRecs) {
				t.Fatalf("%s: frame at %d: ran out of sideinfo records", fx, pos)
			}
			hdrRec := sideRecs[si]
			si++
			if got != int(hdrRec.I32[0]) || grCount != int(hdrRec.I32[1]) {
				t.Fatalf("%s: frame at %d main_data_begin/gr_count = %d/%d, want %d/%d",
					fx, pos, got, grCount, hdrRec.I32[0], hdrRec.I32[1])
			}

			for g := 0; g < grCount; g++ {
				if si >= len(sideRecs) {
					t.Fatalf("%s: frame at %d granule-ch %d: ran out of sideinfo records", fx, pos, g)
				}
				rec := sideRecs[si]
				si++
				gotFields := sideInfoFields(&grBuf[g])
				for i, want := range rec.I32 {
					if !sideInfoFieldDefined(i, grBuf[g].blockType) {
						continue
					}
					if gotFields[i] != want {
						t.Fatalf("%s: frame at %d granule-ch %d field %d = %d, want %d",
							fx, pos, g, i, gotFields[i], want)
					}
				}
			}

			pos += off + fb
		}
		if si != len(sideRecs) {
			t.Fatalf("%s: consumed %d of %d sideinfo records", fx, si, len(sideRecs))
		}
	}
}
