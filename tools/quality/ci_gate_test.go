package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

// baselineGateRan is set by TestQualityBaseline once it passes its skip and
// mutual-exclusion checks, so TestMain can tell a filtered-out gate from a
// run one.
var baselineGateRan atomic.Bool

// ghaTrue is the value GitHub Actions sets GITHUB_ACTIONS to.
const ghaTrue = "true"

// gateSkippedInCI reports an error when the CI gate step (GITHUB_ACTIONS=true
// with MP3_QUALITY_BASELINE set) finished without TestQualityBaseline having
// run. It closes the hole TestQualityGateActivatedInCI cannot: a -run or -skip
// filter on the step excludes that guard test along with the gate.
func gateSkippedInCI(getenv func(string) string, ran bool) error {
	if getenv("GITHUB_ACTIONS") == ghaTrue && getenv("MP3_QUALITY_BASELINE") != "" && !ran {
		return errors.New("the quality regression gate is enabled for this step (MP3_QUALITY_BASELINE is set) " +
			"but TestQualityBaseline did not run: a -run or -skip filter excluded it; remove the filter or move the env")
	}
	return nil
}

// TestMain runs for the package test binary whatever -run selects, so it can
// fail the CI gate step when the gate was filtered out. Residual gap: a step
// that drops ./tools/quality from its package list never starts this binary.
func TestMain(m *testing.M) {
	code := m.Run()
	if err := gateSkippedInCI(os.Getenv, baselineGateRan.Load()); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		code = 1
	}
	os.Exit(code)
}

// TestMainFailsFilteredGate re-runs this test binary with only TestDedupInts
// selected, as a -run filter on the CI gate step would, and checks that
// TestMain turns the passing run into a failure under the gate env, but not
// without GITHUB_ACTIONS.
func TestMainFailsFilteredGate(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := func(env ...string) (string, error) {
		cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestDedupInts$")
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := child("GITHUB_ACTIONS=true", "MP3_QUALITY_BASELINE=1")
	if err == nil || !strings.Contains(out, "TestQualityBaseline did not run") {
		t.Fatalf("filtered gate under CI must fail with the gate message, got err=%v out:\n%s", err, out)
	}
	if out, err := child("GITHUB_ACTIONS=", "MP3_QUALITY_BASELINE=1"); err != nil {
		t.Fatalf("outside GitHub Actions the filtered run must pass: %v\n%s", err, out)
	}
}

func TestGateSkippedInCI(t *testing.T) {
	cases := []struct {
		name         string
		actions, env string
		ran          bool
		wantErr      bool
	}{
		{"CI, gate env, not run", ghaTrue, "1", false, true},
		{"CI, gate env, ran", ghaTrue, "1", true, false},
		{"local, gate env, not run", "", "1", false, false},
		{"actions false, gate env, not run", "false", "1", false, false},
		{"CI, no gate env, not run", ghaTrue, "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := map[string]string{"GITHUB_ACTIONS": c.actions, "MP3_QUALITY_BASELINE": c.env}
			err := gateSkippedInCI(func(k string) string { return env[k] }, c.ran)
			if (err != nil) != c.wantErr {
				t.Fatalf("got %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

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
	//
	// A -run or -skip filter on the gate step would exclude this test too; the
	// package TestMain (gateSkippedInCI) covers that. Residual gap: a step that
	// no longer includes ./tools/quality at all never starts the test binary.
	if os.Getenv("GITHUB_ACTIONS") == ghaTrue && os.Getenv("MP3_QUALITY_BASELINE") == "" {
		t.Fatal("running under GitHub Actions but MP3_QUALITY_BASELINE is unset: the quality " +
			"regression gate (TestQualityBaseline) is skipping in the very job that runs the " +
			"full suite. It must be set on whichever job runs `go test ./...`.")
	}
}
