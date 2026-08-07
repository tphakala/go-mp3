package dec

import "github.com/tphakala/go-mp3/internal/bits"

// fourThirds and twoNinths mirror the two f-suffixed literal fractions in
// l3Pow43's rational approximation (tools/oracle/minimp3.h:738: 4.f/3 and
// 2.f/9). In C, dividing a float literal by a plain int promotes the int
// straight to float (not double, since one operand is already float and
// float outranks int but is itself the target of the usual arithmetic
// conversion, not double), so both are genuine float32 divisions; Go's
// untyped constant arithmetic, rounded once to float32 on assignment,
// reproduces the same correctly-rounded result.
const (
	fourThirds float32 = 4.0 / 3.0
	twoNinths  float32 = 2.0 / 9.0
)

// l3Pow43 mirrors upstream L3_pow_43 (tools/oracle/minimp3.h:721-740): the
// magnitude x^(4/3) for a dequantized Huffman symbol x, read directly from
// pow43Table for x<129 and otherwise approximated by a rational polynomial
// seeded from the same table.
//
// Float discipline: every literal in this function is float32 (the `f`
// suffix on 1.f/4.f/2.f in the C source), and every division pairs a float
// operand with a plain int (x, 3, 9, mult) rather than a double, so C's
// usual arithmetic conversions promote the int operand directly to
// float32. Unlike l3LdexpQ2's expfracTable (bare, unsuffixed double
// literals), there is no double promotion anywhere in this function, so
// every operation below is plain float32 arithmetic in upstream's exact
// order.
func l3Pow43(x int) float32 {
	if x < 129 {
		return pow43Table[16+x]
	}

	mult := 256
	if x < 1024 {
		mult = 16
		x <<= 3
	}

	sign := 2 * x & 64
	frac := float32((x&63)-sign) / float32((x&^63)+sign)
	// float32() on each product feeding a + forces a rounding barrier that
	// blocks arm64 FMA fusion (a bare local assignment does not; the Go
	// compiler fuses across statements). This preserves the pin's exact order
	// (minimp3 L3_pow_43: g_pow43[...]*(1.f + frac*(4.f/3 + frac*(2.f/9)))*mult):
	// frac*twoNinths rounds, then adds fourThirds; frac*inner rounds, then
	// adds 1; the outer g_pow43[...] * outer * mult is a pure multiply chain
	// that does not fuse and needs no wrap.
	inner := fourThirds + float32(frac*twoNinths)
	outer := 1 + float32(frac*inner)
	return pow43Table[16+((x+sign)>>6)] * outer * float32(mult)
}

// peekBits returns bs's next n bits (n in [0,24]) without advancing its
// position, mirroring upstream's PEEK_BITS macro (tools/oracle/minimp3.h:765)
// against the local 32-bit cache l3Huffman keeps in bs_cache. bits.Reader
// has no native peek, so this reads via Bits and rewinds: Bits is a pure
// function of position and n (aside from latching Overrun, see below), so
// the returned value is identical to a true peek.
//
// A peek that would cross bs's limit spuriously latches bs.Overrun(),
// since Bits() checks that before touching the buffer. l3Huffman itself
// never consults Overrun (mirroring upstream, which tracks no such flag
// during Huffman decode: only the explicit BSPOS > layer3gr_limit check
// gates anything), so this has no effect observable from this file.
func peekBits(bs *bits.Reader, n int) uint32 {
	pos := bs.Pos()
	v := bs.Bits(n)
	bs.SetPos(pos)
	return v
}

// peekBitsAfter peeks n bits starting skip bits ahead of bs's current
// position, without advancing it. This is for the one site (the count1
// "quad" codebook's escape lookup) where upstream peeks bits it has
// already peeked but not yet flushed: tools/oracle/minimp3.h:858's
// `bs_cache << 4 >> (32 - (leaf & 3))` reads (leaf&3) bits starting 4 bits
// into the still-unflushed cache, i.e. peekBitsAfter(bs, 4, leaf&3).
func peekBitsAfter(bs *bits.Reader, skip, n int) uint32 {
	pos := bs.Pos()
	bs.SetPos(pos + skip)
	v := bs.Bits(n)
	bs.SetPos(pos)
	return v
}

