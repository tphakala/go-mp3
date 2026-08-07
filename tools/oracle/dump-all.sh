#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
./build.sh

repo_root="../.."

# Materialize the mutated inputs (leading garbage, interior hole) that the
# robustness differential compares against. TestGenerateMutatedInputs writes
# them under tools/oracle/mutated only when MP3_GEN_MUTATED is set, so this is
# the single source of truth for those bytes; they are then dumped below like
# any fixture. Failing here is fatal (set -e) so a missing generator step never
# silently ships a stale or empty mutated corpus.
( cd "$repo_root" && MP3_GEN_MUTATED=1 go test ./internal/dec -run TestGenerateMutatedInputs -count=1 >/dev/null )

shopt -s nullglob
for f in "$repo_root"/testdata/fixtures/*.mp3 "$repo_root"/testdata/vectors/*.bit mutated/*.mp3; do
  outdir="dumps/$(basename "$f")/"
  mkdir -p "$outdir"
  build/mp3dump "$f" "$outdir"
done
