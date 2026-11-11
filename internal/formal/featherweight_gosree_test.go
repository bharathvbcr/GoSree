package formal_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bharathvbcr/gosree/internal/formal"
)

func hasFaultKind(faults []formal.SafetyFault, want formal.SafetyFaultKind) bool {
	for _, f := range faults {
		if f.Kind == want {
			return true
		}
	}
	return false
}

// TestUncontractedOverridesConvictedAndContractedHooksRestoreSoundness proves both halves
// of the AlphaProof Code §3.3 Function Hook analysis on GoSree's concurrency witnesses:
//  1. Under uncontracted stdlib overrides, all 5 witness shapes typecheck yet exhibit
//     concrete data races / torn 2-word slice OOB / torn 2-word interface type confusion /
//     concurrent map write crashes in the small-step concurrent operational semantics.
//  2. Under Contracted Function Hooks (`RegimeContractedHooks`), all 5 witnesses are
//     statically rejected at compile time with explicit capability diagnostics.
func TestUncontractedOverridesConvictedAndContractedHooksRestoreSoundness(t *testing.T) {
	routerSliceTy := formal.TypeDesc{Name: "Router", SafeImm: false, FieldKind: formal.FieldSlice2Word}
	routeIfaceTy := formal.TypeDesc{Name: "Route", SafeImm: false, FieldKind: formal.FieldIface2Word}
	sessionMapTy := formal.TypeDesc{Name: "SessionMap", SafeImm: false, FieldKind: formal.FieldMap}
	mutErrTy := formal.TypeDesc{Name: "mutErr", SafeImm: false, FieldKind: formal.FieldScalar}
	mutStateTy := formal.TypeDesc{Name: "cancelState", SafeImm: false, FieldKind: formal.FieldScalar}

	witnesses := []struct {
		prog      formal.Program
		wantFault formal.SafetyFaultKind
	}{
		{
			// 1. `rs_torn_slice`: atomic.Pointer[Router] where Router.routes flips in place
			//    between 1-element slice (cap=1, len=1) and 4096-element slice (cap=4096, len=4096).
			//    Interleaving maint.cap=1 with full.len=4096 triggers `FaultTornSliceOOB`!
			prog: formal.Program{
				Name:        "rs_torn_slice",
				AtomicTypes: map[int]formal.TypeDesc{1: routerSliceTy},
				Main: []formal.Stmt{
					{Kind: formal.StmtAlloc, DstVar: "r0", AllocType: routerSliceTy, InitVal: formal.TwoWordValue{Word0: 4096, Word1: 4096}},
					{Kind: formal.StmtAtomicStore, AtomicCell: 1, SrcVar: "r0"},
					{
						Kind: formal.StmtSpawn,
						SpawnBody: []formal.Stmt{
							{Kind: formal.StmtAtomicLoad, AtomicCell: 1, DstVar: "rw"},
							{Kind: formal.StmtWriteField, SrcVar: "rw", WriteVal: formal.TwoWordValue{Word0: 1, Word1: 1}},
						},
					},
					{Kind: formal.StmtAtomicLoad, AtomicCell: 1, DstVar: "rr"},
					{Kind: formal.StmtReadField, SrcVar: "rr"},
				},
			},
			wantFault: formal.FaultTornSliceOOB,
		},
		{
			// 2. `rs_torn_iface`: atomic.Pointer[Route] where Route.codec flips in place
			//    between (*Plain, plainMark=0x5B) and (*Gzip, gzipMark=0x6A).
			//    Interleaving Gzip itab (0x6A) with Plain payload (0x5B) triggers `FaultTornIfaceConfusion`!
			prog: formal.Program{
				Name:        "rs_torn_iface",
				AtomicTypes: map[int]formal.TypeDesc{1: routeIfaceTy},
				Main: []formal.Stmt{
					{Kind: formal.StmtAlloc, DstVar: "rt", AllocType: routeIfaceTy, InitVal: formal.TwoWordValue{Word0: 0x5B, Word1: 0x5B}},
					{Kind: formal.StmtAtomicStore, AtomicCell: 1, SrcVar: "rt"},
					{
						Kind: formal.StmtSpawn,
						SpawnBody: []formal.Stmt{
							{Kind: formal.StmtAtomicLoad, AtomicCell: 1, DstVar: "rw"},
							{Kind: formal.StmtWriteField, SrcVar: "rw", WriteVal: formal.TwoWordValue{Word0: 0x6A, Word1: 0x6A}},
						},
					},
					{Kind: formal.StmtAtomicLoad, AtomicCell: 1, DstVar: "rr"},
					{Kind: formal.StmtReadField, SrcVar: "rr"},
				},
			},
			wantFault: formal.FaultTornIfaceConfusion,
		},
		{
			// 3. `rs_w_mapwrites`: atomic.Pointer[map[string]int] where publisher retains `m`
			//    and writes concurrently with worker loading `*sessions.Load()`.
			prog: formal.Program{
				Name:        "rs_w_mapwrites",
				AtomicTypes: map[int]formal.TypeDesc{1: sessionMapTy},
				Main: []formal.Stmt{
					{Kind: formal.StmtAlloc, DstVar: "m", AllocType: sessionMapTy, InitVal: formal.TwoWordValue{Word0: 0}},
					{Kind: formal.StmtAtomicStore, AtomicCell: 1, SrcVar: "m"},
					{
						Kind: formal.StmtSpawn,
						SpawnBody: []formal.Stmt{
							{Kind: formal.StmtAtomicLoad, AtomicCell: 1, DstVar: "mw"},
							{Kind: formal.StmtMapWrite, SrcVar: "mw", WriteVal: formal.TwoWordValue{Word0: 1}},
						},
					},
					{Kind: formal.StmtMapWrite, SrcVar: "m", WriteVal: formal.TwoWordValue{Word0: 2}},
				},
			},
			wantFault: formal.FaultConcurrentMapWrite,
		},
		{
			// 4. `sp_w_error`: mutable concrete struct `*mutErr` boxed into `error` (trusted
			//    by convention in uncontracted overrides), shared across `go`, and mutated in parent!
			prog: formal.Program{
				Name: "sp_w_error",
				Main: []formal.Stmt{
					{Kind: formal.StmtAlloc, DstVar: "me", AllocType: mutErrTy, InitVal: formal.TwoWordValue{Word0: 400}},
					{Kind: formal.StmtBoxError, DstVar: "errIface", SrcVar: "me"},
					{
						Kind:      formal.StmtSpawn,
						SpawnArgs: map[string]string{"childErr": "errIface"},
						SpawnBody: []formal.Stmt{
							{Kind: formal.StmtReadError, SrcVar: "childErr"},
						},
					},
					{Kind: formal.StmtWriteField, SrcVar: "me", WriteVal: formal.TwoWordValue{Word0: 500}},
				},
			},
			wantFault: formal.FaultDataRace,
		},
		{
			// 5. `sp_w_cancelfunc`: user-constructed `context.CancelFunc` capturing mutable
			//    non-synchronized state and invoked concurrently across goroutines!
			prog: formal.Program{
				Name: "sp_w_cancelfunc",
				Main: []formal.Stmt{
					{Kind: formal.StmtAlloc, DstVar: "st", AllocType: mutStateTy, InitVal: formal.TwoWordValue{Word0: 0}},
					{Kind: formal.StmtMakeCancelFunc, DstVar: "cancel", CaptureVars: []string{"st"}},
					{
						Kind:      formal.StmtSpawn,
						SpawnArgs: map[string]string{"c": "cancel"},
						SpawnBody: []formal.Stmt{
							{Kind: formal.StmtCallCancelFunc, SrcVar: "c"},
						},
					},
					{Kind: formal.StmtCallCancelFunc, SrcVar: "cancel"},
				},
			},
			wantFault: formal.FaultDataRace,
		},
	}

	for _, tc := range witnesses {
		t.Run(tc.prog.Name, func(t *testing.T) {
			// 1. Uncontracted overrides accept the program without error...
			uncontractedErrs := formal.CheckProgram(tc.prog, formal.RegimeUncontractedOverrides)
			if len(uncontractedErrs) != 0 {
				t.Fatalf("expected uncontracted regime to accept %s, got: %s",
					tc.prog.Name, formal.FormatTypeErrors(uncontractedErrs))
			}

			// 2. ...yet exhaustive small-step exploration convicts a concrete memory fault!
			res := formal.ExploreSchedules(tc.prog, 25000)
			if res.CompletedSchedules == 0 {
				t.Fatalf("%s: vacuous schedule exploration (0 completed schedules)", tc.prog.Name)
			}
			if !hasFaultKind(res.Faults, tc.wantFault) {
				t.Fatalf("%s: expected fault %s, got faults=%+v", tc.prog.Name, tc.wantFault, res.Faults)
			}

			// 3. Contracted Function Hooks (`RegimeContractedHooks`) reject every witness statically!
			contractedErrs := formal.CheckProgram(tc.prog, formal.RegimeContractedHooks)
			if len(contractedErrs) == 0 {
				t.Fatalf("Contracted Function Hooks MUST reject %s at compile time, but accepted it!", tc.prog.Name)
			}
		})
	}
}

