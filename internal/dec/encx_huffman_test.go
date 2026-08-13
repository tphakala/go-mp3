package dec

import (
	"fmt"
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/bits"
	"github.com/tphakala/go-mp3/internal/enc"
)

// TestEncSfbWidthsMatchDec is a white-box test (package dec, not dec_test):
// it imports internal/enc, the sanctioned exception to "enc must never
// import dec" for _test.go files only (see PROVENANCE.md and
// internal/enc/doc.go).
//
// It confirms enc.SfbWidthsLongRow(rate)'s three MPEG-1 long-block sfb
// width rows equal the decoder's scfLongTable rows for the same three
// rates. hdrGetMySampleRate (internal/dec/sideinfo.go) yields 6/7/8 for
// MPEG-1 44.1/48/32 kHz, and l3ReadSideInfo subtracts 1 before indexing
// scfLongTable, landing on rows 5/6/7.
func TestEncSfbWidthsMatchDec(t *testing.T) {
	decRows := [3]int{5, 6, 7}
	for rate, decRow := range decRows {
		got := enc.SfbWidthsLongRow(rate)
		want := scfLongTable[decRow]
		for i := range 22 {
			if got[i] != int(want[i]) {
				t.Fatalf("rate %d: sfbWidthsLong[%d] = %d, want scfLongTable[%d][%d] = %d",
					rate, i, got[i], decRow, i, want[i])
			}
		}
	}
}

// TestEncLinbitsMatchDec confirms every enc.BigTableLinbits(t) equals the
// decoder's linbitsTable[t], for all 32 table numbers (including the
// invalid slots 4 and 14, where both sides are 0).
func TestEncLinbitsMatchDec(t *testing.T) {
	for tnum := range 32 {
		got := enc.BigTableLinbits(tnum)
		want := int(linbitsTable[tnum])
		if got != want {
			t.Fatalf("table %d: linbits = %d, want %d", tnum, got, want)
		}
	}
}

// validEncBigTables lists every real (non-alias, non-invalid) big-values
// codebook number the round-trip gate must cover: tables 1-13, 15-31.
var validEncBigTables = []int{
	1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12, 13, 15,
	16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
}

// recoverIx recovers a Huffman-decoded dequantized sample's original
// signed quantized index: m = nint(|v|^(3/4)), the exact integer inverse
// of l3Pow43/pow43Table's x -> x^(4/3) for x in [0, 8206] representable
// in float32 (the largest magnitude any table's linbits=13 escape can
// produce; see enc.maxQuant). scf is pinned to 1.0 everywhere by this
// gate's callers, so v IS the dequantized magnitude with no additional
// scale factor to divide out.
func recoverIx(v float32) int32 {
	if v == 0 {
		return 0
	}
	mag := int32(math.Round(math.Pow(math.Abs(float64(v)), 0.75)))
	if v < 0 {
		return -mag
	}
	return mag
}

// buildBigDomainPairs lays out every (x, y) pair in table tnum's full
// domain (magnitude = index, alternating sign so both signs of every
// nonzero magnitude get exercised), plus, for escape tables (linbits>0),
// extra pairs at escape magnitudes {15, 16, 100, the largest this
// table's linbits can reach} (deduplicated, clamped to the table's real
// range) per the task-5 brief's required coverage.
func buildBigDomainPairs(tnum int) [][2]int32 {
	dim := enc.BigTableDim(tnum)
	linbits := enc.BigTableLinbits(tnum)

	sign := int32(1)
	nextSign := func() int32 {
		s := sign
		sign = -sign
		return s
	}

	var pairs [][2]int32
	for x := range dim {
		for y := range dim {
			xv, yv := int32(x), int32(y)
			if xv != 0 {
				xv *= nextSign()
			}
			if yv != 0 {
				yv *= nextSign()
			}
			pairs = append(pairs, [2]int32{xv, yv})
		}
	}

	if linbits > 0 {
		maxDirect := int32(dim - 1)
		maxEsc := maxDirect + (int32(1)<<uint(linbits) - 1)
		seen := map[int32]bool{}
		for _, m := range []int32{15, 16, 100, maxEsc} {
			if m < maxDirect || m > maxEsc || seen[m] {
				continue
			}
			seen[m] = true
			pairs = append(pairs, [2]int32{m * nextSign(), m * nextSign()})
		}
	}

	return pairs
}

