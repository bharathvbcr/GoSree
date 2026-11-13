package errretdom

import "errors"

func work() (*int, error) { return nil, errors.New("x") }

func use(*int) {}

// Recovering returns early via named results; a later branch has an ordinary return,
// and a closure assigns a captured pair after the defer.
func F(c bool) (n int, e error) {
	defer func() {
		if r := recover(); r != nil {
			e = errors.New("recovered")
		}
	}()
	var v *int
	var err error
	if c {
		return 1, nil
	}
	func() { v, err = work() }()
	if err != nil {
		return 0, err
	}
	use(v)
	return 2, nil
}

func cleanup() {}

// G has a defer, and so a Recover block, but the closure call dominates every normal return: the
// pair is tracked and use(v) is checked behind the nil test of err. Nothing is reported.
func G() int {
	defer cleanup()
	var v *int
	var err error
	func() { v, err = work() }()
	if err != nil {
		return 0
	}
	use(v)
	return 1
}
