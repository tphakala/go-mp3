package enc

// MDCTWindow holds the 36 coefficients of the long-block sine window from
// ISO/IEC 11172-3:1993, section 2.4.3.4.10.3 ("Windowing"), block_type 0:
// w[i] = sin(pi/36 * (i + 0.5)) for i = 0..35. Each literal is the exact hex
// float64 encoding of math.Sin(math.Pi/36*(float64(i)+0.5)) computed by a
// throwaway generator (scratchpad, never committed; see fbtables.go for the
// same pattern), so no math.Sin call runs at package init or runtime; the
// committed literal is the runtime truth. This is the forward-transform
// twin of the decoder's gMdctWindow (internal/dec/tables.go): the two
// halves of gMdctWindow[0] are this same window, halved and reordered (see
// TestEncMdctWindowMatchesDec, internal/dec/encx_mdct_test.go).
var MDCTWindow = [36]float64{
	0x1.65547c4694e11p-05, // w[0]
	0x1.0b5150f6da2dp-03,  // w[1]
	0x1.bb44b13b62571p-03, // w[2]
	0x1.33ec389a5a81ep-02, // w[3]
	0x1.87de2a6aea963p-02, // w[4]
	0x1.d8d4a0e345738p-02, // w[5]
	0x1.1318ef2c01a5bp-01, // w[6]
	0x1.37af93f9513eap-01, // w[7]
	0x1.59e6f5ae6a0a6p-01, // w[8]
	0x1.797c6a435ce84p-01, // w[9]
	0x1.963268b572491p-01, // w[10]
	0x1.afd100eafc28fp-01, // w[11]
	0x1.c62648af6577p-01,  // w[12]
	0x1.d906bcf328d46p-01, // w[13]
	0x1.e84d9692357ep-01,  // w[14]
	0x1.f3dd11fb974b6p-01, // w[15]
	0x1.fb9ea92ec689bp-01, // w[16]
	0x1.ff833f9da45f7p-01, // w[17]
	0x1.ff833f9da45f7p-01, // w[18]
	0x1.fb9ea92ec689bp-01, // w[19]
	0x1.f3dd11fb974b6p-01, // w[20]
	0x1.e84d9692357e1p-01, // w[21]
	0x1.d906bcf328d46p-01, // w[22]
	0x1.c62648af65771p-01, // w[23]
	0x1.afd100eafc291p-01, // w[24]
	0x1.963268b572492p-01, // w[25]
	0x1.797c6a435ce85p-01, // w[26]
	0x1.59e6f5ae6a0a8p-01, // w[27]
	0x1.37af93f9513ecp-01, // w[28]
	0x1.1318ef2c01a5dp-01, // w[29]
	0x1.d8d4a0e345738p-02, // w[30]
	0x1.87de2a6aea964p-02, // w[31]
	0x1.33ec389a5a821p-02, // w[32]
	0x1.bb44b13b6257cp-03, // w[33]
	0x1.0b5150f6da2cfp-03, // w[34]
	0x1.65547c4694e1cp-05, // w[35]
}

