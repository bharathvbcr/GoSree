package kinds

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
)

// FuncOverride replaces gosree's summary of a function of another package with an assumption, the
// way Override does for a type. An entry replaces the whole summary, so it states every flag, and
// it asserts only what Why rests on: an entry added to make a result fresh keeps its arguments
// leaked and written.
type FuncOverride struct {
	// Name is the function's types.Func.FullName: "encoding/json.Marshal",
	// "(*bytes.Buffer).Write". It is the key the spawner and lock tables use too.
	Name string
	// Leaks[i] and Writes[i] say the function may retain argument i, or write memory reachable from
	// it. Both follow SSA parameter order (a method's receiver is 0) and have one entry per parameter.
	Leaks, Writes []bool
	// Fresh says every pointer-bearing result is a new allocation nothing else references.
	Fresh bool
	// Why records the documentation or convention the assumption rests on.
	Why string
}

// funcOverrides is the curated table of function summaries gosree assumes rather than computes. Every
// entry counts toward the override budget (TestOverrideBudget).
var funcOverrides = []FuncOverride{
	{"context.WithCancel", []bool{true}, []bool{false}, true,
		"the returned Context is new; its CancelFunc is documented safe to call from multiple goroutines"},
	{"context.WithTimeout", []bool{true, false}, []bool{false, false}, true,
		"the returned Context is new; its CancelFunc is documented safe to call from multiple goroutines"},
	{"io.ReadAll", []bool{true}, []bool{true}, true,
		"returns the data it read in a slice it allocates; the reader is read, so it is written"},
	{"encoding/json.Marshal", []bool{true}, []bool{true}, true,
		"returns a copy of its encoder's buffer; the value may run MarshalJSON, so it stays leaked and written"},
}

// FuncOverrides returns the function override table, sorted by name.
func FuncOverrides() []FuncOverride {
	out := append([]FuncOverride(nil), funcOverrides...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var funcOverrideByName = func() map[string]FuncOverride {
	m := map[string]FuncOverride{}
	for _, o := range funcOverrides {
		m[o.Name] = o
	}
	return m
}()

// FuncOverrideFor returns the override for the function with this FullName.
func FuncOverrideFor(fullName string) (FuncOverride, bool) {
	o, ok := funcOverrideByName[fullName]
	return o, ok
}

// PackageOf returns the import path of the package a FullName ("pkg/path.F", "(*pkg/path.T).M",
// "(pkg/path.T).M") belongs to.
func PackageOf(fullName string) string {
	_, typeName, _ := splitFullName(fullName)
	if typeName != "" {
		return typeName[:strings.LastIndex(typeName, ".")]
	}
	// A function name has no dots; a package path's last element may ("gopkg.in/yaml.v3").
	return fullName[:strings.LastIndex(fullName, ".")]
}

// splitFullName splits a method's FullName into its receiver (pointer or not), its qualified type
// name and its method name. For a function it returns an empty type name.
func splitFullName(name string) (pointer bool, typeName, method string) {
	if !strings.HasPrefix(name, "(") {
		return false, "", ""
	}
	end := strings.Index(name, ").")
	if end < 0 {
		return false, "", ""
	}
	recv := name[1:end]
	if strings.HasPrefix(recv, "*") {
		pointer, recv = true, recv[1:]
	}
	return pointer, recv, name[end+2:]
}

// Resolve finds the function of pkg whose FullName is name. It is the one resolver the call tables
// (spawners, lock operations, function overrides) are checked with, so a renamed function fails the
// check instead of silently dropping out of a table.
func Resolve(pkg *types.Package, name string) (*types.Func, error) {
	if PackageOf(name) != pkg.Path() {
		return nil, fmt.Errorf("%s is not in package %s", name, pkg.Path())
	}
	var obj types.Object
	if pointer, typeName, method := splitFullName(name); typeName != "" {
		tn, ok := pkg.Scope().Lookup(typeName[strings.LastIndex(typeName, ".")+1:]).(*types.TypeName)
		if !ok {
			return nil, fmt.Errorf("%s: package %s has no type %s", name, pkg.Path(), typeName)
		}
		t := tn.Type()
		if pointer {
			t = types.NewPointer(t)
		}
		obj, _, _ = types.LookupFieldOrMethod(t, false, pkg, method)
	} else {
		obj = pkg.Scope().Lookup(name[strings.LastIndex(name, ".")+1:])
	}
	fn, ok := obj.(*types.Func)
	if !ok || fn.FullName() != name {
		return nil, fmt.Errorf("%s does not match any function of %s", name, pkg.Path())
	}
	return fn, nil
}

// Params returns the number of SSA parameters of fn: its receiver, if any, then its parameters.
func Params(fn *types.Func) int {
	sig := fn.Signature()
	n := sig.Params().Len()
	if sig.Recv() != nil {
		n++
	}
	return n
}

// CheckFuncOverrides checks every entry of overrides that belongs to pkg: its name resolves to a
// function of pkg and its Leaks and Writes have one flag per parameter. An entry that does not match
// is an error, never a skipped entry.
func CheckFuncOverrides(pkg *types.Package, overrides []FuncOverride) error {
	for _, o := range overrides {
		if PackageOf(o.Name) != pkg.Path() {
			continue
		}
		fn, err := Resolve(pkg, o.Name)
		if err != nil {
			return fmt.Errorf("function override: %w", err)
		}
		if n := Params(fn); len(o.Leaks) != n || len(o.Writes) != n {
			return fmt.Errorf("function override %s: %d parameters, but %d Leaks and %d Writes flags", o.Name, n, len(o.Leaks), len(o.Writes))
		}
	}
	return nil
}
