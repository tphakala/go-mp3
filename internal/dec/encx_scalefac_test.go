package dec

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/enc"
)

// TestEncPretabMatchesDec: the encoder's preemphasis table (ISO 2.4.3.4.5,
// transcribed) must equal the decoder's independently derived preampTable
// (minimp3 g_preamp, CC0) on the bands that carry preemphasis (11..20).
func TestEncPretabMatchesDec(t *testing.T) {
	pre := enc.PretabLongPin()
	for i := range 11 {
		if pre[i] != 0 {
			t.Errorf("pretab[%d] = %d, want 0", i, pre[i])
		}
	}
	for i := range 10 {
		if pre[11+i] != int(preampTable[i]) {
			t.Errorf("pretab[%d] = %d, dec preampTable[%d] = %d",
				11+i, pre[11+i], i, preampTable[i])
		}
	}
}
