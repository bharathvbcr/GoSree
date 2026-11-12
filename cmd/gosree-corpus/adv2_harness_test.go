package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"
)

// Helpers for the second adversarial pass over the census reach fix (task ft-f138).

// adv2Module is a loaded fixture module: load it once, run the census over it many times.
type adv2Module struct {
	dir  string
	pkgs []*packages.Package
}

func adv2Load(t *testing.T, src string) *adv2Module {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module example.com/gen\n\ngo 1.27\n", "gen.go": src} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &adv2Module{dir: dir, pkgs: loadCensusModule(t, dir)}
}

func (m *adv2Module) run(t *testing.T, names []string) (CensusReport, time.Duration) {
	t.Helper()
	facts := summaries(analyze(m.pkgs))
	start := time.Now()
	rep, err := censusOf(m.pkgs, facts, hatchRows("gen.go", "func Serve(", names), m.dir)
	if err != nil {
		t.Fatal(err)
	}
	return rep, time.Since(start)
}

// adv2Classes maps hatch name to its sorted closure.
func adv2Classes(rep CensusReport) map[string][]string {
	out := map[string][]string{}
	for _, h := range rep.Hatches {
		c := slices.Clone(h.Closure)
		slices.Sort(c)
		out[h.Name] = c
	}
	return out
}

// adv2Dump appends a labelled JSON record to $ADV2_DUMP, for diffing a run against the pre-fix census.
func adv2Dump(t *testing.T, label string, rep CensusReport) {
	t.Helper()
	path := os.Getenv("ADV2_DUMP")
	if path == "" {
		return
	}
	type row struct {
		Hatch   string
		Root    string
		Worst   string
		Closure []string
	}
	var rows []row
	for _, h := range rep.Hatches {
		c := slices.Clone(h.Closure)
		slices.Sort(c)
		rows = append(rows, row{h.Name, h.Root, h.Worst, c})
	}
	slices.SortFunc(rows, func(a, b row) int {
		if a.Hatch < b.Hatch {
			return -1
		}
		if a.Hatch > b.Hatch {
			return 1
		}
		return 0
	})
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintf(f, "== %s\n", label)
	for _, r := range rows {
		b, _ := json.Marshal(r)
		fmt.Fprintf(f, "%s\n", b)
	}
	for _, ty := range rep.Types {
		own := slices.Clone(ty.Own)
		slices.Sort(own)
		fmt.Fprintf(f, "type %s own=%v children=%v closure=%v\n", ty.Type, own, ty.Children, ty.Closure)
	}
}
