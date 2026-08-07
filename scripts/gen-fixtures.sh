#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p testdata/fixtures /tmp/fixsrc
gen() { ffmpeg -y -f lavfi -i "$1" -t 2 -ar "$2" -ac "$3" "/tmp/fixsrc/$4.wav"; }
gen "sine=frequency=440"                     44100 2 sine44s
gen "sine=frequency=1000"                    48000 1 sine48m
gen "anoisesrc=colour=pink"                  32000 2 noise32s
gen "sine=frequency=100:beep_factor=8"       22050 2 beep22s
gen "anoisesrc=colour=white"                 16000 1 noise16m
gen "sine=frequency=3000"                    11025 1 sine11m
gen "sine=frequency=2000"                     8000 1 sine8m
# chirp: transient-dense content, exercises short blocks
ffmpeg -y -f lavfi -i "sine=frequency=200:duration=2" -af "vibrato=f=8:d=0.9" -ar 44100 /tmp/fixsrc/chirp44m.wav
enc() { lame --quiet -b "$2" ${3:-} "/tmp/fixsrc/$1.wav" "testdata/fixtures/$1_$2${4:-}.mp3"; }
enc sine44s 128; enc sine44s 320; enc sine44s 64
enc sine48m 128; enc noise32s 192
enc chirp44m 128
enc beep22s 64          # MPEG-2
enc noise16m 32         # MPEG-2
enc sine11m 32          # MPEG-2.5
enc sine8m 24           # MPEG-2.5
lame --quiet -v -V 5 /tmp/fixsrc/sine44s.wav testdata/fixtures/sine44s_vbr.mp3
lame --quiet -b 128 -m m /tmp/fixsrc/sine48m.wav testdata/fixtures/sine48m_mono128.mp3
lame --quiet -b 192 -m d /tmp/fixsrc/noise32s.wav testdata/fixtures/noise32s_dual192.mp3
lame --quiet --freeformat -b 168 /tmp/fixsrc/sine44s.wav testdata/fixtures/sine44s_free168.mp3
enc sine44s 32          # low-rate joint stereo, stresses joint-stereo decisions
# deterministic corrupt fixtures for Task 12 (committed; self-generated)
head -c 4000 testdata/fixtures/sine44s_128.mp3 > testdata/fixtures/corrupt_truncated.mp3
python3 - <<'PY'
data = bytearray(open('testdata/fixtures/sine44s_128.mp3', 'rb').read())
for i in range(500, len(data), 977):
    data[i] ^= 0x55
open('testdata/fixtures/corrupt_bitflip.mp3', 'wb').write(bytes(data))
PY
