// WITNESS: panic
//
// A fallible constructor returns `(nil, err)` on failure; the caller logs `err` in an `if err != nil`
// block that forgets to `return`, then dereferences the nil companion pointer and panics.
// GoSree's ERR002 statically convicts the unchecked companion dereference.
package main

import "errors"

type Conn struct{ Host string }

func dial(addr string) (*Conn, error) {
	if addr == "" {
		return nil, errors.New("empty address")
	}
	return &Conn{Host: addr}, nil
}

func main() {
	c, err := dial("")
	if err != nil {
		_ = err.Error()
	}
	println(c.Host) // HIT(ERR002)
}
