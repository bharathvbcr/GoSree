package ownership_test

import (
	"strings"
	"testing"
)

// A call of a variable that may hold one of several functions. What the lock check takes from the
// call has to hold for whichever one runs: a lock is held afterwards only if every one of them
// takes it, and is no longer held if any one of them releases it. The check used to look at one
// closure the variable may hold and skip a plain function altogether, so "locks, or does nothing"
// counted as locking and the unguarded write after it was not reported.
const lockTargetsHead = `package p

import (
	"runtime"
	"sync"
)

var _ = runtime.Gosched

var hook func()

type S struct {
	mu sync.Mutex
	//gosree:guarded_by mu
	n int
}

func set(p *func()) { *p = func() {} }

func pick() func() { return func() {} }

func (s *S) unlockIt() { s.mu.Unlock() }

func (s *S) lockIt() { s.mu.Lock() }

func (s *S) lockBound() {
	f := s.mu.Lock
	f()
}

func (s *S) maybeUnlockBound(c bool) {
	f := func() {}
	if c {
		f = s.mu.Unlock
	}
	f()
}

func (s *S) maybeUnlockMethod(c bool) {
	f := func() {}
	if c {
		f = s.unlockIt
	}
	f()
}

func (s *S) maybeUnlock(c bool) {
	f := func() {}
	if c {
		f = func() { s.mu.Unlock() }
	}
	f()
}
`

func lockTargetsDiags(t *testing.T, body string) []string {
	t.Helper()
	return advDiagLines(t, lockTargetsHead+"\nfunc (s *S) m(c bool) {\n\t"+body+"\n}\n", "SAFE009")
}

func TestLockTakenByOneOfSeveralCalleesIsNotHeld(t *testing.T) {
	for name, body := range map[string]string{
		"locks, or does nothing":                 "f := func() { s.mu.Lock() }\n\tif c {\n\t\tf = func() {}\n\t}\n\tf()\n\ts.n++",
		"does nothing, or locks":                 "f := func() {}\n\tif c {\n\t\tf = func() { s.mu.Lock() }\n\t}\n\tf()\n\ts.n++",
		"locks, or reads the field":              "f := func() { s.mu.Lock() }\n\tif c {\n\t\tf = func() { _ = s }\n\t}\n\tf()\n\ts.n++",
		"held, then unlocks or does nothing":     "f := func() {}\n\tif c {\n\t\tf = func() { s.mu.Unlock() }\n\t}\n\ts.mu.Lock()\n\tf()\n\ts.n++",
		"held, then does nothing or unlocks":     "f := func() { s.mu.Unlock() }\n\tif c {\n\t\tf = func() {}\n\t}\n\ts.mu.Lock()\n\tf()\n\ts.n++",
		"held, then one of two closures unlocks": "f := func() { _ = s }\n\tif c {\n\t\tf = func() { s.mu.Unlock() }\n\t}\n\ts.mu.Lock()\n\tf()\n\ts.n++",
		// The variable is in a cell: another closure captures it.
		"a cell: locks, or does nothing":                   "var f func()\n\tf = func() { s.mu.Lock() }\n\tif c {\n\t\tf = func() {}\n\t}\n\tg := func() { f() }\n\t_ = g\n\tf()\n\ts.n++",
		"a cell: held, then does nothing or unlocks":       "var f func()\n\tf = func() {}\n\tif c {\n\t\tf = func() { s.mu.Unlock() }\n\t}\n\tg := func() { f() }\n\t_ = g\n\ts.mu.Lock()\n\tf()\n\ts.n++",
		"a cell another closure overwrites: locks, or not": "f := func() { s.mu.Lock() }\n\treset := func() { f = func() {} }\n\tif c {\n\t\treset()\n\t}\n\tf()\n\ts.n++",
		"a cell whose address is handed on: locks, or not": "f := func() { s.mu.Lock() }\n\tset(&f)\n\tf()\n\ts.n++",
		"held, then a helper calls a closure that unlocks": "s.mu.Lock()\n\ts.maybeUnlock(c)\n\ts.n++",
		"written out: locks on one path":                   "if c {\n\t\ts.mu.Lock()\n\t}\n\ts.n++",
	} {
		got := lockTargetsDiags(t, body)
		if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "without holding it") {
			t.Errorf("%s: n is written without mu held on some path, and it was not reported; got %v", name, got)
		}
	}
}

