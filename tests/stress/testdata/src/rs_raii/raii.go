// Package rs_raii: Rust releases every resource in Drop, so a file, a lock guard, a timer or a
// thread handle is released on every path, early returns and panics included. Go relies on the
// programmer writing defer. gosree has no resource rule. go vet's lostcancel catches only a
// context cancel func that is never called; SAFE009's lockset tracks what is held for guarded
// memory, but nothing reports a function that returns still holding a lock.
package rs_raii

import (
	"context"
	"net/http"
	"os"
	"sync"
	"time"
)

// 1. an unclosed file on the error path.
func FirstByte(path string) (byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	b := make([]byte, 1)
	if _, err := f.Read(b); err != nil {
		return 0, err // MISS(NORULE): f leaks; Rust's File closes on drop
	}
	f.Close()
	return b[0], nil
}

// 2. an unclosed response body: the connection is never reused.
func Status(url string) (int, error) {
	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	return resp.StatusCode, nil // MISS(NORULE): resp.Body is never closed; Rust's Response drops it
}

// 3. a mutex left locked on an early return: the next Lock deadlocks (rs_w_lockleak runs it).
type Account struct {
	mu      sync.Mutex
	balance int //gosree:guarded_by mu
}

func (a *Account) Withdraw(n int) bool {
	a.mu.Lock()
	if a.balance < n {
		return false // MISS(NORULE): returns holding a.mu; Rust's MutexGuard unlocks on drop
	}
	a.balance -= n
	a.mu.Unlock()
	return true
}

// 4. a goroutine leak: nobody ever receives, the sender blocks forever.
func FirstOf(urls []string) string {
	ch := make(chan string)
	for _, u := range urls {
		go func() { ch <- u }() // MISS(NORULE): every sender but the first blocks forever; Rust leaks the thread too (a draw)
	}
	return <-ch
}

// 5. context cancel never called: vet lostcancel reports it.
func Deadline(parent context.Context) context.Context {
	ctx, _ := context.WithTimeout(parent, time.Second) // MISS(NORULE): the cancel func is dropped; vet lostcancel reports this
	return ctx
}

// 6. ticker never stopped (before Go 1.23 this leaked; now it is collected, but the goroutine
// below holds it forever).
func Poll(f func()) {
	t := time.NewTicker(time.Second) // MISS(NORULE): never stopped; Rust's Interval stops on drop
	go func() {                      // HIT(SAFE002)
		for range t.C {
			f()
		}
	}()
}
