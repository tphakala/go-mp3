package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tphakala/go-mp3/internal/quality"
)

// The quality regression gate. It re-measures go-mp3's own objective quality
// on a small deterministic corpus and fails when any gated metric regresses
// past a tolerance against a committed baseline. It gates on go-mp3's absolute
// metrics, not the go-mp3-vs-LAME delta: the delta would move whenever the
// runner's LAME changed, whereas go-mp3 encode -> this repo's pcm decode ->
// cross-correlation align -> Compare is fully in-repo and deterministic on a
// given architecture, so it is a stable signal for an encoder change. LAME
// stays the human-facing reference in `task quality` and the smoke test, not
// this gate's oracle, so the gate needs no external binary.
//
// The committed baseline is generated on amd64. The metrics are deterministic
// per architecture and differ across architectures only at the ~1e-13 level
// (the encoder and pcm decoder are bit-exact cross-arch; only float64 FMA
// reassociation in the FFT/SNR math varies), far under the dB tolerances here,
// so the amd64 baseline gates both the amd64 and arm64 CI legs. The test is
// guarded by MP3_QUALITY_BASELINE=1 so a bare local `go test ./...` skips this
// lame-free but non-trivial run unless opted in; CI's test job sets it on both
// arches. UPDATE_QUALITY_BASELINE=1 rewrites the baseline; refreshing it is a
// deliberate, reviewed action, the same model as the encoder's golden gate.

const (
	baselineSchemaVersion = 1
	baselineSeconds       = 2
	baselinePath          = "testdata/baseline.json"
)

// Directional regression tolerances in dB: SNR, BandSNR, SegSNR are
// higher-is-better; LSD and PreEcho are lower-is-better. They are wide enough
// to absorb floating-point reassociation noise on a single architecture and
// tight enough to catch a real quality change, which a quality-tuning PR is
// expected to trip and then re-freeze the baseline for.
const (
	tolSNR     = 0.5
	tolBandSNR = 0.5
	tolSegSNR  = 0.5
	tolLSD     = 0.5
	tolPreEcho = 1.0
)

// baselineCase is one case's gated go-mp3 metrics. Non-finite values (a metric
// undefined for the case, e.g. PreEcho on a program with no transients) encode
// as JSON null via nullable, matching the report's convention.
type baselineCase struct {
	Program    string   `json:"program"`
	SampleRate int      `json:"sample_rate"`
	Kbps       int      `json:"kbps"`
	Lag        int      `json:"lag"`
	SNR        *float64 `json:"snr"`
	BandSNR    *float64 `json:"band_snr"`
	SegSNR     *float64 `json:"seg_snr"`
	LSD        *float64 `json:"lsd"`
	PreEcho    *float64 `json:"pre_echo"`
	PreEchoN   int      `json:"pre_echo_events"`
}

// baselineFile is the committed baseline document.
type baselineFile struct {
	SchemaVersion int            `json:"schema_version"`
	Seconds       int            `json:"seconds"`
	Note          string         `json:"note"`
	Cases         []baselineCase `json:"cases"`
}

// baselinePrograms is the gate corpus: a small representative slice of the
// synthetic corpus (tonal, noisy, transient, bird-like, and a stereo case),
// run at 44100 Hz and the CBR extremes so a regression at either bit budget is
// caught. Any program added here must have a unique, well-separated
// cross-correlation peak so the zero-tolerance Lag gate stays stable across
// architectures; refresh the baseline (task quality:gate:update) after any
// change to this set.
var (
	baselinePrograms = []string{"multitone", "pink-noise", "tone-click", "bird-chirps", "stereo-decorrelated"}
	baselineRates    = []int{44100}
	baselineBitrates = []int{128, 320}
)

// baselineSpecs expands the gate corpus into cases, in a stable order.
func baselineSpecs(t *testing.T) []caseSpec {
	t.Helper()
	specs := make([]caseSpec, 0, len(baselineRates)*len(baselinePrograms)*len(baselineBitrates))
	for _, sr := range baselineRates {
		for _, name := range baselinePrograms {
			p, ok := quality.ProgramByName(name)
			if !ok {
				t.Fatalf("baseline corpus names unknown program %q", name)
			}
			for _, kbps := range baselineBitrates {
				specs = append(specs, caseSpec{Program: p, SampleRate: sr, Kbps: kbps, Seconds: baselineSeconds})
			}
		}
	}
	return specs
}

