package errexitdeferadvbreaks

// ft-8f3c adversary: functions that never return and never exit the process either (an infinite
// loop, a block that waits forever). The unmodified tip reports ERR005 on each deferred Close; the
// fix silences them although not every path ends in os.Exit or log.Fatal. The wants are the tip
// results, which the invariant says must stay.

import (
	"log"
	"net"
	"os"
	"runtime"
)

func serve()     {}
func cond() bool { return false }

func InfLoopServe(path string) { // exit-ish
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	_, _ = f.Write([]byte("x"))
	for {
		serve()
	}
}

func EmptyFor(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	for {
	}
}

func LoopOnlyExit(path string) { // loop whose only exit is os.Exit
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	for {
		if cond() {
			os.Exit(1)
		}
		serve()
	}
}

func RecvLoop(path string, c chan int) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	for {
		<-c
	}
}

func AcceptLoop(path string, l net.Listener) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}
		_ = c
	}
}

func GotoBackEdgeNoRet(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
top:
	serve()
	goto top
}

func DeferInLoopInf(path string) {
	for {
		f, err := os.Create(path)
		if err != nil {
			continue
		}
		defer f.Close() // want "ERR005"
		serve()
	}
}

func GoexitOnly(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	defer runtime.Goexit()
	for {
	}
}

func InGoroutineLoop(path string) {
	go func() {
		f, err := os.Create(path)
		if err != nil {
			return
		}
		defer f.Close() // want "ERR005"
		for {
			serve()
		}
	}()
}

func GenInf[T any](path string, x T) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	for {
		serve()
		_ = x
	}
}
