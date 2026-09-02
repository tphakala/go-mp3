package quality

import (
	"math"
	"slices"
)

// Tunables of the Go-native metrics. The exported ones appear in reports so
// a reader knows which definitions the numbers were computed under.
const (
	// SNRCap is the SNR reported when the error energy is zero or below
	// epsilon times the signal energy (identical or numerically identical
	// signals).
	SNRCap = 120.0
	// BandLimitHz bounds the STFT bins BandSNR and LSD are computed over, so
	// LAME's deliberate high-frequency lowpass at lower bitrates is not
	// counted as noise there (full-band SNR still sees it).
	BandLimitHz = 16000.0
	// SegSNRMin and SegSNRMax clamp per-segment SNR (the segmental-SNR
	// convention) so one silent or one destroyed segment cannot dominate
	// the mean.
	SegSNRMin = -10.0
	SegSNRMax = 35.0
	// SegSNRSegment is the segmental-SNR segment length in samples: one
	// MPEG-1 Layer III frame.
	SegSNRSegment = 1152
	// BandwidthFloorDB is how far below the long-term PSD peak a bin may sit
	// and still count as "in band".
	BandwidthFloorDB = 50.0

	stftSize    = 2048
	stftHop     = 1024
	activeFloor = 1e-6 // mean-square floor for an "active" segment or frame (-60 dBFS)
	epsilon     = 1e-12

	preEchoWindowMs  = 5    // attack-detection window
	preEchoPreMs     = 20   // pre-attack window the error is integrated over
	preEchoRatio     = 10.0 // window-to-previous-window energy ratio that marks an attack
	preEchoFloor     = 1e-4 // mean-square an attack window must exceed (-40 dBFS)
	preEchoHoldoffMs = 50   // minimum spacing between counted attacks
)

// Metrics is one channel-averaged measurement of a decoded signal against
// its reference. Directions: SNR, BandSNR, SegSNR higher is better; LSD and
// PreEcho lower is better; Bandwidth is informational.
type Metrics struct {
	SNR       float64 // dB, full band, capped at SNRCap
	BandSNR   float64 // dB, STFT bins at or below BandLimitHz
	SegSNR    float64 // dB, mean per-segment SNR clamped to [SegSNRMin, SegSNRMax]; NaN when no active segment
	LSD       float64 // dB, mean log-spectral distance over active frames; NaN when no active frame
	PreEcho   float64 // dB, pre-attack error energy relative to attack energy; NaN when no attack
	PreEchoN  int     // attacks PreEcho averaged over
	Bandwidth float64 // Hz, highest long-term PSD bin within BandwidthFloorDB of the peak
}

// Compare measures deg against ref (planar channels of equal length, already
// aligned) and averages every metric over channels, except Bandwidth, which
// is the maximum over channels, and PreEchoN, which is the total. NaN
// channel values propagate: a metric undefined on one channel is undefined
// for the pair.
func Compare(ref, deg [][]float64, sampleRate int) Metrics {
	var m Metrics
	if len(ref) == 0 || len(deg) != len(ref) {
		return Metrics{SNR: math.NaN(), BandSNR: math.NaN(), SegSNR: math.NaN(),
			LSD: math.NaN(), PreEcho: math.NaN()}
	}
	nch := float64(len(ref))
	for c := range ref {
		m.SNR += SNR(ref[c], deg[c]) / nch
		m.SegSNR += SegmentalSNR(ref[c], deg[c], SegSNRSegment) / nch
		b, l := SpectralMetrics(ref[c], deg[c], sampleRate)
		m.BandSNR += b / nch
		m.LSD += l / nch
		p, n := PreEcho(ref[c], deg[c], sampleRate)
		m.PreEcho += p / nch
		m.PreEchoN += n
		m.Bandwidth = max(m.Bandwidth, Bandwidth(deg[c], sampleRate))
	}
	return m
}

// energy returns the energy (sum of squares) of x over [lo, hi).
func energy(x []float64, lo, hi int) float64 {
	var e float64
	for _, v := range x[lo:hi] {
		e += v * v
	}
	return e
}

