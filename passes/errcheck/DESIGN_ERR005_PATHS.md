# ERR005 across field paths: per-store versioning

Card ft-e92e722c9d460326a0645e5f56d538ef. Design only, written against the tip `55b1e86` (run2/6d5a).
No production code changes with this document. Evidence labels: **verified** (read in code or run),
**inferred** (follows from code read, not run), **unverified**.

## 0. The answer to the question that decides the card size

**Lowering only. No wire change and no solver change.** (Verified by reading, plus one executed
check.)

- A version of an access path is an ordinary slot. A store is an `OpAssignSlot`. A join is an ordinary
  `PhiIR`. The written flag already joins at a phi in both engines: `applyPhisOracle`
  (`internal/gusseterr/oracle.go:1060-1100`, `w = w.join(...)`) and `apply_phis_into`
  (`rust/gosree-errsolve/src/lib.rs:1163-1190`, `joined.writable`), with the lattice
  `Bot < ReadOnly < Writable`, may-join (`oracle.go:564-578`).
- `internal/gusseterr/e92e_versions_test.go` (new, test only) hand-builds the exact IR the design emits:
  a diamond with a written arm and a replaced arm, replaced on both arms, an alias pinned to an old
  version, a loop with and without the root-definition reset. Both engines agree and give the expected
  findings (`go test -run TestE92e ./internal/gusseterr/`, cgo and `CGO_ENABLED=0`, both pass).
- The slot budget is the one place the wire is touched, and only as an existing guard:
  `MaxSlots`, `MaxPhisPerBlock` and `MaxStateCells` refuse a function loudly (GSE1). Versions add
  slots; section 7 bounds them so the design stays far from those caps.

What is **not** lowering-only, and is a separate decision: nothing in the first cut. Two later
extensions would need no wire change either (index and map elements, fresh versions after a kill), see
section 6.

## 1. Where the tip stands

`resourceSlot(v)` (`lower.go:277`) returns the slot of the SSA value `v` that a receiver is, except that
every load through one captured variable (`*ssa.FreeVar`) shares the FreeVar's slot, and a `Store` to
the FreeVar resets that slot (`lower.go:1227-1245`). `OpMarkWritable` (`lower.go:1486`) and
`OpUnhandledClose` (`lower.go:1499`, `1731`) both take `resourceSlot(recv)`.

So `h.w.Write(nil); h.w.Close()` is two loads (`*FieldAddr(h,0)` twice), two SSA values, two slots:
silent. That is the miss. The closure replay of ft-fe6e (`closureeffects.go`) only handles a captured
variable loaded directly from its FreeVar (`loadedFreeVar`, `closureeffects.go:150`).

## 2. Why 69eb7b6 failed (the three classes, each reproduced by reading the commit)

