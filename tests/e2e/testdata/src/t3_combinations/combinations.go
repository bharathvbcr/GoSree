package t3_combinations

import "sync"

// =========================================================================
// Pair 1: \iso with Second-Class Borrows
// =========================================================================

type Item struct {
	Val int
}

// Sub-test 1A: Local borrow taken from \iso object; finishes borrow (NLL); then transfers \iso (Clean Pass)
func IsoBorrowThenTransfer(ch chan *Item) {
	it := &Item{Val: 10}
	//gocap:mut
	b := &it.Val
	*b = 20 // borrow ends here
	ch <- it // Valid: no active borrow at transfer point
}

// Sub-test 1B: Attempting to send \iso over channel while local mutable borrow is still active (BORROW003 / SAFE003)
func IsoTransferDuringActiveBorrow(ch chan *Item) {
	it := &Item{Val: 10}
	//gocap:mut
	b := &it.Val
	ch <- it // want `BORROW003: cannot move 'it' because it is borrowed`
	*b = 30  // want `SAFE003: composite literal is used after it was sent on a channel`
}

// =========================================================================
// Pair 2: \imm with Methods & Second-Class Read-Only Borrows
// =========================================================================

//gocap:imm
type AppConfig struct {
	timeoutMs int
}

//gocap:rob c
func (c *AppConfig) GetTimeout() int {
	return c.timeoutMs
}

// Sub-test 2A: Calling read-only method on \imm via read-only borrow (Clean Pass)
func ValidImmReadBorrow() int {
	cfg := &AppConfig{timeoutMs: 5000}
	//gocap:rob
	r := cfg
	return r.GetTimeout()
}

// Sub-test 2B: Attempting mutable borrow on \imm struct (SAFE005 / BORROW002)
func RejectMutBorrowOnImm() {
	cfg := &AppConfig{timeoutMs: 5000}
	//gocap:mut
	m := &cfg.timeoutMs // want `SAFE005: cannot borrow field of immutable type AppConfig as mutable`
	*m = 1000           // want `SAFE005: write to a field of AppConfig, which is //gocap:imm`
}

// =========================================================================
// Pair 3: Fork-Join with Channel Sends and Goroutine Captures
// =========================================================================

type Task struct {
	Result int
}

// Sub-test 3A: Goroutine captures \iso task, mutates it, notifies master via WaitGroup; master waits and accesses (Clean Pass)
func ForkJoinTaskProcessing() int {
	t := &Task{Result: 0}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t.Result = 42
	}()
	wg.Wait()
	return t.Result // Clean Pass: ownership safely restored
}

// =========================================================================
// Pair 4: Transient Stack Struct Tuple with Function Borrow Parameters
// =========================================================================

type PairRef struct {
	First  *int
	Second *int
}

//gocap:mut p
func SumPairRef(p *PairRef) int {
	return *p.First + *p.Second
}

func TransientTupleWithBorrowParams() int {
	a, b := 10, 20
	//gocap:mut
	ma := &a
	//gocap:mut
	mb := &b
	pair := PairRef{First: ma, Second: mb}
	return SumPairRef(&pair)
}

// =========================================================================
// Pair 5: Multiple Goroutines Sharing \imm While Transferring Disjoint \iso Tokens
// =========================================================================

//gocap:iso item1
//gocap:iso item2
func SharedImmWithDisjointIso(cfg *AppConfig, item1 *Item, item2 *Item, out chan *Item) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if cfg.timeoutMs > 0 {
			item1.Val += 1
		}
		out <- item1
	}()
	go func() {
		defer wg.Done()
		if cfg.timeoutMs > 0 {
			item2.Val += 1
		}
		out <- item2
	}()
	wg.Wait()
}

// =========================================================================
// Pair 6: Method Receiver Mutable Borrow Followed by Read-Only Borrow
// =========================================================================

type Accumulator struct {
	Total int
}

//gocap:mut a
func (a *Accumulator) Add(v int) {
	a.Total += v
}

//gocap:rob a
func (a *Accumulator) Current() int {
	return a.Total
}

func SequentialMethodBorrows() int {
	acc := Accumulator{Total: 0}
	acc.Add(10) // &mut borrow of acc
	acc.Add(20) // sequential &mut borrow
	return acc.Current() // &rob borrow after mut borrows complete
}

// =========================================================================
// Pair 7: Synchronous Closures vs Asynchronous Goroutine Closures
// =========================================================================

// Sub-test 7A: Synchronous closure verifying local borrow inline (Clean Pass)
func SyncClosureBorrow() int {
	x := 10
	//gocap:mut
	b := &x
	func() {
		*b = 50
	}()
	return x
}

// Sub-test 7B: Asynchronous goroutine closure attempting to capture local borrow (CAP003)
func AsyncClosureBorrowEscape() {
	x := 10
	//gocap:mut
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary`
		*b = 50
	}()
}

// =========================================================================
// Pair 8: Channel Select Statement with \iso Send and Fallback
// =========================================================================

func SelectIsoSendWithFallback(ch chan *Item) bool {
	it := &Item{Val: 99}
	select {
	case ch <- it:
		return true
	default:
		// Not moved on fallback!
		return it.Val > 0
	}
}
