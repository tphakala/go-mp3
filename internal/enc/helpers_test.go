package enc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
)

// sha256Float64s hashes each float64 in vs, in order, as its IEEE-754 bit
// pattern encoded as an 8-byte little-endian word, and returns the hex
// digest. This is the exact byte layout shared by TestFBWindowChecksum and
// TestFBGolden (filterbank_test.go) and TestMdctTablesChecksum and
// TestMdctGolden (mdct_test.go); route calls through here rather than
// reimplementing the loop at each call site.
func sha256Float64s(vs ...float64) string {
	h := sha256.New()
	var buf8 [8]byte
	for _, v := range vs {
		binary.LittleEndian.PutUint64(buf8[:], math.Float64bits(v))
		h.Write(buf8[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}
