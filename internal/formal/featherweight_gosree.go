// Package formal implements Featherweight GoSree: an executable small-step
// concurrent operational semantics, vector-clock happens-before + multi-word
// tearing detector, static capability checker, and contracted Function Hook
// model checker for GoSree's permission and override system.
package formal

import (
	"fmt"
	"strings"
)

// Regime selects whether stdlib overrides (`atomic.Pointer[T]`, `error`,
// `context.CancelFunc`) are checked under uncontracted blanket overrides or under
// contracted function hooks (capability preconditions at each call boundary).
type Regime int

const (
	RegimeUncontractedOverrides Regime = iota
	RegimeContractedHooks
)

// Cap represents a GoSree reference capability in the permission lattice.
type Cap int

const (
	CapInvalid Cap = iota
	CapIso         // \iso *T — affine unique owner, linearly consumed on Move / Spawn / AtomicStore
	CapMub         // \mub *T — second-class exclusive mutable borrow
	CapRob         // \rob *T — second-class read-only borrow
	CapImm         // \imm *T — transitively immutable, freely duplicable across goroutines
	CapMutex       // guarded by a specific sync.Mutex ID
)

func (c Cap) String() string {
	switch c {
	case CapIso:
		return "iso"
	case CapMub:
		return "mub"
	case CapRob:
		return "rob"
	case CapImm:
		return "imm"
	case CapMutex:
		return "mutex_guarded"
	default:
		return "invalid"
	}
}

// FieldKind distinguishes single-word scalar fields from multi-word Go headers
// (2-word slice/interface headers that tear under unsynchronized concurrent writes)
// and runtime maps (which fatally abort on concurrent writes).
type FieldKind int

const (
	FieldScalar FieldKind = iota
	FieldSlice2Word
	FieldIface2Word
	FieldMap
)

// TypeDesc describes the static layout and immutability annotation of a heap object.
type TypeDesc struct {
	Name      string
	SafeImm   bool // marked //gosree:imm and verified transitively free of mutable fields
	FieldKind FieldKind
}

// Binding tracks a variable's static capability, target object type, and optional guarding mutex.
type Binding struct {
	Cap     Cap
	Type    TypeDesc
	MutexID int
	Moved   bool
}

// ValueWord0 / ValueWord1 represent up to two machine words of a Go field:
// - Slice:     Word0 = backing capacity, Word1 = slice length
// - Interface: Word0 = dynamic concrete type tag, Word1 = concrete payload magic tag
// - Scalar:    Word0 = scalar value
// - Map:       Word0 = last value written
type TwoWordValue struct {
	Word0 int
	Word1 int
}

// StmtKind enumerates small-step statements in Featherweight GoSree.
type StmtKind int

const (
	StmtAlloc StmtKind = iota
	StmtAtomicStore
	StmtAtomicLoad
	StmtReadField
	StmtWriteField
	StmtMapWrite
	StmtBoxError
	StmtReadError
	StmtMakeCancelFunc
	StmtCallCancelFunc
	StmtLock
	StmtUnlock
	StmtSpawn
)

// Stmt is one statement in a goroutine body. Multi-word reads and writes are
// automatically expanded into two micro-ops by the operational semantics so
// interleaved goroutines can observe genuine torn slice/interface headers.
type Stmt struct {
	Kind        StmtKind
	DstVar      string
	SrcVar      string
	AtomicCell  int
	MutexID     int
	AllocType   TypeDesc
	InitVal     TwoWordValue
	WriteVal    TwoWordValue
	CaptureVars []string
	SpawnBody   []Stmt
	SpawnArgs   map[string]string // childParam -> parentVar
}

// Program is a complete Featherweight GoSree concurrent program.
type Program struct {
	Name         string
	AtomicTypes  map[int]TypeDesc // atomicCellID -> pointee TypeDesc
	InitialLocks int              // number of sync.Mutex instances
	Main         []Stmt
}

// TypeError describes a static capability or Function Hook contract violation.
type TypeError struct {
	Where  string
	Reason string
}

func (e TypeError) Error() string {
	return fmt.Sprintf("%s: %s", e.Where, e.Reason)
}

