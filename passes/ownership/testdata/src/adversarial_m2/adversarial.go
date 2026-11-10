package adversarial_m2

// ============================================================================
// PART 1: DEEP IMMUTABILITY VERIFICATION (\imm)
// ============================================================================

// 1.1 Primitive fields only (Clean Pass)
//
//gocap:imm
type PrimConfig struct { // want PrimConfig:"imm"
	host string
	port int
	rate float64
	flag bool
}

func newPrimConfig() *PrimConfig {
	return &PrimConfig{host: "localhost", port: 8080, rate: 1.5, flag: true}
}

// 1.2 Transitive immutability: pointing to another verified imm struct (Clean Pass)
//
//gocap:imm
type ChildConfig struct { // want ChildConfig:"imm"
	secret string
}

//gocap:imm
type ParentConfig struct { // want ParentConfig:"imm"
	child *ChildConfig
	count int
}

// 1.3 Recursive immutability: tree-like struct pointing to itself (Clean Pass)
//
//gocap:imm
type TreeNode struct { // want TreeNode:"imm"
	val   int
	left  *TreeNode
	right *TreeNode
}

// 1.4 Mutually recursive immutability: A points to B, B points to A (Clean Pass)
//
//gocap:imm
type NodeA struct { // want NodeA:"imm"
	b   *NodeB
	val int
}

//gocap:imm
type NodeB struct { // want NodeB:"imm"
	a   *NodeA
	val int
}

// 1.5 Array of verified imm pointers & array of primitives (Clean Pass)
//
//gocap:imm
type ArrayConfig struct { // want ArrayConfig:"imm"
	tags  [4]byte
	nodes [2]*ChildConfig
}

// 1.6 Unexported field enforcement: exported primitive field rejected (SAFE005)
//
//gocap:imm
type ExportedFieldStruct struct { // want `SAFE005: //gocap:imm type ExportedFieldStruct is not immutable: field Exported is exported`
	Exported int
	internal string
}

// 1.7 Field pointing to non-imm struct rejected (SAFE005)
type MutablePayload struct {
	data int
}

//gocap:imm
type NonImmPointerStruct struct { // want `SAFE005: //gocap:imm type NonImmPointerStruct is not immutable: field payload has type \*adversarial_m2.MutablePayload`
	payload *MutablePayload
}

// 1.8 Slice field rejected (SAFE005)
//
//gocap:imm
type SliceFieldStruct struct { // want `SAFE005: //gocap:imm type SliceFieldStruct is not immutable: field items has type \[\]int`
	items []int
}

// 1.9 Map field rejected (SAFE005)
//
//gocap:imm
type MapFieldStruct struct { // want `SAFE005: //gocap:imm type MapFieldStruct is not immutable: field lookup has type map\[string\]int`
	lookup map[string]int
}

// 1.10 Func field rejected (SAFE005)
//
//gocap:imm
type FuncFieldStruct struct { // want `SAFE005: //gocap:imm type FuncFieldStruct is not immutable: field callback has type func\(\)`
	callback func()
}

// 1.11 Interface field rejected (SAFE005)
//
//gocap:imm
type InterfaceFieldStruct struct { // want `SAFE005: //gocap:imm type InterfaceFieldStruct is not immutable: field anyVal has type any`
	anyVal any
}

// 1.12 Array of pointers to non-imm struct rejected (SAFE005)
//
//gocap:imm
type ArrayNonImmStruct struct { // want `SAFE005: //gocap:imm type ArrayNonImmStruct is not immutable: field ptrs has type \[2\]\*int`
	ptrs [2]*int
}

// 1.13 Rejection of mutable borrows on immutable fields (SAFE005)
//
//gocap:mub p
func takesMutBorrow(p *int) {}

func testImmBorrowRejections(cfg *PrimConfig) {
	// Taking a local //gocap:mut borrow of immutable struct field
	//gocap:mut
	b := &cfg.port // want `SAFE005: cannot borrow field of immutable type PrimConfig as mutable`
	_ = b

	// Passing field of immutable struct to mub parameter
	takesMutBorrow(&cfg.port) // want `SAFE005: cannot borrow field of immutable type PrimConfig as mutable`

	// Read-only borrow of immutable field is PERMITTED (Clean Pass)
	//gocap:rob
	r := &cfg.port
	_ = *r

	// Direct mutation rejected
	cfg.port = 9000 // want `SAFE005: write to a field of PrimConfig`
}

