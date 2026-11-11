package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/bharathvbcr/gosree/internal/kinds"
	"github.com/bharathvbcr/gosree/passes/ownership"
)

// The census answers the question README item 4 turns on: of the transfers hatched because they
// share a long-lived dependency (verdict IMM_BLOCKED), how many could a frozen-after-construction
// imm rule clear? It resolves each hatch's named value to a type, walks every struct type reachable
// from it (field types, and the concrete types stored into interface fields), and sorts what stops
// each one from being imm under that rule.
//
// Blocker classes, worst first:
//
//	W-lock  a field is written after construction in a function that takes a lock, and read in one
//	        that takes none (publish under lock)
//	W       a field is written after construction, with no lock (setter, lazy init, init method)
//	C-written  an unguarded map, slice or pointer-to-plain-data field whose contents are written, or
//	        that is reassigned, after construction (mutates follows the uses)
//	C-frozen   such a field only read once built (a frozen-collections rule could allow it)
//	X       a field of a non-shareable type from outside the module (an override would vouch for it)
//	E       an exported field on an exported type outside internal/ (another module could write it)
//	G       a field written after construction under a lock and read only in functions that take
//	        one: a //gosree:guarded_by row would make it allowed (annotation work, not a blocker)
//	OK      only slots the rule allows: shareable, guarded_by, imm-candidate structs, interface and
//	        func fields whose stored values are themselves candidates
//
// "Takes a lock" means the function calls Lock or RLock anywhere, or every static caller does (three
// levels, lockHeld). Which lock is not checked, and a lock held only on some path counts, so G can
// over-count; a function reached only through a function value or an interface counts as unlocked,
// so W-lock can over-count too.
//
// "After construction" is a store into a field whose base is not an allocation of the storing
// function (a composite literal, new(T), &T{}); a store through a parameter, a load or a global is
// after. The census reads the code and gosree's function summaries (a collection handed to a function
// whose summary writes or keeps it is written); it errs towards reporting a blocker (an interface
// field whose stores it cannot see is X).
const (
	classWLock = "W-lock"
	classW     = "W"
	classX     = "X"
	classE     = "E"
	classG     = "G"
	// classCWritten and classCFrozen split C: an unguarded collection whose contents are written
	// after construction, and one only read once built (a frozen-collections rule could allow it).
	classCWritten = "C-written"
	classCFrozen  = "C-frozen"
	classOK       = "OK"
)

var classOrder = []string{classWLock, classW, classCWritten, classX, classE, classCFrozen, classG, classOK}

// CensusType is one struct type's own blockers (not its children's).
type CensusType struct {
	Type     string   `json:"type"`
	Own      []string `json:"own"`      // classes of this type's own fields and writes
	Reasons  []string `json:"reasons"`  // one line per blocker
	Children []string `json:"children"` // module struct types it reaches
	Closure  []string `json:"closure"`  // classes over the type and everything it reaches
}

// CensusHatch is one IMM_BLOCKED row resolved to its root type.
type CensusHatch struct {
	File    string   `json:"file"`
	Name    string   `json:"name"`
	Root    string   `json:"root"`
	Closure []string `json:"closure"`
	Worst   string   `json:"worst"`
}

// CensusReport is written to <out>/census.json.
type CensusReport struct {
	Hatches    []CensusHatch  `json:"hatches"`
	Types      []CensusType   `json:"types"`
	ByWorst    map[string]int `json:"by_worst"`
	WithWrites int            `json:"with_post_construction_writes"`
	Unresolved int            `json:"unresolved"`
}

type census struct {
	module string
	pkgs   []*packages.Package
	prog   *ssa.Program
	cls    *kinds.Classifier
	// writes[t] are the post-construction writes to fields of t, with whether a lock was held.
	writes map[*types.Named][]fieldWrite
	// reads[t][field] are the loads of a field outside construction, with whether a lock was held.
	reads map[*types.Named]map[string][]fieldWrite
	// contentWrites[t][field] says why the contents of an unguarded collection field of t may be
	// written after construction ("" or absent: only read).
	contentWrites map[*types.Named]map[string]string
	// facts are gosree's summaries of the functions the module calls (std and dependencies included).
	facts map[*types.Func]*ownership.FuncFact
	// ifaceStores[t][field] are the static types stored into an interface or func field of t.
	ifaceStores map[*types.Named]map[string][]types.Type
	types       map[*types.Named]*CensusType
	closure     map[*types.Named][]string
	guarded     map[*types.Var]bool
	imm         map[*types.TypeName]bool
}

