# go-mp3

Pure-Go MP3 (MPEG-1/2/2.5 Layer III decoder, MPEG-1 Layer III encoder)
implemented without cgo, `github.com/tphakala/go-mp3`, MIT licensed. Sibling of
go-flac, go-opus, go-aac, go-wav.

**Status (as of 2026-09-01): decoder, streaming `pcm` container, CBR
encoder, and the LAME quality harness are all COMPLETE and merged.**

- **Decoder** (public package `mp3`: `NewDecoder` / `DecodeFrame` returning
  `(n, FrameInfo, error)` / `Reset`, sentinels `ErrCorruptStream`,
  `ErrUnsupported`): a full MPEG-1/2/2.5 Layer III decoder, a port of the
  pinned minimp3 oracle (CC0-1.0). Bit-exact float PCM against that oracle on
  amd64 AND arm64 across every fixture and the ISO conformance corpus, fuzzed,
  resync-tested, zero-alloc in steady state.
- **Streaming container** (public package `pcm`: `NewDecoder` / `Read` /
  `WriteTo` / `SeekToSample` / `Reset` / `Info`, plus a `WithF32` option): an
  `io.Reader` + `io.WriterTo` over the frame decoder. ID3v2 skip + ID3v1
  trailer, S16 default plus float32 via `WithF32`, Xing/Info/VBRI/LAME tag
  parsing, gapless trim (LAME delay plus the standard 529-sample decoder
  delay at the head, the padding less that delay at the tail, never below
  zero, the window ffmpeg and mpg123 apply; `Info` reports the RAW tag
  fields, not the applied window), bounded resync surfacing
  `mp3.ErrCorruptStream`,
  clean-end/truncation detection, and bit-exact `SeekToSample`. Fuzz +
  streaming conformance run bit-exact on amd64 + arm64 in the CI Oracle job.
- **Encoder** (public package `mp3`: `NewEncoder` / `EncodeFrame` / `Reset` /
  `Drained` / `Delay` / `TotalDelay` / `Stats` / `EncoderConfig`; constants
  `FrameSize`=1152, `DefaultBitrate`=128000, `EncoderDelay`=528,
  `TotalDelay`=1057; sentinels `ErrEncoderNotInitialized`,
  `ErrEncoderFinalized`): a fixed-bitrate CBR MPEG-1 Layer III encoder,
  32/44.1/48 kHz, mono or L/R stereo, the 14 legal CBR bitrates. It carries a
  full pipeline: analysis filterbank + forward MDCT + alias reduction,
  power-law quantizer + ISO B.7 Huffman, framing + inner rate control + a bit
  reservoir, psychoacoustic model 2 (deterministic FFT) driving a perceptual
  outer loop, per-frame M/S joint stereo, and attack-driven short blocks
  (block switching). Output is bit-exact cross-arch, round-trip SNR gated, and
  accepted by ffmpeg + mpg123 in CI. The stream is tagless (no Xing/LAME
  header). VBR is NOT planned (dropped; `EncoderConfig.Quality` was removed
  before the first tag). Quality at a given bitrate still lags a fully tuned
  encoder like LAME; further tuning is the main open work, measured with the
  quality harness below.
- **Quality harness** (`tools/quality`, metrics in `internal/quality`): `task
  quality` compares the encoder against the `lame` binary (black box only) on
  a deterministic synthetic corpus (11 programs: tonal, pink noise, click
  trains, sweep, bird-like chirps, speech-like, three stereo) plus optional
  WAVs (`-corpus DIR`, measured only at their own rate), decoding both
  through `pcm`, aligning by NORMALIZED cross-correlation, and reporting SNR,
  band-limited SNR (at or below 16 kHz),
  segmental SNR, log-spectral distance, pre-echo, bandwidth, and (when
  `visqol` / `peaq-odg` plus `ffmpeg` are on PATH) ViSQOL MOS-LQO and PEAQ
  ODG, as Markdown plus JSON under the gitignored `tools/quality/out/`. On
  this host
  `~/bin/visqol` wraps the `docker.io/jonashaag/visqol:v3` container (rootless
  podman, cwd mounted at /data, so pass relative paths) and `~/bin/peaq-odg`
  wraps a GstPEAQ build under `~/.local`; CI has neither and runs only the
  `TestQualityHarness*` smoke (two cases) in the Compat job with `lame`
  installed. PEAQ basic prints `-nan` when a basic-model MOV has no active
  frames (for instance a reference with no content above 8.1 kHz); the
  harness records that as n/a.
  GOTCHA: a perfectly stationary periodic program cannot be aligned by
  cross-correlation (every lag one period apart scores the same), which is
  why the corpus gives its tonal programs a slow amplitude envelope and why
  the aligner normalizes by both windows' energy. Removing either brings
  back a silently misaligned measurement, not a test failure.

The encoder was built across Phases 3-5 (skeleton, psychoacoustic model + rate
control, tuning), each an agy-reviewed plan derived only from ISO/IEC 11172-3
and published papers per PROVENANCE.md. All merged.

## Start here (fresh session)

1. Recall project memories from the Hindsight bank `go-mp3`
   (`mcp__hindsight-memory-go-mp3__recall`), query "resume". The bank holds
   the authoritative resume state: merged commits, conventions, gotchas, and
   the current carry-forward items. Prefer it over this file for the latest
   fine-grained state; this file is the durable overview.
