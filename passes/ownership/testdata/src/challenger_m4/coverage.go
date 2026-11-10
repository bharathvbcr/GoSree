package challenger_m4

// ============================================================================
// PART 1: RECURSIVE & MUTUALLY RECURSIVE IMMUTABILITY STRUCTS
// ============================================================================

// 1.1 Self-recursive immutable binary tree
//
//gosree:imm
type RecTree struct { // want RecTree:"imm"
	val   int
	left  *RecTree
	right *RecTree
}

func newRecTree() *RecTree {
	return &RecTree{
		val:   10,
		left:  &RecTree{val: 5},
		right: &RecTree{val: 15},
	}
}

// 1.2 Mutually recursive immutable structs (both valid)
//
//gosree:imm
type MutRecA struct { // want MutRecA:"imm"
	val int
	b   *MutRecB
}

//gosree:imm
type MutRecB struct { // want MutRecB:"imm"
	val int
	a   *MutRecA
}

func newMutCycle() *MutRecA {
	return &MutRecA{
		val: 1,
		b: &MutRecB{
			val: 2,
		},
	}
}

// 1.3 Mutually recursive structs with invalid field in one (fixpoint propagation)
//
//gosree:imm
type BadCycleA struct { // want `SAFE005: //gosree:imm type BadCycleA is not immutable: field b has type \*challenger_m4.BadCycleB`
	b *BadCycleB
}

//gosree:imm
type BadCycleB struct { // want `SAFE005: //gosree:imm type BadCycleB is not immutable: field leak has type \[\]int`
	a    *BadCycleA
	leak []int
}

// 1.4 Field write on recursive immutable tree
func writeRecTree(t *RecTree) {
	t.left.val = 42 // want `SAFE005: write to a field of RecTree`
}

// 1.5 Nested pointer overwrite on recursive immutable tree
func overwriteRecTreeChild(t *RecTree) {
	t.right = nil // want `SAFE005: write to a field of RecTree`
}

// 1.6 Mutable borrow of nested field of recursive immutable tree
func borrowRecTreeField(t *RecTree) {
	//gosree:mut
	b := &t.left.val // want `SAFE005: cannot borrow field of immutable type RecTree as mutable`
	_ = b
}

// 1.7 Local value declaration of imm struct reading field (clean pass via onlyReads)
func localImmValueRead() int {
	var c RecTree
	return c.val
}

// 1.8 Local value declaration of imm struct with write to field
func localImmValueWrite() {
	var c RecTree
	c.val = 10 // want `SAFE005: taking the address of variable c, a variable of //gosree:imm type RecTree` `SAFE005: write to a field of RecTree`
}

// 1.9 Taking address of immutable local value
func localImmValueAddr() *RecTree {
	var c RecTree
	return &c // want `SAFE005: taking the address of variable c`
}

// ============================================================================
// PART 2: GOROUTINE SPAWNS & MOVES INSIDE SWITCH AND SELECT
// ============================================================================

type packet struct {
	id   int
	data string
}

type service struct {
	val int
}

func (s *service) run() {}

func worker(p *int) {}

// 2.1 Switch statement with go func capturing \iso variable in one branch
func switchIsoGoroutineCapture(tag int) int {
	p := &packet{id: 1}
	switch tag {
	case 1:
		go func() {
			p.id = 10
		}()
	}
	val := p.id // want `SAFE003: variable p is used after it was captured by a goroutine`
	return val
}

// 2.2 Switch statement with go func capturing stack borrow
func switchMubGoroutineCapture(mode int) {
	val := 42
	switch mode {
	case 1:
		//gosree:mut
		b := &val
		go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
			*b = 100
		}()
	}
}

// 2.3 Select statement with go func capturing \iso in receive case
func selectIsoGoroutineCapture(ch <-chan int) int {
	p := &packet{id: 1}
	select {
	case <-ch:
		go func() {
			p.id = 99
		}()
	}
	val := p.id // want `SAFE003: variable p is used after it was captured by a goroutine`
	return val
}