// CheckProgram type-checks p under regime. Under RegimeContractedHooks, every
// stdlib override (`atomic.Pointer`, `error`, `context.CancelFunc`) enforces its
// capability precondition and postcondition contract.
func CheckProgram(p Program, regime Regime) []TypeError {
	env := map[string]Binding{}
	heldLocks := map[int]bool{}
	return checkBlock("main", p.AtomicTypes, env, heldLocks, p.Main, regime)
}

func deeplyShareablePointee(td TypeDesc) bool {
	if td.FieldKind == FieldMap {
		return false
	}
	return td.SafeImm
}

func checkBlock(
	scope string,
	atomicTypes map[int]TypeDesc,
	env map[string]Binding,
	heldLocks map[int]bool,
	stmts []Stmt,
	regime Regime,
) []TypeError {
	var errs []TypeError
	addErr := func(idx int, msg string, args ...any) {
		errs = append(errs, TypeError{
			Where:  fmt.Sprintf("%s[%d]", scope, idx),
			Reason: fmt.Sprintf(msg, args...),
		})
	}

	requireLive := func(idx int, v string) (Binding, bool) {
		b, ok := env[v]
		if !ok {
			addErr(idx, "unbound variable %q", v)
			return Binding{}, false
		}
		if b.Moved {
			addErr(idx, "use of moved iso variable %q (SAFE003)", v)
			return Binding{}, false
		}
		return b, true
	}

	for i, s := range stmts {
		switch s.Kind {
		case StmtAlloc:
			capKind := CapIso
			if s.AllocType.SafeImm {
				capKind = CapImm
			}
			if s.MutexID > 0 {
				capKind = CapMutex
			}
			env[s.DstVar] = Binding{
				Cap:     capKind,
				Type:    s.AllocType,
				MutexID: s.MutexID,
			}

		case StmtAtomicStore:
			b, ok := requireLive(i, s.SrcVar)
			if !ok {
				continue
			}
			if regime == RegimeContractedHooks {
				if b.Cap != CapIso && b.Cap != CapImm {
					addErr(i, "HookAtomicStore: publishing %q requires iso or imm capability, have %s", s.SrcVar, b.Cap)
				}
				if !deeplyShareablePointee(b.Type) {
					addErr(i, "HookAtomicStore: pointee type %s is not //gosree:imm (unsafe to share across AtomicPointer.Load without synchronization)", b.Type.Name)
				}
				if b.Cap == CapIso {
					b.Moved = true
					env[s.SrcVar] = b
				}
			}

		case StmtAtomicLoad:
			td, ok := atomicTypes[s.AtomicCell]
			if !ok {
				addErr(i, "unknown atomic.Pointer cell %d", s.AtomicCell)
				continue
			}
			if regime == RegimeContractedHooks {
				if !deeplyShareablePointee(td) {
					addErr(i, "HookAtomicLoad: atomic.Pointer[%s] pointee is mutable; Load cannot hand out unguarded shared mutable reference", td.Name)
				}
				// Postcondition of contracted Load(): yields read-only CapImm, NEVER CapMub!
				env[s.DstVar] = Binding{Cap: CapImm, Type: td}
			} else {
				// Uncontracted override bug: treats Load() as handing back a writable pointer!
				env[s.DstVar] = Binding{Cap: CapMub, Type: td}
			}

		case StmtReadField:
			b, ok := requireLive(i, s.SrcVar)
			if !ok {
				continue
			}
			if b.Cap == CapMutex && !heldLocks[b.MutexID] {
				addErr(i, "reading mutex-guarded %q without holding mutex %d", s.SrcVar, b.MutexID)
			}

		case StmtWriteField, StmtMapWrite:
			b, ok := requireLive(i, s.SrcVar)
			if !ok {
				continue
			}
			switch b.Cap {
			case CapImm, CapRob:
				addErr(i, "cannot mutate %q through read-only capability %s", s.SrcVar, b.Cap)
			case CapMutex:
				if !heldLocks[b.MutexID] {
					addErr(i, "mutating mutex-guarded %q without holding mutex %d", s.SrcVar, b.MutexID)
				}
			case CapIso, CapMub:
				// Allowed exclusively for unique owner or exclusive mutable borrow.
			default:
				addErr(i, "invalid capability %s on %q", b.Cap, s.SrcVar)
			}

		case StmtBoxError:
			b, ok := requireLive(i, s.SrcVar)
			if !ok {
				continue
			}
			if regime == RegimeContractedHooks {
				if b.Cap != CapImm || !deeplyShareablePointee(b.Type) {
					addErr(i, "HookBoxError: concrete error %q (%s, cap=%s) must be //gosree:imm to satisfy Shareable(error)", s.SrcVar, b.Type.Name, b.Cap)
				}
			}
			env[s.DstVar] = Binding{
				Cap:  CapImm,
				Type: TypeDesc{Name: "error", SafeImm: true, FieldKind: FieldScalar},
			}

		case StmtReadError:
			if _, ok := requireLive(i, s.SrcVar); !ok {
				continue
			}

		case StmtMakeCancelFunc:
			for _, capVar := range s.CaptureVars {
				b, ok := requireLive(i, capVar)
				if !ok {
					continue
				}
				if regime == RegimeContractedHooks {
					if b.Cap != CapImm && b.Cap != CapMutex {
						addErr(i, "HookCancelFunc: closure capture %q has non-shareable capability %s (must be imm or mutex_guarded)", capVar, b.Cap)
					}
				}
			}
			env[s.DstVar] = Binding{
				Cap:  CapImm,
				Type: TypeDesc{Name: "context.CancelFunc", SafeImm: true, FieldKind: FieldScalar},
			}

		case StmtCallCancelFunc:
			if _, ok := requireLive(i, s.SrcVar); !ok {
				continue
			}

		case StmtLock:
			heldLocks[s.MutexID] = true

		case StmtUnlock:
			if !heldLocks[s.MutexID] {
				addErr(i, "unlock of unheld mutex %d", s.MutexID)
			}
			delete(heldLocks, s.MutexID)

		case StmtSpawn:
			childEnv := map[string]Binding{}
			for childParam, parentVar := range s.SpawnArgs {
				b, ok := requireLive(i, parentVar)
				if !ok {
					continue
				}
				switch b.Cap {
				case CapMub, CapRob:
					addErr(i, "second-class borrow %q (%s) cannot escape into spawned goroutine (CAP003)", parentVar, b.Cap)
				case CapIso:
					// Affine transfer: parent loses ownership, child gains unique CapIso!
					b.Moved = true
					env[parentVar] = b
					childEnv[childParam] = Binding{Cap: CapIso, Type: b.Type, MutexID: b.MutexID}
				case CapImm, CapMutex:
					childEnv[childParam] = b
				}
			}
			childErrs := checkBlock(fmt.Sprintf("%s->go[%d]", scope, i), atomicTypes, childEnv, map[int]bool{}, s.SpawnBody, regime)
			errs = append(errs, childErrs...)
		}
	}
	return errs
}

