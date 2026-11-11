package errcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

func TestPackageLevelFuncLitErrcheck(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"p/p.go": `package p

func fail() error { return nil }

var pkgFn = func() {
	fail() // want "ERR001"
}

var pkgOuter = func() func() {
	return func() {
		fail() // want "ERR001"
	}
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, errcheck.Analyzer, "p")
}

func TestMakeInterfaceCompanionUseNoPhantomFuncHeaderDiagnostic(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"p/p.go": `package p

import "fmt"

func runCmd() ([]byte, error) { return nil, nil }

func badVariadicCompanionInErrBranch() error {
	out, err := runCmd()
	if err != nil {
		return fmt.Errorf("cmd failed: %v: %s", err, out) // want "ERR002"
	}
	return nil
}

func cleanBoxBeforeGuardUsedAfter() string {
	out, err := runCmd()
	var boxed any = out
	if err != nil {
		return ""
	}
	return fmt.Sprint(boxed)
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, errcheck.Analyzer, "p")
}