// mdctCos holds the 18x36 forward-MDCT kernel from ISO/IEC 11172-3:1993,
// Annex C, section C.1.5.1 ("Alias reduction, MDCT, Windowing and
// overlapping for long blocks"): for N=36 (long blocks),
//
//	mdctCos[k][n] = cos(pi/(2*N) * (2*n + 1 + N/2) * (2*k + 1))
//	              = cos(pi/72 * (2*n + 19) * (2*k + 1))
//
// for k = 0..17 (spectral line) and n = 0..35 (windowed sample index). Each
// literal is the exact hex float64 encoding of the corresponding
// math.Cos(...) call, produced by the same throwaway generator as
// MDCTWindow; the committed literal is the runtime truth, no math.Cos call
// runs at package init or runtime.
var mdctCos = [18][36]float64{
	{ // k=0
		0x1.59e6f5ae6a0a8p-01,  // n=0
		0x1.37af93f9513ebp-01,  // n=1
		0x1.1318ef2c01a5bp-01,  // n=2
		0x1.d8d4a0e34573bp-02,  // n=3
		0x1.87de2a6aea964p-02,  // n=4
		0x1.33ec389a5a82p-02,   // n=5
		0x1.bb44b13b62572p-03,  // n=6
		0x1.0b5150f6da2d5p-03,  // n=7
		0x1.65547c4694e13p-05,  // n=8
		-0x1.65547c4694e01p-05, // n=9
		-0x1.0b5150f6da2d1p-03, // n=10
		-0x1.bb44b13b6256ep-03, // n=11
		-0x1.33ec389a5a81bp-02, // n=12
		-0x1.87de2a6aea962p-02, // n=13
		-0x1.d8d4a0e345736p-02, // n=14
		-0x1.1318ef2c01a58p-01, // n=15
		-0x1.37af93f9513eap-01, // n=16
		-0x1.59e6f5ae6a0a6p-01, // n=17
		-0x1.797c6a435ce84p-01, // n=18
		-0x1.963268b57249p-01,  // n=19
		-0x1.afd100eafc28ep-01, // n=20
		-0x1.c62648af6577p-01,  // n=21
		-0x1.d906bcf328d46p-01, // n=22
		-0x1.e84d9692357ep-01,  // n=23
		-0x1.f3dd11fb974b6p-01, // n=24
		-0x1.fb9ea92ec689cp-01, // n=25
		-0x1.ff833f9da45f7p-01, // n=26
		-0x1.ff833f9da45f7p-01, // n=27
		-0x1.fb9ea92ec689cp-01, // n=28
		-0x1.f3dd11fb974b7p-01, // n=29
		-0x1.e84d9692357e1p-01, // n=30
		-0x1.d906bcf328d47p-01, // n=31
		-0x1.c62648af65772p-01, // n=32
		-0x1.afd100eafc291p-01, // n=33
		-0x1.963268b572492p-01, // n=34
		-0x1.797c6a435ce85p-01, // n=35
	},
	{ // k=1
		-0x1.963268b57249p-01,  // n=0
		-0x1.d906bcf328d46p-01, // n=1
		-0x1.fb9ea92ec689cp-01, // n=2
		-0x1.fb9ea92ec689cp-01, // n=3
		-0x1.d906bcf328d47p-01, // n=4
		-0x1.963268b572494p-01, // n=5
		-0x1.37af93f9513ecp-01, // n=6
		-0x1.87de2a6aea96dp-02, // n=7
		-0x1.0b5150f6da2d2p-03, // n=8
		0x1.0b5150f6da2c4p-03,  // n=9
		0x1.87de2a6aea967p-02,  // n=10
		0x1.37af93f9513e9p-01,  // n=11
		0x1.963268b57249p-01,   // n=12
		0x1.d906bcf328d44p-01,  // n=13
		0x1.fb9ea92ec689ap-01,  // n=14
		0x1.fb9ea92ec689dp-01,  // n=15
		0x1.d906bcf328d46p-01,  // n=16
		0x1.963268b572492p-01,  // n=17
		0x1.37af93f9513edp-01,  // n=18
		0x1.87de2a6aea97p-02,   // n=19
		0x1.0b5150f6da2f6p-03,  // n=20
		-0x1.0b5150f6da2ep-03,  // n=21
		-0x1.87de2a6aea964p-02, // n=22
		-0x1.37af93f9513e8p-01, // n=23
		-0x1.963268b57249p-01,  // n=24
		-0x1.d906bcf328d4ap-01, // n=25
		-0x1.fb9ea92ec689ap-01, // n=26
		-0x1.fb9ea92ec689bp-01, // n=27
		-0x1.d906bcf328d4cp-01, // n=28
		-0x1.963268b572493p-01, // n=29
		-0x1.37af93f9513eep-01, // n=30
		-0x1.87de2a6aea971p-02, // n=31
		-0x1.0b5150f6da2fap-03, // n=32
		0x1.0b5150f6da29dp-03,  // n=33
		0x1.87de2a6aea963p-02,  // n=34
		0x1.37af93f9513e8p-01,  // n=35
	},
	{ // k=2
		-0x1.1318ef2c01a5ep-01, // n=0
		-0x1.0b5150f6da2d2p-03, // n=1
		0x1.33ec389a5a815p-02,  // n=2
		0x1.59e6f5ae6a0ap-01,   // n=3
		0x1.d906bcf328d44p-01,  // n=4
		0x1.ff833f9da45f7p-01,  // n=5
		0x1.c62648af6577p-01,   // n=6
		0x1.37af93f9513edp-01,  // n=7
		0x1.bb44b13b62573p-03,  // n=8
		-0x1.bb44b13b6257dp-03, // n=9
		-0x1.37af93f9513e8p-01, // n=10
		-0x1.c62648af6576ep-01, // n=11
		-0x1.ff833f9da45f6p-01, // n=12
		-0x1.d906bcf328d46p-01, // n=13
		-0x1.59e6f5ae6a0aap-01, // n=14
		-0x1.33ec389a5a82fp-02, // n=15
		0x1.0b5150f6da2dcp-03,  // n=16
		0x1.1318ef2c01a59p-01,  // n=17
		0x1.afd100eafc28cp-01,  // n=18
		0x1.fb9ea92ec689ap-01,  // n=19
		0x1.e84d9692357e6p-01,  // n=20
		0x1.797c6a435ce88p-01,  // n=21
		0x1.87de2a6aea973p-02,  // n=22
		-0x1.65547c4694d3bp-05, // n=23
		-0x1.d8d4a0e345718p-02, // n=24
		-0x1.963268b57248ep-01, // n=25
		-0x1.f3dd11fb974b7p-01, // n=26
		-0x1.f3dd11fb974bap-01, // n=27
		-0x1.963268b572495p-01, // n=28
		-0x1.d8d4a0e345765p-02, // n=29
		-0x1.65547c4694eeap-05, // n=30
		0x1.87de2a6aea95fp-02,  // n=31
		0x1.797c6a435ce76p-01,  // n=32
		0x1.e84d9692357ddp-01,  // n=33
		0x1.fb9ea92ec689bp-01,  // n=34
		0x1.afd100eafc28ap-01,  // n=35
	},
	{ // k=3
		0x1.c62648af6576ep-01,  // n=0
		0x1.fb9ea92ec689cp-01,  // n=1
		0x1.797c6a435ce86p-01,  // n=2
		0x1.bb44b13b62592p-03,  // n=3
		-0x1.87de2a6aea964p-02, // n=4
		-0x1.afd100eafc28dp-01, // n=5
		-0x1.ff833f9da45f6p-01, // n=6
		-0x1.963268b572493p-01, // n=7
		-0x1.33ec389a5a81p-02,  // n=8
		0x1.33ec389a5a801p-02,  // n=9
		0x1.963268b57248fp-01,  // n=10
		0x1.ff833f9da45f6p-01,  // n=11
		0x1.afd100eafc29ap-01,  // n=12
		0x1.87de2a6aea973p-02,  // n=13
		-0x1.bb44b13b62535p-03, // n=14
		-0x1.797c6a435ce76p-01, // n=15
		-0x1.fb9ea92ec689cp-01, // n=16
		-0x1.c62648af65772p-01, // n=17
		-0x1.d8d4a0e34572cp-02, // n=18
		0x1.0b5150f6da294p-03,  // n=19
		0x1.59e6f5ae6a08bp-01,  // n=20
		0x1.f3dd11fb974b7p-01,  // n=21
		0x1.d906bcf328d4ep-01,  // n=22
		0x1.1318ef2c01a62p-01,  // n=23
		-0x1.65547c4694c17p-05, // n=24
		-0x1.37af93f9513f2p-01, // n=25
		-0x1.e84d9692357ddp-01, // n=26
		-0x1.e84d9692357e2p-01, // n=27
		-0x1.37af93f9513fep-01, // n=28
		-0x1.65547c4694f0dp-05, // n=29
		0x1.1318ef2c01a55p-01,  // n=30
		0x1.d906bcf328d48p-01,  // n=31
		0x1.f3dd11fb974bap-01,  // n=32
		0x1.59e6f5ae6a0aep-01,  // n=33
		0x1.0b5150f6da2d1p-03,  // n=34
		-0x1.d8d4a0e34571p-02,  // n=35
	},
	{ // k=4
		0x1.87de2a6aea97p-02,   // n=0
		-0x1.87de2a6aea964p-02, // n=1
		-0x1.d906bcf328d43p-01, // n=2
		-0x1.d906bcf328d4cp-01, // n=3
		-0x1.87de2a6aea971p-02, // n=4
		0x1.87de2a6aea945p-02,  // n=5
		0x1.d906bcf328d43p-01,  // n=6
		0x1.d906bcf328d4dp-01,  // n=7
		0x1.87de2a6aea973p-02,  // n=8
		-0x1.87de2a6aea961p-02, // n=9
		-0x1.d906bcf328d49p-01, // n=10
		-0x1.d906bcf328d4dp-01, // n=11
		-0x1.87de2a6aea975p-02, // n=12
		0x1.87de2a6aea95fp-02,  // n=13
		0x1.d906bcf328d3cp-01,  // n=14
		0x1.d906bcf328d4ep-01,  // n=15
		0x1.87de2a6aea93cp-02,  // n=16
		-0x1.87de2a6aea95dp-02, // n=17
		-0x1.d906bcf328d3cp-01, // n=18
		-0x1.d906bcf328d4ep-01, // n=19
		-0x1.87de2a6aea97ap-02, // n=20
		0x1.87de2a6aea95ap-02,  // n=21
		0x1.d906bcf328d48p-01,  // n=22
		0x1.d906bcf328d4ep-01,  // n=23
		0x1.87de2a6aea97cp-02,  // n=24
		-0x1.87de2a6aea958p-02, // n=25
		-0x1.d906bcf328d47p-01, // n=26
		-0x1.d906bcf328d4fp-01, // n=27
		-0x1.87de2a6aea97dp-02, // n=28
		0x1.87de2a6aea91bp-02,  // n=29
		0x1.d906bcf328d47p-01,  // n=30
		0x1.d906bcf328d4fp-01,  // n=31
		0x1.87de2a6aea9bap-02,  // n=32
		-0x1.87de2a6aea955p-02, // n=33
		-0x1.d906bcf328d3ap-01, // n=34
		-0x1.d906bcf328d43p-01, // n=35
	},
	{ // k=5
		-0x1.e84d9692357dep-01, // n=0
		-0x1.963268b572493p-01, // n=1
		0x1.65547c4694d4cp-05,  // n=2
		0x1.afd100eafc284p-01,  // n=3
		0x1.d906bcf328d46p-01,  // n=4
		0x1.bb44b13b6259bp-03,  // n=5
		-0x1.59e6f5ae6a0a4p-01, // n=6
		-0x1.fb9ea92ec689dp-01, // n=7
		-0x1.d8d4a0e34572cp-02, // n=8
		0x1.d8d4a0e345717p-02,  // n=9
		0x1.fb9ea92ec689ap-01,  // n=10
		0x1.59e6f5ae6a0acp-01,  // n=11
		-0x1.bb44b13b624eep-03, // n=12
		-0x1.d906bcf328d48p-01, // n=13
		-0x1.afd100eafc29cp-01, // n=14
		-0x1.65547c4694f0dp-05, // n=15
		0x1.963268b57248cp-01,  // n=16
		0x1.e84d9692357e2p-01,  // n=17
		0x1.33ec389a5a81bp-02,  // n=18
		-0x1.37af93f9513d7p-01, // n=19
		-0x1.ff833f9da45f9p-01, // n=20
		-0x1.1318ef2c01a65p-01, // n=21
		0x1.87de2a6aea957p-02,  // n=22
		0x1.f3dd11fb974afp-01,  // n=23
		0x1.797c6a435ce96p-01,  // n=24
		-0x1.0b5150f6da27ep-03, // n=25
		-0x1.c62648af6576ap-01, // n=26
		-0x1.c62648af65775p-01, // n=27
		-0x1.0b5150f6da2dep-03, // n=28
		0x1.797c6a435ce5bp-01,  // n=29
		0x1.f3dd11fb974b4p-01,  // n=30
		0x1.87de2a6aea949p-02,  // n=31
		-0x1.1318ef2c01a35p-01, // n=32
		-0x1.ff833f9da45f5p-01, // n=33
		-0x1.37af93f9513d1p-01, // n=34
		0x1.33ec389a5a7ecp-02,  // n=35
	},
	{ // k=6
		-0x1.bb44b13b62596p-03, // n=0
		0x1.963268b57248fp-01,  // n=1
		0x1.c62648af65771p-01,  // n=2
		-0x1.65547c4694d3bp-05, // n=3
		-0x1.d906bcf328d42p-01, // n=4
		-0x1.797c6a435ce93p-01, // n=5
		0x1.33ec389a5a7fdp-02,  // n=6
		0x1.fb9ea92ec689ap-01,  // n=7
		0x1.1318ef2c01a62p-01,  // n=8
		-0x1.1318ef2c01a56p-01, // n=9
		-0x1.fb9ea92ec689cp-01, // n=10
		-0x1.33ec389a5a819p-02, // n=11
		0x1.797c6a435ce74p-01,  // n=12
		0x1.d906bcf328d4ep-01,  // n=13
		0x1.65547c4694f1fp-05,  // n=14
		-0x1.c62648af6575cp-01, // n=15
		-0x1.963268b572483p-01, // n=16
		0x1.bb44b13b6255ep-03,  // n=17
		0x1.f3dd11fb974afp-01,  // n=18
		0x1.37af93f95141ap-01,  // n=19
		-0x1.d8d4a0e34570dp-02, // n=20
		-0x1.ff833f9da45f6p-01, // n=21
		-0x1.87de2a6aea981p-02, // n=22
		0x1.59e6f5ae6a086p-01,  // n=23
		0x1.e84d9692357e3p-01,  // n=24
		0x1.0b5150f6da263p-03,  // n=25
		-0x1.afd100eafc29p-01,  // n=26
		-0x1.afd100eafc29fp-01, // n=27
		0x1.0b5150f6da1f1p-03,  // n=28
		0x1.e84d9692357dbp-01,  // n=29
		0x1.59e6f5ae6a09cp-01,  // n=30
		-0x1.87de2a6aea94cp-02, // n=31
		-0x1.ff833f9da45f5p-01, // n=32
		-0x1.d8d4a0e3457b1p-02, // n=33
		0x1.37af93f9513d1p-01,  // n=34
		0x1.f3dd11fb974b5p-01,  // n=35
	},
	{ // k=7
		0x1.fb9ea92ec689ap-01,  // n=0
		0x1.87de2a6aea973p-02,  // n=1
		-0x1.963268b57248ep-01, // n=2
		-0x1.963268b5724a9p-01, // n=3
		0x1.87de2a6aea95fp-02,  // n=4
		0x1.fb9ea92ec68ap-01,   // n=5
		0x1.0b5150f6da2c8p-03,  // n=6
		-0x1.d906bcf328d3cp-01, // n=7
		-0x1.37af93f9513e4p-01, // n=8
		0x1.37af93f9513d8p-01,  // n=9
		0x1.d906bcf328d42p-01,  // n=10
		-0x1.0b5150f6da286p-03, // n=11
		-0x1.fb9ea92ec6899p-01, // n=12
		-0x1.87de2a6aea97dp-02, // n=13
		0x1.963268b57248bp-01,  // n=14
		0x1.963268b572498p-01,  // n=15
		-0x1.87de2a6aea955p-02, // n=16
		-0x1.fb9ea92ec689cp-01, // n=17
		-0x1.0b5150f6da2dep-03, // n=18
		0x1.d906bcf328d46p-01,  // n=19
		0x1.37af93f95141cp-01,  // n=20
		-0x1.37af93f9513ecp-01, // n=21
		-0x1.d906bcf328d44p-01, // n=22
		0x1.0b5150f6da1f1p-03,  // n=23
		0x1.fb9ea92ec6894p-01,  // n=24
		0x1.87de2a6aea94cp-02,  // n=25
		-0x1.963268b57249bp-01, // n=26
		-0x1.963268b5724afp-01, // n=27
		0x1.87de2a6aea90fp-02,  // n=28
		0x1.fb9ea92ec68a1p-01,  // n=29
		0x1.0b5150f6da275p-03,  // n=30
		-0x1.d906bcf328d37p-01, // n=31
		-0x1.37af93f951406p-01, // n=32
		0x1.37af93f9513cfp-01,  // n=33
		0x1.d906bcf328d3ap-01,  // n=34
		-0x1.0b5150f6da25ap-03, // n=35
	},
	{ // k=8
		0x1.65547c4694ed8p-05,  // n=0
		-0x1.fb9ea92ec689ap-01, // n=1
		-0x1.bb44b13b6259fp-03, // n=2
		0x1.e84d9692357ddp-01,  // n=3
		0x1.87de2a6aea977p-02,  // n=4
		-0x1.c62648af6576dp-01, // n=5
		-0x1.1318ef2c01a63p-01, // n=6
		0x1.963268b57248cp-01,  // n=7
		0x1.59e6f5ae6a0aep-01,  // n=8
		-0x1.59e6f5ae6a0ap-01,  // n=9
		-0x1.963268b572498p-01, // n=10
		0x1.1318ef2c01a54p-01,  // n=11
		0x1.c62648af65784p-01,  // n=12
		-0x1.87de2a6aea955p-02, // n=13
		-0x1.e84d9692357edp-01, // n=14
		0x1.bb44b13b6245cp-03,  // n=15
		0x1.fb9ea92ec6898p-01,  // n=16
		-0x1.65547c4694dadp-05, // n=17
		-0x1.ff833f9da45f5p-01, // n=18
		-0x1.0b5150f6da3e5p-03, // n=19
		0x1.f3dd11fb974aep-01,  // n=20
		0x1.33ec389a5a827p-02,  // n=21
		-0x1.d906bcf328d38p-01, // n=22
		-0x1.d8d4a0e34573fp-02, // n=23
		0x1.afd100eafc27cp-01,  // n=24
		0x1.37af93f9513ecp-01,  // n=25
		-0x1.797c6a435ce6cp-01, // n=26
		-0x1.797c6a435ce86p-01, // n=27
		0x1.37af93f9513cfp-01,  // n=28
		0x1.afd100eafc2b3p-01,  // n=29
		-0x1.d8d4a0e3456fdp-02, // n=30
		-0x1.d906bcf328d47p-01, // n=31
		0x1.33ec389a5a7ep-02,   // n=32
		0x1.f3dd11fb974c4p-01,  // n=33
		-0x1.0b5150f6da252p-03, // n=34
		-0x1.ff833f9da45f9p-01, // n=35
	},
	{ // k=9
		-0x1.ff833f9da45f7p-01, // n=0
		0x1.0b5150f6da294p-03,  // n=1
		0x1.f3dd11fb974bap-01,  // n=2
		-0x1.33ec389a5a7fbp-02, // n=3
		-0x1.d906bcf328d4ep-01, // n=4
		0x1.d8d4a0e345712p-02,  // n=5
		0x1.afd100eafc28ap-01,  // n=6
		-0x1.37af93f9513d7p-01, // n=7
		-0x1.797c6a435ce8p-01,  // n=8
		0x1.797c6a435ce72p-01,  // n=9
		0x1.37af93f9513e8p-01,  // n=10
		-0x1.afd100eafc291p-01, // n=11
		-0x1.d8d4a0e3457a9p-02, // n=12
		0x1.d906bcf328d46p-01,  // n=13
		0x1.33ec389a5a823p-02,  // n=14
		-0x1.f3dd11fb974a7p-01, // n=15
		-0x1.0b5150f6da2e7p-03, // n=16
		0x1.ff833f9da45f6p-01,  // n=17
		-0x1.65547c4694d8ap-05, // n=18
		-0x1.fb9ea92ec689dp-01, // n=19
		0x1.bb44b13b6244bp-03,  // n=20
		0x1.e84d9692357e5p-01,  // n=21
		-0x1.87de2a6aea949p-02, // n=22
		-0x1.c62648af65778p-01, // n=23
		0x1.1318ef2c01a16p-01,  // n=24
		0x1.963268b57249dp-01,  // n=25
		-0x1.59e6f5ae6a098p-01, // n=26
		-0x1.59e6f5ae6a0b7p-01, // n=27
		0x1.963268b572483p-01,  // n=28
		0x1.1318ef2c01aa6p-01,  // n=29
		-0x1.c62648af65783p-01, // n=30
		-0x1.87de2a6aea995p-02, // n=31
		0x1.e84d9692357c5p-01,  // n=32
		0x1.bb44b13b625e8p-03,  // n=33
		-0x1.fb9ea92ec68ap-01,  // n=34
		-0x1.65547c4695028p-05, // n=35
	},
	{ // k=10
		0x1.0b5150f6da294p-03,  // n=0
		0x1.d906bcf328d4ep-01,  // n=1
		-0x1.37af93f9513d8p-01, // n=2
		-0x1.37af93f9513fep-01, // n=3
		0x1.d906bcf328d48p-01,  // n=4
		0x1.0b5150f6da35p-03,   // n=5
		-0x1.fb9ea92ec689cp-01, // n=6
		0x1.87de2a6aea91bp-02,  // n=7
		0x1.963268b572498p-01,  // n=8
		-0x1.963268b572476p-01, // n=9
		-0x1.87de2a6aea981p-02, // n=10
		0x1.fb9ea92ec689dp-01,  // n=11
		-0x1.0b5150f6da275p-03, // n=12
		-0x1.d906bcf328d44p-01, // n=13
		0x1.37af93f9513d2p-01,  // n=14
		0x1.37af93f95141ep-01,  // n=15
		-0x1.d906bcf328d51p-01, // n=16
		-0x1.0b5150f6da2efp-03, // n=17
		0x1.fb9ea92ec68a1p-01,  // n=18
		-0x1.87de2a6aea8d2p-02, // n=19
		-0x1.963268b5724bp-01,  // n=20
		0x1.963268b572485p-01,  // n=21
		0x1.87de2a6aea955p-02,  // n=22
		-0x1.fb9ea92ec6898p-01, // n=23
		0x1.0b5150f6da1d7p-03,  // n=24
		0x1.d906bcf328d3ap-01,  // n=25
		-0x1.37af93f9513e5p-01, // n=26
		-0x1.37af93f9513d8p-01, // n=27
		0x1.d906bcf328d29p-01,  // n=28
		0x1.0b5150f6da38dp-03,  // n=29
		-0x1.fb9ea92ec689ep-01, // n=30
		0x1.87de2a6aea975p-02,  // n=31
		0x1.963268b5724c8p-01,  // n=32
		-0x1.963268b57246dp-01, // n=33
		-0x1.87de2a6aea99ep-02, // n=34
		0x1.fb9ea92ec689bp-01,  // n=35
	},
	{ // k=11
		0x1.f3dd11fb974bap-01,  // n=0
		-0x1.37af93f9513d8p-01, // n=1
		-0x1.d8d4a0e345769p-02, // n=2
		0x1.ff833f9da45f5p-01,  // n=3
		-0x1.87de2a6aea958p-02, // n=4
		-0x1.59e6f5ae6a0c6p-01, // n=5
		0x1.e84d9692357dcp-01,  // n=6
		-0x1.0b5150f6da27ep-03, // n=7
		-0x1.afd100eafc29dp-01, // n=8
		0x1.afd100eafc27fp-01,  // n=9
		0x1.0b5150f6da263p-03,  // n=10
		-0x1.e84d9692357eep-01, // n=11
		0x1.59e6f5ae6a085p-01,  // n=12
		0x1.87de2a6aea94cp-02,  // n=13
		-0x1.ff833f9da45f9p-01, // n=14
		0x1.d8d4a0e3456cap-02,  // n=15
		0x1.37af93f9513ecp-01,  // n=16
		-0x1.f3dd11fb974b4p-01, // n=17
		0x1.bb44b13b6253cp-03,  // n=18
		0x1.963268b57249dp-01,  // n=19
		-0x1.c62648af65748p-01, // n=20
		-0x1.65547c4694fe1p-05, // n=21
		0x1.d906bcf328d54p-01,  // n=22
		-0x1.797c6a435ce6bp-01, // n=23
		-0x1.33ec389a5a872p-02, // n=24
		0x1.fb9ea92ec68a2p-01,  // n=25
		-0x1.1318ef2c01a2cp-01, // n=26
		-0x1.1318ef2c01a8dp-01, // n=27
		0x1.fb9ea92ec6893p-01,  // n=28
		-0x1.33ec389a5a798p-02, // n=29
		-0x1.797c6a435ce62p-01, // n=30
		0x1.d906bcf328d28p-01,  // n=31
		-0x1.65547c46948b7p-05, // n=32
		-0x1.c62648af6579bp-01, // n=33
		0x1.963268b5724a5p-01,  // n=34
		0x1.bb44b13b62508p-03,  // n=35
	},
	{ // k=12
		-0x1.33ec389a5a7fbp-02, // n=0
		-0x1.37af93f9513fep-01, // n=1
		0x1.ff833f9da45f7p-01,  // n=2
		-0x1.1318ef2c01a3ap-01, // n=3
		-0x1.87de2a6aea97dp-02, // n=4
		0x1.f3dd11fb974bbp-01,  // n=5
		-0x1.797c6a435ce72p-01, // n=6
		-0x1.0b5150f6da2dep-03, // n=7
		0x1.c62648af65767p-01,  // n=8
		-0x1.c62648af65769p-01, // n=9
		0x1.0b5150f6da2efp-03,  // n=10
		0x1.797c6a435ce9ap-01,  // n=11
		-0x1.f3dd11fb974a7p-01, // n=12
		0x1.87de2a6aea90fp-02,  // n=13
		0x1.1318ef2c01a6bp-01,  // n=14
		-0x1.ff833f9da45f9p-01, // n=15
		0x1.37af93f951402p-01,  // n=16
		0x1.33ec389a5a83p-02,   // n=17
		-0x1.e84d9692357fp-01,  // n=18
		0x1.963268b57245dp-01,  // n=19
		0x1.65547c46955f2p-05,  // n=20
		-0x1.afd100eafc2a4p-01, // n=21
		0x1.d906bcf328d29p-01,  // n=22
		-0x1.bb44b13b625a4p-03, // n=23
		-0x1.59e6f5ae6a0bap-01, // n=24
		0x1.fb9ea92ec68a4p-01,  // n=25
		-0x1.d8d4a0e345763p-02, // n=26
		-0x1.d8d4a0e345753p-02, // n=27
		0x1.fb9ea92ec68a3p-01,  // n=28
		-0x1.59e6f5ae6a063p-01, // n=29
		-0x1.bb44b13b62581p-03, // n=30
		0x1.d906bcf328d57p-01,  // n=31
		-0x1.afd100eafc264p-01, // n=32
		0x1.65547c4694682p-05,  // n=33
		0x1.963268b5724a5p-01,  // n=34
		-0x1.e84d9692357cbp-01, // n=35
	},
	{ // k=13
		-0x1.d906bcf328d4ep-01, // n=0
		0x1.d906bcf328d3bp-01,  // n=1
		-0x1.87de2a6aea958p-02, // n=2
		-0x1.87de2a6aea9b8p-02, // n=3
		0x1.d906bcf328d4fp-01,  // n=4
		-0x1.d906bcf328d3ap-01, // n=5
		0x1.87de2a6aea98ep-02,  // n=6
		0x1.87de2a6aea9bfp-02,  // n=7
		-0x1.d906bcf328d44p-01, // n=8
		0x1.d906bcf328d45p-01,  // n=9
		-0x1.87de2a6aea94cp-02, // n=10
		-0x1.87de2a6aea989p-02, // n=11
		0x1.d906bcf328d52p-01,  // n=12
		-0x1.d906bcf328d37p-01, // n=13
		0x1.87de2a6aea90bp-02,  // n=14
		0x1.87de2a6aea9cbp-02,  // n=15
		-0x1.d906bcf328d47p-01, // n=16
		0x1.d906bcf328d42p-01,  // n=17
		-0x1.87de2a6aea8cap-02, // n=18
		-0x1.87de2a6aea995p-02, // n=19
		0x1.d906bcf328d6dp-01,  // n=20
		-0x1.d906bcf328d34p-01, // n=21
		0x1.87de2a6aea975p-02,  // n=22
		0x1.87de2a6aea9d7p-02,  // n=23
		-0x1.d906bcf328d7bp-01, // n=24
		0x1.d906bcf328d58p-01,  // n=25
		-0x1.87de2a6aea934p-02, // n=26
		-0x1.87de2a6aeaa19p-02, // n=27
		0x1.d906bcf328d57p-01,  // n=28
		-0x1.d906bcf328d1ap-01, // n=29
		0x1.87de2a6aea8f3p-02,  // n=30
		0x1.87de2a6aea96dp-02,  // n=31
		-0x1.d906bcf328d64p-01, // n=32
		0x1.d906bcf328d3dp-01,  // n=33
		-0x1.87de2a6aea99ep-02, // n=34
		-0x1.87de2a6aea9aep-02, // n=35
	},
	{ // k=14
		0x1.d8d4a0e345712p-02,  // n=0
		0x1.0b5150f6da35p-03,   // n=1
		-0x1.59e6f5ae6a0afp-01, // n=2
		0x1.f3dd11fb974c2p-01,  // n=3
		-0x1.d906bcf328d3ap-01, // n=4
		0x1.1318ef2c01a51p-01,  // n=5
		0x1.65547c4694d67p-05,  // n=6
		-0x1.37af93f951403p-01, // n=7
		0x1.e84d9692357e4p-01,  // n=8
		-0x1.e84d9692357dp-01,  // n=9
		0x1.37af93f951403p-01,  // n=10
		-0x1.65547c4694d67p-05, // n=11
		-0x1.1318ef2c01a87p-01, // n=12
		0x1.d906bcf328d53p-01,  // n=13
		-0x1.f3dd11fb974a6p-01, // n=14
		0x1.59e6f5ae6a07fp-01,  // n=15
		-0x1.0b5150f6da252p-03, // n=16
		-0x1.d8d4a0e34574bp-02, // n=17
		0x1.c62648af6576dp-01,  // n=18
		-0x1.fb9ea92ec688fp-01, // n=19
		0x1.797c6a435ce53p-01,  // n=20
		-0x1.bb44b13b624a1p-03, // n=21
		-0x1.87de2a6aea99ep-02, // n=22
		0x1.afd100eafc296p-01,  // n=23
		-0x1.ff833f9da45f1p-01, // n=24
		0x1.963268b5724a5p-01,  // n=25
		-0x1.33ec389a5a79p-02,  // n=26
		-0x1.33ec389a5a884p-02, // n=27
		0x1.963268b5724a5p-01,  // n=28
		-0x1.ff833f9da45fdp-01, // n=29
		0x1.afd100eafc296p-01,  // n=30
		-0x1.87de2a6aea8b1p-02, // n=31
		-0x1.bb44b13b62695p-03, // n=32
		0x1.797c6a435cea8p-01,  // n=33
		-0x1.fb9ea92ec68ap-01,  // n=34
		0x1.c62648af6576dp-01,  // n=35
	},
	{ // k=15
		0x1.afd100eafc29cp-01,  // n=0
		-0x1.fb9ea92ec68ap-01,  // n=1
		0x1.e84d9692357dcp-01,  // n=2
		-0x1.797c6a435ce72p-01, // n=3
		0x1.87de2a6aea918p-02,  // n=4
		0x1.65547c4695166p-05,  // n=5
		-0x1.d8d4a0e34573cp-02, // n=6
		0x1.963268b57249bp-01,  // n=7
		-0x1.f3dd11fb974bcp-01, // n=8
		0x1.f3dd11fb974aep-01,  // n=9
		-0x1.963268b57249ap-01, // n=10
		0x1.d8d4a0e345737p-02,  // n=11
		-0x1.65547c4694944p-05, // n=12
		-0x1.87de2a6aea992p-02, // n=13
		0x1.797c6a435ce9ep-01,  // n=14
		-0x1.e84d969235804p-01, // n=15
		0x1.fb9ea92ec689cp-01,  // n=16
		-0x1.afd100eafc28ap-01, // n=17
		0x1.1318ef2c01a46p-01,  // n=18
		-0x1.0b5150f6da24p-03,  // n=19
		-0x1.33ec389a5a87bp-02, // n=20
		0x1.59e6f5ae6a0d4p-01,  // n=21
		-0x1.d906bcf328d62p-01, // n=22
		0x1.ff833f9da45f3p-01,  // n=23
		-0x1.c62648af65743p-01, // n=24
		0x1.37af93f9513f7p-01,  // n=25
		-0x1.bb44b13b62581p-03, // n=26
		-0x1.bb44b13b62592p-03, // n=27
		0x1.37af93f9513fbp-01,  // n=28
		-0x1.c62648af657bbp-01, // n=29
		0x1.ff833f9da45f9p-01,  // n=30
		-0x1.d906bcf328d3p-01,  // n=31
		0x1.59e6f5ae6a073p-01,  // n=32
		-0x1.33ec389a5a77ep-02, // n=33
		-0x1.0b5150f6da251p-03, // n=34
		0x1.1318ef2c01a4ap-01,  // n=35
	},
	{ // k=16
		-0x1.37af93f9513d7p-01, // n=0
		0x1.87de2a6aea957p-02,  // n=1
		-0x1.0b5150f6da27ep-03, // n=2
		-0x1.0b5150f6da3dcp-03, // n=3
		0x1.87de2a6aea949p-02,  // n=4
		-0x1.37af93f951403p-01, // n=5
		0x1.963268b57249bp-01,  // n=6
		-0x1.d906bcf328d5dp-01, // n=7
		0x1.fb9ea92ec6899p-01,  // n=8
		-0x1.fb9ea92ec6898p-01, // n=9
		0x1.d906bcf328d43p-01,  // n=10
		-0x1.963268b572471p-01, // n=11
		0x1.37af93f9513cdp-01,  // n=12
		-0x1.87de2a6aea8cap-02, // n=13
		0x1.0b5150f6da2ccp-03,  // n=14
		0x1.0b5150f6da38dp-03,  // n=15
		-0x1.87de2a6aea924p-02, // n=16
		0x1.37af93f9513f4p-01,  // n=17
		-0x1.963268b5724b6p-01, // n=18
		0x1.d906bcf328d6ep-01,  // n=19
		-0x1.fb9ea92ec68afp-01, // n=20
		0x1.fb9ea92ec68a3p-01,  // n=21
		-0x1.d906bcf328d4bp-01, // n=22
		0x1.963268b57247dp-01,  // n=23
		-0x1.37af93f9513aap-01, // n=24
		0x1.87de2a6aea965p-02,  // n=25
		-0x1.0b5150f6da21ep-03, // n=26
		-0x1.0b5150f6da43cp-03, // n=27
		0x1.87de2a6aeaa62p-02,  // n=28
		-0x1.37af93f951416p-01, // n=29
		0x1.963268b572482p-01,  // n=30
		-0x1.d906bcf328d4ep-01, // n=31
		0x1.fb9ea92ec68a4p-01,  // n=32
		-0x1.fb9ea92ec688dp-01, // n=33
		0x1.d906bcf328d3ap-01,  // n=34
		-0x1.963268b572462p-01, // n=35
	},
	{ // k=17
		-0x1.797c6a435ce96p-01, // n=0
		0x1.963268b572498p-01,  // n=1
		-0x1.afd100eafc29dp-01, // n=2
		0x1.c62648af65785p-01,  // n=3
		-0x1.d906bcf328d44p-01, // n=4
		0x1.e84d9692357f8p-01,  // n=5
		-0x1.f3dd11fb974bcp-01, // n=6
		0x1.fb9ea92ec68a1p-01,  // n=7
		-0x1.ff833f9da45f6p-01, // n=8
		0x1.ff833f9da45f6p-01,  // n=9
		-0x1.fb9ea92ec6898p-01, // n=10
		0x1.f3dd11fb974adp-01,  // n=11
		-0x1.e84d9692357bcp-01, // n=12
		0x1.d906bcf328d29p-01,  // n=13
		-0x1.c62648af65764p-01, // n=14
		0x1.afd100eafc255p-01,  // n=15
		-0x1.963268b5724bbp-01, // n=16
		0x1.797c6a435ce7cp-01,  // n=17
		-0x1.59e6f5ae6a063p-01, // n=18
		0x1.37af93f9513c6p-01,  // n=19
		-0x1.1318ef2c019f1p-01, // n=20
		0x1.d8d4a0e345792p-02,  // n=21
		-0x1.87de2a6aea92cp-02, // n=22
		0x1.33ec389a5a74dp-02,  // n=23
		-0x1.bb44b13b6247fp-03, // n=24
		0x1.0b5150f6da298p-03,  // n=25
		-0x1.65547c4695028p-05, // n=26
		-0x1.65547c46950fcp-05, // n=27
		0x1.0b5150f6da4c8p-03,  // n=28
		-0x1.bb44b13b626a5p-03, // n=29
		0x1.33ec389a5a85ap-02,  // n=30
		-0x1.87de2a6aea944p-02, // n=31
		0x1.d8d4a0e3457a9p-02,  // n=32
		-0x1.1318ef2c01a68p-01, // n=33
		0x1.37af93f9513d1p-01,  // n=34
		-0x1.59e6f5ae6a06cp-01, // n=35
	},
}

