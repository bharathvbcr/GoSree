package errexitdeferadv

// ft-8f3c adversary: every shape beside the card's own fixture. Where the fix removes ERR005 the
// function has no path that returns, panics or unwinds; the `_ret` partner is the same function with
// a way out, and keeps the finding. Shapes the classifier cannot see (a wrapper around os.Exit, a
// method value of a Logger, an interface Fatalf, syscall.Exit) keep the finding.

import (
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"syscall"
	"testing"
)

func serve()              {}
func die()                { os.Exit(1) }
func more() bool          { return false }
func cond() bool          { return false }
func logger() *log.Logger { return log.Default() }

type fataler interface{ Fatalf(string, ...any) }

type G[T any] struct{ f *os.File }

// ---- infinite loops / blocking ----

func InfLoopServe_ret(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	_, _ = f.Write([]byte("x"))
	for more() {
		serve()
	}
}

func EmptyFor_ret(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	for more() {
	}
}

func LoopOnlyExit_ret(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	for {
		if cond() {
			return
		}
		serve()
	}
}

func SelectEmpty(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	select {}
}

func SelectEmpty_ret(path string, c chan int) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	select {
	case <-c:
	}
}

func RecvLoop_ret(path string, c chan int) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	for range c {
	}
}

func SelectLoop(path string, c, d chan int) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	for {
		select {
		case <-c:
		case <-d:
		}
	}
}

func SelectLoop_ret(path string, c, d chan int) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	for {
		select {
		case <-c:
		case <-d:
			return
		}
	}
}

func AcceptLoop_ret(path string, l net.Listener) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		_ = c
	}
}

func LabelledBreak(path string) { // labelled break out of infinite loop to return
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
outer:
	for {
		for {
			if cond() {
				break outer
			}
		}
	}
}

func LabelledBreakExit(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
outer:
	for {
		for {
			if cond() {
				break outer
			}
		}
	}
	os.Exit(1)
}

func GotoBackEdge(path string) { // goto loop, exit to return
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
top:
	if cond() {
		goto top
	}
	return
}

func GotoThenExit(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
top:
	if cond() {
		goto top
	}
	os.Exit(1)
}

// ---- unknown terminators ----

func DieWrapper(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	die()
}

func DieWrapper_ret(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	serve()
}

func NewLoggerFatal(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	log.New(os.Stderr, "", 0).Fatal("x")
}

func IfaceFatalf(path string, t fataler) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	t.Fatalf("x")
}

func FuncValueFatal(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	g := log.Fatal
	g("x")
}

func FuncValueExit(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	exit := os.Exit
	exit(1)
}

func SyscallExit(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	syscall.Exit(1)
}

func LoggerMethodValue(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	g := logger().Fatal
	g("x")
}

func TestFatal(path string, t *testing.T) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	t.Fatal("x")
}

func TestFailNow(path string, t *testing.T) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	t.FailNow()
}

func TestFatalTB(path string, t testing.TB) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	t.Fatalf("x")
}

func TestSkipNow(path string, t *testing.T) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	t.SkipNow()
}

// ---- recover ----

func RecoverThenExit(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	defer func() { recover() }()
	os.Exit(1)
}

func RecoverPanic(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	defer func() { recover() }()
	panic("x")
}

func RecoverLoopPanic(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	defer func() { recover() }()
	for {
		serve()
		if cond() {
			panic(1)
		}
	}
}

// ---- defer placement ----

func DeferInIfExitElse(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	if cond() {
		defer f.Close()
		os.Exit(1)
	}
	serve()
}

func DeferInIfExitElse_ret(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	if cond() {
		defer f.Close() // want "ERR005"
		return
	}
	serve()
}

func DeferInIfExitAfter(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	if cond() {
		defer f.Close()
	}
	os.Exit(1)
}

func DeferInIfExitAfter_ret(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	if cond() {
		defer f.Close() // want "ERR005"
	}
}

func DeferInLoopExit(path string, n int) {
	for i := 0; i < n; i++ {
		f, err := os.Create(path)
		if err != nil {
			continue
		}
		defer f.Close()
	}
	os.Exit(1)
}

func DeferInLoopExit_ret(path string, n int) {
	for i := 0; i < n; i++ {
		f, err := os.Create(path)
		if err != nil {
			continue
		}
		defer f.Close() // want "ERR005"
	}
}

func DeferInLoopExitInside(path string, n int) {
	for i := 0; i < n; i++ {
		f, err := os.Create(path)
		if err != nil {
			continue
		}
		defer f.Close() // want "ERR005"
		if cond() {
			os.Exit(1)
		}
	}
}

