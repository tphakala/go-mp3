package dec

// gSec mirrors upstream mp3d_DCT_II's static const g_sec[24]
// (tools/oracle/minimp3.h:1276-1278): the DCT-II butterfly rotation
// coefficients. Every literal is f-suffixed in the pin, so there is no
// double promotion; the values live here (not tables.go) because they are
// used only by mp3dDctII, exactly as the pin scopes them to that function.
var gSec = [24]float32{
	10.19000816, 0.50060302, 0.50241929, 3.40760851, 0.50547093, 0.52249861,
	2.05778098, 0.51544732, 0.56694406, 1.48416460, 0.53104258, 0.64682180,
	1.16943991, 0.55310392, 0.78815460, 0.97256821, 0.58293498, 1.06067765,
	0.83934963, 0.62250412, 1.72244716, 0.74453628, 0.67480832, 5.10114861,
}

// gWin mirrors upstream mp3d_synth's static const g_win[]
// (tools/oracle/minimp3.h:1482-1498): the 15x16 synthesis window
// coefficients, streamed once per mp3dSynth call by a single advancing
// cursor. The pin declares them as float via unsuffixed integer literals;
// each value is a small integer exactly representable in float32, so the Go
// untyped constants convert without loss and there is no double promotion.
var gWin = [15 * 16]float32{
	-1, 26, -31, 208, 218, 401, -519, 2063, 2000, 4788, -5517, 7134, 5959, 35640, -39336, 74992,
	-1, 24, -35, 202, 222, 347, -581, 2080, 1952, 4425, -5879, 7640, 5288, 33791, -41176, 74856,
	-1, 21, -38, 196, 225, 294, -645, 2087, 1893, 4063, -6237, 8092, 4561, 31947, -43006, 74630,
	-1, 19, -41, 190, 227, 244, -711, 2085, 1822, 3705, -6589, 8492, 3776, 30112, -44821, 74313,
	-1, 17, -45, 183, 228, 197, -779, 2075, 1739, 3351, -6935, 8840, 2935, 28289, -46617, 73908,
	-1, 16, -49, 176, 228, 153, -848, 2057, 1644, 3004, -7271, 9139, 2037, 26482, -48390, 73415,
	-2, 14, -53, 169, 227, 111, -919, 2032, 1535, 2663, -7597, 9389, 1082, 24694, -50137, 72835,
	-2, 13, -58, 161, 224, 72, -991, 2001, 1414, 2330, -7910, 9592, 70, 22929, -51853, 72169,
	-2, 11, -63, 154, 221, 36, -1064, 1962, 1280, 2006, -8209, 9750, -998, 21189, -53534, 71420,
	-2, 10, -68, 147, 215, 2, -1137, 1919, 1131, 1692, -8491, 9863, -2122, 19478, -55178, 70590,
	-3, 9, -73, 139, 208, -29, -1210, 1870, 970, 1388, -8755, 9935, -3300, 17799, -56778, 69679,
	-3, 8, -79, 132, 200, -57, -1283, 1817, 794, 1095, -8998, 9966, -4533, 16155, -58333, 68692,
	-4, 7, -85, 125, 189, -83, -1356, 1759, 605, 814, -9219, 9959, -5818, 14548, -59838, 67629,
	-4, 7, -91, 117, 177, -106, -1428, 1698, 402, 545, -9416, 9916, -7154, 12980, -61289, 66494,
	-5, 6, -97, 111, 163, -127, -1498, 1634, 185, 288, -9585, 9838, -8540, 11455, -62684, 65290,
}

// mp3dScalePcm mirrors upstream mp3d_scale_pcm's MINIMP3_FLOAT_OUTPUT path
// (tools/oracle/minimp3.h:1445-1448): scale by 1/32768. The oracle builds
// with -DMINIMP3_FLOAT_OUTPUT, so this is the reference. 1.0/32768.0 is
// exactly 2^-15 in float32, so the untyped constant converts without loss
// and the multiply is a single exact scaling with no double promotion.
func mp3dScalePcm(sample float32) float32 {
	return sample * (1.0 / 32768.0)
}