69eb7b6 replaced `resourceSlot` for a non-empty path with `pathSlot(accessKey{root, path})`: one slot per
(root, path) for the whole function, and a visible store to the same path resets it
(`OpAssignSlot` from the stored value's slot).

| class | what happened | why |
|---|---|---|
| stale flag (FPs) | `h.reset()`, `swapW(&h.w)`, `swapW(&w)`, `alias.w = open()` leave the flag set | the only way the flag was cleared was a store the lowering could see in this function. A call, an alias store and a pointer-to-field store replace the object without one. |
| shared root cell (5 FPs) | `h = other` keeps the old holder's write | for a captured holder every load shares the root cell; a store to the root cell reset the root's slot, not the slots of the paths below it (`(cell, "->0")` stayed). |
| alias miss (3 new misses) | `a := h.w; a.Write; h.w = open(); a.Close()` went silent | `a` is the load of path `h.w`, so `a` was keyed to the path's slot. The later store reset that slot, and `a.Close()` read the reset slot. But `a` holds the old object; the store changed the path, not `a`. Identity was keyed by path, and the path is not the object. |

All three have one cause: **the written flag lived on the path, which changes, and not on the object
that a load pinned.** There is a fourth, latent in 69eb7b6 (inferred, not in the commit's fixtures): a
path whose root is defined inside a loop (a phi `n = n.next`, an `Alloc` in the body) keeps its slot
across iterations, so a write of iteration 1 reaches the Close of iteration 2. The design resets it
(section 3.5, and the wire test models it).

## 3. Design

### 3.1 Identity: the version

A **version** of an access path is the interval between two events that can change which object the
path holds. A load of the path reads the version current *at the load*; the written flag lives on the
version's slot; two loads of the same path with no event between them share a version, so they share a
slot (the basic win), and a load made before an event keeps its old version (the alias case).

Today: identity = the SSA value that was loaded. 69eb7b6: identity = the path. Design: identity = the
(path, version) at the load. Because a loaded SSA value is an immutable copy of what the path held, the
object it denotes never changes after the load. A version is a sound name for that object as long as
every event that can replace the object ends the version.

Why it fixes the three classes:

- alias miss: `a := h.w` is a load at version v0. `a.Write` marks v0. `h.w = open()` ends v0 and
  starts v1 (an `OpAssignSlot` to v1's slot). `a.Close()` reads v0, which is written: reported, as on
  the tip. Fixture `aliasThenReplace*`.
- stale flag: `h.reset()` / `swapW(&h.w)` / `alias.w = open()` are events that end v0 and start the
  UNKNOWN version, so `h.w.Close()` after them is not keyed to v0 at all. Fixture `stale*`.
- shared root: `h = other` is a store to the root cell, an event that ends every version of every path
  below that root. Fixture `staleHolderReassignedCaptured`.

### 3.2 What is tracked (paths)

A **path** is a root plus steps. Flatten, as 69eb7b6 did and `valueKeyOf`/`addrKeyOf` today do not:

- root: a `Parameter`, a `FreeVar`, a non-cell `Alloc`, a `Global`, a local variable cell
  (`localCells`) or any other value (call result, phi). A load of a variable cell (a FreeVar or a
  local cell) is the cell itself, not a fresh root per load, so every load of captured `h` has one root.
- steps: `field(structType, index)` for `FieldAddr` and `Field`, `deref` for a pointer load.
  Nested chains (`o.in.w`) and embedded fields (`h.H.w`) are just longer step lists.
- `IndexAddr`, `Lookup`, `MapUpdate` are not steps in the first cut (section 6).

Only paths that a **resource operation** uses are tracked: the receiver of a write-mutating call
(`isWriteMutatingMethod`), of a Close the lowering consumes as an ERR005 site (`matchCloseSite`), of a
deferred Close, or of a bound-method write (section 3.8), whose path has at least one step. A pre-pass
collects them. A function with none is lowered byte-for-byte as today (the invariant, and zero cost).
A path with no steps (the variable itself) is exactly the tip's code (`rootSlot`, the FreeVar slot) and
is never touched.

Tracked paths per function are capped (K = 32, section 7). A path over the cap is untracked.

### 3.3 Events that end a version (invalidation)

An event ends the version of a tracked path P and starts the next one. Two kinds of next version:

- **known**: a store to exactly P. The new version's slot takes the stored value's written state
  (`OpAssignSlot` from `resourceSlot(stored)`, the same as the FreeVar store at `lower.go:1233`), so
  `h.w = other.w` carries `other.w`'s flag and `h.w = open()` starts unwritten.
- **UNKNOWN**: every other event. UNKNOWN is `nil`: a load at UNKNOWN is **keyed exactly as on the
  tip** (`rootSlot`, its own SSA value's slot). This is the invariant: *an untracked shape behaves
  exactly as the tip, in both directions.* UNKNOWN is deliberately not "may be written" (which would
  add reports) and not a fresh known version (which would add reports on a later load pair; see
  section 6 for it as a measured extension).

The events, in the order a pre-pass records them per block:

| event | effect on P |
|---|---|
| `Store` to an address that flattens to exactly P | new known version |
| `Store` to a proper prefix of P (the holder `h`, the inner struct `o.in`, `*h = H{}`) on the same root | UNKNOWN |
| `Store` to any other address that may alias P (below) | UNKNOWN |
| a `Call` or `Go` that may write P (below) | UNKNOWN |
| channel send/receive, `Select` (another goroutine may have written) | UNKNOWN, shared roots only |
| the definition of the root (an `Alloc` or phi inside a loop, any non-parameter) | the **root version**: reset (`OpAssignSlot` from `NoneU32`) right after the definition, so a flag cannot cross an iteration |
| `Defer` | none: the deferred call runs later; its receiver was bound at the `defer` |
| `MapUpdate`, `Send` of a value, `Field`/`Index` reads | none |

**May alias a store.** Each tracked path records the types of the locations along it: the type of every
struct on the way (with the field index) and of the final field. A store to address A with stored type
V ends P when A flattens to a path on the same root that is a prefix of P (exact: the store above), or
A is on a different shared root, or is not flattenable (a call result, an `IndexAddr`), and A's last
step is the same `(structType, field)` as a field of P, or V is identical to a location type of P.
`alias.w = open()` for `h, alias *H` is a hit of the first kind (same field `(H, w)`); `*q = open()`
with `q *io.WriteCloser` is a hit of the second. A store to a **private** root (section 3.4) other than
P's own root never aliases P. Work is a lookup in a map from type key to tracked paths, so it is
proportional to the paths hit, not to all paths.

**May write a call.** This needs a fact the repo has only partly (3.4). Rule for the first cut,
deliberately simple and conservative:

- a call kills every tracked path whose root is **shared**; a path whose root is **private** is never
  killed by a call;
- exemptions that kill nothing: the resource operations themselves (the matched Close, and a
  write-mutating method call: `h.w.Write` does not replace `h.w`), and `builtin` calls. This is an
  assumption about `Write`/`Flush`/`Sync`/`Close` implementations, stated in the risks, and the same
  one the tip makes when it treats those calls as markers;
- a tracked closure's call (the dominating call of `closureEffectsAt`) kills only what the closure body
  kills (the body is scanned by the same rules, section 3.7), not "everything", otherwise the replay
  could never produce a report.

### 3.4 Escape: which existing facts say a pointer does not escape

The repo has no points-to and no mod/ref summary. The solver's call graph carries may-return-nil
summaries only (verified: `internal/gusseterr`, `MayReturnNil`). The usable facts, all flow-insensitive
referrer scans, are:

- `isLocalCellAlloc` (`lower.go:2390`): an `Alloc` whose referrers are only `Store(Addr)`, `Load` or
  `MakeClosure` is a variable cell;
- `closureeffects.go:176-193`: a captured variable is **unstable** when its FreeVar has any referrer
  that is not a load, a `Store` through it, or a `MakeClosure` (stores to or leaks it);
- `closureCallFor` (the shared closure-pair plan conditions): the closure value goes nowhere else;
- `valStoredInLocalCell` (`lower.go:1071`).

The design adds one predicate in the same style, `privateRoot(root)`: the root is an `Alloc` and every
live referrer is a `DebugRef`, a `Load`, a `Field`/`Index` read, a `Store` with `Addr` equal to it
(never `Val`), or a `FieldAddr` that is itself private by the same test (recursively). Any call
operand, `Return`, `Store.Val`, `MakeClosure`, `Go`, `Defer`, `MakeInterface`, `Phi` or `ChangeType`
use makes it shared. A parameter, a FreeVar, a global, a loaded pointer and a call result are shared.

Consequence, stated plainly: in the first cut a **shared** root gets reports only over spans with no
call other than the resource operations; a **private** root gets them over any span.
`sharedParamCall` stays silent and `privateLocalCall` reports. Section 8 asks whether that is the
wanted trade.

### 3.5 Joins

A version at a block entry with several predecessors is a phi version, built on demand (Braun et al.,
"Simple and efficient construction of SSA form", demand-driven, memoised per (path, block)):

```go
// lookup returns the version of path p at the end of block b, or nil for UNKNOWN.
func (v *versions) end(p pathID, b *ssa.BasicBlock) *version {
    for i := len(events[b]) - 1; i >= 0; i-- {            // last event on p in b decides
        if e := events[b][i]; e.touches(p) { return e.next(p) }
    }
    return v.entry(p, b)
}
func (v *versions) entry(p pathID, b *ssa.BasicBlock) *version {
    if m, ok := v.memo[p][b]; ok { return m }
    switch len(b.Preds) {
    case 0:  return v.rootEntry(p)                         // parameter/freevar/global: a slot never assigned (Bot)
    case 1:  r := v.end(p, b.Preds[0]); memo; return r
    default:
        ph := &version{slot: newSlot(), pending: true}     // placeholder first: a loop reaches b again
        v.memo[p][b] = ph
        for _, pr := range b.Preds { in = append(in, v.end(p, pr)) }
        if anyNil(in) { v.memo[p][b] = nil; return nil }   // UNKNOWN poisons the join: tip keying
        if allSame(in, ph) { memo = that one; return it }  // trivial phi removed
        ph.phi = PhiIR{Dst: ph.slot, Incoming: [(pred.Index, in[i].slot)]}
        return ph
    }
}
```

- A join of known versions is a `PhiIR` over the predecessors' version slots, appended to the target
  block's phi list after all blocks are built (blocks are lowered in index order, a lookup may reach an
  earlier-emitted block). This is the existing wire (`MaxPhisPerBlock`, `MaxPhiArity` apply).
- A join with any UNKNOWN incoming is UNKNOWN. A path that is UNKNOWN on some predecessor therefore
  behaves as the tip at that load: no new report, and none removed.
- The flag joins may (`Writable` if any incoming is): the same semantics as a local variable. So
  `if c { h.w.Write() } else { h.w = open() }; h.w.Close()` reports, as its variable twin does today
  (`joinWriteOrReplace` and `joinWriteOrReplaceTwin`). The correlated-infeasible-path imprecision is
  the one every `w` variable already has; the twin fixtures pin that it is not new.
- A loop header: the placeholder makes `for { h.w.Write(); h.w.Close() }` one phi version
  (`loopWriteThenClose` reports, the variable twin does).
- A root defined by a phi or an `Alloc` in a loop is reset where it is defined (3.3), so the flag does
  not cross iterations (`loopHolderChanges` stays silent). The wire test `loopFunc(true/false)` is
  exactly this: with the reset no finding, without it the false positive.

### 3.6 Defer

`defer h.w.Close()` binds its receiver at the `defer`. The version read there is the version that the
deferred Close closes: `staleDeferBindsEarly` (`defer h.w.Close(); h.w = open(); h.w.Write(nil)`) is
silent, which a path-keyed design reports. The solver's exit check (`oracle.go:1489`, the writable state
of the slot at every return block) reads the registered version's slot. A version slot is only written by
`MarkWritable`, by the assign of its own store event and by its phi; a later store makes a new slot, so
the exit state is the registered object's state. (In a loop the same store re-executes and reassigns the
same slot, which is the same behaviour an SSA value defined in a loop has today.)

### 3.7 Closures

The replay needs the path in its summary: `closureFx` records `(freeVar index, steps)` instead of a
free-variable index alone. Building the summary reuses `buildClosureFx`'s scan with the new receiver
flattening; `loadedFreeVar` becomes "the path of the receiver, rooted at a FreeVar".

At the creator's dominating call the replay translates `(FreeVar i, steps)` to the creator's root
`Bindings[i]` (a captured by-ref variable is an `Alloc` binding; a by-value capture is the value) and
emits `OpMarkWritable` on the creator's version of that path at the call, and `OpUnhandledClose` for a
Close, as it does for a plain variable. The creator's pre-pass registers those paths as tracked when
it sees a tracked closure call. The rule that a closure which stores to, or leaks, a captured variable
is not replayed extends to paths: the body kills P (any event of 3.3 on P in the body) means P is
UNKNOWN after the call and the closure's write on P is not replayed.

Five of the ten `er_closureclose` markers are path markers and fall to this design: lines 62, 68, 76
(`h.w` through a captured pointer, its twin, and a field of a variable captured by reference) and 85, 91
(`(*pw)`). Five are closure-flow, not paths, and **keep their markers with their reasons**: 47 (a closure
that is deferred), 99 (a Close in a closure nested in the returning closure), 106 (range-over-func
body, a deferred Close before the loop), 122 (the write is in another closure the body calls), 130 (the
deferred close is resolved at the yield function's return). Replaying a deferred or an escaping closure
is a different mechanism (a replay at the exit blocks, a different dominance condition) and a separate
card.

### 3.8 Bound method value as a write

`wr := w.Write; wr(nil); w.Close()`. In SSA `wr` is a `MakeClosure` whose `Fn` is the synthetic bound
wrapper and `Bindings[0]` is the receiver. A `Call` whose `Value` is such a `MakeClosure`, and whose
wrapped method passes `isWriteMutatingMethod`, emits `OpMarkWritable` on `resourceSlot(Bindings[0])`.
A bound value that is only assigned (`boundWriteUnused`) or handed away (`boundWriteEscapes`) is
never a call, so nothing is emitted. This part is independent of versioning (it works for a plain
variable too) and can ship first.

## 4. Scope

| shape | v1 |
|---|---|
| `h.w.Write; h.w.Close` (param, FreeVar, local struct), `defer h.w.Close()` | fixed |
| `(*pw).Write; (*pw).Close` | fixed (step `deref`) |
| nested `o.in.w`, embedded `h.H.w`, promoted `e.Close()` on an embedded interface/field | fixed |
| interface-typed field, generic holder `G[T]` | fixed |
| aliases: `a := h.w`, `p := &h.w` | fixed; the alias-replace shapes keep reporting |
| the five path `er_closureclose` markers (62, 68, 76, 85, 91) | fixed via 3.7 |
| closure writes a path, creator closes (and the reverse), `errc7ddadv/b.go:99` | fixed via 3.7 |
| bound method value called as a write | fixed (3.8) |
| `m[k]`, `s[i]`, `s[i].w` | **left** (section 6) |
| two holders sharing one resource (`h1.w = w; h2.w = w`) | left: needs alias analysis; silent today |
| the five closure-flow `er_closureclose` markers (47, 99, 106, 122, 130); `errc7ddadv` c01-c14, s01, w02 (a goroutine), w07 | left, with reasons above |
| `errc7ddadvgap`/`errc7ddadv` ap01, ap02, ap05, ap07, ap14 and `errc7ddadvgap` all four | fixed (they are paths) |

## 5. Fixtures that pin today (this commit)

`testdata/src/erre92e/a.go`: 77 functions, each annotated `// e92e-target: report | stay silent |
leave`, with the markers that state today's output exactly. `TestE92eBaseline` runs it,
`TestE92eSnapshot` compares to `testdata/e92e.golden` (17 lines, ten identical runs, both engines),
`TestE92eTargetsAnnotated` checks every function is annotated, that no `stay silent` function reports
today, and logs the wins still to deliver. The implementation round's diff is exactly: the markers of
the listed wins appear, nothing else moves.

## 6. Extensions, each measured before it is allowed

1. **Fresh known version after a kill** instead of UNKNOWN: `h.reset(); h.w.Write; h.w.Close()` would
   report. Sound if the kill set is complete, adds reports on shapes the tip does not track. Violates the
   stated invariant as written, so it is a separate decision with its own corpus diff.
2. **Index and map elements**: a step `index(v)` keyed by the SSA value of the index, kills by the same
   type signature plus `MapUpdate` and `IndexAddr` stores. `sliceOther` (`s[0]` vs `s[1]`) must keep
   silent: a constant index differs. No wire change.
3. **Value chains** (`v := *h; v.w.Write(); v.w.Close()`): a `Field` of an SSA value is immutable, so it
   needs a stable slot per (value, steps) and no kills. 69eb7b6 put these through the store machinery,
   which was a category error.
4. **Mod/ref summaries** to narrow the call kill for shared roots: only with a measured count of the
   reports section 3.4's rule loses.

## 7. Cost

All work is lowering time, linear in the function plus a bounded factor:

- pre-pass: one scan of the instructions, `O(instrs)`, only in a function with at least one tracked path;
- tracked paths capped at **K = 32**; events recorded only for them;
- lookups memoised per (path, block): at most `K x blocks` steps, and the phi operands are the
  predecessors, `K x edges`; demand-driven, so only paths and blocks a tracked load reaches;
- slots: one per known version actually read, one per phi: at most `K x (stores + joins)`, and in
  practice the loads that read them;
- kill by a call: one pass over the K paths per call (`K x calls`); by a store: a type-key map lookup,
  proportional to the paths hit.

**Counter.** Count `pathWork++` per instruction scanned, per event applied to a path, per (path, block)
lookup step and per phi operand, and add it to the existing `planWork` so the existing harness
(`planWorkOf`, `closurePlanWorkHook`, `TestAdvC7ddScaleCounters`) measures it. Scale tests, all
deterministic, no clock: n holders, n stores per path, n blocks, a loop nest of depth d, nesting of
`FieldAddr` chains depth d. Assert `work(2n) <= 2.2 * work(n)` for n >= 256, and an absolute bound of
`c * (instrs + K * blocks)`. A fixture with K+1 tracked paths must show path K+1 untouched
(tip-keyed) and the work flat. `slotsAdded` is reported beside it and asserted under a fraction of
`MaxSlots`.

## 8. Risks and hard stops for the implementation round

Hard stops, each a test that fails the round if broken:

1. **No new false positive and no new miss against the tip, by full-line diff** of every errcheck
   fixture and the stress corpus (the ft-fe6e method), with the single permitted exception of the
   listed wins appearing. A diff that is neither a listed win nor identical fails.
2. **No duplicates**: no Close line reported twice (once as a Close, once as a deferred Close); the
   existing `TestAdvC7ddSpecSnapshot` assertion stays.
3. **Untracked shape identical**: for a function with no tracked path the emitted `FuncIR` is equal to
   the tip's, frame for frame (golden over the whole corpus). For an UNKNOWN load the slot chosen is the
   tip's.
4. **Linear work**: the scale counters of section 7.
5. **Both engines byte-identical** on erre92e and the corpus (`cgo` on go1.27.3, `CGO_ENABLED=0`).
6. **No wire or solver change.** If an implementation seems to need one, stop and report.
7. The 69eb7b6 adversary set (`errc7ddadvfp`, `errc7ddadvmiss`) passes unchanged.

Risks:

- **A missing kill is a false positive.** The kill set is the whole soundness argument. The type-based
  alias rule and "any call kills shared roots" are chosen to over-kill. The cost of over-killing is only
  a miss that stays a miss; the cost of under-killing is an FP. Review the event table against
  `unsafe`, `reflect` and cgo as out of model.
- **Write/Flush/Sync/Close exempt from killing** assumes those methods do not replace the caller's
  holder fields. A `Write` that calls back is unmodelled, as on the tip.
- **Shared roots lose reports across any call** (3.4). The first cut under-reports by design; the number
  of lost reports on the corpus should be counted and reported before the call rule is relaxed.
- **A version slot re-executed in a loop** is reassigned each iteration; that is the behaviour of any
  SSA value slot in a loop today, but it interacts with the defer exit check; `loopWriteThenClose` and a
  defer-in-loop fixture must be in the round.
- **Slot budget.** Programs with many tracked paths in a huge function approach `MaxStateCells`; the
  cap K and the demand-driven slots are the control, and a function the solver refuses still refuses
  loudly (GSE1), not silently.
- The five-of-ten claim on the markers is by reading the shapes; it is verified only when the
  implementation round flips them.

## 9. Open questions for Fable

1. Is "a call kills every tracked shared root" (first cut) acceptable, or should the round include a
   mod/ref refinement before it ships? Without one a field of a parameter reports only over call-free
   spans.
2. Fresh known version after a kill (6.1): allowed as a measured second step, or never?
3. Is exempting the write-mutating and Close method calls from killing an acceptable stated assumption?
4. Closure path replay (3.7) and bound-method writes (3.8): one card with the versioning, or three
   milestones (3.8, then versioning, then 3.7), each with its own full-line diff? Recommended: three.
5. K = 32 for the tracked-path cap: any corpus datum to size it? Proposal: take the maximum number of
   distinct resource-op paths per function over the repo, its fixtures and the standard library, then
   set K at three times that, the way `maxReplayDepth` was set.
6. Index and map elements (6.2): in this card or the next?