func testNestedImmBorrowRejections(p *ParentConfig) {
	// Taking mutable borrow on nested imm field
	//gocap:mut
	b := &p.child.secret // want `SAFE005: cannot borrow field of immutable type ChildConfig as mutable`
	_ = b
}

func testImmMutBorrowDirect(cfg *PrimConfig) {
	//gocap:mut
	b := cfg // want `SAFE005: cannot borrow immutable type PrimConfig as mutable`
	_ = b
}


func testImmOverwrite(cfg *PrimConfig) {
	*cfg = PrimConfig{host: "remote", port: 80} // want `SAFE005: store through a \*PrimConfig overwrites an immutable value`
}

func testImmStackAddress() *PrimConfig {
	var c PrimConfig
	return &c // want `SAFE005: taking the address of variable c`
}

// 1.14 Verified imm structs can be shared across goroutines without invalidation (Clean Pass)
func testImmGoroutineSharing(ch chan *PrimConfig) string {
	c := newPrimConfig()
	ch <- c
	go func() {
		_ = c.port
		_ = c.host
	}()
	return c.host // Clean Pass
}


// ============================================================================
// PART 2: STACK-SCOPED BORROW CONFINEMENT (CAP003)
// ============================================================================

func dummyWorker(p *int) {}
func dummyReader(p *int) {}

// 2.1 Channel sends of stack-scoped borrows rejected (CAP003)
//
//gocap:mub b
func escapeMubParamChannel(ch chan *int, b *int) {
	ch <- b // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
}

//gocap:rob r
func escapeRobParamChannel(ch chan *int, r *int) {
	ch <- r // want `CAP003: stack-scoped borrow 'r' \(\\rob\) cannot cross goroutine boundary: cannot be sent on a channel`
}

func escapeMubLocalChannel(ch chan *int) {
	x := 100
	//gocap:mut
	b := &x
	ch <- b // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
}

func escapeRobLocalChannel(ch chan *int) {
	x := 100
	//gocap:rob
	r := &x
	ch <- r // want `CAP003: stack-scoped borrow 'r' \(\\rob\) cannot cross goroutine boundary: cannot be sent on a channel`
}

// 2.2 Select statements: blocking & non-blocking
//
//gocap:mub b
func escapeMubSelectBlocking(ch chan *int, b *int) {
	select {
	case ch <- b: // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
	}
}

//gocap:rob r
func escapeRobSelectBlocking(ch chan *int, r *int) {
	select {
	case ch <- r: // want `CAP003: stack-scoped borrow 'r' \(\\rob\) cannot cross goroutine boundary: cannot be sent on a channel`
	}
}

//gocap:mub b
func escapeMubSelectNonBlocking(ch chan *int, b *int) {
	select { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
	case ch <- b:
	default:
	}
}

//gocap:rob r
func escapeRobSelectNonBlocking(ch chan *int, r *int) {
	select { // want `CAP003: stack-scoped borrow 'r' \(\\rob\) cannot cross goroutine boundary: cannot be sent on a channel`
	case ch <- r:
	default:
	}
}

// 2.3 Goroutine closures: capturing borrows rejected (CAP003)
//
//gocap:mub b
func escapeMubParamGoClosure(b *int) {
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		*b = 99
	}()
}

//gocap:rob r
func escapeRobParamGoClosure(r *int) {
	go func() { // want `CAP003: stack-scoped borrow 'r' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		println(*r)
	}()
}

func escapeMubLocalGoClosure() {
	x := 42
	//gocap:mut
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		*b = 50
	}()
}

func escapeRobLocalGoClosure() {
	x := 42
	//gocap:rob
	r := &x
	go func() { // want `CAP003: stack-scoped borrow 'r' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		println(*r)
	}()
}

// Nested closure inside go statement capturing borrow rejected
//
//gocap:mub b
func escapeMubNestedGoClosure(b *int) {
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		func() {
			*b = 100
		}()
	}()
}

