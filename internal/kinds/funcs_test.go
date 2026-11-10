package kinds

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestPackageOf(t *testing.T) {
	for name, want := range map[string]string{
		"encoding/json.Marshal":                  "encoding/json",
		"context.WithCancel":                     "context",
		"(*bytes.Buffer).Write":                  "bytes",
		"(time.Time).Add":                        "time",
		"(*golang.org/x/sync/errgroup.Group).Go": "golang.org/x/sync/errgroup",
		"gopkg.in/yaml.v3.Marshal":               "gopkg.in/yaml.v3",
	} {
		if got := PackageOf(name); got != want {
			t.Errorf("PackageOf(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestCheckFuncOverrides checks that an entry which does not match its package is an error: a wrong
// name, a method on the wrong receiver, or the wrong number of flags.
func TestCheckFuncOverrides(t *testing.T) {
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedTypes}, "bytes")
	if err != nil || len(pkgs) != 1 || pkgs[0].Types == nil {
		t.Fatalf("load bytes: %v", err)
	}
	pkg := pkgs[0].Types
	two := []bool{false, false}
	ok := []FuncOverride{
		{Name: "bytes.Clone", Leaks: []bool{false}, Writes: []bool{false}},
		{Name: "(*bytes.Buffer).WriteString", Leaks: two, Writes: two},
		{Name: "(*bytes.Buffer).Len", Leaks: []bool{false}, Writes: []bool{false}},
		{Name: "strings.Clone"}, // another package: not checked here
	}
	if err := CheckFuncOverrides(pkg, ok); err != nil {
		t.Errorf("valid entries: %v", err)
	}
	for _, tc := range []struct {
		o    FuncOverride
		want string
	}{
		{FuncOverride{Name: "bytes.Klone", Leaks: []bool{false}, Writes: []bool{false}}, "does not match"},
		{FuncOverride{Name: "(bytes.Buffer).WriteString", Leaks: two, Writes: two}, "does not match"},
		{FuncOverride{Name: "(*bytes.Bufer).WriteString", Leaks: two, Writes: two}, "has no type"},
		{FuncOverride{Name: "bytes.Clone", Leaks: two, Writes: []bool{false}}, "1 parameters"},
	} {
		err := CheckFuncOverrides(pkg, []FuncOverride{tc.o})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", tc.o.Name, err, tc.want)
		}
	}
}
