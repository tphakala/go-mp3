package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/tphakala/go-mp3/internal/quality"
)

// Metric column names, shared by the report tables, the summary, and the
// win-direction table.
const (
	mSNR       = "SNR"
	mBandSNR   = "BandSNR"
	mSegSNR    = "SegSNR"
	mLSD       = "LSD"
	mPreEcho   = "PreEcho"
	mBandwidth = "Bandwidth"
	mMOS       = "MOS"
	mODG       = "ODG"
)

// metric describes one report column: how to read it from a result, and
// which direction counts as better. scored is false for a column that is
// reported but not judged, so it contributes a delta and no win.
//
// One table rather than three parallel ones on purpose: with a separate name
// slice, getter switch and direction map, adding a metric to one and not the
// others compiles and runs, and the failure is silent (an always-n/a column,
// or an inverted win count).
type metric struct {
	name   string
	get    func(*encoderResult) float64
	better int // +1 higher is better, -1 lower is better
	scored bool
}

var metrics = []metric{
	{mSNR, func(r *encoderResult) float64 { return r.Metrics.SNR }, +1, true},
	{mBandSNR, func(r *encoderResult) float64 { return r.Metrics.BandSNR }, +1, true},
	{mSegSNR, func(r *encoderResult) float64 { return r.Metrics.SegSNR }, +1, true},
	{mLSD, func(r *encoderResult) float64 { return r.Metrics.LSD }, -1, true},
	{mPreEcho, func(r *encoderResult) float64 { return r.Metrics.PreEcho }, -1, true},
	// Bandwidth is informational: LAME's lowpass is a deliberate bitrate
	// dependent choice, so "wider" is not "better" and scoring it would
	// award points for declining to lowpass.
	{mBandwidth, func(r *encoderResult) float64 { return r.Metrics.Bandwidth / 1000 }, +1, false},
	{mMOS, func(r *encoderResult) float64 { return r.MOS }, +1, true},
	{mODG, func(r *encoderResult) float64 { return r.ODG }, +1, true},
}

// report is the whole run: provenance header plus every case.
type report struct {
	GeneratedUTC string       `json:"generated_utc"`
	GoMP3Rev     string       `json:"gomp3_rev"`
	LAMEVersion  string       `json:"lame_version"`
	Tools        []string     `json:"tools"`
	Seconds      int          `json:"seconds"`
	Attempted    int          `json:"attempted"`
	Failed       int          `json:"failed"`
	Cases        []caseResult `json:"cases"`
}

// summaryKey groups the summary by BOTH sample rate and bitrate. Keying on
// bitrate alone silently merged a multi-rate run into one row per bitrate,
// while the detail tables above it stayed split by rate.
type summaryKey struct {
	SampleRate int
	Kbps       int
}

// summaryRow aggregates one (rate, bitrate) across programs.
type summaryRow struct {
	summaryKey
	Programs int
	// MeanDelta is the mean of (go-mp3 minus LAME) per metric over the
	// programs where both values are finite; NaN when none are.
	MeanDelta map[string]float64
	// Compared counts those programs, and Wins how many of them go-mp3 won.
	// Wins is printed over Compared, never over Programs: a metric no case
	// could measure would otherwise read as a clean sweep of losses.
	Compared map[string]int
	Wins     map[string]int
}

