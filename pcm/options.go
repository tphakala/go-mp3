package pcm

// WithF32 configures a Decoder to emit interleaved little-endian float32
// bytes (4 bytes/sample), the decoder's own native sample type, instead of
// the default S16 (2 bytes/sample) conversion. Everything else (gapless
// trim, Xing/VBRI-tag exclusion, Read/WriteTo semantics) is unchanged; only
// the per-sample pack width and format differ.
func WithF32() Option {
	return func(c *config) {
		c.f32 = true
	}
}
