// WITNESS: race
//
// Config passes the imm shape check because its only direct field, inner, is unexported; but
// inner's exported field Port is promoted, so encoding/json (or any other package) can write it.
// The imm *Config is shared with a reader goroutine and the writer decodes JSON into it: no field
// address, no conversion, nothing for the store checks to see.
package main

import (
	"encoding/json"
	"sync"
)

type inner struct{ Port int }

// Config is accepted as imm.
//
//gosree:imm
type Config struct { // MISS(SAFE005): the promoted exported field Port makes Config writable outside its package; only direct fields are checked for Exported()
	inner
}

func main() {
	c := &Config{inner{Port: 1}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s := 0
		for i := 0; i < 200; i++ {
			s += c.Port
		}
		_ = s
	}()
	for i := 0; i < 200; i++ {
		_ = json.Unmarshal([]byte(`{"Port":5}`), c) // MISS(SAFE005): decodes into the shared imm value, writing c.Port
	}
	wg.Wait()
}
