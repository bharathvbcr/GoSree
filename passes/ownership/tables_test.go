package ownership

import (
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/bharathvbcr/gosree/internal/kinds"
)

// TestOverrideBudget turns the abandon criterion's cap on assumptions into a failing build.
func TestOverrideBudget(t *testing.T) {
	n := assumptions()
	t.Logf("assumptions: %d of %d (types %d, functions %d, spawners %d)",
		n, overrideBudget, len(kinds.Overrides()), len(kinds.FuncOverrides()), len(spawners))
	if n > overrideBudget {
		t.Fatalf("%d assumptions exceed the budget of %d", n, overrideBudget)
	}
}

// notInModule names packages call tables refer to that this module does not depend on: their entries
// are checked only when gosree analyses a program that imports them.
var notInModule = map[string]bool{"golang.org/x/sync/errgroup": true}

// TestCallTablesResolve loads every package a call table names on the current toolchain and checks
// its entries the way run does, so a function renamed upstream fails here.
func TestCallTablesResolve(t *testing.T) {
	paths := map[string]bool{}
	for _, name := range callTableNames() {
		paths[kinds.PackageOf(name)] = true
	}
	for _, o := range kinds.FuncOverrides() {
		paths[kinds.PackageOf(o.Name)] = true
	}
	var load []string
	for p := range paths {
		if !notInModule[p] {
			load = append(load, p)
		}
	}
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedTypes}, load...)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != len(load) {
		t.Fatalf("loaded %d packages, want %d", len(pkgs), len(load))
	}
	for _, p := range pkgs {
		if len(p.Errors) > 0 || p.Types == nil {
			t.Fatalf("%s: %v", p.PkgPath, p.Errors)
		}
		if err := checkCallTables(p.Types); err != nil {
			t.Error(err)
		}
	}
}
