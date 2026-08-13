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