// summarize aggregates go-mp3 minus LAME deltas per (rate, bitrate).
func summarize(cases []caseResult) []summaryRow {
	rows := map[summaryKey]*summaryRow{}
	for i := range cases {
		c := &cases[i]
		key := summaryKey{c.SampleRate, c.Kbps}
		row := rows[key]
		if row == nil {
			row = &summaryRow{summaryKey: key, MeanDelta: map[string]float64{},
				Compared: map[string]int{}, Wins: map[string]int{}}
			rows[key] = row
		}
		row.Programs++
		for _, m := range metrics {
			g, l := m.get(&c.GoMP3), m.get(&c.LAME)
			if !finite(g) || !finite(l) {
				continue
			}
			row.MeanDelta[m.name] += g - l
			row.Compared[m.name]++
			if m.scored && (g-l)*float64(m.better) > 0 {
				row.Wins[m.name]++
			}
		}
	}
	out := make([]summaryRow, 0, len(rows))
	for _, row := range rows {
		for _, m := range metrics {
			if n := row.Compared[m.name]; n > 0 {
				row.MeanDelta[m.name] /= float64(n)
			} else {
				row.MeanDelta[m.name] = math.NaN()
			}
		}
		out = append(out, *row)
	}
	slices.SortFunc(out, func(a, b summaryRow) int {
		if a.SampleRate != b.SampleRate {
			return a.SampleRate - b.SampleRate
		}
		return a.Kbps - b.Kbps
	})
	return out
}

// finite reports whether v is a real measurement rather than a NaN or an
// infinity. The Markdown and the JSON both treat the two the same way.
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// notMeasured is the cell text for a figure that was not measured (the
// external tool was absent) or is undefined for that program.
const notMeasured = "n/a"

// fmtMetric renders a metric cell: notMeasured for anything not finite, two
// decimals otherwise.
func fmtMetric(v float64) string {
	if !finite(v) {
		return notMeasured
	}
	return fmt.Sprintf("%.2f", v)
}

// cell escapes a value for a Markdown table cell. Program names can come from
// a corpus file name, and a pipe or a newline there would split the cell or
// forge a whole row.
func cell(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(s)
}

