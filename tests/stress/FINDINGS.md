# GoSree stress findings

What the adversarial suite finds, as of 2026-11-10 (re-counted 2026-11-11 after that day's branches were merged into one line, and 2026-11-12 after the changes at the end of section 5). Every finding below is
locked into a fixture or a test; the paths are given so each can be read and re-run. Counts come
from the harness's own ledger (`go test ./tests/stress -run '^TestStress$' -v`), not from the text of
an earlier version of this file.

## State these numbers are for

| | |
|---|---|
| Toolchain | go1.27.3 darwin/arm64 |
| Git HEAD | `1b5b79a` on `rework/source`: `main` at `2ef069c` merged into the branch at `a11ce98`, and the commit after the merge that carries this text. The branch at `a11ce98` is `3f0c738` (seven commits that change a checker file, on top of `b2f480c` and a merge of `main` at `3322a22`, `a2801ef`) and six commits after it that change no checker file. `2ef069c` changes `lockset.go`, comments in `graph.go` and two test files |
| Checker fingerprint | `84db323e0c9300ae`: `bash tests/stress/fingerprint.sh`. It was `0e10f1465156690c` at `3f0c738` and up to `a11ce98` |
| Run | 2026-11-12 late night, at `9f4c592` on `main`, which changes no checker file (fingerprint `84db323e0c9300ae`): the two reach tests of `cmd/gosree-corpus` bound the number of type nodes `reach` visits instead of a one-second duration, after the failure recorded next. The visit counts were the same in seven runs (cgo on and off, and under `taskpolicy -b`) while the duration ran from 83 to 255 ms, and a walk made to run twice fails both tests; the one-second failure itself was not reproduced in 16 tries, so no run shows the old test failing on demand. A duration now fails those tests only past 60 s. On the `f0dea98` copy with the two changed files, load average 54 falling to 22: `go vet ./...` and `go test ./... -count=1 -timeout 90m` with cgo, 17 packages with tests, all `ok`; that load was lower than in the run that failed, so this run does not by itself show the fix. The reference service (`bb60b53`) was measured again the same night without cgo, with the census tool built from an archive of each commit, and every figure recorded for it in section 5 came out the same: production 2,037 rows at `08bb22f` and at `2ef069c` (432 of 578 transfers flagged, SAFE008 420) and 1,958 at `4f11abb` (431 of 578, SAFE008 344), 83 gone, 4 new, 6 reworded; annotated 694 with 344 hatched and 690 with 343 hatched and 426 flagged; with tests 7,719 at `2ef069c`, which had not been run there, and 7,645 at `4f11abb` (3,106 of 4,805 flagged), 87 gone, 13 new, 9 reworded. Two production runs of one binary gave the same rows. `census.json` from the tool before and after `9f4c592`, three runs each, agrees on every hatch, class, child and closure; the text of its `reasons` differs in all six, between runs of one binary as well, and the cause was not traced. Not run again: the standard library, the race corpus, the Rust suite (no Rust file changed) and the Lean step of CI. Before that, 2026-11-12 night, at `f0dea98` on `main`, which differs from `1b5b79a` in this file only: the Rust suite and the tests with cgo, on a `git archive` copy of that commit beside a `git archive` of gusset at the pinned `0ab48ca` (the live gusset checkout had uncommitted changes and was not used), load average 67 to 226 from other work on the machine. The four Rust commands of `.github/workflows/ci.yml` exit 0: `cargo fmt --all -- --check`, `cargo clippy --locked --workspace --all-targets -- -D warnings`, `cargo test --locked --workspace` (24 tests pass, the 21 recorded before plus the three `80a766b` added) and `cargo build --locked --release`. With cgo, `go vet ./...` is clean, and one run of `go test ./... -count=1 -timeout 90m` has 16 of 17 packages `ok` and one failure: `TestAdv2ReachGenericDAG` in `cmd/gosree-corpus` returned the two expected types in 1.10 s against its one-second bound. It was not reproduced: alone it passed 10 of 10 (five with cgo, five without, 78 to 187 ms), and with its whole package under cgo 3 of 3 (156 to 191 ms). The load average in those reruns (98 to 116) was that of the full run (103 to 113), so the load average alone does not explain the failure; what differed was the other 16 packages' tests running beside it, which is inferred. No Go file of that package changed after `08bb22f`. `go list` gave `engine_unix.go` as the cgo file of `internal/gusseterr` and `engine_other.go` as ignored, so the Rust solver was the engine of that run. `tests/stress` passed; `TestScale`, which failed once under cgo at `78f0879`, skips only under `-short`, which was not set. Not run at this commit: the Lean step of CI, and the corpora (their figures stand as measured earlier on 2026-11-12). Before that, 2026-11-12 evening, at `1b5b79a`, load average 100 to 230 from other work on the machine: `go vet ./...` and `go test ./... -count=1 -timeout 90m` without cgo, 17 packages with tests, all `ok` (`passes/ownership` took 23 minutes at that load). The merge had no conflict and no marker was edited, so the ledger below stands as counted at `dcbc28b`. Not run at this commit: the tests with cgo and the Rust suite; neither side of the merge changed a Rust or cgo file. Before that, 2026-11-12 afternoon, at `3f0c738`, with nothing else of this run alongside: `go vet ./...` and `go test ./... -count=1 -timeout 60m` without cgo, 17 packages with tests, all `ok`. The markers were not edited and `TestStress` holds, so the ledger below stands as counted at `dcbc28b`. Not run at this commit: the tests with cgo and the Rust suite; no Rust or cgo file changed. Before that, 2026-11-12 morning, at `dcbc28b`, load average 50-65: `cargo test --release` and `cargo build --release` in `rust/`, `go vet ./...` and `go test ./... -count=1 -timeout 60m` without cgo and with it (17 packages with tests, all `ok` in both; `passes/ownership` takes about twelve minutes at that load). The ledger below was counted at that commit by `go test ./tests/stress -run '^(TestStress|TestWitnesses)$' -v` without cgo (58 of 58 witnesses convicted). The acceptance tiers were not run again at this commit; `25779ca` on `run/integ-1111` had them at 12/12. The 2026-11-10 suites are listed under [What was run](#what-was-run) |

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
| SAFE003 use after move | 179 | 44 | 12 |
| SAFE004 moved and escapes | 3 | 0 | 1 |
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
| **Total** | **627** | **173** | **46** |

The ERR005 row changed on 2026-11-11, and every change is in the new root `er_w_rangecapture`:
- Two hits: a deferred `Close` on a resource captured by a closure, and a deferred `Close` in a range-over-func body with the write in the body.
- One miss: a `defer Close` in a range-over-func body, with the write after the loop. The defer runs at the enclosing function's return, but the close is resolved at the yield function's synthetic return.
- One false positive: a captured resource reassigned by another closure (`replacedByCall`). The written flag is not reset across that call.

The fixes that day touched errcheck, ownership, borrow and the corpus tools. No existing marker changed.

The SAFE003 row changed on 2026-11-12: five known misses are reported and their markers are now
`HIT`. Each is a use after a call through a method value (section 5): of an iso parameter in
`mv_iso`, `mv_marker` and, for a method of another package, `sp_xpkg`; and of `WaitGroup.Go` in
`sp_table` and `sp_w_methodvalue`. No other marker changed in that day's seven commits.

Runtime witnesses: 58 programs carry a `WITNESS` marker, 45 for the race detector and 13 for a panic
or fatal error, and `TestWitnesses` passes on all 58 (the runtime convicts every one). For 33 of
them the checker accepts the program (the file carries a `MISS`); the other 25 carry no `MISS` and
at least one `HIT`, so the hazard is reported statically. Both numbers were counted from the files
at `dcbc28b`. This text said 37 and 21 before; by the same count it was 34 and 24 until
`sp_w_methodvalue` was promoted on 2026-11-12. Known analyzer crashes: 0.

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
shareable (`internal/kinds/kinds.go`), so a value converted to one stopped being tracked. Any
`func()` converts to `CancelFunc`. Since 2026-11-11 a value boxed into an interface is tracked
when the value itself is, whatever the interface (`graph.go`, the `MakeInterface` case), which
closes the first and the last line below; the others are as they were.

- pointer sent or spawned as `error`: reported now in `sp_w_error`, `mv_w_error`, `sp_launder`
  and `mv_shareable`; `im_w_errfield` is as it was
- closure over a local as `CancelFunc`: `sp_w_cancelfunc`, `mv_w_cancelfunc`
- `context.WithValue` carrying a mutable pointer: reported now in `sp_w_ctxvalue`, `mv_w_ctxvalue`,
  `mv_shareable`, `sp_launder`; custom `context.Context` implementations remain open (`sp_launder`,
  `mv_shareable`)
- knock-on in fork-join: a joined goroutine hands the value to a grandchild inside an `error`, and
  `wg.Wait` returned ownership to the parent while the grandchild still writes: reported now in
  `sp_w_grandchild` and `sp_forkjoin`

### C. Only static calls are modelled

Spawners and `//gosree:iso` parameters are recognised by the static callee. The same thing reached
through an interface or a func variable is an ordinary call. A method value is the method since
2026-11-12 (section 5): `WaitGroup.Go` as a method value (`sp_w_methodvalue`, `sp_table`) and an iso
parameter through one (`mv_iso`, `mv_marker`, `sp_xpkg`) are reported.

- `WaitGroup.Go` behind an interface (`bw_race_ifacego`, `sp_table`) and `time.AfterFunc` in a
  package func variable (`sp_table`)
- an iso parameter through an interface, func value or generic helper: the callee
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
  elsewhere" note. The same holds for the 664 diagnostics of the 133 fixture roots there were then
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
  133 fixture roots there were then are the same lines before and after. Of the old limits, four were reached
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

- The merge of 2026-11-11. The main checkout's uncommitted work (committed as `2fd93f0`) and
  every branch of that day were merged into one line: `run/integ-1111`, the four branches made
  after it (`run/b6a2-ownership`, `run/3b3c-errcheck`, `run/3b3c-adv2`, `run/b6a2-adv2`),
  `harden/gosree-gates`, and `harden/merge-main-1111`, a second merge of the first two made by
  another session at the same time. What the merge itself changed, beyond joining the two sides:
  - Three defects were fixed, each with a test that fails without the fix; the first two came in
    with the branches and the third is older than both sides. (1) `60968b7` skipped the search for escaped package variables in a package with no
    direct call to a freezing publisher, so a publish reached through an interface took an escaped
    table for init-only and lost both its findings; the set is now built when the exemption first
    asks (`globals_escape_routes_test.go`). (2) A printing call handed a slice that had been
    written through (`s[0] = g; fmt.Println(s...)`) was judged harmless though fmt runs the
    element's `String` method; `404bb00` carried that as a failing test with no fix
    (`TestAdv2SliceStoreThenSpreadBreak`). (3) A `go` statement on a variable that may hold one
    of two closures reported what one of them borrows. Two more changes came with
    `harden/merge-main-1111`: a borrowed parameter handed to a publishing container is CAP003
    (the check skipped every parameter), and CI runs the tests with a 30-minute timeout.
  - One change of the checker came from `harden/gosree-gates`: an interface value is tracked when
    the value boxed in it is. A pointer boxed as an `error` counted as shareable, so handing it
    to a goroutine or a channel was no transfer. Ten fixture lines went from miss to hit (ledger
    612/188/46 before it, 622/178/46 after). The rest of that branch's pass files are an older
    form of work this line has in a later form and were not taken; its tests for them are here
    and pass. Three fixture lines it marks as hits are not reported here and keep their markers
    (`rs_w_mixedatomic`, two in `lk_w_getter`).
  - Measured without cgo, the same four runs as above. Before the merge (`2fd93f0`), after the
    branches and the first two fixes, and after the boxed-value change:

    | Run | before | merged | with the boxed-value change |
    |---|---|---|---|
    | reference service, production | 2,036 | 2,036 | 2,036 |
    | reference service, with tests | 7,721 | 7,721 (limit raised) | 7,721 (limit raised) |
    | reference service, annotated | 694 | 694 | 694 |
    | standard library, with tests | 13,029 | 13,031 | 13,073 |
    | fixtures (`TestDump`) | 664 | 667 | 677 |

    On the reference service no finding changed in any of the three runs at either step (same
    package, position, rule and message). On the standard library the merge adds two findings
    and renames the package variable in two (`net/conf_test.go`: a variable now seen to escape,
    and the choice among variables at one position made stable); the boxed-value change adds 38,
    of which 37 are in `net/http/serve_test.go` and one in `context`: values boxed in an
    interface, handed to a goroutine and used afterwards. They were not read one by one. The
    fixtures gain the three lines of the new root `er_w_rangecapture`, then the ten hits. The
    changes made after the boxed-value one (CAP003 for a published borrowed parameter, every
    closure a started value may be, the two limits) change no line of any of the five runs.
  - "Limit raised": since `a6693fc` the build without cgo enforces the solver's limits as the
    Rust engine does, so the run with tests now stops at `internal/wisdev` (18,973 functions,
    limit 16,384), as it always did with cgo. The with-tests column comes from a second binary
    built from the same tree with only `MaxFuncs` raised to 65,536 (`go build -overlay`); the
    product was not changed. The measurements made earlier in this section without cgo did not
    pass through that limit.
  - The 43 "take one part out and see a test fail" checks of the earlier fixes were run again at
    `9823ff0` (the branches and the first two fixes merged; before the boxed-value change and
    what followed): each of the 43 still makes a test fail.
  - After the merge, on the same day, four more changes went in on top of it (`d754bb7` to
    `b5e77e1`). Where one of them changes a finding in a corpus it says which.
    1. Whether a trace was cut at a limit is no longer a flag on the pass that a check clears,
       sets and reads. A check asks for a question, the traces are methods of the question, and
       a third test that reads the source (`TestEveryQuestionIsRead`) fails for a check that
       asks and does not read the answer, unless it is listed with a reason. No finding differs,
       also with the limits turned down to 6 values and 4 steps, where the `-incomplete`
       findings go from 1, 169 and 96 to 121, 659 and 848.
    2. A function that returns what one of several callables or receivers handed to it returns
       was taken to return a fresh value as soon as one of them did, so `pick(c, &pool, shared)`
       was sent on as owned. Fresh now needs every returned value to be fresh
       (`fresh_forward_test.go`: 11 cases that were not reported). No corpus finding differs.
    3. A publishing contract at a call stood for everything the callee does with the argument,
       so a helper that publishes its argument and also stores it, or hands it to a second
       callable, was a publish and nothing more; the same statements written out were reported.
       The publish is still taken from the contract and what the callee does besides is an
       escape as well (`publish_and_more_test.go`: 15 cases that were not reported). Standard
       library with tests: 14 findings more and none fewer, all SAFE004 at calls of helpers
       that publish an argument and do something else with it: `(*http.Server).Serve` and the
       four functions that call it (5), `gzipPoolPut` in `net/http` and `http2` (4),
       `httptrace.WithClientTrace` (2), `(*reflect.rtype).ptrTo` (3). At each of those call
       sites the publish was already reported. They were read, not judged as defects.
    4. A call of a variable that holds one of several functions was resolved to one of them:
       the first closure found, with a function that captures nothing skipped. The lock check
       took "locks, or does nothing" for locking, in either order, and the shared-load check
       and the handler write check followed "reads, or writes" into the reading closure. All
       three now ask for every function the value may be, and for what else it may be
       (`callTargets`; `resolveClosureVal` is gone). The lock check takes each function's part
       on its own: what any one releases is released, and a lock is held afterwards only if
       every one takes it and nothing is left unresolved. A method used as a value
       (`f := s.unlockIt`) is looked into as the method it is, which it was not before
       (`lock_targets_test.go`, `one_of_several_test.go`, `handler_targets_test.go`: 19 cases
       that were not reported and 2 that were reported wrongly). Reference service: one finding
       more in production code and with tests, 2,037 and 7,722: SAFE008 at
       `internal/api/internal_ops.go:134`, a call of "a function read from the handler, or a
       method of the handler". It is the shape already reported at
       `internal/api/paper.go:1207`, where the second possibility is a plain function; here it
       is a method value, and the old code followed that one alone. Nothing else differs: the annotated run (694), the standard library
       with tests (13,087) and the fixtures (677), and the same one row with the limits turned
       down to 6 and 4. The test that reads the source for dropped walk answers now covers
       `callTargets` too, and a second one lists the ten functions that may ask for the
       functions without what else the value may be
       (`TestCallersThatAskForAnyTargetAreListed`); a dropped answer planted in two places and
       a new caller of each helper passed before and fail now.
       `TestSharedLoadCalleeThroughManyConversions` had its expectation changed: a writer
       converted between two function types 16 times is now found and its write reported,
       where the test used to expect "could not be followed"; a case where the function comes
       in as a parameter still expects that.
    Each change has tests that fail without it, and each of its parts was taken out in turn to
    see a test fail.
  - On 2026-11-12 five more changes went in on top of those (`0427d1e` to `d921195`, six
    commits and one of tests), on the branch `rework/one-tracer`. Where one of them changes a
    finding in a corpus it says which. Changes 1 to 4 are the fold and the two defects it was to be followed by. Change 5
    was found while 3 and 4 were written, goes further than they do and changes what is
    reported, so it can be told apart: `ff81ad1` is the head without it.
    1. The eleven walks that follow a value backwards are one walk (`passes/ownership/trace.go`,
       `trace.follow`). What is asked about the value is a parameter of the walk: the values
       it may be, the allocations it may point to, a field of it, what is stored at it. The
       limits and the visited set are in that one function, and each step (the arguments of a
       parameter, the bindings of a captured variable, what a call returns, the stores to a
       cell or a package variable, a field behind a pointer) is one method. Before the old
       walks were removed they were run beside the new one and compared at every question:
       30,248,001 questions in the package's tests and 21,619,660 in nine corpus runs at both
       settings of the limits, no difference. No finding differs. 798 lines out, 636 in.
    2. At a call through a method value (`g := r.second; h := g(noop, p.Store)`) the trace
       took the argument for a parameter one place to the left, and nothing for the first. A
       method that hands back one of its arguments was read as handing back the other
       (`method_value_args_test.go`: 6 publishes that were not reported and 5 reported that
       were not there). The step had been in two of the three copies. No corpus finding differs.
    3. A local variable that a closure captures, or whose address is taken, was read at what
       its own function stores to it: `f := noop; set := func() { f = p.Store }; set(); f(x)`
       called `noop`. The stores are now read through every address the variable has
       (`usesOfCell` in `graph.go`, which `callTargets` already did for itself and now shares),
       and a variable whose address goes somewhere that is not read answers "may not be all"
       (`cell_internal_test.go`, `cell_writes_test.go`: 8 publishes that were not reported).
       No corpus finding differs.
    4. The address of a variable or struct passed to a function of this package is followed
       into that function (`set(&f)`, `s.arm()` with a pointer receiver, `arm(q)`), where the
       parameter is one more address of the same memory. A call that runs one function with a
       body is followed; a method of an interface, a function held in a variable and a
       function of another package leave the memory "may not be all". One reading stops at 64
       names of the memory it reads (`cellViewLimit`, listed in `walkBounds`, both sides
       tested), and since change 5 each pointer variable that holds the address is read on a
       count of its own. `callTargets` reads cells the same way, so a function variable set
       through its address is among the targets of a call of it; the lock check takes that as
       it takes any targets (`TestLockThroughAVariableWhoseAddressIsPassedOn`), and did not
       then match a release by a function made in the helper (pinned as a known miss; matched
       since the later changes of that day, below) (8 publishes that were not
       reported). Standard library with tests: 5 findings
       fewer, 13,082. They are the "could not be followed to the end" rows at the four calls
       of `f1` in the generic compose helpers of `net/http/httptrace` (`trace.go:217`, `230`,
       `243` twice, `256`). The chain, printed from a scratch build, ended one step over the
       depth limit; with the address followed into `compose` a store is found one step sooner,
       and the walk ends at a value read from a context, which it cannot follow and does not
       claim to. The callees are no more known than before.
    5. A struct was read through the allocation alone, so a store through another name for its
       pointer was not read: `s := newS(); s.fn = p.Store; s.fn(x)` called what the constructor
       had put there. The same for a pointer chosen on a branch, a pointer kept in another
       struct or a package variable, and a pointer variable a closure captures. The reading
       now takes as further names the phi where two pointers meet, a pointer converted to a
       named pointer type, the result of a call when the function the address was passed to
       returns it, and each load of a local pointer variable the address is stored in. Where
       the address goes somewhere that is not read (returned by the function that made it,
       stored in a field or a package variable, put in an interface, passed to a function
       that is not known), every store the package makes to that field of that struct type
       stands in, as it already did where a pointer leads nowhere in sight
       (`struct_alias_test.go`: 6 publishes that were not reported). Reference service with
       tests: one finding fewer, 7,721, the "could not be followed to the end" row at
       `cmd/server/shutdown_handlers_test.go:123`. The struct there is held in a pointer
       variable that closures capture; its field used to be answered by every store to that
       field in the package, a chain that ran over the depth limit, and is now answered by
       the stores made through the variable. With the limits turned down to 6 values and 4
       steps the standard library gains 15 "could not be followed to the end" rows, all
       `instr.op(instr, state, ...)` in `encoding/gob/codec_test.go`: the struct is passed to
       the function held in its own field, which is not known, so the package's stores to
       `encInstr.op` stand in and are more than 6 values. Nothing else differs at either
       setting: reference service 2,037 in production code and 694 annotated, standard
       library 13,082, fixtures 677. A review of this change found one case it answered as
       complete that was not: a function handed both the struct and the pointer variable that
       holds it (`pick(q, &q)`) can return the struct to a call that passed neither, and the
       return was taken back only to the calls that passed the struct. Such a function now
       leaves the struct "may not be all" (the sixth commit; no corpus finding differs).
    The corpora do not say what the stand-in of change 5 costs in reports of stores made to
    another struct of the type: almost no corpus finding rests on it at the normal limits.
    Each of the 29 parts of the reading that changes 3 to 5 added, and the 3 added on review,
    was taken out in turn and makes a test fail. Two guards in it have no test, because no
    program that compiles reaches them: a call with more arguments than its callee has
    parameters, and an address used by a call otherwise than as an argument. What the reading
    costs in time was not measured: it is not cached, and the wall times of these runs, at a
    load average of 30 to 65, say nothing either way.
  - Later on 2026-11-12 the six gaps that were then listed under "Not covered" were taken up, in
    seven commits on the branch `rework/not-covered` (`5bfec07` to `dcbc28b`, on top of
    `ae6f578`). Each has tests that fail without it, and each of its parts was taken out in turn
    to see a test fail; a part with no test is named. The corpora were run after every commit at
    both settings of the limits, and every row that moved is accounted for below. What was left
    open is under "Not covered".
    1. `5bfec07`. A call of a variable returns a fresh value when every function the variable
       may hold does (`mk := newA; if c { mk = newB }; return mk()`); before, only a function
       called by name did. The summaries were worked out by the functions a body names, and a
       call of a variable is answered by functions the body need not name, so a summary read
       while another is worked out is now recorded and its reader goes round again when it
       changes (`called_variable_fresh_test.go`: four calls reported that should not be, and
       one, `mkOld`, that kept "fresh" from a summary later withdrawn). Reference service with
       tests: 6 findings fewer, 7,715, the rows about the result of
       `(*dynamicQuestionOptionsFlight).do` in `questioning_options_flight_test.go`; its result
       is a call of its `build` parameter, and every caller passes a closure that returns a
       fresh value. Standard library with tests: 14 fewer, 13,068: 12 at
       `t.Run(..., checker(...))` in `os/exec/dot_test.go`, and `net/cgo_unix.go:68` and
       `unique/canonmap_test.go:83`. With the limits at 6 and 4: 6 and 13 fewer.
    2. `de4abd0`. What a transferring call hands back is not a reason why the value it was
       handed is "not owned". `actual, _ := m.LoadOrStore(k, c)` reported `c`, this function's
       own allocation, as published but not owned, because it "may alias" the call's result;
       with the result dropped there was no report. The reason is left out when it comes from
       the transferring instruction itself, unless that instruction can run again after itself
       (in a loop the value may be what an earlier round handed back). This holds for every
       transfer, not only a publish (`publish_own_result_test.go`: 3 reported that should not
       be, 4 that must still be). It was found through the next change: a fixture that calls
       `LoadOrStore` through a method value and keeps the result. Standard library with tests:
       2 fewer, 13,066, at `context/context.go:653` (`time.AfterFunc`) and
       `net/http/internal/http2/clientconn_test.go:321`; the same 2 at 6 and 4. Five messages are reworded.
    3. `525ea45`. A method called through a value is the method. A method value (`g := r.m`)
       is a closure over a wrapper and a method expression (`g := (*R).m`) is a thunk; both
       only call the method, and three readers looked the method's facts up under the wrapper,
       where there are none: the contract of a call, the summary, and the index of call sites.
       A publish through a method value was not seen at the caller, a struct argument of a
       method called this way was answered by every store to its field in the package, and a
       direct call beside a call through a value was taken for all the method's calls
       (`method_value_args_test.go`: 19 cases fail without the change, 11 not reported and 8
       reported that should not be). Five fixture misses are reported now (the ledger above),
       and one fixture message is reworded (`rb_closures`). No corpus finding is added or
       removed at the normal limits; 7 messages are reworded. At 6 and 4 the reference service
       has one more "could not be followed to the end" row in production code,
       `internal/api/paper.go:1207`.
    4. `2f78d0c`. A local array of functions is read like any other local memory. The step
       read the stores made through the array's own element addresses in its own function,
       and `arr := [2]func(*T){noop, p.Store}` has none: the literal is built in memory of its
       own and stored to `arr` whole. The same for an element a closure writes and for
       `for _, f := range arr`. The array is now read through every name it has, with the
       elements of every array stored to it whole (`array_internal_test.go`: 8 of 11 arrays
       found nothing; `array_of_functions_test.go`: 7 of 10 uses after a publish were missed).
       Nothing differs at the normal limits. At 6 and 4 there are 4 more "could not be followed
       to the end" rows in the reference service's tests (`attempts.go:172`,
       `recorder.go:172`, `autonomous.go:2006`, `runtime_state.go:2358`) and 4 in the standard
       library's (`database/sql/sql_test.go:3100`, `go/types/check_test.go:176`,
       `net/http/cgi/host_test.go:99`, `net/http/routing_index_test.go:120`). Three of the
       standard-library four were read: each calls an element of a slice of functions (a
       variadic parameter, a list of checks), whose array is now read and holds more than 6
       values. The fourth and the reference-service rows were not read.
    5. `3e3ecea`. A function made elsewhere unlocks what its maker's values name. The lock
       check keeps a function's locks as paths from that function's values, so a release by a
       function that a helper stored through the address of a variable
       (`s.setUnlock(&f); f()`) matched nothing and was dropped. A value such a function runs
       on is now brought to the function being checked: a parameter of the helper is the
       argument at each call the address went in by, a variable of the helper that holds one
       of its parameters is that parameter, a captured variable is what the closure was made
       with. A value that cannot be brought is lost, and then nothing is held after the call
       (`lock_targets_test.go`: 10 of 11 writes after such a call were not reported; 8 where
       the function unlocks the mutex of another struct stay quiet). No corpus finding differs
       at either setting.
    6. `9a42a24`. A call of a closure that publishes a variable it captured is a publish of
       the variable, in the function that owns it. The closure was told that the variable is
       not its own to publish (SAFE002) and the owner was told nothing about a use after the
       call; a caller of a function that publishes its parameter this way was told nothing
       either (`closure_publish_test.go`: 9 of 10 uses after such a call were not reported; 6
       must stay quiet, among them a deferred closure where it is deferred). No corpus finding
       differs at either setting.
    7. `dcbc28b`. What stands in for memory whose address is out of sight. For a struct,
       every store the package makes to the field of that struct type stood in, also a store
       to another allocation: `s := newS(); u := &S{fn: p.Store}; s.fn(x)` was reported. The
       index now keeps what is known of the address each store was made through, and a store
       known to be to another allocation does not stand in; one through a pointer that came
       from anywhere else, or that may be the allocation asked about, still does. So that a
       struct copied over the one asked about is not lost (`*keepS = u`), the struct values
       the package stores whole stand in with their field. For a variable or a field that
       holds a function, the functions the package stores through a pointer (`*p = f`) stand
       in when the address is out of sight, where nothing did: `f := noop; o.set(&f); f(x)`
       called `noop` (`struct_alias_test.go`, `address_out_of_sight_test.go`: 13 cases fail
       without the change, 5 not reported and 8 reported that should not be). Reference
       service with tests: 2 findings fewer, 7,713, the two "could not be followed to the
       end" rows at one call in `fulltext_version_links_run_test.go:260`. They are not from
       the stand-ins: where a pointer leads nowhere the stores by type are now read once on a
       walk, and a second reading made past the depth limit used to mark the question as cut
       though every value had been visited already. With that one line taken out the two rows
       are back. Nothing else differs at either setting. A review of the reading-once found
       that its first form could lose a finding: a reading made at the depth limit reads
       nothing, it counted as the one reading, and a later one from a shallower step was
       skipped, so a publish that used to be reported became "could not be followed to the
       end" (`read_once_depth_test.go` fails on that form). The record now keeps the depth of
       the reading, and one from a shallower step is made again. Thirty-four parts of this
       change were taken out in turn and each makes a test of the ownership package fail. Two
       have no such test: leaving a store to the variable itself out of the list of stores
       through a pointer changes no answer, and reading the stores by type once rests on the
       first scale shape below, which fails its bound without it.
    After the seven: reference service 2,037 in production code, 7,713 with tests and 694
    annotated; standard library with tests 13,066; fixtures 682 diagnostics. With the limits at
    6 and 4: 2,158, 8,202 and 13,843. Before them: 2,037, 7,721, 694, 13,082 and 677; 2,157,
    8,204 and 13,854.
    What change 7 costs was measured, because a review asked. Its first version passed every
    test and could read the copies of a struct again at every struct it came to, until the
    depth limit cut the question. Three shapes were added to `scale_test.go`. N structs kept
    in one package variable with N copies made through it take 0.30 s and allocate 108 MB at
    N=400 before the change, and 1.1 s and 1,084 MB after, growing with the square of N in
    both; a version in between took 13.5 s and 24,153 MB and grew with the cube, because it
    read the stores by type again at every copy and built their list anew each time. Two structs copied
    over each other by N functions, and N function variables with N stores through a pointer,
    grow in line with N. The stores through a pointer are followed on the walk's budget of 96
    values, one for each function that makes such a store: in a package with more of them than
    that for one function type, each call of a function variable whose address is out of sight
    says it "could not be followed to the end" (the third shape: 2 findings before the change
    at any N, N+2 after at N=100, 200 and 400). No row of that kind appeared in the corpora, at
    the normal limits or at 6 and 4.
    In time, change 7 as it was before the review's correction to the record (`1ba9edd`; the
    correction leaves the allocation of the three shapes as it was to the megabyte) was
    compared with the commit before it by CPU seconds, runs interleaved,
    at a load average of 50 to 65 where wall time says nothing. The fixture suite
    (`TestStress`), three runs each: 5.72, 5.71, 5.74 before and 5.70, 5.55, 5.88 after. Most
    of the ownership package's tests, two runs each: 465 and 498 before, 465 and 489 after. No
    difference shows at this spread.
    The first six changes were not timed.
  - The two tests that read the source (`walk_answers_test.go`) were themselves tried against
    seven planted defects: a sub-walk's answer dropped, an answer passed straight to another
    call, a counted loop, a size limit written as a number, a `for range` with a count, a new
    named limit, and a listed limit removed. Each makes one of the two fail. The first version
    of the limit test missed the counted loop and the written-out size limit.

  - Later still on 2026-11-12 the question was turned round: not which shape is missed next, but
    where real code puts its calls and what the checker knows at each. Four of the seven commits
    above moved no finding on the corpora at the normal limits. A build with a counter at every
    call (not committed) was run on the reference service's production code and on the standard
    library with tests, and x/tools' whole-program call graph (CHA, then VTA, in
    `golang.org/x/tools/go/callgraph`, already a dependency) was built for both as an oracle:
    28 s and 4.5 GB for the service, 229 s and 16 GB for the standard library with tests. Six
    commits that change the checker came of it on the branch `rework/source` (`eb228af` to
    `5f13bac`, on top of `b2f480c`). Every number below is for `5f13bac` against `b2f480c` unless
    it says otherwise; the corpus rows are the same at `ba9c7f4`, `b5f6c76` and `5f13bac`.
    While this was worked on, `main` moved from `b2f480c` to `3322a22` (twelve commits of other
    work). It was merged into the branch (`a2801ef`) and a seventh commit followed (`3f0c738`); no
    corpus row differs between `5f13bac` and `3f0c738`. What the merge showed is the last item
    below.
    - **Where the calls are.** Reference service, production code: 87,917 call sites, 53,344 of
      which hand over memory (an argument that is not of a shareable type). Of those, 64.9% are
      calls by name (19,511 in the package, 15,079 into another package, each of them with a
      summary read from a fact), 32.8% are builtins, 1.5% are interface calls (805) and 0.8% are
      calls through a function value (434). Standard library with tests: 158,394, with 1.6%
      (2,600) and 1.5% (2,362). The walk that follows a called value serves about one call in
      forty.
    - **What the walk knows there.** Calls through a function value, reference service: every
      target at 230 (53%), some at 61, none at 143. The second, smaller resolver (`callTargets`)
      has every target at 25 of the 230. No site is cut by the depth or the budget limit: the
      count of cuts is 0 on the service. The reason given most often for "may not be all" is a
      parameter of an unexported function with no call in sight (62 sites, the only reason at
      each). Interface calls: every type at 5 of 805, none at 680. The whole-program graph
      resolves 711 of them, 562 to methods in the calling package or packages it imports, 71 in
      the calling package alone and 78 to methods of packages it does not import (`GetOwnedJob`
      is called in `internal/wisdev` and implemented once in production code, in
      `internal/api`, which imports it).
    - **What knowing the targets would change, for each argument.** For the calls the
      whole-program graph resolves and the checker does not, the summaries of the graph's
      targets were read for each argument that carries memory. Calls through a value: 158 calls,
      248 such arguments; a target keeps and writes 82% of them, 11% are only read, and at 21
      calls (13%) every argument is only read. Interface calls: 711 calls, 789 arguments (the
      receiver not counted); 73% kept and written, 15% handed to a target with no summary, 7%
      only read, and at 46 calls (6.5%) every argument is only read. So resolving these calls,
      with the targets' summaries as they are, would clear about one in eight calls through a
      value and one in fifteen interface calls;
      the rest are kept pessimistic by the summaries, which hold one bit for each parameter over
      a class of values that does not tell fields apart. Across the service's 13,701 functions
      with a body, 22% keep and write some parameter; at calls into other packages the callees
      that say so most are `fmt.Errorf` (1,509 calls), `fmt.Sprintf` (1,477) and the `log/slog`
      functions (about 1,700), whose bodies reach reflection and interface calls.
    - **The known misses of the fixture suite have no one source.** The 173 markers were sorted
      by where the checker loses the hazard: 62 have no rule, 24 are a boundary the checker does
      not know (a registry, an unlisted callback), 24 a way a value travels that one check does
      not follow, 14 a called value not resolved, 14 the trust base, 11 the table of types
      assumed shareable, 9 a kind of access that is not counted, 7 out of the package, and 8 in
      three smaller groups. About half were sorted from the marker's own text without running
      the fixture.
    - **What was wrong in the checker and is changed.**
      1. A call through a function value whose targets are all known took nothing from the
         targets' summaries: every argument escaped and was written, the result was of unknown
         origin, and a target that owns its parameter or starts a goroutine with a callback did
         not move it. Such a call is now the call of its targets (`graph.callOfKnown`,
         `state.calledFunctions`): an argument is moved if one of them moves it, kept if one
         keeps it, written if one writes it; the memory any of them joins is joined; the result
         is fresh if every target's is. A helper that only hands its own parameters to a
         function it was given (`func apply(f func(*T), j *T) { f(j) }`) stays the question of
         its callers, as before (`eb228af`, `a303048`).
      2. The handler write check and the package-variable check each had their own answer for
         a call through a value. Both now ask the same question. A closure made elsewhere and
         read from memory the handler shares still counts as writing that memory when its
         summary says it writes something it captured (`eb228af`, `a303048`).
      3. "Every target is known" now takes a report away, so three places where the walk said
         it without being able to know were closed. A method this package calls through an
         interface has callers the list of its calls does not show (`a303048`), also where
         the interface call is made by a wrapper the compiler writes, for a method value of the
         interface or of a struct that embeds it (`5f13bac`; found by asking of each of these
         rules which other way the same thing is written). A package
         variable, or a field of one, whose address is stored, returned or passed on can be
         assigned through that address anywhere: `var LstatP = &lstat` in `path/filepath`,
         `hookSetter(&testHookEnterRoundTrip)` in `net/http` (`ba9c7f4`, `b5f6c76`). These two
         were found by the oracle on the standard library, not by a fixture. The stores to a
         package variable have two readers in the walk and both now read that record.
      4. In a forwarding helper, the targets' joins put a key and the value stored under it in
         one region, and the escape accounted to the key was read as something the helper does
         with the value besides its contract (`7a0f643`).
      5. The handler write check treated a builtin as a call of something unknown: `len` of a
         map every request shares was "passed to a dynamic function call, which may write it".
         The builtins that write are writes already; a call of any other now ends there
         (`ba9c7f4`). Found by reading the rows that were left, not by a fixture.
      6. `main` had gained `fde27e1` meanwhile: another review of the same code names four
         shapes where the trace says "these are all the functions" and cannot know, and stops
         taking a call of a variable as fresh by its targets until that is honest. Three of
         its four shapes had their rule here by then (item 3). The fourth had not: a channel
         made here and handed to another function, which may send on it. Merged with this
         branch, the report its test holds was lost again. The walk now says "may not be all"
         of a channel with a use that is not a send, a receive, `len`, `cap` or `close`
         (`3f0c738`). With the four shapes answered, the four calls of fresh functions that
         `fde27e1` reports on purpose are quiet again, as its test asks, and its test of the
         four hidden shapes still holds.
    - **Written down before each run**, with what came. First round (the call of known
      targets): rows worded "passed to a dynamic function call, which may write it" 108,
      predicted 62 to 100 with at least 8 gone outright, came 82. The prediction
      was made from what the targets do with their arguments and undercounted, because the rows
      that went were about the called value itself, a captured function variable. SAFE004 rows
      of that wording 2, predicted 0 to 2, came 0; every other category within 20 rows, came
      within 3; transfers 578, came 578. Second round: the package variables with an address
      handed on, predicted 0 to 3 findings back on production and fewer than 20 moved in the
      standard library, came 0 and 0; the builtins, predicted 38 to 50 SAFE008 rows gone and at
      most 5 new, came 51 gone and none new, 7 of them not predicted (below).
    - **Reference service, production code**: 2,037 findings before and 1,958 after; 83 gone, 4
      new, 6 reworded; 431 of 578 transfers flagged, one fewer. SAFE008 420 to 344. Two runs
      with one binary give the same rows.
      - 51 SAFE008 gone for the builtins, all `len`. A script names the builtin at the reported
        column of 44. The other 7 are at the line of the registration itself, where a builtin
        that has no position of its own is reported; a build that prints such a builtin names
        `len` for each, the length the compiler takes of a slice a function the handler calls
        ranges over (`buildCommitteePapers`, `keepSourcesAdmittedByGate` and two more).
      - 24 SAFE008 gone where a handler calls a function variable declared beside it
        (`requireQuestioningStateStore`, `writeQuestionOptionResponse`,
        `patchDynamicQuestionOptions` and three more in `questioning_routes.go`, `run` in
        `wisdev_maintenance_routes.go`). The six bodies in `questioning_routes.go` were read:
        none writes anything the handlers share. `run` is a function literal that captures
        nothing and hands its arguments to a method.
      - 1 SAFE008 gone at `mux.Handler(probe)` in `route_not_found.go:95`, a lookup. The
        summary of `(*net/http.ServeMux).Handler` said it keeps and writes; it now says keeps.
        Of the 92,131 function summaries the run computes, 111 changed (compared at `ba9c7f4`):
        110 lost a bit and one gained one (`ai.typedConfigFn$1` in genkit, not read).
      - 1 SAFE008 moved: the call of the package variable `runUnifiedResearchLoop` was reported
        at the call and is now reported inside it, at `runtime.RunLoop`
        (`research_routes.go:118`).
      - 3 SAFE006 gone: package variables that hold a function or a struct of functions
        (`claimEmbedBatch`, `claimEmbeddingBackfillTarget`, `findingEmbeddingBackfillTarget`)
        were "written outside init" at a line that passes them to a function which calls them.
        No assignment to any of the three exists outside its declaration in production code.
      - 2 SAFE004 gone, both read. `paper_fulltext_resolve.go:496` calls the package variable
        `fullTextCheckRetractions`, which is `search.CheckRetractionsBatch`: it copies the DOIs
        it is handed. `manuscript_pipeline.go:2552` calls the closure
        `liftAuthorVisualsFromSections` returns: both closures it may be rewrite the slice in
        place and return it, and neither keeps it.
      - 1 SAFE002 gone, read: `server.go:659` registers the result of the package variable
        `metricsHandlerFn`, which is `telemetry.MetricsHandler` and returns a new handler.
      - 3 new, all on transfers that were flagged already, none a race on reading: a call that
        used to "escape" its arguments now joins them. `questioning_routes.go:1894` writes the
        response from options stored into a session of which a shallow copy went to a
        background task (both sides only read). `tree_search_runtime.go:262` is after
        `wg.Wait()` on a `SafeGoWG` group, the join the checker does not recognise.
        `router.go:487` says the handler's mux may alias what `otelhttp.NewHandler` returns;
        the middleware chain at line 499 is called through values and now joins the mux to its
        result (the reason was not traced further than that).
      - Of the 34 rows that still say "a dynamic function call", 20 are a handler calling a
        closure made by the function that registered it, where every target is known and the
        closure's summary says it writes something it captured
        (`buildDynamicQuestionOptionsOnce`, `persistDynamicQuestionOptions`). The rest are at
        calls whose targets are not all known, or name such a call in the reason.
    - **Reference service with tests**: 7,713 to 7,645; 81 gone (76 SAFE008), 13 new, 8
      reworded; 3,106 of 4,805 transfers flagged, one fewer. Of the new rows 3 are the
      production ones; 8 are SAFE004 in tests on transfers that were flagged already, where a
      handler built by calling the closure `ResilienceMiddleware` or `otelhttp.NewHandler`
      returns is now joined to the handler it wraps (one read); 2 in
      `sse_write_deadline_test.go` were not read. Annotated copy: 694 to 690.
    - **Standard library with tests**: 13,066 to 13,071; 5 gone, 10 new, 2 reworded; transfers
      4,415 to 4,423 and flagged 2,412 to 2,418, because a goroutine started by a function
      reached through a value is now a transfer.
      - 5 new SAFE003 in `database/sql/closemu_test.go`: `start(t, m.Lock)`, where `start` is a
        local closure that runs its argument on a goroutine, and `m` is used after. That is the
        shape the change was made for; `m` is a mutex of the package's own, so there is no
        race.
      - 2 new in `crypto/rand/rand_test.go`: `Reader.Read` handed to `testConcurrentRead`
        through a parameter with five known targets.
      - 3 new SAFE006 that are false: the test tables `upperTests`, `lowerTests` and
        `trimSpaceTests` in `bytes_test.go`. `runStringTests` calls its parameter `f`; the
        summary of `ToUpper` says it writes its argument (its body only reads it), and the
        result is now joined to the argument as it is for a call by name, so the write reaches
        the table through the arguments of `t.Errorf`. A fixture with the same loop written
        both ways reports the table for the call by name too.
      - 4 SAFE006 gone (`cipherSuites`, two OIDs in `crypto/x509`, `NoStack`); three read: each
        was handed to a function variable or parameter whose targets only read it. 1 SAFE008
        gone for a builtin (`net/http/server_test.go:139`).
    - **Race corpus**: 229 of 234 race tests flagged and 37 of 129 race-free tests clean, 43
      clean in the annotated copy: as before.
    - **Against `main` as it moved.** `3322a22` against `b2f480c`: the reference service's
      production code and annotated copy are the same row for row; the standard library with
      tests has 14 more SAFE002 rows (13,080), the calls `fde27e1` reports on purpose (12 in
      `os/exec/dot_test.go`, `net/cgo_unix.go:68`, `unique/canonmap_test.go:83`); the service
      with tests has 6 more (7,719). `3f0c738` against `3322a22`: production 83 gone, 4 new, 6
      reworded (the same rows as above); with tests 87 gone, 13 new, 9 reworded; annotated 4
      gone; standard library with tests 19 gone, 10 new, 2 reworded, the 19 being those 14 and
      the 5 above. Race corpus the same at both.
    - **Against `main` as it moved again.** `main` went on to `2ef069c` (a call of a variable
      whose walk was cut short releases every lock when it is started as a goroutine or made in a
      helper, too) and was merged into the branch a second time (`1b5b79a`, no conflict). On
      `main`, `2ef069c` against `3322a22`: production (2,037 rows), the annotated copy (694) and
      the race corpus are the same row for row; the two corpora with tests were not run there.
      The merge against `3f0c738`: all five are the same row for row (production 1,958, annotated
      690 with 426 of 578 transfers flagged and 343 hatched, the service with tests 7,645, the
      standard library with tests 13,071, race corpus 229 of 234 and 37 of 129, 43 annotated).
      So `2ef069c` moves no corpus row on either base: what it restores is held by its own test
      (`TestWalkCutShortLosesWhatIsHeld`), not by code in these corpora. The first run of the
      standard library on the merge was killed from outside at 158 s holding 20 GB, with the
      machine short of memory; the figure is from a second run that ran to the end.
    - **The walk's complete answers against the oracle** (made at `ba9c7f4`; the rules added
      after it only turn "complete" into "may not be all"). Reference service: at every call
      through a value, with memory handed over or not, where the walk says it has every target
      and the graph has any, the two
      sets are equal (492, and 4 more where the count is equal and the list the counter prints
      is capped), except one, where the walk has 2 of the graph's 5: `stage.newEvent` in
      `recordRefusedRewrite`, which two callers reach with two of five stage values. The walk
      follows the parameter to its callers; the graph goes by the field's type. Standard
      library with tests: 2,490 equal, 31 equal with the list capped, 10 where the graph has
      more. 9 of the 10 are closures of packages the caller cannot reach (`net/http_test`
      closures at a call in `reflect`; one read). The tenth is `mustPanic` in
      `crypto/internal/cryptotest`, 22 targets against 23: the missing one is passed at a call
      after `t.Skip`. The intermediate form the checker is given ends a block at a call that
      cannot return (`buildssa` hands go/ssa the `NoReturn` answers, x/tools v0.52.0), and the
      count has no call of the checker's on the lines after that `t.Skip`; the oracle's form
      was built without it. Before the address rule there were 13, the other 3 being the two
      hooks above. At 16 more sites the walk is complete in the build of a package without its
      tests and not in the build with them.
    - **Tests.** `call_through_value_test.go` and `open_callers_internal_test.go`. Each test
      named in a commit as proving it was run at the commit before and fails there. The cases
      added after the take-out pass (below) hold a part in place and were not run against the
      commit before. `go vet ./...` and `go test ./... -count=1 -timeout 60m` without cgo at
      `3f0c738`, with nothing else of this run alongside: 17 packages, all `ok`; the same at
      `5f13bac` before the merge.
      At `b5f6c76`, with the corpus runs alongside and a load average near 50,
      `TestWitnesses/lk_wit_maplookup` failed once: "the runtime did not convict this program
      of a race". Alone it failed 2 more times in about 200 runs there and 0 times in 120 at
      `b2f480c`. The harness runs the witness with `go run -race` and reads its output (read:
      `runWitness`); the checker has no part in it, and the program and the harness are the
      same files at both commits. It is a witness the race detector convicts most of the time
      and not every time.
      Not run at this commit: the tests with cgo, and `cargo test`; no Rust file and no cgo file
      changed since `dcbc28b`. Not measured: time. The corpus runs took 17 to 100 s of wall
      time each at loads that differed between runs.
    - **Each part taken out in turn** (45 parts over the seven commits, by a script that edits one
      line and runs the tests above with `TestStress` and `TestWitnesses`): a test fails for 39,
      and without the guard that keeps the reading of wrappers to the ones the compiler writes
      the tests did not finish in ten minutes. A wrapper that calls another wrapper was
      followed at first; no test needed it and it was removed.
      The first round left 8 parts that no test noticed or that the script could not apply;
      tests were added for 3 (an argument written by a target, a closure that only reads what
      it captured, a published argument a target also keeps). No test tells these 5 from their
      removal: the write of what a closure handed to an owning or a starting target
      captured (two lines, the same as for a call by name), the contract that an atomic store
      is not a write, the guard that a closure's captures are followed only where the closure
      was made in the handler's own frame, and the refusal of a complete answer that holds
      something other than a function (no complete answer on either corpus holds one).

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
  (`sp_w_registry`) and unlisted callbacks (sections C and D) remain unmodelled. A method value of
  a method whose body is in sight is the method since 2026-11-12; one of an interface method is not.
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
`seed_publish`, `sp_w_syncmap`, `sp_w_ctxvalue`, `mv_w_ctxvalue`, `rs_send`, `sp_w_methodvalue`.
Twenty-five witness programs carry no `MISS` today: the runtime convicts them and the checker
reports them.