// =============================================================================
// Small-Step Concurrent Operational Semantics & Exhaustive Schedule Explorer
// =============================================================================

// SafetyFaultKind classifies concrete runtime violations discovered during
// exhaustive schedule exploration.
type SafetyFaultKind string

const (
	FaultDataRace           SafetyFaultKind = "DataRace"
	FaultTornSliceOOB       SafetyFaultKind = "TornSliceOutOfBounds"
	FaultTornIfaceConfusion SafetyFaultKind = "TornInterfaceTypeConfusion"
	FaultConcurrentMapWrite SafetyFaultKind = "ConcurrentMapWriteFatal"
)

// SafetyFault records a concrete witness schedule and memory fault.
type SafetyFault struct {
	Kind      SafetyFaultKind
	ObjectID  int
	Goroutine int
	Detail    string
	Schedule  []int
}

// ExplorationSummary summarizes exhaustive small-step concurrent model checking.
type ExplorationSummary struct {
	ExploredStates     int
	CompletedSchedules int
	Faults             []SafetyFault
}

type microOpKind int

const (
	uAlloc microOpKind = iota
	uAtomicStore
	uAtomicLoad
	uReadW0
	uReadW1AndValidate
	uWriteW0
	uWriteW1
	uMapWrite
	uLock
	uUnlock
	uSpawn
	uMakeCancelClosure
	uCallCancelClosure
)

