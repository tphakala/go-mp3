// Command quality compares this project's MP3 encoder against LAME (used
// strictly as a black-box binary, see PROVENANCE.md) on a deterministic
// synthetic corpus and optional user WAV files. Both streams are decoded
// through this project's own pcm decoder, aligned by cross-correlation, and
// scored with the internal/quality metrics, plus ViSQOL MOS-LQO and PEAQ ODG
// when those tools are on PATH. Output is a Markdown report and a JSON twin.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	mp3 "github.com/tphakala/go-mp3"
	"github.com/tphakala/go-mp3/internal/quality"
	"github.com/tphakala/go-mp3/pcm"
)

// cmdTimeout bounds every external binary invocation.
const cmdTimeout = 5 * time.Minute

// mp3TotalDelay is this project's own encoder-plus-decoder algorithmic delay,
// the lag a correctly aligned tagless stream measures at. Named here so the
// report's legend can state it without importing the root package twice.
const mp3TotalDelay = mp3.TotalDelay

// alignment search window handed to quality.AlignLag: a small negative
// allowance for an over-trimmed stream, and well past any MP3 codec delay
// on the positive side.
const (
	alignMinLag = -128
	alignMaxLag = 4096
)

// crosscheckMinSNR is the floor, in dB, below which the -crosscheck diagnostic
// treats this project's pcm decode and ffmpeg's decode of the same stream as
// diverging and logs a warning. A faithful decode of a stream agrees with
// ffmpeg's to well over 100 dB (measured ~128 dB), so this wide margin fires
// only on a real disagreement in decode or gapless trimming, the class of bug
// that a go-mp3-versus-LAME comparison cannot see because it routes both
// encoders through the same pcm decode, and never on ordinary rounding.
const crosscheckMinSNR = 40.0

// caseSpec names one (program, sample rate, bitrate) comparison.
type caseSpec struct {
	Program    quality.Program
	SampleRate int
	Kbps       int
	Seconds    int
}

// encoderResult is one encoder's score on one case. MOS and ODG are NaN
// when the external tool was not run or produced no number.
type encoderResult struct {
	Name    string
	Lag     int
	Bytes   int
	Metrics quality.Metrics
	MOS     float64
	ODG     float64
}

// caseResult pairs the two encoders' results on one case.
type caseResult struct {
	Program    string
	Channels   int
	SampleRate int
	Kbps       int
	GoMP3      encoderResult
	LAME       encoderResult
}

// runCase executes one comparison inside dir, which must exist and is where
// every intermediate file (reference WAV, both MP3 streams, aligned WAVs for
// the external tools) is written.
func runCase(ctx context.Context, tl tools, dir string, spec caseSpec, ref [][]float64, crosscheck bool, errw io.Writer) (caseResult, error) {
	if len(ref) == 0 || len(ref[0]) == 0 {
		return caseResult{}, errors.New("empty program at this sample rate")
	}
	// ref is the quantized 16-bit signal, shared read-only across this
	// program's bitrate cases. LAME reads it from the WAV written here; go-mp3
	// gets the identical samples as float32. Nothing below mutates ref.
	if err := writeWAVFile(filepath.Join(dir, "ref.wav"), spec.SampleRate, ref); err != nil {
		return caseResult{}, err
	}

	goStream, err := encodeGoMP3(ref, spec.SampleRate, spec.Kbps)
	if err != nil {
		return caseResult{}, fmt.Errorf("go-mp3 encode: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gomp3.mp3"), goStream, 0o644); err != nil {
		return caseResult{}, err
	}
	lameStream, err := encodeLAME(ctx, tl.lame, dir, "ref.wav", "lame.mp3", spec.Kbps)
	if err != nil {
		return caseResult{}, fmt.Errorf("lame encode: %w", err)
	}

	res := caseResult{Program: spec.Program.Name, Channels: len(ref), SampleRate: spec.SampleRate, Kbps: spec.Kbps}
	res.GoMP3, err = measure(ctx, tl, dir, "go-mp3", "gomp3", ref, goStream, spec.SampleRate, crosscheck, errw)
	if err != nil {
		return caseResult{}, err
	}
	res.LAME, err = measure(ctx, tl, dir, "lame", "lame", ref, lameStream, spec.SampleRate, crosscheck, errw)
	if err != nil {
		return caseResult{}, err
	}
	return res, nil
}