## Not covered

- Left open by the changes of 2026-11-12 to the six gaps that stood here (section 5):
  - A method called through a value. The lock check still takes a method expression
    (`(*R).m`) for a function of another package, so anything it is handed may be unlocked, and
    a method value's summary carries no locks taken. A link between the receiver and a
    parameter or result is kept only for a method expression whose first parameter is the
    receiver as it stands; for a method value, a promoted method and a value receiver, what the
    link joined is taken the safe way (the parameter leaks, the result is not fresh, the
    receiver is retained), which errs toward reporting. A method value of an interface method
    is as before (further down).
  - What stands in for memory whose address is out of sight. Where a pointer leads nowhere in
    sight (`fieldBehind`) and where a struct value leads nowhere (`fieldByType`), every store
    to the field by type still stands in, with no allocation to compare against. Nothing
    stands in for a variable that holds something other than a function. The functions a
    called value may be for the lock check (`callTargets`) take no stand-in. A package with
    more than 96 stores of one function type through pointers gets "could not be followed to
    the end" at each call of such a variable (section 5).
  - An array of functions that is a field of a struct (`s.fs[1](x)`) is not followed (seen
    with a probe; no test), and `callTargets` does not include the elements of an array, so
    for the lock check such a call may run anything.
  - A closure that publishes a variable it captured, when it is handed to a function that
    calls it, or is one of several closures a variable may hold: a use after the call, in the
    function that owns the variable, is not a use after publish
    (`TestUseAfterAClosureCalledOutOfSightIsAKnownMiss`).
  - A function made elsewhere and called here: only what it unlocks is brought to the function
    being checked. What it locks is not counted as held afterwards, and a value it runs on
    that cannot be brought (a parameter of a closure that was not entered with the address,
    something loaded from a package variable) releases everything. Both err toward reporting.
  - Seen while the tests were written, older than these changes and not changed: where every
    function a field's stores lead to has been visited already on the walk, the answer for
    the field is empty, and an empty answer is read as "the pointer led nowhere", so every
    store to the field by type stands in. Two tests had to give a struct a function of its
    own to see past it. Errs toward reporting.
