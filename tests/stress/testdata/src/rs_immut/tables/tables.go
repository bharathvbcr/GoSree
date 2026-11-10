// Package tables holds "constant" lookup tables and an exported config, the way many Go packages
// do. Rust statics are immutable unless declared `static mut` (unsafe to touch) and struct fields
// are private by default.
package tables

// Primes is a lookup table every importer is meant to read.
var Primes = []int{2, 3, 5, 7, 11}

// Names maps codes to names.
var Names = map[int]string{1: "one", 2: "two"}

// Config has exported fields.
type Config struct {
	Limit int
	Hosts []string
}

// Default is the package's default config.
var Default = &Config{Limit: 10, Hosts: []string{"a"}}