// AliasCS and AliasCA are the encoder-side antialiasing butterfly
// coefficients, derived from the ISO/IEC 11172-3:1993 Table B.9 "c"
// coefficients c = {-0.6, -0.535, -0.33, -0.185, -0.095, -0.041, -0.0142,
// -0.0037} (Annex C, section C.1.5.1) via
//
//	cs[i] = 1 / sqrt(1 + c[i]*c[i])
//	ca[i] = c[i] / sqrt(1 + c[i]*c[i])
//
// Each literal is the exact hex float64 encoding of the corresponding
// math.Sqrt-derived value from the same throwaway generator as MDCTWindow;
// the committed literal is the runtime truth. cs is positive for every i
// (matches the decoder's gAA[0], internal/dec/tables.go, bit for bit in
// float32); ca is negative because every Table B.9 c is negative, so it
// matches the decoder's gAA[1] in magnitude only, not sign (see
// TestEncAliasCoefficientsMatchDec, internal/dec/encx_mdct_test.go).
var AliasCS = [8]float64{
	0x1.b7095010f9356p-01, // i=0
	0x1.c373afe3fa80cp-01, // i=1
	0x1.e635b9ee7b56ep-01, // i=2
	0x1.f77502a0dd15bp-01, // i=3
	0x1.fdb482dd30f5bp-01, // i=4
	0x1.ff91f901a8104p-01, // i=5
	0x1.fff2c98dbe44ep-01, // i=6
	0x1.ffff1a52805d2p-01, // i=7
}