- Left open by the changes of 2026-11-12 to a call through a function value (section 5, `eb228af`
  to `3f0c738`):
  - An interface call whose receiver types are all known is still treated as keeping and writing
    everything. The walk has every type at 5 of the 805 interface calls that hand over memory on
    the reference service; the whole-program graph resolves 711, and at 46 of those every
    argument is only read by every target.
  - The lock check still asks the smaller resolver (`callTargets`), which has every target at 25
    of the 230 calls where the walk does.
  - A use after a move made inside a helper that only forwards its parameters to an owning
    target is not reported at the helper's caller (`mv_iso/iso.go:64`, a known miss as before).
  - A handler that calls a closure made by the function that registered it, where the closure's
    summary says it writes something it captured, is reported as "passed to a dynamic function
    call, which may write it" although the target is known, and the closure is not followed
    into. 20 rows on the reference service's production code.
  - A deferred or started builtin in a handler's reach is reported as a call of something
    unknown, as before: the write rule (`writeOf`) reads only plain calls.
  - A package variable whose address is used for anything but reading or writing through it is
    "may not be all" wherever the address goes, also when it never leaves the package.
  - Joining what the targets join carries a coarse summary further: a target said to write its
    argument now writes what its result is joined to, as for a call by name. Three false
    SAFE006 rows on test tables in the standard library's `bytes_test.go` came of it.
  - Where an argument published through a known target also escapes, the reason still reads
    "passed to a dynamic function call" and does not name the target.
  - Parts with no test that tells them from their removal: listed at the end of the entry in
    section 5.
  - `fde27e1`'s guard stays: a contract worked out for a call of a variable is not "fresh" by
    the targets' summaries alone (`evalFunctionOutcome`). The result of a call whose targets
    are all known is read from their summaries in `callOfKnown` instead.
  - A channel is "may not be all" as soon as it is handed to any call, also one whose body is
    in sight and sends nothing.
  - `TestWitnesses/lk_wit_maplookup` is not convicted by the race detector on every run (3
    misses in about 200 runs under load).
