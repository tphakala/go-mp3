# AGENTS.md

Orientation for coding agents and automated reviewers working on this repository. README.md is the user-facing reference (API examples, quality comparison, benchmarks); PROVENANCE.md is the clean-room record; this file is the map for changing and reviewing the code.

## What this is

`github.com/tphakala/go-mp3` is a pure-Go MP3 codec: an MPEG-1/2/2.5 Layer III decoder and an MPEG-1 Layer III CBR encoder, without cgo. It is MIT licensed and a sibling to go-flac, go-opus, go-aac and go-wav. It aims to:

1. **Build anywhere Go builds.** No cgo, no system codec library. SIMD kernels are optional and gated by a build tag.
2. **Produce identical bytes on every architecture.** Decoder output is bit-exact against the pinned minimp3 oracle on amd64 and arm64; encoder output is bit-exact against committed goldens on both, with and without SIMD.
3. **Stay clean-room.** The decoder derives only from minimp3 (CC0-1.0), the encoder only from ISO/IEC 11172-3 and published papers, so the whole module can be MIT.

| Component | Package | Status |
|---|---|---|
| Frame decoder | `mp3` (`NewDecoder`, `DecodeFrame` returning `(n, FrameInfo, error)`, `Reset`; sentinels `ErrCorruptStream`, `ErrUnsupported`) | complete: MPEG-1/2/2.5 Layer III to float PCM, bit-exact on amd64 and arm64 across the fixtures and the ISO conformance corpus, fuzzed, resync-tested, zero-alloc in steady state |
| Streaming container | `pcm` (`NewDecoder`, `Read`, `WriteTo`, `SeekToSample`, `Reset`, `Info`, `WithF32`; `DecodeInterleaved`, `DecodeInterleavedLimit`) | complete: ID3v2 skip and ID3v1 trailer, S16 by default or float32 via `WithF32`, Xing/Info/VBRI/LAME tag parsing, gapless trim, bounded resync, clean-end and truncation detection, bit-exact `SeekToSample` |
| Frame encoder | `mp3` (`NewEncoder`, `EncodeFrame`, `Reset`, `Drained`, `Delay`, `TotalDelay`, `Stats`, `EncoderConfig`, `RateControl`) | complete: CBR MPEG-1 Layer III at 32/44.1/48 kHz, mono or L/R stereo, the 14 legal CBR bitrates; quality tuning is the open work |
| Streaming encoder | `pcm` (`NewEncoder`, `EncodeInterleaved`, `Config`) | complete: writes a Xing/Info + LAME gapless tag by default |
| Quality harness | `tools/quality`, metrics in `internal/quality` | complete: black-box comparison against `lame`, plus a lame-free regression gate in CI |

Non-goals: Layer I and II decoding, VBR encoding.

### Decoder details

- Gapless trim applies encoder delay plus the standard 529-sample decoder delay at the head and the padding less that delay at the tail, clamped at zero: the window ffmpeg and mpg123 apply. `Info` reports the raw tag fields, not the applied window.
- Bounded resync surfaces `ErrCorruptStream`. A short final frame at true end of stream is reported as corruption, not silently accepted: at that point lost data and a legitimately short frame cannot be told apart.

### Encoder details

Constants: `FrameSize` = 1152, `DefaultBitrate` = 128000, `EncoderDelay` = 528, `TotalDelay` = 1057. Sentinels `ErrEncoderNotInitialized`, `ErrEncoderFinalized`.

The pipeline is the analysis filterbank, forward MDCT and alias reduction, a power-law quantizer with ISO B.7 Huffman, framing plus inner rate control plus a bit reservoir, psychoacoustic model 2, per-frame M/S joint stereo, and attack-driven short blocks. The inner rate loop picks each granule's global_gain by an exact upward scan (`RateControlExact`, the default) or a binary search (`RateControlFast`, about an order of magnitude faster on escalation-heavy frames, rarely and only slightly coarser, never finer). Both modes are cross-arch deterministic (integer selection), but Fast is not bit-identical to Exact.

The raw frame encoder's stream is tagless (no Xing or LAME header). The `pcm` encoder (`pcm.EncodeInterleaved`, and `pcm.NewEncoder` when its sink is an `io.WriteSeeker`) writes a Xing/Info + LAME gapless tag by default, so an encode then decode round trip is sample-accurate; `pcm.Config.OmitGaplessTag` opts out.

Quality at a given bitrate still lags a fully tuned encoder like LAME. See Next work.

### API stability

The public API is not stable before v1.0.0. Exported types, functions, struct fields and error types may change in any v0.x release when a cleaner design calls for it, and such a change ships as a minor version bump with a note in the release. Prefer the better API over compatibility: do not add deprecated aliases, compatibility shims or wrapper functions to keep an old signature working, and do not bend a design to avoid a breaking change. A changed signature still needs every caller in this repo updated, plus the README and godoc.