// flushBits advances bs by n bits without reading a value, mirroring
// upstream's FLUSH_BITS macro (tools/oracle/minimp3.h:766).
//
// Upstream's CHECK_BITS macro (minimp3.h:767) has no counterpart here: it
// exists purely to keep bs_cache's manual 32-bit window topped up from
// bs_next_ptr, with no semantic effect on the decode itself. Since this
// port reads bits.Reader directly (a pure function of bit position) rather
// than maintaining a local cache, there is nothing for it to keep topped
// up, so every CHECK_BITS call site upstream is simply omitted below.
func flushBits(bs *bits.Reader, n int) {
	bs.SetPos(bs.Pos() + n)
}

// huffmanLeaf walks codebook's canonical Huffman tree from bs's current
// position and returns the decoded leaf, mirroring the codeword lookup
// that upstream's big-values loop performs identically in both its
// linbits and no-linbits branches (tools/oracle/minimp3.h:791-801 and
// :828-838): peek 5 bits, follow negative ("escape") table entries by
// widening the peek per the entry's low 3 bits and re-indexing with its
// remaining bits as a (wrapping, per C's uint32_t-int usual-arithmetic-
// conversion) offset, until a non-negative leaf is found, then flush its
// code length (leaf>>8). Factored out because the two upstream call sites
// are otherwise byte-for-byte identical; this changes no operation or
// order performed at either site.
func huffmanLeaf(bs *bits.Reader, codebook []int16) int {
	w := 5
	leaf := int(codebook[peekBits(bs, w)])
	for leaf < 0 {
		flushBits(bs, w)
		w = leaf & 7
		// codebook[PEEK_BITS(w) - (leaf>>3)]: leaf>>3 is a signed
		// arithmetic shift (leaf<0 here) of an int; PEEK_BITS(w) is
		// uint32_t. C's usual arithmetic conversions convert the signed
		// int to uint32_t before subtracting, giving two's-complement
		// wraparound that adds the (positive) magnitude of leaf>>3.
		leaf = int(codebook[peekBits(bs, w)-uint32(leaf>>3)])
	}
	flushBits(bs, leaf>>8)
	return leaf
}

