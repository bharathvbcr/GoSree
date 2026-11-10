package challenger_m2_gate4_imm

//gosree:imm
type SubConfig struct { // want SubConfig:"imm"
	value int
	desc  string
}

//gosree:imm
type HostConfig struct { // want HostConfig:"imm"
	sub  *SubConfig
	rate int
}

type NormalData struct {
	count int
}

// -------------------------------------------------------------------------
// Dimension 1: \imm struct and nested field immutable borrows (&h.sub.value)
// -------------------------------------------------------------------------

// 1.1 Read nested field via read-only borrow (valid local borrow, clean pass)
func readNestedFieldImmBorrow(h *HostConfig) int {
	r := &h.sub.value
	return *r
}

// 1.2 Mutable borrow of nested field of immutable struct must be rejected
func mutBorrowNestedFieldImm(h *HostConfig) {
	//gosree:mut
	m := &h.sub.value // want `SAFE005: cannot borrow field of immutable type SubConfig as mutable`
	_ = m
}

// 1.3 Direct write to nested field of immutable struct must be rejected
func writeNestedField(h *HostConfig) {
	h.sub.value = 42 // want `SAFE005: write to a field of SubConfig`
}

// 1.4 Address of imm local variable must be rejected
func addrOfImmVar() *HostConfig {
	var h HostConfig
	return &h // want `SAFE005: taking the address of variable h`
}

// 1.5 Overwrite imm struct through pointer must be rejected
func overwriteImm(h *HostConfig) {
	*h = HostConfig{} // want `SAFE005: store through a \*HostConfig overwrites an immutable value`
}

// 1.6 Stack-scoped read-only borrow of nested field cannot escape into a goroutine
func nestedFieldBorrowEscape(h *HostConfig) {
	//gosree:rob
	r := &h.sub.value
	go func() { // want `CAP003: stack-scoped borrow 'r' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		_ = *r
	}()
}

// -------------------------------------------------------------------------
// Dimension 2: Direct pointer bindings (b := cfg)
// -------------------------------------------------------------------------

// 2.1 Direct pointer binding without borrow annotation can be shared freely across goroutines
func directPointerBindingShare(cfg *HostConfig) int {
	b := cfg
	go func() {
		_ = b.sub.value
	}()
	return b.rate
}

// 2.2 Direct pointer binding marked as \rob cannot escape into a goroutine
func directPointerBindingRobEscape(cfg *HostConfig) {
	//gosree:rob
	b := cfg
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		_ = b.rate
	}()
}

// 2.3 Direct pointer binding marked as \mut on immutable type must be rejected
func directPointerBindingMutReject(cfg *HostConfig) {
	//gosree:mut
	b := cfg // want `SAFE005: cannot borrow immutable type HostConfig as mutable`
	_ = b
}

// -------------------------------------------------------------------------
// Dimension 3: Receiver parameter interactions with goroutines and closures
// -------------------------------------------------------------------------

func (h *HostConfig) value() int {
	return h.sub.value
}

// 3.1 Immutable receiver can spawn goroutine reading its fields
func (h *HostConfig) spawnReader() int {
	go func() {
		_ = h.sub.value
	}()
	return h.rate
}

// 3.2 Method value on immutable receiver can be started as a goroutine
func runMethodAsGoroutine(h *HostConfig) {
	go h.value()
}

// 3.3 Normal struct method receiver marked as \rob cannot escape into a goroutine
//gosree:rob h
func (h *NormalData) escapeRobReceiver() {
	go func() { // want `CAP003: stack-scoped borrow 'h' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		_ = h.count
	}()
}

// 3.4 Normal struct method receiver marked as \mub cannot escape into a goroutine
//gosree:mub h
func (h *NormalData) escapeMubReceiver() {
	go func() { // want `CAP003: stack-scoped borrow 'h' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		h.count = 100
	}()
}

func (h *NormalData) read() int {
	return h.count
}

