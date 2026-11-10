// Package er_mustuse stress-tests ERR001 (Rust #[must_use] Result discipline) across direct calls,
// goroutines, defers, generics, closures, method values, and interfaces, plus false-positive probes.
package er_mustuse

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Runner interface {
	Run() error
}

type flusher struct{}

func (flusher) Flush() error { return errors.New("flush failed") }

func fallible() error { return errors.New("boom") }

func falliblePair() (int, error) { return 42, errors.New("boom") }

func genericFallible[T any](v T) error {
	_ = v
	return errors.New("generic boom")
}

func multiErr() (error, error) {
	return errors.New("e1"), errors.New("e2")
}

// 1. Bare statement call to error-returning function.
func bareCall() {
	fallible() // HIT(ERR001)
}

// 2. Bare statement call to (T, error) function.
func barePairCall() {
	falliblePair() // HIT(ERR001)
}

// 3. Goroutine spawn silently drops returned error.
func goDropsError() {
	go fallible() // HIT(ERR001)
}

// 4. Deferred call silently drops returned error.
func deferDropsError() {
	defer fallible() // HIT(ERR001)
}

// 5. Generic instantiation returning error dropped as statement.
func genericDrop() {
	genericFallible("payload") // HIT(ERR001)
}

// 6. Closure returning error dropped as statement.
func closureDrop() {
	fn := func() error { return errors.New("closure err") }
	fn() // HIT(ERR001)
}

// 7. Method value returning error dropped as statement.
func methodValueDrop(f flusher) {
	m := f.Flush
	m() // HIT(ERR001)
}

// 8. Interface method returning error dropped as statement.
func interfaceDrop(r Runner) {
	r.Run() // HIT(ERR001)
}

// 9. Multi-error return dropped completely.
func multiErrBare() {
	multiErr() // HIT(ERR001)
}

// --- Benign controls / FP probes (must produce zero diagnostics) ---

func explicitBlankDiscard() {
	_ = fallible()
	_, _ = falliblePair()
}

func benignStdlibWriters() {
	fmt.Println("hello", 123)
	fmt.Printf("val=%d\n", 42)
	fmt.Fprintln(os.Stdout, "stdout")

	var buf bytes.Buffer
	buf.Write([]byte("abc"))
	buf.WriteString("def")
	buf.WriteByte('g')

	var sb strings.Builder
	sb.WriteString("xyz")
	sb.WriteRune('!')

	h := sha256.New()
	h.Write([]byte("digest"))
	_ = h.Sum(nil)
}

func handledProperly() error {
	if err := fallible(); err != nil {
		return err
	}
	v, err := falliblePair()
	if err != nil {
		return err
	}
	_ = v
	return nil
}