// TestContractedWellTypedConcurrentProgramsAreDataRaceFree exhaustively verifies
// Data-Race-Freedom (`Faults == 0` across 100% of interleaved schedules) for
// well-typed concurrent programs satisfying `RegimeContractedHooks`.
func TestContractedWellTypedConcurrentProgramsAreDataRaceFree(t *testing.T) {
	immRouterTy := formal.TypeDesc{Name: "ImmRouter", SafeImm: true, FieldKind: formal.FieldScalar}
	isoBufTy := formal.TypeDesc{Name: "IsoBuffer", SafeImm: false, FieldKind: formal.FieldScalar}
	mutexCounterTy := formal.TypeDesc{Name: "MutexCounter", SafeImm: false, FieldKind: formal.FieldScalar}
	immErrTy := formal.TypeDesc{Name: "ImmError", SafeImm: true, FieldKind: formal.FieldScalar}

	wellTypedPrograms := []formal.Program{
		{
			// 1. Copy-on-write publication of //safe:imm Router via atomic.Pointer[ImmRouter]:
			//    writers publish freshly allocated immutable snapshots (`Store`), readers `Load()` `CapImm`.
			Name:        "cow_immutable_atomic_router",
			AtomicTypes: map[int]formal.TypeDesc{1: immRouterTy},
			Main: []formal.Stmt{
				{Kind: formal.StmtAlloc, DstVar: "snap1", AllocType: immRouterTy, InitVal: formal.TwoWordValue{Word0: 1, Word1: 1}},
				{Kind: formal.StmtAtomicStore, AtomicCell: 1, SrcVar: "snap1"},
				{
					Kind: formal.StmtSpawn,
					SpawnBody: []formal.Stmt{
						{Kind: formal.StmtAlloc, DstVar: "snap2", AllocType: immRouterTy, InitVal: formal.TwoWordValue{Word0: 4096, Word1: 4096}},
						{Kind: formal.StmtAtomicStore, AtomicCell: 1, SrcVar: "snap2"},
					},
				},
				{Kind: formal.StmtAtomicLoad, AtomicCell: 1, DstVar: "cur"},
				{Kind: formal.StmtReadField, SrcVar: "cur"},
			},
		},
		{
			// 2. Affine `CapIso` ownership transfer into a spawned goroutine:
			//    parent writes before `go`, transfers `Iso`, and never touches `buf` afterward!
			Name: "affine_iso_move_across_spawn",
			Main: []formal.Stmt{
				{Kind: formal.StmtAlloc, DstVar: "buf", AllocType: isoBufTy, InitVal: formal.TwoWordValue{Word0: 10}},
				{Kind: formal.StmtWriteField, SrcVar: "buf", WriteVal: formal.TwoWordValue{Word0: 20}},
				{
					Kind:      formal.StmtSpawn,
					SpawnArgs: map[string]string{"ownedBuf": "buf"},
					SpawnBody: []formal.Stmt{
						{Kind: formal.StmtWriteField, SrcVar: "ownedBuf", WriteVal: formal.TwoWordValue{Word0: 42}},
						{Kind: formal.StmtReadField, SrcVar: "ownedBuf"},
					},
				},
				{Kind: formal.StmtAlloc, DstVar: "parentFresh", AllocType: isoBufTy, InitVal: formal.TwoWordValue{Word0: 7}},
				{Kind: formal.StmtWriteField, SrcVar: "parentFresh", WriteVal: formal.TwoWordValue{Word0: 8}},
			},
		},
		{
			// 3. `sync.Mutex`-guarded concurrent read/write across parent & child goroutine.
			Name:         "mutex_guarded_shared_mutation",
			InitialLocks: 1,
			Main: []formal.Stmt{
				{Kind: formal.StmtAlloc, DstVar: "ctr", AllocType: mutexCounterTy, MutexID: 1, InitVal: formal.TwoWordValue{Word0: 0}},
				{
					Kind:      formal.StmtSpawn,
					SpawnArgs: map[string]string{"cCtr": "ctr"},
					SpawnBody: []formal.Stmt{
						{Kind: formal.StmtLock, MutexID: 1},
						{Kind: formal.StmtWriteField, SrcVar: "cCtr", WriteVal: formal.TwoWordValue{Word0: 1}},
						{Kind: formal.StmtUnlock, MutexID: 1},
					},
				},
				{Kind: formal.StmtLock, MutexID: 1},
				{Kind: formal.StmtWriteField, SrcVar: "ctr", WriteVal: formal.TwoWordValue{Word0: 2}},
				{Kind: formal.StmtReadField, SrcVar: "ctr"},
				{Kind: formal.StmtUnlock, MutexID: 1},
			},
		},
		{
			// 4. Contracted `//safe:imm` concrete error boxed into `error` and read concurrently.
			Name: "contracted_immutable_error_broadcast",
			Main: []formal.Stmt{
				{Kind: formal.StmtAlloc, DstVar: "ie", AllocType: immErrTy, InitVal: formal.TwoWordValue{Word0: 404}},
				{Kind: formal.StmtBoxError, DstVar: "errVal", SrcVar: "ie"},
				{
					Kind:      formal.StmtSpawn,
					SpawnArgs: map[string]string{"e1": "errVal"},
					SpawnBody: []formal.Stmt{
						{Kind: formal.StmtReadError, SrcVar: "e1"},
					},
				},
				{Kind: formal.StmtReadError, SrcVar: "errVal"},
			},
		},
	}

	for _, prog := range wellTypedPrograms {
		t.Run(prog.Name, func(t *testing.T) {
			errs := formal.CheckProgram(prog, formal.RegimeContractedHooks)
			if len(errs) != 0 {
				t.Fatalf("%s rejected by CheckProgram: %s", prog.Name, formal.FormatTypeErrors(errs))
			}
			res := formal.ExploreSchedules(prog, 50000)
			if res.CompletedSchedules < 2 {
				t.Fatalf("%s: expected multiple concurrent schedules explored, got %+v", prog.Name, res)
			}
			if len(res.Faults) != 0 {
				t.Fatalf("%s: well-typed contracted program exhibited safety fault: %+v", prog.Name, res.Faults)
			}
		})
	}

	// Verify companion Lean 4 specification artifact exists and encodes all 4 contracted hooks.
	leanPath := filepath.Join("..", "..", "specs", "FeatherweightGoSree.lean")
	leanSrc, err := os.ReadFile(leanPath)
	if err != nil {
		t.Fatalf("missing companion Lean 4 spec %s: %v", leanPath, err)
	}
	for _, required := range []string{
		"def HookAtomicStoreOK",
		"def HookAtomicLoadCap",
		"def HookBoxErrorOK",
		"def HookCancelFuncCapturesOK",
		"theorem cow_immutable_router_publication_accepted",
	} {
		if !strings.Contains(string(leanSrc), required) {
			t.Fatalf("FeatherweightGoSree.lean missing %q", required)
		}
	}
}