type microOp struct {
	kind        microOpKind
	dstVar      string
	srcVar      string
	captureVars []string
	atomicCell  int
	mutexID     int
	allocType   TypeDesc
	val         TwoWordValue
	spawnOps    []microOp
	spawnArgs   map[string]string
}

func lowerStmts(stmts []Stmt, cancelBodies map[string][]Stmt) []microOp {
	var out []microOp
	for _, s := range stmts {
		switch s.Kind {
		case StmtAlloc:
			out = append(out, microOp{
				kind:      uAlloc,
				dstVar:    s.DstVar,
				allocType: s.AllocType,
				val:       s.InitVal,
			})
		case StmtAtomicStore:
			out = append(out, microOp{
				kind:       uAtomicStore,
				srcVar:     s.SrcVar,
				atomicCell: s.AtomicCell,
			})
		case StmtAtomicLoad:
			out = append(out, microOp{
				kind:       uAtomicLoad,
				dstVar:     s.DstVar,
				atomicCell: s.AtomicCell,
			})
		case StmtReadField, StmtReadError:
			out = append(out,
				microOp{kind: uReadW0, srcVar: s.SrcVar},
				microOp{kind: uReadW1AndValidate, srcVar: s.SrcVar},
			)
		case StmtWriteField:
			out = append(out,
				microOp{kind: uWriteW0, srcVar: s.SrcVar, val: s.WriteVal},
				microOp{kind: uWriteW1, srcVar: s.SrcVar, val: s.WriteVal},
			)
		case StmtMapWrite:
			out = append(out, microOp{kind: uMapWrite, srcVar: s.SrcVar, val: s.WriteVal})
		case StmtBoxError:
			// Boxing copies the pointer reference to the underlying error object into DstVar.
			out = append(out, microOp{
				kind:   uReadW0, // Models reading/aliasing pointer variable SrcVar -> DstVar
				dstVar: s.DstVar,
				srcVar: s.SrcVar,
			})
		case StmtMakeCancelFunc:
			out = append(out, microOp{
				kind:        uMakeCancelClosure,
				dstVar:      s.DstVar,
				captureVars: append([]string(nil), s.CaptureVars...),
			})
		case StmtCallCancelFunc:
			out = append(out, microOp{
				kind:   uCallCancelClosure,
				srcVar: s.SrcVar,
			})
		case StmtLock:
			out = append(out, microOp{kind: uLock, mutexID: s.MutexID})
		case StmtUnlock:
			out = append(out, microOp{kind: uUnlock, mutexID: s.MutexID})
		case StmtSpawn:
			childOps := lowerStmts(s.SpawnBody, cancelBodies)
			argsCopy := map[string]string{}
			for k, v := range s.SpawnArgs {
				argsCopy[k] = v
			}
			out = append(out, microOp{
				kind:      uSpawn,
				spawnOps:  childOps,
				spawnArgs: argsCopy,
			})
		}
	}
	return out
}

type vclock [6]int

func (vc vclock) join(other vclock) vclock {
	var res vclock
	for i := range vc {
		if vc[i] >= other[i] {
			res[i] = vc[i]
		} else {
			res[i] = other[i]
		}
	}
	return res
}

func happensBefore(aG int, aVC vclock, bVC vclock) bool {
	return aVC[aG] <= bVC[aG]
}

type accessStamp struct {
	has       bool
	goroutine int
	vc        vclock
}

type heapObj struct {
	td         TypeDesc
	val        TwoWordValue
	captures   []int
	lastWrite0 accessStamp
	lastWrite1 accessStamp
	lastRead0  accessStamp
	lastRead1  accessStamp
	activeMapW int // >0 if another goroutine is mid-write or concurrently writing without hb
}

type goroutineState struct {
	ops      []microOp
	pc       int
	locals   map[string]int // varName -> heapObj index
	vc       vclock
	latched0 int // Word0 latched by uReadW0 before uReadW1AndValidate runs
}