type fieldWrite struct {
	field  string
	fn     string
	locked bool
}

func runCensus(pkgs []*packages.Package, acts []*checker.Action, rows []annotation, dir, out string) {
	rep, err := censusOf(pkgs, summaries(acts), rows, dir)
	if err != nil {
		log.Fatalf("-census: %v", err)
	}
	path := writeJSON(out, "census.json", rep)
	printCensus(rep, path)
}

// summaries collects gosree's function summaries from an analysis run: each root action carries the
// facts of every package it imports.
func summaries(acts []*checker.Action) map[*types.Func]*ownership.FuncFact {
	out := map[*types.Func]*ownership.FuncFact{}
	for _, act := range acts {
		if act.Analyzer != ownership.Analyzer {
			continue
		}
		for _, of := range act.AllObjectFacts() {
			if f, ok := of.Fact.(*ownership.FuncFact); ok {
				if fn, ok := of.Object.(*types.Func); ok {
					out[fn] = f
				}
			}
		}
	}
	return out
}

// censusOf classifies the production IMM_BLOCKED rows of a table against the loaded module in dir.
func censusOf(pkgs []*packages.Package, facts map[*types.Func]*ownership.FuncFact, rows []annotation, dir string) (CensusReport, error) {
	c := &census{
		pkgs:          pkgs,
		facts:         facts,
		contentWrites: map[*types.Named]map[string]string{},
		writes:        map[*types.Named][]fieldWrite{},
		reads:         map[*types.Named]map[string][]fieldWrite{},
		ifaceStores:   map[*types.Named]map[string][]types.Type{},
		types:         map[*types.Named]*CensusType{},
		closure:       map[*types.Named][]string{},
		guarded:       map[*types.Var]bool{},
		imm:           map[*types.TypeName]bool{},
	}
	// LoadAllSyntax does not load module information, so the module path comes from go.mod.
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return CensusReport{}, err
	}
	c.module = modulePath(mod)
	if c.module == "" {
		return CensusReport{}, fmt.Errorf("no module line in %s/go.mod", dir)
	}
	c.cls = kinds.NewClassifier(func(tn *types.TypeName) bool { return c.imm[tn] })
	c.readDirectives()
	prog, _ := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()
	c.prog = prog
	c.scanWrites()

	rep := CensusReport{ByWorst: map[string]int{}}
	for _, r := range rows {
		if strings.HasSuffix(r.file, "_test.go") || !strings.HasPrefix(r.verdict, "IMM_BLOCKED:") {
			continue
		}
		name := strings.TrimPrefix(r.verdict, "IMM_BLOCKED:")
		h := CensusHatch{File: r.file, Name: name}
		if t := c.resolve(dir, r, name); t != nil {
			h.Root, h.Closure = c.rootClasses(t)
		}
		if h.Root == "" {
			h.Root = "(unresolved)"
			rep.Unresolved++
		}
		h.Worst = worst(h.Closure)
		rep.ByWorst[h.Worst]++
		if slices.Contains(h.Closure, classW) || slices.Contains(h.Closure, classWLock) {
			rep.WithWrites++
		}
		rep.Hatches = append(rep.Hatches, h)
	}
	for _, ct := range c.types {
		rep.Types = append(rep.Types, *ct)
	}
	sort.Slice(rep.Types, func(i, j int) bool { return rep.Types[i].Type < rep.Types[j].Type })
	return rep, nil
}

// modulePath returns the path on go.mod's module line, or "" if it has none.
func modulePath(gomod []byte) string {
	for l := range strings.Lines(string(gomod)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "module"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

func worst(classes []string) string {
	for _, k := range classOrder {
		if slices.Contains(classes, k) {
			return k
		}
	}
	return "(unresolved)"
}

// readDirectives records //gosree:guarded_by fields and //gosree:imm types from the syntax.
func (c *census) readDirectives() {
	for _, p := range c.pkgs {
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.GenDecl:
					for _, s := range n.Specs {
						ts, ok := s.(*ast.TypeSpec)
						if !ok {
							continue
						}
						if hasDirective(n.Doc, "gosree:imm") || hasDirective(ts.Doc, "gosree:imm") {
							if tn, ok := p.TypesInfo.Defs[ts.Name].(*types.TypeName); ok {
								c.imm[tn] = true
							}
						}
					}
				case *ast.Field:
					if hasDirective(n.Doc, "gosree:guarded_by") || hasDirective(n.Comment, "gosree:guarded_by") {
						for _, id := range n.Names {
							if v, ok := p.TypesInfo.Defs[id].(*types.Var); ok {
								c.guarded[v] = true
							}
						}
					}
				}
				return true
			})
		}
	}
}

