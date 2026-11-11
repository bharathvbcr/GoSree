package main

import (
	"slices"
	"testing"
)

// Probe 4: classes the fix was not meant to change. The fixture is ordinary code that the pre-fix
// census (6b401b9) handled. adv2RegressOld holds the closure it gave for each hatch, recorded by
// running this test against 6b401b9's census.go (ADV2_DUMP); the test asserts the fixed census gives
// the same, except for the hatches in adv2RegressIntended, which the fix changes on purpose.
const adv2RegressSrc = `package gen

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

type Shape interface{ Area() int }
type Sq struct{ w int }

func (s *Sq) Area() int { return s.w }

type Circ struct{ r []int }

func (c *Circ) Area() int { return len(c.r) }

// Own fields only.
type Plain struct {
	a int
	s string
}
type Setter struct{ a int }

func (s *Setter) Set(v int) { s.a = v }

type LockedW struct {
	mu sync.Mutex
	v  int
}

func (l *LockedW) Set(v int) { l.mu.Lock(); l.v = v; l.mu.Unlock() }
func (l *LockedW) Get() int  { return l.v }

type LockedBoth struct {
	mu sync.Mutex
	v  int
}

func (l *LockedBoth) Set(v int) { l.mu.Lock(); l.v = v; l.mu.Unlock() }
func (l *LockedBoth) Get() int  { l.mu.Lock(); defer l.mu.Unlock(); return l.v }

type Ext struct{ b bytes.Buffer }
type ExtPtr struct{ b *bytes.Buffer }
type Exported struct{ A int }

// Collections.
type SliceFrozen struct{ xs []int }
type SliceW struct{ xs []int }

func (s *SliceW) Add(v int) { s.xs = append(s.xs, v) }

type MapFrozen struct{ m map[string]int }
type MapW struct{ m map[string]int }

func (s *MapW) Put(k string, v int) { s.m[k] = v }

type MapLocked struct {
	mu sync.Mutex
	m  map[string]int
}

func (s *MapLocked) Put(k string, v int) { s.mu.Lock(); s.m[k] = v; s.mu.Unlock() }
func (s *MapLocked) Get(k string) int    { s.mu.Lock(); defer s.mu.Unlock(); return s.m[k] }

type PtrPlain struct{ p *int }

// guarded_by and imm.
type Guarded struct {
	mu sync.Mutex
	//gosree:guarded_by mu
	m map[string]int
}

func (g *Guarded) Put(k string, v int) { g.mu.Lock(); g.m[k] = v; g.mu.Unlock() }

//gosree:imm
type Frozen struct{ a int }
type HoldsFrozen struct{ f *Frozen }
type HoldsFrozenVal struct{ f Frozen }

// Children through pointers, interface slots and cycles.
type PtrChild struct{ s *Setter }
type PtrChildPlain struct{ s *Plain }
type IfaceDirect struct{ s Shape }

func NewIfaceDirect() *IfaceDirect { return &IfaceDirect{s: &Sq{}} }

type IfaceParam struct{ s Shape }

func NewIfaceParam(s Shape) *IfaceParam { return &IfaceParam{s: s} }

type IfaceExt struct{ w io.Writer }

func NewIfaceExt() *IfaceExt { return &IfaceExt{w: &bytes.Buffer{}} }

type IfaceNone struct{ s Shape }
type FuncField struct{ f func() }

type CycA struct{ b *CycB }
type CycB struct {
	a *CycA
	n int
}

func (b *CycB) Bump() { b.n++ }

type Self struct {
	next *Self
	n    int
}

// Shareable overrides.
type Over struct {
	mu  *sync.Mutex
	rw  *sync.RWMutex
	n   *atomic.Int64
	ctx context.Context
	err error
	t   time.Time
	lg  *log.Logger
	re  *regexp.Regexp
}

// Intended changes: a shareable module struct held by value (or embedded), a channel, a pointer
// override over a module type.
type ValSetter struct{ s Setter }
type ValPlain struct {
	p  Plain
	xs []int
}
type EmbSetter struct{ Setter }
type ArrSetter struct{ a [2]Setter }
type ChanSetter struct{ c chan *Setter }

var errX = errors.New("x")

func Serve(plain *Plain, setter *Setter, lockedW *LockedW, lockedBoth *LockedBoth, ext *Ext, extPtr *ExtPtr, exported *Exported,
	sliceFrozen *SliceFrozen, sliceW *SliceW, mapFrozen *MapFrozen, mapW *MapW, mapLocked *MapLocked, ptrPlain *PtrPlain,
	guarded *Guarded, frozen *Frozen, holdsFrozen *HoldsFrozen, holdsFrozenVal *HoldsFrozenVal,
	ptrChild *PtrChild, ptrChildPlain *PtrChildPlain, ifaceDirect *IfaceDirect, ifaceParam *IfaceParam, ifaceExt *IfaceExt, ifaceNone *IfaceNone,
	funcField *FuncField, cycA *CycA, cycB *CycB, self *Self, over *Over,
	valSetter *ValSetter, valPlain *ValPlain, embSetter *EmbSetter, arrSetter *ArrSetter, chanSetter *ChanSetter,
	xs []int, e error, ctx context.Context, f func(), w io.Writer, sh Shape, bp *bytes.Buffer, mu *sync.Mutex, m map[string]*Setter, st Setter) {
}
`