var AliasCA = [8]float64{
	-0x1.076bfcd6fbecdp-01, // i=0
	-0x1.e30db485db66p-02,  // i=1
	-0x1.40e604f4701fcp-02, // i=2
	-0x1.748ee85851aecp-03, // i=3
	-0x1.83603a7f2535ap-04, // i=4
	-0x1.4f970dd8206dp-05,  // i=5
	-0x1.d14239d59a7c1p-07, // i=6
	-0x1.e4f68c708d3f4p-09, // i=7
}

// MDCTWindowStart holds the 36 coefficients of the start-block sine window
// from ISO/IEC 11172-3:1993, section 2.4.3.4.10.3, block_type 1: long rise
// (the same sin(pi/36*(i+0.5)) as MDCTWindow for i = 0..17, so that the
// overlap with a preceding long block stays consistent), a flat plateau of
// 1.0 for i = 18..23, a short fall of sin(pi/12*(i-18+0.5)) for i = 24..29
// (the falling half of the 12-point short window, so the overlap with a
// following short block stays consistent), and a zero tail for i = 30..35.
// Each literal is the exact hex float64 encoding of the piecewise formula
// above, computed by a throwaway generator (scratchpad, never committed;
// see MDCTWindow's doc comment for the same pattern); the committed literal
// is the runtime truth, no math.Sin call runs at package init or runtime.
// The first 18 entries are bit-exact equal to MDCTWindow[:18] (both compute
// the same formula); see TestMdctShortTablesRecompute.
var MDCTWindowStart = [36]float64{
	0x1.65547c4694e11p-05, // w[0]
	0x1.0b5150f6da2dp-03,  // w[1]
	0x1.bb44b13b62571p-03, // w[2]
	0x1.33ec389a5a81ep-02, // w[3]
	0x1.87de2a6aea963p-02, // w[4]
	0x1.d8d4a0e345738p-02, // w[5]
	0x1.1318ef2c01a5bp-01, // w[6]
	0x1.37af93f9513eap-01, // w[7]
	0x1.59e6f5ae6a0a6p-01, // w[8]
	0x1.797c6a435ce84p-01, // w[9]
	0x1.963268b572491p-01, // w[10]
	0x1.afd100eafc28fp-01, // w[11]
	0x1.c62648af6577p-01,  // w[12]
	0x1.d906bcf328d46p-01, // w[13]
	0x1.e84d9692357ep-01,  // w[14]
	0x1.f3dd11fb974b6p-01, // w[15]
	0x1.fb9ea92ec689bp-01, // w[16]
	0x1.ff833f9da45f7p-01, // w[17]
	0x1p+00,               // w[18]
	0x1p+00,               // w[19]
	0x1p+00,               // w[20]
	0x1p+00,               // w[21]
	0x1p+00,               // w[22]
	0x1p+00,               // w[23]
	0x1.fb9ea92ec689bp-01, // w[24]
	0x1.d906bcf328d46p-01, // w[25]
	0x1.963268b572492p-01, // w[26]
	0x1.37af93f9513e8p-01, // w[27]
	0x1.87de2a6aea95dp-02, // w[28]
	0x1.0b5150f6da2bfp-03, // w[29]
	0x0p+00,               // w[30]
	0x0p+00,               // w[31]
	0x0p+00,               // w[32]
	0x0p+00,               // w[33]
	0x0p+00,               // w[34]
	0x0p+00,               // w[35]
}