func hasDirective(cg *ast.CommentGroup, d string) bool {
	if cg == nil {
		return false
	}
	for _, cm := range cg.List {
		if strings.HasPrefix(strings.TrimPrefix(cm.Text, "//"), d) {
			return true
		}
	}
	return false
}

// scanWrites finds every store into a field of a module struct type, and every value stored into an
// interface or func field, across the module's functions.
func (c *census) scanWrites() {
	held := c.lockHeld()
	for fn := range ssautil.AllFunctions(c.prog) {
		if fn.Pkg == nil || !c.inModule(fn.Pkg.Pkg) {
			continue
		}
		locked := held[fn]
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				var addr ssa.Value
				var val ssa.Value
				switch ins := ins.(type) {
				case *ssa.Store:
					addr, val = ins.Addr, ins.Val
				case *ssa.Field:
					// A collection field of a struct held by value (ServerConfig is passed by value):
					// the copy shares the collection, so its uses count like a load's.
					if t := namedStruct(ins.X.Type()); t != nil && c.inModule(t.Obj().Pkg()) {
						c.checkContents(t, structOf(t).Field(ins.Field), ins)
					}
				}
				if ld, ok := ins.(*ssa.UnOp); ok && ld.Op == token.MUL {
					c.recordRead(ld.X, fn.String(), locked)
					if fa, ok := ld.X.(*ssa.FieldAddr); ok && !isLocalAlloc(fa.X) {
						if t := namedStruct(fa.X.Type()); t != nil && c.inModule(t.Obj().Pkg()) {
							c.checkContents(t, structOf(t).Field(fa.Field), ld)
						}
					}
					continue
				}
				fa, ok := addr.(*ssa.FieldAddr)
				if !ok {
					continue
				}
				t := namedStruct(fa.X.Type())
				if t == nil || !c.inModule(t.Obj().Pkg()) {
					continue
				}
				field := structOf(t).Field(fa.Field)
				if val != nil && isSlot(field.Type()) {
					if c.ifaceStores[t] == nil {
						c.ifaceStores[t] = map[string][]types.Type{}
					}
					c.ifaceStores[t][field.Name()] = append(c.ifaceStores[t][field.Name()], dynamicType(val))
				}
				if !isLocalAlloc(fa.X) {
					c.writes[t] = append(c.writes[t], fieldWrite{field: field.Name(), fn: fn.String(), locked: locked})
				}
			}
		}
	}
}

// isCollection reports whether a field's type is shared mutable data the imm rule disallows unless
// guarded: a map, a slice, or a pointer to something other than a struct (a struct pointer is a child
// type the census walks instead).
func isCollection(t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Map, *types.Slice:
		return true
	case *types.Pointer:
		return namedStruct(u) == nil
	}
	return false
}

// checkContents records, for an unguarded collection field read after construction as v, the first
// use of v that may write its contents.
func (c *census) checkContents(t *types.Named, f *types.Var, v ssa.Value) {
	if !isCollection(f.Type()) || c.guarded[f] {
		return
	}
	if c.contentWrites[t] == nil {
		c.contentWrites[t] = map[string]string{}
	}
	if c.contentWrites[t][f.Name()] != "" {
		return
	}
	if why := c.mutates(v, 0, map[ssa.Value]bool{}); why != "" {
		c.contentWrites[t][f.Name()] = why
	}
}

// mutates follows the uses of a collection value and returns why its contents may be written, or ""
// if every use only reads it: an index or map read, range, len or cap, a comparison, or a call whose
// summary neither writes nor keeps it. An update, an element store, append or copy into it, delete or
// clear, storing it anywhere, returning it, or a call gosree cannot see all count as writes.
func (c *census) mutates(v ssa.Value, depth int, seen map[ssa.Value]bool) string {
	if seen[v] {
		return ""
	}
	seen[v] = true
	refs := v.Referrers()
	if refs == nil {
		return ""
	}
	for _, r := range *refs {
		in := " in " + r.Parent().String()
		switch r := r.(type) {
		case *ssa.MapUpdate:
			return "map update" + in
		case *ssa.IndexAddr:
			if why := c.addrWritten(r, depth, seen); why != "" {
				return why
			}
		case *ssa.Slice, *ssa.ChangeType, *ssa.Phi, *ssa.Convert:
			if why := c.mutates(r.(ssa.Value), depth, seen); why != "" {
				return why
			}
		case *ssa.Lookup, *ssa.Index, *ssa.Range, *ssa.BinOp, *ssa.DebugRef, *ssa.If, *ssa.UnOp:
			// Reads: a map or index read, ranging, comparing, dereferencing a pointer to plain data.
		case *ssa.Store:
			if r.Addr == v {
				return "store through it" + in
			}
			return "stored elsewhere" + in
		case ssa.CallInstruction:
			if why := c.callMutates(r, v, depth, seen); why != "" {
				return why
			}
		default:
			return fmt.Sprintf("%T", r) + in
		}
	}
	return ""
}