// What the fix must not take away: when every function the variable may hold takes the lock, it
// is held afterwards; a single closure is as before, also one that another closure only calls.
func TestLockTakenByEveryCalleeIsHeld(t *testing.T) {
	for name, body := range map[string]string{
		"one closure that locks":                           "f := func() { s.mu.Lock() }\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"two closures that both lock":                      "f := func() { s.mu.Lock() }\n\tif c {\n\t\tf = func() { s.mu.Lock(); _ = c }\n\t}\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"held, and neither unlocks":                        "f := func() { _ = s }\n\tif c {\n\t\tf = func() {}\n\t}\n\ts.mu.Lock()\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"a cell: one closure that locks, called by others": "var f func()\n\tf = func() { s.mu.Lock() }\n\tg := func() { f(); s.mu.Unlock() }\n\t_ = g\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
	} {
		if got := lockTargetsDiags(t, body); len(got) != 0 {
			t.Errorf("%s: mu is held where n is written, and it was reported: %v", name, got)
		}
	}
}

// One of the things the variable may hold is a mutex method bound to its mutex (f = s.mu.Unlock),
// a method of this package bound to its receiver (f = s.unlockIt), or a function of another
// package. Each of them gives its own part: what any one releases is released, whatever the
// others are, and a lock is held afterwards only if each of them takes it.
func TestLockMethodValueAmongSeveralCallees(t *testing.T) {
	for name, body := range map[string]string{
		"held; nothing, or mu.Unlock":                 "s.mu.Lock()\n\tf := func() {}\n\tif c {\n\t\tf = s.mu.Unlock\n\t}\n\tf()\n\ts.n++",
		"held; mu.Unlock, or nothing":                 "s.mu.Lock()\n\tf := s.mu.Unlock\n\tif c {\n\t\tf = func() {}\n\t}\n\tf()\n\ts.n++",
		"held; a closure that unlocks, or Gosched":    "s.mu.Lock()\n\tf := func() { s.mu.Unlock() }\n\tif c {\n\t\tf = runtime.Gosched\n\t}\n\tf()\n\ts.n++",
		"held; Gosched, or a closure that unlocks":    "s.mu.Lock()\n\tf := runtime.Gosched\n\tif c {\n\t\tf = func() { s.mu.Unlock() }\n\t}\n\tf()\n\ts.n++",
		"held; nothing, or an interface's Unlock":     "var l sync.Locker = &s.mu\n\tf := func() {}\n\tif c {\n\t\tf = l.Unlock\n\t}\n\ts.mu.Lock()\n\tf()\n\ts.n++",
		"locks, or what a call returns":               "f := func() { s.mu.Lock() }\n\tif c {\n\t\tf = pick()\n\t}\n\tf()\n\ts.n++",
		"held; mu.Unlock alone":                       "s.mu.Lock()\n\tf := s.mu.Unlock\n\tf()\n\ts.n++",
		"nothing, or mu.Lock":                         "f := func() {}\n\tif c {\n\t\tf = s.mu.Lock\n\t}\n\tf()\n\ts.n++",
		"held; s.unlockIt alone":                      "s.mu.Lock()\n\tf := s.unlockIt\n\tf()\n\ts.n++",
		"held; nothing, or s.unlockIt":                "s.mu.Lock()\n\tf := func() {}\n\tif c {\n\t\tf = s.unlockIt\n\t}\n\tf()\n\ts.n++",
		"nothing, or s.lockIt":                        "f := func() {}\n\tif c {\n\t\tf = s.lockIt\n\t}\n\tf()\n\ts.n++",
		"held; a helper that may call mu.Unlock":      "s.mu.Lock()\n\ts.maybeUnlockBound(c)\n\ts.n++",
		"held; a helper that may call s.unlockIt":     "s.mu.Lock()\n\ts.maybeUnlockMethod(c)\n\ts.n++",
		"held; a closure that unlocks, or a hook":     "s.mu.Lock()\n\tf := func() { s.mu.Unlock() }\n\tif c {\n\t\tf = hook\n\t}\n\tf()\n\ts.n++",
		"held; a goroutine of nothing, or mu.Unlock":  "s.mu.Lock()\n\tf := func() {}\n\tif c {\n\t\tf = s.mu.Unlock\n\t}\n\tgo f()\n\ts.n++",
		"held; a goroutine of nothing, or s.unlockIt": "s.mu.Lock()\n\tf := func() {}\n\tif c {\n\t\tf = s.unlockIt\n\t}\n\tgo f()\n\ts.n++",
		"mu.Lock, or a hook":                          "f := s.mu.Lock\n\tif c {\n\t\tf = hook\n\t}\n\tf()\n\ts.n++",
	} {
		got := lockTargetsDiags(t, body)
		if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "without holding it") {
			t.Errorf("%s: n is written without mu held on some path, and it was not reported; got %v", name, got)
		}
	}
	for name, body := range map[string]string{
		"mu.Lock, or a closure that locks":               "f := s.mu.Lock\n\tif c {\n\t\tf = func() { s.mu.Lock() }\n\t}\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"a helper that locks through mu.Lock as a value": "s.lockBound()\n\ts.n++\n\ts.mu.Unlock()",
		"mu.Lock alone":                           "f := s.mu.Lock\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"s.lockIt alone":                          "f := s.lockIt\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"s.lockIt, or mu.Lock":                    "f := s.lockIt\n\tif c {\n\t\tf = s.mu.Lock\n\t}\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"held; Gosched, or a closure that reads":  "s.mu.Lock()\n\tf := runtime.Gosched\n\tif c {\n\t\tf = func() { _ = s }\n\t}\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"held; nothing, or a hook handed nothing": "s.mu.Lock()\n\tf := func() {}\n\tif c {\n\t\tf = hook\n\t}\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
	} {
		if got := lockTargetsDiags(t, body); len(got) != 0 {
			t.Errorf("%s: mu is held where n is written, and it was reported: %v", name, got)
		}
	}
}

