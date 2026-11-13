package errclosurepairadv

// Adversarial fixture for 4b7ffd3. C_x closes over a (v, err) pair, T_x is the same shape without a
// closure. The markers record what the build reports today: a tracked closure reports at the use in
// the enclosing function, an untracked one (call on some paths only, recover, early return, other
// closures or addresses touching the cells) reports at the closure store, and the twin shows what the
// engine would say with no closure. Every C_x that reported at the closure before the fix still
// reports somewhere, except the named-result shapes in errclosurepairbreak. Checked shapes that
// both builds report (errors.Is, errors.As, helper checks) are engine limits shared with the twin.

import (
	"errors"
	"fmt"
	"sync"
)

type T struct{ n int }

func (t *T) M() int { return t.n }

var errNo = errors.New("no")

func workV() (*T, error) {
	if len(fmt.Sprint(1)) == 2 {
		return nil, errNo
	}
	return &T{n: 1}, nil
}

func workW() (*T, error) { return workV() }

func use(t *T) int { return t.n }

func usePtr(p **T) int { return (*p).n }

func cond() bool { return len(fmt.Sprint(2)) == 1 }

func check(err error) bool { return err != nil }

// ---- 1. control flow: call reached on only some paths -------------------------------------

func C_Goto(c bool) int {
	var v *T
	var err error
	_ = err
	if c {
		goto done
	}
	func() { v, err = workV() }() // want "ERR002"
done:
	return use(v)
}

func T_Goto(c bool) int {
	var v *T
	var err error
	if c {
		goto done
	}
	v, err = workV()
done:
	_ = err
	return use(v) // want "ERR002"
}

func C_GotoBack() int {
	var v *T
	var err error
	_ = err
	i := 0
again:
	func() { v, err = workV() }()
	i++
	if i < 2 {
		goto again
	}
	return use(v) // want "ERR002"
}

func T_GotoBack() int {
	var v *T
	var err error
	i := 0
again:
	v, err = workV() // want "ERR003"
	i++
	if i < 2 {
		goto again
	}
	_ = err
	return use(v) // want "ERR002"
}

func C_LabelBreak() int {
	var v *T
	var err error
	_ = err
outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if cond() {
				break outer
			}
			func() { v, err = workV() }() // want "ERR002"
		}
	}
	return use(v)
}

func T_LabelBreak() int {
	var v *T
	var err error
outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if cond() {
				break outer
			}
			v, err = workV() // want "ERR003"
		}
	}
	_ = err
	return use(v) // want "ERR002"
}

func C_LabelContinue() int {
	var v *T
	var err error
	_ = err
outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if cond() {
				continue outer
			}
			func() { v, err = workV() }() // want "ERR002"
		}
	}
	return use(v)
}

func T_LabelContinue() int {
	var v *T
	var err error
outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if cond() {
				continue outer
			}
			v, err = workV() // want "ERR003"
		}
	}
	_ = err
	return use(v) // want "ERR002"
}

func C_Select(ch chan int) int {
	var v *T
	var err error
	_ = err
	select {
	case <-ch:
		func() { v, err = workV() }() // want "ERR002"
	default:
	}
	return use(v)
}

func T_Select(ch chan int) int {
	var v *T
	var err error
	select {
	case <-ch:
		v, err = workV()
	default:
	}
	_ = err
	return use(v) // want "ERR002"
}

func C_Fallthrough(x int) int {
	var v *T
	var err error
	_ = err
	switch x {
	case 1:
		fallthrough
	case 2:
		func() { v, err = workV() }() // want "ERR002"
	case 3:
	}
	return use(v)
}

func T_Fallthrough(x int) int {
	var v *T
	var err error
	switch x {
	case 1:
		fallthrough
	case 2:
		v, err = workV()
	case 3:
	}
	_ = err
	return use(v) // want "ERR002"
}

func C_Recover() (n int) {
	var v *T
	var err error
	defer func() {
		if r := recover(); r != nil {
			n = use(v)
		}
	}()
	func() { v, err = workV() }() // want "ERR002"
	_ = err
	return use(v)
}

func T_Recover() (n int) {
	var v *T
	var err error
	defer func() {
		if r := recover(); r != nil {
			n = use(v)
		}
	}()
	v, err = workV()
	_ = err
	return use(v) // want "ERR002"
}

