package errshadowwin

func fail() error { return nil }

func use(error) {}

// Outer read after the inner scope: reported.
func readAfter(c bool) error {
	err := fail()
	if err != nil {
		return err
	}
	if c {
		err := fail() // want "ERR003"
		use(err)
	}
	return err
}

// Top-level overwrite of the outer err (not reading it) before any read: not reported.
func overwrittenBeforeRead(c bool) error {
	err := fail()
	if err != nil {
		return err
	}
	if c {
		err := fail()
		use(err)
	}
	err = fail()
	return err
}

// Overwrite whose right-hand side reads the outer err does not end the window: reported.
func overwrittenFromSelf(c bool) error {
	err := fail()
	if err != nil {
		return err
	}
	if c {
		err := fail() // want "ERR003"
		use(err)
	}
	err = wrap(err)
	return err
}

func wrap(err error) error { return err }

// Only a plain assignment to the outer err follows: not a read, not reported.
func assignedOnlyAfter(c bool) {
	var err error
	err = fail()
	if err != nil {
		return
	}
	if c {
		err := fail()
		use(err)
	}
	err = nil
}

// Named result read by a bare return: reported.
func namedResultBareReturn(c bool) (err error) {
	err = fail()
	if err != nil {
		return
	}
	if c {
		err := fail() // want "ERR003"
		use(err)
	}
	return
}

// Named result overwritten before the bare return: not reported.
func namedResultOverwritten(c bool) (err error) {
	err = fail()
	if err != nil {
		return
	}
	if c {
		err := fail()
		use(err)
	}
	err = fail()
	return
}

// Sibling scopes: each shadows the same outer err.
func siblings(a, b bool) error {
	err := fail()
	if err != nil {
		return err
	}
	if a {
		err := fail() // want "ERR003"
		use(err)
	}
	if b {
		err := fail() // want "ERR003"
		use(err)
	}
	return err
}