// addrWritten follows the uses of an element address.
func (c *census) addrWritten(a ssa.Value, depth int, seen map[ssa.Value]bool) string {
	for _, r := range *a.Referrers() {
		in := " in " + r.Parent().String()
		switch r := r.(type) {
		case *ssa.Store:
			if r.Addr == a {
				return "element store" + in
			}
			return "element address stored" + in
		case *ssa.UnOp, *ssa.DebugRef:
		case *ssa.FieldAddr, *ssa.IndexAddr:
			if why := c.addrWritten(r.(ssa.Value), depth, seen); why != "" {
				return why
			}
		case ssa.CallInstruction:
			if why := c.callMutates(r, a, depth, seen); why != "" {
				return why
			}
		default:
			return fmt.Sprintf("element address used by %T", r) + in
		}
	}
	return ""
}

// callMutates says whether a call may write or keep v, which it receives as an argument.
func (c *census) callMutates(call ssa.CallInstruction, v ssa.Value, depth int, seen map[ssa.Value]bool) string {
	cc := call.Common()
	in := " in " + call.Parent().String()
	if b, ok := cc.Value.(*ssa.Builtin); ok {
		switch b.Name() {
		case "len", "cap", "min", "max", "print", "println":
			return ""
		case "copy":
			if cc.Args[0] == v {
				return "copy into it" + in
			}
			return ""
		case "append":
			if cc.Args[0] == v {
				return "append to it" + in
			}
			return ""
		}
		return b.Name() + in
	}
	if cc.IsInvoke() || cc.Value == v {
		return "passed to a dynamic call" + in
	}
	callee := cc.StaticCallee()
	if callee == nil {
		return "passed to a dynamic call" + in
	}
	for i, a := range cc.Args {
		if a != v {
			continue
		}
		if obj, ok := callee.Object().(*types.Func); ok {
			if f := c.facts[obj.Origin()]; f != nil {
				if f.Unknown || i >= len(f.Leaks) || f.Leaks[i] || f.Writes[i] {
					return "passed to " + callee.String() + ", which may write or keep it" + in
				}
				continue
			}
		}
		// No exported summary: an unexported function of the module is followed into its body.
		if depth < 3 && callee.Blocks != nil && i < len(callee.Params) && callee.Pkg != nil && c.inModule(callee.Pkg.Pkg) {
			if why := c.mutates(callee.Params[i], depth+1, seen); why != "" {
				return why
			}
			continue
		}
		return "passed to " + callee.String() + " (no summary)" + in
	}
	return ""
}

// recordRead notes a load of a field of a module struct through a base that is not under
// construction, and whether the reading function takes a lock.
func (c *census) recordRead(addr ssa.Value, fn string, locked bool) {
	fa, ok := addr.(*ssa.FieldAddr)
	if !ok || isLocalAlloc(fa.X) {
		return
	}
	t := namedStruct(fa.X.Type())
	if t == nil || !c.inModule(t.Obj().Pkg()) {
		return
	}
	name := structOf(t).Field(fa.Field).Name()
	if c.reads[t] == nil {
		c.reads[t] = map[string][]fieldWrite{}
	}
	c.reads[t][name] = append(c.reads[t][name], fieldWrite{field: name, fn: fn, locked: locked})
}

// unlockedRead returns a function that reads the field without taking a lock, or "".
func (c *census) unlockedRead(t *types.Named, field string) string {
	for _, r := range c.reads[t][field] {
		if !r.locked {
			return r.fn
		}
	}
	return ""
}

// isLocalAlloc reports whether v is an allocation made by the function it is used in: the object is
// still under construction there.
func isLocalAlloc(v ssa.Value) bool {
	switch v := v.(type) {
	case *ssa.Alloc:
		return true
	case *ssa.FieldAddr:
		return isLocalAlloc(v.X)
	}
	return false
}

