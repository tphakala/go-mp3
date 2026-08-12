// Package enc is the clean-room MPEG-1 Layer III encoder for go-mp3.
//
// Provenance: every algorithm in this package is derived only from
// ISO/IEC 11172-3 (and its published errata) and from openly published
// papers on MPEG audio coding. No LGPL, GPL, or field-of-use-restricted
// codec source (LAME, Shine, dist10, libmad, mpg123, FFmpeg, Helix) is
// consulted during its development; LAME, ffmpeg, and mpg123 binaries are
// used only as black-box compatibility and quality references, never read
// as source. See PROVENANCE.md at the repository root.
//
// This package must never import internal/dec: the decoder (a function-
// by-function port of minimp3, see PROVENANCE.md) and the encoder are two
// separately sourced provenance tracks, and the import graph enforces that
// separation at compile time. Tests are the one sanctioned exception: a
// white-box reconstruction gate lives in internal/dec as a _test.go file
// (package dec) and imports internal/enc to drive the decoder's synthesis
// filterbank with this package's analysis output, because verifying the
// forward transform against its own inverse is the only oracle available
// for an encoder with no bit-exact reference stream.
package enc
