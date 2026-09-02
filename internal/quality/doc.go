// Package quality holds the objective audio quality metrics, alignment,
// WAV I/O, and deterministic test programs behind the tools/quality
// harness, which compares this project's MP3 encoder against LAME (used
// strictly as a black-box binary; see PROVENANCE.md).
//
// Nothing here runs on any encode or decode path, so the package is free to
// use libm transcendentals and carries no cross-architecture bit-exactness
// requirement: it measures, it does not produce output that is golden-pinned.
// It is a normal (non-_test) package so both tools/quality and its tests can
// import it.
package quality
