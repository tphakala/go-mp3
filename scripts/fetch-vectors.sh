#!/usr/bin/env bash
# ISO conformance material: fetched for local/CI use, never committed.
# One codeload tarball request: no GitHub API, no rate limits in CI.
set -euo pipefail
cd "$(dirname "$0")/.."
PIN=$(cat tools/oracle/PIN)
mkdir -p testdata/vectors
curl -sfL "https://codeload.github.com/lieff/minimp3/tar.gz/$PIN" \
  | tar -xz -C testdata/vectors --strip-components=2 "minimp3-$PIN/vectors"
ls testdata/vectors
