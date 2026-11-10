// Package im_alias attacks SAFE005 through aliases of imm memory that the store walk can lose: the
// write happens somewhere the root of the address is no longer visibly an imm type.
package im_alias

import (
	"fmt"
	"sync/atomic"
)

// Config is a valid imm type: unexported pointer-free fields only.
//
//gocap:imm
type Config struct {
	port int
	n    int64
	arr  [4]int
	in   inner
}

// inner is an ordinary (non-imm) pointer-free struct held by value in Config.
type inner struct{ hits int }

func (i *inner) bump() { i.hits++ }

func setInt(p *int, v int) { *p = v }

func set[T any](p *T, v T) { *p = v }

// Control: the direct field write is caught.
func directWrite(c *Config) {
	c.port = 1 // HIT(SAFE005)
}

// Control: a local alias of the field address is the same SSA value, so it is caught.
func localAlias(c *Config) {
	p := &c.port
	*p = 1 // HIT(SAFE005)
}

// Locked: the field address handed straight to a helper call is caught (witness: im_w_helper).
func helperWrite(c *Config) {
	setInt(&c.port, 1) // HIT(SAFE005)
}

// Locked: same, through a generic setter.
func genericWrite(c *Config) {
	set(&c.port, 2) // HIT(SAFE005)
}

// Locked: a pointer-receiver method of a non-imm field called on the imm value.
func promotedMethod(c *Config) {
	c.in.bump() // HIT(SAFE005)
}

// Control: the same field written in place is caught.
func promotedInPlace(c *Config) {
	c.in.hits++ // HIT(SAFE005)
}

// Attack: slicing an array field gives a []int whose element stores are traced back
// (witness: im_w_slice).
func sliceArray(c *Config) {
	s := c.arr[:]
	s[0] = 1 // HIT(SAFE005): c.arr[:] aliases imm memory
}

// Control: indexing the array field in place is caught.
func indexArray(c *Config) {
	c.arr[0] = 1 // HIT(SAFE005)
}

// Attack: the builtin copy into a slice of the array field.
func copyIntoArray(c *Config) {
	copy(c.arr[:], []int{1, 2}) // HIT(SAFE005): copy writes the imm array through c.arr[:]
}

// Locked: atomic add on a field of an imm value.
func atomicAdd(c *Config) {
	atomic.AddInt64(&c.n, 1) // HIT(SAFE005)
}

// Attack: a variadic ...any callee receives the field address boxed in an interface inside a slice.
func sscan(c *Config) {
	_, _ = fmt.Sscan("7", &c.port) // HIT(SAFE005): Sscan stores through &c.port
}

// Locked: whole-value overwrite of the embedded non-imm struct through its address.
func overwriteInner(c *Config) {
	resetInner(&c.in) // HIT(SAFE005)
}

func resetInner(i *inner) { *i = inner{} }

// portAddr returns an interior pointer into an imm value.
func (c *Config) portAddr() *int { return &c.port }

// Attack: write through an interior pointer returned by an accessor (witness: im_w_accessor).
func viaAccessor(c *Config) {
	*c.portAddr() = 9 // HIT(SAFE005): writes c.port through the *int that portAddr returned
}

// Attack: the field address is parked in a slice before reaching the helper.
func viaSlot(c *Config) {
	ps := []*int{&c.port}
	setInt(ps[0], 3) // HIT(SAFE005): &c.port reaches setInt through a slice element
}

// Control: overwriting the embedded struct in place is caught.
func overwriteInnerInPlace(c *Config) {
	c.in = inner{} // HIT(SAFE005)
}
