package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"
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

// metricNames is the report column order; higherBetter drives the summary's
// win counting.
var metricNames = []string{mSNR, mBandSNR, mSegSNR, mLSD, mPreEcho, mBandwidth, mMOS, mODG}

var higherBetter = map[string]bool{
	mSNR: true, mBandSNR: true, mSegSNR: true, mLSD: false, mPreEcho: false,
	mBandwidth: true, mMOS: true, mODG: true,
}

// metricValue reads one named metric from an encoder result (Bandwidth in
// kHz for readability).
func metricValue(r *encoderResult, name string) float64 {
	switch name {
	case mSNR:
		return r.Metrics.SNR
	case mBandSNR:
		return r.Metrics.BandSNR
	case mSegSNR:
		return r.Metrics.SegSNR
	case mLSD:
		return r.Metrics.LSD
	case mPreEcho:
		return r.Metrics.PreEcho
	case mBandwidth:
		return r.Metrics.Bandwidth / 1000
	case mMOS:
		return r.MOS
	case mODG:
		return r.ODG
	}
	return math.NaN()
}

// report is the whole run: provenance header plus every case.
type report struct {
	GeneratedUTC string       `json:"generated_utc"`
	GoMP3Rev     string       `json:"gomp3_rev"`
	LAMEVersion  string       `json:"lame_version"`
	Tools        []string     `json:"tools"`
	Seconds      int          `json:"seconds"`
	Cases        []caseResult `json:"cases"`
}

// summaryRow aggregates one bitrate across programs.
type summaryRow struct {
	Kbps     int
	Programs int
	// MeanDelta is the mean of (go-mp3 minus LAME) per metric over the
	// programs where both values are finite; NaN when none are.
	MeanDelta map[string]float64
	// Wins counts the programs where go-mp3 beats LAME on that metric.
	Wins map[string]int
}

// summarize aggregates go-mp3 minus LAME deltas per bitrate across programs.
func summarize(cases []caseResult) []summaryRow {
	byKbps := map[int]*summaryRow{}
	counts := map[int]map[string]int{}
	for i := range cases {
		c := &cases[i]
		row := byKbps[c.Kbps]
		if row == nil {
			row = &summaryRow{Kbps: c.Kbps, MeanDelta: map[string]float64{}, Wins: map[string]int{}}
			byKbps[c.Kbps] = row
			counts[c.Kbps] = map[string]int{}
		}
		row.Programs++
		for _, m := range metricNames {
			g, l := metricValue(&c.GoMP3, m), metricValue(&c.LAME, m)
			if math.IsNaN(g) || math.IsNaN(l) {
				continue
			}
			row.MeanDelta[m] += g - l
			counts[c.Kbps][m]++
			if (higherBetter[m] && g > l) || (!higherBetter[m] && g < l) {
				row.Wins[m]++
			}
		}
	}
	rows := make([]summaryRow, 0, len(byKbps))
	for kbps, row := range byKbps {
		for _, m := range metricNames {
			if n := counts[kbps][m]; n > 0 {
				row.MeanDelta[m] /= float64(n)
			} else {
				row.MeanDelta[m] = math.NaN()
			}
		}
		rows = append(rows, *row)
	}
	slices.SortFunc(rows, func(a, b summaryRow) int { return a.Kbps - b.Kbps })
	return rows
}

// fmtMetric renders a metric cell: n/a for NaN, two decimals otherwise.
func fmtMetric(v float64) string {
	if math.IsNaN(v) {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", v)
}

// writeMarkdown renders the full report: header, one table per sample rate,
// then the per-bitrate summary.
func writeMarkdown(w io.Writer, r *report) error {
	var b strings.Builder
	b.WriteString("# go-mp3 versus LAME quality report\n\n")
	fmt.Fprintf(&b, "- Generated: %s\n- go-mp3: %s\n- LAME: %s\n- External metrics: %s\n- Program length: %d s\n\n",
		r.GeneratedUTC, r.GoMP3Rev, r.LAMEVersion, orNone(r.Tools), r.Seconds)
	b.WriteString("Metrics: SNR, BandSNR (bins at or below 16 kHz), SegSNR, MOS (ViSQOL MOS-LQO), ODG (PEAQ basic): higher is better. LSD, PreEcho: lower is better. Bandwidth is informational (kHz). delta is go-mp3 minus LAME.\n\n")

	rates := map[int]bool{}
	for i := range r.Cases {
		rates[r.Cases[i].SampleRate] = true
	}
	sortedRates := slices.Collect(maps.Keys(rates))
	slices.Sort(sortedRates)
	for _, sr := range sortedRates {
		fmt.Fprintf(&b, "## %d Hz\n\n", sr)
		writeTableHeader(&b, "| Program | kbps | ch |", "|---|---|---|", " %s go | %s LAME | %s delta |", "---|---|---|")
		for i := range r.Cases {
			c := &r.Cases[i]
			if c.SampleRate != sr {
				continue
			}
			fmt.Fprintf(&b, "| %s | %d | %d |", c.Program, c.Kbps, c.Channels)
			for _, m := range metricNames {
				g, l := metricValue(&c.GoMP3, m), metricValue(&c.LAME, m)
				fmt.Fprintf(&b, " %s | %s | %s |", fmtMetric(g), fmtMetric(l), fmtMetric(g-l))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Summary\n\nMean delta (go-mp3 minus LAME) per bitrate across programs, and the number of programs go-mp3 wins.\n\n")
	writeTableHeader(&b, "| kbps | programs |", "|---|---|", " %s mean delta | %s wins |", "---|---|")
	for _, row := range summarize(r.Cases) {
		fmt.Fprintf(&b, "| %d | %d |", row.Kbps, row.Programs)
		for _, m := range metricNames {
			fmt.Fprintf(&b, " %s | %d/%d |", fmtMetric(row.MeanDelta[m]), row.Wins[m], row.Programs)
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeTableHeader emits a Markdown table header: the fixed leading cells,
// one perMetric group (formatted with the metric name repeated for each %s)
// per metric, then the separator row.
func writeTableHeader(b *strings.Builder, lead, leadSep, perMetric, perMetricSep string) {
	b.WriteString(lead)
	for _, m := range metricNames {
		fmt.Fprintf(b, perMetric, m, m, m) //nolint:govet // the two-%s variant ignores the third argument by design
	}
	b.WriteString("\n" + leadSep)
	for range metricNames {
		b.WriteString(perMetricSep)
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

func nullable(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

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
