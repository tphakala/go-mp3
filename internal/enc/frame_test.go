package enc

import (
	"testing"

	"github.com/tphakala/go-mp3/internal/testsignal"
)

// TestFrameHeaderKnownBytes hand-computes the packed header bytes for a
// small grid of cases and checks frameHeader against them bit by bit, never
// against frameHeader's own output. Byte layout: byte0 = top 8 bits of
// sync (always 0xFF); byte1 = sync tail(3)=111, ID(2)=11, layer(2)=01,
// protection_bit(1)=1 (always 0xFB for this scope); byte2 =
// bitrate_index(4) | sampling_frequency(2) | padding_bit(1) |
// private_bit(1)=0; byte3 = mode(2) | mode_extension(2)=0 | copyright(1)=0 |
// original(1)=1 | emphasis(2)=0.
func TestFrameHeaderKnownBytes(t *testing.T) {
	cases := []struct {
		name                              string
		bitrateIndex, srIndex, padding, m int
		want                              [4]byte
	}{
		// 128 kbps (index 9) = 1001b -> byte2 = 1001_00_0_0 = 0x90;
		// stereo (mode 0) -> byte3 = 00_00_0_1_00 = 0x04.
		{"128kbps 44.1kHz stereo unpadded", 9, 0, 0, 0, [4]byte{0xFF, 0xFB, 0x90, 0x04}},
		// mono (mode 3) -> byte3 = 11_00_0_1_00 = 0xC4.
		{"128kbps 44.1kHz mono unpadded", 9, 0, 0, 3, [4]byte{0xFF, 0xFB, 0x90, 0xC4}},
		// srIndex 1 (48000) -> byte2 = 1001_01_0_0 = 0x94.
		{"128kbps 48kHz stereo unpadded", 9, 1, 0, 0, [4]byte{0xFF, 0xFB, 0x94, 0x04}},
		// srIndex 2 (32000) -> byte2 = 1001_10_0_0 = 0x98.
		{"128kbps 32kHz stereo unpadded", 9, 2, 0, 0, [4]byte{0xFF, 0xFB, 0x98, 0x04}},
		// padding=1 -> byte2 = 1001_00_1_0 = 0x92.
		{"128kbps 44.1kHz stereo padded", 9, 0, 1, 0, [4]byte{0xFF, 0xFB, 0x92, 0x04}},
		// bitrateIndex 1 (32kbps) = 0001b -> byte2 = 0001_00_0_0 = 0x10.
		{"32kbps (index 1) 44.1kHz stereo unpadded", 1, 0, 0, 0, [4]byte{0xFF, 0xFB, 0x10, 0x04}},
		// bitrateIndex 14 (320kbps) = 1110b -> byte2 = 1110_00_0_0 = 0xE0.
		{"320kbps (index 14) 44.1kHz stereo unpadded", 14, 0, 0, 0, [4]byte{0xFF, 0xFB, 0xE0, 0x04}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := frameHeader(c.bitrateIndex, c.srIndex, c.padding, c.m)
			if got != c.want {
				t.Fatalf("frameHeader(%d,%d,%d,%d) = % X, want % X",
					c.bitrateIndex, c.srIndex, c.padding, c.m, got, c.want)
			}
		})
	}
}

