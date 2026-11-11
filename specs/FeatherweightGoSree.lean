/-
  FeatherweightGoSree.lean
  Formal Capability Calculus & Contracted Function-Hook Specification for GoSree

  Not machine-checked: no Lean toolchain runs in this repository. The Go reference model in
  internal/formal mirrors it, and its test only checks that this file names the four hooks.

  Standard library shared-by-design overrides (`sync/atomic.Pointer[T]`, `error`,
  `context.CancelFunc`, `sync.Mutex`) are sound ONLY when paired with explicit
  capability preconditions on their boundaries rather than unverified blanket `Shareable`
  overrides.
-/

namespace FeatherweightGoSree

/-- Reference capabilities in GoSree's permission lattice. -/
inductive Cap where
  | iso          : Cap        -- `\iso *T`: unique affine ownership (linearly consumed on move/publish)
  | mub          : Cap        -- `\mub *T`: second-class exclusive mutable borrow
  | rob          : Cap        -- `\rob *T`: second-class read-only borrow
  | imm          : Cap        -- `\imm *T`: transitively immutable, freely duplicable across goroutines
  | mutexGuarded : Nat → Cap  -- guarded by static lock identifier `mu`
  deriving DecidableEq, Repr

/-- Abstract heap/value layouts, distinguishing multi-word Go headers that can tear. -/
inductive Ty where
  | scalar       : Ty
  | slice2Word   : Ty                 -- `(ptr, len)` 2-word slice header (`rs_torn_slice`)
  | iface2Word   : Ty                 -- `(itab, data)` 2-word interface header (`rs_torn_iface`)
  | rawMap       : Ty                 -- unsynchronized runtime `hmap` (`rs_w_mapwrites`)
  | structImm    : List Ty → Ty       -- `//gosree:imm` struct whose fields are never mutated post-init
  | structMut    : List Ty → Ty       -- mutable struct (`Router`, `Route`, `mutErr`)
  | atomicPtr    : Ty → Ty            -- `sync/atomic.Pointer[T]`
  | errorIface   : Ty                 -- `error` interface
  | cancelFunc   : Ty                 -- `context.CancelFunc`
  deriving DecidableEq, Repr

/-- Deep immutability / safe concurrent pointee predicate required by contracted hooks. -/
def DeeplyShareablePointee : Ty → Bool
  | .scalar          => true
  | .slice2Word      => false
  | .iface2Word      => false
  | .rawMap          => false
  | .structImm fs    => fs.all DeeplyShareablePointee
  | .structMut _     => false
  | .atomicPtr inner => DeeplyShareablePointee inner
  | .errorIface      => true
  | .cancelFunc      => true

/-- Shareable capability predicate across goroutine spawn (`go f(...)`). -/
def CanCrossGoroutine (c : Cap) : Bool :=
  match c with
  | .iso            => true   -- affine move: removed from parent Γ
  | .imm            => true   -- duplicable immutable capability
  | .mutexGuarded _ => true   -- synchronized by lock acquisition
  | .mub            => false  -- second-class borrow cannot escape to `go`
  | .rob            => false  -- second-class borrow cannot escape to `go`

/-- Contracted Function Hook 1: `(*atomic.Pointer[T]).Store(val)`
    Requires:
    1. `val` has capability `.iso` (linearly consumed!) or `.imm`, preventing post-publication
       alias writes (`rs_w_mapwrites`).
    2. Pointee `t` is immutable once published or internally synchronized (`DeeplyShareablePointee t`),
       preventing unsynchronized in-place field writes after `Load()`. -/
def HookAtomicStoreOK (valCap : Cap) (pointeeTy : Ty) : Bool :=
  (valCap == .iso || valCap == .imm) && DeeplyShareablePointee pointeeTy

/-- Contracted Function Hook 2: `(*atomic.Pointer[T]).Load()`
    Returns `.imm` (read-only shared capability), NEVER `.mub`!
    Eliminates `rs_torn_slice` (`live.Load().routes = maint`) and
    `rs_torn_iface` (`active.Load().codec = gzip`). -/
def HookAtomicLoadCap (_pointeeTy : Ty) : Cap :=
  .imm

/-- Contracted Function Hook 3: Boxing a concrete value `(c, t)` into `error`.
    Requires `c == .imm` and `DeeplyShareablePointee t` (eliminating `sp_w_error`). -/
def HookBoxErrorOK (valCap : Cap) (concreteTy : Ty) : Bool :=
  valCap == .imm && DeeplyShareablePointee concreteTy

/-- Contracted Function Hook 4: Constructing a `context.CancelFunc` closure over captures `caps`.
    Every captured variable must be goroutine-shareable without unsynchronized mutation
    (eliminating `sp_w_cancelfunc`). -/
def HookCancelFuncCapturesOK (capturedCaps : List Cap) : Bool :=
  capturedCaps.all (fun c => c == .imm || match c with | .mutexGuarded _ => true | _ => false)

/-- Non-vacuity sanity theorems checkable by definitional reduction (`rfl`). -/
theorem rs_torn_slice_load_forbids_write :
    HookAtomicLoadCap (.structMut [.slice2Word]) ≠ .mub := by
  decide

theorem rs_w_mapwrites_store_rejected :
    HookAtomicStoreOK .iso .rawMap = false := by
  rfl

theorem sp_w_error_mutable_struct_rejected :
    HookBoxErrorOK .mub (.structMut [.scalar]) = false := by
  rfl

theorem sp_w_cancelfunc_mut_capture_rejected :
    HookCancelFuncCapturesOK [.mub] = false := by
  rfl

theorem cow_immutable_router_publication_accepted :
    HookAtomicStoreOK .iso (.structImm [.scalar]) = true ∧
    HookAtomicLoadCap (.structImm [.scalar]) = .imm := by
   exact ⟨rfl, rfl⟩

end FeatherweightGoSree