// measure decodes, aligns, and scores one encoder's stream against ref. The
// external perceptual tools run only when configured; their failures are
// reported through errw and leave the value NaN rather than failing the case.
func measure(ctx context.Context, tl tools, dir, name, base string, ref [][]float64, stream []byte, sampleRate int, crosscheck bool, errw io.Writer) (encoderResult, error) {
	deg, err := decodeStream(stream)
	if err != nil {
		return encoderResult{}, fmt.Errorf("%s decode: %w", name, err)
	}
	if len(deg) != len(ref) {
		return encoderResult{}, fmt.Errorf("%s: decoded %d channels, want %d", name, len(deg), len(ref))
	}
	// The cross-check compares this pcm decode against ffmpeg's decode of the
	// same stream, so it uses the raw deg, before the ref alignment and trim
	// below that the metrics need.
	if crosscheck {
		crossCheck(ctx, tl, dir, name, base+".mp3", deg, sampleRate, errw)
	}
	refA, degA, lag := alignTrim(ref, deg, alignMinLag, alignMaxLag)
	if len(refA) == 0 || len(refA[0]) < quality.SegSNRSegment {
		// Nothing meaningful overlaps. Scoring this would report the metrics'
		// degenerate-input values as a measurement, so fail the case instead.
		return encoderResult{}, fmt.Errorf("%s: alignment at lag %d left %d overlapping samples", name, lag, len(refA[0]))
	}
	r := encoderResult{Name: name, Lag: lag, Bytes: len(stream), MOS: math.NaN(), ODG: math.NaN()}
	r.Metrics = quality.Compare(refA, degA, sampleRate)
	if tl.visqol == "" && tl.peaq == "" {
		return r, nil
	}
	// The reference is aligned per encoder (the lags differ), so its file name
	// is namespaced too: a shared name would leave -keep holding only the last
	// encoder's copy, and the other's scores unreproducible.
	refWav, degWav := base+"-ref-aligned.wav", base+"-aligned.wav"
	if err := writeWAVFile(filepath.Join(dir, refWav), sampleRate, refA); err != nil {
		return encoderResult{}, err
	}
	if err := writeWAVFile(filepath.Join(dir, degWav), sampleRate, degA); err != nil {
		return encoderResult{}, err
	}
	if tl.visqol != "" {
		if v, err := runVisqol(ctx, tl, dir, refWav, degWav, sampleRate); err == nil {
			r.MOS = v
		} else {
			logf(errw, "warning: visqol %s/%s: %v\n", filepath.Base(dir), name, err)
		}
	}
	if tl.peaq != "" {
		if v, err := runPEAQ(ctx, tl, dir, refWav, degWav, sampleRate); err == nil {
			r.ODG = v
		} else {
			logf(errw, "warning: peaq %s/%s: %v\n", filepath.Base(dir), name, err)
		}
	}
	return r, nil
}

// writeWAVFile writes planar float64 channels as a 16-bit WAV at path.
func writeWAVFile(path string, sampleRate int, ch [][]float64) error {
	return writeFile(path, func(f *os.File) error {
		return quality.WriteWAV16(f, sampleRate, ch)
	})
}