// A //gosree:holds helper must not unlock its mutex, also not through one of several functions a
// variable may hold.
func TestLockHoldsHelperUnlockingThroughOneOfSeveral(t *testing.T) {
	holds := func(body string) []string {
		return advDiagLines(t, lockTargetsHead+"\n//gosree:holds mu\nfunc (s *S) h(c bool) {\n\t"+body+"\n}\n", "SAFE007")
	}
	for name, body := range map[string]string{
		"nothing, or mu.Unlock":  "f := func() {}\n\tif c {\n\t\tf = s.mu.Unlock\n\t}\n\tf()",
		"mu.Unlock, or nothing":  "f := s.mu.Unlock\n\tif c {\n\t\tf = func() {}\n\t}\n\tf()",
		"mu.Unlock alone":        "f := s.mu.Unlock\n\tf()",
		"nothing, or s.unlockIt": "f := func() {}\n\tif c {\n\t\tf = s.unlockIt\n\t}\n\tf()",
		"s.unlockIt alone":       "f := s.unlockIt\n\tf()",
		"s.unlockIt called":      "s.unlockIt()",
	} {
		if got := holds(body); len(got) == 0 {
			t.Errorf("%s: the helper may unlock the mutex it is declared to hold, and it was not reported", name)
		}
	}
	for name, body := range map[string]string{
		"nothing, or a closure that reads": "f := func() {}\n\tif c {\n\t\tf = func() { _ = s }\n\t}\n\tf()",
		"nothing, or Gosched":              "f := func() {}\n\tif c {\n\t\tf = runtime.Gosched\n\t}\n\tf()",
	} {
		if got := holds(body); len(got) != 0 {
			t.Errorf("%s: the helper does not unlock its mutex, and it was reported: %v", name, got)
		}
	}
}

// Pinned, not wanted: a //gosree:holds helper that unlocks its mutex inside a closure is not
// reported. The closure sees the receiver through the cell it captures, and the helper's own
// mutexes are kept as paths from its parameter, so the two are not matched.
func TestLockHoldsHelperUnlockingInAClosureIsAKnownMiss(t *testing.T) {
	got := advDiagLines(t, lockTargetsHead+"\n//gosree:holds mu\nfunc (s *S) h(c bool) {\n\tf := func() { s.mu.Unlock() }\n\tf()\n}\n", "SAFE007")
	if len(got) != 0 {
		t.Fatalf("the known miss is reported now; make this a must-report case: %v", got)
	}
}

