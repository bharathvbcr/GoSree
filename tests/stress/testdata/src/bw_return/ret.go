// Package bw_return probes BORROW004 ("cannot return reference to local stack variable").
//
// BORROW004 is not opt-in: checkDanglingReturn runs on every function, annotated or not, and
// reports any pointer result whose SSA origin is an Alloc named after a local. In Go this never
// dangles: escape analysis moves an address-taken local that outlives its frame to the heap. The
// rationale in docs/BORROW_CHECKING_AND_NLL.md §6 ("the pointer will point to deallocated or
// overwritten stack memory") describes C, not Go. So every report below is on correct, idiomatic Go.
package bw_return

type Config struct {
	Name string
	Size int
}

// 1. the constructor idiom: build a value, return its address.
func NewConfig(name string) *Config {
	c := Config{Name: name, Size: 1}
	return &c // FP(BORROW004): c escapes to the heap; `return &c` is the standard Go constructor shape
}

// 2. zero value declared with var.
func NewEmpty() *Config {
	var c Config
	return &c // FP(BORROW004): same as NewConfig; Go guarantees the pointer stays valid
}

// 3. address of a parameter: a common way to get a pointer to a literal value.
func IntPtr(v int) *int {
	return &v // FP(BORROW004): v is heap-allocated by the compiler; this is how proto/aws-sdk style Ptr helpers are written
}

// 4. through an alias.
func NewViaAlias() *Config {
	c := Config{}
	p := &c
	p.Size = 2
	return p // FP(BORROW004): p == &c, which escapes to the heap
}

// 5. a field of a local.
func SizePtr() *int {
	c := Config{Size: 3}
	return &c.Size // FP(BORROW004): &c.Size keeps c alive on the heap
}

// 6. controls: the spellings the checker exempts by Alloc comment ("complit", "new").
func NewLit() *Config {
	return &Config{Name: "a"}
}

func NewNew() *Config {
	return new(Config)
}

// 7. a slice of a local array: also a reference to a local, also safe in Go, and not reported
// (results that are not *types.Pointer are skipped). Recorded clean: consistent with Go, though
// not with the wording of docs §6.
func Window() []int {
	var arr [4]int
	arr[0] = 1
	return arr[:]
}