func C_Panic() int {
	var v *T
	var err error
	_ = err
	func() {
		v, err = workV()
		panic("x")
	}()
	return use(v) // want "ERR002"
}

func C_ZeroLoop(n int) int {
	var v *T
	var err error
	_ = err
	for i := 0; i < n; i++ {
		func() { v, err = workV() }() // want "ERR002"
	}
	return use(v)
}

func T_ZeroLoop(n int) int {
	var v *T
	var err error
	for i := 0; i < n; i++ {
		v, err = workV() // want "ERR003"
	}
	_ = err
	return use(v) // want "ERR002"
}

func C_EarlyReturn(c bool) int {
	var v *T
	var err error
	_ = err
	if c {
		return 0
	}
	func() { v, err = workV() }() // want "ERR002"
	return use(v)
}

func T_EarlyReturn(c bool) int {
	var v *T
	var err error
	if c {
		return 0
	}
	v, err = workV()
	_ = err
	return use(v) // want "ERR002"
}

// the use sits on the path that skipped the call
func C_UseOnSkipPath(c bool) int {
	var v *T
	var err error
	if !c {
		func() { v, err = workV() }() // want "ERR002"
		if err != nil {
			return 0
		}
	}
	return use(v)
}

func T_UseOnSkipPath(c bool) int {
	var v *T
	var err error
	if !c {
		v, err = workV()
		if err != nil {
			return 0
		}
	}
	return use(v)
}

// loop that runs the call each time; use before check inside the loop
func C_LoopUnchecked() int {
	var v *T
	var err error
	for i := 0; ; i++ {
		func() { v, err = workV() }()
		if i == 1 {
			_ = use(v) // want "ERR002"
		}
		if err != nil {
			return 0
		}
		if i > 2 {
			break
		}
	}
	return 0
}

func T_LoopUnchecked() int {
	var v *T
	var err error
	for i := 0; ; i++ {
		v, err = workV()
		if i == 1 {
			_ = use(v) // want "ERR002"
		}
		if err != nil {
			return 0
		}
		if i > 2 {
			break
		}
	}
	return 0
}

// the check is for the previous iteration; the use follows the next call
func C_LoopStale() int {
	var v *T
	var err error
	for i := 0; ; i++ {
		if i > 0 && err != nil {
			return 0
		}
		func() { v, err = workV() }() // want "ERR002"
		_ = use(v)
		if i > 2 {
			break
		}
	}
	return 0
}

func T_LoopStale() int {
	var v *T
	var err error
	for i := 0; ; i++ {
		if i > 0 && err != nil {
			return 0
		}
		v, err = workV() // want "ERR003"
		_ = use(v) // want "ERR002"
		if i > 2 {
			break
		}
	}
	return 0
}

// ---- 2. use that is not a plain load ------------------------------------------------------

func C_AddrBefore() int {
	var v *T
	var err error
	p := &v
	func() { v, err = workV() }()
	_ = err
	return use(*p) // want "ERR002"
}

func T_AddrBefore() int {
	var v *T
	var err error
	p := &v
	v, err = workV()
	_ = err
	return use(*p) // want "ERR002"
}

func C_AddrAfter() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	p := &v
	_ = err
	return use(*p) // want "ERR002"
}

func T_AddrAfter() int {
	var v *T
	var err error
	v, err = workV()
	p := &v
	_ = err
	return use(*p) // want "ERR002"
}

func C_PtrArg() int {
	var v *T
	var err error
	func() { v, err = workV() }() // want "ERR002"
	_ = err
	return usePtr(&v)
}

func T_PtrArg() int {
	var v *T
	var err error
	v, err = workV() // want "ERR002"
	_ = err
	return usePtr(&v)
}

func C_SecondClosureReads() int {
	var v *T
	var err error
	func() { v, err = workV() }() // want "ERR002"
	g := func() int { return use(v) }
	_ = err
	return g()
}

func T_SecondClosureReads() int {
	var v *T
	var err error
	v, err = workV()
	g := func() int { return use(v) }
	_ = err
	return g()
}

func C_DeferReads() int {
	var v *T
	var err error
	func() { v, err = workV() }() // want "ERR002"
	defer func() { _ = use(v) }()
	_ = err
	return 0
}

func T_DeferReads() int {
	var v *T
	var err error
	v, err = workV()
	defer func() { _ = use(v) }()
	_ = err
	return 0
}

