package main

import (
	"go/types"
	"testing"
	"time"
)

// reach itself, called directly. This file refers to (*census).reach, so it does not build against
// the pre-fix census.go.

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

// HOLDS, with a caveat. reach visits a type expression as a tree, so on Pair[P1, P1] with
// P1 = Pair[B, B] (one instance in memory) it does 2^k visits: 20ms at k=20, 74ms at k=22, 309ms at
// k=24, 584ms at k=25, doubling per level. But go/types needs 1.3s, 4.5s, 23s and 42s to check the
// same declarations, so the toolchain gives out first (reach is about 1/70 of the load time); no
// package that loads can make reach take a second. The test pins the bound at k=22.
func TestAdv2ReachGenericDAG(t *testing.T) {
	m := adv2Load(t, adv2PairDAG(22))
	c := &census{module: "example.com/gen"}
	ft := adv2FieldType(t, m, "H", "p")
	start := time.Now()
	got := c.reach(ft)
	d := time.Since(start)
	t.Logf("k=22: reach %v -> %v", d, got)
	if len(got) != 2 {
		t.Errorf("reach = %v, want Pair and B", got)
	}
	if d > time.Second {
		t.Errorf("reach took %v at k=22", d)
	}
}

// The same through unnamed struct literals (reach's *types.Struct case).
func TestAdv2ReachUnnamedDAG(t *testing.T) {
	m := adv2Load(t, adv2StructDAG(22))
	c := &census{module: "example.com/gen"}
	ft := adv2FieldType(t, m, "H", "u")
	start := time.Now()
	got := c.reach(ft)
	d := time.Since(start)
	t.Logf("k=22: reach %v -> %v", d, got)
	if len(got) != 1 {
		t.Errorf("reach = %v, want B", got)
	}
	if d > time.Second {
		t.Errorf("reach took %v at k=22", d)
	}
}
