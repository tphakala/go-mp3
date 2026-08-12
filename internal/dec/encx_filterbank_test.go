package dec

import (
	"math"
	"testing"

	"github.com/tphakala/go-mp3/internal/enc"
)

// TestReconstructionGate is a white-box test (package dec, not dec_test): it
// drives the unexported mp3dSynthGranule directly, bypassing DecodeFrame's
// bitstream parsing entirely, and it imports internal/enc. That import
// direction is the sanctioned exception to "enc must never import dec" (see
// PROVENANCE.md and internal/enc/doc.go): dec's production code never
// imports enc, only this _test.go file does.
//
// The encoder's analysis filterbank (internal/enc.Filterbank) has no
// bit-exact reference stream of its own to check against; the reconstruction
// gate is the only oracle available. It streams a synthetic PCM program
// through Filterbank.AnalyzeGranule, hands each granule's subband output
// straight to the decoder's synthesis filterbank (mp3dSynthGranule, the
// mathematical inverse of the analysis filterbank), and asserts the
// round trip reproduces the input at high SNR after correcting for the
// chain's fixed delay and gain.
//
// grbuf layout: analyzeGranule's out[t][b] (subband b at subband-sample
// time t) maps to grbuf[b*18+t]. This is confirmed against synth.go: full
// decoding calls mp3dSynthGranule(qmfState, grbuf, nbands=18, ...), and
// mp3dDctII(grbuf[576*i:576*i+576], nbands) walks grbuf[k:] for k in
// [0,nbands) reading y[m*18] for m = 0..31 (32 values strided by 18) - so
// "nbands" here is the count of subband-sample TIME steps (18 per granule),
// not the count of subbands (32); passing 32 would run mp3dDctII with a
// starting offset up to 31, reading index 31+31*18=589 against a 576-long
// slice, an out-of-bounds panic. nbands=18 is therefore the only value that
// fits the buffer and matches decode.go's own call convention.
//
// No sign flip is applied here: the decoder applies l3ChangeSign between
// IMDCT and synthesis (decode.go), so synthesis' input domain equals
// analysis' output domain directly; the flip pair belongs to the MDCT
// (Task 3), not this filterbank pair.
func TestReconstructionGate(t *testing.T) {
	const sr = 44100.0
	const granules = 24
	const samplesPerGranule = 576
	const totalSamples = granules * samplesPerGranule

	const f1, a1 = 1000.0, 1.0  // 1 kHz full-scale
	const f2, a2 = 11000.0, 0.1 // 11 kHz at -20 dB (20*log10(0.1) = -20 dB)

	x := make([]float64, totalSamples)
	for n := range x {
		t := float64(n) / sr
		x[n] = a1*math.Sin(2*math.Pi*f1*t) + a2*math.Sin(2*math.Pi*f2*t)
	}

	pcm := make([]float32, totalSamples)

	var fb enc.Filterbank
	var qmfState [960]float32
	lins := make([]float32, (18+15)*64)
	grbuf := make([]float32, 576)
	in := make([]float64, samplesPerGranule)
	var out [18][32]float64

	for g := range granules {
		for i := range samplesPerGranule {
			in[i] = enc.PCMScale * x[g*samplesPerGranule+i]
		}
		fb.AnalyzeGranule(in, &out)
		for tt := range 18 {
			for b := range 32 {
				grbuf[b*18+tt] = float32(out[tt][b])
			}
		}
		mp3dSynthGranule(qmfState[:], grbuf, 18, 1, pcm[g*samplesPerGranule:g*samplesPerGranule+samplesPerGranule], lins)
	}

	delay := measureDelay(x, pcm)

	// fbChainDelay is the measured round-trip sample delay of the analysis
	// (512-tap window, this task) plus synthesis (mp3dSynthGranule,
	// internal/dec/synth.go) filterbank pair, found by cross-correlating
	// the reconstructed signal against the input over a +-1200 sample
	// window. It landed at the classic analysis+synthesis polyphase delay
	// predicted in the task brief (near 481 samples): the 512-tap analysis
	// window minus the 32-sample hop it slides by minus one (512-32-... ),
	// matched here empirically rather than derived, since the exact
	// closed-form accounting depends on both filterbanks' internal
	// bookkeeping.
	const fbChainDelay = 481
	if delay != fbChainDelay {
		t.Fatalf("measured delay = %d, want frozen fbChainDelay = %d (re-measure and update the constant if this is a legitimate change)", delay, fbChainDelay)
	}

	// Per-granule gain over the last 16 granules (steady state, past the
	// filterbank's warm-up transient), checked for consistency before
	// freezing a single constant.
	const checkGranules = 16
	gains := make([]float64, 0, checkGranules)
	for g := granules - checkGranules; g < granules; g++ {
		gain, ok := granuleRMSRatio(x, pcm, delay, g*samplesPerGranule, samplesPerGranule)
		if !ok {
			t.Fatalf("granule %d: no valid overlap for gain measurement", g)
		}
		gains = append(gains, gain)
	}
	mean := 0.0
	for _, g := range gains {
		mean += g
	}
	mean /= float64(len(gains))
	for i, g := range gains {
		if math.Abs(g-mean) > mean*0.01 {
			t.Fatalf("granule %d gain = %v, mean = %v, deviates more than 1%%", granules-checkGranules+i, g, mean)
		}
	}

	// fbChainGain is the measured round-trip gain of the analysis+synthesis
	// filterbank pair once PCMScale pre-scales the input (see PCMScale's
	// doc comment in internal/enc/filterbank.go for how its value of 0.5
	// was calibrated against this same measurement). It measures 1.0: the
	// ISO analysis window (fbWindow) and matrix (fbMatrix), combined with
	// PCMScale=0.5, land the analysis+synthesis round trip at unity gain.
	const fbChainGain = 1.0
	if math.Abs(mean-fbChainGain) > fbChainGain*0.01 {
		t.Fatalf("measured mean gain = %v, want within 1%% of frozen fbChainGain = %v", mean, fbChainGain)
	}

	// SNR over the steady-state region: skip a startup margin (the
	// filterbank's shift register and the synthesis history both need to
	// fill before reconstruction is meaningful) and any tail samples the
	// delay shift pushes out of range.
	const margin = 2000
	snr := computeSNR(x, pcm, delay, fbChainGain, margin, totalSamples-margin)

	// Measure-then-freeze: the floor is 5 dB below the measured ~96.11 dB,
	// raised in this same commit from the 80 dB do-not-regress backstop
	// the brief specifies for the first (pre-measurement) run.
	const minSNRdB = 91.11
	if snr < minSNRdB {
		t.Fatalf("reconstruction SNR = %.2f dB, want >= %.2f dB", snr, minSNRdB)
	}
	t.Logf("delay=%d gain=%.6f snr=%.2fdB", delay, mean, snr)
}

