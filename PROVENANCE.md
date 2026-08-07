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

The encoder (later phase) is an independent implementation from ISO/IEC
11172-3 and published literature. No LGPL, GPL, or field-of-use-restricted
codec source (LAME, Shine, dist10, libmad, mpg123, FFmpeg, Helix) is
consulted during its development. LAME, ffmpeg, and mpg123 binaries are
used only as black-box compatibility and quality references.

## Float parity fallback sites

Sites where Go cannot reproduce the oracle bit-exactly (tolerance at most
1 ULP) are listed here with reasons. Currently: none.