// mp3dDctII mirrors upstream mp3d_DCT_II's scalar (non-SIMD) path
// (tools/oracle/minimp3.h:1368-1425): an in-place DCT-II over n columns of
// grbuf, each column strided 18 floats apart. The pin's flat t[4][8] scratch
// (indexed x[0]/x[8]/x[16]/x[24] as t[0..3][i]) is a plain [4][8] here.
//
// Float discipline: several products feed a later + or - (t2/t3 into
// mat[2]/mat[3], the x6/x3 rotations into x0/xt and mat[2]/mat[6], and the
// three "rotate by PI/8" products into x5/x7), any of which arm64 can fuse
// into an FMA. A bare local assignment does NOT block that fusion; the Go
// compiler fuses across statements. Each such product carries an explicit
// float32() conversion, the only reliable barrier, forcing a separate
// rounding step to match the pin under -ffp-contract=off. Combines of
// already-materialized add/sub results (t[2][i]+t[3][i] etc.) and a single
// (sum)*coeff product with no trailing add cannot fuse and are left bare.
//
//nolint:gocognit,gocyclo // faithful port of mp3d_DCT_II's three fixed loops; restructuring would break the bit-exact operation order.
func mp3dDctII(grbuf []float32, n int) {
	for k := range n {
		var mat [4][8]float32
		y := grbuf[k:]

		for i := range 8 {
			y0 := y[i*18]
			y1 := y[(15-i)*18]
			y2 := y[(16+i)*18]
			y3 := y[(31-i)*18]
			t0 := y0 + y3
			t1 := y1 + y2
			t2 := float32((y1 - y2) * gSec[3*i+0])
			t3 := float32((y0 - y3) * gSec[3*i+1])
			mat[0][i] = t0 + t1
			mat[1][i] = (t0 - t1) * gSec[3*i+2]
			mat[2][i] = t3 + t2
			mat[3][i] = (t3 - t2) * gSec[3*i+2]
		}

		for r := range 4 {
			x0, x1, x2, x3 := mat[r][0], mat[r][1], mat[r][2], mat[r][3]
			x4, x5, x6, x7 := mat[r][4], mat[r][5], mat[r][6], mat[r][7]
			var xt float32
			xt = x0 - x7
			x0 += x7
			x7 = x1 - x6
			x1 += x6
			x6 = x2 - x5
			x2 += x5
			x5 = x3 - x4
			x3 += x4
			x4 = x0 - x3
			x0 += x3
			x3 = x1 - x2
			x1 += x2
			mat[r][0] = x0 + x1
			mat[r][4] = (x0 - x1) * 0.70710677
			x5 += x6
			x6 = float32((x6 + x7) * 0.70710677)
			x7 += xt
			x3 = float32((x3 + x4) * 0.70710677)
			// rotate by PI/8: the explicit float32() on each product is the
			// barrier that blocks arm64 FMA fusion against the following +=/-=
			// (a bare local assignment does not; the Go compiler fuses across
			// statements).
			p := float32(x7 * 0.198912367)
			x5 -= p
			p = float32(x5 * 0.382683432)
			x7 += p
			p = float32(x7 * 0.198912367)
			x5 -= p
			x0 = xt - x6
			xt += x6
			mat[r][1] = (xt + x7) * 0.50979561
			mat[r][2] = (x4 + x3) * 0.54119611
			mat[r][3] = (x0 - x5) * 0.60134488
			mat[r][5] = (x0 + x5) * 0.89997619
			mat[r][6] = (x4 - x3) * 1.30656302
			mat[r][7] = (xt - x7) * 2.56291556
		}

		for i := range 7 {
			y[0*18] = mat[0][i]
			y[1*18] = mat[2][i] + mat[3][i] + mat[3][i+1]
			y[2*18] = mat[1][i] + mat[1][i+1]
			y[3*18] = mat[2][i+1] + mat[3][i] + mat[3][i+1]
			y = y[4*18:]
		}
		y[0*18] = mat[0][7]
		y[1*18] = mat[2][7] + mat[3][7]
		y[2*18] = mat[1][7]
		y[3*18] = mat[3][7]
	}
}

