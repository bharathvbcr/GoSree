//go:build ignore || (linux && !darwin && windows)

// This file is excluded by its build constraint; it would not compile if it were analysed.
package rb_misc

func excluded() { undefinedSymbol() }
