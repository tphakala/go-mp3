package enc

// blockswitch.go implements the attack detector and window-state machine
// (Brandenburg/Stoll's published energy-ratio attack detection; ISO/IEC
// 11172-3 2.4.1.7 for the window-compatibility grammar the state machine
// enforces). See design decisions 9 and 10 for the full rationale. No
// codec source was consulted for either piece: attackDetect is a small,
// self-contained energy comparison, and blockTypeFor's four-case switch is
// this project's own reading of the published grammar.

// attackRatio and attackFloorE are the attack detector's two tunables
// (design decision 9), calibrated against the behavioral gates in
// blockswitch_test.go / encoder_test.go / internal/dec's encx tests:
// TestEncoderSwitchesOnTransients (must fire on a click/step, never on a
// steady tone or silence) and TestEncoderNeverSwitchesOnTones. Measured: a
// synthetic 10x step in sub-block energy is comfortably separated from a
// steady multi-tone program's largest frame-to-frame sub-block energy
// ratio (never above about 1.5x in the calibration program), so
// attackRatio = 10.0 leaves wide margin on both sides. attackFloorE = 1e-3
// (clamped-sample energy units, i.e. sum of up to 192 squared samples each
// in [-1,1]) keeps near-silent noise (e.g. dithered digital silence) from
// tripping the ratio test on meaningless energy fluctuations: 192 samples
// at full scale sum to 192, so 1e-3 is far below any audible signal but
// well above quantization-noise-level floors.
const (
	attackRatio  = 10.0
	attackFloorE = 1e-3
)

// attackNoPrior seeds attackDetect's prevE for the very first sub-block of a
// stream, where no preceding energy exists to compare against. It is far
// larger than any real 192-sample sub-block energy (bounded by ~192 for
// clamped [-1,1] PCM), so the k=0 ratio test e[0] > attackRatio*prevE can
// never fire: a stream that opens with steady sound is not misread as an
// attack by the uninitialized zero carry, which would otherwise force a
// spurious short block on the very first granule (and, at stream start, an
// illegal 0->2 window transition). A real attack in a later sub-block, or a
// genuine mid-stream silence-to-loud onset, still triggers normally.
const attackNoPrior = 1e300

// attackDetect reports whether granule pcm (576 clamped [-1,1] samples, the
// same domain design decision 11's pcmHist stores) contains an attack:
// sub-block energies e[0..2] over the granule's three 192-sample thirds,
// each compared against the immediately preceding sub-block's energy
// (e[-1] = prevE, the caller-carried value from the previous granule's
// last sub-block), Brandenburg/Stoll's published energy-ratio test. attack
// is true if ANY of the three sub-blocks trips the ratio while also
// clearing the absolute floor (a ratio test alone is meaningless against
// near-zero energy). lastE is e[2], the new carry for the NEXT granule's
// call.
//
// Every sub-block energy sum is FMA-blocked (sum += float64(v*v)); the
// ratio comparison (attackRatio*prev) feeds only a comparison, never a
// +/-, so it needs no such wrap.
func attackDetect(pcm []float64, prevE float64) (attack bool, lastE float64) {
	var e [3]float64
	for k := range 3 {
		var sum float64
		for _, v := range pcm[k*192 : k*192+192] {
			sum += float64(v * v)
		}
		e[k] = sum
	}

	prev := prevE
	for k := range 3 {
		if e[k] > attackFloorE && e[k] > attackRatio*prev {
			attack = true
		}
		prev = e[k]
	}
	return attack, e[2]
}

// blockTypeFor advances the per-channel window state machine (design
// decision 10) by one granule: prev is the previous granule's decided
// block type, want is this granule's own attack verdict, and wantNext is
// the NEXT granule's attack verdict (decision 11's one-granule lookahead
// makes this available before this granule is decided).
//
// The cases, in priority order:
//
//   - want: this granule itself is attacking, so it is short regardless of
//     what came before (a cascading run of attacks stays short granule
//     after granule).
//   - prev == blockShort (and not want): the run cannot legally jump
//     straight to start (ISO 2.4.1.7's window-compatibility grammar has no
//     2 -> 1 edge: entering a short run always passes through start
//     first). If wantNext, the run BRIDGES one more granule as short
//     rather than closing early, exactly covering the "one quiet granule
//     between two attacks" gap a naive want/wantNext-only formula would
//     otherwise resolve illegally; otherwise the run closes as stop,
//     carrying the long window's fall out of it.
//   - wantNext (prev is long or stop): the NEXT granule attacks, so this
//     one opens the transition as start, carrying the long window's rise
//     into the short run that follows.
//   - otherwise: long, the steady-state case.
//
// This total function is never asked to decide an inconsistent state in
// practice (the caller always feeds the same cached want/wantNext values
// used to decide the neighboring granules, chained through the actual
// decided prev, never a hypothetical one), but it is defined for every
// (prev, want, wantNext) input. Provable by induction on that
// self-consistent chain that every EMITTED transition lands in ISO
// 2.4.1.7's legal successor sets (0->{0,1}, 1->{2}, 2->{2,3}, 3->{0,1}):
// want(g) implies wantNext(g-1), which by construction never lands
// bt(g-1) at long or stop, so bt(g)=short's only reachable predecessors
// are start and short (never long or stop, i.e. 0->2 and 3->2 cannot
// arise); bt(g)=start requires want(g)=false, so its only reachable
// predecessors are long and stop (the prev==blockShort case, checked
// before this one, already claims short); and whenever bt(g)=start,
// wantNext(g) was true by construction, so want(g+1) is unconditionally
// true at the next granule, making short the ONLY reachable successor of
// start (matching 1->{2} and making 1->3 structurally unreachable). The
// unit test proves this by exhaustive simulation over long random
// want-sequences, not just by checking single-step inputs.
func blockTypeFor(prev int, want, wantNext bool) int {
	switch {
	case prev == blockStart:
		// A start block's only legal successor is a short block (ISO
		// 11172-3 window grammar 1->{2}); the run of short blocks always
		// begins on the granule after a start.
		return blockShort
	case want:
		// This granule wants a short block. blockShort is a legal successor
		// only of blockStart or blockShort; from blockLong or blockStop it
		// would be an illegal transition (e.g. 0->2 straight after the
		// decoder's implicit initial long state at stream start), so open
		// the run with a start block instead, which forces the next granule
		// short via the case above. In steady state prev is always blockShort
		// here (a want granule is preceded by the start that wantNext emitted),
		// so this only diverges at stream start, where prev is seeded to long.
		if prev == blockShort {
			return blockShort
		}
		return blockStart
	case prev == blockShort:
		if wantNext {
			return blockShort // bridge: 2->1 is illegal, so stay short one more granule
		}
		return blockStop
	case wantNext:
		return blockStart
	default:
		return blockLong
	}
}
