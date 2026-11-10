// Package er_shadow stress-tests ERR003: shadowed error declarations, overwritten unread errors
// across sequential/branching/loop control flow, named-return wipes, and nilerr bugs.
package er_shadow

import (
	"errors"
	"fmt"
)

func step1() error            { return errors.New("step1") }
func step2() error            { return errors.New("step2") }
func loadNum() (int, error)   { return 1, errors.New("load") }

// 1. Inner := shadows named error return and falls through, returning nil on failure.
func shadowNamedReturn(run bool) (n int, err error) {
	if run {
		v, err := loadNum() // HIT(ERR003)
		if err == nil {
			n = v
		}
	}
	return n, err
}

// 2. Inner := shadows outer local err variable that is returned afterward.
func shadowOuterLocal(run bool) error {
	var err error
	if run {
		_, err := loadNum() // HIT(ERR003)
		_ = err
	}
	return err
}

// 3. Sequential overwrite of unread error.
func sequentialOverwrite() error {
	err := step1()
	err = step2() // HIT(ERR003)
	return err
}

// 4. Conditional overwrite of unread error on one branch.
func branchOverwrite(both bool) error {
	err := step1()
	if both {
		err = step2() // HIT(ERR003)
	}
	return err
}

// 5. Named return assigned an error, then wiped by explicit `return nil` without observing err.
func namedReturnWiped() (err error) {
	err = step1()
	return nil // HIT(ERR003)
}

// 6. Classic nilerr bug: `if err != nil { return nil }` swallows a proven non-nil error.
func nilErrBug() error {
	err := step1()
	if err != nil {
		return nil // HIT(ERR003)
	}
	return nil
}

// 7. Loop overwrites unread err on subsequent iterations.
func loopOverwrite(n int) error {
	var err error
	for i := 0; i < n; i++ {
		err = step1() // HIT(ERR003)
	}
	return err
}

// --- Clean controls / FP probes (must produce zero diagnostics) ---

// 8. Clean: sequential steps where first error is checked before second assignment.
func sequentialChecked() error {
	err := step1()
	if err != nil {
		return err
	}
	err = step2()
	return err
}

// 9. Clean: retry/fallback pattern observes primary error via `if err != nil` before overwriting.
func retryOnFailure() error {
	err := step1()
	if err != nil {
		err = step2()
	}
	return err
}

// 10. Clean: scoped `if err := ...; err != nil { return err }` cannot leak error on fallthrough.
func scopedGuardedIf() error {
	var err error
	if _, err := loadNum(); err != nil {
		return fmt.Errorf("load failed: %w", err)
	}
	err = step2()
	return err
}

// 11. Clean: inner closure has its own independent function scope.
func closureOwnErr() error {
	err := step1()
	if err != nil {
		return err
	}
	fn := func() error {
		_, err := loadNum()
		return err
	}
	return fn()
}