func C_MethodValue() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	m := v.M
	if err != nil {
		return 0
	}
	return m()
}

func T_MethodValue() int {
	var v *T
	var err error
	v, err = workV()
	m := v.M
	if err != nil {
		return 0
	}
	return m()
}

func C_MethodCall() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	_ = err
	return v.M() // want "ERR002"
}

func T_MethodCall() int {
	var v *T
	var err error
	v, err = workV()
	_ = err
	return v.M() // want "ERR002"
}

// ---- 3. err observed through something else -----------------------------------------------

func C_Alias() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	e := err
	if e != nil {
		return 0
	}
	return use(v)
}

func T_Alias() int {
	var v *T
	var err error
	v, err = workV()
	e := err
	if e != nil {
		return 0
	}
	return use(v)
}

func C_OtherErr() int {
	var v *T
	var err, err2 error
	func() { v, err = workV() }()
	_, err2 = workV()
	if err2 != nil {
		return 0
	}
	_ = err
	return use(v) // want "ERR002"
}

func T_OtherErr() int {
	var v *T
	var err, err2 error
	v, err = workV()
	_, err2 = workV()
	if err2 != nil {
		return 0
	}
	_ = err
	return use(v) // want "ERR002"
}

func C_ErrorsAs() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	var pe *fmt.State
	if errors.As(err, &pe) {
		return 0
	}
	return use(v) // want "ERR002"
}

func T_ErrorsAs() int {
	var v *T
	var err error
	v, err = workV()
	var pe *fmt.State
	if errors.As(err, &pe) {
		return 0
	}
	return use(v) // want "ERR002"
}

func C_Helper() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	if check(err) {
		return 0
	}
	return use(v) // want "ERR002"
}

func T_Helper() int {
	var v *T
	var err error
	v, err = workV()
	if check(err) {
		return 0
	}
	return use(v) // want "ERR002"
}

func C_ErrorsIs() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	if errors.Is(err, errNo) {
		return 0
	}
	return use(v) // want "ERR002"
}

func T_ErrorsIs() int {
	var v *T
	var err error
	v, err = workV()
	if errors.Is(err, errNo) {
		return 0
	}
	return use(v) // want "ERR002"
}

// ---- 4. reassignment ----------------------------------------------------------------------

func C_SecondClosureErrOnly() int {
	var v *T
	var err error
	func() { v, err = workV() }() // want "ERR002"
	func() { err = nil }()
	if err != nil {
		return 0
	}
	return use(v)
}

func T_SecondClosureErrOnly() int {
	var v *T
	var err error
	v, err = workV()
	func() { err = nil }()
	if err != nil {
		return 0
	}
	return use(v)
}

func C_SecondClosureValOnly() int {
	var v *T
	var err error
	func() { v, err = workV() }() // want "ERR002"
	func() { v = nil }()
	if err != nil {
		return 0
	}
	return use(v)
}

func T_SecondClosureValOnly() int {
	var v *T
	var err error
	v, err = workV()
	func() { v = nil }()
	if err != nil {
		return 0
	}
	return use(v)
}

func C_GoroutineWritesV() int {
	var v *T
	var err error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); v = nil }()
	func() { v, err = workV() }() // want "ERR002"
	wg.Wait()
	_ = err
	return use(v)
}

func T_GoroutineWritesV() int {
	var v *T
	var err error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); v = nil }()
	v, err = workV()
	wg.Wait()
	_ = err
	return use(v) // want "ERR002"
}

func C_ParentReassignsPair() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	v, err = workW()
	if err != nil {
		return 0
	}
	return use(v)
}

func T_ParentReassignsPair() int {
	var v *T
	var err error
	v, err = workV()
	v, err = workW() // want "ERR003"
	if err != nil {
		return 0
	}
	return use(v)
}

// parent rewrites v from a fresh fallible call but checks the stale err
func C_ParentReassignsV() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	if err != nil {
		return 0
	}
	v, _ = workW() // want "ERR002"
	return use(v)
}

func T_ParentReassignsV() int {
	var v *T
	var err error
	v, err = workV()
	if err != nil {
		return 0
	}
	v, _ = workW() // want "ERR002"
	return use(v)
}

