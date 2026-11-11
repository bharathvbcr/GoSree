package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runAcceptanceScript(t *testing.T, filter string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs the e2e tiers")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "tests", "run_acceptance_tests.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), "GOSREE_ACCEPTANCE_FILTER="+filter)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

// TestAcceptanceScriptFailsAnEmptyTier: `--tier all` used to report PASSED when one tier ran no
// test, because only the total was checked.
func TestAcceptanceScriptFailsAnEmptyTier(t *testing.T) {
	stdout, _, code := runAcceptanceScript(t, "^TestTier[1-3]_", "--tier", "all")
	if code == 0 || strings.Contains(stdout, "\nPASSED") || !strings.Contains(stdout, "ran no tests") {
		t.Errorf("exit %d; want a failure naming the empty tier\n%s", code, stdout)
	}
	if _, _, code := runAcceptanceScript(t, "^NoSuchTest$", "--tier", "all"); code == 0 {
		t.Error("a filter that matches nothing passed")
	}
}

// TestAcceptanceScriptJSONIsPure: with --json -v, stdout is exactly the JSON document; the go test
// stream goes to stderr.
func TestAcceptanceScriptJSONIsPure(t *testing.T) {
	stdout, stderr, code := runAcceptanceScript(t, "^TestTier2_", "--tier", "2", "--json", "-v")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	var doc struct{ Status string }
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc.Status != "PASSED" {
		t.Errorf("stdout is not the JSON summary (%v): %q", err, stdout)
	}
	if !strings.Contains(stderr, "--- PASS: TestTier2_") {
		t.Errorf("the go test stream did not reach stderr: %q", stderr)
	}
}