// lockHeld reports, for each module function, whether a lock is taken in it or in every one of its
// static callers (to a depth of three, and only when it has callers): a helper such as stopLocked runs
// under its caller's lock. A function reached through a function value or an interface has no static
// caller and counts as unlocked unless it locks.
func (c *census) lockHeld() map[*ssa.Function]bool {
	callers := map[*ssa.Function][]*ssa.Function{}
	held := map[*ssa.Function]bool{}
	var fns []*ssa.Function
	for fn := range ssautil.AllFunctions(c.prog) {
		if fn.Pkg == nil || !c.inModule(fn.Pkg.Pkg) {
			continue
		}
		fns = append(fns, fn)
		held[fn] = takesLock(fn)
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				if call, ok := ins.(ssa.CallInstruction); ok {
					if callee := call.Common().StaticCallee(); callee != nil {
						callers[callee] = append(callers[callee], fn)
					}
				}
			}
		}
	}
	for range 3 {
		next := map[*ssa.Function]bool{}
		for _, fn := range fns {
			ok := held[fn]
			if !ok && len(callers[fn]) > 0 {
				ok = true
				for _, caller := range callers[fn] {
					if !held[caller] {
						ok = false
						break
					}
				}
			}
			next[fn] = ok
		}
		held = next
	}
	return held
}

func takesLock(fn *ssa.Function) bool {
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			call, ok := ins.(ssa.CallInstruction)
			if !ok {
				continue
			}
			if callee := call.Common().StaticCallee(); callee != nil {
				switch callee.String() {
				case "(*sync.Mutex).Lock", "(*sync.RWMutex).Lock", "(*sync.RWMutex).RLock":
					return true
				}
			}
		}
	}
	return false
}

// dynamicType is the static type of what is stored into an interface field: the operand of the
// conversion, or the value's own type when it is already an interface (unknown dynamic type).
func dynamicType(v ssa.Value) types.Type {
	switch v := v.(type) {
	case *ssa.MakeInterface:
		return v.X.Type()
	case *ssa.ChangeInterface:
		return v.X.Type()
	}
	return v.Type()
}

func isSlot(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Interface, *types.Signature:
		return true
	}
	return false
}

func (c *census) inModule(p *types.Package) bool {
	return p != nil && c.module != "" && (p.Path() == c.module || strings.HasPrefix(p.Path(), c.module+"/"))
}

func namedStruct(t types.Type) *types.Named {
	t = types.Unalias(t)
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, ok := t.(*types.Named)
	if !ok {
		return nil
	}
	if _, ok := n.Underlying().(*types.Struct); !ok {
		return nil
	}
	return n.Origin()
}

// reach returns the module struct types a type expression names, in order of first appearance: a
// named module struct (its generic origin) and the type arguments of any instantiated named type,
// and through arrays, slices, maps, channels, pointers, unnamed struct literals and the underlying
// type of named non-struct types (type Bs []*B). Left out on purpose:
//   - func and interface element types: what they hold is only known per field from the stores
//     the census sees (ifaceStores), not from the type, so an element of such a type is not a child;
//   - type parameters: uninstantiated, they stand for whatever each use site supplies, and every
//     use site's type arguments are walked where the instantiation appears;
//   - a named struct's own fields: that struct is a child, and the closure walk reads its fields.
//
// The walk terminates on recursive named types (type T []T, type List[X] struct{ next *List[X] }):
// a struct is never entered, and a named non-struct's underlying type is entered once.
func (c *census) reach(t types.Type) []*types.Named {
	var out []*types.Named
	seen := map[*types.Named]bool{}
	var walk func(types.Type)
	walk = func(t types.Type) {
		switch t := types.Unalias(t).(type) {
		case *types.Named:
			o := t.Origin()
			// The type expression is finite, so its arguments are always walked (Box[Box[B]] names
			// B through two instantiations of one origin); only the underlying type of a named
			// non-struct can recurse into itself, so only that step is guarded.
			for a := range t.TypeArgs().Types() {
				walk(a)
			}
			if _, ok := o.Underlying().(*types.Struct); ok {
				if c.inModule(o.Obj().Pkg()) && !slices.Contains(out, o) {
					out = append(out, o)
				}
				return
			}
			if !seen[o] {
				seen[o] = true
				walk(o.Underlying())
			}
		case *types.Pointer:
			walk(t.Elem())
		case *types.Slice:
			walk(t.Elem())
		case *types.Array:
			walk(t.Elem())
		case *types.Chan:
			walk(t.Elem())
		case *types.Map:
			walk(t.Key())
			walk(t.Elem())
		case *types.Struct:
			for f := range t.Fields() {
				walk(f.Type())
			}
		}
	}
	walk(t)
	return out
}

