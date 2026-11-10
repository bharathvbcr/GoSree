package store

import (
	"fmt"
	"sync"
)

// Poke and Hold have no Go body (an assembly or linkname implementation, say): gocap cannot see what
// they do, so their summaries say they may write every argument, and say why.
func Poke(p *int) // want Poke:"ownership summary"

func Hold(mu *sync.Mutex) // want Hold:"ownership summary"

// Show formats its argument. fmt.Println puts s in a variadic slice and may write that slice, but a
// container holding s is not s: Show does not write s.
func Show(s *Store) { fmt.Println(s) } // want Show:"ownership summary"