// 2.4 Select statement with go func capturing \rob stack borrow
func selectRobGoroutineCapture(ch <-chan int) {
	val := 10
	select {
	case <-ch:
		//gosree:rob
		r := &val
		go func() { // want `CAP003: stack-scoped borrow 'r' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
			println(*r)
		}()
	}
}

// 2.5 Select statement with stack borrow passed to go argument
func selectGoArgBorrow(ch <-chan int) {
	val := 55
	select {
	case <-ch:
		//gosree:mut
		b := &val
		go worker(b) // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
	}
}

// 2.6 Method receiver started as goroutine with stack-scoped borrow
func methodReceiverBorrowGoroutine() {
	var s service
	//gosree:mut
	m := &s
	go m.run() // want `CAP003: stack-scoped borrow 'm' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// 2.7 Read-only borrow method receiver started as goroutine
func methodReceiverRobGoroutine() {
	var s service
	//gosree:rob
	r := &s
	go r.run() // want `CAP003: stack-scoped borrow 'r' \(\\rob\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// ============================================================================
// PART 3: MULTI-BRANCH SELECT STATEMENTS
// ============================================================================

// 3.1 Multi-branch select with multiple \iso send cases used inside case body
func selectMultiSendUseInside(ch1, ch2 chan *packet) {
	p1 := &packet{id: 1}
	p2 := &packet{id: 2}
	select {
	case ch1 <- p1:
		_ = p1.id // want `SAFE003: .* is used after it was sent on a channel in select`
	case ch2 <- p2:
		_ = p2.id // want `SAFE003: .* is used after it was sent on a channel in select`
	}
}

// 3.2 Multi-branch select with multiple stack-scoped borrow sends
func selectMultiBorrowSend(ch1, ch2 chan *int) {
	x := 10
	y := 20
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	select { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be sent on a channel`
	case ch1 <- b1:
	case ch2 <- b2:
	default:
	}
}

// 3.3 Select statement with send case and subsequent use after select
func selectSendUseAfter(ch chan *packet) int {
	p := &packet{id: 7}
	select {
	case ch <- p:
	default:
	}
	return p.id // want `SAFE003: .* is used after it was sent on a channel in select`
}

// ============================================================================
// PART 4: CHANNELS PASSING FUNCTIONS, CLOSURES & CAPABILITIES
// ============================================================================

// 4.1 Channel passing closure that captured \iso variable
func channelPassingClosure(ch chan func()) {
	p := &packet{id: 1}
	fn := func() {
		p.id = 100
	}
	ch <- fn
	_ = p.id // want `SAFE003: variable p is used after it was sent on a channel`
}

// 4.2 Channel of channels passing \iso capability
func channelOfChannels(pipe chan chan *packet) {
	c := make(chan *packet)
	p := &packet{id: 1}
	pipe <- c
	c <- p
	_ = p.id // want `SAFE003: .* is used after it was sent on a channel`
}

// 4.3 Conditional closure assignment with Phi passed to goroutine
func conditionalClosurePhi(cond bool) int {
	var fn func()
	p := &packet{id: 1}
	if cond {
		fn = func() { p.id = 10 }
	} else {
		fn = func() { p.id = 20 }
	}
	go fn()
	return p.id // want `SAFE003: variable p is used after it was captured by a goroutine`
}

// 4.4 Named function type conversion passed to goroutine
type action func()

func namedFuncTypeGoroutine() int {
	p := &packet{id: 1}
	act := action(func() { p.id = 99 })
	go act()
	return p.id // want `SAFE003: variable p is used after it was captured by a goroutine`
}

// ============================================================================
// PART 5: CFG PHI NODE EDGE COVERAGE (lastInstr)
// ============================================================================

// 5.1 Moved \iso value read directly into another variable / Phi edge
func phiEdgeUseAfterMove(ch chan *packet, cond bool) int {
	p := &packet{id: 1}
	ch <- p // want `SAFE003: .* is used after it was sent on a channel`
	var q *packet
	if cond {
		q = p
	} else {
		q = nil
	}
	if q != nil {
		return q.id
	}
	return 0
}
