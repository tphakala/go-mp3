#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
./build.sh

repo_root="../.."
shopt -s nullglob
for f in "$repo_root"/testdata/fixtures/*.mp3 "$repo_root"/testdata/vectors/*.bit; do
  outdir="dumps/$(basename "$f")/"
  mkdir -p "$outdir"
  build/mp3dump "$f" "$outdir"
done