func C_ParentReassignsErr() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	_, err = workW()
	if err != nil {
		return 0
	}
	return use(v) // want "ERR002"
}

func T_ParentReassignsErr() int {
	var v *T
	var err error
	v, err = workV()
	_, err = workW() // want "ERR003"
	if err != nil {
		return 0
	}
	return use(v) // want "ERR002"
}

// closure writes through a pre-existing checked pair first
func C_ConditionalStore(c bool) int {
	var v *T
	var err error
	_ = err
	func() {
		if c {
			v, err = workV()
		}
	}()
	return use(v) // want "ERR002"
}

func T_ConditionalStore(c bool) int {
	var v *T
	var err error
	if c {
		v, err = workV()
	}
	_ = err
	return use(v) // want "ERR002"
}

// ---- 5. generics, method values, returned and immediately invoked closures -----------------

func C_Generic[X any](f func() (X, error), u func(X)) {
	var v X
	var err error
	func() { v, err = f() }()
	_ = err
	u(v) // want "ERR002"
}

func T_Generic[X any](f func() (X, error), u func(X)) {
	var v X
	var err error
	v, err = f()
	_ = err
	u(v) // want "ERR002"
}

func C_GenericChecked[X any](f func() (X, error), u func(X)) {
	var v X
	var err error
	func() { v, err = f() }()
	if err != nil {
		return
	}
	u(v)
}

type P struct{ n int }

func (p *P) load() (*T, error) { return workV() }

func C_MethodSource(p *P) int {
	var v *T
	var err error
	func() { v, err = p.load() }()
	_ = err
	return use(v) // want "ERR002"
}

func T_MethodSource(p *P) int {
	var v *T
	var err error
	v, err = p.load()
	_ = err
	return use(v) // want "ERR002"
}

func C_MethodValueSource(p *P) int {
	var v *T
	var err error
	ld := p.load
	func() { v, err = ld() }()
	_ = err
	return use(v) // want "ERR002"
}

func T_MethodValueSource(p *P) int {
	var v *T
	var err error
	ld := p.load
	v, err = ld()
	_ = err
	return use(v) // want "ERR002"
}

func mk() func() {
	var v *T
	var err error
	f := func() { v, err = workV() } // want "ERR002"
	f()
	_ = err
	_ = use(v)
	return f
}

func C_ReturnedAndInvoked() int {
	var v *T
	var err error
	g := func() func() {
		return func() { v, err = workV() } // want "ERR002"
	}
	g()()
	_ = err
	return use(v)
}

func T_ReturnedAndInvoked() int {
	var v *T
	var err error
	v, err = workV()
	_ = err
	return use(v) // want "ERR002"
}

func C_IIFEArgs() int {
	var v *T
	var err error
	r := func(x int) error {
		v, err = workV()
		return nil
	}(1)
	_ = r
	_ = err
	return use(v) // want "ERR002"
}

func T_IIFEArgs() int {
	var v *T
	var err error
	v, err = workV()
	_ = err
	return use(v) // want "ERR002"
}

func C_IIFEReturnsErr() int {
	var v *T
	var err error
	r := func(x int) error {
		v, err = workV()
		return err
	}(1)
	if r != nil {
		return 0
	}
	return use(v) // want "ERR002"
}

func T_IIFEReturnsErr() int {
	var v *T
	var err error
	v, err = workV()
	r := err
	if r != nil {
		return 0
	}
	return use(v)
}

func C_IIFEErrIgnored() int {
	var v *T
	var err error
	_ = func(x int) error {
		v, err = workV()
		return err
	}(1)
	return use(v) // want "ERR002"
}

func T_IIFEErrIgnored() int {
	var v *T
	var err error
	v, err = workV()
	_ = err
	return use(v) // want "ERR002"
}

// ---- 6. other value types, multiple pairs --------------------------------------------------

type S struct{ a, b int }

func workS() (S, error)            { return S{}, errNo }
func workI() (fmt.Stringer, error) { return nil, errNo }
func workPP() (**T, error)         { return nil, errNo }

func useS(s S) int { return s.a }

func C_Struct() int {
	var s S
	var err error
	func() { s, err = workS() }()
	_ = err
	return useS(s) // want "ERR002"
}

func T_Struct() int {
	var s S
	var err error
	s, err = workS()
	_ = err
	return useS(s) // want "ERR002"
}

