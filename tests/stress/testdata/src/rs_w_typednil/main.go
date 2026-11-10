// WITNESS: panic
//
// A typed nil in an interface: validate returns a nil *ValidationError, the error interface that
// carries it is not nil, and the caller's err != nil branch calls a method on a nil pointer. Rust
// has no null and no implicit conversion that could produce this. gocap has no rule; go vet's
// nilness analyzer does not see it either (the nil crosses a function boundary).
package main

import "fmt"

type ValidationError struct{ Field string }

func (e *ValidationError) Error() string { return "invalid " + e.Field }

func validate(name string) *ValidationError {
	if name == "" {
		return &ValidationError{Field: "name"}
	}
	return nil
}

func register(name string) error {
	return validate(name) // HIT(ERR004): a nil *ValidationError becomes a non-nil error
}

func main() {
	if err := register("gopher"); err != nil {
		fmt.Println("rejected:", err.Error()) // MISS(NORULE): nil pointer dereference inside Error
	}
}