// measureGoMP3 runs one case through go-mp3 only (encode, pcm decode, align,
// score), reusing the production measure path with no external tools and no
// cross-check. It returns the metrics and the alignment lag, which is go-mp3's
// algorithmic delay and part of the gate.
func measureGoMP3(t *testing.T, spec caseSpec) (m quality.Metrics, lag int) {
	t.Helper()
	ref := caseRef(spec)
	if len(ref) == 0 || len(ref[0]) == 0 {
		t.Fatalf("empty reference for %s at %d Hz", spec.Program.Name, spec.SampleRate)
	}
	stream, err := encodeGoMP3(ref, spec.SampleRate, spec.Kbps)
	if err != nil {
		t.Fatalf("%s %d/%d: go-mp3 encode: %v", spec.Program.Name, spec.SampleRate, spec.Kbps, err)
	}
	r, err := measure(t.Context(), tools{}, t.TempDir(), "go-mp3", "gomp3", ref, stream, spec.SampleRate, false, io.Discard)
	if err != nil {
		t.Fatalf("%s %d/%d: measure: %v", spec.Program.Name, spec.SampleRate, spec.Kbps, err)
	}
	return r.Metrics, r.Lag
}

// toBaselineCase captures the gated metrics of one case.
func toBaselineCase(spec caseSpec, m quality.Metrics, lag int) baselineCase {
	return baselineCase{
		Program: spec.Program.Name, SampleRate: spec.SampleRate, Kbps: spec.Kbps, Lag: lag,
		SNR: nullable(m.SNR), BandSNR: nullable(m.BandSNR), SegSNR: nullable(m.SegSNR),
		LSD: nullable(m.LSD), PreEcho: nullable(m.PreEcho), PreEchoN: m.PreEchoN,
	}
}

// TestQualityBaseline is the regression gate. See the file comment for what it
// gates on and why it is guarded by an environment variable.
func TestQualityBaseline(t *testing.T) {
	update := os.Getenv("UPDATE_QUALITY_BASELINE") != ""
	if !update && os.Getenv("MP3_QUALITY_BASELINE") == "" {
		t.Skip("set MP3_QUALITY_BASELINE=1 to run the quality regression gate (or UPDATE_QUALITY_BASELINE=1 to refresh the baseline)")
	}
	// Refreshing and gating are mutually exclusive intents: with both set the
	// update path would silently overwrite the baseline and report a pass,
	// hiding any regression. Force one explicit intent.
	if update && os.Getenv("MP3_QUALITY_BASELINE") != "" {
		t.Fatal("set only one of UPDATE_QUALITY_BASELINE (refresh) or MP3_QUALITY_BASELINE (gate); with both set, refreshing would overwrite the baseline instead of checking against it")
	}

	specs := baselineSpecs(t)
	cur := make([]baselineCase, len(specs))
	for i, spec := range specs {
		m, lag := measureGoMP3(t, spec)
		cur[i] = toBaselineCase(spec, m, lag)
	}

	if update {
		writeBaseline(t, cur)
		return
	}

	base := readBaseline(t)
	if base.SchemaVersion != baselineSchemaVersion {
		t.Fatalf("baseline schema version %d, want %d: refresh with UPDATE_QUALITY_BASELINE=1", base.SchemaVersion, baselineSchemaVersion)
	}
	// A seconds mismatch means the baseline was measured at a different program
	// length; the metrics would not be comparable. Fail with the same refresh
	// hint as the schema check rather than reporting confusing metric drift.
	if base.Seconds != baselineSeconds {
		t.Fatalf("baseline seconds %d, want %d: refresh with UPDATE_QUALITY_BASELINE=1", base.Seconds, baselineSeconds)
	}
	byKey := make(map[string]baselineCase, len(base.Cases))
	for _, bc := range base.Cases {
		byKey[caseKey(bc.Program, bc.SampleRate, bc.Kbps)] = bc
	}
	if len(cur) != len(base.Cases) {
		t.Errorf("measured %d cases but baseline has %d: refresh with UPDATE_QUALITY_BASELINE=1", len(cur), len(base.Cases))
	}
	for _, cc := range cur {
		bc, ok := byKey[caseKey(cc.Program, cc.SampleRate, cc.Kbps)]
		if !ok {
			t.Errorf("%s %d/%d: no baseline case; refresh with UPDATE_QUALITY_BASELINE=1", cc.Program, cc.SampleRate, cc.Kbps)
			continue
		}
		t.Run(fmt.Sprintf("%s_%d_%d", cc.Program, cc.SampleRate, cc.Kbps), func(t *testing.T) {
			checkRegression(t, &bc, &cc)
		})
	}
}