func C_Iface() string {
	var s fmt.Stringer
	var err error
	func() { s, err = workI() }()
	_ = err
	return s.String() // want "ERR002"
}

func T_Iface() string {
	var s fmt.Stringer
	var err error
	s, err = workI()
	_ = err
	return s.String() // want "ERR002"
}

func C_IfaceChecked() string {
	var s fmt.Stringer
	var err error
	func() { s, err = workI() }()
	if err != nil {
		return ""
	}
	return s.String()
}

func C_PtrPtr() int {
	var p **T
	var err error
	func() { p, err = workPP() }()
	_ = err
	return (*p).n // want "ERR002"
}

func T_PtrPtr() int {
	var p **T
	var err error
	p, err = workPP()
	_ = err
	return (*p).n // want "ERR002"
}

func C_TwoPairsWrongErr() int {
	var a, b *T
	var err1, err2 error
	func() {
		a, err1 = workV() // want "ERR002"
		b, err2 = workW() // want "ERR002"
	}()
	if err2 != nil {
		return 0
	}
	_ = err1
	return use(a) + use(b)
}

func T_TwoPairsWrongErr() int {
	var a, b *T
	var err1, err2 error
	a, err1 = workV()
	b, err2 = workW()
	if err2 != nil {
		return 0
	}
	_ = err1
	return use(a) + use(b) // want "ERR002"
}

func C_TwoPairsRight() int {
	var a, b *T
	var err1, err2 error
	func() {
		a, err1 = workV() // want "ERR002"
		b, err2 = workW() // want "ERR002"
	}()
	if err1 != nil || err2 != nil {
		return 0
	}
	return use(a) + use(b)
}

func C_TwoClosuresWrongErr() int {
	var a, b *T
	var err1, err2 error
	func() { a, err1 = workV() }()
	func() { b, err2 = workW() }()
	if err2 != nil {
		return 0
	}
	_ = err1
	return use(a) + use(b) // want "ERR002"
}

func T_TwoClosuresWrongErr() int {
	var a, b *T
	var err1, err2 error
	a, err1 = workV()
	b, err2 = workW()
	if err2 != nil {
		return 0
	}
	_ = err1
	return use(a) + use(b) // want "ERR002"
}

// crossed: the closure stores v1 with err2's call and v2 with err1's call
func C_Crossed() int {
	var a, b *T
	var err1, err2 error
	func() {
		a, err2 = workV() // want "ERR002"
		b, err1 = workW() // want "ERR002"
	}()
	if err1 != nil {
		return 0
	}
	_ = err2
	return use(a) + use(b)
}

func T_Crossed() int {
	var a, b *T
	var err1, err2 error
	a, err2 = workV()
	b, err1 = workW()
	if err1 != nil {
		return 0
	}
	_ = err2
	return use(a) + use(b) // want "ERR002"
}

// the closure keeps the error of one call and the value of another
func C_MixedCalls() int {
	var a *T
	var err error
	func() {
		a, _ = workV() // want "ERR002"
		_, err = workW()
	}()
	if err != nil {
		return 0
	}
	return use(a)
}

func T_MixedCalls() int {
	var a *T
	var err error
	a, _ = workV() // want "ERR002"
	_, err = workW()
	if err != nil {
		return 0
	}
	return use(a)
}

// the closure stores the value in a branch and the error unconditionally
func C_SplitBlocks(c bool) int {
	var a *T
	var err error
	func() {
		var e error
		var r *T
		r, e = workV()
		if c {
			a = r // want "ERR002"
		}
		err = e
	}()
	if err != nil {
		return 0
	}
	return use(a)
}

// closure returns its own error and stores a second err cell afterwards
func C_ErrCellReset() int {
	var v *T
	var err error
	func() {
		v, err = workV() // want "ERR002"
		if cond() {
			err = nil
		}
	}()
	if err != nil {
		return 0
	}
	return use(v)
}

func T_ErrCellReset() int {
	var v *T
	var err error
	v, err = workV()
	if cond() {
		err = nil
	}
	if err != nil {
		return 0
	}
	return use(v) // want "ERR002"
}

// err cell stored in the closure but the closure branches before the value store
func C_ErrFirst() int {
	var v *T
	var err error
	func() {
		r, e := workV()
		err = e
		v = r
	}()
	_ = err
	return use(v) // want "ERR002"
}