// byValueContainer reports whether t is an array, at any depth, of module structs or pointers to
// them: a type whose only content is children the closure walk reaches.
func byValueContainer(t types.Type) bool {
	a, ok := types.Unalias(t).Underlying().(*types.Array)
	if !ok {
		return false
	}
	if inner := types.Unalias(a.Elem()); byValueContainer(inner) {
		return true
	}
	return namedStruct(a.Elem()) != nil
}

func structOf(n *types.Named) *types.Struct { return n.Underlying().(*types.Struct) }

// rootClasses classifies the hatch's root type: a module struct is walked; anything else is
// classified directly.
func (c *census) rootClasses(t types.Type) (string, []string) {
	if n := namedStruct(t); n != nil && c.inModule(n.Obj().Pkg()) {
		cl := c.classesOf(n)
		for _, k := range c.reach(t) {
			cl = union(slices.Clone(cl), c.classesOf(k))
		}
		slices.Sort(cl)
		return n.String(), cl
	}
	if c.cls.Shareable(t) {
		return t.String(), []string{classOK}
	}
	if types.IsInterface(t) {
		return t.String(), c.interfaceClasses(t)
	}
	return t.String(), []string{classX}
}

// interfaceClasses classifies an interface-typed value by the module types that implement it; with
// none in the module, the value is of a type gosree would have to vouch for (X).
func (c *census) interfaceClasses(t types.Type) []string {
	iface, _ := t.Underlying().(*types.Interface)
	var out []string
	found := false
	for _, p := range c.pkgs {
		if !c.inModule(p.Types) {
			continue
		}
		for _, name := range p.Types.Scope().Names() {
			tn, ok := p.Types.Scope().Lookup(name).(*types.TypeName)
			if !ok || types.IsInterface(tn.Type()) {
				continue
			}
			n := namedStruct(tn.Type())
			if n == nil || iface == nil {
				continue
			}
			if types.Implements(n, iface) || types.Implements(types.NewPointer(n), iface) {
				found = true
				out = union(out, c.classesOf(n))
			}
		}
	}
	if !found {
		return []string{classX}
	}
	return out
}

// classesOf returns the classes over n and every module struct it reaches. Types that reference
// each other (Server{cfg *Config}, Config{srv *Server}) share one class, the join over the whole
// strongly connected component, so the answer does not depend on which member is asked first.
func (c *census) classesOf(n *types.Named) []string {
	if cl, ok := c.closure[n]; ok {
		return cl
	}
	s := &sccWalk{c: c, index: map[*types.Named]int{}, low: map[*types.Named]int{}, onStack: map[*types.Named]bool{}, owns: map[*types.Named]ownResult{}}
	s.visit(n)
	return c.closure[n]
}

// sccWalk is Tarjan's algorithm over the struct-reference graph that own reports. A component is
// closed only once every type it reaches outside itself has a final closure, so nothing computed
// inside an open cycle is ever cached.
type sccWalk struct {
	c       *census
	next    int
	index   map[*types.Named]int
	low     map[*types.Named]int
	onStack map[*types.Named]bool
	stack   []*types.Named
	owns    map[*types.Named]ownResult
}

func (s *sccWalk) visit(v *types.Named) {
	s.index[v], s.low[v] = s.next, s.next
	s.next++
	s.stack = append(s.stack, v)
	s.onStack[v] = true
	own := s.c.own(v)
	s.owns[v] = own
	for _, ch := range own.childTypes {
		if _, done := s.c.closure[ch]; done {
			continue
		}
		if _, seen := s.index[ch]; !seen {
			s.visit(ch)
			s.low[v] = min(s.low[v], s.low[ch])
		} else if s.onStack[ch] {
			s.low[v] = min(s.low[v], s.index[ch])
		}
	}
	if s.low[v] != s.index[v] {
		return
	}
	var members []*types.Named
	in := map[*types.Named]bool{}
	for {
		m := s.stack[len(s.stack)-1]
		s.stack = s.stack[:len(s.stack)-1]
		s.onStack[m] = false
		members = append(members, m)
		in[m] = true
		if m == v {
			break
		}
	}
	var cl []string
	for _, m := range members {
		o := s.owns[m]
		cl = union(cl, o.Own)
		for _, ch := range o.childTypes {
			if !in[ch] {
				cl = union(cl, s.c.closure[ch])
			}
		}
	}
	if len(cl) == 0 {
		cl = []string{classOK}
	}
	slices.Sort(cl)
	for _, m := range members {
		s.c.closure[m] = cl
		s.owns[m].Closure = cl
	}
}

