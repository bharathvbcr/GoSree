// Package kinds decides which Go types can be handed to another goroutine without creating
// unchecked shared mutable memory.
package kinds

import (
	"go/types"
	"sort"
)

// Override is a named type gocap treats as shareable by assumption rather than by proof.
type Override struct {
	// Name is "pkgpath.Type"; Pointer says the assumption covers *Type rather than Type.
	Name    string
	Pointer bool
	// Why records the documentation or convention the assumption rests on.
	Why string
}

// overrides is the curated table of shared-by-design types. Every entry is an assumption, not a
// check: gocap trusts that these types synchronise their own state.
var overrides = []Override{
	{"sync.Mutex", true, "synchronisation primitive"},
	{"sync.RWMutex", true, "synchronisation primitive"},
	{"sync.WaitGroup", true, "synchronisation primitive"},
	{"sync.Once", true, "synchronisation primitive"},
	{"sync.Cond", true, "synchronisation primitive"},
	{"sync.Map", true, "documented safe for concurrent use"},
	{"sync.Pool", true, "documented safe for concurrent use"},
	{"sync/atomic.Bool", true, "atomic"},
	{"sync/atomic.Int32", true, "atomic"},
	{"sync/atomic.Int64", true, "atomic"},
	{"sync/atomic.Uint32", true, "atomic"},
	{"sync/atomic.Uint64", true, "atomic"},
	{"sync/atomic.Uintptr", true, "atomic"},
	{"sync/atomic.Pointer", true, "atomic"},
	{"sync/atomic.Value", true, "atomic"},
	{"testing.T", true, "reporting methods are documented safe from other goroutines"},
	{"testing.B", true, "reporting methods are documented safe from other goroutines"},
	{"testing.F", true, "reporting methods are documented safe from other goroutines"},
	{"testing.TB", false, "interface over *testing.T/*testing.B/*testing.F"},
	{"context.Context", false, "documented safe for simultaneous use by multiple goroutines"},
	{"error", false, "assumed: error values are not mutated after creation (convention, not checked)"},
	{"time.Time", false, "value type; its *Location is never mutated after creation"},
	{"time.Location", true, "never mutated after creation"},
	{"reflect.Type", false, "type descriptors are immutable"},
	{"regexp.Regexp", true, "documented safe for concurrent use"},
	{"log.Logger", true, "documented safe for concurrent use"},
	{"log/slog.Logger", true, "documented safe for concurrent use"},
}

// Overrides returns the override table, sorted by name.
func Overrides() []Override {
	out := append([]Override(nil), overrides...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var (
	pointerOverride = map[string]bool{}
	valueOverride   = map[string]bool{}
)

func init() {
	for _, o := range overrides {
		if o.Pointer {
			pointerOverride[o.Name] = true
		} else {
			valueOverride[o.Name] = true
		}
	}
}

// Classifier answers Shareable for one package's view of the world.
type Classifier struct {
	// IsImm reports whether a named type is a verified //safe:imm type.
	IsImm func(*types.TypeName) bool
	memo  map[types.Type]bool
}

// NewClassifier returns a Classifier that consults isImm for //safe:imm types.
func NewClassifier(isImm func(*types.TypeName) bool) *Classifier {
	return &Classifier{IsImm: isImm, memo: map[types.Type]bool{}}
}

// QualifiedName returns "pkgpath.Name" for a named type, or "" for other types.
func QualifiedName(t types.Type) string {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return ""
	}
	obj := n.Origin().Obj()
	if obj.Pkg() == nil {
		return obj.Name()
	}
	return obj.Pkg().Path() + "." + obj.Name()
}

// Shareable reports whether a value of type t can be copied to another goroutine without creating
// shared mutable memory that gocap cannot see: pointer-free values, strings, channels, verified imm
// types, pointers to imm types, and the override table.
func (c *Classifier) Shareable(t types.Type) bool {
	if v, ok := c.memo[t]; ok {
		return v
	}
	v := c.shareable(t)
	c.memo[t] = v
	return v
}

func (c *Classifier) shareable(t types.Type) bool {
	t = types.Unalias(t)
	if name := QualifiedName(t); name != "" {
		if valueOverride[name] {
			return true
		}
		if c.imm(t) {
			return true
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		return u.Kind() != types.UnsafePointer
	case *types.Pointer:
		elem := types.Unalias(u.Elem())
		if pointerOverride[QualifiedName(elem)] {
			return true
		}
		return c.imm(elem)
	case *types.Chan:
		return true
	case *types.Struct:
		for f := range u.Fields() {
			if !c.Shareable(f.Type()) {
				return false
			}
		}
		return true
	case *types.Array:
		return c.Shareable(u.Elem())
	case *types.Tuple:
		for v := range u.Variables() {
			if !c.Shareable(v.Type()) {
				return false
			}
		}
		return true
	}
	// Slices, maps, funcs, non-override interfaces and type parameters may all carry mutable
	// shared memory.
	return false
}

func (c *Classifier) imm(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && c.IsImm != nil && c.IsImm(n.Origin().Obj())
}