That freedom is bounded by the sibling codecs. go-mp3 is one of a family (go-flac, go-opus, go-aac, go-wav) that callers such as go-m4a, go-audio-testkit and BirdNET-Go use interchangeably, so the API follows their established shape rather than inventing its own:

- The `pcm` package carries the shared surface: `NewDecoder(r, opts...)` returning a `*Decoder` that is an `io.Reader` and `io.WriterTo` with `Info` and `Reset`; `NewEncoder(w, cfg)` with a `Config`; the one-shot `DecodeInterleaved`, `DecodeInterleavedLimit` (bounded by `DefaultMaxDecodedBytes`, `maxBytes <= 0` meaning unbounded, overflow returning a wrapped `ErrDecodeLimit`) and `EncodeInterleaved(w, cfg, pcm)`. Interleaved little-endian S16 is the default sample layout.
- Functional options are `Option func(*config)` with `With...` constructors.
- Sentinel errors are prefixed with the package path (`go-mp3:`, `go-mp3/pcm:`, `go-mp3/enc:`), matching the siblings' `go-flac/pcm:` style.
- A failed `Reset` must never leave a decoder or encoder that keeps working on the previous stream's state.
- Helpers such as the capped writer are duplicated per module on purpose; do not introduce a shared helper module between the codecs.

Before adding, renaming or reshaping an exported symbol, check how go-flac, go-opus and go-aac spell the same concept and match it. Diverge only where the format genuinely differs (an option that has no meaning for MP3), and say why in the PR. When a better design applies to the whole family, change the siblings in step rather than letting go-mp3 drift.

The design rules below are not part of this: they are the library's contract and hold across every release.

## Design rules you must not break

A change that violates one is wrong even if tests pass.

1. **MIT provenance.** The decoder derives only from the pinned minimp3 (CC0-1.0) vendored under `tools/oracle/`; the encoder derives only from ISO/IEC 11172-3 and published papers. Never open LAME, Shine, dist10, libmad, mpg123, FFmpeg or Helix source. LAME, ffmpeg, mpg123 and the harness's optional `visqol` and `peaq-odg` scorers are black-box binaries only. See PROVENANCE.md.
2. **Two separate provenance tracks.** `internal/enc` must never import `internal/dec`. The one sanctioned exception is test code in `internal/dec` (the `encx_*_test.go` files) that imports `internal/enc` to check the forward transforms against the decoder's inverse.
3. **ISO conformance vectors are never committed.** They are fetched by `scripts/fetch-vectors.sh` into the gitignored `testdata/vectors/`.
4. **Bit-exactness discipline for all DSP code.** Block FMA fusion with an explicit `float32(a*b)` (or `float64(a*b)`) conversion on any product feeding a `+` or `-`. A bare two-statement local (`t := a*b; t + c`) does NOT block fusion in Go: the compiler fuses across statements (verified via `GOARCH=arm64 go build -gcflags=-S`). The amd64 default `GOAMD64=v1` has no FMA instruction, so fusion bugs are invisible there and only the arm64 legs in CI catch them. Match C double-promotion and upstream operation order in the decoder port.
5. **Deterministic math in the encoder.** `math.Log`, `Exp`, `Exp2`, `Pow` and other libm transcendentals are not cross-arch bit-portable and are banned from `internal/enc` production code by the `EncNoLibmTranscendentals` ruleguard rule (`rules/detmath.go`). Use `plog`/`pexp2` (`internal/enc/detmath.go`), the literal hex-float tables, `sqrt(x*sqrt(x))` for x^0.75 and `Ldexp` plus `pow2Quarter` for 2^(q/4). Tests may use the stdlib as a reference with stated tolerances.
6. **The encoder is golden-neutral.** Any change to production encoder code must keep output bit-exact against the committed goldens on amd64 and arm64, with and without the `noasm` tag, unless the PR is an explicit quality-tuning PR, which re-freezes the goldens on purpose and carries a before/after `task quality` summary in its description.
7. **Zero allocations in steady state.** `DecodeFrame`, `EncodeFrame` and the internal decode and encode paths are allocation-free after warm-up; alloc tests guard this. Error paths may allocate.
8. **SIMD stays mp3-local and optional.** The kernels in `internal/enc` (`rangecost_kernel_*`, `escfamily_kernel_*`: AVX2 and NEON with a pure-Go reference) are gated by the `noasm` tag: the default build links them, `-tags noasm` compiles the pure-Go dispatcher. `github.com/tphakala/simd` may be used where a generic primitive fits, but mp3-specific fused kernels (bit-exact Huffman and DSP logic) live in this repo and are never pushed up into the simd library. Each kernel carries a differential parity and fuzz test against its reference.
9. **`chooseRegions`' memo arrays stay `[40][40]`.** `rcCost`/`rcSeen` in `internal/enc/huffman.go` are reachable with `lay.nBands` up to 39 (a `blockLong`-typed granule over a short layout, as in `TestOuterLoopShortConverges`), so any smaller dimension truncates the memo and panics. The "a,b <= 22" comment holds only for the production long-block path.

