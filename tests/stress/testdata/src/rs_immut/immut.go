// Package rs_immut: immutability by default. Rust bindings and statics are immutable unless
// marked mut, and another crate cannot write a static at all without unsafe. Go package variables
// are writable by every importer through their elements, entries and pointees. gosree's SAFE006
// is per package: it reports writes to a package's own variables, and its README says it "sees
// direct writes only ... A write through a method of another package, or through a pointer that
// escaped the package, does not" count.
package rs_immut

import "rs_immut/tables"

// 1. an importer writes an element of another package's "constant" table.
func Patch() {
	tables.Primes[0] = 4 // MISS(SAFE006): a write to a package variable outside initialisation, from another package
}

// 2. an importer writes a map entry of another package's table.
func Rename() {
	tables.Names[1] = "uno" // MISS(SAFE006): the same, through a map update
}

// 3. an importer mutates exported fields of a shared default.
func Tune() {
	tables.Default.Limit = 0                                 // MISS(SAFE006): a write through a package variable's pointee
	tables.Default.Hosts = append(tables.Default.Hosts, "b") // MISS(SAFE006): the same
}