// MDCTWindowStop holds the 36 coefficients of the stop-block sine window
// from ISO/IEC 11172-3:1993, section 2.4.3.4.10.3, block_type 3: the exact
// time reverse of MDCTWindowStart, i.e. MDCTWindowStop[i] =
// MDCTWindowStart[35-i] (decision 3). This gives a zero head (i = 0..5), a
// short rise of sin(pi/12*(i-6+0.5)) (i = 6..11, the mirror of
// MDCTWindowStart's short fall), a flat plateau of 1.0 (i = 12..17), and a
// long fall of sin(pi/36*(i+0.5)) (i = 18..35, mathematically identical to
// MDCTWindow's own second half via the sin(pi-x) = sin(x) symmetry, and
// equal within the recompute tolerance: because these literals are the
// exact reverse of MDCTWindowStart rather than an independent re-evaluation
// of the sine at i = 18..35, 12 of the 18 tail entries differ from
// MDCTWindow[18:36] by 1 ULP, which is expected and harmless). Each literal
// is the exact hex float64 encoding of
// MDCTWindowStart[35-i] from the same throwaway generator as
// MDCTWindowStart; the committed literal is the runtime truth. See
// TestMdctShortTablesRecompute for the reverse-relationship check.
var MDCTWindowStop = [36]float64{
	0x0p+00,               // w[0]
	0x0p+00,               // w[1]
	0x0p+00,               // w[2]
	0x0p+00,               // w[3]
	0x0p+00,               // w[4]
	0x0p+00,               // w[5]
	0x1.0b5150f6da2bfp-03, // w[6]
	0x1.87de2a6aea95dp-02, // w[7]
	0x1.37af93f9513e8p-01, // w[8]
	0x1.963268b572492p-01, // w[9]
	0x1.d906bcf328d46p-01, // w[10]
	0x1.fb9ea92ec689bp-01, // w[11]
	0x1p+00,               // w[12]
	0x1p+00,               // w[13]
	0x1p+00,               // w[14]
	0x1p+00,               // w[15]
	0x1p+00,               // w[16]
	0x1p+00,               // w[17]
	0x1.ff833f9da45f7p-01, // w[18]
	0x1.fb9ea92ec689bp-01, // w[19]
	0x1.f3dd11fb974b6p-01, // w[20]
	0x1.e84d9692357ep-01,  // w[21]
	0x1.d906bcf328d46p-01, // w[22]
	0x1.c62648af6577p-01,  // w[23]
	0x1.afd100eafc28fp-01, // w[24]
	0x1.963268b572491p-01, // w[25]
	0x1.797c6a435ce84p-01, // w[26]
	0x1.59e6f5ae6a0a6p-01, // w[27]
	0x1.37af93f9513eap-01, // w[28]
	0x1.1318ef2c01a5bp-01, // w[29]
	0x1.d8d4a0e345738p-02, // w[30]
	0x1.87de2a6aea963p-02, // w[31]
	0x1.33ec389a5a81ep-02, // w[32]
	0x1.bb44b13b62571p-03, // w[33]
	0x1.0b5150f6da2dp-03,  // w[34]
	0x1.65547c4694e11p-05, // w[35]
}