// encodeGoMP3 runs the public mp3.Encoder over ref (planar float64) in
// FrameSize frames, the last one possibly short, and drains it.
func encodeGoMP3(ref [][]float64, sampleRate, kbps int) ([]byte, error) {
	e, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: sampleRate, Channels: len(ref), Bitrate: kbps * 1000})
	if err != nil {
		return nil, err
	}
	n := len(ref[0])
	buf := make([][]float32, len(ref))
	views := make([][]float32, len(ref))
	for c := range buf {
		buf[c] = make([]float32, mp3.FrameSize)
	}
	var out []byte
	for pos := 0; pos < n; pos += mp3.FrameSize {
		end := min(pos+mp3.FrameSize, n)
		for c := range ref {
			v := buf[c][:end-pos]
			for i := range v {
				v[i] = float32(ref[c][pos+i])
			}
			views[c] = v
		}
		if out, err = e.EncodeFrame(out, views); err != nil {
			return nil, err
		}
	}
	return e.EncodeFrame(out, nil)
}

// encodeLAME runs the lame binary on wavName inside dir, writing mp3Name,
// and returns the stream bytes. CBR at kbps; every other setting is LAME's
// default (joint stereo for stereo input, its own lowpass and tuning), which
// is exactly the "fully tuned encoder" baseline being measured against.
func encodeLAME(ctx context.Context, lame, dir, wavName, mp3Name string, kbps int) ([]byte, error) {
	if lame == "" {
		return nil, errors.New("lame binary not configured")
	}
	// runTool runs inside dir with the shared timeout and combined-output
	// capture; --quiet keeps stdout empty, so its output on failure is LAME's
	// diagnostics. The stream is read back from the file LAME wrote.
	if out, err := runTool(ctx, dir, lame, "--quiet", "--cbr", "-b", fmt.Sprint(kbps), wavName, mp3Name); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
	}
	return os.ReadFile(filepath.Join(dir, mp3Name))
}

// decodeStream decodes an MP3 stream through this project's pcm decoder
// (native float32 output) to planar float64 channels. A LAME-tagged stream
// comes back gapless-trimmed; this project's tagless streams keep their
// mp3.TotalDelay leading samples, which alignTrim measures and removes.
func decodeStream(stream []byte) ([][]float64, error) {
	d, err := pcm.NewDecoder(bytes.NewReader(stream), pcm.WithF32())
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(d)
	if err != nil {
		return nil, err
	}
	nch := d.Info().Channels
	// A zero channel count is a pcm decoder bug, not bad input, and silent
	// without the guard: it would divide by zero in the unpack below.
	if nch <= 0 {
		return nil, fmt.Errorf("decoded stream reports %d channels", nch)
	}
	return deinterleaveF32LE(raw, nch)
}

// deinterleaveF32LE unpacks interleaved little-endian float32 samples into
// channels planar float64 channels. It errors when the byte count is not a
// whole number of channel-sized frames, which means the producer and this
// unpacking disagree about the layout and would otherwise drop the odd tail.
// channels must be positive; callers guarantee it.
func deinterleaveF32LE(b []byte, channels int) ([][]float64, error) {
	frameBytes := 4 * channels
	if len(b)%frameBytes != 0 {
		return nil, fmt.Errorf("decoded %d bytes, not a whole number of %d-byte frames (%d channels of float32)", len(b), frameBytes, channels)
	}
	frames := len(b) / frameBytes
	out := make([][]float64, channels)
	for c := range out {
		out[c] = make([]float64, frames)
	}
	for i := range frames {
		for c := range channels {
			off := (i*channels + c) * 4
			out[c][i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(b[off:])))
		}
	}
	return out, nil
}

// alignTrim measures the lag of deg against ref on channel 0 within
// [minLag, maxLag], applies it to every channel, and trims both to the common
// length.
func alignTrim(ref, deg [][]float64, minLag, maxLag int) (refOut, degOut [][]float64, lag int) {
	lag = quality.AlignLag(ref[0], deg[0], minLag, maxLag)
	refOut = make([][]float64, len(ref))
	degOut = make([][]float64, len(ref))
	for c := range ref {
		r, d := ref[c], deg[c]
		if lag >= 0 {
			d = d[min(lag, len(d)):]
		} else {
			r = r[min(-lag, len(r)):]
		}
		n := min(len(r), len(d))
		refOut[c], degOut[c] = r[:n], d[:n]
	}
	return refOut, degOut, lag
}

