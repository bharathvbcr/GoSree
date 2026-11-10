// Package im_launder attacks SAFE005 through the type system: plain Go conversions and
// declarations that strip the imm annotation from the memory being written.
package im_launder

// Config is a valid imm type.
//
//gocap:imm
type Config struct {
	port int
	name string
}

// Mutable has Config's underlying type and no annotation.
type Mutable Config

// Twin is a separately declared struct with identical fields.
type Twin struct {
	port int
	name string
}

// Alias is a type alias of Config, so it is the same type.
type Alias = Config

// Attack: a pointer conversion to a defined type with the same underlying struct.
func convertDefined(c *Config) {
	(*Mutable)(c).port = 1 // HIT(SAFE005): (*Mutable)(c) converts pointer to same underlying imm memory
}

// Attack: conversion to an unrelated struct type with identical fields.
func convertTwin(c *Config) {
	(*Twin)(c).port = 1 // HIT(SAFE005): *Config converts to *Twin (identical underlying types)
}

// Attack: whole-value overwrite through the converted pointer.
func convertOverwrite(c *Config) {
	*(*Mutable)(c) = Mutable{} // HIT(SAFE005): store through *Mutable overwrites the imm value
}

// Attack: conversion kept in a variable first.
func convertViaVar(c *Config) {
	m := (*Mutable)(c)
	m.name = "x" // HIT(SAFE005): m aliases c
}

// Control: an alias is the same type, so the write is caught.
func viaAlias(c *Alias) {
	c.port = 1 // HIT(SAFE005)
}

// Control: a value conversion copies, so writing the copy is fine.
func valueConversion(c *Config) Mutable {
	m := Mutable(*c)
	m.port = 1
	return m
}

// Control: a value-receiver method returning a modified copy is legal.
func (c Config) WithPort(p int) Config {
	return Config{port: p, name: c.name}
}

func useWith(c *Config) int { return c.WithPort(3).port }