- Found on 2026-11-11 after the merge and not fixed:
  - A read of an argument after it is published, inside a helper. Written out, any use after
    the publish is SAFE003. A helper's summary says what it keeps, not in which order it does
    things, so `pubAndCall(p.Store, readOnly, x)` is a publish and nothing more
    (`TestReadAfterPublishInsideAHelperIsAKnownMiss`).
  - A write through memory loaded from a shared container, made in a closure that captures the
    loaded value, is not reported, also when the closure is called on the next line
    (`TestWriteOfLoadedMemoryThroughACaptureIsAKnownMiss`).
  - The handler write check has the same gap as the shared-load check above: a handler's
    closure that captures the shared value and writes it is not followed
    (`TestHandlerWriteThroughACaptureIsAKnownMiss`).
  - The goroutine check transfers one of the closures a started variable may hold. Both
    closures' captures are reported all the same, because the values a variable may hold share
    one class (`TestGoOfVariableThatMayBeTwoClosures`); that is the only thing it leans on.
  - `callTargets` takes the stores to a variable's cell together, whatever their order, so a
    variable that held a locking closure and was reassigned before the call counts as "one of
    two". That errs toward reporting.
  - A `//gosree:holds` helper that unlocks its mutex inside a closure is not reported: the
    closure sees the receiver through the cell it captures and the helper's mutexes are kept
    as paths from its parameter (`TestLockHoldsHelperUnlockingInAClosureIsAKnownMiss`).
  - Whether a package takes any lock that other packages could be told about (`hasLockOps`)
    now counts a lock taken by any function a call may run. It decides only whether those
    facts are worked out, and has no test of its own.
  - The handler write check words a function read from the handler and called as "is passed to
    a dynamic function call". The finding is meant; the wording is that of an argument.
  - The eleven walks that follow a value backwards are one walk since 2026-11-12 (section 5).
    Two things in it are as the separate walks had them and were not decided: a field of a
    struct value and what is stored at an address are asked without marking what is visited
    and without spending budget, and the second is not stopped by depth. No case was found
    that this gets wrong; the depth limit ends the one cycle that results.
  - What is done besides a publish is judged by what a callee keeps. A receiver's method that
    has no publishing answer counts as keeping the argument without being looked into, and a
    method value used as the forwarded callable counts too; either can report a helper that is
    harmless. No such report was seen in the corpora. (The second was not looked at again after a
    method value became the method on 2026-11-12.)
  - The 14 new standard-library findings above were not triaged beyond reading the helpers.

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
  undercount: 20 were fixed and then six more in `contracts.go`, section 5, and these are left).
  This list was made by reading the code on one day. Since the merge of 2026-11-11 a test reads
  the source instead: `TestEveryBoundOnAWalkIsListed` (`passes/ownership/walk_answers_test.go`)
  finds every fixed number a walk or a fixpoint is compared with and fails unless each is listed
  there with what the code does when it is reached, and fails for a listed one that is gone. It
  covers the ownership pass only:
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
    arguments, so nesting stops at one. A counter saw no depth above 1 in any run. Should it ever
    be reached, it now marks the question as cut, like a trace out of budget, where it used to
    answer "no contract" and nothing else (`TestCallableNestedPastTheLimitIsACut`).
  - `contracts.go`, one committed: the 16-step scan at the top of `sharedLoadRoot`. For guarded
    fields the `slotOf` check before it gives the same answer, and no program was found in which
    the scan's limit changes a finding, so it has no test and was not changed. A counter saw it
    reached in no run.
  - `lockset.go`: `maxAcquireRounds` (16) and the release-summary widening (`maxReleaseSteps`,
    `maxReleasesPerFn`). Both err toward reporting, an acquisition not credited or a release
    assumed, and are described in `docs/RULES.md`.
  - `localguard.go`: the 32 rounds carrying a guarded local's cell into closures are gone. The
    limit was not reachable (a function is scanned before its closures, so one round carries a
    cell to any depth) and the rounds end by themselves (`TestGuardedLocalInDeepClosure` pins 40,
    before and after).
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
- A table that an unexported helper stores into memory its caller gave it
  (`func put(h *H) { h.m = table }`), where the caller then hands that memory out, is still taken
  for init-only: in the helper the table only joins a parameter's memory, which the search for
  escapes does not count, and in the caller the table does not appear. Publishing it frozen is
  then not reported. The same helper exported is reported. Pinned as a known miss in
  `TestTableStoredIntoCallerMemoryIsAKnownMiss`; `TestAdv2DirectStoreToOtherGlobalPreExisting`
  pins two more shapes (assigned to another package variable inside a function; stored by a
  helper into a slice that is then printed).
