// Package mp3 implements an MPEG-1/2/2.5 Layer III (MP3) decoder and an
// MPEG-1 Layer III encoder, in pure Go without cgo.
//
// The decoder is a port of minimp3 (CC0-1.0); see PROVENANCE.md. The
// encoder is an independent implementation derived from ISO/IEC 11172-3
// and published literature (see PROVENANCE.md): a fixed-bitrate CBR encoder
// with a psychoacoustic model, a bit reservoir, per-frame M/S joint stereo,
// and attack-driven short blocks (block switching). Quality at a given
// bitrate still lags a fully tuned encoder like LAME; further tuning is
// planned.
package mp3