## Layout

```
doc.go                 Package mp3 godoc
decoder.go             Public frame decoder (wraps internal/dec)
encoder.go             Public frame encoder, EncoderConfig, RateControl (wraps internal/enc)
compat_test.go         ffmpeg/mpg123 decode-compatibility grid (MP3_REQUIRE_COMPAT)

internal/bits/         Bit reader and writer
internal/dec/          minimp3 port: header, sideinfo, scalefactors, huffman, stereo, reorder,
                       imdct, synth; dumps_test.go and conformance_test.go are the oracle and
                       ISO differentials; encx_*_test.go check encoder transforms against the
                       decoder's inverse
internal/enc/          Clean-room encoder: filterbank, mdct, psymodel (model 2), blockswitch,
                       stereo, quantize, loop (rate control), huffman, reservoir, frame;
                       detmath.go plus the *tables.go hex-float tables; SIMD kernels in
                       rangecost_kernel_* and escfamily_kernel_* (_amd64.s, _arm64.s, _fallback.go)
internal/quality/      Harness metrics: alignment, SNR family, log-spectral distance, pre-echo,
                       synthetic programs, WAV I/O
internal/testsignal/   Deterministic test signals shared by tests

pcm/                   Streaming decoder (Read, WriteTo, SeekToSample), one-shot decode and
                       encode, streaming encoder, ID3, Xing/Info, VBRI and LAME tag parse and write

tools/oracle/          Pinned minimp3.h (sha256 checked), hooks.patch, mp3dump.c, build and dump
                       scripts; build/, dumps/, mutated/ are generated and gitignored
tools/quality/         `task quality` CLI, the CI regression gate and testdata/baseline.json
scripts/               fetch-vectors.sh (ISO vectors), gen-fixtures.sh (fixtures via ffmpeg + lame)
rules/                 gocritic ruleguard matchers (build tag `ruleguard`, lint-only)
testdata/fixtures/     Committed LAME-encoded fixtures and two corrupt streams
```

## Testing approach

- **Decoder oracle differentials.** Every decoder unit has an oracle dump hook (patch-based, in `tools/oracle/hooks.patch`, never editing the pristine `minimp3.h`) and a differential test that byte-compares Go output against the oracle. `task oracle:build` then `task oracle:dump` regenerates the gitignored dumps; the differential tests skip without them, or fail loudly under `MP3_REQUIRE_DUMPS=1`. `task diff` fetches vectors, builds and dumps the oracle, and runs the oracle and conformance tests. The Oracle workflow runs this on both arches.
- **Encoder goldens.** `TestEncodeGolden`, `TestEncodeGoldenTransient` and related cases in `internal/enc/encoder_test.go` freeze sha256 of full encoded streams. With them every encoder unit lands with a round-trip SNR gate, a zero-alloc `EncodeFrame` gate, structural validation, and the ffmpeg/mpg123 compat gate in CI.
- **Quality gate.** `TestQualityBaseline` (`MP3_QUALITY_BASELINE=1`, `task quality:gate`) re-measures go-mp3's own objective quality on a small deterministic corpus and fails when a metric regresses past tolerance against `tools/quality/testdata/baseline.json`. It needs no external binary and runs in the test job on both arches. Refresh the baseline deliberately with `task quality:gate:update` on amd64 and review the diff. `TestQualityGateActivatedInCI` fails if the CI env line that enables the gate disappears.
- **External binaries** are opt-in hard requirements: `MP3_REQUIRE_COMPAT=1` (ffmpeg and mpg123), `MP3_REQUIRE_LAME=1`, `MP3_REQUIRE_FFMPEG=1`. Without them those tests skip.
- **Alignment gotcha.** A perfectly stationary periodic program cannot be aligned by cross-correlation (every lag one period apart scores the same), which is why the corpus gives its tonal programs a slow amplitude envelope and why the aligner normalizes by both windows' energy. Removing either brings back a silently misaligned measurement, not a test failure.

New behaviour gets a test. Bug fixes get a regression test that fails before the fix. A test must be able to fail: check that the assertion would break if the fixed line were reverted.

## Commands