// TestPaddingAccumulator checks paddingState.next against an
// independently-derived closed form. With acc starting at 0, incrementing
// by a fixed residue r < sr each step and firing (subtracting sr) whenever
// acc >= sr is the standard rational-accumulator identity: after k steps,
// the number of fires equals floor(k*r/sr) and the cumulative base+padding
// byte total equals floor(k*144000*kbps/sr) exactly (144000*kbps =
// sr*base+r by definition of base and r, so k*base + floor(k*r/sr) =
// floor(k*(sr*base+r)/sr) = floor(k*144000*kbps/sr)).
func TestPaddingAccumulator(t *testing.T) {
	t.Run("44.1kHz 128kbps", func(t *testing.T) {
		const kbps = 128
		const sr = 44100
		const base = 417 // 144000*128/44100 floored, hand-computed: 44100*417=18,389,700
		// hand-computed: 144000*128 = 18,432,000; 18,432,000 - 44100*417
		// (18,389,700) = 42,300.
		const r = 42300

		var p paddingState
		gotPadded := 0
		cumBytes := 0
		for k := 1; k <= 1000; k++ {
			pad := p.next(kbps, sr)
			if pad != 0 && pad != 1 {
				t.Fatalf("frame %d: padding = %d, want 0 or 1", k, pad)
			}
			gotPadded += pad
			cumBytes += base + pad

			wantCum := (k * 144000 * kbps) / sr
			if cumBytes != wantCum {
				t.Fatalf("frame %d: cumulative bytes = %d, want %d", k, cumBytes, wantCum)
			}
		}

		wantPadded := (1000 * r) / sr
		if gotPadded != wantPadded {
			t.Fatalf("padded count over 1000 frames = %d, want floor(1000*%d/%d) = %d", gotPadded, r, sr, wantPadded)
		}
	})

	t.Run("48kHz never padded", func(t *testing.T) {
		rates := []int{32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
		for _, kbps := range rates {
			var p paddingState
			for k := range 1000 {
				if pad := p.next(kbps, 48000); pad != 0 {
					t.Fatalf("kbps=%d frame %d: padding = %d, want 0 (48kHz divides 144000*kbps exactly)", kbps, k, pad)
				}
			}
		}
	})
}

// fullScaleSpectrum builds a 576-line LCG spectrum tapered toward the tail
// (so partitionSpectrum sometimes finds a nonempty count1/rzero region) but
// scaled to amp at the low-frequency end, which quantizeGranule and
// minGlobalGain can drive arbitrarily close to maxQuant.
func fullScaleSpectrum(seed *uint64, amp float64) [576]float64 {
	var xr [576]float64
	for i := range xr {
		frac := float64(576-i) / 576
		v := testsignal.LCG(seed) * amp * frac
		if testsignal.LCG(seed) < 0.5 {
			v = -v
		}
		xr[i] = v
	}
	return xr
}

// TestCodeGranuleFits requires codeGranule always ends with ri.bits <=
// budget, across a spread of budgets from very tight to generous. The
// tightest budget (120 bits) is far below what even global_gain=255 can
// reach for a full-scale spectrum, so it must exercise the spectral
// truncation fallback; that is checked directly (globalGain saturated at
// 255).
func TestCodeGranuleFits(t *testing.T) {
	var seed uint64 = 99
	xr := fullScaleSpectrum(&seed, 30000)
	sfbWidths := &sfbWidthsLong[0]

	budgets := []int{120, 300, 3000}
	for _, budget := range budgets {
		var gc granuleCoding
		codeGranule(&xr, budget, sfbWidths, &gc)
		if gc.ri.bits > budget {
			t.Fatalf("budget %d: ri.bits = %d, exceeds budget", budget, gc.ri.bits)
		}
		if budget == 120 && gc.globalGain != 255 {
			t.Fatalf("budget 120: want the truncation fallback engaged (globalGain=255), got globalGain=%d bits=%d",
				gc.globalGain, gc.ri.bits)
		}
	}
}

// TestCodeGranuleBudgetCap is the addendum's new hard requirement: a
// budgetBits value beyond maxPart23Length (4095, part_2_3_length's 12-bit
// field width) must still leave ri.bits <= 4095, not merely <= budgetBits.
// 5676 is the addendum's worked 32kHz/320kbps/mono example (mainBits=11352,
// budget=mainBits/2=5676). A full-scale granule's bit cost decreases
// gradually as global_gain rises; an implementation that rate-loops against
// the raw uncapped budget would very likely stop somewhere in (4095,5676],
// which this assertion catches.
func TestCodeGranuleBudgetCap(t *testing.T) {
	var seed uint64 = 4095
	xr := fullScaleSpectrum(&seed, 8000)
	sfbWidths := &sfbWidthsLong[2] // 32000 Hz, matching the worked example's rate

	var gc granuleCoding
	codeGranule(&xr, 5676, sfbWidths, &gc)
	if gc.ri.bits > maxPart23Length {
		t.Fatalf("budgetBits=5676: ri.bits = %d, want <= %d (the part_2_3_length field cap)", gc.ri.bits, maxPart23Length)
	}
	if gc.part23Length > maxPart23Length {
		t.Fatalf("budgetBits=5676: part23Length = %d, want <= %d", gc.part23Length, maxPart23Length)
	}
}

// TestAppendFrameLength requires appendFrame's assembled frame length
// equals the ISO CBR formula 144000*kbps/sr + padding exactly, over every
// sample rate, every one of the 14 CBR bitrate indices, both channel modes,
// and both padding values. Granules are coded on the all-zero spectrum (0
// Huffman bits, well within any of this grid's budgets), isolating the
// assertion to appendFrame's framing/stuffing arithmetic rather than the
// rate loop.
func TestAppendFrameLength(t *testing.T) {
	var xr [576]float64 // all-zero: codeGranule always lands at 0 bits

	modes := []struct {
		mode int
		nch  int
	}{
		{0, 2}, // stereo
		{3, 1}, // single_channel
	}

	for sr := range 3 {
		sfb := &sfbWidthsLong[sr]
		for bitrateIndex := 1; bitrateIndex <= 14; bitrateIndex++ {
			for _, m := range modes {
				for _, padding := range []int{0, 1} {
					budget := granuleBudgetBits(bitrateIndex, sr, padding, m.nch)

					var gr [2][2]granuleCoding
					for g := range 2 {
						for ch := range m.nch {
							codeGranule(&xr, budget, sfb, &gr[g][ch])
						}
					}

					got := appendFrame(nil, bitrateIndex, sr, padding, m.mode, &gr, m.nch)
					want := frameLength(bitrateIndex, sr, padding)
					if len(got) != want {
						t.Fatalf("sr=%d bitrateIndex=%d nch=%d padding=%d: len = %d, want %d",
							sr, bitrateIndex, m.nch, padding, len(got), want)
					}
				}
			}
		}
	}
}
