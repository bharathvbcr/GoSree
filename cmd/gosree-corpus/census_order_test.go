package main

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func loadCensusModule(t *testing.T, dir string) []*packages.Package {
	t.Helper()
	pkgs, err := packages.Load(&packages.Config{Mode: packages.LoadAllSyntax, Dir: dir}, "./...")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("fixture did not type-check")
	}
	return pkgs
}

func hatchRows(file, scope string, names []string) []annotation {
	rows := make([]annotation, 0, len(names))
	for _, n := range names {
		rows = append(rows, annotation{file: file, scope: scope, verdict: "IMM_BLOCKED:" + n})
	}
	return rows
}

// censusSignature is everything about a report that must not depend on row order, keyed by hatch.
func censusSignature(rep CensusReport) (map[string]string, map[string]int, int) {
	hatches := map[string]string{}
	for _, h := range rep.Hatches {
		hatches[h.Name] = fmt.Sprintf("%s %s %v", h.Root, h.Worst, h.Closure)
	}
	return hatches, rep.ByWorst, rep.WithWrites
}

func permutations(in []string) [][]string {
	if len(in) <= 1 {
		return [][]string{slices.Clone(in)}
	}
	var out [][]string
	for i := range in {
		rest := slices.Concat(in[:i:i], in[i+1:])
		for _, p := range permutations(rest) {
			out = append(out, append([]string{in[i]}, p...))
		}
	}
	return out
}

// TestCensusCycleOrderIndependent runs every row order of a table whose types reference each
// other. A type reached again while it is being resolved used to cache an incomplete class, so the
// worst class of Server (no writes of its own) depended on whether Config's row came first.
func TestCensusCycleOrderIndependent(t *testing.T) {
	dir, err := filepath.Abs("testdata/censuscycle")
	if err != nil {
		t.Fatal(err)
	}
	pkgs := loadCensusModule(t, dir)
	const file, scope = "internal/svc/svc.go", "func Serve("
	facts := summaries(analyze(pkgs))
	wantWorst := map[string]string{"a": classW, "b": classW, "x": classW, "y": classW, "z": classW}
	for _, order := range permutations([]string{"a", "b", "x", "y", "z"}) {
		rep, err := censusOf(pkgs, facts, hatchRows(file, scope, order), dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range rep.Hatches {
			if h.Worst != wantWorst[h.Name] {
				t.Fatalf("row order %v: hatch %s (%s) worst %s, want %s; closure %v", order, h.Name, h.Root, h.Worst, wantWorst[h.Name], h.Closure)
			}
		}
		if rep.ByWorst[classW] != 5 || rep.WithWrites != 5 {
			t.Fatalf("row order %v: by_worst %v with_writes %d, want W:5 and 5", order, rep.ByWorst, rep.WithWrites)
		}
		for _, ct := range rep.Types {
			if !slices.Equal(ct.Closure, []string{classW}) {
				t.Fatalf("row order %v: type %s closure %v, want [W]", order, ct.Type, ct.Closure)
			}
		}
	}
}

// genCensusModule writes a module of n struct types with random references (cycles included) and
// a post-construction write on some of them, and returns the oracle: for each type, whether any
// type it reaches (itself included) is written.
func genCensusModule(t *testing.T, rng *rand.Rand, n int) (dir string, writes []bool, reaches []bool) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/gen\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edges := make([][]int, n)
	writes = make([]bool, n)
	var b strings.Builder
	b.WriteString("package gen\n\n")
	for i := range n {
		fmt.Fprintf(&b, "type T%d struct {\n\tn int\n", i)
		for j := range n {
			if rng.IntN(n) < 2 {
				edges[i] = append(edges[i], j)
				fmt.Fprintf(&b, "\tf%d *T%d\n", j, j)
			}
		}
		b.WriteString("}\n\n")
		if rng.IntN(4) == 0 {
			writes[i] = true
			fmt.Fprintf(&b, "func (t *T%d) Bump() { t.n++ }\n\n", i)
		}
	}
	params := make([]string, n)
	for i := range n {
		params[i] = fmt.Sprintf("p%d *T%d", i, i)
	}
	fmt.Fprintf(&b, "func Serve(%s) {}\n", strings.Join(params, ", "))
	if err := os.WriteFile(filepath.Join(dir, "gen.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	reaches = make([]bool, n)
	for i := range n {
		seen := make([]bool, n)
		stack := []int{i}
		for len(stack) > 0 {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[k] {
				continue
			}
			seen[k] = true
			reaches[i] = reaches[i] || writes[k]
			stack = append(stack, edges[k]...)
		}
	}
	return dir, writes, reaches
}

// TestCensusRandomOrderIndependent compares random row orders of a generated, cyclic type graph
// against the first order and against a reachability oracle.
func TestCensusRandomOrderIndependent(t *testing.T) {
	const n = 12
	rng := rand.New(rand.NewPCG(1, 2))
	for graph := range 3 {
		dir, _, reaches := genCensusModule(t, rng, n)
		pkgs := loadCensusModule(t, dir)
		facts := summaries(analyze(pkgs))
		names := make([]string, n)
		for i := range names {
			names[i] = fmt.Sprintf("p%d", i)
		}
		var base map[string]string
		var baseWorst map[string]int
		var baseWrites int
		for run := range 6 {
			order := slices.Clone(names)
			if run > 0 {
				rng.Shuffle(n, func(i, j int) { order[i], order[j] = order[j], order[i] })
			}
			rep, err := censusOf(pkgs, facts, hatchRows("gen.go", "func Serve(", order), dir)
			if err != nil {
				t.Fatal(err)
			}
			sig, byWorst, writes := censusSignature(rep)
			for i := range n {
				want := classOK
				if reaches[i] {
					want = classW
				}
				var got string
				for _, h := range rep.Hatches {
					if h.Name == fmt.Sprintf("p%d", i) {
						got = h.Worst
					}
				}
				if got != want {
					t.Fatalf("graph %d order %v: p%d worst %s, oracle %s", graph, order, i, got, want)
				}
			}
			if run == 0 {
				base, baseWorst, baseWrites = sig, byWorst, writes
				continue
			}
			if !maps.Equal(sig, base) || !maps.Equal(byWorst, baseWorst) || writes != baseWrites {
				t.Fatalf("graph %d order %v: report differs from the first order\n got %v %v %d\nwant %v %v %d", graph, order, sig, byWorst, writes, base, baseWorst, baseWrites)
			}
		}
	}
}
