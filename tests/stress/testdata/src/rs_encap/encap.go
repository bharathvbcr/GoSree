// Package rs_encap: Rust's privacy is per module, and a struct with a private field cannot be
// built outside its module, so a constructor's invariant holds for every value. Copying is opt-in
// (Clone/Copy), so a type whose copies would share state is not copied by accident. In Go, every
// importer can make the zero value of any exported type and copy any value. gocap has no rule.
package rs_encap

import "rs_encap/money"

// 1. zero-value construction bypasses money.New: the currency invariant is broken.
func Zero() string {
	var a money.Amount // MISS(NORULE): builds an Amount without New; Rust cannot name a struct with private fields outside its module
	return a.Currency()
}

// 2. a struct copy shares the unexported history slice: appends through one copy can be seen
// through the other (when the backing array has room).
func Fork(a money.Amount) (money.Amount, money.Amount) {
	b := a // MISS(NORULE): copies the private slice header; Rust needs an explicit clone, which would deep-copy the Vec
	a.Add(1)
	b.Add(2)
	return a, b
}