func T_ErrFirst() int {
	var v *T
	var err error
	r, e := workV()
	err = e
	v = r
	_ = err
	return use(v) // want "ERR002"
}

// use of v in the parent before the call
func C_UseBefore() int {
	var v *T
	var err error
	n := use(v)
	func() { v, err = workV() }()
	if err != nil {
		return 0
	}
	return n + use(v)
}

func T_UseBefore() int {
	var v *T
	var err error
	n := use(v)
	v, err = workV()
	if err != nil {
		return 0
	}
	return n + use(v)
}

// an error check that returns nothing and falls through to the use
func C_CheckNoReturn() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	if err != nil {
		println("bad")
	}
	return use(v) // want "ERR002"
}

func T_CheckNoReturn() int {
	var v *T
	var err error
	v, err = workV()
	if err != nil {
		println("bad")
	}
	return use(v) // want "ERR002"
}

// the error check sits in a deferred function
func C_CheckInDefer() (n int) {
	var v *T
	var err error
	func() { v, err = workV() }() // want "ERR002"
	defer func() {
		if err != nil {
			n = -1
		}
	}()
	return use(v)
}

func T_CheckInDefer() (n int) {
	var v *T
	var err error
	v, err = workV()
	defer func() {
		if err != nil {
			n = -1
		}
	}()
	return use(v) // want "ERR002"
}

// ---- 7. returns, summaries, loops, conversions ---------------------------------------------

func C_Named() (v *T, err error) {
	func() { v, err = workV() }()
	return
}

func T_Named() (v *T, err error) {
	v, err = workV()
	return
}

func C_ReturnPair() (*T, error) {
	var v *T
	var err error
	func() { v, err = workV() }()
	return v, err
}

func T_ReturnPair() (*T, error) {
	var v *T
	var err error
	v, err = workV()
	return v, err
}

func C_ReturnV() *T {
	var v *T
	var err error
	func() { v, err = workV() }()
	_ = err
	return v // want "ERR002"
}

func T_ReturnV() *T {
	var v *T
	var err error
	v, err = workV()
	_ = err
	return v // want "ERR002"
}

// callers of a function that returns the pair through a closure must still owe a check
func C_CallerIgnores() int {
	v, _ := C_ReturnPair() // want "ERR002"
	return use(v)
}

func T_CallerIgnores() int {
	v, _ := T_ReturnPair() // want "ERR002"
	return use(v)
}

func C_CallerUnchecked() int {
	v, err := C_ReturnPair()
	_ = err
	return use(v) // want "ERR002"
}

func T_CallerUnchecked() int {
	v, err := T_ReturnPair()
	_ = err
	return use(v) // want "ERR002"
}

func C_LoopBreakUnchecked() int {
	var v *T
	var err error
	for i := 0; ; i++ {
		func() { v, err = workV() }()
		if i > 2 {
			break
		}
	}
	_ = err
	return use(v) // want "ERR002"
}

func T_LoopBreakUnchecked() int {
	var v *T
	var err error
	for i := 0; ; i++ {
		v, err = workV() // want "ERR003"
		if i > 2 {
			break
		}
	}
	_ = err
	return use(v) // want "ERR002"
}

func C_LoopCheckSome() int {
	var v *T
	var err error
	for i := 0; ; i++ {
		func() { v, err = workV() }()
		if i == 0 {
			if err != nil {
				return 0
			}
		}
		if i > 2 {
			break
		}
	}
	return use(v) // want "ERR002"
}

func T_LoopCheckSome() int {
	var v *T
	var err error
	for i := 0; ; i++ {
		v, err = workV() // want "ERR003"
		if i == 0 {
			if err != nil {
				return 0
			}
		}
		if i > 2 {
			break
		}
	}
	return use(v) // want "ERR002"
}

func C_CopyBefore() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	w := v
	n := use(w) // want "ERR002"
	if err != nil {
		return 0
	}
	return n
}

func T_CopyBefore() int {
	var v *T
	var err error
	v, err = workV()
	w := v
	n := use(w) // want "ERR002"
	if err != nil {
		return 0
	}
	return n
}

func C_CopyAfterCheck() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	w := v
	if err != nil {
		return 0
	}
	return use(w)
}

func T_CopyAfterCheck() int {
	var v *T
	var err error
	v, err = workV()
	w := v
	if err != nil {
		return 0
	}
	return use(w)
}