// mp3dSynthPair mirrors upstream mp3d_synth_pair (tools/oracle/minimp3.h:1451-1474):
// two windowed sums over the z[] filterbank memory (strided 64 floats apart),
// scaled to PCM. pcm writes at pcm[0] and pcm[16*nch]. The integer window
// weights are all exactly representable in float32, so multiplying a float32
// by them stays in float32 with no double promotion.
//
// Float discipline: each `a += (...)*w` is a c + a*b site, and arm64 can
// fuse the product into an FMA against the accumulator. A bare local
// assignment does NOT block that (the Go compiler fuses across statements),
// so every product carries an explicit float32() conversion, the only
// reliable barrier. The two initial `a = (...)*w` products are wrapped for
// the same reason: each is used only by the next `a += t`, so without the
// wrap arm64 fuses that first multiply into the accumulate instead.
func mp3dSynthPair(pcm []float32, nch int, z []float32) {
	var a, t float32

	a = float32((z[14*64] - z[0*64]) * 29)
	t = float32((z[1*64] + z[13*64]) * 213)
	a += t
	t = float32((z[12*64] - z[2*64]) * 459)
	a += t
	t = float32((z[3*64] + z[11*64]) * 2037)
	a += t
	t = float32((z[10*64] - z[4*64]) * 5153)
	a += t
	t = float32((z[5*64] + z[9*64]) * 6574)
	a += t
	t = float32((z[8*64] - z[6*64]) * 37489)
	a += t
	t = float32(z[7*64] * 75038)
	a += t
	pcm[0] = mp3dScalePcm(a)

	z = z[2:]
	a = float32(z[14*64] * 104)
	t = float32(z[12*64] * 1567)
	a += t
	t = float32(z[10*64] * 9727)
	a += t
	t = float32(z[8*64] * 64019)
	a += t
	t = float32(z[6*64] * -9975)
	a += t
	t = float32(z[4*64] * -45)
	a += t
	t = float32(z[2*64] * 146)
	a += t
	t = float32(z[0*64] * -5)
	a += t
	pcm[16*nch] = mp3dScalePcm(a)
}

// synthZOff mirrors the `zlin = lins + 15*64` offset in mp3d_synth
// (tools/oracle/minimp3.h:1499): the pin indexes zlin with negative offsets
// (e.g. zlin[4*i - 15*64]) that always resolve to a non-negative lins index,
// so this port indexes lins with the absolute offset instead of slicing zlin
// (a negative Go slice index would panic).
const synthZOff = 15 * 64

// mp3dSynthStep applies one of the three window accumulation steps from
// mp3d_synth's S0/S1/S2 macros (tools/oracle/minimp3.h:1601-1603) over the 4
// interleaved lanes. mode selects the pattern: 0 assigns (S0), 1 accumulates
// with a = vz*w0 - vy*w1 (S1), 2 accumulates with a = vy*w1 - vz*w0 (S2).
//
// Float discipline: every product carries an explicit float32() conversion,
// the only reliable barrier against arm64 fusing it into an FMA against the
// following combine (a bare local assignment does not; the Go compiler fuses
// across statements). This forces the pin's `b[j] += vz[j]*w1 + vy[j]*w0`
// evaluation order exactly (both products round, then their sum rounds, then
// the accumulate rounds).
func mp3dSynthStep(a, b *[4]float32, vz, vy []float32, w0, w1 float32, mode int) {
	for j := range 4 {
		pzw1 := float32(vz[j] * w1)
		pyw0 := float32(vy[j] * w0)
		sumB := pzw1 + pyw0

		var pa0, pa1 float32
		if mode == 2 {
			pa0 = float32(vy[j] * w1)
			pa1 = float32(vz[j] * w0)
		} else {
			pa0 = float32(vz[j] * w0)
			pa1 = float32(vy[j] * w1)
		}
		diffA := pa0 - pa1

		if mode == 0 {
			b[j] = sumB
			a[j] = diffA
		} else {
			b[j] += sumB
			a[j] += diffA
		}
	}
}