// 2.4 Goroutine arguments: passing borrows as arguments to go rejected (CAP003)
//
//gocap:mub b
func escapeMubParamGoArg(b *int) {
	go dummyWorker(b) // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

//gocap:rob r
func escapeRobParamGoArg(r *int) {
	go dummyReader(r) // want `CAP003: stack-scoped borrow 'r' \(\\rob\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

func escapeMubLocalGoArg() {
	x := 10
	//gocap:mut
	b := &x
	go dummyWorker(b) // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

func escapeRobLocalGoArg() {
	x := 10
	//gocap:rob
	r := &x
	go dummyReader(r) // want `CAP003: stack-scoped borrow 'r' \(\\rob\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// Anonymous goroutine argument
//
//gocap:mub b
func escapeMubAnonGoArg(b *int) {
	go func(p *int) { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
		*p = 10
	}(b)
}


// ============================================================================
// PART 3: SYNCHRONOUS CLOSURES (Clean Pass - NO ERROR)
// ============================================================================

// 3.1 Directly invoked synchronous closure mutating \mub parameter (Clean Pass)
//
//gocap:mub b
func syncClosureMutParam(b *int) {
	func() {
		*b = 50
	}()
}

// 3.2 Directly invoked synchronous closure reading \rob parameter (Clean Pass)
//
//gocap:rob r
func syncClosureRobParam(r *int) int {
	var res int
	func() {
		res = *r
	}()
	return res
}

// 3.3 Synchronous closure mutating local \mub (Clean Pass)
func syncClosureMutLocal() int {
	x := 10
	//gocap:mut
	b := &x
	func() {
		*b = 50
	}()
	return *b
}

// 3.4 Nested synchronous closures mutating \mub (Clean Pass)
//
//gocap:mub b
func syncNestedClosures(b *int) {
	func() {
		func() {
			*b = 999
		}()
	}()
}

// 3.5 Synchronous closure with argument (Clean Pass)
//
//gocap:mub b
func syncClosureWithArg(b *int) {
	func(p *int) {
		*p = 77
	}(b)
}

// ============================================================================
// PART 4: ADVERSARIAL EDGE CASES
// ============================================================================

// 4.1 Multi-case select statement
//
//gocap:mub b
func escapeMubSelectMultiCase(ch1 chan int, ch2 chan *int, b *int) {
	select { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
	case ch1 <- 1:
	case ch2 <- b:
	}
}

// 4.2 Multi-arg go statement: non-borrow argument followed by borrow argument
//
//gocap:mub b
func escapeMubSecondArg(b *int) {
	go func(val int, ptr *int) {}(42, b) // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// 4.3 Closure assigned to variable and started with go
//
//gocap:mub b
func escapeMubClosureVar(b *int) {
	fn := func() {
		*b = 100
	}
	go fn() // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
}

// 4.4 Method started as goroutine on borrow receiver
type counter struct {
	val int
}

func (c *counter) inc() {
	c.val++
}

//gocap:mub c
func escapeMubReceiver(c *counter) {
	go c.inc() // want `CAP003: stack-scoped borrow 'c' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// 4.5 Local borrow receiver in go statement
func escapeMubLocalReceiver() {
	cnt := counter{val: 0}
	//gocap:mut
	c := &cnt
	go c.inc() // want `CAP003: stack-scoped borrow 'c' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// 4.6 Reassigned borrow escaping on channel
//
//gocap:mub b
func escapeMubReassignedChannel(ch chan *int, b *int) {
	c := b
	ch <- c // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
}

// 4.7 Reassigned borrow captured in go closure
//
//gocap:mub b
func escapeMubReassignedClosure(b *int) {
	c := b
	go func() { // want `SAFE002: the closure is captured by a goroutine but is not owned here: it may alias parameter b`
		*c = 50
	}()
}

// 4.8 Reassigned borrow passed as go arg
//
//gocap:mub b
func escapeMubReassignedGoArg(b *int) {
	c := b
	go dummyWorker(c) // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// 4.9 Synchronous closure sending borrow on channel
//
//gocap:mub b
func syncClosureSend(ch chan *int, b *int) {
	func() {
		ch <- b // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
	}()
}

// 4.10 Multiline channel send of local borrow
func escapeMubMultilineSend(ch chan *int) {
	x := 10
	//gocap:mut
	b := &x
	ch <- // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
		b
}

// 4.11 Mutable borrow of a field in a sub-struct of an immutable struct
type plainSub struct {
	value int
}

//gocap:imm
type ImmHolder struct { // want ImmHolder:"imm"
	sub plainSub
}

//gocap:mub p
func mutateInt(p *int) {
	*p = 999
}

func attackSubField(h *ImmHolder) {
	// Taking mutable borrow on sub-field of immutable struct
	//gocap:mut
	b := &h.sub.value // want `SAFE005: cannot borrow field of immutable type ImmHolder as mutable`
	_ = b

	// Passing sub-field of immutable struct to mub function
	mutateInt(&h.sub.value) // want `SAFE005: cannot borrow field of immutable type ImmHolder as mutable`
}



