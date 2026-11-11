package errrangefunc

import (
	"iter"
	"os"
)

func fail() error { return nil }

func pair() (*os.File, error) { return nil, nil }

type item struct{ v int }

func fetch() (*item, error) { return nil, nil }

func seq(n int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := range n {
			if !yield(i) {
				return
			}
		}
	}
}

func seq2() iter.Seq2[int, int] {
	return func(yield func(int, int) bool) { yield(0, 0) }
}

// ERR001 inside the yield body.
func rangeFuncErr001() {
	for x := range seq(3) {
		fail() // want "ERR001"
		_ = x
	}
}

// ERR005 inside the yield body.
func rangeFuncErr005(names []string) {
	for x := range seq(3) {
		f, err := os.Create(names[x])
		if err != nil {
			continue
		}
		_, _ = f.WriteString("x")
		f.Close() // want "ERR005"
	}
}

// ERR002 blanked inside the yield body.
func rangeFuncErr002Blanked() int {
	total := 0
	for x := range seq(3) {
		it, _ := fetch() // want "ERR002"
		total += it.v + x
	}
	return total
}

// ERR003 shadow inside the yield body.
func rangeFuncErr003Shadow() error {
	err := fail()
	if err != nil {
		return err
	}
	for x := range seq(3) {
		if x > 1 {
			err := fail() // want "ERR003"
			if err != nil {
				_ = x
			}
		}
	}
	return err
}

// ERR003 shadow where the outer error lives inside the yield body.
func rangeFuncErr003ShadowInBody() {
	for x := range seq(3) {
		err := fail()
		if err != nil {
			continue
		}
		if x > 1 {
			err := fail() // want "ERR003"
			_ = err
		}
		_ = err
	}
}

// Nested range-over-func: both levels are scanned.
func rangeFuncNested() {
	for x := range seq(2) {
		for k, v := range seq2() {
			fail() // want "ERR001"
			_, _, _ = x, k, v
		}
		fail() // want "ERR001"
	}
}

// Range-over-func inside a closure.
func rangeFuncInClosure() {
	f := func() {
		for x := range seq(2) {
			fail() // want "ERR001"
			_ = x
		}
	}
	f()
}

// Closure inside a range-over-func body.
func closureInRangeFunc() {
	for x := range seq(2) {
		g := func() {
			fail() // want "ERR001"
		}
		g()
		_ = x
	}
}

// Clean: handled errors inside range-over-func produce nothing.
func rangeFuncClean() error {
	for x := range seq(3) {
		if err := fail(); err != nil {
			return err
		}
		_ = x
	}
	return nil
}
