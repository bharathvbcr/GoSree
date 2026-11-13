package main

import (
	"go/types"
	"testing"
	"time"
)

// reach itself, called directly. This file refers to (*census).reachCounted, so it does not build
// against a census.go from before the reach fix.

func adv2FieldType(t *testing.T, m *adv2Module, typ, field string) types.Type {
	t.Helper()
	for _, p := range m.pkgs {
		if obj := p.Types.Scope().Lookup(typ); obj != nil {
			st := obj.Type().Underlying().(*types.Struct)
			for f := range st.Fields() {
				if f.Name() == field {
					return f.Type()
				}
			}
		}
	}
	t.Fatalf("no %s.%s", typ, field)
	return nil
}

// adv2ReachCeiling is the time after which a reach call counts as hung, the limit adv2Timed's
// callers give a whole census. It is not a performance bound: the visit count is.
const adv2ReachCeiling = 60 * time.Second

// adv2Reach loads src, runs reach on the type of H.<field> and returns what it names and how many
// type nodes it visited. The duration is logged, and fails the test only past adv2ReachCeiling.
func adv2Reach(t *testing.T, k int, src, field string) ([]*types.Named, int) {
	t.Helper()
	m := adv2Load(t, src)
	c := &census{module: "example.com/gen"}
	ft := adv2FieldType(t, m, "H", field)
	start := time.Now()
	got, visits := c.reachCounted(ft)
	d := time.Since(start)
	t.Logf("k=%d: reach %v, %d visits -> %v", k, d, visits, got)
	if d > adv2ReachCeiling {
		t.Errorf("k=%d: reach took %v, over the %v hang limit", k, d, adv2ReachCeiling)
	}
	return got, visits
}

// HOLDS, with a caveat. reach visits a type expression as a tree, so on Pair[P1, P1] with
// P1 = Pair[B, B] (one instance in memory) the visits double per level: 20ms at k=20, 74ms at k=22,
// 309ms at k=24, 584ms at k=25 on an idle machine. But go/types needs 1.3s, 4.5s, 23s and 42s to
// check the same declarations, so the toolchain gives out first (reach is about 1/70 of the load
// time).
//
// The test pins the number of visits, not the time. A one-second limit stood here and failed once
// at 1.10s with the right answer (2026-11-12, the whole suite under cgo at load average 100; 13
// reruns passed at 78 to 191ms), because a duration measures the machine as well as the walk. The
// count is the size of the expression as a tree and is the same at any load: the pointer, 2^k - 1
// instances of Pair and 2^k leaves B, which is 2^(k+1), or 4 at k=1 (*P1, Pair, B, B). A walk that
// does more fails; one that visits each instance once would pass.
func TestAdv2ReachGenericDAG(t *testing.T) {
	for _, k := range []int{1, 3, 22} {
		got, visits := adv2Reach(t, k, adv2PairDAG(k), "p")
		if len(got) != 2 {
			t.Errorf("k=%d: reach = %v, want Pair and B", k, got)
		}
		if tree := 1 << (k + 1); visits > tree {
			t.Errorf("k=%d: reach made %d visits, want at most %d, the size of the type as a tree", k, visits, tree)
		}
	}
}

// The same through unnamed struct literals (reach's *types.Struct case): 2^k - 1 structs and, at
// each of the 2^k leaves, the pointer U0 and B, which is 3*2^k - 1, or 5 at k=1.
func TestAdv2ReachUnnamedDAG(t *testing.T) {
	for _, k := range []int{1, 3, 22} {
		got, visits := adv2Reach(t, k, adv2StructDAG(k), "u")
		if len(got) != 1 {
			t.Errorf("k=%d: reach = %v, want B", k, got)
		}
		if tree := 3<<k - 1; visits > tree {
			t.Errorf("k=%d: reach made %d visits, want at most %d, the size of the type as a tree", k, visits, tree)
		}
	}
}