2. Completed phase plans and their SDD ledgers live under `docs/` and
   `.superpowers/` (both gitignored, local only): Phase 0+1 (decoder) and
   Phase 2 (pcm) dated 2026-08-06/07, Phase 3 (encoder skeleton) dated
   2026-08-12, Phases 4-5 (psymodel, rate control, tuning) in the encoder-era
   plans. They remain for history.
3. NEXT work: encoder quality tuning against the LAME baseline. The baseline
   exists: 44 cases at 44.1 kHz, 0 failed, taken 2026-09-02 against LAME
   3.100, recorded in the Hindsight bank under the tag `baseline` and locally
   at `tools/quality/out/baseline-44k.{md,json}` (gitignored). Re-run it with
   `task quality`. It says the encoder wins raw and segmental SNR decisively
   at 128 and 192 kbps yet loses log-spectral distance and PEAQ ODG at every
   bitrate below 256, which is the signature of a psychoacoustic model not
   spending its bits where they matter rather than a coding-noise problem.
   Aim the tuning there, and treat the low-bitrate cases as the informative
   ones: the band-limited SNR advantage inverts at 256 and 320. Every tuning
   PR re-runs the harness and reports the Summary-table deltas per bitrate in
   its PR description. Such PRs are NOT golden-neutral: each one re-freezes
   the encoder goldens deliberately and runs the arm64 leg. Open GitHub
   issue: #53, batched low-severity harness follow-ups, none of them blocking.

## Workflow conventions (established this project)

- Cut a feature branch from `main`, one PR per grouped unit. Scaffolding and
  rules (this file, linter config, CI, README) commit straight to `main` with
  no PR (they have no plausible reviewer objection). Everything that changes
  behavior or a public symbol goes through a PR.
- Squash-merge each PR with a conventional subject and the PR number appended
  (matches go-flac/go-aac/go-wav); no merge commits. The process is plan,
  branch, implement, test, then the pre-push gate (`/gate`), `/watch-pr`, and
  `/wrapup`.
- `task check` runs build + vet + golangci-lint + test and must be green
  before every commit. golangci-lint uses the shared sibling config
  (`.golangci.yaml` + `rules/*.go` + the ruleguard dsl anchored by
  `tools/tools.go`). Write idiomatic Go (range-over-int for counting loops,
  etc.); a faithful port that is genuinely complex may carry a justified
  `//nolint:gocognit,gocyclo`.
- Decoder units land with an oracle dump hook (patch-based, in
  `tools/oracle/hooks.patch`, never editing the pristine `minimp3.h`) plus a
  differential test that byte-compares Go output against the oracle. Run
  `task oracle:build` then `task oracle:dump` to regenerate dumps
  (gitignored); differential tests skip without them, or fail loudly under
  `MP3_REQUIRE_DUMPS=1`.
- Encoder units land with a bit-exact cross-arch golden gate, a round-trip
  SNR gate, a zero-alloc `EncodeFrame` gate, and structural validation, plus
  the ffmpeg/mpg123 decode-compatibility gate in CI. The encoder is
  GOLDEN-NEUTRAL: any change to production encoder code must keep output
  bit-exact against the committed goldens on amd64 AND arm64, unless the PR
  is an explicit quality-tuning PR, which re-freezes the goldens on purpose
  and carries a before/after `task quality` summary in its description.

## Hard rules

- MIT provenance: the decoder derives only from the pinned minimp3 (CC0-1.0)
  vendored under `tools/oracle/`; the encoder derives only from ISO/IEC
  11172-3 and published papers. Never open LAME, Shine, dist10, libmad,
  mpg123, FFmpeg, or Helix source. LAME/ffmpeg/mpg123, and the quality
  harness's optional visqol/peaq-odg scorers, are black-box binaries only.
  See PROVENANCE.md.
- `docs/` is private planning material: gitignored, never committed, never
  pushed. The SDD ledger under `.superpowers/` is likewise local only.
- ISO conformance vectors are never committed; fetch scripts only.
- Bit-exactness discipline for all DSP code (decoder and encoder): follow the
  plans' Global Constraints (FMA blocking on arm64, C double-promotion
  matching, upstream operation order). The oracle builds with
  `-ffp-contract=off`, so the arm64 anti-fusion discipline is load-bearing.
  IMPORTANT: block FMA fusion with an explicit `float32(a*b)` conversion on
  any product feeding a `+`/`-`; a bare two-statement local (`t := a*b;
  t + c`) does NOT block fusion in Go 1.26 (the compiler fuses across
  statements, verified via `GOARCH=arm64 go build -gcflags=-S`). amd64 default
  `GOAMD64=v1` has no FMA instruction, so fusion bugs are invisible there and
  only the arm64 differential (CI) catches them.
- Gotcha: `internal/enc/huffman.go` `chooseRegions`' `rcCost`/`rcSeen` memo
  arrays MUST stay `[40][40]`; do NOT shrink them. `chooseRegions` is
  reachable with `lay.nBands` up to 39 (a `blockLong`-typed granule over a
  short layout, as in `TestOuterLoopShortConverges`), so any smaller dimension
  truncates the memo and panics. The "a,b <= 22" comment holds only for the
  production long-block path.
- Peer review partner: agy (Gemini 3.1 Pro High for design and code review,
  Flash for research); review every phase plan before executing it.
