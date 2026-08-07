#!/usr/bin/env bash
# ISO conformance material: fetched for local/CI use, never committed.
# One codeload tarball request: no GitHub API, no rate limits in CI.
set -euo pipefail
cd "$(dirname "$0")/.."
PIN=$(cat tools/oracle/PIN)
mkdir -p testdata/vectors
curl -sfL "https://codeload.github.com/lieff/minimp3/tar.gz/$PIN" \
  | tar -xz -C testdata/vectors --strip-components=2 "minimp3-$PIN/vectors"
count=$(find testdata/vectors -type f | wc -l)
if [ "$count" -eq 0 ]; then
  echo "fetch-vectors.sh: no vectors extracted (bad PIN or changed tarball layout?)" >&2
  exit 1
fi
echo "fetched $count vector files into testdata/vectors"