type machineState struct {
	heap        []heapObj
	atomics     map[int]int
	atomicClock map[int]vclock
	mutexOwner  map[int]int // mutexID -> goroutine (-1 if unlocked)
	mutexClock  map[int]vclock
	gs          []goroutineState
}

func (m machineState) clone() machineState {
	cp := machineState{
		heap:        append([]heapObj(nil), m.heap...),
		atomics:     make(map[int]int, len(m.atomics)),
		atomicClock: make(map[int]vclock, len(m.atomicClock)),
		mutexOwner:  make(map[int]int, len(m.mutexOwner)),
		mutexClock:  make(map[int]vclock, len(m.mutexClock)),
		gs:          make([]goroutineState, len(m.gs)),
	}
	for k, v := range m.atomics {
		cp.atomics[k] = v
	}
	for k, v := range m.atomicClock {
		cp.atomicClock[k] = v
	}
	for k, v := range m.mutexOwner {
		cp.mutexOwner[k] = v
	}
	for k, v := range m.mutexClock {
		cp.mutexClock[k] = v
	}
	for i, g := range m.gs {
		loc := make(map[string]int, len(g.locals))
		for k, v := range g.locals {
			loc[k] = v
		}
		cp.gs[i] = goroutineState{
			ops:      g.ops,
			pc:       g.pc,
			locals:   loc,
			vc:       g.vc,
			latched0: g.latched0,
		}
	}
	return cp
}

// ExploreSchedules exhaustively explores interleaved concurrent schedules of p
// up to maxStates, checking vector-clock happens-before data races, 2-word slice
// out-of-bounds tearing, 2-word interface itab/data type confusion, and
// concurrent map writes.
func ExploreSchedules(p Program, maxStates int) ExplorationSummary {
	if maxStates <= 0 {
		maxStates = 25000
	}
	cancelBodies := map[string][]Stmt{}
	mainOps := lowerStmts(p.Main, cancelBodies)

	var initVC vclock
	initVC[0] = 1
	init := machineState{
		atomics:     map[int]int{},
		atomicClock: map[int]vclock{},
		mutexOwner:  map[int]int{},
		mutexClock:  map[int]vclock{},
		gs: []goroutineState{{
			ops:    mainOps,
			pc:     0,
			locals: map[string]int{},
			vc:     initVC,
		}},
	}
	for mu := 1; mu <= p.InitialLocks; mu++ {
		init.mutexOwner[mu] = -1
	}

	var summary ExplorationSummary
	seenFaultKinds := map[string]bool{}

	var dfs func(st machineState, sched []int)
	dfs = func(st machineState, sched []int) {
		if summary.ExploredStates >= maxStates {
			return
		}
		summary.ExploredStates++

		anyEnabled := false
		allDone := true
		for gid := range st.gs {
			if st.gs[gid].pc >= len(st.gs[gid].ops) {
				continue
			}
			allDone = false
			op := st.gs[gid].ops[st.gs[gid].pc]
			if op.kind == uLock && st.mutexOwner[op.mutexID] != -1 {
				continue
			}
			anyEnabled = true

			next := st.clone()
			nextSched := append(append([]int(nil), sched...), gid)
			faults := stepGoroutine(&next, gid, nextSched)
			for _, f := range faults {
				key := fmt.Sprintf("%s:%d", f.Kind, f.ObjectID)
				if !seenFaultKinds[key] {
					seenFaultKinds[key] = true
					summary.Faults = append(summary.Faults, f)
				}
			}
			dfs(next, nextSched)
		}
		if allDone || !anyEnabled {
			if allDone {
				summary.CompletedSchedules++
			}
		}
	}

	dfs(init, nil)
	return summary
}

func checkRace(prev accessStamp, curG int, curVC vclock) bool {
	if !prev.has || prev.goroutine == curG {
		return false
	}
	return !happensBefore(prev.goroutine, prev.vc, curVC)
}