var adv2RegressNames = []string{
	"plain", "setter", "lockedW", "lockedBoth", "ext", "extPtr", "exported",
	"sliceFrozen", "sliceW", "mapFrozen", "mapW", "mapLocked", "ptrPlain",
	"guarded", "frozen", "holdsFrozen", "holdsFrozenVal",
	"ptrChild", "ptrChildPlain", "ifaceDirect", "ifaceParam", "ifaceExt", "ifaceNone",
	"funcField", "cycA", "cycB", "self", "over",
	"valSetter", "valPlain", "embSetter", "arrSetter", "chanSetter",
	"xs", "e", "ctx", "f", "w", "sh", "bp", "mu", "m", "st",
}

// adv2RegressIntended are the hatches whose closure the fix changes on purpose: each reaches a module
// struct through a field the old walk skipped because the field's type was shareable or not a struct.
var adv2RegressIntended = map[string]string{
	"valSetter":  "a Setter held by value is now a child (the field type is shareable, but the struct is written)",
	"valPlain":   "a Plain held by value is now a child: the closure gains OK beside C-frozen",
	"embSetter":  "an embedded Setter is a child",
	"arrSetter":  "[2]Setter is a by-value container of a child",
	"chanSetter": "chan *Setter reaches Setter",
}

func TestAdv2RegressionClasses(t *testing.T) {
	m := adv2Load(t, adv2RegressSrc)
	rep, _ := m.run(t, adv2RegressNames)
	adv2Dump(t, "regress", rep)
	got := adv2Classes(rep)
	if len(got) != len(adv2RegressNames) {
		t.Fatalf("%d hatches resolved, want %d", len(got), len(adv2RegressNames))
	}
	for _, n := range adv2RegressNames {
		if _, ok := adv2RegressIntended[n]; ok {
			continue
		}
		w, ok := adv2RegressOld[n]
		if !ok {
			t.Fatalf("no recorded pre-fix closure for %s", n)
		}
		if !slices.Equal(got[n], w) {
			t.Errorf("REGRESSION %s: closure %v, pre-fix %v", n, got[n], w)
		}
	}
	for n := range adv2RegressIntended {
		t.Logf("intended change %s: now %v, pre-fix %v (%s)", n, got[n], adv2RegressOld[n], adv2RegressIntended[n])
	}
}

// adv2RegressOld is the closure each hatch had at 6b401b9 (recorded from a run of that census.go).
var adv2RegressOld = map[string][]string{
	"arrSetter":      {classOK},
	"bp":             {classX},
	"chanSetter":     {classOK},
	"ctx":            {classOK},
	"cycA":           {classW},
	"cycB":           {classW},
	"e":              {classOK},
	"embSetter":      {classE},
	"exported":       {classE},
	"ext":            {classX},
	"extPtr":         {classX},
	"f":              {classX},
	"frozen":         {classOK},
	"funcField":      {classOK},
	"guarded":        {classOK},
	"holdsFrozen":    {classOK},
	"holdsFrozenVal": {classOK},
	"ifaceDirect":    {classOK},
	"ifaceExt":       {classX},
	"ifaceNone":      {classOK},
	"ifaceParam":     {classCFrozen, classOK},
	"lockedBoth":     {classG},
	"lockedW":        {classWLock},
	"m":              {classX},
	"mapFrozen":      {classCFrozen},
	"mapLocked":      {classG},
	"mapW":           {classCWritten},
	"mu":             {classOK},
	"over":           {classOK},
	"plain":          {classOK},
	"ptrChild":       {classW},
	"ptrChildPlain":  {classOK},
	"ptrPlain":       {classCFrozen},
	"self":           {classOK},
	"setter":         {classW},
	"sh":             {classCFrozen, classOK},
	"sliceFrozen":    {classCFrozen},
	"sliceW":         {classCWritten, classW},
	"st":             {classW},
	"valPlain":       {classCFrozen},
	"valSetter":      {classOK},
	"w":              {classX},
	"xs":             {classX},
}