- `resolveClosureVal` names one closure where a value may be one of several: the first edge of a
  phi that leads to a closure, the last store to a variable's cell. The borrow check at a `go`
  statement asked it and so missed what the other closures borrow; that one now asks for every
  candidate (`TestGoOfVariableBoundToTwoClosuresReportsEachBorrow`). Its other call sites
  (`lockset.go` 5, `handlerwrite.go` 2, `graph.go` 1, `contracts.go` 1, `check.go` 1) were not
  examined for whether one closure may stand for all.
- The 38 findings the boxed-value change adds on the standard library's tests (section 5, the
  merge of 2026-11-11) were not read one by one.
- With its tests, the reference service cannot be analysed by the product as built: the error
  pass refuses a package of more than 16,384 functions, and `internal/wisdev` with its tests has
  18,973 (task ft-bc5b on the board). The with-tests figures in section 5 after the merge come
  from a build with only that limit raised, made for measuring.
- One de-duplication key still goes by line where there may be none: `checkGuardedFunc`
  (SAFE009) merges accesses to one variable that have no position within a function. They are
  reported, once, at the package clause. No input that produces one was found.
- A `return` go/ssa adds without a position (the recover block's) is not checked by the errcheck
  pass (`lower.go`, lowered as a stop). Whether that hides a finding was not looked into.
- Scale shapes for the handler walk and for the borrow pass beyond local borrows.
- The reference-service hatch ratio and the race corpus were not re-measured against any of this.