func stepGoroutine(st *machineState, gid int, sched []int) []SafetyFault {
	g := &st.gs[gid]
	op := g.ops[g.pc]
	g.pc++
	var faults []SafetyFault

	switch op.kind {
	case uAlloc:
		id := len(st.heap)
		st.heap = append(st.heap, heapObj{
			td:  op.allocType,
			val: op.val,
			lastWrite0: accessStamp{
				has:       true,
				goroutine: gid,
				vc:        g.vc,
			},
			lastWrite1: accessStamp{
				has:       true,
				goroutine: gid,
				vc:        g.vc,
			},
		})
		g.locals[op.dstVar] = id
		g.vc[gid]++

	case uAtomicStore:
		objID := g.locals[op.srcVar]
		st.atomics[op.atomicCell] = objID
		st.atomicClock[op.atomicCell] = st.atomicClock[op.atomicCell].join(g.vc)
		g.vc[gid]++

	case uAtomicLoad:
		objID := st.atomics[op.atomicCell]
		g.vc = g.vc.join(st.atomicClock[op.atomicCell])
		g.locals[op.dstVar] = objID
		g.vc[gid]++

	case uReadW0:
		// Special case: StmtBoxError aliases SrcVar -> DstVar without dereferencing heap words yet.
		if op.dstVar != "" {
			g.locals[op.dstVar] = g.locals[op.srcVar]
			break
		}
		objID := g.locals[op.srcVar]
		obj := &st.heap[objID]
		if checkRace(obj.lastWrite0, gid, g.vc) {
			faults = append(faults, SafetyFault{
				Kind:      FaultDataRace,
				ObjectID:  objID,
				Goroutine: gid,
				Detail:    fmt.Sprintf("unsynchronized read of %s.word0 racing with concurrent write by G%d", obj.td.Name, obj.lastWrite0.goroutine),
				Schedule:  sched,
			})
		}
		g.latched0 = obj.val.Word0
		obj.lastRead0 = accessStamp{has: true, goroutine: gid, vc: g.vc}
		g.vc[gid]++

	case uReadW1AndValidate:
		objID := g.locals[op.srcVar]
		obj := &st.heap[objID]
		if checkRace(obj.lastWrite1, gid, g.vc) {
			faults = append(faults, SafetyFault{
				Kind:      FaultDataRace,
				ObjectID:  objID,
				Goroutine: gid,
				Detail:    fmt.Sprintf("unsynchronized read of %s.word1 racing with concurrent write by G%d", obj.td.Name, obj.lastWrite1.goroutine),
				Schedule:  sched,
			})
		}
		w0 := g.latched0
		w1 := obj.val.Word1
		obj.lastRead1 = accessStamp{has: true, goroutine: gid, vc: g.vc}

		switch obj.td.FieldKind {
		case FieldSlice2Word:
			// Word0 = backing allocation capacity, Word1 = slice length (`rs_torn_slice`)
			if w1 > w0 {
				faults = append(faults, SafetyFault{
					Kind:      FaultTornSliceOOB,
					ObjectID:  objID,
					Goroutine: gid,
					Detail:    fmt.Sprintf("torn 2-word slice header on %s: len=%d > backing_cap=%d (out-of-bounds read!)", obj.td.Name, w1, w0),
					Schedule:  sched,
				})
			}
		case FieldIface2Word:
			// Word0 = concrete itab tag, Word1 = concrete payload magic tag (`rs_torn_iface`)
			if w0 != w1 {
				faults = append(faults, SafetyFault{
					Kind:      FaultTornIfaceConfusion,
					ObjectID:  objID,
					Goroutine: gid,
					Detail:    fmt.Sprintf("torn 2-word interface header on %s: itab=%#x invoked on payload=%#x (type confusion!)", obj.td.Name, w0, w1),
					Schedule:  sched,
				})
			}
		}
		g.vc[gid]++

	case uWriteW0:
		objID := g.locals[op.srcVar]
		obj := &st.heap[objID]
		if checkRace(obj.lastWrite0, gid, g.vc) || checkRace(obj.lastRead0, gid, g.vc) {
			faults = append(faults, SafetyFault{
				Kind:      FaultDataRace,
				ObjectID:  objID,
				Goroutine: gid,
				Detail:    fmt.Sprintf("unsynchronized write to %s.word0", obj.td.Name),
				Schedule:  sched,
			})
		}
		obj.val.Word0 = op.val.Word0
		obj.lastWrite0 = accessStamp{has: true, goroutine: gid, vc: g.vc}
		g.vc[gid]++

	case uWriteW1:
		objID := g.locals[op.srcVar]
		obj := &st.heap[objID]
		if checkRace(obj.lastWrite1, gid, g.vc) || checkRace(obj.lastRead1, gid, g.vc) {
			faults = append(faults, SafetyFault{
				Kind:      FaultDataRace,
				ObjectID:  objID,
				Goroutine: gid,
				Detail:    fmt.Sprintf("unsynchronized write to %s.word1", obj.td.Name),
				Schedule:  sched,
			})
		}
		obj.val.Word1 = op.val.Word1
		obj.lastWrite1 = accessStamp{has: true, goroutine: gid, vc: g.vc}
		g.vc[gid]++

	case uMapWrite:
		objID := g.locals[op.srcVar]
		obj := &st.heap[objID]
		if checkRace(obj.lastWrite0, gid, g.vc) || checkRace(obj.lastRead0, gid, g.vc) {
			faults = append(faults,
				SafetyFault{
					Kind:      FaultConcurrentMapWrite,
					ObjectID:  objID,
					Goroutine: gid,
					Detail:    fmt.Sprintf("fatal error: concurrent map writes on %s", obj.td.Name),
					Schedule:  sched,
				},
				SafetyFault{
					Kind:      FaultDataRace,
					ObjectID:  objID,
					Goroutine: gid,
					Detail:    fmt.Sprintf("unsynchronized map write on %s", obj.td.Name),
					Schedule:  sched,
				},
			)
		}
		obj.val.Word0 = op.val.Word0
		obj.lastWrite0 = accessStamp{has: true, goroutine: gid, vc: g.vc}
		g.vc[gid]++

	case uLock:
		st.mutexOwner[op.mutexID] = gid
		g.vc = g.vc.join(st.mutexClock[op.mutexID])
		g.vc[gid]++

	case uUnlock:
		st.mutexClock[op.mutexID] = st.mutexClock[op.mutexID].join(g.vc)
		st.mutexOwner[op.mutexID] = -1
		g.vc[gid]++

	case uSpawn:
		childID := len(st.gs)
		childVC := g.vc
		if childID < len(childVC) {
			childVC[childID] = 1
		}
		childLocals := map[string]int{}
		for param, parentVar := range op.spawnArgs {
			childLocals[param] = g.locals[parentVar]
		}
		g.vc[gid]++
		st.gs = append(st.gs, goroutineState{
			ops:    op.spawnOps,
			pc:     0,
			locals: childLocals,
			vc:     childVC,
		})

	case uMakeCancelClosure:
		caps := make([]int, 0, len(op.captureVars))
		for _, cv := range op.captureVars {
			caps = append(caps, g.locals[cv])
		}
		id := len(st.heap)
		st.heap = append(st.heap, heapObj{
			td:       TypeDesc{Name: "context.CancelFunc", SafeImm: true, FieldKind: FieldScalar},
			captures: caps,
		})
		g.locals[op.dstVar] = id
		g.vc[gid]++

	case uCallCancelClosure:
		closureID := g.locals[op.srcVar]
		capturedIDs := append([]int(nil), st.heap[closureID].captures...)
		for _, targetID := range capturedIDs {
			obj := &st.heap[targetID]
			if checkRace(obj.lastWrite0, gid, g.vc) || checkRace(obj.lastRead0, gid, g.vc) {
				faults = append(faults, SafetyFault{
					Kind:      FaultDataRace,
					ObjectID:  targetID,
					Goroutine: gid,
					Detail:    fmt.Sprintf("unsynchronized closure capture mutation on %s by CancelFunc", obj.td.Name),
					Schedule:  sched,
				})
			}
			obj.val.Word0 = 999
			obj.lastWrite0 = accessStamp{has: true, goroutine: gid, vc: g.vc}
		}
		g.vc[gid]++
	}
	return faults
}

// FormatTypeErrors joins TypeError slices into a human-readable diagnostic list.
func FormatTypeErrors(errs []TypeError) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}
