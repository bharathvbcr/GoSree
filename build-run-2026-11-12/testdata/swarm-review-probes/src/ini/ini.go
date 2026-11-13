// Package ini: init soundness probes.
package ini

type Cfg struct{ N int }

var cfg = &Cfg{}

var counter int

func init() {
	cfg.N = 1
	go func() { cfg.N = 2 }() // goroutine started by init writes the package variable
	counter = 5
}

// Read is called after init, concurrently with the goroutine init started.
func Read() int { return cfg.N }

// Bump writes a package variable outside init.
func Bump() { counter++ }
