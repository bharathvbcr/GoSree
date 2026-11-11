// Package recursive_types holds types that refer to themselves through a
// slice, map, array, channel or struct field, with no pointer in the cycle.
// The storage checker asks whether each captured value can hold a borrow; it
// must terminate on these and report nothing, because no value here holds one.
package recursive_types

type SliceRec []SliceRec

type MapRec map[string]MapRec

type ChanRec chan ChanRec

type Tree struct {
	Kids []Tree
}

type Forest struct {
	Trees [2][]Forest
	Index map[int]Forest
}

func captureAll() {
	var s SliceRec
	var m MapRec
	var c ChanRec
	var t Tree
	var f Forest
	g := func() {
		_ = s
		_ = m
		_ = c
		_ = t
		_ = f
	}
	g()
}