func DeferInLoopExitInside_ret(path string, n int) {
	for i := 0; i < n; i++ {
		f, err := os.Create(path)
		if err != nil {
			continue
		}
		defer f.Close() // want "ERR005"
		if cond() {
			return
		}
	}
}

func TwoDefersExit(path, p2 string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	g, err := os.Create(p2)
	if err != nil {
		return
	}
	defer g.Close()
	os.Exit(1)
}

func TwoDefersExit_ret(path, p2 string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	g, err := os.Create(p2)
	if err != nil {
		return
	}
	defer g.Close() // want "ERR005"
}

func TwoDefersSecondBranchExit(path, p2 string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	if cond() {
		g, err := os.Create(p2)
		if err != nil {
			return
		}
		defer g.Close()
		os.Exit(1)
	}
}

func DeferThenExitBranchesWithReturnLater(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	if cond() {
		os.Exit(1)
	} else {
		log.Fatal("x")
	}
}

func SwitchAllExit(path string, n int) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	switch n {
	case 1:
		os.Exit(1)
	case 2:
		log.Fatal("x")
	default:
		os.Exit(2)
	}
}

func SwitchOneReturn(path string, n int) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	switch n {
	case 1:
		os.Exit(1)
	case 2:
		log.Fatal("x")
	default:
		return
	}
}

func SwitchOnePanic(path string, n int) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	switch n {
	case 1:
		os.Exit(1)
	default:
		panic("x")
	}
}

func SwitchOneGoexit(path string, n int) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	switch n {
	case 1:
		os.Exit(1)
	default:
		runtime.Goexit()
	}
}

func ExitThenReturnSameBlock(path string) { // exit then more code
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	os.Exit(1)
	serve()
}

func ExitBeforeDefer(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	if cond() {
		os.Exit(1)
	}
	defer f.Close() // want "ERR005"
}

func ExitBeforeDefer_ret(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	if cond() {
		return
	}
	defer f.Close() // want "ERR005"
}

func ExitEarlierInSameBlockThenDeferInLoop(path string, n int) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	for i := 0; i < n; i++ {
		defer f.Close()
	}
	os.Exit(1)
}

// ---- closures / goroutines ----

func ExitInCalledClosure(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	func() { os.Exit(1) }()
}

func ExitInCalledClosure_ret(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	func() { serve() }()
}

func ExitInGoroutine(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	go func() { os.Exit(1) }()
}

func ExitInGoroutineThenBlock(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	go func() { os.Exit(1) }()
	select {}
}

func ExitInDeferred(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	defer func() { os.Exit(1) }()
}

func ExitInDeferred_ret(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	defer func() { serve() }()
}

func GoexitInDeferred(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	defer func() { runtime.Goexit() }()
	os.Exit(1)
}

func InDeferClosureExit(path string) {
	defer func() {
		f, err := os.Create(path)
		if err != nil {
			return
		}
		defer f.Close()
		os.Exit(1)
	}()
}

func InDeferClosureExit_ret(path string) {
	defer func() {
		f, err := os.Create(path)
		if err != nil {
			return
		}
		defer f.Close() // want "ERR005"
	}()
}

func InClosureExit(path string) {
	func() {
		f, err := os.Create(path)
		if err != nil {
			return
		}
		defer f.Close()
		os.Exit(1)
	}()
}

func InClosureExit_ret(path string) {
	func() {
		f, err := os.Create(path)
		if err != nil {
			return
		}
		defer f.Close() // want "ERR005"
	}()
}

func InGoroutineExit(path string) {
	go func() {
		f, err := os.Create(path)
		if err != nil {
			return
		}
		defer f.Close()
		os.Exit(1)
	}()
}

func init() {
	f, err := os.Create("x")
	if err != nil {
		return
	}
	defer f.Close()
	log.Fatal("x")
}

func init() {
	f, err := os.Create("x")
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	serve()
}

// ---- generics ----

func GenExit[T io.Closer](path string, c T) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	defer c.Close()
	os.Exit(1)
}

func GenExit_ret[T io.Closer](path string, c T) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	defer c.Close()
}

func (g *G[T]) Run(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	os.Exit(1)
}

func (g *G[T]) Run_ret(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
}

// ---- control ----

func Exit(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	os.Exit(1)
}

func Exit_ret(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
}

func Fatal(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	log.Fatal("x")
}

func Panic(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	panic("x")
}