type ownResult struct {
	*CensusType
	childTypes []*types.Named
}

func (c *census) own(n *types.Named) ownResult {
	ct := &CensusType{Type: n.String()}
	c.types[n] = ct
	var children []*types.Named
	add := func(class, reason string) {
		if !slices.Contains(ct.Own, class) {
			ct.Own = append(ct.Own, class)
		}
		ct.Reasons = append(ct.Reasons, class+": "+reason)
	}
	if c.imm[n.Obj()] {
		return ownResult{ct, nil}
	}
	exportedType := n.Obj().Exported() && !strings.Contains(n.Obj().Pkg().Path(), "/internal/")
	st := structOf(n)
	for f := range st.Fields() {
		ft := f.Type()
		switch {
		case c.guarded[f]:
			continue
		case f.Exported() && exportedType:
			add(classE, "exported field "+f.Name())
		}
		if !isSlot(ft) {
			// Every module struct the field's type names is a child, whether or not the type is
			// shareable: the struct itself, a generic's type arguments, and the elements of arrays,
			// slices, maps, channels and pointers.
			children = append(children, c.reach(ft)...)
		}
		switch {
		case c.cls.Shareable(ft):
		case isSlot(ft):
			for _, st := range c.ifaceStores[n][f.Name()] {
				if kids := c.reach(st); len(kids) > 0 {
					children = append(children, kids...)
				} else if impls := c.implementers(st); impls != nil {
					// A module interface stored as it came: its dynamic type is one of the module's
					// implementations, or a type from outside the census cannot see.
					children = append(children, impls...)
				} else if !c.cls.Shareable(st) && !isFunc(st) {
					add(classX, fmt.Sprintf("field %s holds %s", f.Name(), st))
				}
			}
		default:
			if child := namedStruct(ft); child != nil && c.inModule(child.Obj().Pkg()) {
				continue
			}
			switch {
			case byValueContainer(ft):
				// An array of module structs (or of pointers to them) holds no slot of its own: its
				// elements are the children above, as a lone struct or pointer field would be.
			case isCollection(ft):
				if c.allAccessLocked(n, f.Name()) && (c.contentWrites[n][f.Name()] != "" || c.reassigned(n, f.Name()) != "") {
					// Every use after construction goes through a load of the field, so if each load
					// and each reassignment holds a lock, a guarded_by row would cover the contents.
					add(classG, fmt.Sprintf("collection %s written and read only under a lock", f.Name()))
				} else if why := c.contentWrites[n][f.Name()]; why != "" {
					add(classCWritten, fmt.Sprintf("unguarded %s field %s: %s", ft, f.Name(), why))
				} else if w := c.reassigned(n, f.Name()); w != "" {
					add(classCWritten, fmt.Sprintf("unguarded %s field %s: reassigned in %s", ft, f.Name(), w))
				} else {
					add(classCFrozen, fmt.Sprintf("unguarded %s field %s, only read after construction", ft, f.Name()))
				}
			case namedStruct(ft) != nil:
				add(classX, fmt.Sprintf("field %s of external type %s", f.Name(), ft))
			default:
				add(classX, fmt.Sprintf("field %s of type %s", f.Name(), ft))
			}
		}
	}
	seen := map[string]bool{}
	for _, w := range c.writes[n] {
		if c.fieldGuarded(st, w.field) || seen[w.field+w.fn] {
			continue
		}
		seen[w.field+w.fn] = true
		if w.locked {
			// Written under a lock: a guarded_by field if every read takes a lock too (annotation
			// work, not a blocker); published under a lock and read without one otherwise.
			if r := c.unlockedRead(n, w.field); r != "" {
				add(classWLock, fmt.Sprintf("%s written in %s under a lock, read in %s without one", w.field, w.fn, r))
			} else {
				add(classG, fmt.Sprintf("%s written in %s under a lock, every read locked", w.field, w.fn))
			}
		} else {
			add(classW, fmt.Sprintf("%s written in %s", w.field, w.fn))
		}
	}
	slices.Sort(ct.Own)
	for _, ch := range children {
		ct.Children = append(ct.Children, ch.String())
	}
	slices.Sort(ct.Children)
	ct.Children = slices.Compact(ct.Children)
	return ownResult{ct, children}
}