type H struct{ t *T }

func C_StoreField(h *H) {
	var v *T
	var err error
	func() { v, err = workV() }()
	h.t = v // want "ERR002"
	_ = err
}

func T_StoreField(h *H) {
	var v *T
	var err error
	v, err = workV()
	h.t = v // want "ERR002"
	_ = err
}

func C_ChanSend(ch chan *T) {
	var v *T
	var err error
	func() { v, err = workV() }()
	ch <- v // want "ERR002"
	_ = err
}

func T_ChanSend(ch chan *T) {
	var v *T
	var err error
	v, err = workV()
	ch <- v // want "ERR002"
	_ = err
}

func C_GoUse() {
	var v *T
	var err error
	func() { v, err = workV() }() // want "ERR002"
	go func() { _ = use(v) }()
	_ = err
}

func T_GoUse() {
	var v *T
	var err error
	v, err = workV()
	go func() { _ = use(v) }()
	_ = err
}

func workE() (error, error) { return errNo, errNo }

func C_ErrValue() error {
	var v error
	var err error
	func() { v, err = workE() }()
	_ = err
	return v
}

func T_ErrValue() error {
	var v error
	var err error
	v, err = workE()
	_ = err
	return v
}

type Str struct{}

func (Str) String() string { return "" }

func workStr() (Str, error) { return Str{}, errNo }

func C_Convert() string {
	var s fmt.Stringer
	var err error
	func() { s, err = workStr() }() // want "ERR002"
	_ = err
	return s.String()
}

func T_Convert() string {
	var s fmt.Stringer
	var err error
	s, err = workStr()
	_ = err
	return s.String() // want "ERR002"
}

func work3() (*T, *T, error) { return nil, nil, errNo }

func C_Three() int {
	var a, b *T
	var err error
	func() { a, b, err = work3() }()
	_ = err
	return use(a) + use(b) // want "ERR002"
}

func T_Three() int {
	var a, b *T
	var err error
	a, b, err = work3()
	_ = err
	return use(a) + use(b) // want "ERR002"
}

func C_ThreeCheckedHalf() int {
	var a, b *T
	var err error
	func() { a, b, err = work3() }()
	if err != nil {
		return 0
	}
	return use(a) + use(b)
}

// a value cell loaded into a second function's argument inside a closure the pair closure shares
func C_UseInsideLaterClosure() int {
	var v *T
	var err error
	func() { v, err = workV() }() // want "ERR002"
	if err != nil {
		return 0
	}
	return func() int { return use(v) }()
}

// ---- 8. returns with a replaced or missing error --------------------------------------------

func C_ReturnNil() (*T, error) {
	var v *T
	var err error
	func() { v, err = workV() }()
	_ = err
	return v, nil // want "ERR002"
}

func T_ReturnNil() (*T, error) {
	var v *T
	var err error
	v, err = workV()
	_ = err
	return v, nil // want "ERR002"
}

func C_ReturnOtherErr() (*T, error) {
	var v *T
	var err, other error
	func() { v, err = workV() }()
	_ = err
	return v, other // want "ERR002"
}

func T_ReturnOtherErr() (*T, error) {
	var v *T
	var err, other error
	v, err = workV()
	_ = err
	return v, other // want "ERR002"
}

func C_NamedUse() (v *T, err error) {
	func() { v, err = workV() }()
	_ = use(v) // want "ERR002"
	return
}

func T_NamedUse() (v *T, err error) {
	v, err = workV()
	_ = use(v) // want "ERR002"
	return
}

func C_NamedValOnlyReturn() (*T, error) {
	var v *T
	var err error
	func() { v, err = workV() }()
	_ = err
	return v, errors.New("wrapped")
}

// ---- 9. usefulness limit (not a safety break) ------------------------------------------------

// Two checked pairs in a row with early returns: the second call is not on the path of the first
// check's return, so only the first closure is tracked and the second stays a false positive at
// the closure store (the same report the base build gives for both).
func C_SeqChecked() int {
	var a, b *T
	var ea, eb error
	func() { a, ea = workV() }()
	if ea != nil {
		return 0
	}
	func() { b, eb = workW() }() // want "ERR002"
	if eb != nil {
		return 0
	}
	return use(a) + use(b)
}
