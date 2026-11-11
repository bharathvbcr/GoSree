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

Fixture suite (`tests/stress`), 142 packages in 133 roots:

| Rule | Hits locked | Known misses | Known false positives |
|---|---|---|---|
| SAFE001 unsafe ban | 57 | 3 | 0 |
| SAFE002 not owned | 33 | 0 | 9 |
| SAFE003 use after move | 164 | 58 | 12 |
| SAFE004 moved and escapes | 3 | 1 | 1 |
| SAFE005 immutability | 50 | 8 | 4 |
| SAFE006 package variables | 22 | 7 | 1 |
| SAFE007 directives | 77 | 1 | 0 |
| SAFE008 handler writes | 12 | 12 | 0 |
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
| **Total** | **602** | **187** | **46** |

The ERR005 row changed on 2026-11-11, and every change is in the new root `er_w_rangecapture`:
- Two hits: a deferred `Close` on a resource captured by a closure, and a deferred `Close` in a range-over-func body with the write in the body.
- One miss: a `defer Close` in a range-over-func body, with the write after the loop. The defer runs at the enclosing function's return, but the close is resolved at the yield function's synthetic return.
- One false positive: a captured resource reassigned by another closure (`replacedByCall`). The written flag is not reset across that call.

The fixes that day touched errcheck, ownership, borrow and the corpus tools. No existing marker changed.

Runtime witnesses: 57 programs carry a `WITNESS` marker, 44 for the race detector and 13 for a panic
or fatal error, and `TestWitnesses` passes on all 57 (the runtime convicts every one). For 36 of
them the checker accepts the program (the file carries a `MISS`); for the other 21 it reports the
hazard statically (`HIT` only). Known analyzer crashes: 0.

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
- Scale shapes for the handler walk and for the borrow pass beyond local borrows.
- The reference-service hatch ratio and the race corpus were not re-measured against any of this.