const lockAddressExtra = `
var keepF *func()

func peek(p *func()) bool { return *p != nil }

func (s *S) setLock(p *func()) { *p = s.lockIt }

func (s *S) setUnlock(p *func()) { *p = s.unlockIt }

func (s *S) setUnlockClosure(p *func()) { *p = func() { s.mu.Unlock() } }

func (s *S) setUnlockBound(p *func()) { *p = s.mu.Unlock }

func setUnlockOf(p *func(), t *S) { *p = t.unlockIt }

func (s *S) setVia(p *func()) { s.setUnlock(p) }

func setRec(p *func(), t *S, n int) {
	if n > 0 {
		setRec(p, t, n-1)
		return
	}
	*p = t.unlockIt
}

var keepS *S

func setUnlockKept(p *func()) { *p = keepS.unlockIt }
`

func lockAddressDiags(t *testing.T, body string) []string {
	t.Helper()
	return advDiagLines(t, lockTargetsHead+lockAddressExtra+"\nfunc (s *S) m(c bool) {\n\t"+body+"\n}\n", "SAFE009")
}

// A function variable whose address is passed to a function of this package is read in that
// function, so what it may hold is known there too, where it used to be "not known" as soon as
// the address was taken. A lock is held after the call only if every function the variable may
// hold takes it; a helper that only reads the variable leaves the one function it holds.
func TestLockThroughAVariableWhoseAddressIsPassedOn(t *testing.T) {
	for name, body := range map[string]string{
		"the helper stores a function that does not lock":   "f := func() { s.mu.Lock() }\n\tset(&f)\n\tf()\n\ts.n++",
		"the helper stores a method that locks its own s":   "f := func() { s.mu.Lock() }\n\ts.setLock(&f)\n\tf()\n\ts.n++",
		"the address is kept where anything may write it":   "f := func() { s.mu.Lock() }\n\tkeepF = &f\n\tf()\n\ts.n++",
		"nothing locks, the helper only reads the variable": "f := func() {}\n\t_ = peek(&f)\n\tf()\n\ts.n++",
	} {
		if got := lockAddressDiags(t, body); len(got) == 0 {
			t.Errorf("%s: the lock is not known to be held when n is written, and that was not reported", name)
		}
	}
	for name, body := range map[string]string{
		"the helper only reads the variable":           "f := func() { s.mu.Lock() }\n\t_ = peek(&f)\n\tf()\n\ts.n++",
		"held before, the helper only reads, unlocked": "s.mu.Lock()\n\tf := func() {}\n\t_ = peek(&f)\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
	} {
		if got := lockAddressDiags(t, body); len(got) != 0 {
			t.Errorf("%s: the lock is held when n is written, and a write without it was reported: %s", name, strings.Join(got, "; "))
		}
	}
}

