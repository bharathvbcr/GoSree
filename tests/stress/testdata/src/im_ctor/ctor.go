// Package im_ctor probes where "construction" of an imm value ends, and the idiomatic
// copy-on-write patterns on imm values.
package im_ctor

// Config is a valid imm type.
//
//gosree:imm
type Config struct {
	port int
	name string
}

// FP probe: a value-receiver With-method that edits its own copy and returns it is the
// idiomatic way to derive a new immutable value; no shared memory is written.
func (c Config) WithPort(p int) Config {
	c.port = p // FP(SAFE005): c is the receiver's private copy, never shared; the With-copy idiom is race-free (reported twice: address taken, and field write)
	return c
}

// FP probe: copying a shared value into a local and editing the local.
func derive(src *Config) Config {
	c := *src
	c.name = "derived" // FP(SAFE005): c is a local copy of *src; nothing shared is written
	return c
}

// Control: building through a composite literal is the documented way.
func build(p int) *Config {
	return &Config{port: p, name: "a"}
}

// Control: new then field writes is reported (construction is the literal only).
func viaNew() *Config {
	c := new(Config)
	c.port = 1 // HIT(SAFE005)
	return c
}

// Attack: a local imm variable captured by a goroutine and then reassigned. The address is taken
// by the closure (allowed) and the local reassignment is allowed, so the goroutine's read races.
func capturedReassign() {
	c := Config{port: 1}
	done := make(chan int)
	go func() { done <- c.port }()
	c = Config{port: 2} // MISS(SAFE005): c is captured by reference by a running goroutine; reassigning it writes shared imm memory
	<-done
	_ = c
}

// Control: taking the address explicitly is caught.
func addrTaken() *Config {
	c := Config{port: 1}
	return &c // HIT(SAFE005) HIT(BORROW004)
}
