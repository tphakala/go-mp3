package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// gateActivationRe matches the ci.yml env line that turns the quality
// regression gate on: MP3_QUALITY_BASELINE set to 1. It is anchored to a whole
// line (a YAML mapping entry, optional leading indent) and admits the usual
// quoting styles ('1', "1", 1), so a harmless requoting does not red it. The colon
// is what distinguishes the live mapping from the comment block above it, which
// spells the variable "MP3_QUALITY_BASELINE=1" (equals form, no colon).
var gateActivationRe = regexp.MustCompile(`(?m)^\s*MP3_QUALITY_BASELINE:\s*['"]?1['"]?\s*$`)

// TestQualityGateActivatedInCI guards the CI quality regression gate against
// silent deactivation. TestQualityBaseline only runs when MP3_QUALITY_BASELINE
// is set, which ci.yml does on the test job's full-suite step. If that env line
// is ever dropped, the gate would skip and CI would stay green with no
// protection, and nothing else in the suite would notice. This test reads
// ci.yml and fails when the activation line is gone, so the removal reddens
// immediately (here, in `task check`, and in CI) instead of vanishing unseen.
//
// It reads the workflow file rather than inspecting the runtime environment on
// purpose: it must fail in the exact commit that drops the line, not only in a
// later CI run, and it must not itself depend on any env being set.
func TestQualityGateActivatedInCI(t *testing.T) {
	// From tools/quality up to the repo root, where .github lives.
	path := filepath.Join("..", "..", ".github", "workflows", "ci.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !gateActivationRe.Match(b) {
		t.Fatalf("%s no longer sets MP3_QUALITY_BASELINE=1 on a job step: the quality "+
			"regression gate (TestQualityBaseline) would silently skip in CI, leaving no "+
			"protection. Restore the env on the test job, or update gateActivationRe here if "+
			"the activation intentionally moved.", path)
	}
	// The file check above catches the line being deleted. It cannot catch the
	// env being moved to a job that does not run the gate (a different job would
	// still match the regex). So when actually running under GitHub Actions
	// (which always sets GITHUB_ACTIONS=true), also assert the gate is live in
	// THIS job's environment, mirroring TestQualityBaseline's own activation
	// predicate (MP3_QUALITY_BASELINE non-empty). This test executes only under
	// the full-suite step; the compat job's -run filter does not select it.
	if os.Getenv("GITHUB_ACTIONS") == "true" && os.Getenv("MP3_QUALITY_BASELINE") == "" {
		t.Fatal("running under GitHub Actions but MP3_QUALITY_BASELINE is unset: the quality " +
			"regression gate (TestQualityBaseline) is skipping in the very job that runs the " +
			"full suite. It must be set on whichever job runs `go test ./...`.")
	}
}
