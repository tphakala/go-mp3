# Provenance

## Decoder

The decoder in internal/dec is a function-by-function port of minimp3
(https://github.com/lieff/minimp3), dedicated to the public domain under
CC0-1.0. The port is pinned to upstream commit:

    ea99364f61c14656440e8d77e9c233ccf3124633

The vendored copy at tools/oracle/minimp3.h is byte-identical to that
commit's file (sha256 recorded in minimp3.h.sha256 and checked on every
oracle build). Dump hooks live only in tools/oracle/hooks.patch and are
applied into a build-time copy; the decoder port derives from the
pristine pin.

## Oracle build

Differential gates compare against a harness built from the vendored pin
with exactly these flags (the compiler and flags are part of the pin):

    cc -O2 -ffp-contract=off -DMINIMP3_NO_SIMD -DMINIMP3_FLOAT_OUTPUT -DMP3DUMP

## Encoder

The encoder in internal/enc is an independent implementation from ISO/IEC
11172-3 and published literature. No LGPL, GPL, or field-of-use-restricted
codec source (LAME, Shine, dist10, libmad, mpg123, FFmpeg, Helix) is
consulted during its development. LAME, ffmpeg, and mpg123 binaries are
used only as black-box compatibility and quality references. The
`tools/quality` harness drives the `lame` binary (and, when present, the
`visqol` and `peaq-odg` binaries) through `os/exec` only; no encoder source
is consulted, and the harness decodes both encoders' output through this
project's own decoder.

Its normative tables are transcribed directly from the standard: Table B.7
Huffman code tables (internal/enc/hufftables.go), Table B.8 scalefactor
band widths (internal/enc/sfbtables.go), Table B.9 alias-reduction c
coefficients converted to cs/ca (internal/enc/mdcttables.go), and Annex C
Table 3-C.1 analysis window (internal/enc/fbtables.go), plus the
closed-form window and twiddle definitions of Annex C section C.1.5.1. All
of these are generated as exact hex float literals, cross-checked against
the decoder's independently derived (minimp3/CC0) copies by the
internal/dec `encx_` test suite, with no third-party encoder source
consulted at any point.

## Float parity fallback sites

Sites where Go cannot reproduce the oracle bit-exactly are listed here with
reasons and the measured tolerance actually required.

- `internal/dec/encx_mdct_test.go`, `TestEncAliasCoefficientsMatchDec`
  (Task 3): `enc.AliasCA[5..7]` versus `gAA[1][5..7]` (float32 magnitude),
  measured 1/9/20 ULP. ISO/IEC 11172-3 Table B.9 publishes the `c`
  coefficients to only 2-4 significant decimal digits (e.g. `c[7] =
  -0.0037`), while minimp3's `g_aa[1]` literals carry 8 significant digits
  directly (`0.00369997f`) rather than being computed from `c` at compile
  time. Re-deriving `ca = c/sqrt(1+c*c)` from the low-precision published
  `c` cannot recover minimp3's extra digits, so the smallest-magnitude
  coefficients drift beyond the 1 ULP a straight float64-to-float32 rounding
  difference would explain. Asserted within 20 ULP (the measured worst
  case), not 1.
- `internal/dec/encx_mdct_test.go`, `TestEncMdctWindowMatchesDec`
  (Task 3): `enc.MDCTWindow` versus `gMdctWindow[0]` (float32), measured up
  to 2 ULP. Same root cause: minimp3's `g_mdct_window` literals are an
  8-digit decimal transcription (`0.04361938f`), not a `math.Sin` call, so a
  handful of entries round to a different nearest float32 than a
  full-precision `sin(pi/36*(i+0.5))` computation does. Asserted within
  2 ULP.