// A helper that stores, through the variable's address, a function that unlocks. The function is
// among those the variable may hold, but it was made in the helper, and the mutex it unlocks is
// named there by the helper's own values: its receiver, a parameter, a variable it captured. The
// lock check keeps the locks of the function it is checking as paths from that function's
// values, so the release matched nothing and the write after the call still counted as guarded.
//
// What such a function runs on is now brought to the function being checked: a parameter of the
// helper is the argument at the call the variable's address went in by, a captured variable is
// what the closure was made with. Where that cannot be done the function may unlock anything.
func TestLockReleasedByAFunctionAHelperStores(t *testing.T) {
	for name, body := range map[string]string{
		"a method value of the helper's receiver":             "s.mu.Lock()\n\tf := func() {}\n\ts.setUnlock(&f)\n\tf()\n\ts.n++",
		"a closure made in the helper":                        "s.mu.Lock()\n\tf := func() {}\n\ts.setUnlockClosure(&f)\n\tf()\n\ts.n++",
		"the mutex's own method, bound in the helper":         "s.mu.Lock()\n\tf := func() {}\n\ts.setUnlockBound(&f)\n\tf()\n\ts.n++",
		"a helper handed s as an argument":                    "s.mu.Lock()\n\tf := func() {}\n\tsetUnlockOf(&f, s)\n\tf()\n\ts.n++",
		"through two helpers":                                 "s.mu.Lock()\n\tf := func() {}\n\ts.setVia(&f)\n\tf()\n\ts.n++",
		"a helper that calls itself":                          "s.mu.Lock()\n\tf := func() {}\n\tsetRec(&f, s, 3)\n\tf()\n\ts.n++",
		"a closure made in a closure of this function":        "s.mu.Lock()\n\tf := func() {}\n\tw := func() { f = func() { s.mu.Unlock() } }\n\tw()\n\tf()\n\ts.n++",
		"a method value made in a closure":                    "s.mu.Lock()\n\tf := func() {}\n\tw := func() { f = s.unlockIt }\n\tw()\n\tf()\n\ts.n++",
		"a closure handed s as an argument":                   "s.mu.Lock()\n\tf := func() {}\n\tw := func(t *S) { f = t.unlockIt }\n\tw(s)\n\tf()\n\ts.n++",
		"a helper that unlocks what a package variable holds": "s.mu.Lock()\n\tkeepS = s\n\tf := func() {}\n\tsetUnlockKept(&f)\n\tf()\n\ts.n++",
		"control: the function is made here":                  "s.mu.Lock()\n\tf := func() {}\n\tif c {\n\t\tf = s.unlockIt\n\t}\n\tf()\n\ts.n++",
	} {
		if got := lockAddressDiags(t, body); len(got) == 0 {
			t.Errorf("%s: a function the variable may hold unlocks mu, and the write of n after the call was not reported", name)
		}
	}
	for name, body := range map[string]string{
		"a helper handed another S":                                "o := &S{}\n\ts.mu.Lock()\n\tf := func() {}\n\tsetUnlockOf(&f, o)\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"a method of another S stores its own unlock":              "o := &S{}\n\ts.mu.Lock()\n\tf := func() {}\n\to.setUnlock(&f)\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"through two helpers, of another S":                        "o := &S{}\n\ts.mu.Lock()\n\tf := func() {}\n\to.setVia(&f)\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"a closure made in the helper, of another S":               "o := &S{}\n\ts.mu.Lock()\n\tf := func() {}\n\to.setUnlockClosure(&f)\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"the mutex's own method bound in the helper, of another S": "o := &S{}\n\ts.mu.Lock()\n\tf := func() {}\n\to.setUnlockBound(&f)\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"a helper that calls itself, of another S":                 "o := &S{}\n\ts.mu.Lock()\n\tf := func() {}\n\tsetRec(&f, o, 3)\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"a closure made in a closure, of another S":                "o := &S{}\n\ts.mu.Lock()\n\tf := func() {}\n\tw := func() { f = func() { o.mu.Unlock() } }\n\tw()\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
		"control: made here, of another S":                         "o := &S{}\n\ts.mu.Lock()\n\tf := func() {}\n\tif c {\n\t\tf = o.unlockIt\n\t}\n\tf()\n\ts.n++\n\ts.mu.Unlock()",
	} {
		if got := lockAddressDiags(t, body); len(got) != 0 {
			t.Errorf("%s: the function unlocks another mutex and mu is held when n is written, and it was reported: %s", name, strings.Join(got, "; "))
		}
	}
}

// A function of another package that a helper bound to the helper's own value is handed that
// value, and may unlock what is in it, as any function of another package may that is handed it
// here. (*sync.RWMutex).RLocker is not one of the mutex operations the check knows, so it stands
// for any such function.
func TestLockHandedToAnotherPackageByAFunctionAHelperStores(t *testing.T) {
	const src = `package p

import "sync"

type RW struct {
	mu sync.RWMutex
	//gosree:guarded_by mu
	n int
}

func (r *RW) setLocker(p *func() sync.Locker) { *p = r.mu.RLocker }

func (r *RW) viaHelper() {
	r.mu.Lock()
	f := func() sync.Locker { return nil }
	r.setLocker(&f)
	f()
	r.n++
}

func (r *RW) here(c bool) {
	r.mu.Lock()
	f := func() sync.Locker { return nil }
	if c {
		f = r.mu.RLocker
	}
	f()
	r.n++
}

func (r *RW) ofAnother(o *RW) {
	r.mu.Lock()
	f := func() sync.Locker { return nil }
	o.setLocker(&f)
	f()
	r.n++
	r.mu.Unlock()
}
`
	got := advDiagLines(t, src, "SAFE009")
	lines := map[string]bool{}
	for _, d := range got {
		line, _, _ := strings.Cut(d, ":")
		lines[line] = true
	}
	if !lines["18"] {
		t.Errorf("bound in a helper: mu was handed to a function of another package, and the write of n after the call was not reported: %v", got)
	}
	if !lines["28"] {
		t.Errorf("control, bound here: the write of n after the call was not reported: %v", got)
	}
	if lines["36"] {
		t.Errorf("bound in a helper to another RW: mu is held when n is written, and it was reported: %v", got)
	}
}
