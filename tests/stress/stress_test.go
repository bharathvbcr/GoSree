package stress_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestStress checks every fixture's markers against what the analyzers report, and logs the ledger.
func TestStress(t *testing.T) {
	fixtures := analyze(t, fixtureRoots(t))
	l := &ledger{byCode: map[string][3]int{}}
	for _, fx := range fixtures {
		for _, p := range fx.verify(l) {
			t.Error(p)
		}
	}
	crashes := crashRoots(t)
	for _, root := range crashes {
		crashed, out := probeCrash(t, root)
		if !crashed {
			t.Errorf("%s: carries a CRASH marker but every analyzer ran: remove the marker\n%s", root, out)
			continue
		}
		l.crashes++
	}
	t.Logf("%d fixture packages, %d crash fixtures probed in child processes\n%s", len(fixtures), len(crashes), l)
}

// crashProbeEnv names the fixture root a child test process should analyze.
const crashProbeEnv = "GOCAP_STRESS_CRASH_PROBE"

// TestCrashProbe is the child half of the crash check: TestStress re-runs this test binary with
// crashProbeEnv set, once per CRASH fixture, because an analyzer that panics takes the process down.
func TestCrashProbe(t *testing.T) {
	root := os.Getenv(crashProbeEnv)
	switch root {
	case "":
		t.Skip("runs only as a child process of TestStress")
	case probeSelfTestPanic:
		panic("self-test: dying as a broken analyzer would")
	case probeSelfTestClean:
		return
	}
	for _, fx := range analyze(t, []string{root}) {
		for _, f := range fx.failures {
			t.Errorf("ANALYZER FAILED: %s: %s", fx.path, f)
		}
	}
}

// Roots no fixture can have, which make the child die or pass on demand.
const (
	probeSelfTestPanic = "//selftest-panic"
	probeSelfTestClean = "//selftest-clean"
)

// TestCrashProbeTellsCrashFromClean checks the probe both ways: no fixture crashes an analyzer
// today, so without this nothing would show that a CRASH marker is ever checked.
func TestCrashProbeTellsCrashFromClean(t *testing.T) {
	if crashed, out := probeCrash(t, probeSelfTestPanic); !crashed {
		t.Errorf("a child that panicked was not seen as a crash\n%s", out)
	}
	if crashed, out := probeCrash(t, probeSelfTestClean); crashed {
		t.Errorf("a child that ran cleanly was seen as a crash\n%s", out)
	}
}

// probeCrash analyzes one fixture root in a child process and reports whether an analyzer failed
// there: a panic, a fatal signal, or a returned error. Any other way for the child to fail (a
// fixture that does not compile, a timeout) fails the test: that is not the crash the marker claims.
func probeCrash(t *testing.T, root string) (crashed bool, output string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestCrashProbe$", "-test.count=1")
	cmd.Env = append(os.Environ(), crashProbeEnv+"="+root)
	out, runErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s: crash probe timed out\n%s", root, out)
	}
	if runErr == nil {
		return false, string(out)
	}
	for _, sign := range []string{"panic: ", "fatal error: ", "ANALYZER FAILED: ", "[signal "} {
		if bytes.Contains(out, []byte(sign)) {
			return true, string(out)
		}
	}
	t.Fatalf("%s: crash probe failed without an analyzer failure (%v)\n%s", root, runErr, out)
	return false, ""
}

// TestDump prints every diagnostic for the fixture roots named in GOCAP_STRESS_DUMP (comma
// separated), without checking markers. It is the exploration tool: write a fixture, dump it, then
// mark each line.
//
//	GOCAP_STRESS_DUMP=moves_alias go test ./tests/stress -run TestDump -v
func TestDump(t *testing.T) {
	names := os.Getenv("GOCAP_STRESS_DUMP")
	if names == "" {
		t.Skip("set GOCAP_STRESS_DUMP to a comma-separated list of fixture roots")
	}
	for _, fx := range analyze(t, strings.Split(names, ",")) {
		var b strings.Builder
		fmt.Fprintf(&b, "== %s: %d diagnostics\n", fx.path, len(fx.findings))
		for _, f := range fx.failures {
			fmt.Fprintf(&b, "ANALYZER FAILED: %s\n", f)
		}
		for _, f := range fx.findings {
			fmt.Fprintf(&b, "%s:%d:%d: [%s] %s\n", shortPath(f.file), f.line, f.col, f.analyzer, f.message)
		}
		t.Log(b.String())
	}
}

