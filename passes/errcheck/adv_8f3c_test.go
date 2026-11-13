package errcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

// ft-8f3c adversary, defended shapes: loops that exit through os.Exit or log.Fatal on every path
// out, defers in branches and loops, several defers, goto and labelled-break back edges, an exit
// in a called closure, a goroutine or a deferred closure, init, generics, and the shapes the
// classifier cannot see. Each `_ret` partner is the same function with a way out and keeps ERR005.
func TestAdv8f3cExitDeferDefended(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errexitdeferadv")
}

// ft-8f3c adversary, the invariant that the fix may only silence ERR005 where every path after the
// defer ends in a process exit. A function that loops forever or waits forever has no such path
// and no path that returns; the unmodified tip reports it and the fix does not.
func TestAdv8f3cNoExitPathKeepsFinding(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errexitdeferadvbreaks")
}
