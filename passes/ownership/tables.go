package ownership

import (
	"fmt"
	"go/types"
	"sort"

	"github.com/bharathvbcr/gocap/internal/kinds"
)

// The call tables name functions of other packages by types.Func.FullName: spawners (spawn.go), the
// mutex vocabulary (lockOps, lockset.go) and the function overrides (internal/kinds/funcs.go). They
// answer different questions and stay separate, but they are checked in one place, with one
// resolver: when gocap analyses the package a table entry belongs to, the entry must resolve to a
// function of it, or the analysis fails. A renamed function is an error, never an entry that
// silently stops matching.

// callTableNames returns every FullName the spawner and lock tables refer to, sorted.
func callTableNames() []string {
	var out []string
	for name, sp := range spawners {
		out = append(out, name)
		if sp.wait != "" {
			out = append(out, sp.wait)
		}
	}
	for name := range lockOps {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// checkCallTables checks the entries of every call table that belong to pkg.
func checkCallTables(pkg *types.Package) error {
	for _, name := range callTableNames() {
		if kinds.PackageOf(name) != pkg.Path() {
			continue
		}
		if _, err := kinds.Resolve(pkg, name); err != nil {
			return fmt.Errorf("call table: %w", err)
		}
	}
	return kinds.CheckFuncOverrides(pkg, kinds.FuncOverrides())
}

// atomicPkg's functions are synchronised accesses: an atomic add or store through a pointer is not
// a write that races with another atomic access. Like lockOps for SAFE009, this defines vocabulary
// (what a synchronised write is) rather than vouching for a callee, so it is not counted toward the
// budget. The functions have no Go body, so without it their summaries would say they write every
// argument, and `atomic.AddInt64(&h.hits, 1)` in a handler would be SAFE008.
const atomicPkg = "sync/atomic"

// overrideBudget is the abandon criterion's cap on assumptions about code gocap does not check.
const overrideBudget = 100

// assumptions counts the entries that assert something about a type or function instead of
// checking it: type overrides, function overrides and spawners. lockOps are not counted: they define
// what SAFE009 understands as locking, rather than vouch that a callee is safe.
func assumptions() int {
	return len(kinds.Overrides()) + len(kinds.FuncOverrides()) + len(spawners)
}
