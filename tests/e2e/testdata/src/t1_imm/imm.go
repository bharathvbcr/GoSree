package t1_imm

// TC-T1-7.1: //gosree:imm struct with primitive types is verified immutable (Clean Pass)
//
//gosree:imm
type ServerConfig struct { // want ServerConfig:"imm"
	host string
	port int
}

func newServerConfig(h string, p int) *ServerConfig {
	return &ServerConfig{host: h, port: p}
}

// TC-T1-7.2: //gosree:imm struct with mutable slice rejected (SAFE005)
//
//gosree:imm
type BadSliceConfig struct { // want `SAFE005: //gosree:imm type BadSliceConfig is not immutable: field items has type \[\]string`
	items []string
}

// TC-T1-7.3: Post-construction field write to immutable struct rejected (SAFE005)
func writeImmField(c *ServerConfig) {
	c.port = 9090 // want `SAFE005: write to a field of ServerConfig`
}

// TC-T1-7.4: Overwrite through *ServerConfig rejected (SAFE005)
func overwriteImm(c *ServerConfig) {
	*c = ServerConfig{host: "0.0.0.0", port: 80} // want `SAFE005: store through a \*ServerConfig overwrites an immutable value`
}

// TC-T1-7.5: Verified //gosree:imm shared freely across goroutines without move invalidation (Clean Pass)
func shareImm(ch chan *ServerConfig) string {
	c := newServerConfig("production.local", 443)
	ch <- c
	go func() {
		_ = c.port
	}()
	return c.host // Clean Pass: immutable values can be read concurrently
}

// TC-T1-7.6: //gosree:imm struct with mutable channel rejected (SAFE005)
//
//gosree:imm
type BadChanConfig struct { // want `SAFE005: //gosree:imm type BadChanConfig is not immutable: field ch has type chan \*int`
	ch chan *int
}

// TC-T1-7.7: //gosree:imm struct with array of mutable channel rejected (SAFE005)
//
//gosree:imm
type BadArrayChanConfig struct { // want `SAFE005: //gosree:imm type BadArrayChanConfig is not immutable: field chs has type \[2\]chan \*int`
	chs [2]chan *int
}

// TC-T1-7.8: Mutating //gosree:imm field via helper pointer call rejected (SAFE005)
func writeIntPtr(p *int) {
	*p = 8080
}

func mutateImmFieldViaCall(c *ServerConfig) {
	writeIntPtr(&c.port) // want `SAFE005: write to a field of ServerConfig, which is //gosree:imm; imm values are only written by their composite literal`
}
