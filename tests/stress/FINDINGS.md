# gocap stress findings

What the adversarial suite found, as of 2026-11-10. Every finding below is locked into a fixture or
a test; the paths are given so each can be read and re-run.

## State these numbers are for

| | |
|---|---|
| Toolchain | go1.27.3 darwin/arm64 |
| Git HEAD | `3c9677c`, plus uncommitted checker edits (see the warning below) |
| Checker fingerprint | `ffe873d611b617dd` (`bash tests/stress/fingerprint.sh`) |
| Run | 2026-11-10 08:55, `go test ./tests/stress ./cmd/gocap-erase -count=1`: both `ok`, same fingerprint before and after |

**The checker was being edited by another session while this suite was written.** Its sources
changed at least six times between 08:00 and 08:55, and several holes found here were closed in
that window (listed under [Closed while testing](#closed-while-testing)). The suite is green at the
fingerprint above. At any other fingerprint a red run is expected and is the suite working: each
failure names the marker to promote or the lock that regressed.

Root causes are quoted from the reading of whoever found the hole, with file and line as they were
then. They were not re-derived afterwards, and the line numbers drift as the checker changes.

## Ledger

Fixture suite (`tests/stress`), 125 packages in 117 roots:

| Rule | Hits locked | Known misses | Known false positives |
|---|---|---|---|
| SAFE001 unsafe ban | 29 | 18 | 1 |
| SAFE002 not owned | 35 | 0 | 9 |
| SAFE003 use after move | 142 | 73 | 12 |
| SAFE004 moved and escapes | 3 | 1 | 1 |
| SAFE005 immutability | 28 | 23 | 4 |
| SAFE006 package variables | 11 | 15 | 1 |
| SAFE007 directives | 35 | 8 | 2 |
| SAFE008 handler writes | 6 | 18 | 0 |
| SAFE009 lock discipline | 27 | 34 | 7 |
| BORROW001 storage | 14 | 7 | 2 |
| BORROW002 conflicts | 21 | 9 | 6 |
| BORROW003 owner invalidation | 15 | 9 | 1 |
| BORROW004 dangling return | 1 | 0 | 6 |
| CAP003 borrow crosses goroutine | 3 | 7 | 0 |
| NORULE (no rule claims it) | 0 | 78 | 0 |
| **Total** | **370** | **300** | **52** |

Runtime witnesses: 54 programs that gocap accepts and the runtime convicts (43 by the race
detector, 11 by a panic or fatal error). Known analyzer crashes: 0.

Other suites: `cmd/gocap-erase/stress_erase_test.go` holds 21 known bugs and 30 locks;
`tests/stress/scale_test.go` and `vetparity_test.go` hold 11 known bugs (`RB-*`).

A miss is filed under the rule that should have fired. Where no documented rule covers the case it
is `NORULE`. For publication and unlisted callbacks the choice between `SAFE003` and `NORULE` is a
judgement, and different fixtures made it differently.

## 1. Memory corruption in safe Go that gocap accepts

Go is memory-safe only while there are no data races: a slice, interface or map header written by
one goroutine can be read half-updated by another. Three programs, none using `unsafe`, cgo or
reflection, get zero diagnostics from gocap and corrupt memory on every run:

| Fixture | What happens | 20 runs |
|---|---|---|
| `rs_torn_slice` | `panic: out-of-bounds read: element 4095 of a 1-element slice` | 20/20 |
| `rs_torn_iface` | `panic: type confusion: (*Gzip).Check ran on memory holding ... a *Plain` | 20/20 |
| `rs_w_mapwrites` | `fatal error: concurrent map writes` | 20/20 |

All three share memory the same way: a value is published through `atomic.Pointer[T]` and then
edited in place. The atomic makes swapping the pointer safe, not writing what it points to. To the
checker `Load` returns memory nobody else is known to hold, and `Store` is an ordinary call.
`rs_torn_slice_caught` is the control: the same race through a plain package variable is reported
(SAFE006).

A fourth, `dx_syscall_witness`, forges a pointer with `reflect.Value.Pointer()` and
`syscall.Syscall`, with no `unsafe` import, and dies with
`unexpected fault address 0x4141414141414141`.

## 2. Root causes, most severe first

### A. Storing without spawning is never checked

An escape is only read while a transfer is being checked. A value put where another goroutine will
find it, by a publisher that keeps writing it, is reported nowhere.

- `atomic.Pointer`: `seed_publish`, `rs_torn_slice`, `rs_torn_iface`, `rs_w_mapwrites`
- `sync.Map.Store`: `sp_w_syncmap`. A mutex-guarded registry map (the map is checked, its values
  are not): `sp_w_registry`
- `sync.Pool` Put-then-use: `seed_moves`, `rs_lifetime`
- a closure stored in a global during `init` and called from two goroutines: `sp_publish`
- `exec.Cmd.Stdout = &buf` then `Start`: `sp_boundary`, `mv_w_exec`

### B. Types on the "assumed shareable" table launder pointers

`error`, `context.Context`, `context.CancelFunc`, `net.Listener` and `testing.TB` are treated as
shareable (`internal/kinds/kinds.go`), so a value converted to one stops being tracked. Any
`func()` converts to `CancelFunc`.

- pointer sent or spawned as `error`: `sp_w_error`, `mv_w_error`, `im_w_errfield`
- closure over a local as `CancelFunc`: `sp_w_cancelfunc`, `mv_w_cancelfunc`
- `context.WithValue` carrying a mutable pointer: `sp_w_ctxvalue`, `mv_w_ctxvalue`
- knock-on in fork-join: a joined goroutine hands the value to a grandchild inside an `error`, and
  `wg.Wait` returns ownership to the parent while the grandchild still writes: `sp_w_grandchild`

### C. Only static calls are modelled

Spawners, `//gocap:iso` parameters and `//safe:holds` are recognised by the static callee. The same
thing reached through an interface, a method value or a func variable is an ordinary call.

- `WaitGroup.Go` behind an interface or as a method value: `bw_race_ifacego`, `sp_w_methodvalue`,
  `sp_table`
- an iso parameter through an interface, method value, func value or generic helper: the callee
  owns the value, the caller sees no move, and the move is checked by neither: `mv_w_iso_iface`,
  `sp_w_isoiface`, `mv_iso`, `sp_xpkg`
- a holds helper run by `go`, `defer` or an interface: `lk_wit_holdsgo`, `lk_holds`
- `reflect.Value.Send` moves a value past both passes: `dx_reflect_send_witness`

### D. The spawner table is short

Standard-library callbacks that run concurrently and are not on it:

- witnessed: `runtime.AddCleanup` (`sp_w_cleanup`), `sync.Pool.New` (`sp_w_poolnew`),
  `b.RunParallel` (`sp_w_runparallel`), slog `Handler.Handle` (`sp_w_slog`), `Timer.Reset`
  re-running an `AfterFunc` closure the model treats as one-shot (`mv_w_reset`)
- from the standard library's documentation only: `runtime.SetFinalizer`, `http.Server.ConnState`,
  `httptrace` hooks, `expvar.Func`, parallel subtests (`sp_boundary`, `seed_moves`)
- handlers the SAFE008 walk never reaches: one made by a factory, one wrapped by
  `http.StripPrefix`, `http.Server{Handler: h}`, a route table registered in a loop:
  `lk_wit_factory`, `lk_wit_middleware`, `lk_handlers`

### E. Immutable types can be written

A `//gocap:imm` pointer is shared across goroutines with no move, so every write that gets through
is a race the checker has approved.

- pointer conversion to an unannotated twin, `(*Mutable)(c).port = 1`: `im_w_convert`
- an imm type embedding an unexported struct with an exported field; `json.Unmarshal` writes the
  promoted field: `im_w_unmarshal`, `im_xpkg`
- a method returning `&c.port`: `im_w_accessor`
- slicing an array field, `c.arr[:]`: `im_w_slice`
- a captured local reassigned: `im_w_capture`. Overwriting the container of a shared imm value:
  `im_w_array`
- `fmt.Sscan(&c.port)`, `clear(cs)`, an address parked in a slice: `im_alias`, `im_container`

### F. Package-variable writes that are not seen (SAFE006)

- code that runs after init counted as init: a goroutine started in `init`, a closure built there
  and stored: `im_w_initgo`, `im_globals`
- a write through a pointer-receiver method or a helper taking the address: `im_w_method`
- a write from another package: `im_w_xpkg`, `rs_immut`
- a write through a type assertion on a global interface: `im_globals`

### G. Lock discipline (SAFE009)

- guarded memory written through its address, `s.buf.WriteString(...)`, `set(&s.n)`:
  `lk_wit_guardedfield`
- a map header copied under the lock and used after `Unlock`; `copy` reading its source after
  `Unlock`: `lk_wit_maplookup`
- an unlock the lockset does not see (in a closure, in a helper through a package mutex, through a
  method value, through `cond.L`): `lk_wit_closureunlock`, `lk_wit_globalrelease`, `lk_release`
- the wrong instance after a redirect through `&p`: `lk_wit_redirect`
- whole-struct reads, `fmt.Println(s)`, `cp := *s`, `*a == *b`: `lk_wit_printstruct`
- a function returning with the lock held is not reported: `rs_w_lockleak`

### H. The trust base: unsafe, directives, the escape hatch

- **`unsafe` without importing it.** A package converts pointers through a type another package
  exports (`lib.P = unsafe.Pointer`) and SAFE001 is silent: `dx_unsafe_spell/user`
- **Reflection writers not named `Set*`**: `reflect.Copy`, `Value.Clear`, `reflect.Swapper`,
  `Value.Pointer`, and `SetInt` as a method value or through an interface: `dx_reflect`
- **Files the analysis never loads.** A file behind `//go:build ignore`, a GOOS suffix or a custom
  tag can hold any amount of `unsafe`: `dx_buildtag`
- **Misspelled directives fail open.** `// safe:guarded_by mu` (one space), `//Safe:guarded_by`,
  `/* safe:guarded_by mu */` and `// GOCAP:ISO j` are dropped silently, and the accesses they were
  meant to guard go unreported. gofmt rewrites `//gocap:Iso j` in a doc comment into the silently
  dropped form: `dx_directive`
- **The documented inline form is rejected.** `func f(b *int /*gocap:mub*/)`, as written in
  `docs/DIRECTIVES.md`, is reported as malformed and the borrow is dropped: `bw_forms`
- **The hatch mutes every rule in its span.** A `//gocap:unsafe` written for a pointer cast hides
  an unrelated use-after-move on the same line, in a goroutine the function starts, or in an `if`
  body; and a conflicting borrow on a later line: `dx_hatch`
- **`//line` moves the hatch.** A hatch in one function mutes a cast in another when a `//line`
  directive gives both the same line number: `dx_line`
- a hatch with no reason is reported and still mutes; a hatch trailing a multi-line statement
  attaches to the next statement; a stale hatch is counted but never printed: `dx_hatch`

### I. Borrow checker

- a borrow used at the top of a loop body is not seen as live across the back edge: `bw_nll`
- aliasing the owner check cannot see (`q := p`, the same object passed twice as `\mub`, a
  sub-slice, two values out of one interface): `bw_owner`
- the owner changed other than by plain assignment (pointer-receiver method, `*p =` through an
  alias, `copy(s[:])`, `append(s[:1], 9)` into spare capacity, a stored closure): `bw_invalidate`
- a borrow leaving the frame through an unannotated callee, a returned closure or a channel of
  closures: `bw_storage`, `bw_cap003`

## 3. False positives on ordinary Go

52 recorded. The ones a user meets first:

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
| two unrelated `&Pair{}` borrows conflict (owner name defaults to `"x"`) | BORROW002 | `bw_owner` |
| a local flagged because another function has a borrow of the same name | BORROW001 | `bw_storage` |
| borrows of disjoint fields `&s.A`, `&s.B` | BORROW002 | `bw_owner` |
| the per-iteration copy of a 3-clause loop variable (go1.22+) | SAFE003 | `vetparity_test.go` RB-LOOPCOPY |

## 4. gocap-erase

21 known bugs in `cmd/gocap-erase/stress_erase_test.go`. Matching is by identifier name, with no
scope resolution, which accounts for most of the first group.

| Class | Bugs |
|---|---|
| Behaviour changes silently | ER-01 a local or parameter named `cap` has its own `Move` method call deleted. ER-05 a local `Move` under a dot import. ER-02 `cap.Move[int64](5)` loses its type argument (`int64` becomes `int`; `Move[*int](nil)` compares equal to nil). ER-03 an untyped constant loses the type the call gave it (`cap.Move(1)/2.0` is 0 before, 0.5 after). ER-04 `defer cap.Move(f())` becomes `defer f()`, so `f` runs at exit. ER-19 an embedded `cap.Mut[T]` field is renamed from `Mut` to `T`. |
| Output does not compile | ER-06 marker call as a statement. ER-07 import kept when a local shadows `cap`. ER-08 dot import deleted while `Move[int]` is still referenced. ER-09 constant overflow. ER-10 a field named `Mut` indexed. ER-18 valid input refused with a formatter error. |
| Writes outside the tree | ER-15 `-w dir` follows a `.go` symlink and rewrites its target, exit 0. |
| Crashes | ER-21 `defer cap.Move(1)` or `go cap.BorrowMut(&x)` panics. ER-22 that panic mid-walk leaves the tree half rewritten. |
| Contract and docs | ER-11 marker helpers used as values are left in. ER-12 a blank import survives. ER-13 `-w .` does nothing and exits 0. ER-14 the documented `-w ./...` fails. ER-16 untouched files are reformatted and CRLF converted. ER-23 "bit-for-bit parity": the machine code matches but the binaries differ. |

## 5. Scale, determinism and the two drivers

No analyzer panicked on any generated or hand-written input, including Go 1.27 generic methods,
generic aliases and range-over-func. The problems are cost and contract (`scale_test.go`,
`vetparity_test.go`; n is in each test's log):

| Id | What | Measured |
|---|---|---|
| RB-DIAMOND | one function with N sequential `if/else` around a send: cubic | N=100 1.4 s, N=200 9.9 s, N=400 74 s and 74 GB allocated |
| RB-RESEND | one value sent and used N times: cubic | N=400 1.5 s, 846 MB |
| RB-CHAIN | call chain N deep, callers declared first: quadratic | N=1600 4.2 s against 20 ms callee-first |
| RB-LITERAL | slice literal of N pointers: quadratic allocation | N=20000 5.0 s, 24.9 GB |
| RB-ORDER | the finding set is stable; the order printed is not | differs in 9 of 9 reruns |
| RB-FIX | `gocap -fix` exits 0 and prints nothing when there are findings | |
| RB-TAGS | `gocap -tags=x` is ignored (0 findings where `go vet -tags=x` has 2) | |
| RB-NOPOS | a BORROW004 printed as `-:` with no file, which no hatch can reach | |
| RB-JSONDUP | `-json` prints each finding twice for a package with tests | |
| RB-STALE | a hatch that mutes only a borrow finding is counted stale | |

Held: standalone `gocap` and `go vet -vettool` agree on all 110 distinct findings across 14
packages, facts survive the vet driver's round trip, and every unanalysable input (type error,
syntax error, missing import, unknown go version) exits 1 in both drivers. The race detector is
clean on the checker itself.

## 6. Distance to Rust

| Rust guarantee | gocap today | Fixture | What would close it |
|---|---|---|---|
| No data races | captured locals, receivers, parameters and package variables are caught; publication, unlisted callbacks and dynamic calls are not (sections A to D) | `rs_send`, the witnesses above | transfers for `atomic.Pointer`, `sync.Map`, `sync.Pool`; results of `Load` unowned unless imm |
| No out-of-bounds or type confusion | only through the race rules | `rs_torn_slice`, `rs_torn_iface` | the same |
| Null safety | no rule | `rs_null`, `rs_w_typednil`, `rs_w_nilmap` | a nil-flow analysis; x/tools `nilness` found none of these |
| Exhaustive match, checked downcast | no rule | `rs_exhaust` | a rule over constant enums and unchecked `x.(T)` |
| `Result` must be used | no rule | `rs_errors` | an ignored-error rule |
| Aliasing XOR mutation by default | opt-in by annotation only | `rs_alias`, `rs_w_stale_ptr` | extend BORROW to unannotated `&s[i]` across `append` |
| Locks and WaitGroups not copyable | silent | `rs_alias` | bundle vet's `copylocks` |
| Lifetimes of borrowed buffers | no rule | `rs_lifetime`, `rs_w_scanner` | a "valid until the next call" table |
| RAII | no rule; a return with the lock held is silent | `rs_raii`, `rs_w_lockleak` | report it from the existing lockset; bundle `lostcancel` |
| Checked arithmetic | no rule | `rs_arith`, `rs_w_overflow` | a rule for size multiplication and narrowing |
| No mixed atomic and plain access | caught as a `go` argument, missed in a closure | `rs_w_mixedatomic` | a per-variable rule |
| Statics not writable by other crates | SAFE006 is per package | `rs_immut`, `im_w_xpkg` | check stores into another package's variables |
| `==` and hashing checked by trait | no rule | `rs_generics`, `rs_w_ifaceeq` | a rule for interface comparison |
| Deadlock freedom | no rule (nor in Rust) | `rs_conc` | double `Lock` is reachable from the lockset |

`copylocks`, `lostcancel`, `nilness`, `unusedresult` and `unusedwrite` come from
`golang.org/x/tools`, which gocap already depends on.

## 7. Claims the tool does not yet deliver

- README, "Why only goroutine boundaries": the memory bug that remains in Go is the data race, "a
  torn write to a two-word interface or slice header", so gocap checks where a value crosses to
  another goroutine. The premise is right and the README lists the crossings it means. But
  `rs_torn_slice` and `rs_torn_iface` are exactly that torn write, through a crossing not on the
  list (publication), and gocap accepts both.
- `docs/DIRECTIVES.md` and `docs/RULES.md` show `/*gocap:mub*/` inline on a parameter. It is
  rejected as malformed (`bw_forms`).
- `docs/DIRECTIVES.md` 2.7: a stale hatch "is flagged as stale". It is counted in `Stats` and
  never printed (`dx_hatch`).
- `docs/TOOLING_AND_ERASURE.md`: `gocap-erase -w ./...`. It fails with "no such file" (ER-14).
- `PROJECT.md`: "Bit-for-bit parity". Machine code matches; the binaries do not (ER-23).
- `docs/BORROW_CHECKING_AND_NLL.md` section 6 justifies BORROW004 by "deallocated stack memory".
  Go moves such a variable to the heap; the rule flags the standard constructor (`bw_return`).
- RULES.md SAFE008 says any `net/http.Handler`. Only handlers registered directly through
  `Handle` and `HandleFunc` are checked (`lk_handlers`).

## Closed while testing

Found here and then fixed in the working tree by the other session before the final run. Each is
now a `HIT`, so reverting the fix fails the suite:

- a panic in `holdsPaths` on a `//safe:holds` method called through a method value, which took the
  whole driver down: `lk_holdsvalue`
- imm writes through a helper taking the field's address, a generic setter, `atomic.AddInt64`,
  `copy` and `append` into a slice of imm values, and a cross-package setter: `im_alias`,
  `im_container`, `im_w_helper`
- `cap.Move(j)` treated as still aliasing `j`, which rejected `ch <- cap.Move(j)`: `mv_marker`

## Since the pinned run

The checker kept changing after `ffe873d611b617dd`. At `8aa62108f64a7545` (2026-11-10 09:08) the
fixture ledger is 374 hits, 294 misses, 52 false positives, and `TestStress` fails on the two
regressions below. The tables above were not recounted.

Fixed, markers promoted to `HIT`:

- a hatch trailing a multi-line statement now covers that statement and not the next: `dx_hatch`
- SAFE006 sees a write through a helper taking the address and through a pointer-receiver method,
  including a method of another package: `im_globals`, `im_w_method`, `rs_send`
- a handler calling a func-valued field of its receiver is reported at the call: `lk_handlers`

Regressed, left failing on purpose:

- **Two results that alias each other are no longer tracked** (`mv_alias/alias.go:163,176`).
  `a, b := two()` where `two` returns the same pointer twice, then `ch <- a; b.ID = 1`, is silent.
  Read in `passes/ownership/graph.go` at that fingerprint: the `*ssa.Extract` case gives each result
  of a static call its own node instead of joining it with the tuple, and freshness is now recorded
  per result, but nothing records that two results share memory.
- **The generated test main is flagged in standalone mode only** (`TestVetParity`): SAFE006 on
  `tests`, `benchmarks`, `fuzzTargets` and `examples` in the `go-build` cache file, which `go vet`
  mode does not report. First seen at 09:02; not re-run at `8aa62108f64a7545`.

## Not covered

- cgo (`import "C"`) has no fixture: one would need a C toolchain to load.
- amd64 and Linux: every run was on darwin/arm64. The torn-header witnesses depend on codegen.
- `reflect.Value.Call`, `reflect.Select`, `signal.Notify`, `database/sql` hooks, `testing/synctest`,
  the real `golang.org/x/sync/errgroup` (not importable by the fixtures).
- Reborrows, two-phase borrows, map values as owners, `RWMutex.RLocker()`, `iter.Seq` over guarded
  data, guarded access across packages.
- Scale shapes for the handler walk and for the borrow pass beyond local borrows.
- `gocap-erase`: a crash mid-write (the write is not atomic), concurrent runs, symlink loops.
- The reference-service hatch ratio and the race corpus were not re-measured against any of this.
