package dec

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/enc"
	"github.com/tphakala/go-mp3/internal/testsignal"
)

// TestReorderShortInverse is the l3Reorder-inverse gate for
// enc.ReorderShort (internal/enc/reorder.go): reorderShort maps an
// MDCT-order short-block spectrum into coding order (the layout the
// bitstream carries), and this decoder's l3Reorder maps coding order into
// the window-interleave A1's TDAC test already proved end-to-end: grbuf
// [18b+3k+w] = src[b*18+6w+k] (b the subband 0..31, k the line within it
// 0..5, w the window 0..2). Running enc.ReorderShort's output through
// l3Reorder with a grInfo set the way l3ReadSideInfo sets it for a short,
// non-mixed granule (sfbTab = scfShortTable[srIdx], nLongSfb = 0,
// nShortSfb = 39) must reproduce that identity for every line, at each of
// the three MPEG-1 rates.
func TestReorderShortInverse(t *testing.T) {
	decRows := [3]int{5, 6, 7}
	for rate, decRow := range decRows {
		widths := enc.SfbWidthsShortRow(rate)

		var src, dst [576]float64
		seed := uint64(rate)*2 + 1
		for i := range src {
			src[i] = testsignal.LCGSigned(&seed)
		}
		enc.ReorderShort(&src, &dst, &widths)

		var grbuf [576]float32
		for i := range grbuf {
			grbuf[i] = float32(dst[i])
		}

		gi := grInfo{
			sfbTab:    scfShortTable[decRow][:],
			nLongSfb:  0,
			nShortSfb: 39,
		}
		var scratch [576]float32
		l3Reorder(grbuf[:], scratch[:], &gi)

		for b := range 32 {
			for k := range 6 {
				for w := range 3 {
					got := grbuf[18*b+3*k+w]
					want := float32(src[b*18+6*w+k])
					if got != want {
						t.Fatalf("rate %d: grbuf[%d] = %v, want %v (b=%d k=%d w=%d)",
							rate, 18*b+3*k+w, got, want, b, k, w)
					}
				}
			}
		}
	}
}
