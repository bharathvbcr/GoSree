// Package im_shape attacks the imm shape check: field types the classifier accepts as
// shareable although they reach mutable memory, and correct imm types it rejects.
package im_shape

import (
	"bytes"
	"strings"
	"sync"
)

type counter struct{ n int }

func (c *counter) Error() string { return "e" }

// ErrHolder holds an error interface; the override table assumes error values are never mutated.
//
//gocap:imm
type ErrHolder struct {
	err error
}

// FuncHolder holds a func: classifier rejects funcs.
//
//gocap:imm
type FuncHolder struct { // HIT(SAFE005)
	f func() int
}

// ChanChan holds a channel of channels of pointers. Locked: the channel element check is applied
// to the nested channel too.
//
//gocap:imm
type ChanChan struct { // HIT(SAFE005)
	ch chan chan *counter
}

// Control: a channel of mutable pointers is rejected.
//
//gocap:imm
type ChanPtr struct { // HIT(SAFE005)
	ch chan *counter
}

// MuHolder holds a sync.Mutex value (pointer-free struct): accepted.
//
//gocap:imm
type MuHolder struct {
	mu sync.Mutex
}

// BufHolder holds a bytes.Buffer value, which contains a []byte.
//
//gocap:imm
type BufHolder struct { // HIT(SAFE005)
	b bytes.Buffer
}

// BuilderHolder holds a strings.Builder value, which contains a pointer and a []byte.
//
//gocap:imm
type BuilderHolder struct { // HIT(SAFE005)
	b strings.Builder
}

// Pair is a generic imm type whose type parameter is constrained to pointer-free types.
//
//gocap:imm
type Pair[T int | string] struct { // FP(SAFE005): T is constrained to int|string, so every instantiation is pointer-free; the classifier rejects every type parameter
	a, b T
}

// Box is a generic imm type over any T.
//
//gocap:imm
type Box[T any] struct { // HIT(SAFE005)
	v T
}

// Rec is recursive through a pointer to itself: accepted to fixpoint.
//
//gocap:imm
type Rec struct {
	next *Rec
	v    int
}

// NotStruct annotated imm: a named slice.
//
//gocap:imm
type Tags []string // HIT(SAFE005)

func errMutate(h *ErrHolder) {
	h.err.(*counter).n++ // MISS(SAFE005): the error override lets an imm value reach a *counter that is then written; kinds.go overrides "error" is assumed, not checked (witness: im_w_errfield)
}

func useAll(c *ChanChan, f *FuncHolder, p *ChanPtr, m *MuHolder, b *BufHolder, s *BuilderHolder, x *Pair[int], y *Box[int], r *Rec, t Tags) {
	_, _, _, _, _, _, _, _, _, _ = c, f, p, m, b, s, x, y, r, t
}
