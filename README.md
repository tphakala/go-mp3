# go-mp3

Pure-Go MP3 (MPEG-1/2/2.5 Layer III) decoder and, in later phases, encoder,
implemented without cgo. MIT licensed. Work in progress.

The decoder is validated bit-exactly against a pinned minimp3 C oracle across
amd64 and arm64. The encoder is an independent implementation from ISO/IEC
11172-3 and published literature. See PROVENANCE.md for licensing provenance.
