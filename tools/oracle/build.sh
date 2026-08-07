#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
sha256sum -c --quiet minimp3.h.sha256   # pristine header must match the pin
mkdir -p build
case "${1:-}" in
  --prep)
    cp minimp3.h build/minimp3.h
    if [ -s hooks.patch ]; then patch -s build/minimp3.h hooks.patch; fi
    exit 0 ;;
  --rehook)
    if [ ! -f build/minimp3.h ]; then
      echo "build.sh: build/minimp3.h missing; run --prep first" >&2
      exit 1
    fi
    # Write to a temp file first so a diff failure cannot truncate the committed
    # hooks.patch and silently ship an uninstrumented oracle.
    tmp=$(mktemp)
    diff -u minimp3.h build/minimp3.h > "$tmp" || true
    mv "$tmp" hooks.patch
    exit 0 ;;
esac
cp minimp3.h build/minimp3.h
if [ -s hooks.patch ]; then patch -s build/minimp3.h hooks.patch; fi
cc -O2 -ffp-contract=off -DMINIMP3_NO_SIMD -DMINIMP3_FLOAT_OUTPUT -DMP3DUMP \
   -o build/mp3dump mp3dump.c -lm
