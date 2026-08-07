# go-mp3

Pure-Go MP3 (MPEG Layer III) codec library, `github.com/tphakala/go-mp3`,
MIT licensed. Sibling of go-flac, go-opus, go-aac.

**Status: planning complete, implementation NOT started.**

## Start here (fresh session)

1. Recall project memories from the Hindsight bank `go-mp3`
   (`mcp__hindsight-memory-go-mp3__recall`).
2. Read the approved spec: `docs/superpowers/specs/2026-08-06-go-mp3-design.md`.
3. Read the peer-reviewed Phase 0+1 plan (13 tasks, scaffold + decoder core):
   `docs/superpowers/plans/2026-08-06-go-mp3-phase0-1-decoder.md`.
4. Execute the plan task by task with superpowers:subagent-driven-development
   (or superpowers:executing-plans if the user prefers inline). Task 1 is the
   repo scaffold; its `.gitignore` MUST exclude `docs/` before the first
   commit.
5. Phases 2-5 (pcm container layer, encoder skeleton, psymodel + rate
   control, tuning) each get their own plan when reached; write it with
   superpowers:writing-plans and have agy review it first.

## Hard rules

- MIT provenance: the decoder derives only from the pinned minimp3 (CC0-1.0)
  vendored under `tools/oracle/`; the encoder derives only from ISO/IEC
  11172-3 and published papers. Never open LAME, Shine, dist10, libmad,
  mpg123, FFmpeg, or Helix source. LAME/ffmpeg/mpg123 are black-box test
  binaries only. See PROVENANCE.md once scaffolded.
- `docs/` is private planning material: gitignored, never committed, never
  pushed.
- ISO conformance vectors are never committed; fetch scripts only.
- Bit-exactness discipline for all ported DSP code: follow the plan's Global
  Constraints (FMA blocking on arm64, C double-promotion matching, upstream
  operation order).
- Peer review partner: agy (Gemini 3.1 Pro High for design and code review,
  Flash for research); review every phase plan before executing it.