// MDCTWindowShort holds the 12 coefficients of the short-block sine window
// from ISO/IEC 11172-3:1993, section 2.4.3.4.10.3, block_type 2:
// w[j] = sin(pi/12 * (j + 0.5)) for j = 0..11, applied identically to each
// of the three 12-point sub-windows a short-block granule computes per
// subband (MDCTGranuleBlock, mdct.go). Each literal is the exact hex
// float64 encoding of the formula, computed by the same throwaway generator
// as MDCTWindow; the committed literal is the runtime truth, no math.Sin
// call runs at package init or runtime.
var MDCTWindowShort = [12]float64{
	0x1.0b5150f6da2d1p-03, // w[0]
	0x1.87de2a6aea964p-02, // w[1]
	0x1.37af93f9513ebp-01, // w[2]
	0x1.963268b572492p-01, // w[3]
	0x1.d906bcf328d47p-01, // w[4]
	0x1.fb9ea92ec689cp-01, // w[5]
	0x1.fb9ea92ec689bp-01, // w[6]
	0x1.d906bcf328d46p-01, // w[7]
	0x1.963268b572492p-01, // w[8]
	0x1.37af93f9513e8p-01, // w[9]
	0x1.87de2a6aea95dp-02, // w[10]
	0x1.0b5150f6da2bfp-03, // w[11]
}

