package ownership_test

import (
	"strings"
	"testing"
)

// A function that returns what a callable or a receiver handed to it returns has a fresh result
// only when every value it may return is one: every forwarded target returns a fresh value at
// this call, and whatever else it returns is fresh too. It used to be taken for fresh as soon as
// one forwarded target was, so a result that may be a package variable was sent on as owned.
const freshForwardHead = `package p

import "sync"

type B struct{ n int }

var global = &B{}

var pool sync.Pool

type getter interface{ Get() any }

type sharedGetter struct{}

func (sharedGetter) Get() any { return global }

func wrapEarly(f getter) any { return whole(f) }

func pick(c bool, f, g getter) any {
	if c {
		return f.Get()
	}
	return g.Get()
}

func half(c bool, f getter) any {
	if c {
		return f.Get()
	}
	return global
}

func whole(f getter) any { return f.Get() }

func wrapLate(f getter) any { return whole(f) }

func wrapPick(c bool, f, g getter) any { return pick(c, f, g) }

func wrapHalf(c bool, f getter) any { return half(c, f) }

func wrapMixed(c bool, f getter) any { return pick(c, f, sharedGetter{}) }

func wrapMixedFresh(c bool, f getter) any { return pick(c, f, &pool) }

func getPool() any   { return pool.Get() }
func getShared() any { return global }

func wrapFnEarly(f func() any) any { return wholeFn(f) }

func pickFn(c bool, f, g func() any) any {
	if c {
		return f()
	}
	return g()
}

func halfFn(c bool, f func() any) any {
	if c {
		return f()
	}
	return global
}

func wholeFn(f func() any) any { return f() }

func wrapFnLate(f func() any) any { return wholeFn(f) }
`

func freshForwardDiags(t *testing.T, params, call string) []string {
	t.Helper()
	return advDiagLines(t, freshForwardHead+"\nfunc Use("+params+") {\n\tb := "+call+"\n\tch <- b\n}\n", "")
}

func TestForwardedResultIsFreshOnlyWhenEveryReturnIs(t *testing.T) {
	const std = "c bool, ch chan any"
	for _, tc := range []struct{ name, params, call string }{
		{"two receivers, the second shared", std, "pick(c, &pool, sharedGetter{})"},
		{"two receivers, the first shared", std, "pick(c, sharedGetter{}, &pool)"},
		{"a receiver and a package variable", std, "half(c, &pool)"},
		{"two receivers, one not known", std + ", g getter", "pick(c, &pool, g)"},
		{"through a wrapper declared before, shared", std, "wrapEarly(sharedGetter{})"},
		{"through a wrapper declared after, shared", std, "wrapLate(sharedGetter{})"},
		{"through a wrapper of two receivers", std, "wrapPick(c, &pool, sharedGetter{})"},
		{"through a wrapper of a receiver and a package variable", std, "wrapHalf(c, &pool)"},
		{"through a wrapper that hands on one receiver and fixes the other as shared", std, "wrapMixed(c, &pool)"},
		{"two callables, the second shared", std, "pickFn(c, getPool, getShared)"},
		{"two callables, the first shared", std, "pickFn(c, getShared, getPool)"},
		{"a callable and a package variable", std, "halfFn(c, getPool)"},
		{"two callables, one not known", std + ", g func() any", "pickFn(c, getPool, g)"},
		{"callable through a wrapper declared before, shared", std, "wrapFnEarly(getShared)"},
		{"callable through a wrapper declared after, shared", std, "wrapFnLate(getShared)"},
	} {
		got := freshForwardDiags(t, tc.params, tc.call)
		found := false
		for _, l := range got {
			if strings.Contains(l, "SAFE002") && strings.Contains(l, "not known to return a fresh value") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: %s may return a value that is not fresh, and sending it was not reported; got %v", tc.name, tc.call, got)
		}
	}
}

// What the fix must not take away: when every value the function may return is fresh at the call,
// its result is still fresh, directly and through a wrapper declared on either side.
func TestForwardedResultStaysFreshWhenEveryReturnIs(t *testing.T) {
	const std = "c bool, ch chan any"
	for _, call := range []string{
		"pick(c, &pool, &pool)",
		"whole(&pool)",
		"wrapEarly(&pool)",
		"wrapLate(&pool)",
		"wrapPick(c, &pool, &pool)",
		"wrapMixedFresh(c, &pool)",
		"pickFn(c, getPool, getPool)",
		"wholeFn(getPool)",
		"wrapFnEarly(getPool)",
		"wrapFnLate(getPool)",
	} {
		if got := freshForwardDiags(t, std, call); len(got) != 0 {
			t.Errorf("%s returns a fresh value on every path and was reported: %v", call, got)
		}
	}
}