// crossCheck decodes mp3Name a second time through ffmpeg and compares it to
// deg, this project's pcm decode of the same stream, logging a warning when
// the two fall below crosscheckMinSNR. It is best-effort and diagnostic only:
// any error and any result leave the case's measured metrics untouched, so it
// can only ever add a log line, never change a number or fail a case.
func crossCheck(ctx context.Context, tl tools, dir, name, mp3Name string, deg [][]float64, sampleRate int, errw io.Writer) {
	// crossCheckDecode forces ffmpeg to len(deg) channels, so ff always has the
	// same channel count as deg; no channel-count guard is needed.
	ff, err := crossCheckDecode(ctx, tl.ffmpeg, dir, mp3Name, sampleRate, len(deg))
	if err != nil {
		logf(errw, "warning: crosscheck %s/%s: %v\n", filepath.Base(dir), name, err)
		return
	}
	// Both sides decode the SAME stream, so the lag is near zero, but either
	// side can be the trimmed one (ffmpeg and pcm may handle a stream's gapless
	// padding differently), so align over a SYMMETRIC window rather than the
	// asymmetric one measure uses for a coded stream against its source: a
	// larger trim on the pcm side is a negative lag the asymmetric floor would
	// miss and report as a false divergence.
	degA, ffA, lag := alignTrim(deg, ff, -alignMaxLag, alignMaxLag)
	if len(degA) == 0 || len(degA[0]) == 0 {
		logf(errw, "warning: crosscheck %s/%s: pcm and ffmpeg decodes do not overlap (lag %d)\n", filepath.Base(dir), name, lag)
		return
	}
	// Score the WORST channel, not a mean: a mean would let one agreeing or one
	// silent channel hide a real divergence on another. A channel whose pcm side
	// is digital silence has an undefined SNR (NaN) and is skipped. That is a
	// blind spot in one direction, a pcm bug that silences a channel ffmpeg
	// still decodes goes unseen, but only at exactly zero energy: near-silence
	// still fires with a hugely negative SNR, and the reverse (ffmpeg silent,
	// pcm loud) fires normally. If every channel is silent, worst stays +Inf and
	// nothing fires, which is correct.
	worst := math.Inf(1)
	for c := range degA {
		s := quality.SNR(degA[c], ffA[c])
		if math.IsNaN(s) {
			continue
		}
		worst = min(worst, s)
	}
	if worst < crosscheckMinSNR {
		logf(errw, "warning: crosscheck %s/%s: pcm and ffmpeg decodes diverge at %.1f dB (lag %d), below %.0f dB\n",
			filepath.Base(dir), name, worst, lag, crosscheckMinSNR)
	}
}

// crossCheckDecode decodes mp3Name inside dir through ffmpeg to planar float64
// channels, the reference second decoder for the -crosscheck diagnostic. It
// forces ffmpeg's output layout to channels at sampleRate (the stream's own,
// so this asserts the layout rather than resampling) and reads back the raw
// f32le samples.
func crossCheckDecode(ctx context.Context, ffmpeg, dir, mp3Name string, sampleRate, channels int) ([][]float64, error) {
	rawName := mp3Name + ".f32"
	if out, err := runTool(ctx, dir, ffmpeg, "-v", "error", "-y", "-i", mp3Name,
		"-f", "f32le", "-ac", fmt.Sprint(channels), "-ar", fmt.Sprint(sampleRate), rawName); err != nil {
		return nil, fmt.Errorf("ffmpeg decode: %w: %s", err, strings.TrimSpace(out))
	}
	b, err := os.ReadFile(filepath.Join(dir, rawName))
	if err != nil {
		return nil, err
	}
	return deinterleaveF32LE(b, channels)
}