// writeMarkdown renders the full report: header, one table per sample rate,
// then the per-bitrate summary.
func writeMarkdown(w io.Writer, r *report) error {
	var b strings.Builder
	b.WriteString("# go-mp3 versus LAME quality report\n\n")
	fmt.Fprintf(&b, "- Generated: %s\n- go-mp3: %s\n- LAME: %s\n- External metrics: %s\n- Program length: %d s\n- Cases: %d attempted, %d failed\n\n",
		r.GeneratedUTC, r.GoMP3Rev, r.LAMEVersion, orNone(r.Tools), r.Seconds, r.Attempted, r.Failed)
	fmt.Fprintf(&b, "Metrics: SNR, BandSNR (bins at or below %.0f kHz), SegSNR, MOS (ViSQOL MOS-LQO), ODG (PEAQ basic): higher is better. LSD, PreEcho: lower is better. Bandwidth (kHz) is informational and is not scored. delta is go-mp3 minus LAME. Lag is the measured alignment in samples: %d for this project's tagless streams, 0 for a gapless-trimmed LAME stream. n/a means the figure was not measured (the external tool was absent) or is undefined for that program (no attack detected, no active frame).\n\n",
		quality.BandLimitHz/1000, mp3TotalDelay)

	rates := map[int]bool{}
	for i := range r.Cases {
		rates[r.Cases[i].SampleRate] = true
	}
	sortedRates := slices.Collect(maps.Keys(rates))
	slices.Sort(sortedRates)
	for _, sr := range sortedRates {
		fmt.Fprintf(&b, "## %d Hz\n\n", sr)
		writeHeader(&b, "| Program | kbps | ch | go lag | LAME lag |", "|---|---|---|---|---|",
			[]string{"go", "LAME", "delta"})
		for i := range r.Cases {
			c := &r.Cases[i]
			if c.SampleRate != sr {
				continue
			}
			fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |", cell(c.Program), c.Kbps, c.Channels, c.GoMP3.Lag, c.LAME.Lag)
			for _, m := range metrics {
				g, l := m.get(&c.GoMP3), m.get(&c.LAME)
				fmt.Fprintf(&b, " %s | %s | %s |", fmtMetric(g), fmtMetric(l), fmtMetric(g-l))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Summary\n\nMean delta (go-mp3 minus LAME) per sample rate and bitrate across programs, and how many of the programs that could be compared go-mp3 won.\n\n")
	writeHeader(&b, "| Hz | kbps | programs |", "|---|---|---|", []string{"mean delta", "wins"})
	for _, row := range summarize(r.Cases) {
		fmt.Fprintf(&b, "| %d | %d | %d |", row.SampleRate, row.Kbps, row.Programs)
		for _, m := range metrics {
			wins := notMeasured
			if n := row.Compared[m.name]; n > 0 && m.scored {
				wins = fmt.Sprintf("%d/%d", row.Wins[m.name], n)
			}
			fmt.Fprintf(&b, " %s | %s |", fmtMetric(row.MeanDelta[m.name]), wins)
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeHeader emits a Markdown table header: the fixed leading cells, then
// one "<metric> <suffix>" cell per metric per suffix, then the separator row.
//
// It takes the suffixes rather than a format string on purpose. The previous
// shape passed a format and three arguments to every caller, and the Summary
// caller's format had only two verbs, so fmt appended %!(EXTRA string=...) to
// each of its eight header cells and the whole table stopped rendering.
func writeHeader(b *strings.Builder, lead, leadSep string, suffixes []string) {
	b.WriteString(lead)
	for _, m := range metrics {
		for _, s := range suffixes {
			fmt.Fprintf(b, " %s %s |", m.name, s)
		}
	}
	b.WriteString("\n" + leadSep)
	for range metrics {
		for range suffixes {
			b.WriteString("---|")
		}
	}
	b.WriteString("\n")
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// jsonEncoderResult mirrors encoderResult with nullable floats, because
// encoding/json rejects NaN.
type jsonEncoderResult struct {
	Name      string   `json:"name"`
	Lag       int      `json:"lag"`
	Bytes     int      `json:"bytes"`
	SNR       *float64 `json:"snr"`
	BandSNR   *float64 `json:"band_snr"`
	SegSNR    *float64 `json:"seg_snr"`
	LSD       *float64 `json:"lsd"`
	PreEcho   *float64 `json:"pre_echo"`
	PreEchoN  int      `json:"pre_echo_events"`
	Bandwidth *float64 `json:"bandwidth_hz"`
	MOS       *float64 `json:"mos"`
	ODG       *float64 `json:"odg"`
}

// nullable renders a non-finite measurement as JSON null, matching what
// fmtMetric renders as n/a.
func nullable(v float64) *float64 {
	if !finite(v) {
		return nil
	}
	return &v
}

// toJSONResult converts one encoder's result to its nullable-float mirror.
func toJSONResult(r *encoderResult) jsonEncoderResult {
	m := &r.Metrics
	return jsonEncoderResult{
		Name: r.Name, Lag: r.Lag, Bytes: r.Bytes,
		SNR: nullable(m.SNR), BandSNR: nullable(m.BandSNR), SegSNR: nullable(m.SegSNR), LSD: nullable(m.LSD),
		PreEcho: nullable(m.PreEcho), PreEchoN: m.PreEchoN, Bandwidth: nullable(m.Bandwidth),
		MOS: nullable(r.MOS), ODG: nullable(r.ODG),
	}
}

// MarshalJSON renders a caseResult with nullable metric floats. The pointer
// receiver is honored by encoding/json for the addressable elements of
// report.Cases.
func (c *caseResult) MarshalJSON() ([]byte, error) {
	type jc struct {
		Program    string            `json:"program"`
		Channels   int               `json:"channels"`
		SampleRate int               `json:"sample_rate"`
		Kbps       int               `json:"kbps"`
		GoMP3      jsonEncoderResult `json:"gomp3"`
		LAME       jsonEncoderResult `json:"lame"`
	}
	return json.Marshal(jc{c.Program, c.Channels, c.SampleRate, c.Kbps, toJSONResult(&c.GoMP3), toJSONResult(&c.LAME)})
}

// writeJSON writes the report as indented JSON.
func writeJSON(w io.Writer, r *report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
