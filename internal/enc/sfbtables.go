package enc

// sfbWidthsLong[r] are the 22 long-block scalefactor band widths for
// MPEG-1 rate r (0 = 44100, 1 = 48000, 2 = 32000), ISO/IEC 11172-3 Table
// B.8. Recognizable as the standard MPEG-1 long-block sfBandIndex widths
// (cumulative band edges 0,4,8,... summing to 576 for each rate);
// TestEncSfbWidthsMatchDec (internal/dec/encx_huffman_test.go) confirms
// each row equals the decoder's scfLongTable[5/6/7][:22] (the decoder's
// ISO-derived rows for these same three MPEG-1 rates).
var sfbWidthsLong = [3][22]int{
	{4, 4, 4, 4, 4, 4, 6, 6, 8, 8, 10, 12, 16, 20, 24, 28, 34, 42, 50, 54, 76, 158},  // 44100 Hz
	{4, 4, 4, 4, 4, 4, 6, 6, 6, 8, 10, 12, 16, 18, 22, 28, 34, 40, 46, 54, 54, 192},  // 48000 Hz
	{4, 4, 4, 4, 4, 4, 6, 6, 8, 10, 12, 16, 20, 24, 30, 38, 46, 56, 68, 84, 102, 26}, // 32000 Hz
}

// sfbWidthsShort[r] are the 13 short-block scalefactor band widths (one
// window's worth of lines, ISO/IEC 11172-3 Table B.8) for MPEG-1 rate r
// (0 = 44100, 1 = 48000, 2 = 32000). Each row sums to 192, the line count
// of a single short window; a short granule's coding-order region for one
// sfb repeats this width three times, once per window (bandLayout,
// blocktypes.go). TestEncSfbWidthsShortMatchDec
// (internal/dec/encx_huffman_test.go) confirms each row triple-collapse-
// equals the decoder's scfShortTable[5/6/7] (the decoder's ISO-derived
// rows for these same three MPEG-1 rates); that gate caught and corrected
// a mis-transcription of the 44100 Hz row during this task's
// implementation (see task-A2-report.md).
var sfbWidthsShort = [3][13]int{
	{4, 4, 4, 4, 6, 8, 10, 12, 14, 18, 22, 30, 56}, // 44100 Hz
	{4, 4, 4, 4, 6, 6, 10, 12, 14, 16, 20, 26, 66}, // 48000 Hz
	{4, 4, 4, 4, 6, 8, 12, 16, 20, 26, 34, 42, 12}, // 32000 Hz
}

// SfbWidthsShortRow returns a copy of sfbWidthsShort[rate] (rate: 0=44100,
// 1=48000, 2=32000 Hz), for cross-checking against the decoder's
// scfShortTable rows. Returned by value for the same reason as
// SfbWidthsLongRow (huffman.go): a live pointer into the package-level
// table would let a cross-package caller mutate or race on it.
func SfbWidthsShortRow(rate int) [13]int { return sfbWidthsShort[rate] }