// 3.5 Method receiver marked as \rob passed to go call
//gosree:rob h
func (h *NormalData) escapeRobReceiverMethodGo() {
	go h.read() // want `CAP003: stack-scoped borrow 'h' \(\\rob\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// -------------------------------------------------------------------------
// Dimension 4: Absence of spurious SAFE002 on closures capturing immutable values or borrows
// -------------------------------------------------------------------------

// 4.1 Closure capturing only immutable value: 0 diagnostics
func captureOnlyImm(cfg *HostConfig) int {
	go func() {
		_ = cfg.sub.value
	}()
	return cfg.rate
}

// 4.2 Closure capturing immutable value and unowned parameter: SAFE002 only on unowned parameter
func captureImmAndUnowned(cfg *HostConfig, p *NormalData) {
	go func() { // want `SAFE002: the closure is captured by a goroutine but is not owned here: it may alias parameter p`
		_ = cfg.rate
		_ = p.count
	}()
}

// 4.3 Closure capturing immutable value and stack-scoped borrow: CAP003 only on borrow
func captureImmAndBorrow(cfg *HostConfig) int {
	x := 42
	//gosree:mut
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		_ = cfg.rate + *b
	}()
	return cfg.rate
}

// 4.4 Closure capturing immutable value, \iso variable, and stack-scoped borrow:
// CAP003 on borrow, SAFE003 on isoP, NO spurious SAFE002 or SAFE005 on cfg.
func captureImmIsoAndBorrow(cfg *HostConfig) int {
	isoP := &NormalData{count: 1}
	x := 42
	//gosree:rob
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		_ = cfg.rate + *b
		isoP.count = 2
	}()
	isoP.count = 3 // want `SAFE003: variable isoP is used after it was captured by a goroutine`
	return cfg.rate
}

// 4.5 Channel send with immutable value and multi-goroutine fan-out
func fanOutImm(cfg *HostConfig) {
	ch := make(chan *HostConfig, 2)
	ch <- cfg
	go func() {
		_ = cfg.rate
	}()
	go func() {
		_ = cfg.sub.value
	}()
	_ = cfg.sub.desc
}

// 4.6 Sending a stack-scoped borrow of an immutable field on a channel must be rejected
func sendImmBorrowOnChannel(cfg *HostConfig, ch chan *int) {
	//gosree:rob
	b := &cfg.sub.value
	ch <- b // want `CAP003: stack-scoped borrow 'b' \(\\rob\) cannot cross goroutine boundary: cannot be sent on a channel`
}

// 4.7 Multi-level nested immutable struct mutable borrow
//gosree:imm
type Level3 struct { // want Level3:"imm"
	val int
}

//gosree:imm
type Level2 struct { // want Level2:"imm"
	l3 *Level3
}

//gosree:imm
type Level1 struct { // want Level1:"imm"
	l2 *Level2
}

// 4.8 Closure capturing immutable borrow and \iso variable with use-after-move
func captureImmFieldRobAndIso(h *HostConfig) int {
	p := &NormalData{count: 10}
	//gosree:rob
	r := &h.sub.value
	go func() { // want `CAP003: stack-scoped borrow 'r' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		p.count = *r
	}()
	p.count = 20 // want `SAFE003: variable p is used after it was captured by a goroutine`
	return p.count
}

// 4.9 Closure capturing immutable struct, multiple distinct borrows, and \iso variable
func captureImmMultiBorrowsIso(h *HostConfig) int {
	p := &NormalData{count: 10}
	x := 1
	y := 2
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	go func() { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		p.count = *b1 + *b2 + h.rate
	}()
	p.count = 99 // want `SAFE003: variable p is used after it was captured by a goroutine`
	return p.count
}

// 4.10 Immutable method taking borrow argument called via go statement
//gosree:mut b
func (h *HostConfig) mutateWithBorrow(b *int) {
	*b = h.rate
}

func callImmMethodWithBorrowGo(h *HostConfig) {
	x := 42
	//gosree:mut
	b := &x
	go h.mutateWithBorrow(b) // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// 4.11 Rejection of non-immutable types annotated with //gosree:imm
//gosree:imm
type BadImm struct { // want `SAFE005: //gosree:imm type BadImm is not immutable: field ptr has type \*int`
	ptr *int
}

//gosree:imm
type BadSlice struct { // want `SAFE005: //gosree:imm type BadSlice is not immutable: field items has type \[\]int`
	items []int
}

//gosree:imm
type BadMap struct { // want `SAFE005: //gosree:imm type BadMap is not immutable: field table has type map\[string\]int`
	table map[string]int
}

