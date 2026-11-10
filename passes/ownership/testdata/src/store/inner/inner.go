// Package inner is the last link of a cross-package write: handlers -> store.Forward -> inner.Set.
package inner

// Set replaces the map *m points to.
func Set(m *map[string]int) { *m = map[string]int{} } // want Set:"ownership summary"