// TestDeterministic runs the analyzers twice over every fixture and requires identical output: a
// checker whose findings depend on map order cannot gate a build.
func TestDeterministic(t *testing.T) {
	if testing.Short() {
		t.Skip("second full analysis run skipped in -short mode")
	}
	render := func() string {
		var b strings.Builder
		for _, fx := range analyze(t, fixtureRoots(t)) {
			for _, f := range fx.failures {
				fmt.Fprintf(&b, "%s: FAILED %s\n", fx.path, f)
			}
			for _, f := range fx.findings {
				fmt.Fprintf(&b, "%s:%d:%d: %s\n", shortPath(f.file), f.line, f.col, f.message)
			}
		}
		return b.String()
	}
	first, second := render(), render()
	if first != second {
		t.Errorf("two runs over the same fixtures disagree\n--- first\n%s\n--- second\n%s", first, second)
	}
}

// TestWitnesses runs each WITNESS fixture and requires the runtime to convict it: a data race found
// by the race detector, or a panic or fatal error. Together with TestStress, which requires the same
// file to be clean but for its MISS markers, it proves the hole is real: the checker accepts a
// program the runtime rejects.
func TestWitnesses(t *testing.T) {
	if testing.Short() {
		t.Skip("runtime witnesses skipped in -short mode")
	}
	type witness struct{ file, kind string }
	var witnesses []witness
	files, err := filepath.Glob(filepath.Join("testdata", "src", "*", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !selected(filepath.Base(filepath.Dir(f))) {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(string(src), "\n") {
			if m := witnessRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				witnesses = append(witnesses, witness{f, m[1]})
				break
			}
		}
	}
	if len(witnesses) == 0 {
		t.Skip("no WITNESS fixtures selected")
	}
	// The witnesses build and run in the same GOPATH the analyzers load from, so one may import the
	// cap package or a sub-package of its own root.
	path := gopath(t)
	src, err := filepath.Abs(filepath.Join("testdata", "src"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range witnesses {
		root := filepath.Base(filepath.Dir(w.file))
		t.Run(root+"/"+filepath.Base(w.file), func(t *testing.T) {
			t.Parallel()
			convicted, out, err := runWitness(path, src, root, w.kind)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if !convicted {
				t.Fatalf("the runtime did not convict this program of a %s: it witnesses nothing\n%s", w.kind, out)
			}
		})
	}
}

var buildFailureRE = regexp.MustCompile(`(?m)^(# \S+|package \S+ is not in std|\S+\.go:\d+:\d+: )`)

// runWitness runs package pkg of the GOPATH gopath, from srcDir, and reports whether the runtime
// convicted it of kind: a detected data race, or a panic or fatal error. A program that fails to
// build or times out is an error, not a conviction.
func runWitness(gopath, srcDir, pkg, kind string) (convicted bool, output string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	args := []string{"run"}
	want := []string{"panic: ", "fatal error: "}
	if kind == "race" {
		args = append(args, "-race")
		want = []string{"WARNING: DATA RACE"}
	}
	cmd := exec.CommandContext(ctx, "go", append(args, pkg)...)
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(),
		"GOPATH="+gopath, "GO111MODULE=off", "GOPROXY=off", "GOWORK=off", "GOFLAGS=", "GORACE=halt_on_error=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return false, out.String(), fmt.Errorf("witness timed out")
	}
	for _, s := range want {
		if strings.Contains(out.String(), s) {
			return true, out.String(), nil
		}
	}
	// `go run` prints the build errors and exits 1 without running anything.
	if runErr != nil && buildFailureRE.MatchString(out.String()) {
		return false, out.String(), fmt.Errorf("witness does not build: %v", runErr)
	}
	return false, out.String(), nil
}

// TestWitnessRunnerAcquitsCleanProgram checks the runner the other way: a program with no race and
// no panic must not be convicted of either, or every WITNESS marker would pass; and one that does not
// build must be an error, not a silent acquittal.
func TestWitnessRunnerAcquitsCleanProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("runtime witnesses skipped in -short mode")
	}
	root := t.TempDir()
	write := func(pkg, src string) {
		dir := filepath.Join(root, "src", pkg)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("clean", `package main

import "sync"

func main() {
	var mu sync.Mutex
	var wg sync.WaitGroup
	n := 0
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); mu.Lock(); n++; mu.Unlock() }()
	}
	wg.Wait()
}
`)
	write("broken", "package main\n\nfunc main() { undefined() }\n")
	srcDir := filepath.Join(root, "src")
	for _, kind := range []string{"race", "panic"} {
		convicted, out, err := runWitness(root, srcDir, "clean", kind)
		if err != nil {
			t.Fatalf("%s: %v\n%s", kind, err, out)
		}
		if convicted {
			t.Errorf("a clean program was convicted of a %s\n%s", kind, out)
		}
		if _, out, err := runWitness(root, srcDir, "broken", kind); err == nil {
			t.Errorf("%s: a program that does not build was not reported as an error\n%s", kind, out)
		}
	}
}
