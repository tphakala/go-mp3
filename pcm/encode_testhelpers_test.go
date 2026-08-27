package pcm

import (
	"encoding/binary"
	"math"
)

// genSineS16 returns nSamplesPerCh inter-channel samples of a full-scale-safe
// sine wave, interleaved little-endian S16, for the given channel count. Every
// channel carries the same tone at amplitude 0.5 (well inside [-1, 1] so no
// clamping occurs on the encode-side conversion), which MP3 at typical bitrates
// reproduces with high fidelity, giving the round-trip SNR test a clean signal.
func genSineS16(nSamplesPerCh, channels, freq, sampleRate int) []byte {
	out := make([]byte, nSamplesPerCh*channels*2)
	for i := range nSamplesPerCh {
		v := 0.5 * math.Sin(2*math.Pi*float64(freq)*float64(i)/float64(sampleRate))
		s := int16(math.Round(v * 32768))
		for c := range channels {
			binary.LittleEndian.PutUint16(out[(i*channels+c)*2:], uint16(s))
		}
	}
	return out
}

// snrDB is the signal-to-noise ratio in decibels between reference ref and
// measured got over their overlapping length, treating the sample-by-sample
// difference as noise. It returns math.Inf(1) when the overlap is silent-noise
// (an exact match). ref and got are aligned by the caller before this is called.
func snrDB(ref, got []int16) float64 {
	n := len(ref)
	if len(got) < n {
		n = len(got)
	}
	var sig, noise float64
	for i := range n {
		d := float64(ref[i]) - float64(got[i])
		sig += float64(ref[i]) * float64(ref[i])
		noise += d * d
	}
	if noise == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(sig/noise)
}

// bytesToS16 reinterprets interleaved little-endian S16 bytes as []int16.
func bytesToS16(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return out
}