// measureDelay cross-correlates y (the reconstructed signal) against x (the
// original) over lag in [-1200, 1200] and returns the lag that maximizes
// their dot product, restricted to a steady-state sample range so startup
// and tail edge effects do not bias the search. If y[n] approx
// gain*x[n-delay] for a constant positive gain, the dot product
// sum_i x[i]*y[i+lag] is maximized at lag == delay.
func measureDelay(x []float64, y []float32) int {
	const searchRadius = 1200
	const margin = 3000

	bestLag := 0
	bestCorr := math.Inf(-1)
	for lag := -searchRadius; lag <= searchRadius; lag++ {
		sum := 0.0
		for i := margin; i < len(x)-margin; i++ {
			j := i + lag
			if j < 0 || j >= len(y) {
				continue
			}
			sum += x[i] * float64(y[j])
		}
		if sum > bestCorr {
			bestCorr = sum
			bestLag = lag
		}
	}
	return bestLag
}

// granuleRMSRatio computes RMS(y aligned by delay) / RMS(x) over one
// granule's sample range. Since both sums are normalized by the same
// sample count n, the ratio is sqrt(sumSqY / sumSqX) with n canceling.
func granuleRMSRatio(x []float64, y []float32, delay, start, length int) (gain float64, ok bool) {
	var sumSqX, sumSqY float64
	n := 0
	for i := range length {
		xi := start + i
		yi := xi + delay
		if yi < 0 || yi >= len(y) {
			continue
		}
		sumSqX += x[xi] * x[xi]
		sumSqY += float64(y[yi]) * float64(y[yi])
		n++
	}
	if n == 0 || sumSqX == 0 {
		return 0, false
	}
	return math.Sqrt(sumSqY / sumSqX), true
}

// computeSNR returns 10*log10(signal power / noise power) in dB, comparing
// x[i] against y[i+delay]/gain over [start, end), the delay-aligned and
// gain-normalized reconstruction error.
func computeSNR(x []float64, y []float32, delay int, gain float64, start, end int) float64 {
	var sigPower, noisePower float64
	n := 0
	for i := start; i < end; i++ {
		j := i + delay
		if j < 0 || j >= len(y) {
			continue
		}
		recon := float64(y[j]) / gain
		err := x[i] - recon
		sigPower += x[i] * x[i]
		noisePower += err * err
		n++
	}
	if n == 0 || noisePower == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(sigPower/noisePower)
}