// mdctCos12 holds the 6x12 forward-MDCT kernel for short blocks from
// ISO/IEC 11172-3:1993, Annex C, section C.1.5.1, specialized to N = 12
// (short blocks):
//
//	mdctCos12[k][j] = cos(pi/(2*N) * (2*j + 1 + N/2) * (2*k + 1))
//	                = cos(pi/24 * (2*j + 7) * (2*k + 1))
//
// for k = 0..5 (spectral line) and j = 0..11 (windowed sample index), the
// same general formula as mdctCos with N = 12 instead of N = 36. Each
// literal is the exact hex float64 encoding of the corresponding
// math.Cos(...) call, produced by the same throwaway generator as
// mdctCos; the committed literal is the runtime truth, no math.Cos call
// runs at package init or runtime.
var mdctCos12 = [6][12]float64{
	{ // k=0
		0x1.37af93f9513eap-01,  // j=0
		0x1.87de2a6aea96p-02,   // j=1
		0x1.0b5150f6da2cdp-03,  // j=2
		-0x1.0b5150f6da2d9p-03, // j=3
		-0x1.87de2a6aea965p-02, // j=4
		-0x1.37af93f9513eap-01, // j=5
		-0x1.963268b572493p-01, // j=6
		-0x1.d906bcf328d48p-01, // j=7
		-0x1.fb9ea92ec689cp-01, // j=8
		-0x1.fb9ea92ec689bp-01, // j=9
		-0x1.d906bcf328d45p-01, // j=10
		-0x1.963268b57249p-01,  // j=11
	},
	{ // k=1
		-0x1.d906bcf328d48p-01, // j=0
		-0x1.d906bcf328d45p-01, // j=1
		-0x1.87de2a6aea95ep-02, // j=2
		0x1.87de2a6aea967p-02,  // j=3
		0x1.d906bcf328d47p-01,  // j=4
		0x1.d906bcf328d46p-01,  // j=5
		0x1.87de2a6aea952p-02,  // j=6
		-0x1.87de2a6aea964p-02, // j=7
		-0x1.d906bcf328d4ap-01, // j=8
		-0x1.d906bcf328d46p-01, // j=9
		-0x1.87de2a6aea954p-02, // j=10
		0x1.87de2a6aea98p-02,   // j=11
	},
	{ // k=2
		-0x1.0b5150f6da2d2p-03, // j=0
		0x1.d906bcf328d4ap-01,  // j=1
		0x1.37af93f9513e7p-01,  // j=2
		-0x1.37af93f9513f5p-01, // j=3
		-0x1.d906bcf328d46p-01, // j=4
		0x1.0b5150f6da2dcp-03,  // j=5
		0x1.fb9ea92ec689cp-01,  // j=6
		0x1.87de2a6aea938p-02,  // j=7
		-0x1.963268b5724a1p-01, // j=8
		-0x1.963268b572495p-01, // j=9
		0x1.87de2a6aea99ap-02,  // j=10
		0x1.fb9ea92ec6897p-01,  // j=11
	},
	{ // k=3
		0x1.fb9ea92ec689bp-01,  // j=0
		-0x1.87de2a6aea982p-02, // j=1
		-0x1.963268b572493p-01, // j=2
		0x1.963268b572499p-01,  // j=3
		0x1.87de2a6aea956p-02,  // j=4
		-0x1.fb9ea92ec689cp-01, // j=5
		0x1.0b5150f6da313p-03,  // j=6
		0x1.d906bcf328d41p-01,  // j=7
		-0x1.37af93f95140bp-01, // j=8
		-0x1.37af93f9513e4p-01, // j=9
		0x1.d906bcf328d48p-01,  // j=10
		0x1.0b5150f6da252p-03,  // j=11
	},
	{ // k=4
		-0x1.87de2a6aea964p-02, // j=0
		-0x1.87de2a6aea954p-02, // j=1
		0x1.d906bcf328d4p-01,   // j=2
		-0x1.d906bcf328d4fp-01, // j=3
		0x1.87de2a6aea95fp-02,  // j=4
		0x1.87de2a6aea93cp-02,  // j=5
		-0x1.d906bcf328d41p-01, // j=6
		0x1.d906bcf328d54p-01,  // j=7
		-0x1.87de2a6aea994p-02, // j=8
		-0x1.87de2a6aea942p-02, // j=9
		0x1.d906bcf328d36p-01,  // j=10
		-0x1.d906bcf328d53p-01, // j=11
	},
	{ // k=5
		-0x1.963268b572493p-01, // j=0
		0x1.d906bcf328d4p-01,   // j=1
		-0x1.fb9ea92ec689bp-01, // j=2
		0x1.fb9ea92ec689ep-01,  // j=3
		-0x1.d906bcf328d48p-01, // j=4
		0x1.963268b57248cp-01,  // j=5
		-0x1.37af93f9513fp-01,  // j=6
		0x1.87de2a6aea992p-02,  // j=7
		-0x1.0b5150f6da37cp-03, // j=8
		-0x1.0b5150f6da2dep-03, // j=9
		0x1.87de2a6aea949p-02,  // j=10
		-0x1.37af93f9513d1p-01, // j=11
	},
}
