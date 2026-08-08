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
go-flac/go-aac/go-wav). Phase 2 (pcm container) is COMPLETE (as of 2026-08-08,
PRs 8-11 merged): the streaming `pcm.Decoder` (io.Reader + io.WriterTo) adds
ID3v2 skip + ID3v1 trailer, S16 default plus a WithF32 option,
Xing/Info/VBRI/LAME tag parsing, gapless trim, bounded resync with
mp3.ErrCorruptStream, clean-end/truncation detection, SeekToSample (bit-exact
via exact-frame-index recovery + reservoir/MDCT priming), and a fuzz +
streaming conformance suite the CI Oracle job runs bit-exact on amd64 + arm64
(pcm coverage ~91%). The T7 fuzz surfaced two pre-existing decoder bugs: an
intensity/MS-stereo panic on a malformed mono frame, FIXED and merged (PR12
cc81f9d, an l3Decode nch==2 guard); and a pcm.Decoder wholesale-construction
failure on a short stream whose first frame's FrameBytes exceeds maxFrameBytes
(l3-nonstandard-big-iscf), root-caused and DEFERRED (non-crashing, documented
and self-guarding in the conformance test). Next: the big-iscf fix, two test
fast-follows, then Phases 3-5 (encoder). No encoder exists yet.**

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
3. Phase 0+1 (PRs 1-7) and Phase 2 (pcm container, PRs 8-11) are DONE; main
   HEAD cc81f9d. Phase 2 PRs: PR8 T1+T2 (fbd786f), PR9 T3+T5 (d3c03aa), PR10
   T4 SeekToSample + T6 robustness (2c8392c), PR11 T7 fuzz + conformance
   (cbe855c); decoder-robustness fix PR12 (cc81f9d) followed. NEXT, on fresh
   branches off main (authoritative list + root-cause analyses in the Phase 2
   ledger): (a) big-iscf pcm.Decoder wholesale-construction failure - the
   first frame's FrameBytes (3242, via a 1338-byte FrameOffset) exceeds
   maxFrameBytes (2880), violating the streaming window assumption, so Reset
   fails wholesale where the frame API recovers 2 audio frames; deliberate
   design fix, must not regress T6 robustness; (b) two test fast-follows
   (deterministic seek-overflow-guard coverage; fuzz fixture-load guard should
   f.Fatalf not silently skip); (c) PR10 carry-forwards (frameOffsets O(n) walk
   / TOC narrowing / frame-index cache; truncatedFrame binary-trailer
   false-reject; frameLength <-> internal/dec shared helper; src/seekable field
   consolidation). Then Phases 3-5 (encoder), each with its own agy-reviewed
   plan. Execute each the same way: plan, branch, implement, review, gate,
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