// allAccessLocked reports whether every load and every reassignment of the field after construction
// happens in a function that holds a lock (lockHeld). A struct held by value and read through a
// Field instruction is not a load and is not seen here.
func (c *census) allAccessLocked(n *types.Named, field string) bool {
	for _, r := range c.reads[n][field] {
		if !r.locked {
			return false
		}
	}
	for _, w := range c.writes[n] {
		if w.field == field && !w.locked {
			return false
		}
	}
	return true
}

// reassigned returns a function that assigns the field after construction, or "".
func (c *census) reassigned(n *types.Named, field string) string {
	for _, w := range c.writes[n] {
		if w.field == field {
			return w.fn
		}
	}
	return ""
}

// implementers returns the module's struct types implementing a module interface type, or nil when t
// is not one or none implements it.
func (c *census) implementers(t types.Type) []*types.Named {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || !types.IsInterface(t) || !c.inModule(named.Obj().Pkg()) {
		return nil
	}
	iface := t.Underlying().(*types.Interface)
	var out []*types.Named
	for _, p := range c.pkgs {
		if !c.inModule(p.Types) {
			continue
		}
		for _, name := range p.Types.Scope().Names() {
			tn, ok := p.Types.Scope().Lookup(name).(*types.TypeName)
			if !ok || types.IsInterface(tn.Type()) {
				continue
			}
			if n := namedStruct(tn.Type()); n != nil && (types.Implements(n, iface) || types.Implements(types.NewPointer(n), iface)) {
				out = append(out, n)
			}
		}
	}
	return out
}

func (c *census) fieldGuarded(st *types.Struct, name string) bool {
	for f := range st.Fields() {
		if f.Name() == name {
			return c.guarded[f]
		}
	}
	return false
}

func isFunc(t types.Type) bool {
	_, ok := t.Underlying().(*types.Signature)
	return ok
}

func union(a, b []string) []string {
	for _, x := range b {
		if !slices.Contains(a, x) {
			a = append(a, x)
		}
	}
	return a
}

// resolve finds the type of the value a hatch row names, in the function the row is scoped to: a
// receiver, parameter or local of that name, a package variable, or "result of F".
func (c *census) resolve(dir string, r annotation, name string) types.Type {
	path := filepath.Join(dir, r.file)
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	line := -1
	for i, l := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(l, r.scope) {
			line = i + 1
			break
		}
	}
	for _, p := range c.pkgs {
		for _, f := range p.Syntax {
			if p.Fset.Position(f.Pos()).Filename != path {
				continue
			}
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || p.Fset.Position(fd.Pos()).Line != line {
					continue
				}
				return lookupIn(p, fd, name)
			}
		}
	}
	return nil
}

func lookupIn(p *packages.Package, fd *ast.FuncDecl, name string) types.Type {
	if fn, ok := strings.CutPrefix(name, "result of "); ok {
		if i := strings.LastIndex(fn, "."); i >= 0 {
			fn = fn[i+1:]
		}
		var t types.Type
		ast.Inspect(fd, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == fn && t == nil {
				if f, ok := p.TypesInfo.Uses[id].(*types.Func); ok && f.Signature().Results().Len() > 0 {
					t = f.Signature().Results().At(0).Type()
				}
			}
			return t == nil
		})
		return t
	}
	var t types.Type
	ast.Inspect(fd, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != name || t != nil {
			return t == nil
		}
		if v, ok := p.TypesInfo.Defs[id].(*types.Var); ok {
			t = v.Type()
		} else if v, ok := p.TypesInfo.Uses[id].(*types.Var); ok {
			t = v.Type()
		}
		return t == nil
	})
	return t
}

func printCensus(rep CensusReport, path string) {
	n := len(rep.Hatches)
	fmt.Printf("census of %d production IMM_BLOCKED hatches (%d unresolved)\n", n, rep.Unresolved)
	for _, k := range append(classOrder, "(unresolved)") {
		if rep.ByWorst[k] > 0 {
			fmt.Printf("  worst %-12s %4d  %5.1f%%\n", k, rep.ByWorst[k], 100*float64(rep.ByWorst[k])/float64(n))
		}
	}
	fmt.Printf("  reach a post-construction write (W or W-lock): %d = %.1f%% (refuter threshold 40%%)\n", rep.WithWrites, 100*float64(rep.WithWrites)/float64(n))
	fmt.Printf("  report: %s\n", path)
}
