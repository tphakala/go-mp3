package enc

// resHardCapBytes is the 9-bit main_data_begin field's limit.
const resHardCapBytes = 511

// mainAreaBytes is the frame's main-data area: frameLength minus the 4-byte
// header minus the side info.
func mainAreaBytes(bitrateIndex, srIndex, padding, nch int) int {
	return frameLength(bitrateIndex, srIndex, padding) - 4 - sideInfoBits(nch)/8
}

// resCapBytes is the occupancy cap: min(511, 7*unpadded area). Design
// decision 2: the 7-area term bounds the FIFO depth to a fixed slot count
// at every rate.
func resCapBytes(bitrateIndex, srIndex, nch int) int {
	area := mainAreaBytes(bitrateIndex, srIndex, 0, nch)
	return min(resHardCapBytes, 7*area)
}

// reservoir tracks main-data occupancy in whole bytes (design decision 1:
// occupancy IS the next frame's main_data_begin).
type reservoir struct{ occ int }

// spendBounds returns the frame's legal PHYSICAL spend range in bytes
// (design decision 3): lo = max(0, occ+area-cap), hi = occ+area. The
// Huffman field capacity is deliberately NOT folded into hi; it caps only
// the coded portion inside planFrame (the decoupling agy's review
// mandated: at 320kbps/32kHz mono, area 1419 exceeds huffCap 1023, and a
// field-capped hi would fall below lo near full occupancy).
func (r *reservoir) spendBounds(area, capBytes, nch int) (lo, hi int) {
	hi = r.occ + area
	lo = max(0, r.occ+area-capBytes)
	return lo, hi
}

// granuleDemandBits maps one granule-channel's PE to its bit demand (design
// decision 4). meanGB is area*8/(2*nch).
func granuleDemandBits(pe float64, meanGB int) int {
	if !(pe > 0) {
		pe = 0
	}
	if pe > 1<<20 {
		pe = 1 << 20
	}
	peInt := int(pe)

	lo := meanGB / 2
	hi := min(maxPart23Length, 2*meanGB)
	switch {
	case peInt < lo:
		return lo
	case peInt > hi:
		return hi
	default:
		return peInt
	}
}

// planFrame turns per-granule demands into the frame's plan (design
// decision 5): huffTargetBytes = min(ceil(sum demands/8), min(occ+area,
// nGC*maxPart23Length/8)), the coded spend the fields can absorb; budgets
// split huffTargetBytes*8 proportionally (remainder to the last
// granule-channel; zero demand sum yields zero budgets) with the
// deterministic cap-redistribution LOOP, so sum(budgets) ==
// huffTargetBytes*8 and every budget <= maxPart23Length; spendBytes =
// max(huffTargetBytes, lo), the physical spend whose excess over the coded
// portion renderMainData fills with ancillary zeros. demands and budgets
// are caller-owned arrays indexed granule-major (g*nch+ch).
func (r *reservoir) planFrame(demands *[4]int, nGC, area, capBytes int) (spendBytes, huffTargetBytes int, budgets [4]int) {
	sum := 0
	for i := range nGC {
		sum += demands[i]
	}

	huffCap := nGC * maxPart23Length / 8
	huffTarget := min((sum+7)/8, min(r.occ+area, huffCap))

	huffBits := huffTarget * 8
	if sum > 0 {
		assigned := 0
		for i := range nGC {
			budgets[i] = huffBits * demands[i] / sum
			assigned += budgets[i]
		}
		budgets[nGC-1] += huffBits - assigned

		// Deterministic cap-redistribution loop: while any granule-channel
		// exceeds maxPart23Length, pool the excess of every violator,
		// clamp the violators to the cap, then split the pool equally
		// over the still-uncapped indexes in ascending order with the
		// remainder to the last uncapped one. Repeats until no violator
		// remains; each pass strictly reduces the number of uncapped
		// slots or terminates, so it converges within nGC iterations. The
		// total is conserved at every step.
		for {
			pool := 0
			over := false
			for i := range nGC {
				if budgets[i] > maxPart23Length {
					pool += budgets[i] - maxPart23Length
					budgets[i] = maxPart23Length
					over = true
				}
			}
			if !over {
				break
			}
			var uncapped []int
			for i := range nGC {
				if budgets[i] < maxPart23Length {
					uncapped = append(uncapped, i)
				}
			}
			if len(uncapped) == 0 {
				// Nowhere left to put the pool: every slot is at the cap
				// already (sum requested more than nGC*maxPart23Length,
				// which huffCap above already prevents). Drop it rather
				// than loop forever.
				break
			}
			share := pool / len(uncapped)
			for _, i := range uncapped {
				budgets[i] += share
			}
			budgets[uncapped[len(uncapped)-1]] += pool - share*len(uncapped)
		}
	}

	lo, _ := r.spendBounds(area, capBytes, 2)
	spendBytes = max(huffTarget, lo)
	return spendBytes, huffTarget, budgets
}

// ResCapBytesPin exposes the occupancy cap to the internal/dec
// occupancy-replay validator (test-only entry, the AppendFramePin
// precedent), keyed by the header-derived kbps and sample rate. Guards both
// map lookups (carry-forward from Task 1's review: an unknown kbps or
// sampleRateHz would otherwise silently fall through to index 0 and compute
// a wrong, possibly negative cap) with a panic instead, since a caller
// passing an illegal combination is a test-harness bug this pin should
// surface immediately rather than let ripple into a confusing replay
// failure. Defense in depth only: real callers derive kbps/sampleRateHz
// from a decoder-validated header, which already rejects illegal bitrate
// and sample-rate indices at sync.
func ResCapBytesPin(kbps, sampleRateHz, nch int) int {
	bitrateIndex, ok := bitrateIndexForKbps[kbps]
	if !ok {
		panic("enc: ResCapBytesPin: unknown bitrate")
	}
	srIndex, ok := srIndexForRate[sampleRateHz]
	if !ok {
		panic("enc: ResCapBytesPin: unknown sample rate")
	}
	return resCapBytes(bitrateIndex, srIndex, nch)
}

// commitFrame records a coded frame: occupancy gains the unspent bytes.
// Callers guarantee lo <= spentBytes <= hi, so 0 <= occ <= cap holds as an
// invariant (asserted in the reservoir tests; commitFrame itself does no
// bounds checking, trusting the spendBounds-clamped inputs).
func (r *reservoir) commitFrame(area, spentBytes int) {
	r.occ += area - spentBytes
}