// mp3dSynth mirrors upstream mp3d_synth's scalar (non-SIMD, float-output)
// path (tools/oracle/minimp3.h:1476-1516, 1598-1625): the polyphase
// synthesis of one pair of subbands into 64 interleaved PCM samples per
// channel, updating the zlin filterbank memory in place. xl points at the
// current subband within a channel's DCT-II output; xr is the matching
// subband in the second channel (xl itself when mono). dstl/dstr are the
// left/right PCM write cursors; lins is the shared filterbank memory (whose
// tail becomes the next call's history).
//
// The single g_win cursor (wIdx) advances across all 15 i-iterations,
// matching the pin's `const float *w = g_win;` declared once before the loop.
//
//nolint:gocognit // faithful port of mp3d_synth's fixed 15-iteration loop and 8 window steps; the structure mirrors the pin for auditability.
func mp3dSynth(xl, dstl []float32, nch int, lins []float32) {
	xr := xl[576*(nch-1):]
	dstr := dstl[nch-1:]
	wIdx := 0

	lins[synthZOff+4*15] = xl[18*16]
	lins[synthZOff+4*15+1] = xr[18*16]
	lins[synthZOff+4*15+2] = xl[0]
	lins[synthZOff+4*15+3] = xr[0]

	lins[synthZOff+4*31] = xl[1+18*16]
	lins[synthZOff+4*31+1] = xr[1+18*16]
	lins[synthZOff+4*31+2] = xl[1]
	lins[synthZOff+4*31+3] = xr[1]

	mp3dSynthPair(dstr, nch, lins[4*15+1:])
	mp3dSynthPair(dstr[32*nch:], nch, lins[4*15+64+1:])
	mp3dSynthPair(dstl, nch, lins[4*15:])
	mp3dSynthPair(dstl[32*nch:], nch, lins[4*15+64:])

	for i := 14; i >= 0; i-- {
		var a, b [4]float32
		zbase := synthZOff + 4*i

		lins[zbase+0] = xl[18*(31-i)]
		lins[zbase+1] = xr[18*(31-i)]
		lins[zbase+2] = xl[1+18*(31-i)]
		lins[zbase+3] = xr[1+18*(31-i)]
		lins[zbase+64] = xl[1+18*(1+i)]
		lins[zbase+64+1] = xr[1+18*(1+i)]
		lins[zbase-64+2] = xl[18*(1+i)]
		lins[zbase-64+3] = xr[18*(1+i)]

		// S0(0) S2(1) S1(2) S2(3) S1(4) S2(5) S1(6) S2(7): one advancing
		// window cursor, vz/vy strided by 64 floats per step k.
		steps := [8]int{0, 2, 1, 2, 1, 2, 1, 2}
		for k, mode := range steps {
			w0 := gWin[wIdx]
			w1 := gWin[wIdx+1]
			wIdx += 2
			vz := lins[zbase-k*64:]
			vy := lins[zbase-(15-k)*64:]
			mp3dSynthStep(&a, &b, vz, vy, w0, w1, mode)
		}

		dstr[(15-i)*nch] = mp3dScalePcm(a[1])
		dstr[(17+i)*nch] = mp3dScalePcm(b[1])
		dstl[(15-i)*nch] = mp3dScalePcm(a[0])
		dstl[(17+i)*nch] = mp3dScalePcm(b[0])
		dstr[(47-i)*nch] = mp3dScalePcm(a[3])
		dstr[(49+i)*nch] = mp3dScalePcm(b[3])
		dstl[(47-i)*nch] = mp3dScalePcm(a[2])
		dstl[(49+i)*nch] = mp3dScalePcm(b[2])
	}
}

// mp3dSynthGranule mirrors upstream mp3d_synth_granule
// (tools/oracle/minimp3.h:1629-1655): runs the DCT-II over each channel's
// nbands columns, seeds the filterbank memory from qmfState, synthesizes each
// even subband pair into pcm, then saves the tail back into qmfState. grbuf
// is the flat 2*576 spectral buffer (channel c at grbuf[576*c:]); lins is the
// scratch syn buffer. MINIMP3_NONSTANDARD_BUT_LOGICAL is not defined in the
// oracle build, so the mono write-back uses the strided (every-other-float)
// form.
func mp3dSynthGranule(qmfState, grbuf []float32, nbands, nch int, pcm, lins []float32) {
	for i := range nch {
		mp3dDctII(grbuf[576*i:576*i+576], nbands)
	}

	copy(lins[:15*64], qmfState[:15*64])

	for i := 0; i < nbands; i += 2 {
		mp3dSynth(grbuf[i:], pcm[32*nch*i:], nch, lins[i*64:])
	}

	if nch == 1 {
		for i := 0; i < 15*64; i += 2 {
			qmfState[i] = lins[nbands*64+i]
		}
	} else {
		copy(qmfState[:15*64], lins[nbands*64:nbands*64+15*64])
	}
}
