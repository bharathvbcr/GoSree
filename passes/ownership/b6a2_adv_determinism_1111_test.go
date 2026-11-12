package ownership_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// Several values escape in one statement and several borrow variables could match one allocation.
// The ownership analyzer is run 20 times; every rendering must be byte-identical.
func TestAdvOwnershipDeterministic(t *testing.T) {
	src := `package p

type B struct{ n int }

type Job struct{ n int }

var r1, r2, r3 []*Job

func sink(j *Job) {}

//gosree:mub b
//gosree:mub c
func borrows(b, c *B, ch chan *B) {
	ch <- b
	go func() { _ = b; _ = c }()
}

func multi(j *Job, ch chan *Job) {
	r1 = append(r1, j)
	r2 = append(r2, j)
	r3 = append(r3, j)
	ch <- j
	go sink(j)
	j.n = 1
}

func multiEscape(a, b *Job) {
	r1, r2 = append(r1, a, b), append(r2, a, b)
	go func() { sink(a); sink(b) }()
}

func loop(js []*Job) {
	for _, j := range js {
		r1 = append(r1, j)
		r2 = append(r2, j)
		go sink(j)
	}
}
`
	run := func() string {
		pkgs := loadTempModule(t, "1.27", map[string]string{"p/p.go": src})
		g, err := checker.Analyze([]*analysis.Analyzer{ownership.Analyzer}, pkgs, nil)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		var lines []string
		for _, act := range g.Roots {
			for _, d := range act.Diagnostics {
				lines = append(lines, fmt.Sprintf("%d: %s", act.Package.Fset.Position(d.Pos).Line, d.Message))
			}
		}
		sort.Strings(lines)
		return strings.Join(lines, "\n")
	}
	first := run()
	t.Logf("diagnostics:\n%s", first)
	if first == "" {
		t.Fatal("fixture produced no diagnostics; it tests nothing")
	}
	for i := 1; i < 20; i++ {
		if got := run(); got != first {
			t.Fatalf("run %d differs:\n%s\n--- vs first ---\n%s", i, got, first)
		}
	}
}
