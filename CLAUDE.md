# go-mp3

Pure-Go MP3 (MPEG Layer III) codec library, `github.com/tphakala/go-mp3`,
MIT licensed. Sibling of go-flac, go-opus, go-aac, go-wav.

**Status: Phase 0+1 COMPLETE, all 13 of 13 tasks done and merged (as of
2026-08-07). The pure-Go MPEG-1 Layer III decoder produces full-frame float
PCM bit-exact with the pinned minimp3 oracle on amd64 AND arm64, across every
fixture and the ISO conformance vector corpus: frame sync, side info,
scalefactors, Huffman and dequantization, stereo/reorder/antialias, hybrid
IMDCT with overlap, synthesis filterbank, and full-frame decode
(mp3dec_decode_frame). It is fuzzed, resync-tested, zero-alloc in steady
state, and exposed through the public package `mp3` (NewDecoder / DecodeFrame
returning (n, FrameInfo, error) / Reset, sibling-faithful to
go-flac/go-aac/go-wav). Phase 2 (pcm container) is UNDERWAY: PRs 8-9 merged, a
streaming `pcm.Decoder` (io.Reader + io.WriterTo) with ID3v2 skip, S16 default
plus a WithF32 option, Xing/Info/VBRI/LAME tag parsing, and gapless trim (the
LAME-gapless conformance discrepancy closes to zero). Remaining Phase 2: PR10
(SeekToSample + streaming robustness) and PR11 (fuzz + streaming conformance),
then Phases 3-5 (encoder). No encoder exists yet.**

## Start here (fresh session)

1. Recall project memories from the Hindsight bank `go-mp3`
   (`mcp__hindsight-memory-go-mp3__recall`), query "resume". The bank holds
   the authoritative resume state: merged commits, conventions, and the
   carry-forward items below.
2. The active plan is Phase 2 (pcm container):
   `docs/superpowers/plans/2026-08-07-go-mp3-phase2-pcm-container.md`
   (agy-reviewed), ledger
   `.superpowers/sdd/2026-08-07-go-mp3-phase2-pcm-container/progress.md`
   (local, gitignored), which records each task's status, commit range, the
   review findings folded in, and the authoritative carry-forward list. The
   completed Phase 0+1 plan/ledger (dated 2026-08-06) remain for history.
3. Phase 0+1 is DONE (13 tasks, PRs 1-6 + cleanup PR7). Phase 2 is UNDERWAY.
   DONE: PR8 = T1 streaming skeleton + T2 Xing/Info (fbd786f); PR9 = T3
   VBRI/LAME/gapless + T5 WithF32 (d3c03aa). NEXT: PR10 = T4 (SeekToSample) +
   T6 (streaming robustness); then PR11 = T7 (fuzz + streaming conformance).
   KEY carry-forwards (full list in the Phase 2 ledger): T4 must wire
   parseVBRI into the decode loop AND exclude the VBRI tag frame (currently a
   VBRI-tagged stream emits its tag frame as audio; no fixture yet), and must
   prime the bit reservoir + MDCT overlap on seek (step back ~10 frames and
   decode silently forward, do NOT just Reset at the landing frame), with a
   TOC-narrowed binary search. T6 must bound the free-format findFrame
   O(n*2304) scan at the streaming boundary (retain the last 2880 bytes on
   discard), handle parseXing FrameOffset!=0, add the ID3v1 trailer, and
   reconsider Layer I/II skip-and-continue. Execute PR10/PR11 the same way:
   plan is written and agy-reviewed, so branch, implement, review, gate,
   watch-pr, merge.
4. Execute task by task with superpowers:subagent-driven-development (a fresh
   implementer subagent per task, then a task review, following the ledger's
   established pattern).
5. Phases 2-5 (pcm container, encoder skeleton, psymodel + rate control,
   tuning) each get their own plan when reached; write it with
   superpowers:writing-plans and have agy review it first.

## Workflow conventions (established this project)

