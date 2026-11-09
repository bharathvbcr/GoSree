package imm

// Config is shared read-only between goroutines.
//
//safe:imm
type Config struct { // want Config:"imm"
	name   string
	port   int
	parent *Config
}

func newConfig(n string, parent *Config) *Config { return &Config{name: n, port: 80, parent: parent} }

// An imm value can be sent and still used: nobody can write it.
func share(ch chan *Config) string {
	c := newConfig("a", nil)
	ch <- c
	go func() { _ = c.port }()
	return c.name
}

func (c *Config) setPort(p int) { c.port = p } // want `SAFE005: write to a field of Config`

func overwrite(c *Config) { *c = Config{name: "z"} } // want `SAFE005: store through a \*Config overwrites an immutable value`

func addr() *Config {
	var c Config
	return &c // want `SAFE005: taking the address of variable c`
}

func mutateAfterBuild() *Config {
	c := &Config{name: "a"}
	c.port = 1 // want `SAFE005: write to a field of Config`
	return c
}

// Bad holds a slice, which another goroutine could still mutate.
//
//safe:imm
type Bad struct { // want `SAFE005: //safe:imm type Bad is not immutable: field names has type \[\]string`
	names []string
}

// Exported fields can be written by other packages.
//
//safe:imm
type Open struct { // want `SAFE005: //safe:imm type Open is not immutable: field Port is exported`
	Port int
}

// A rejected imm type gets no sharing privileges.
func sendBad(ch chan *Bad, b *Bad) {
	ch <- b // want `SAFE002: parameter b is sent on a channel but is not owned here`
}