// energies returns the reference and error energies over [lo, hi).
func energies(ref, deg []float64, lo, hi int) (sig, errE float64) {
	for i := lo; i < hi; i++ {
		d := ref[i] - deg[i]
		sig += ref[i] * ref[i]
		errE += d * d
	}
	return sig, errE
}

// snrFrom converts energies to dB with the cap. A reference with no energy
// at all yields NaN rather than the cap: there was nothing to measure, and
// reporting the top of the scale for that would turn "no data" into "perfect"
// (the sibling metrics all return NaN for the same degenerate input).
func snrFrom(sig, errE float64) float64 {
	if sig <= 0 {
		return math.NaN()
	}
	if errE == 0 || errE <= epsilon*sig {
		return SNRCap
	}
	return min(SNRCap, 10*math.Log10(sig/errE))
}

// SNR is the full-band signal-to-noise ratio of deg against ref in dB over
// their common length.
func SNR(ref, deg []float64) float64 {
	n := min(len(ref), len(deg))
	sig, errE := energies(ref, deg, 0, n)
	return snrFrom(sig, errE)
}

// SegmentalSNR is the mean over active seg-sample segments (reference
// mean-square above the active floor) of the per-segment SNR clamped to
// [SegSNRMin, SegSNRMax]. It returns NaN when no segment is active.
func SegmentalSNR(ref, deg []float64, seg int) float64 {
	if seg <= 0 {
		return math.NaN() // a non-positive segment length would not terminate
	}
	n := min(len(ref), len(deg))
	var sum float64
	var count int
	for lo := 0; lo+seg <= n; lo += seg {
		sig, errE := energies(ref, deg, lo, lo+seg)
		if sig/float64(seg) < activeFloor {
			continue
		}
		sum += max(SegSNRMin, min(SegSNRMax, snrFrom(sig, errE)))
		count++
	}
	if count == 0 {
		return math.NaN()
	}
	return sum / float64(count)
}

// stftWindow is the Hann window for the fixed stftSize, computed once. It is
// read-only after init, so concurrent callers (the harness scores cases in
// parallel) share it safely.
var stftWindow = hannWindow(stftSize)

// stftFrames calls fn with the Hann-windowed power spectrum (bins
// 0..stftSize/2, unnormalized FFT) of every full stftSize frame of x at hop
// stftHop, and returns the number of frames visited. The power slice is
// reused between calls; fn must copy what it keeps.
func stftFrames(x []float64, fn func(power []float64)) int {
	w := stftWindow
	re := make([]float64, stftSize)
	im := make([]float64, stftSize)
	power := make([]float64, stftSize/2+1)
	frames := 0
	for lo := 0; lo+stftSize <= len(x); lo += stftHop {
		for i := range stftSize {
			re[i] = x[lo+i] * w[i]
			im[i] = 0
		}
		fft(re, im)
		for k := range power {
			power[k] = re[k]*re[k] + im[k]*im[k]
		}
		fn(power)
		frames++
	}
	return frames
}

// bandBins returns the number of STFT bins (from DC) at or below
// BandLimitHz at sampleRate, capped at the Nyquist bin.
func bandBins(sampleRate int) int {
	return min(stftSize/2, int(BandLimitHz*stftSize/float64(sampleRate))) + 1
}

