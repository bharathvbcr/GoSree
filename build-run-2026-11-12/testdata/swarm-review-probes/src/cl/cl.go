// Package cl: clone-modelling probes for SAFE002. Each send shares memory the caller does not own.
package cl

import (
	"maps"
	"slices"
)

type Item struct{ N int }

// C1: slices.Clone of pointers shares the pointees.
func C1(ch chan<- []*Item, b []*Item) { ch <- slices.Clone(b) }

// C2: maps.Clone of a map of pointers shares the pointees.
func C2(ch chan<- map[string]*Item, m map[string]*Item) { ch <- maps.Clone(m) }

// C3: append to a nil slice of pointers shares the pointees.
func C3(ch chan<- []*Item, b []*Item) { ch <- append([]*Item(nil), b...) }

// C4: append onto a parameter shares the parameter's backing array.
func C4(ch chan<- []byte, dst, b []byte) { ch <- append(dst, b...) }

// C5: append to b[:0:0] copies a byte slice; fine. Control.
func C5(ch chan<- []byte, b []byte) { ch <- append(b[:0:0], b...) }

// C6: maps.Clone of a map of slices shares the slices.
func C6(ch chan<- map[string][]byte, m map[string][]byte) { ch <- maps.Clone(m) }

// C7: slices.Clone of a slice of structs that hold pointers.
type Box struct{ P *Item }

func C7(ch chan<- []Box, b []Box) { ch <- slices.Clone(b) }

// C8: append([]byte(nil), b...) to a nil slice of a slice-of-slices.
func C8(ch chan<- [][]byte, b [][]byte) { ch <- append([][]byte(nil), b...) }

// C9: slices.Clone of a sub-slice of a parameter of pointers.
func C9(ch chan<- []*Item, b []*Item) { ch <- slices.Clone(b[1:]) }

// C10: maps.Clone then the clone is mutated by receiver; values are ints. Control (fine).
func C10(ch chan<- map[string]int, m map[string]int) { ch <- maps.Clone(m) }

// C11: slices.Clone of a string-keyed struct slice whose struct holds a map.
type Bag struct{ M map[string]int }

func C11(ch chan<- []Bag, b []Bag) { ch <- slices.Clone(b) }
