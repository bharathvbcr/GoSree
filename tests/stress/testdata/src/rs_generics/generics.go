// Package rs_generics: Rust's traits make equality and hashing static. A type without PartialEq
// cannot be compared, one without Hash + Eq cannot be a map key, and a Box<dyn Any> must be
// downcast through Option. Go's interfaces accept any dynamic type and check comparability at run
// time, so == and map insertion panic. gosree has no rule; go vet has none.
package rs_generics

// 1. == on interfaces holding slices panics (rs_w_ifaceeq runs it).
func Same(a, b any) bool {
	return a == b // MISS(NORULE): panics when both hold the same uncomparable type, e.g. []int
}

// 2. a map keyed by an interface panics on an unhashable key.
func CountKeys(keys []any) map[any]int {
	m := map[any]int{}
	for _, k := range keys {
		m[k]++ // MISS(NORULE): panics for a key holding a slice, map or func; Rust requires K: Hash + Eq
	}
	return m
}

// 3. an any-typed container with unchecked assertions.
type Bag struct{ items []any }

func (b *Bag) Put(v any) { b.items = append(b.items, v) }

func (b *Bag) Ints() int {
	sum := 0
	for _, v := range b.items {
		sum += v.(int) // MISS(NORULE): panics on the first non-int; Rust's Vec<i64> or downcast_ref::<i64>() -> Option
	}
	return sum
}

// 4. comparable allows interfaces since Go 1.20, so the generic version compiles and panics too.
func Index[T comparable](xs []T, x T) int {
	for i, v := range xs {
		if v == x { // MISS(NORULE): with T = any holding slices this panics; Rust's PartialEq bound is checked statically
			return i
		}
	}
	return -1
}

func IndexAny(xs []any, x any) int { return Index(xs, x) }