Go 1.27, golangci-lint v2.13.2 (pinned in `GOLANGCI_LINT_VERSION` in `.github/workflows/ci.yml`). Tasks are in `Taskfile.yml`.

```
task check               # build, vet, golangci-lint, test (must be green before every commit)
task test                # go test ./... -timeout 20m (the suite is compute-heavy)
task lint
go test -tags noasm ./internal/enc -timeout 20m      # pure-Go fallback, as CI runs it
task diff                # vectors + oracle build + dumps + oracle/conformance tests
task compat              # ffmpeg/mpg123 compat grid (fails if either is missing)
task quality -- <flags>  # full LAME comparison, report in tools/quality/out/
task quality:smoke       # harness end to end against lame
task quality:gate        # the lame-free regression gate CI runs
task quality:gate:update # refresh the committed baseline (deliberate, on amd64)
```

CI runs the test, `noasm` and compat jobs on amd64 and arm64 (`ubuntu-24.04-arm`); the arm64 legs are the FMA and float-parity gate. For local arm64 runs, cross-compile the test binaries and run them on real arm64 hardware rather than under QEMU.

## Conventions

- Cut a feature branch from `main`, one PR per grouped unit. Scaffolding and rules (this file, linter config, CI, README) may commit straight to `main`; everything that changes behaviour or a public symbol goes through a PR.
- Squash-merge each PR with a Conventional Commits subject and the PR number appended (`feat(pcm): ... (#72)`); no merge commits.
- Lint config is `.golangci.yaml`, the shared standard of the codec family (gocritic with ruleguard, revive, errorlint, exhaustive with `default` counting as exhaustive, gocognit 50).
- Write idiomatic, current Go (range-over-int for counting loops and so on). A faithful port that is genuinely complex may carry a justified `//nolint:gocognit,gocyclo`.
- Comments explain why (the ISO clause, the minimp3 function, the fusion or determinism hazard being avoided). Match the existing density.
- Text style: no em or en dashes anywhere (code, comments, docs, commit messages); use commas, colons, parentheses or a plain hyphen.

## Review guidance

For CodeRabbit, Copilot and human reviewers.

Flag these, they are real defects here:

- Any break of the design rules above, above all a product feeding `+` or `-` without an explicit float conversion, a libm transcendental in `internal/enc`, or an `internal/enc` import of `internal/dec`.
- A change to production encoder code that moves the goldens outside a declared quality-tuning PR, or a quality-tuning PR without a before/after `task quality` summary.
- A decoder change without a matching oracle differential, or a differential that cannot fail.
- An allocation on the steady-state decode or encode path.
- A SIMD kernel without a parity test against its pure-Go reference, or one that changes output with `-tags noasm`.
- An exported API that diverges from the go-flac/go-opus/go-aac shape without a stated reason.
- A committed ISO vector, oracle dump or generated output.

Intentional, do not flag:

- Explicit `float32(a*b)` and `float64(a*b)` conversions that look redundant; they block FMA fusion.
- Hex-float literal tables in `internal/enc` instead of computed values; they are what keeps output cross-arch identical.
- `//nolint:gocognit,gocyclo` on faithful minimp3 ports and the rate loop.
- The `[40][40]` memo dimensions in `chooseRegions`.
- `math.Log`, `math.Exp` and similar in test files.
- Tests that skip when oracle dumps, vectors or external binaries are missing; CI sets the `MP3_REQUIRE_*` variables where it matters.
- Duplicated helpers (capped writer, decode limits) that also exist in sibling modules.
- Breaking changes to the exported API before v1.0.0 (see API stability), and the absence of deprecated aliases or shims for the old form.

## Next work

Encoder quality tuning against LAME. The recorded baseline shows the encoder winning raw and segmental SNR at 128 and 192 kbps yet losing log-spectral distance and PEAQ ODG at every bitrate below 256, the signature of a psychoacoustic model that does not spend its bits where they matter rather than a coding-noise problem. Aim the tuning there, and treat the low-bitrate cases as the informative ones: the band-limited SNR advantage inverts at 256 and 320. Every tuning PR re-runs the harness, reports the per-bitrate deltas in its description, re-freezes the encoder goldens and the quality baseline deliberately, and runs the arm64 leg.

## Things that do not belong in the repo

- ISO conformance vectors (`testdata/vectors/`), the local WAV corpus (`testdata/corpus/`), oracle builds and dumps, fuzz corpora and harness output (`tools/quality/out/`).
- Planning documents (`docs/`), local assistant notes (`*.local.md`), and editor and agent state (`.serena/`, `.codegraph/`, `.superpowers/`).
- Build outputs (`*.test`, `*.exe`, profiles, coverage).