// checkRegression fails t for every gated metric of cur that is worse than
// base beyond its tolerance. A baseline null (metric undefined for the case) is
// skipped; a current value that turned non-finite where the baseline was finite
// is a regression.
func checkRegression(t *testing.T, base, cur *baselineCase) {
	t.Helper()
	// SNR is finite for every active (non-silent) program in the corpus, so a
	// null baseline SNR means the baseline file is degenerate or corrupt, not
	// that the metric is legitimately undefined (as LSD and PreEcho can be for
	// a tonal program). Fail loudly rather than skip every metric below and
	// pass vacuously on a broken baseline.
	if base.SNR == nil {
		t.Errorf("baseline SNR is null: the baseline looks corrupt, refresh with UPDATE_QUALITY_BASELINE=1")
		return
	}
	// Alignment lag is go-mp3's algorithmic delay. A shifted delay is scored
	// away by the cross-correlation aligner, so SNR/LSD would still pass while
	// gapless playback and A/V sync broke; gate it with zero tolerance. Unlike
	// the dB metrics, Lag is an integer argmax of a sharply peaked
	// cross-correlation (every corpus program has a unique, well-separated
	// peak), so it is stable across architectures despite arm64 FMA in the
	// correlation sum: a ~1e-13 perturbation cannot move the peak. PreEchoN is
	// deliberately NOT gated here: it counts attacks in the reference only
	// (independent of the encoder), so it cannot signal an encoder regression;
	// it is kept in the baseline as informational context. The transient
	// quality signal lives in the PreEcho dB value below.
	if cur.Lag != base.Lag {
		t.Errorf("alignment lag changed: %d, baseline %d (encoder delay regression)", cur.Lag, base.Lag)
	}
	higher := []struct {
		name      string
		base, cur *float64
		tol       float64
	}{
		{"SNR", base.SNR, cur.SNR, tolSNR},
		{"BandSNR", base.BandSNR, cur.BandSNR, tolBandSNR},
		{"SegSNR", base.SegSNR, cur.SegSNR, tolSegSNR},
	}
	for _, m := range higher {
		if m.base == nil {
			continue
		}
		if m.cur == nil {
			t.Errorf("%s regressed to undefined (baseline %.3f dB)", m.name, *m.base)
			continue
		}
		if *m.cur < *m.base-m.tol {
			t.Errorf("%s regressed: %.3f dB, baseline %.3f dB, tolerance %.2f dB (lower is worse)", m.name, *m.cur, *m.base, m.tol)
		}
	}
	lower := []struct {
		name      string
		base, cur *float64
		tol       float64
	}{
		{"LSD", base.LSD, cur.LSD, tolLSD},
		{"PreEcho", base.PreEcho, cur.PreEcho, tolPreEcho},
	}
	for _, m := range lower {
		if m.base == nil {
			continue
		}
		if m.cur == nil {
			t.Errorf("%s regressed to undefined (baseline %.3f dB)", m.name, *m.base)
			continue
		}
		if *m.cur > *m.base+m.tol {
			t.Errorf("%s regressed: %.3f dB, baseline %.3f dB, tolerance %.2f dB (higher is worse)", m.name, *m.cur, *m.base, m.tol)
		}
	}
}

func caseKey(program string, sampleRate, kbps int) string {
	return fmt.Sprintf("%s|%d|%d", program, sampleRate, kbps)
}

// writeBaseline rewrites the committed baseline from the measured cases.
func writeBaseline(t *testing.T, cases []baselineCase) {
	t.Helper()
	doc := baselineFile{
		SchemaVersion: baselineSchemaVersion,
		Seconds:       baselineSeconds,
		Note:          "go-mp3 objective quality regression baseline; refresh with UPDATE_QUALITY_BASELINE=1 (a deliberate, reviewed action). Generated on amd64.",
		Cases:         cases,
	}
	if err := os.MkdirAll(filepath.Dir(baselinePath), 0o755); err != nil {
		t.Fatalf("create baseline dir: %v", err)
	}
	f, err := os.Create(baselinePath)
	if err != nil {
		t.Fatalf("create baseline: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Fatalf("close baseline: %v", cerr)
		}
	}()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		t.Fatalf("encode baseline: %v", err)
	}
	t.Logf("wrote %s (%d cases)", baselinePath, len(cases))
}

// readBaseline loads the committed baseline.
func readBaseline(t *testing.T) baselineFile {
	t.Helper()
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read baseline (generate it with UPDATE_QUALITY_BASELINE=1): %v", err)
	}
	var doc baselineFile
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}
	return doc
}
