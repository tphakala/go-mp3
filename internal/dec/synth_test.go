package dec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

// TestSynthTablesMatchPin guards gSec and gWin against accidental edits with
// a golden checksum over their float32 bit patterns (gSec's 24 entries then
// gWin's 240). Unlike TestImdctTablesMatchOracle, the golden hash is derived
// from these tables rather than an independent tools/oracle/tables.c run,
// because this task does not extend tables.c; the authoritative bit-exact
// check on both tables is TestFullStreamMatchesOracle, which runs every value
// through mp3dDctII/mp3dSynth and byte-compares the result to the C oracle.
// This test is a cheap regression tripwire on top of that.
func TestSynthTablesMatchPin(t *testing.T) {
	const wantHex = "fdce6bc17da3f6b73afaf6a4d93cb16cbd5dab67eeec99b3a567d3363625b73e"

	h := sha256.New()
	var buf4 [4]byte
	for _, f := range gSec {
		binary.LittleEndian.PutUint32(buf4[:], math.Float32bits(f))
		h.Write(buf4[:])
	}
	for _, f := range gWin {
		binary.LittleEndian.PutUint32(buf4[:], math.Float32bits(f))
		h.Write(buf4[:])
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		t.Fatalf("synth table checksum = %s, want %s", got, wantHex)
	}
}

// TestScalePcmExact checks mp3dScalePcm applies exactly the pin's float
// scaling (sample * 1/32768). 1/32768 is 2^-15, exactly representable, so the
// result must be bit-identical to the reference computed the same way.
func TestScalePcmExact(t *testing.T) {
	const scale = float32(1.0) / 32768.0
	for _, sample := range []float32{0, 1, -1, 32768, -32768, 12345.678, -0.5, 1e-30} {
		want := sample * scale
		got := mp3dScalePcm(sample)
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("mp3dScalePcm(%v) = %08x, want %08x", sample, math.Float32bits(got), math.Float32bits(want))
		}
	}
}

// TestSynthTablesShape documents gSec's and gWin's dimensions and a few pin
// sentinel values (the first and last of each), so a wrong-sized transcription
// fails fast and independently of the checksum's exact digest.
func TestSynthTablesShape(t *testing.T) {
	if len(gSec) != 24 {
		t.Fatalf("len(gSec) = %d, want 24", len(gSec))
	}
	if len(gWin) != 15*16 {
		t.Fatalf("len(gWin) = %d, want %d", len(gWin), 15*16)
	}
	if gSec[0] != float32(10.19000816) || gSec[23] != float32(5.10114861) {
		t.Fatalf("gSec endpoints = %v, %v", gSec[0], gSec[23])
	}
	if gWin[0] != -1 || gWin[len(gWin)-1] != 65290 {
		t.Fatalf("gWin endpoints = %v, %v", gWin[0], gWin[len(gWin)-1])
	}
}
