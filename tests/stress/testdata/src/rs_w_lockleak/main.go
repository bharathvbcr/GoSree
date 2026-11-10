// WITNESS: panic
//
// A lock leaked on an early return: Withdraw returns false still holding a.mu, and the next call
// blocks forever. With no other goroutine alive the runtime reports the deadlock as a fatal error.
// Rust's MutexGuard unlocks on every path. gocap's SAFE009 tracks a must-lockset over this
// function for the guarded field, so it knows a.mu is held at the early return, and says nothing.
package main

import "sync"

type Account struct {
	mu      sync.Mutex
	balance int //safe:guarded_by mu
}

func (a *Account) Withdraw(n int) bool {
	a.mu.Lock()
	if a.balance < n {
		return false // MISS(NORULE): returns holding a.mu
	}
	a.balance -= n
	a.mu.Unlock()
	return true
}

func main() {
	a := &Account{balance: 10}
	a.Withdraw(100)
	a.Withdraw(1) // blocks forever: fatal error: all goroutines are asleep - deadlock!
}
