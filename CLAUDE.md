# go-mp3

Pure-Go MP3 codec: an MPEG-1/2/2.5 Layer III decoder and an MPEG-1 Layer III
CBR encoder, implemented without cgo. Module `github.com/tphakala/go-mp3`, MIT
licensed. A sibling to go-flac, go-opus, go-aac, and go-wav.

## Status

Decoder, streaming `pcm` container, CBR encoder, and the LAME quality harness
are all complete.

- **Decoder** (package `mp3`: `NewDecoder`, `DecodeFrame` returning
  `(n, FrameInfo, error)`, `Reset`; sentinels `ErrCorruptStream`,
  `ErrUnsupported`): a full MPEG-1/2/2.5 Layer III decoder producing float PCM,
  verified bit-exact on amd64 and arm64 across the fixtures and the ISO
  conformance corpus, fuzzed, resync-tested, and zero-alloc in steady state.
- **Streaming container** (package `pcm`: `NewDecoder`, `Read`, `WriteTo`,
  `SeekToSample`, `Reset`, `Info`, plus a `WithF32` option): an `io.Reader` and
  `io.WriterTo` over the frame decoder. It handles ID3v2 skip and ID3v1
  trailer, S16 by default with float32 via `WithF32`, Xing/Info/VBRI/LAME tag
  parsing, gapless trim (encoder delay plus the standard 529-sample decoder
  delay at the head, the padding less that delay at the tail, clamped at zero,
  the window ffmpeg and mpg123 apply; `Info` reports the raw tag fields, not the
  applied window), bounded resync surfacing `ErrCorruptStream`, clean-end and
  truncation detection, and bit-exact `SeekToSample`.
- **Encoder** (package `mp3`: `NewEncoder`, `EncodeFrame`, `Reset`, `Drained`,
  `Delay`, `TotalDelay`, `Stats`, `EncoderConfig`; constants `FrameSize`=1152,
  `DefaultBitrate`=128000, `EncoderDelay`=528, `TotalDelay`=1057; sentinels
  `ErrEncoderNotInitialized`, `ErrEncoderFinalized`): a fixed-bitrate CBR
  MPEG-1 Layer III encoder at 32/44.1/48 kHz, mono or L/R stereo, the 14 legal
  CBR bitrates. The pipeline covers the analysis filterbank, forward MDCT and
  alias reduction, a power-law quantizer with ISO B.7 Huffman, framing plus
  inner rate control plus a bit reservoir, psychoacoustic model 2, per-frame
  M/S joint stereo, and attack-driven short blocks. Output is bit-exact
  cross-arch, round-trip SNR gated, and accepted by ffmpeg and mpg123. The
  stream is tagless (no Xing or LAME header). VBR is not planned. Quality at a
  given bitrate still lags a fully tuned encoder like LAME; further tuning is
  the main open work, measured with the quality harness.
- **Quality harness** (`tools/quality`, metrics in `internal/quality`): `task
  quality` compares the encoder against the `lame` binary (black box only) on a
  deterministic synthetic corpus plus optional WAVs (`-corpus DIR`), decoding
  both through `pcm`, aligning by normalized cross-correlation, and reporting
  SNR, band-limited SNR (at or below 16 kHz), segmental SNR, log-spectral
  distance, pre-echo, bandwidth, and (when the optional `visqol` and `peaq-odg`
  scorers plus `ffmpeg` are on PATH) ViSQOL MOS-LQO and PEAQ ODG, as Markdown
  plus JSON under the gitignored `tools/quality/out/`. CI runs only the
  `TestQualityHarness*` smoke cases with `lame` installed.
  - Gotcha: a perfectly stationary periodic program cannot be aligned by
    cross-correlation (every lag one period apart scores the same), which is why
    the corpus gives its tonal programs a slow amplitude envelope and why the
    aligner normalizes by both windows' energy. Removing either brings back a
    silently misaligned measurement, not a test failure.

## Workflow conventions

- Cut a feature branch from `main`, one PR per grouped unit. Scaffolding and
  rules (this file, linter config, CI, README) may commit straight to `main`.
  Everything that changes behavior or a public symbol goes through a PR.
- Squash-merge each PR with a conventional subject and the PR number appended;
  no merge commits.
- `task check` runs build, vet, golangci-lint, and test, and must be green
  before every commit. Write idiomatic Go (range-over-int for counting loops,
  and so on); a faithful port that is genuinely complex may carry a justified
  `//nolint:gocognit,gocyclo`.
- Decoder units land with an oracle dump hook (patch-based, under
  `tools/oracle/`, never editing the pristine `minimp3.h`) plus a differential
  test that byte-compares Go output against the oracle. Run `task oracle:build`
  then `task oracle:dump` to regenerate dumps (gitignored); differential tests
  skip without them, or fail loudly under `MP3_REQUIRE_DUMPS=1`.
- Encoder units land with a bit-exact cross-arch golden gate, a round-trip SNR
  gate, a zero-alloc `EncodeFrame` gate, and structural validation, plus the
  ffmpeg/mpg123 decode-compatibility gate in CI. The encoder is
  GOLDEN-NEUTRAL: any change to production encoder code must keep output
  bit-exact against the committed goldens on amd64 and arm64, unless the PR is
  an explicit quality-tuning PR, which re-freezes the goldens on purpose and
  carries a before/after `task quality` summary in its description.

## Hard rules

- MIT provenance: the decoder derives only from the pinned minimp3 (CC0-1.0)
  vendored under `tools/oracle/`; the encoder derives only from ISO/IEC 11172-3
  and published papers. Never open LAME, Shine, dist10, libmad, mpg123, FFmpeg,
  or Helix source. LAME, ffmpeg, mpg123, and the harness's optional visqol and
  peaq-odg scorers are black-box binaries only. See PROVENANCE.md.
- ISO conformance vectors are never committed; fetch scripts only.
- Bit-exactness discipline for all DSP code (decoder and encoder): block FMA
  fusion with an explicit `float32(a*b)` conversion on any product feeding a
  `+` or `-`. A bare two-statement local (`t := a*b; t + c`) does NOT block
  fusion in Go (the compiler fuses across statements, verified via
  `GOARCH=arm64 go build -gcflags=-S`). The amd64 default `GOAMD64=v1` has no
  FMA instruction, so fusion bugs are invisible there and only the arm64
  differential (CI) catches them. Match C double-promotion and upstream
  operation order.
- Gotcha: `internal/enc/huffman.go` `chooseRegions`' `rcCost`/`rcSeen` memo
  arrays MUST stay `[40][40]`; do NOT shrink them. `chooseRegions` is reachable
  with `lay.nBands` up to 39 (a `blockLong`-typed granule over a short layout,
  as in `TestOuterLoopShortConverges`), so any smaller dimension truncates the
  memo and panics. The "a,b <= 22" comment holds only for the production
  long-block path.

## Next work

Encoder quality tuning against LAME. The recorded baseline shows the encoder
winning raw and segmental SNR at 128 and 192 kbps yet losing log-spectral
distance and PEAQ ODG at every bitrate below 256, the signature of a
psychoacoustic model that does not spend its bits where they matter rather than
a coding-noise problem. Aim the tuning there, and treat the low-bitrate cases
as the informative ones: the band-limited SNR advantage inverts at 256 and 320.
Every tuning PR re-runs the harness and reports the per-bitrate deltas in its
description. Such PRs are not golden-neutral: each re-freezes the encoder
goldens deliberately and runs the arm64 leg.