// SpectralMetrics returns the band-limited SNR (bins at or below
// BandLimitHz, all frames pooled) and the mean log-spectral distance over
// active frames (in-band reference power above the active floor), both in
// dB. LSD is NaN when no frame is active.
func SpectralMetrics(ref, deg []float64, sampleRate int) (bandSNR, lsd float64) {
	n := min(len(ref), len(deg))
	ref, deg = ref[:n], deg[:n]
	nb := bandBins(sampleRate)

	var refPow [][]float64
	stftFrames(ref, func(p []float64) {
		cp := make([]float64, nb)
		copy(cp, p[:nb])
		refPow = append(refPow, cp)
	})

	errSig := make([]float64, n)
	for i := range errSig {
		errSig[i] = ref[i] - deg[i]
	}
	if len(refPow) == 0 {
		// Shorter than one STFT frame: nothing was transformed, so neither
		// figure is defined. Returning the SNR cap here would report a
		// too-short input as a perfect match.
		return math.NaN(), math.NaN()
	}

	var sig, errE float64
	f := 0
	stftFrames(errSig, func(p []float64) {
		for k := range nb {
			sig += refPow[f][k]
			errE += p[k]
		}
		f++
	})
	bandSNR = snrFrom(sig, errE)

	// Active-frame floor in the unnormalized power domain: the in-band power
	// sum of band-limited white noise at the activeFloor mean-square, i.e.
	// activeFloor times the periodic Hann energy (3N/8) per bin (Parseval
	// spreads N*E over N bins) times the nb bins summed.
	frameFloor := activeFloor * (3.0 * stftSize / 8) * float64(nb)
	var lsdSum float64
	var active int
	f = 0
	stftFrames(deg, func(p []float64) {
		rp := refPow[f]
		f++
		var frameSig float64
		for _, v := range rp {
			frameSig += v
		}
		if frameSig < frameFloor {
			return
		}
		var acc float64
		for k := range nb {
			d := 10 * math.Log10((rp[k]+epsilon)/(p[k]+epsilon))
			acc += d * d
		}
		lsdSum += math.Sqrt(acc / float64(nb))
		active++
	})
	if active == 0 {
		return bandSNR, math.NaN()
	}
	return bandSNR, lsdSum / float64(active)
}

// Bandwidth returns the frequency in Hz of the highest bin whose long-term
// average power spectrum lies within BandwidthFloorDB of the spectrum's
// peak. It reports 0 for an all-silent or too-short signal.
func Bandwidth(x []float64, sampleRate int) float64 {
	acc := make([]float64, stftSize/2+1)
	frames := stftFrames(x, func(p []float64) {
		for k := range acc {
			acc[k] += p[k]
		}
	})
	if frames == 0 {
		return 0
	}
	peak := 0.0
	for _, v := range acc {
		peak = max(peak, v)
	}
	if peak <= 0 {
		return 0
	}
	floor := peak * math.Pow(10, -BandwidthFloorDB/10)
	for k, v := range slices.Backward(acc) {
		if v >= floor {
			return float64(k) * float64(sampleRate) / stftSize
		}
	}
	return 0
}

// PreEcho detects attacks in ref (a preEchoWindowMs window whose energy
// exceeds preEchoRatio times the previous window's and preEchoFloor in
// mean-square, at least preEchoHoldoffMs after the previous counted attack
// and at least preEchoPreMs into the signal) and, for each, measures the
// error energy in the preEchoPreMs before the attack window relative to the
// attack window's reference energy, in dB. It returns the mean over attacks
// and the attack count; the mean is NaN when no attack is found. Lower is
// better: a clean encoder sits far below 0 dB, audible pre-echo approaches
// it.
func PreEcho(ref, deg []float64, sampleRate int) (meanDB float64, events int) {
	n := min(len(ref), len(deg))
	win := sampleRate * preEchoWindowMs / 1000
	pre := sampleRate * preEchoPreMs / 1000
	holdoff := preEchoHoldoffMs / preEchoWindowMs
	if win == 0 || n < pre+win {
		return math.NaN(), 0
	}
	var sum float64
	prevE := 0.0
	lastAttack := -holdoff
	for k := 0; (k+1)*win <= n; k++ {
		lo := k * win
		e := energy(ref, lo, lo+win)
		isAttack := e > preEchoRatio*prevE && e/float64(win) > preEchoFloor &&
			lo >= pre && k-lastAttack >= holdoff
		prevE = e
		if !isAttack {
			continue
		}
		lastAttack = k
		_, errPre := energies(ref, deg, lo-pre, lo)
		sum += 10 * math.Log10((errPre+epsilon)/(e+epsilon))
		events++
	}
	if events == 0 {
		return math.NaN(), 0
	}
	return sum / float64(events), events
}
