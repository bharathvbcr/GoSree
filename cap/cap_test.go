package cap_test

import (
	"testing"

	"github.com/bharathvbcr/gocap/cap"
)

type Point struct {
	X, Y int
}

func (p *Point) Set(x, y int) {
	p.X = x
	p.Y = y
}

func (p *Point) Sum() int {
	return p.X + p.Y
}

// TestTypeEquivalence verifies that cap marker type aliases are bit-for-bit
// identical to standard Go pointer types with zero conversion overhead.
func TestTypeEquivalence(t *testing.T) {
	val := 100
	var rawPtr *int = &val

	// Test Mut
	var mutPtr cap.Mut[int] = rawPtr
	if *mutPtr != 100 {
		t.Fatalf("Mut deref got %d, want 100", *mutPtr)
	}
	*mutPtr = 200
	if val != 200 {
		t.Fatalf("Mut mutation failed: val is %d, want 200", val)
	}
	var rawBack *int = mutPtr
	if rawBack != &val {
		t.Fatalf("Mut round-trip pointer mismatch")
	}

	// Test Ref
	var refPtr cap.Ref[int] = rawPtr
	if *refPtr != 200 {
		t.Fatalf("Ref deref got %d, want 200", *refPtr)
	}

	// Test Mub and Rob aliases
	var mubPtr cap.Mub[int] = rawPtr
	var robPtr cap.Rob[int] = rawPtr
	if *mubPtr != 200 || *robPtr != 200 {
		t.Fatalf("Mub/Rob aliases failed")
	}

	// Test Iso and Imm
	var isoPtr cap.Iso[int] = rawPtr
	var immPtr cap.Imm[int] = rawPtr
	if *isoPtr != 200 || *immPtr != 200 {
		t.Fatalf("Iso/Imm aliases failed")
	}

	// Nil compatibility
	var nilMut cap.Mut[int] = nil
	var nilRef cap.Ref[int] = nil
	var nilIso cap.Iso[int] = nil
	var nilImm cap.Imm[int] = nil
	if nilMut != nil || nilRef != nil || nilIso != nil || nilImm != nil {
		t.Fatalf("Nil pointer assignment failed")
	}
}

// TestMethodAndFieldAccess verifies direct struct field and method access
// through cap aliases without wrappers or boilerplate.
func TestMethodAndFieldAccess(t *testing.T) {
	pt := Point{X: 10, Y: 20}
	var m cap.Mut[Point] = &pt

	// Direct field access
	m.X = 30
	if pt.X != 30 {
		t.Fatalf("Direct field write failed: pt.X = %d", pt.X)
	}

	// Direct method call
	m.Set(40, 50)
	if pt.X != 40 || pt.Y != 50 {
		t.Fatalf("Direct method call failed: pt = %+v", pt)
	}

	var r cap.Ref[Point] = &pt
	if r.Sum() != 90 {
		t.Fatalf("Read method call failed: got %d, want 90", r.Sum())
	}
}

// TestDummyExpressions verifies that helper functions return identity values.
func TestDummyExpressions(t *testing.T) {
	val := 42
	ptr := &val

	m := cap.BorrowMut(ptr)
	if m != ptr {
		t.Fatalf("BorrowMut returned %p, want %p", m, ptr)
	}

	r := cap.Borrow(ptr)
	if r != ptr {
		t.Fatalf("Borrow returned %p, want %p", r, ptr)
	}

	r2 := cap.BorrowRef(ptr)
	if r2 != ptr {
		t.Fatalf("BorrowRef returned %p, want %p", r2, ptr)
	}

	m2 := cap.BorrowMub(ptr)
	if m2 != ptr {
		t.Fatalf("BorrowMub returned %p, want %p", m2, ptr)
	}

	r3 := cap.BorrowRob(ptr)
	if r3 != ptr {
		t.Fatalf("BorrowRob returned %p, want %p", r3, ptr)
	}

	// Move with various types
	if moved := cap.Move(val); moved != 42 {
		t.Fatalf("Move int got %d, want 42", moved)
	}
	if movedPtr := cap.Move(ptr); movedPtr != ptr {
		t.Fatalf("Move ptr got %p, want %p", movedPtr, ptr)
	}
	ch := make(chan int, 1)
	if movedCh := cap.Move(ch); movedCh != ch {
		t.Fatalf("Move chan mismatch")
	}
}

// TestZeroAllocs verifies zero memory allocations for all cap operations.
func TestZeroAllocs(t *testing.T) {
	val := 42
	ptr := &val

	allocs := testing.AllocsPerRun(1000, func() {
		m := cap.BorrowMut(ptr)
		_ = cap.Move(m)
		r := cap.Borrow(ptr)
		_ = cap.Move(r)
	})

	if allocs != 0 {
		t.Fatalf("Expected 0 allocs/op, got %f", allocs)
	}
}

func BenchmarkBorrowMut(b *testing.B) {
	val := 42
	ptr := &val
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cap.BorrowMut(ptr)
	}
}

func BenchmarkMove(b *testing.B) {
	val := 42
	ptr := &val
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cap.Move(ptr)
	}
}
