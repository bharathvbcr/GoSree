# GoSree stress findings

What the adversarial suite finds, as of 2026-11-10 (re-counted 2026-11-11 on the build run's integration branch). Every finding below is
locked into a fixture or a test; the paths are given so each can be read and re-run. Counts come
from the harness's own ledger (`go test ./tests/stress -run '^TestStress$' -v`), not from the text of
an earlier version of this file.

## State these numbers are for

| | |
|---|---|
| Toolchain | go1.27.3 darwin/arm64 |
| Git HEAD | `25779ca` on `run/integ-1111` (no uncommitted checker edits) |
| Checker fingerprint | `77f03b52f18b13af`: `bash tests/stress/fingerprint.sh` |
| Run | 2026-11-11 16:12-16:18, load average 55-84: `go vet ./...`, `cargo test --release`, `go test ./... -count=1` with and without cgo (all 15 packages with tests `ok` in both), the ledger below, and the acceptance tiers (12/12). The 2026-11-10 suites are listed under [What was run](#what-was-run) |

The suite is green at a fingerprint when every marker still holds. At any other fingerprint a red run
is expected and is the suite working: each failure names the marker to promote or the lock that
regressed.

Root causes are quoted from the reading of whoever found the hole, with file and line as they were
then. They were not re-derived afterwards, and the line numbers drift as the checker changes.

## Ledger

Fixture suite (`tests/stress`), 144 packages in 134 roots:

| Rule | Hits locked | Known misses | Known false positives |
|---|---|---|---|
| SAFE001 unsafe ban | 57 | 3 | 0 |
| SAFE002 not owned | 33 | 0 | 9 |
| SAFE003 use after move | 164 | 58 | 12 |
| SAFE004 moved and escapes | 3 | 1 | 1 |
| SAFE005 immutability | 52 | 8 | 4 |
| SAFE006 package variables | 22 | 7 | 1 |
| SAFE007 directives | 77 | 1 | 0 |
| SAFE008 handler writes | 20 | 13 | 0 |
| SAFE009 lock discipline | 41 | 25 | 7 |
| BORROW001 storage | 33 | 0 | 1 |
| BORROW002 conflicts | 30 | 1 | 3 |
| BORROW003 owner invalidation | 26 | 0 | 1 |
| BORROW004 dangling return | 1 | 0 | 6 |
| CAP003 borrow crosses goroutine | 4 | 6 | 0 |
| ERR001 error result discarded | 11 | 0 | 0 |
| ERR002 companion result unchecked | 12 | 0 | 0 |
| ERR003 shadowed, overwritten or inverted error | 8 | 0 | 0 |
| ERR004 typed nil boxed into error | 11 | 1 | 0 |
| ERR005 writable resource Close error discarded | 7 | 1 | 1 |
| NORULE (no rule claims it) | 0 | 63 | 0 |
| **Total** | **612** | **188** | **46** |

The ERR005 row changed on 2026-11-11, and every change is in the new root `er_w_rangecapture`:
- Two hits: a deferred `Close` on a resource captured by a closure, and a deferred `Close` in a range-over-func body with the write in the body.
- One miss: a `defer Close` in a range-over-func body, with the write after the loop. The defer runs at the enclosing function's return, but the close is resolved at the yield function's synthetic return.
- One false positive: a captured resource reassigned by another closure (`replacedByCall`). The written flag is not reset across that call.

The fixes that day touched errcheck, ownership, borrow and the corpus tools. No existing marker changed.

Runtime witnesses: 58 programs carry a `WITNESS` marker, 45 for the race detector and 13 for a panic
or fatal error, and `TestWitnesses` passes on all 58 (the runtime convicts every one). For 37 of
them the checker accepts the program (the file carries a `MISS`); for the other 21 it reports the
hazard statically (`HIT` only). Known analyzer crashes: 0.

`lk_w_getter` (2 packages, one race witness) was added on 2026-11-11, after the run described
below, with 2 hits and 7 misses on a clean build of `7b739fc`. The same day the walk behind it was
changed (`passes/ownership/handlerwrite.go`, section C), and the fixture now holds 8 hits and 1 miss.
The totals above were counted after that change by
`go test ./tests/stress -run '^(TestStress|TestWitnesses)$' -v` (58 of 58 witnesses convicted), in a
working tree that also held uncommitted changes to `passes/ownership/contracts.go`. In one run of the
whole package that day the race detector did not convict `lk_wit_maplookup`; it convicted it in each
of 40 runs afterwards.

Other suites, counted from the test files:

- `cmd/gosree-erase/stress_erase_test.go`: `TestStressEraseLedger` reports 80 locks and 1 known bug
  (ER-23), measured by `go test ./cmd/gosree-erase -run TestStress`.
- `tests/stress/scale_test.go` and `vetparity_test.go`: 4 open `RB-*` scale assertions (`RB-DIAMOND`,
  `RB-RESEND`, `RB-CHAIN`, `RB-LITERAL`), and 7 fixed and locked as regression assertions (`RB-FIX`,
  `RB-TAGS`, `RB-STALE`, `RB-ORDER`, `RB-NOPOS`, `RB-JSONDUP`, `RB-LOOPCOPY`); see section 5.

A miss is filed under the rule that should have fired. Where no documented rule covers the case it
is `NORULE`. For publication and unlisted callbacks the choice between `SAFE003` and `NORULE` is a
judgement, and different fixtures made it differently.

### What was run

All on 2026-11-10, including the full `./tests/stress` package in one invocation (`go test -timeout 30m ./tests/stress`).

| Command (`go test`) | Result |
|---|---|
| `./tests/stress` (full suite) | ok; all `TestStress`, `TestWitnesses` (57/57), `TestDeterministic`, `TestScale`, `TestScaleOddShapes`, `TestScaleDeterminism`, `TestScaleDeterminismProcesses`, `TestScaleUnsafeStaleBorrow`, `TestVetParity`, `TestVetCLIContract`, `TestVetModuleGoVersion` pass |
| `./cmd/gosree-erase -run TestStress` | ok; 80 locks, ER-23 confirmed |

## 1. Memory corruption in safe Go

Go is memory-safe only while there are no data races: a slice, interface or map header written by
one goroutine can be read half-updated by another. Three programs, none using `unsafe`, cgo or
reflection, corrupt memory on every run (re-run 20 times each on 2026-11-10, built once and
executed; every run died with the message shown). All three are now reported statically via stdlib
capability contracts on `sync/atomic.Pointer[T]` (`SAFE005` on writes through `Load` and `SAFE003`
on post-`Store` publisher use):

| Fixture | What happens | 20 runs | Static check |
|---|---|---|---|
| `rs_torn_slice` | `panic: out-of-bounds read: element 4095 of a 1-element slice` | 20/20 | `HIT(SAFE005)` |
| `rs_torn_iface` | `panic: type confusion: (*Gzip).Check ran on memory holding ... a *Plain` | 20/20 | `HIT(SAFE005)` |
| `rs_w_mapwrites` | `fatal error: concurrent map writes` | 20/20 | `HIT(SAFE005)`, `HIT(SAFE003)` |

All three share memory the same way: a value is published through `atomic.Pointer[T]` and then
edited in place. The atomic makes swapping the pointer safe, not writing what it points to. `Store`
now consumes ownership of non-shareable payloads and `Load` enforces read-only (`\imm`) access.
`rs_torn_slice_caught` is the control: the same race through a plain package variable is reported
(SAFE006).

A fourth, `dx_syscall_witness`, forges a pointer with `reflect.Value.Pointer()` and
`syscall.Syscall`, with no `unsafe` import, and dies with
`unexpected fault address 0x4141414141414141` (20/20 on the same re-run; still accepted statically).

## 2. Root causes, most severe first

Every fixture named here still carries a `MISS` marker for the mechanism described, except where a
sentence says it is reported now.

### A. Storing without spawning

An escape is only read while a transfer is being checked. Stdlib concurrent containers in
`capContracts` (`atomic.Pointer`, `atomic.Value`, `sync.Map`, `sync.Pool`, `context.WithValue`) are
now modelled as transfers; unmodelled user registries and process/IO structs remain open:

- `atomic.Pointer`: reported now in `seed_publish`, `rs_torn_slice`, `rs_torn_iface`, `rs_w_mapwrites`
- `sync.Map.Store`: reported now in `sp_w_syncmap` and `sp_launder`. A mutex-guarded registry map
  (the map is checked, its values are not): `sp_w_registry`
- `sync.Pool` Put-then-use: reported now in `seed_moves`, `rs_lifetime`, `rs_send`
- a closure stored in a global during `init` and called from two goroutines: `sp_publish`
- `exec.Cmd.Stdout = &buf` then `Start`: `sp_boundary`, `mv_w_exec`, `mv_spawn`

### B. Types on the "assumed shareable" table launder pointers

`error`, `context.Context`, `context.CancelFunc`, `net.Listener` and `testing.TB` are treated as
shareable (`internal/kinds/kinds.go`), so a value converted to one stops being tracked. Any
`func()` converts to `CancelFunc`.

- pointer sent or spawned as `error`: `sp_w_error`, `mv_w_error`, `im_w_errfield`
- closure over a local as `CancelFunc`: `sp_w_cancelfunc`, `mv_w_cancelfunc`
- `context.WithValue` carrying a mutable pointer: reported now in `sp_w_ctxvalue`, `mv_w_ctxvalue`,
  `mv_shareable`, `sp_launder`; custom `context.Context` implementations remain open (`sp_launder`,
  `mv_shareable`)
- knock-on in fork-join: a joined goroutine hands the value to a grandchild inside an `error`, and
  `wg.Wait` returns ownership to the parent while the grandchild still writes: `sp_w_grandchild`

### C. Only static calls are modelled

Spawners and `//gosree:iso` parameters are recognised by the static callee. The same thing reached
through an interface, a method value or a func variable is an ordinary call.

- `WaitGroup.Go` behind an interface or as a method value: `bw_race_ifacego`, `sp_w_methodvalue`,
  `sp_table`
- an iso parameter through an interface, method value, func value or generic helper: the callee
  owns the value, the caller sees no move, and the move is checked by neither: `mv_w_iso_iface`,
  `sp_w_isoiface`, `mv_iso`, `sp_xpkg`
- a `//gosree:holds` helper called through an interface: `lk_holds`. (Called by `go` or `defer` it
  is reported: `lk_holds`, `lk_wit_holdsgo`.)
- `reflect.Value.Send` moves a value past both passes: `dx_reflect_send_witness`
- a handler that reaches shared memory through a new wrapper: a call that returns a new struct
  holding the shared pointer (`gw.Boxed().L.Run()`) is not followed back to it, whether the callee is
  in this package or another: `lk_w_getter`

Reported now, each with a `HIT` lock in `lk_w_getter`: a handler that reaches shared memory through
a call result instead of a field read. SAFE008 ties an argument back to the handler's shared memory
(`passes/ownership/handlerwrite.go`), and that walk used to follow a call result only into a callee
of the same package with one result, so a getter of another package, a `(value, ok)` getter of
either package, the second result of a pair and a getter on a getter's result all hid the write.
The walk now reads the callee's summary where it has no body and follows each result at its own
index. Found on the reference service, where `gateway.ResearchLoop().Run(...)` in a handler was
silent and the same call through the field was reported. `passes/ownership/handler_getter_test.go`
holds the recursive shapes and the bound on a long chain of getters.

### D. The spawner table is short

Standard-library callbacks that run concurrently and are not on it:

- witnessed: `runtime.AddCleanup` (`sp_w_cleanup`), `sync.Pool.New` (`sp_w_poolnew`),
  `b.RunParallel` (`sp_w_runparallel`), slog `Handler.Handle` (`sp_w_slog`), `Timer.Reset`
  re-running an `AfterFunc` closure the model treats as one-shot (`mv_w_reset`)
- not witnessed, marked from the standard library's documentation only: `runtime.SetFinalizer`,
  `http.Server.ConnState`, `httptrace` hooks, `expvar.Func`, parallel subtests (`sp_boundary`,
  `seed_moves`)
- handlers the SAFE008 walk never reaches: `http.Server{Handler: h}` and a route table registered in
  a loop (`lk_handlers`). A handler made by a factory or wrapped by `http.StripPrefix` is reported
  (`lk_wit_factory`, `lk_wit_middleware`).

### E. Immutable types can be written

A `//gosree:imm` pointer is shared across goroutines with no move, so every write that gets through
is a race the checker has approved.

- an imm type whose embedded unexported struct has an exported field, so `json.Unmarshal` writes
  the promoted field: `im_w_unmarshal`, `im_xpkg`
- a captured local reassigned: `im_w_capture`, `im_ctor`. Overwriting the container of a shared imm
  value: `im_w_array`
- a write through the `error` override of an imm value's field: `im_w_errfield`, `im_shape`

Reported now, each with a `HIT` lock: the pointer conversion to an unannotated twin
(`im_w_convert`), a method returning `&c.port` (`im_w_accessor`), slicing an array field
(`im_w_slice`), `fmt.Sscan(&c.port)`, `clear(cs)` and an address parked in a slice (`im_alias`,
`im_container`).

### F. Package-variable writes that are not seen (SAFE006)

- a write from another package: `im_w_xpkg`, `im_xpkg`, `rs_immut`

Reported now: code that runs after init counted as init (`im_w_initgo`, `im_globals`), a write
through a pointer-receiver method or a helper taking the address (`im_w_method`), a write through a
type assertion on a global interface (`im_globals`).

### G. Lock discipline (SAFE009)

- guarded memory written through its address, `s.buf.WriteString(...)`, `set(&s.n)`:
  `lk_wit_guardedfield`, `lk_access`
- a map read or ranged after `Unlock`; `copy` reading its source after `Unlock`:
  `lk_wit_maplookup`, `lk_access`
- an unlock the lockset does not see (through `cond.L`, through a slice element,
  through an interface call or a dynamic call on a package mutex): `lk_release`
- an unlock inside a closure called in place: release summaries record parameter paths only, so the
  closure's unlock of a captured mutex is lost and the lock stays held (`lk_release`
  `closureUnlocks`)
- the wrong instance after a redirect through `&p`: `lk_wit_redirect`, `lk_alias`
- whole-struct reads, `fmt.Println(s)`, `cp := *s`, `*a == *b`: `lk_wit_printstruct`, `lk_access`
- a function returning with the lock held is not reported: `rs_w_lockleak`

Reported now: an unlock in a same-package helper of a package-level mutex (`lk_wit_globalrelease`,
`lk_release`), through the method value `s.mu.Unlock` (`lk_release`), and in an in-place closure
in `lk_wit_closureunlock`.

### H. The trust base: unsafe, directives, the escape hatch

- **Files the analysis never loads.** A file behind `//go:build ignore`, a GOOS suffix or a custom
  tag can hold any amount of `unsafe`: `dx_buildtag`
- **A misspelled directive is reported but not applied.** A comment whose text, after the comment
  marker and optional white space, starts with `gosree:` in any letter case but is not a valid
  directive is a near miss and is reported as SAFE007: `// gosree:guarded_by mu` (a space),
  `//Gosree:guarded_by mu`, `/* gosree:guarded_by mu */`, `// GOSREE:ISO j`. The comment is not
  applied, so the accesses it was meant to guard stay unreported (`MISS(SAFE009)` and
  `MISS(SAFE003)` in `dx_directive`): SAFE007 tells the author, nothing else changes. Spellings that
  do not contain `gosree:` are plain comments and are not reported. gofmt rewrites `//gosree:Iso j`
  in a doc comment to `// gosree:Iso j`, which is still reported as a near miss (`dx_directive`).
- **The hatch mutes every rule in its span.** A `//gosree:unsafe` written for a pointer cast hides
  an unrelated use-after-move on the same line, in a goroutine the function starts, or a write on
  an earlier line when the hatched statement is a send in a loop; and a conflicting borrow on a
  later line: `dx_hatch`
- **A stale hatch is never printed by `gosree`.** A hatch that mutes nothing is reported by
  `safestale` (`internal/annot/stale`), which `cmd/gosree` does not register, and counted by
  `gosree-corpus` (`unsafe_stale`); `gosree ./...` does not print it (`dx_hatch`, `MISS(SAFE007)`).

- **`unsafe` spelled through another package.** Reported now: a package that never imports `unsafe`
  converting memory through names another package exports (an alias of `unsafe.Pointer`, a defined
  type whose underlying type is `unsafe.Pointer`, or an `unsafe.Pointer` value returned by a call or
  exposed via function signatures, maps, or interfaces) is flagged as `HIT(SAFE001)` in
  `dx_unsafe_spell/user`.

Reported now, each with a lock: reflection writers not named `Set*`
(`dx_reflect`), cross-package `unsafe.Pointer` exposure (`dx_unsafe_spell/user`), a hatch trailing
an `if` line covering only that line and not the body, a hatch trailing a multi-line statement
covering that statement (`dx_hatch`), a hatch with no reason or an invisible one (rejected and
mutes nothing, `dx_hatch`), a `//line` directive redirecting a hatch to another function
(`dx_line`), the documented inline borrow form `func f(b *int /*gosree:mub*/)` (`bw_forms`; the
same block comment between the name and the type is rejected as malformed, and that is a `HIT`).

### I. Borrow checker

- a borrow that leaves the frame into a goroutine (CAP003): through a struct field, a method value,
  a callee that starts the goroutine, or a channel of closures: `bw_cap003`, `bw_storage`,
  `bw_race_ifacego`
- a borrow stored to a package variable or alias: reported now in package-level function literals
  (`dx_x_inline`, locked as `HIT(BORROW001)`) and through pointer-store laundering (`bw_nll`,
  locked as `HIT(BORROW001)`).
- borrow conflict tracking under NLL: reported now across loop back-edges inside one block,
  backward `goto` loops, same-line statements, and deferred or called closures (`bw_nll`, locked as
  `HIT(BORROW002)`).

Reported now: all major `bw_nll` liveness shapes (a loop back edge across blocks, an owner write in
a loop, backward-goto loops, same-line statement conflicts, a deferred or later-called closure, a
closure passed to a helper, a pointer to a borrow, an identity helper, and the `*pc = b1` store itself
as BORROW001), borrows leaving through an unannotated callee or a returned closure (`bw_storage`,
BORROW001), package-level function literals (`dx_x_inline`, BORROW001), and the owner-aliasing and
owner-invalidation shapes (`bw_owner`, `bw_invalidate`).

## 3. False positives on ordinary Go

45 recorded. The ones a user meets first:

| Code flagged | Rule | Fixture |
|---|---|---|
| `c := T{}; return &c`, `return &v`: the standard constructor, in all code, annotated or not | BORROW004 | `bw_return` |
| one goroutine per slice element, `go f(&items[i])` | SAFE003 | `mv_idiom` |
| cloning a parameter and sending the clone (`bytes.Clone`, `slices.Clone`, `append([]byte(nil), b...)`) | SAFE002 | `mv_idiom` |
| `out <- batch; total += len(batch)` | SAFE003 | `mv_idiom` |
| a request struct sent by value, then `<-r.reply` | SAFE003 | `mv_idiom` |
| `go func(it *Item){ out <- it }(it)` | SAFE002 | `mv_idiom` |
| a range-over-func body sending each yielded value | SAFE002 | `mv_idiom`, `rb_rangefunc` |
| `defer func(){ wg.Done() }()`, a `WaitGroup` embedded in a struct, a join inside `select` | SAFE003 | `sp_forkjoin` |
| lazy init through `sync.Once` | SAFE006 | `im_globals` |
| value-receiver `WithPort` returning a modified copy of an imm type | SAFE005 | `im_ctor` |
| `sort.Slice(s.data, less)` under the lock; `cond.L.Lock()` with `cond.Wait()`; a sharded map | SAFE009 | `lk_fp` |
| borrows of disjoint fields `&s.A`, `&s.B` | BORROW002 | `bw_owner` |
| a send inside an `if false` branch or a `for false` body | SAFE003 | `rb_deadcode` |

## 4. gosree-erase

One known bug is registered in `cmd/gosree-erase/stress_erase_test.go`: ER-23. The ids ER-01 to
ER-22 are not in the registry; `cmd/gosree-erase/fix_erase_test.go` ("Tests added with the fixes for
ER-01..ER-22") holds the tests that pin the behaviour they described, and `TestStressEraseLedger`
reports 80 locks passing.

| Id | Class | Bug |
|---|---|---|
| ER-23 | contract drift | Machine code matches after erasure but the binaries differ: with `-trimpath` and line numbers aligned, the annotated build's `__gopclntab` is 8 bytes larger (inlined-helper metadata, inferred by the test's author), so the files are not byte-identical. `main.work` disassembles identically (`go tool objdump`, addresses normalised) and the erased binary names `gosree/cap` zero times |

The registry text of ER-23 cites `PROJECT.md:107` as the place that claims "Bit-for-bit parity". At
HEAD `PROJECT.md` has no such sentence and line 107 is a different row; the docs that carry the
phrase today are the diagram node in `docs/TOOLING_AND_ERASURE.md` ("Bit-for-Bit Instruction
Parity") and `docs/ARCHITECTURE.md`. The finding stands for those: instruction parity holds, whole
binary identity does not.

## 5. Scale, determinism and the two drivers

No analyzer panicked on any generated or hand-written input in `TestStress`, `TestScale`,
`TestScaleOddShapes`, `TestScaleDeterminism`, `TestScaleDeterminismProcesses`, `TestVetParity`,
`TestVetCLIContract`, or `TestVetModuleGoVersion` on 2026-11-10. Outside these suites, the borrow
pass overflowed the goroutine stack on a type that contains a slice of itself
(`type Tree struct{ Kids []Tree }`), found on the reference service; `f9b5915` fixed it and
`TestRecursiveTypesTerminate` in `passes/borrow` locks it.

Open: four `RB-*` scale assertions. Measured in the full-suite run; the exponents compare the
two largest sizes, and time is measured above the cost of building SSA:

- `RB-DIAMOND`: one function with N sequential `if/else` diamonds around a send. Allocation grows
  with exponent 3.00 and time with 1.33 (N 50 -> 100).
- `RB-RESEND`: one value sent and then used N times. Allocation exponent 2.93, time 2.59
  (N 200 -> 400).
- `RB-CHAIN`: a call chain N deep with callers declared before callees. Allocation exponent 1.96,
  time 3.27 (N 200 -> 400). Declared callee-first, the same chain is linear.
- `RB-LITERAL`: a slice literal of N pointer elements. Allocation exponent 1.91, time 1.53
  (N 1000 -> 2000).

Fixed, each locked by a regression assertion:

- `RB-ORDER`: diagnostics are strictly deterministic across runs and processes. Locked by
  `TestScaleDeterminism` and `TestScaleDeterminismProcesses` (10 runs, 0 reordered).
- `RB-NOPOS`: diagnostics on deferred returns and recover blocks preserve valid source positions.
  Locked by `TestVetCLIContract` and `TestDanglingReturnNoPosAndDedup`.
- The same class in SAFE008, found on the reference service on 2026-11-11 and not in the registry: a
  handler write found inside a function go/ssa synthesises (the wrapper around a method of a generic
  type of another package) was reported at no position, and because one rule at one position is one
  finding, every such finding in a package was reported as the first alone. Each is now reported at
  the call in source that leads into the wrapper. Locked by
  `TestHandlerWriteInSyntheticWrapperHasPosition` in `passes/ownership`.
- The rest of that class, found by looking for it on 2026-11-11 and not in the registry. A finding
  with no position was merged into another or dropped in five more places, none of which the
  fixtures here reach:
  - SAFE005 through a method value or a promoted method of another package's type
    (`put := c.Put; put(k, v)` on a value loaded from an `atomic.Pointer`): five such calls were
    reported as one, at no line. Each is reported at its call. Locked by
    `TestSharedLoadWriteInSyntheticWrapperHasPosition`.
  - The ownership pass's one reporting point reported a finding with no position at no position,
    and keyed SAFE003 and SAFE008 on the position alone. It now reports at the transfer or the
    package clause, says so, and keys on the message. Locked by `TestReportNeverWithoutPosition`.
  - Both report functions of the borrow pass, one site of its storage check, and the reporting loop
    of the errcheck pass returned without a word when a finding had no position. The storage check
    runs on the syntax and again on the SSA, so in every case seen the dropped finding repeated one
    the syntax check had reported (seven in `passes/borrow`'s own fixtures). A storage finding with
    no position is now reported at its function unless that function has a finding of the same
    rule with a position, reported or hatched; then it is taken for that finding again. The match
    is by rule, not by wording, because the two checks word one defect differently, so a second,
    different BORROW001 with no position in such a function still goes unreported until the first
    is fixed. Locked, limit included, by `TestReportNeverDropsForWantOfPosition` in
    `passes/borrow`, and `TestFindingWithNoPositionIsReported` in `passes/errcheck`.
  - The errcheck loop dropped two different things under one test. A finding at a source site
    whose instruction has no position is now reported at its function. A finding the solver raises
    on its site 0, an operation the lowering adds that is no place in the source, is still not
    reported, and is now counted (`Summary.FindingsUnsited`): 11 on the standard library with its
    tests, all ERR003 "overwrites an earlier unchecked error", in functions with a named error
    result (`internal/poll.sendFile`, `runtime/debug.ParseBuildInfo`, `os.doInRoot` among them);
    none on the reference service. Whether any of the 11 is a real defect was not established.
  On the standard library with its tests (12,933 SAFE findings) and the reference service with its
  tests (7,538), no finding of any pass is at no position and none carries the "reported
  elsewhere" note. The same holds for the 664 diagnostics of the 133 fixture roots here
  (`TestDump` over all of them) and for the reference service with its annotation table applied
  (693 findings, 37 of them SAFE009), which are the runs that reach the annotation-driven checks.
- Five fixed sizes at which the handler write check (SAFE008) stopped without saying so, each of
  which reported a handler past it as clean. None was reached on the standard library or the
  reference service, except the fourth, 35 times on the reference service, where lifting it found
  nothing more. Each is locked by a handler just past the old size in
  `passes/ownership/handler_caps_test.go`:
  - 10000 scans of function bodies, used up by a chain of 100 methods passing 100 shared pointers,
    because a function was queued again for every value added to it. It is queued once now.
  - 16 steps from the registered value to the handler's body (a chain of constructors), and 16
    conversions of the handler function. Both walks now end by themselves.
  - 64 values in one walk back from a write to shared memory (a pointer chosen among 71), and 64
    steps along one chain (a write 70 pointers from the receiver). The budget is 4096, and the same
    in the SAFE005 check.
  Two bounds remain, the 4096 and the 10000, and reaching either is now a finding of its own
  (`SAFE008-incomplete`, `SAFE005-incomplete`) at the line or the registration where the check
  stopped. Locked by `TestHandlerStepLimitIsReported` and `TestRootWalkBudgetIsReported`.
- Twenty more fixed sizes in the ownership pass, outside the handler write check, at which a walk
  stopped and answered "nothing here". Each is locked by a program just past the old size
  (`passes/ownership/walk_caps_test.go`, `walk_internal_test.go`), and each of those tests fails
  when its limit alone is put back into the fixed code (for the two borrow lookups in the syntax,
  whose depth counter is gone, a limit of the same size on what they have visited; the 64 steps
  and 64 values of the walk to a package variable as one limit):
  - SAFE009: 64 steps from an access back to its guarded field (a write 70 structs inside a guarded
    field was not checked); 32 steps along a lock's path (a mutex 40 structs deep was not seen to
    be taken, and the write under it was reported); 64 passes of the held-locks analysis (a block
    further than that from the entry was not checked); 8 conversions on a value handed to unknown
    code (the lock inside it read as still held); 16 steps from a branch condition back to its
    `TryLock` (the write under the lock was reported).
  - SAFE005: 16 types in the search of a struct for an `imm` value (a store over a struct with
    more field types than that passed); 64 values back from a written address; 64 values forward
    from an `imm` field to the calls that receive it.
  - SAFE006: 64 values and 64 steps back to a package variable (a write 40 pointers from the
    variable was missed); 16 steps in each of the two walks back to a parameter; 32 steps in the
    walk that recognises a read of a variable written only during initialisation.
  - SAFE003, SAFE004, CAP003: 10 steps and 32 values to the closure behind a function value (the
    goroutine read as started on an unknown function, and a borrow the closure captures was
    missed); 5 conversions between a closure and the `go` statement that starts it (reported as
    SAFE004 "also escapes"); and the three borrow lookups, which stopped two assignments back,
    five closures deep and 64 values from a borrowed parameter (a borrow copied through six
    variables and then sent on a channel was not reported).
  These walks now run to their end. Each ends by itself: it visits the values of one function
  once, or follows operands, which are defined strictly earlier, or (the held-locks analysis) can
  only shrink the sets it keeps. Two bounds stay and are no longer silent:
  - The walk back to a package variable goes into the functions it meets, so it keeps a budget,
    4096 values instead of 64. Reaching it is a finding (`SAFE006-incomplete`, locked by
    `TestGlobalRootBudgetIsReported`).
  - The rounds over a recursive function's returns (8) need not settle: a function that calls
    itself with two parameters swapped alternates between two answers for ever. Reaching the
    limit is a finding at the function (`SAFE005-incomplete`, `TestImmReturnsThatDoNotSettle`).
  On the standard library with its tests and on the reference service (production code, with
  tests, and with its annotation table), measured on two frozen copies that differ only in these
  files, nothing changed: 12,933, 2,035, 7,538 and 693 findings before and after, the same
  position and rule for every one, none in an `-incomplete` category. The 664 diagnostics of the
  133 fixture roots here are the same lines before and after. Of the old limits, four were reached
  on that code (the 16 types, the 64 values to a package variable, the 32 values to a closure and
  the borrow lookup's depth); with them lifted, 420 walks on the reference service and 772 on the
  standard library go past 64 values, 36 and 8 of them reach a package variable they did not
  reach before, the largest visits more than 1024 values and none reaches 4096. Why those 44 change
  no finding was not looked into. The standard library and the unannotated service carry no `imm`
  type, no guarded field and no stack-scoped borrow; the annotated service carries 66 guarded
  fields and one `imm` type, and its 37 SAFE009 and 20 SAFE005 findings are the same before and
  after. Nothing measured carries a borrow, so only the tests speak for the CAP003 lookups.
  Message text is not a measure of this change: four runs of one binary give the same positions
  and rules and differ in which write line or which alias a message names, in 1 to 9 rows on the
  reference service and 48 to 73 on the standard library, before and after alike. No row that is
  stable on both sides differs between them.
  Cost, as CPU time of the corpus tool (user plus system, three runs of each binary taken in turn,
  on a machine with a load average of 50 to 70): reference service 54.0 s before (62.8, 60.1,
  54.0) and 57.9 s after (65.5, 63.6, 57.9), more in each of the three pairs, by 3 to 4 s;
  standard library with tests 95.3 s before (118.7, 101.3, 95.3) and 91.2 s after (108.1, 91.2,
  95.6), with no consistent sign. Three runs on a busy machine: about 5% more on the service is
  what they show, not a measured figure. Wall time swung between 27 and 61 s for one binary on one
  input and says nothing.
- Six fixed sizes in the contract resolution (`contracts.go`) lifted, and two more (the origin
  tracers' budget and depth) kept but no longer passed in silence, while that code was itself
  uncommitted work of another change. Each is locked by a program just past the old size next to
  the same program well inside it (`passes/ownership/contracts_caps_test.go`), and each of those
  tests fails when its limit alone is put back. The tests pin the behaviour of that work as it
  stood on 2026-11-11 and will need to move with it.
  - `unwrapPublishedArg`, 16 conversions: a string-like value boxed through 18 interface types and
    stored in a `sync.Map` was taken for a published value, and the use of the box afterwards was
    reported.
  - The fixpoint that builds the contract index, four rounds per function plus 16: a helper that
    rotates 100 function parameters learns one parameter per round, and a `sync.Map.Store` passed
    as the last was not seen to publish. The rounds now run until one adds nothing; they end
    because every change adds a rule to a list that holds each rule once, or flips a flag one way.
  - `returnedExprIsContractFresh` and `collectReturnedCalls`, 13 steps: what a `sync.Pool` hands out,
    returned through 14 conversions, was no longer the caller's own (sending it was reported), and
    a `Load` returned through 14 was not found (the write to what it loaded was missed).
  - `paramFlowIndexSeen`, 16 steps along a chain and 13 variables deep: a parameter handed to a
    callback through more was not recognised. The helper was reported for publishing a value it
    does not own, in place of its caller for using the value afterwards; through 40 conversions the
    caller's use was not reported at all.
  - The origin tracers (`traceSSAOrigins`, `tracePtrBaseOrigins`) keep their budget of 96 values,
    but running out of it no longer answers "these are all the values". It answers "incomplete",
    which withholds the claims that depend on knowing every callee (the result is the caller's own,
    the call is an atomic one), and the shared-load check, which looks into the callees a trace
    finds, reports a call whose callees it could not all find (`SAFE005-incomplete`). A function
    chosen among 100 pool getters and one that returns a package variable used to pass as returning
    fresh values. The price is on the other side: a function chosen among the 100 pool getters and
    nothing else is now reported too, because past the budget the check does not know they all
    are. Five places inside the tracers drop a nested trace's "incomplete"; each answers
    "incomplete" itself on that path whatever the nested trace said (read, not tested).
  - The depth of 14 in those tracers and in `traceStructFieldValueOrigins` stays, because it is
    what ends one cycle (a field of a struct value, read through a pointer whose trace starts a new
    visited set, read from a stored struct value again). It already answered "incomplete", but the
    shared-load check drops that answer, so a writer called with loaded memory through 16
    conversions between function types was passed without a word. The three guards now mark the
    trace as cut, and the shared-load check reports the call (`SAFE005-incomplete`), as it does
    for the budget. One test per guard; the one for the struct-value tracer needs a struct
    converted from another type, see "Not covered".
  On the standard library with its tests and the reference service (production, with tests,
  annotated), on frozen copies that differ only in these hunks: 12,933, 2,035, 7,538 and 693
  findings before and after, the same position and rule for every one, none in an `-incomplete`
  category, and the same 664 fixture diagnostics. The budget of 96 is reached often on that code
  (a build with a counter at each limit: 36,554 and 35,883 times in the two tracers on the
  service's production code, 119,056 and 282,333 on the standard library); why answering
  "incomplete" there changes no finding was not looked into. The two 13-step limits were reached 8
  and 3 times on the standard library, and the other four not at all outside tests. Message text
  varied from run to run in each binary at the time of this measurement (it no longer does: the
  ordering fix under "Four gaps found" below) and was no measure of these changes. Four runs of each,
  taken one after the other, differed pair by pair in 3 to 9 rows before and 6 to 18 after on
  the service; four runs of each taken in turn (the binary before all the contract work and the
  final one) differed in 8 to 19 rows before and 2 to 9 after, the other way round. On the
  service the spread follows the run, not the binary. On the standard library the later binary
  varied more in all three comparisons made (48 to 73 against 50 to 75, then 70 to 82, and taken
  in turn 40 to 74 against 52 to 114, with 99 and 147 rows varying); that is not explained.
  Withholding "the result is the caller's own" where a trace was cut gives a value more reasons
  to name, which would do it, and was not tested. Ten rows of the service (`cmd/server/server.go`, five SAFE002 and five SAFE004)
  that said the same in four runs of one binary and named one of three package variables in four
  of the other show why: a build of each side that prints every reason at those transfers finds
  the same 13 package variables on both; the message names the one with the lowest position
  (`earliest`, `check.go`, the same code on both sides), and the three lie in three files of one
  package, so which is lowest follows the order the files were loaded in. One row of the
  standard library that was stable on each side of the runs taken in turn and differed between
  them takes both values within each binary across the other runs.
- A contract resolved from a cut trace spoke for the callees the trace reached, and what any one
  callee may do was simply missing for the others. Three findings went missing that way, each
  shown by a program with 101 candidates, or 16 conversions, next to the same with 3
  (`passes/ownership/contracts_cut_test.go`):
  - A publish. A value handed to a function chosen among 100 that do nothing and one that stores
    it in a `sync.Map`, and used afterwards, got no SAFE003. The same through an interface whose
    receiver is one of 100 types or a `*sync.Map`, through a callee that is handed the function,
    and from inside a helper, where the helper's caller lost its SAFE003 as well.
  - A load. A function chosen among 100 that return a new value and one that returns what an
    `atomic.Pointer` holds: the write through the result got no SAFE005.
  - A freeze. With 100 publishers into an `atomic.Pointer` and one into a `sync.Map`, "every
    callee freezes what it publishes" was taken from the 96 found, and a value referring to an
    init-only package variable passed. That is now withheld like the other claims that need
    every callee, and the value is reported.
  The first two cannot be recovered without the callees, so the call says it could not be
  followed: `SAFE002-incomplete` where it passes an argument a callee could publish,
  `SAFE005-incomplete` where its result may be loaded memory. Not at every cut call: only where
  the argument is not a constant, not safe to share and not already published by the callees
  found; only where the result is of a type that is not safe to share; and for an interface
  method only where a container's method of that name and signature exists, since that table is
  the only place such a call gets a contract from. A method with no such entry is also no longer
  traced at all. Each of those conditions has a program that must stay silent, and each of
  eleven parts of the change, taken out alone, fails a test.
  Measured on frozen copies that differ only in this change: the reference service's production
  code 2,035 findings before and 2,036 after (one `SAFE005-incomplete`, at a call of a function
  held in a struct field), with its tests 7,538 and 7,705 (71 `SAFE002-incomplete` and 96
  `SAFE005-incomplete`), annotated 693 and 694 with the same 578 transfers and 344 hatched, the
  standard library with its tests 12,933 and 12,981 (28 and 20), and the same 664 fixture
  diagnostics. No finding was lost anywhere. The reports with tests are calls of functions held
  in variables, struct fields and parameters; 61 of the 167 on the service are calls of one
  package variable that tests assign to. Before the filter for interface methods the same build gave 49
  new reports on production code and 622 with tests, most of them at `ctx.Value(...)` and other
  methods that have no contract.
  Cost of all the contract changes together, as CPU time of the corpus tool (user plus system,
  four runs of the binary before them and of the final one taken in turn, the order
  alternating; a scratch script, not a benchmark in this repository, so a cost check and not a
  claim of speed): reference service 47.3 s before (55.3, 51.8, 49.7, 47.3) and 39.1 s after (43.4,
  42.2, 39.1, 39.2), less in each of the four rounds; standard library with tests 85.7 s before
  (91.4, 85.7, 96.1, 89.1) and 84.9 s after (85.7, 86.3, 84.9, 88.7), less in three of four,
  which is no consistent difference. Receivers of interface methods that can have no contract
  are no longer traced, the likely reason for the service; that was not measured on its own.
  The four fixes in the next item were checked the same way afterwards (three rounds, the
  build measured above against the build with all four): 40.5 s and 38.5 s on the service, lower
  in each round by 0.5 to 2.8 s, inside the spread of either; on the standard library whichever
  build ran first in a round took about 35 s longer, so that run shows the order only. No cost
  was found; none this small would have been.
- Four gaps found during the work above, each with a program that showed it and that now passes.
  Each was found by probing the fix before it; a fifth round of probes found none, which says
  those probes passed and not that no gap is left.
  - **Which reason a message names.** Where several events explain a finding, or a package
    variable has several writes, the message names the first, and "first" was the lowest
    `token.Pos`, which orders files by when they were loaded. The same binary named different
    package variables and write lines from run to run (3 to 19 rows on the service, 40 to 114 on
    the standard library). Positions are now ordered by file name and place in the file
    (`comparePos`), and two events at one position by their description. Four runs of the fixed
    binary on the service and on the standard library with tests give the same text in every
    row. The comparator has a unit test with one file set loaded in both orders
    (`TestFirstPositionDoesNotFollowLoadOrder`); that the three call sites use it is shown by
    those four runs, not by a test.
  - **Struct conversions.** The stores into a struct field are looked up by the struct's type, so
    a struct converted from another type had none, and a function held in its field was not
    found as a callee: called with loaded memory, its write was missed. A conversion in sight is
    now followed to the struct it converts, and the lookup takes in the types the package
    converts to and from, values and pointers, in both directions
    (`TestSharedLoadCalleeInConvertedStruct`: in sight, down a call chain, stored in a map and
    read back, set through a converted pointer; and a case where following the conversion in
    sight keeps a writer in another struct of the same type from being blamed).
  - **Interface methods.** The shared-load check skipped every call of an interface method, so
    a method that writes loaded memory it is handed, or that it is called on, was never
    reported, with one receiver type in sight or many. It now follows the loaded memory into the
    method of each type the receiver may be, with the receiver as the method's first parameter;
    a method whose body is in another package is judged by its summary; a receiver trace cut by
    its bounds is reported like a cut callee trace; and a call that hands over nothing loaded
    does not trace its receiver (`passes/ownership/contracts_iface_test.go`).
  - **The package's own methods behind an interface.** Found by probing the fix above from the
    other side. A call of an interface method took its contract from the container table alone:
    a method of the package under analysis that stores its argument in a `sync.Map`, or returns
    what an `atomic.Pointer` holds, was seen when called directly and not when called through
    an interface, whatever the receiver was (eight shapes, all missed). The call now asks the
    method of each type its receiver may be, the declared method and not a promotion wrapper,
    for what a direct call of it gets, with the call's own arguments so that a method which
    hands its argument to a function it is given is followed too; and a method of the package
    with such a contract makes a cut receiver trace worth reporting, by name and signature, as
    a container's method does (`passes/ownership/contracts_usermethod_test.go`; ten parts
    taken out one at a time each fail a test). Two claims that need every callee were probed
    for being granted too easily this way and are pinned: "frozen when published" holds only
    if every type in sight freezes and the receiver's types are all known (an exported
    function's parameter is not), and the result of an interface method is never taken as the
    caller's own, before or after, even where every type returns a new value (a false alarm
    there, not a miss). A method that answers through another interface its receiver holds is
    followed, whichever is declared first. Measured against the build before it: no
    finding changes on the reference service's production code or its annotated run (578
    transfers, 344 hatched), 2 more reports with its tests (one call in a test file, reported
    as `SAFE002-incomplete` and `SAFE005-incomplete`), 5 more `SAFE002-incomplete` on the
    standard library with tests (`crypto/tls`, `net/http`), nothing lost, fixtures identical.
    No plain finding appeared on either corpus. The likely reason, not counted: the receivers
    of most interface calls there are not in sight of the calling package, and such a call is
    silent as before ("Not covered").
  Ten parts of the first three, taken out one at a time, each fail a test. Measured on frozen copies,
  each fix against the build before it: the first two change no finding on the reference service
  (production, with tests, annotated), the standard library with tests or the fixtures, only the
  text of rows that used to vary. The third adds nothing on the service's production code or
  its annotated run (still 578 transfers and 344 hatched), 14 SAFE005 with its tests, and 43
  `SAFE005-incomplete` on the standard library with tests (42 in `encoding/gob`, where the
  receiver is a `reflect.Type`); five existing SAFE005 in `math/rand` name another Load of the
  same value. The 14 are writes in the `RecordUsage` and `EmitAlert` methods of test doubles in
  `internal/usage`: the recorder is loaded from an `atomic.Pointer`, its store is an interface
  field of it, and each double writes its own fields when called. That is what the rule says is
  a write through loaded memory, and what a direct call of such a method gets (tried on a small
  program, where a method called directly on a field of a loaded struct is reported for writing
  its receiver; not tried by rewriting the service's calls). But 12
  of the 14 writes are made with the double's own mutex held (read from the source), and the
  rule credits a mutex only as a `//gosree:guarded_by` claim, so to a reader those 12 are false
  alarms until the fields are annotated; the other two (`fakeStore`) have no lock.
- `RB-JSONDUP`: standalone `gosree -json` does not report package test findings more than once.
  Locked by `TestVetParity`.
- `RB-LOOPCOPY`: go1.22+ per-iteration 3-clause loop variable copies are not falsely flagged as
  SAFE003. Locked by `TestVetModuleGoVersion`.
- `RB-FIX`: `gosree -fix` exits 2 with a refusal, instead of exiting 0 and printing nothing.
- `RB-TAGS`: `gosree -tags=x` is forwarded to the go command through `GOFLAGS`, and both drivers
  analyse the file it guards. (`TestVetCLIContract` asserts both and passes.)
- `RB-STALE`: a `//gosree:unsafe` that mutes only a BORROW004 or an ERR001 is not counted stale: the
  borrow and errcheck passes record their mutes, and the stale count is taken by `safestale`, after
  every pass that mutes. `TestScaleUnsafeStaleBorrow` locks it (0 stale scopes in each of 10 runs).

Held (`TestVetParity`, `TestVetCLIContract`, `TestVetModuleGoVersion`, all passing on 2026-11-10):
standalone `gosree` and `go vet -vettool` agree on all distinct findings across the parity suite,
facts survive the vet driver's round trip, and unanalysable inputs exit 1: a type error, a syntax
error, one broken file and a nonexistent path in both drivers; a missing import and a broken
dependency in `gosree`; and an unknown go version (1.99) is a nonzero exit in both.

## 6. Distance to Rust

| Rust guarantee | gosree today | Fixture | What would close it |
|---|---|---|---|
| No data races | captured locals, receivers, parameters, package variables, and stdlib concurrent container transfers (`atomic.Pointer`, `atomic.Value`, `sync.Map`, `sync.Pool`, `context.WithValue`) are caught; unmodelled user registries and dynamic calls remain open (sections A to D) | `rs_send`, `rs_torn_slice`, `rs_torn_iface`, `rs_w_mapwrites` | transfers for user-defined mutex registries (`sp_w_registry`) and dynamic calls |
| No out-of-bounds or type confusion | caught via stdlib capability contracts (`SAFE005`/`SAFE003`) on torn headers | `rs_torn_slice`, `rs_torn_iface`, `rs_w_mapwrites` | complete for stdlib containers; user registries remain open |
| Null safety | only the typed-nil error (ERR004); a nil dereference or a nil map write has no rule | `rs_null`, `rs_w_typednil`, `rs_w_nilmap` | a nil-flow analysis; whether x/tools `nilness` finds these was reported earlier and not re-checked (unverified) |
| Exhaustive match, checked downcast | no rule | `rs_exhaust` | a rule over constant enums and unchecked `x.(T)` |
| `Result` must be used | `safeerr` reports a discarded error result (ERR001), a companion result used before its error is checked (ERR002), a shadowed or overwritten `err` (ERR003) and a discarded `Close` error on a writable resource (ERR005); an explicit `_ = f()` is accepted | `rs_errors`, `er_mustuse`, `er_unwrap`, `er_shadow`, `er_close` | a rule for the explicit discard, if wanted |
| Aliasing XOR mutation by default | opt-in by annotation only | `rs_alias`, `rs_w_stale_ptr` | extend BORROW to unannotated `&s[i]` across `append` |
| Locks and WaitGroups not copyable | silent | `rs_alias` | bundle vet's `copylocks` |
| Lifetimes of borrowed buffers | no rule | `rs_lifetime`, `rs_w_scanner` | a "valid until the next call" table |
| RAII | no rule; a return with the lock held is silent | `rs_raii`, `rs_w_lockleak` | report it from the existing lockset; bundle `lostcancel` |
| Checked arithmetic | no rule | `rs_arith`, `rs_w_overflow` | a rule for size multiplication and narrowing |
| No mixed atomic and plain access | caught as a `go` argument, missed in a closure | `rs_conc`, `rs_w_mixedatomic` | a per-variable rule |
| Statics not writable by other crates | SAFE006 is per package | `rs_immut`, `im_w_xpkg` | check stores into another package's variables |
| `==` and hashing checked by trait | no rule | `rs_generics`, `rs_w_ifaceeq` | a rule for interface comparison |
| Deadlock freedom | no rule (nor in Rust) | `rs_conc` | double `Lock` is reachable from the lockset |

`copylocks`, `lostcancel`, `nilness`, `unusedresult` and `unusedwrite` come from
`golang.org/x/tools` (packages `go/analysis/passes/copylock`, `lostcancel`, `nilness`,
`unusedresult`, `unusedwrite`; present in v0.52.0, the version `go.mod` requires), which `gosree`
already depends on.

## 7. Claims the tool does not yet deliver

- README, "Why only goroutine boundaries": `atomic.Pointer`, `atomic.Value`, `sync.Map`, `sync.Pool`
  and `context.WithValue` are now modelled as capability transfers (`rs_torn_slice` and
  `rs_torn_iface` are reported), while publication through user-defined mutex-guarded registries
  (`sp_w_registry`) and unlisted callbacks/method values (sections C and D) remain unmodelled.
- `docs/BORROW_CHECKING_AND_NLL.md` section 6 justifies BORROW004 by a pointer to "deallocated or
  overwritten stack memory". Go moves such a variable to the heap; the rule flags the standard
  constructor (`bw_return`).

## Locked since they were found

Fixtures that first recorded a hole and now hold only `HIT` lines for it, so reverting the fix fails
the suite: `lk_holdsvalue` (a `//gosree:holds` method called through a method value), `mv_marker`
(`cap.Move(j)` in a send), `im_alias`, `im_container`, `im_w_helper`, `bw_forms`, `bw_nll`,
`bw_owner`, `bw_invalidate`, `dx_line`, `dx_reflect`, `dx_unsafe_spell/user`, `dx_x_inline`,
`lk_wit_closureunlock`, `lk_wit_factory`,
`lk_wit_globalrelease`, `lk_wit_holdsgo`, `lk_wit_middleware`, `im_w_accessor`, `im_w_convert`,
`im_w_initgo`, `im_w_method`, `im_w_slice`, `rs_torn_slice`, `rs_torn_iface`, `rs_w_mapwrites`,
`seed_publish`, `sp_w_syncmap`, `sp_w_ctxvalue`, `mv_w_ctxvalue`, `rs_send`. Twenty-one witness
programs are in this state: the runtime convicts them and the checker reports them.

## Not covered

- cgo (`import "C"`) has no fixture: one would need a C toolchain to load.
- amd64 and Linux: every run was on darwin/arm64. The torn-header witnesses depend on codegen.
- `reflect.Value.Call`, `reflect.Select`, `signal.Notify`, `database/sql` hooks and
  `testing/synctest` appear in no fixture (a text search of `testdata` on 2026-11-10), and the real
  `golang.org/x/sync/errgroup` is not importable by the fixtures; `sp_table/errgroup` is a local
  stand-in.
- `RWMutex.RLocker()` and `iter.Seq` over guarded data appear in no `lk_` fixture. Reborrows,
  two-phase borrows, map values as owners and guarded access across packages were not searched for;
  treat them as unverified.
- The shared-load check says nothing where it cannot find the functions a called value may be, or
  the types a receiver may be, for a reason other than its two bounds: a function value or an
  interface that comes in as a parameter of a function with callers outside the package, or from
  another package, has callees the package does not show. The check follows the ones it sees and
  is silent about the rest (`TestSharedLoadIntoInterfaceMethodOfUnknownReceiver`,
  `TestSharedLoadCalleeInUnsetStructThroughManyCalls`). Reporting every such call was not tried;
  it would be a report at most calls that pass loaded memory to a callback or an interface.
- The same holds for what a call of an interface method publishes or returns: it is known through
  the types the receiver is seen to be, and a receiver whose types are not in sight gives the
  call no contract and no report. A method declared in another package gives none either,
  unless it is a container's: the index holds the facts of this package's bodies only. And a
  method value of an interface method bound in one function and called in another is resolved
  without the call's arguments, so a method that only hands its argument on is not followed
  there (no test; the direct shapes are in `contracts_usermethod_test.go`).
- A struct that holds a function reaches a call in more ways than were tried. Tested: a
  conversion, the result of a call, a field of another struct, a slice element, a channel
  receive (`TestSharedLoadCalleeInConvertedStruct`,
  `TestSharedLoadCalleeInStructReachedOtherWays`). The lookup is by the struct's type, so the
  way the value travels should not matter; a function stored through reflection or built in
  another package is not seen.
- A SAFE005 message says "(line N)" for the Load without naming the file, and the Load may be in
  another file of the package than the write.
- Fixed bounds that remain in the ownership pass and say nothing when reached (a sweep of the code
  for numeric limits on 2026-11-11; an earlier note here counted 13 in all, which was an
  undercount: 20 were fixed and then six more in `contracts.go`, section 5, and these are left):
  - `contracts.go`, the depth of 14 and the budget of 96 values in `traceSSAOrigins`,
    `tracePtrBaseOrigins` and `traceStructFieldValueOrigins`. Both stay and both answer
    "incomplete" (section 5). A cut trace is no longer silent at the four places that act on
    one: the claims that need every callee are withheld, and a call whose callees may publish an
    argument, return loaded memory or write loaded memory it is passed, as an argument or as
    the receiver of an interface method, says that it was cut
    (`SAFE002-incomplete`, `SAFE005-incomplete`). Every caller of the tracers goes through one
    of those four (read, by listing the callers). What is left: a cut is also reported where the depth ended the cycle
    it is there for and nothing was skipped (the reports cannot tell the two apart); a call
    whose cut trace was for its result is reported for its arguments as well, and the other way
    round, since one mark covers every trace the call needed; and the report is one per call,
    not one per finding lost, so the use after a publish that was missed is named only by the
    call that could not be followed.
  - `contracts.go`, `evalValueAsCallable`, depth 6. Not reachable: the nested call passes no
    arguments, so nesting stops at one. A counter saw no depth above 1 in any run.
  - `contracts.go`, one committed: the 16-step scan at the top of `sharedLoadRoot`. For guarded
    fields the `slotOf` check before it gives the same answer, and no program was found in which
    the scan's limit changes a finding, so it has no test and was not changed. A counter saw it
    reached in no run.
  - `lockset.go`: `maxAcquireRounds` (16) and the release-summary widening (`maxReleaseSteps`,
    `maxReleasesPerFn`). Both err toward reporting, an acquisition not credited or a release
    assumed, and are described in `docs/RULES.md`.
  - `localguard.go`: 32 rounds carrying a guarded local's cell into closures. Not reachable: a
    function is scanned before its closures, so one round carries a cell to any depth
    (`TestGuardedLocalInDeepClosure` pins 40).
  - The borrow pass has six of its own (`passes/borrow/check.go`: 8, 64, 16 and 16 steps;
    `storage.go`: 16 steps and its own 64-step copy of the walk back to a package variable). Not
    looked into.
- Seen while writing the tests for those limits, and not fixed or looked into further:
  - The address of a field of a local stack-scoped borrow sent on a channel (`ch <- &b.in`, with
    `b := &v` marked `//gosree:mut`) is not reported as CAP003. The same send from a borrowed
    parameter is.
  - With the mutex inside a wrapper struct (`mu lock`, `type lock struct{ sync.Mutex }`), handing
    `&h.mu` to unknown code did not drop the held lock; with a plain `sync.Mutex` field it does.
  - What the new walks cost on a hub: N slices of an `imm` field stored into one variable is
    quadratic in N (40 ms at N=400, `TestScale`). Six shapes for the lifted walks are in
    `scale_test.go`; none grows faster than that.
- One de-duplication key still goes by line where there may be none: `checkGuardedFunc`
  (SAFE009) merges accesses to one variable that have no position within a function. They are
  reported, once, at the package clause. No input that produces one was found.
- A `return` go/ssa adds without a position (the recover block's) is not checked by the errcheck
  pass (`lower.go`, lowered as a stop). Whether that hides a finding was not looked into.
- Scale shapes for the handler walk and for the borrow pass beyond local borrows.
- The reference-service hatch ratio and the race corpus were not re-measured against any of this.