// l3Huffman mirrors upstream L3_huffman (tools/oracle/minimp3.h:742-877):
// decodes gr's Huffman-coded spectral coefficients into dst (576 entries,
// dequantized against scf and gr's global gain via scf's already-applied
// scaling), consuming bits from bs starting at its current position, and
// finally sets bs's position to layer3gr, mirroring upstream's
// unconditional `bs->pos = layer3gr_limit` at the very end regardless of
// how far the actual decode reached.
//
// dst is not zeroed by this function: upstream relies on the caller
// memset-ing mp3dec_scratch_t.grbuf before every granule
// (tools/oracle/minimp3.h:1772), so any dst entries beyond what this
// function actually writes are left however the caller initialized them,
// exactly mirroring upstream.
//nolint:gocognit,gocyclo // faithful port of minimp3 L3_huffman; structure mirrors the pin for auditability and bit-exactness
func l3Huffman(dst []float32, bs *bits.Reader, gr *grInfo, scf []float32, layer3gr int) {
	dstIdx, sfbIdx, scfIdx := 0, 0, 0
	one := float32(0)
	ireg := 0
	bigValCnt := int(gr.bigValues)
	sfb := gr.sfbTab

	for bigValCnt > 0 {
		tabNum := int(gr.tableSelect[ireg])
		sfbCnt := int(gr.regionCount[ireg])
		ireg++
		codebook := huffTabs[huffTabIndex[tabNum]:]
		linbits := int(linbitsTable[tabNum])

		if linbits != 0 {
			for {
				np := int(sfb[sfbIdx]) / 2
				sfbIdx++
				pairsToDecode := min(bigValCnt, np)
				one = scf[scfIdx]
				scfIdx++

				for {
					leaf := huffmanLeaf(bs, codebook)

					for range 2 {
						lsb := leaf & 0x0F
						if lsb == 15 {
							lsb += int(peekBits(bs, linbits))
							flushBits(bs, linbits)
							sign := float32(1)
							if peekBits(bs, 1) != 0 {
								sign = -1
							}
							dst[dstIdx] = one * l3Pow43(lsb) * sign
						} else {
							sign := 0
							if peekBits(bs, 1) != 0 {
								sign = 1
							}
							dst[dstIdx] = pow43Table[16+lsb-16*sign] * one
						}
						if lsb != 0 {
							flushBits(bs, 1)
						}
						dstIdx++
						leaf >>= 4
					}

					pairsToDecode--
					if pairsToDecode == 0 {
						break
					}
				}

				bigValCnt -= np
				sfbCnt--
				if bigValCnt <= 0 || sfbCnt < 0 {
					break
				}
			}
		} else {
			for {
				np := int(sfb[sfbIdx]) / 2
				sfbIdx++
				pairsToDecode := min(bigValCnt, np)
				one = scf[scfIdx]
				scfIdx++

				for {
					leaf := huffmanLeaf(bs, codebook)

					for range 2 {
						lsb := leaf & 0x0F
						sign := 0
						if peekBits(bs, 1) != 0 {
							sign = 1
						}
						dst[dstIdx] = pow43Table[16+lsb-16*sign] * one
						if lsb != 0 {
							flushBits(bs, 1)
						}
						dstIdx++
						leaf >>= 4
					}

					pairsToDecode--
					if pairsToDecode == 0 {
						break
					}
				}

				bigValCnt -= np
				sfbCnt--
				if bigValCnt <= 0 || sfbCnt < 0 {
					break
				}
			}
		}
	}

	np := 1 - bigValCnt
	for {
		codebookCount1 := tab32Table[:]
		if gr.count1Table != 0 {
			codebookCount1 = tab33Table[:]
		}
		leaf := int(codebookCount1[peekBits(bs, 4)])
		if leaf&8 == 0 {
			leaf = int(codebookCount1[(leaf>>3)+int(peekBitsAfter(bs, 4, leaf&3))])
		}
		flushBits(bs, leaf&7)
		if bs.Pos() > layer3gr {
			break
		}

		np--
		if np == 0 {
			np = int(sfb[sfbIdx]) / 2
			sfbIdx++
			if np == 0 {
				break
			}
			one = scf[scfIdx]
			scfIdx++
		}
		if leaf&128 != 0 {
			v := one
			if peekBits(bs, 1) != 0 {
				v = -one
			}
			dst[dstIdx+0] = v
			flushBits(bs, 1)
		}
		if leaf&64 != 0 {
			v := one
			if peekBits(bs, 1) != 0 {
				v = -one
			}
			dst[dstIdx+1] = v
			flushBits(bs, 1)
		}

		np--
		if np == 0 {
			np = int(sfb[sfbIdx]) / 2
			sfbIdx++
			if np == 0 {
				break
			}
			one = scf[scfIdx]
			scfIdx++
		}
		if leaf&32 != 0 {
			v := one
			if peekBits(bs, 1) != 0 {
				v = -one
			}
			dst[dstIdx+2] = v
			flushBits(bs, 1)
		}
		if leaf&16 != 0 {
			v := one
			if peekBits(bs, 1) != 0 {
				v = -one
			}
			dst[dstIdx+3] = v
			flushBits(bs, 1)
		}

		dstIdx += 4
	}

	bs.SetPos(layer3gr)
}
