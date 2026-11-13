// Package er_exitdefer stress-tests ERR005 on a deferred Close in a function that ends in a process
// exit. os.Exit and log.Fatal* skip deferred calls, so no Close runs and the finding is false;
// panic, runtime.Goexit and log.Panic* run them, and so does any path that returns normally.
// Card ft-8f3c. The process-exit shapes were ten false-positive markers and are clean since the fix:
// they carry no marker, as the harness asks of a fixed false positive.
package er_exitdefer

import (
	"log"
	"os"
	"runtime"
)

// ft-8f3c: os.Exit and log.Fatal* end the process, so a deferred Close never runs and ERR005 on it
// is a false positive. panic, runtime.Goexit and log.Panic* unwind, so the deferred Close runs and
// the finding stands; so does every function that can also return normally.

func exitAfterWrite(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte("x"))
	os.Exit(1)
}

func fatalAfterWrite(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte("x"))
	log.Fatal("done")
}

func fatalfAfterWrite(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte("x"))
	log.Fatalf("done %d", 1)
}

func fatallnAfterWrite(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte("x"))
	log.Fatalln("done")
}

func loggerFatalAfterWrite(path string, l *log.Logger) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte("x"))
	l.Fatal("done")
}

func loggerFatalfAfterWrite(path string, l *log.Logger) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte("x"))
	l.Fatalf("done %d", 1)
}

func loggerFatallnAfterWrite(path string, l *log.Logger) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte("x"))
	l.Fatalln("done")
}

func exitOnBothBranches(path string, bad bool) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte("x"))
	if bad {
		os.Exit(1)
	}
	log.Fatal("done")
}

func exitInLoop(path string, n int) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte("x"))
	for range n {
		_, _ = f.Write([]byte("y"))
	}
	os.Exit(1)
}

// No write at all: os.Create makes the resource writable when the Close is deferred.
func exitBeforeAnyWrite(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	os.Exit(1)
}

func panicAfterWrite(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	_, _ = f.Write([]byte("x"))
	panic("done")
}

func goexitAfterWrite(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	_, _ = f.Write([]byte("x"))
	runtime.Goexit()
}

func logPanicAfterWrite(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	_, _ = f.Write([]byte("x"))
	log.Panic("done")
}

func logPanicfAfterWrite(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	_, _ = f.Write([]byte("x"))
	log.Panicf("done %d", 1)
}

func logPaniclnAfterWrite(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	_, _ = f.Write([]byte("x"))
	log.Panicln("done")
}

func loggerPanicAfterWrite(path string, l *log.Logger) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	_, _ = f.Write([]byte("x"))
	l.Panic("done")
}

func exitOnOnePath(path string, bad bool) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	_, _ = f.Write([]byte("x"))
	if bad {
		os.Exit(1)
	}
}

func exitOrPanic(path string, bad bool) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	_, _ = f.Write([]byte("x"))
	if bad {
		os.Exit(1)
	}
	panic("done")
}

func fatalOrGoexit(path string, bad bool) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	_, _ = f.Write([]byte("x"))
	if bad {
		log.Fatal("done")
	}
	runtime.Goexit()
}

func exitInClosure(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	_, _ = f.Write([]byte("x"))
	func() {
		os.Exit(1)
	}()
}

func exitInGoroutine(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	_, _ = f.Write([]byte("x"))
	go func() {
		os.Exit(1)
	}()
}

func exitInDeferredClosure(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	_, _ = f.Write([]byte("x"))
	defer func() {
		os.Exit(1)
	}()
}

// The exit is on the error path of the write; the success path returns and runs the Close.
func exitOnWriteError(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // HIT(ERR005)
	if _, err := f.Write([]byte("x")); err != nil {
		os.Exit(1)
	}
}