- Cut a feature branch from `main`, one PR per grouped unit (PR1 = T2+T3,
  PR2 = T4+T5, PR3 = T6+T7, PR4 = T8+T9, PR5 = T10+T11, PR6 = T12+T13).
  Scaffolding and rules (this file, linter config, CI, README) commit
  straight to `main` with no PR.
- Squash-merge each PR with a conventional subject and the PR number
  appended (matches go-flac/go-aac/go-wav); no merge commits. The process is
  plan, branch, implement, test, then the pre-push gate and PR watch.
- `task check` runs build + vet + golangci-lint + test and must be green
  before every commit. golangci-lint uses the shared sibling config
  (`.golangci.yaml` + `rules/*.go` + the ruleguard dsl anchored by
  `tools/tools.go`). Write idiomatic Go (range-over-int for counting loops,
  etc.); a faithful port that is genuinely complex may carry a justified
  `//nolint:gocognit,gocyclo`.
- Each decoder unit lands with an oracle dump hook (patch-based, in
  `tools/oracle/hooks.patch`, never editing the pristine `minimp3.h`) plus a
  differential test that byte-compares Go output against the oracle. Run
  `task oracle:build` then `task oracle:dump` to regenerate dumps
  (gitignored); differential tests skip without them, or fail loudly under
  `MP3_REQUIRE_DUMPS=1`. Replay tests iterate `replayFixtures` (all fixtures
  except `corrupt_bitflip.mp3`).

## Carry-forward items for the remaining tasks

- T10 (stateful decoder): replicate mp3dec_decode_frame's fast-path header
  cache and re-include `corrupt_bitflip.mp3` in its full-stream PCM
  differential; port L3_change_sign here (it sits after the IMDCT dump hook).
- T11 (conformance): the LAME fixture corpus emits no intensity stereo and no
  mixed blocks, so those faithfully-ported paths stay untested until the ISO
  vectors run here; also add the arm64 oracle differential CI job.
- T12 (robustness): do NOT expect bit-exact oracle parity on a deliberately
  truncated stream at a reservoir-boundary tail. bits.Reader returns
  deterministic 0 past its limit while upstream reads raw scratch bytes; the
  Go behaviour is the correct, safer one.
- T13 (public API): follow the sibling conventions across go-flac, go-aac,
  go-wav (constructor naming, streaming vs frame model, io.Reader usage,
  error handling); reconcile the plan's proposed API against the actual
  sibling decoder surfaces.
- Deferred minors (see the ledger): extend the scalefactor table-checksum
  test to all 8 tables; consolidate the duplicated test helpers flagged on
  PR4; note that the diff-based `hooks.patch` churns its mtime header lines
  on regeneration (harmless, the patch body is stable).

## Hard rules

- MIT provenance: the decoder derives only from the pinned minimp3 (CC0-1.0)
  vendored under `tools/oracle/`; the encoder derives only from ISO/IEC
  11172-3 and published papers. Never open LAME, Shine, dist10, libmad,
  mpg123, FFmpeg, or Helix source. LAME/ffmpeg/mpg123 are black-box test
  binaries only. See PROVENANCE.md.
- `docs/` is private planning material: gitignored, never committed, never
  pushed. The SDD ledger under `.superpowers/` is likewise local only.
- ISO conformance vectors are never committed; fetch scripts only.
- Bit-exactness discipline for all ported DSP code: follow the plan's Global
  Constraints (FMA blocking on arm64, C double-promotion matching, upstream
  operation order). The oracle builds with `-ffp-contract=off`, so the arm64
  anti-fusion discipline is load-bearing. IMPORTANT: block FMA fusion with an
  explicit `float32(a*b)` conversion on any product feeding a `+`/`-`; a bare
  two-statement local (`t := a*b; t + c`) does NOT block fusion in Go 1.26
  (the compiler fuses across statements, verified via `GOARCH=arm64 go build
  -gcflags=-S`). amd64 default `GOAMD64=v1` has no FMA instruction, so fusion
  bugs are invisible there and only the arm64 differential (CI) catches them.
- Peer review partner: agy (Gemini 3.1 Pro High for design and code review,
  Flash for research); review every phase plan before executing it.