// runBigTableRoundTrip encodes buildBigDomainPairs(tnum)'s pairs with
// table tnum pinned across the whole (single-region) big-values walk,
// decodes with the decoder's independent ISO tables, and requires every
// recovered magnitude and sign to match what was encoded.
func runBigTableRoundTrip(t *testing.T, tnum int) {
	t.Helper()
	pairs := buildBigDomainPairs(tnum)
	if len(pairs) > 288 {
		t.Fatalf("table %d: %d pairs exceeds a granule's 288-pair big-values max", tnum, len(pairs))
	}

	var ix [576]int32
	for i, p := range pairs {
		ix[2*i], ix[2*i+1] = p[0], p[1]
	}
	bigValues := len(pairs)

	sfbWidths := enc.SfbWidthsLongRow(1) // 48 kHz, matches scfLongTable[6] pinned below
	var buf []byte
	w := bits.NewWriter(buf)
	bitsWritten := enc.EncodeHuffmanPin(&w, &ix, bigValues, 0, uint8(tnum), 0, sfbWidths)
	buf = w.Flush()
	// l3Huffman's count1 walk always attempts one more codeword peek past
	// the granule's declared length before its `bs.Pos() > layer3gr`
	// check catches the overshoot (internal/dec/huffman.go:251-258); pad
	// generously so that peek (up to a two-level table lookup, at most a
	// few bits past bitsWritten) never touches bits.Reader's true limit
	// and spuriously latches Overrun, mirroring how a real bitstream
	// always has further frame bytes beyond one granule's true end.
	buf = append(buf, make([]byte, 8)...)

	br := bits.NewReader(buf)
	gi := grInfo{
		sfbTab:      scfLongTable[6][:],
		nLongSfb:    22,
		bigValues:   uint16(bigValues),
		tableSelect: [3]uint8{uint8(tnum), uint8(tnum), uint8(tnum)},
		regionCount: [3]uint8{21, 0, 0}, // covers all 22 real sfb's in region 0; table is pinned everywhere anyway
		count1Table: 0,
	}
	var scf [40]float32
	for i := range scf {
		scf[i] = 1
	}
	var dst [576]float32
	l3Huffman(dst[:], &br, &gi, scf[:], bitsWritten)

	if br.Overrun() {
		t.Fatalf("table %d: decode overran the padded buffer", tnum)
	}
	if br.Pos() != bitsWritten {
		t.Fatalf("table %d: bits.Reader.Pos() = %d after the walk, want %d (ri.bits)", tnum, br.Pos(), bitsWritten)
	}
	for i, p := range pairs {
		gotX, gotY := recoverIx(dst[2*i]), recoverIx(dst[2*i+1])
		if gotX != p[0] || gotY != p[1] {
			t.Fatalf("table %d pair %d: decoded (%d,%d), want (%d,%d)", tnum, i, gotX, gotY, p[0], p[1])
		}
	}
}

// runCount1RoundTrip encodes all 16 count1 presence patterns (idx =
// v<<3|w<<2|x<<1|y, alternating sign on every nonzero element) under
// count1Table (0 = A, 1 = B), decodes, and requires every recovered
// value to match.
func runCount1RoundTrip(t *testing.T, count1Table uint8, name string) {
	t.Helper()
	var ix [576]int32
	sign := int32(1)
	for idx := range 16 {
		base := 4 * idx
		bits4 := [4]int32{0, 0, 0, 0}
		if idx&8 != 0 {
			bits4[0] = 1
		}
		if idx&4 != 0 {
			bits4[1] = 1
		}
		if idx&2 != 0 {
			bits4[2] = 1
		}
		if idx&1 != 0 {
			bits4[3] = 1
		}
		for j, v := range bits4 {
			if v != 0 {
				ix[base+j] = v * sign
				sign = -sign
			}
		}
	}

	sfbWidths := enc.SfbWidthsLongRow(1)
	var buf []byte
	w := bits.NewWriter(buf)
	bitsWritten := enc.EncodeHuffmanPin(&w, &ix, 0, 16, 0, count1Table, sfbWidths)
	buf = w.Flush()
	buf = append(buf, make([]byte, 8)...) // see runBigTableRoundTrip's identical padding comment

	br := bits.NewReader(buf)
	gi := grInfo{
		sfbTab:      scfLongTable[6][:],
		nLongSfb:    22,
		bigValues:   0,
		count1Table: count1Table,
	}
	var scf [40]float32
	for i := range scf {
		scf[i] = 1
	}
	var dst [576]float32
	l3Huffman(dst[:], &br, &gi, scf[:], bitsWritten)

	if br.Overrun() {
		t.Fatalf("%s: decode overran the padded buffer", name)
	}
	if br.Pos() != bitsWritten {
		t.Fatalf("%s: bits.Reader.Pos() = %d after the walk, want %d (ri.bits)", name, br.Pos(), bitsWritten)
	}
	for i := range 64 {
		if got := recoverIx(dst[i]); got != ix[i] {
			t.Fatalf("%s: dst[%d] = %d, want %d", name, i, got, ix[i])
		}
	}
}

// TestEncHuffmanDecRoundTrip is the arbiter of this package's Huffman
// codeword transcription and writeSpectrum's bit field order: it encodes
// with internal/enc's real writeSpectrum (via the EncodeHuffmanPin test
// shim) and decodes with this package's independent, oracle-verified ISO
// tables (l3Huffman). Every valid big-values table (1-13, 15-31) and
// both count1 tables (A, B) must round-trip every codeword exactly.
func TestEncHuffmanDecRoundTrip(t *testing.T) {
	for _, tnum := range validEncBigTables {
		t.Run(fmt.Sprintf("table%d", tnum), func(t *testing.T) {
			runBigTableRoundTrip(t, tnum)
		})
	}
	t.Run("count1A", func(t *testing.T) { runCount1RoundTrip(t, 0, "count1A") })
	t.Run("count1B", func(t *testing.T) { runCount1RoundTrip(t, 1, "count1B") })
}
