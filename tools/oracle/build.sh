#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
sha256sum -c --quiet minimp3.h.sha256   # pristine header must match the pin
mkdir -p build
case "${1:-}" in
  --prep)
    cp minimp3.h build/minimp3.h
    [ -s hooks.patch ] && patch -s build/minimp3.h hooks.patch
    exit 0 ;;
  --rehook)
    diff -u minimp3.h build/minimp3.h > hooks.patch || true
    exit 0 ;;
esac
cp minimp3.h build/minimp3.h
[ -s hooks.patch ] && patch -s build/minimp3.h hooks.patch
cc -O2 -ffp-contract=off -DMINIMP3_NO_SIMD -DMINIMP3_FLOAT_OUTPUT -DMP3DUMP \
   -o build/mp3dump mp3dump.c -lm
