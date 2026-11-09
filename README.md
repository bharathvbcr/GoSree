# gocap

Ownership checking for ordinary Go. gocap is a set of `go/analysis` passes that bring a Rust-style
"one owner at a time" rule to the places where Go code actually shares memory between goroutines. The
code stays plain `.go`: annotations are comments, so gofmt, gopls, vet and `go test` see normal Go.

```bash
go build -o build/gocap ./cmd/gocap
```

```bash
go vet -vettool=/absolute/path/to/build/gocap ./...
```

## Why only goroutine boundaries

In a garbage-collected Go program, use-after-free and out-of-bounds access cannot happen. The memory
bug that remains is the **data race**: a torn write to a two-word interface or slice header can pair a
type with the wrong pointer. So gocap checks ownership only where a value crosses to another
goroutine: a `go` statement (its arguments and the closure it starts), a callback handed to a
function that runs it on another goroutine (`time.AfterFunc`, `context.AfterFunc`,
`sync.WaitGroup.Go`, `errgroup.Group.Go`/`TryGo`, and `net/http` handler registration), a channel
send, and an argument for a `//safe:iso` parameter. A closure's captured variables cross only when
the closure itself does. Inside one goroutine, aliasing is left alone.

The callback functions are a curated table in `passes/ownership/spawn.go`, an assumption about each
callee like the type overrides. A function that runs its callback before returning (`sync.Once.Do`,
`testing.T.Run` without `Parallel`, `slices.SortFunc`) is an ordinary call. An HTTP handler runs on
every request's goroutine, concurrently with itself, so it never owns what it captured (moving a
capture on is SAFE002) and never hands anything back.

## Rules

| ID | Rule |
|---|---|
| SAFE001 | `unsafe.Pointer`/`Add`/`Slice`/`String*` (however `unsafe` is imported, dot imports included), cgo, `//go:linkname`, functions declared without a Go body (assembly), `reflect.Value.Set*`/`UnsafeAddr`/`UnsafePointer`, `reflect.NewAt` |
| SAFE002 | a value crossing to another goroutine is not owned: it may alias a parameter, a package variable, or a result the callee may still reference |
| SAFE003 | a value is used after it was moved; `SAFE003-forkjoin` marks uses reached only after a channel receive, `select`, `WaitGroup.Wait` or `Lock` that is not a [join](#fork-join) for that move |
| SAFE004 | a moved value also escapes elsewhere (stored in a package variable, kept by a callee, returned) |
| SAFE005 | a `//safe:imm` type is malformed, or is written after its composite literal |
| SAFE006 | a package variable written outside `init` is accessed by code that runs on a goroutine |
| SAFE007 | a malformed `//safe:` directive |

## Annotations

```go
//safe:iso j            // on a func: j is owned; passing an argument to it moves the argument
func forward(ch chan *Job, j *Job) { ch <- j }

//safe:imm              // on a struct: deeply immutable, so it may be shared freely
type Config struct{ name string; port int }

//safe:unsafe <reason>  // exempts the next statement, or the whole function from its doc comment
```

A `//safe:unsafe` on a transfer (a `go` statement, a send, a call to a `//safe:iso` parameter) covers
everything that transfer causes: the SAFE002 and SAFE004 at the transfer, and the SAFE003 uses after
it, wherever they are. One placed on a later use covers only that use.

An owned value is one that starts as a fresh allocation (`&T{}`, `new`, `make`), a value received
from a channel, a `//safe:iso` parameter, or the result of a function whose summary says it returns
fresh memory. It stays owned while nothing outside its own object graph points into it.

Shareable without ownership: pointer-free values, strings, channels, verified imm types and pointers
to them, and a curated override table (`sync` and `sync/atomic` types, `*testing.T`,
`context.Context`, `error`, `time.Time`, `*regexp.Regexp`, `*log.Logger`, `*slog.Logger`, ...). Each
override is an **assumption, not a proof**; see `internal/kinds/kinds.go`. The `error` entry
is the weakest one: it rests on the convention that errors are not mutated after creation.

## How it works

- **Alias analysis:** per-function Steensgaard unification over SSA (`golang.org/x/tools/go/ssa`).
  It is flow-insensitive and field-insensitive on purpose: it reports too much and never too little.
  Only the move-kill (a use after a move unless the value is redefined first) is flow-sensitive.
- **Summaries:** each function gets a summary saying which arguments it may retain or share, and
  whether it returns fresh memory. Summaries are computed to a greatest fixpoint within a package and
  exported as analysis facts for exported functions, so they cross package boundaries. That includes
  the standard library: `(*bytes.Buffer).WriteString` is shown not to retain its receiver. Functions
  without bodies, interface calls, dynamic calls and wrappers are treated as retaining everything.
- **Change from the design advice:** the original plan took standard-library escape information from
  the compiler (`go build -gcflags=-m`). The MVP uses the same summary facts for every package
  instead, which gives one mechanism and no parsing of compiler output. The compiler's escape
  analysis remains a good differential test of these summaries; that test is not written yet.

The only dependency is `golang.org/x/tools` (plus its own `x/mod` and `x/sync`).

## Fork-join

A goroutine can hand back what it was given. A move made by `go F(...)` ends at a **join**: a
parent instruction after which `F` provably no longer touches the moved memory. A use the parent
reaches only through joins is not a use after the move (`passes/ownership/forkjoin.go`).

```go
x := 0
var wg sync.WaitGroup
wg.Add(1)
go func() { x = 1; wg.Done() }()
wg.Wait()
use(x) // accepted: the goroutine joined at Wait
```

A join needs all of these:

- **The goroutine is started once.** `F` is a function literal whose closure value only that `go`
  statement uses, and the `go` statement is not inside a loop.
- **The release follows the last use.** No instruction of `F` that touches the moved memory can run
  after any of `F`'s releases. Deferred calls count as running at exit: after every plain release,
  and after a deferred release when they were registered before it (defers run last-in, first-out).
  "Touches" uses `F`'s own alias graph, so a closure over the memory built before the release and
  called after it counts.
- **Nothing else keeps it.** `F` does not move the memory on (no `go`, send or `//safe:iso`
  argument) and does not let it escape. Escape comes from the callee summaries, so this is only as
  precise as they are. A callback over the memory handed to `time.AfterFunc` is itself a move, and
  one handed to `runtime.SetFinalizer` is caught because its summary marks it as retained (a case
  covers `AfterFunc`; `TestRaceFin` in the corpus covers `SetFinalizer`).
- **The sync object is local and fully visible.** It is one of:
  - **A channel the parent makes**, captured by `F`, whose only sends and closes anywhere are in `F`
    (a send, a `select` send case, `close`, `defer close`). Everything else may only receive from it
    or take `len`/`cap`. The join is a plain receive in the parent: `<-c`, `v, ok := <-c` or the exit
    of `for range c`. By the Go memory model, a send or close is synchronised before the receive that
    observes it, and `F` is the only sender.
  - **A `sync.WaitGroup` local to the parent**, used only through `Add`, `Done` and `Wait`. Every
    `Add` is in the parent, adds a positive constant, is not in a loop, and dominates every `go`
    statement whose goroutine calls `Done`. Each such goroutine is started once and calls `Done` at
    most once on any path. The `Add`s sum to the number of those goroutines. The join is `Wait` in
    the parent. Under those conditions `Wait` returns only after every `Done`, `F`'s included.
  - **The receiver of `WaitGroup.Go` or `errgroup.Group.Go`/`TryGo`** that started `F`. The join is
    `Wait` on that same value in the parent, since `Wait` returns only after every function started
    on it has returned. No release check is needed (`F` returns after its last touch), and the join
    holds in a loop too, since one `Wait` covers every function started before it.

`F` is started by a `go` statement or by a one-shot callback function (`time.AfterFunc`,
`WaitGroup.Go` and the others in `spawn.go`). An HTTP handler never joins.

Accepted shapes (each has an `analysistest` case in `testdata/src/moves/forkjoin.go`): `Done` after
the last write, `defer wg.Done()`, one `Add(2)` covering two goroutines, send handshake, `close` with a
comma-ok receive, `for range c`, `x = <-c` with the goroutine doing `c <- x`, a `select` send
as the release, and a pointer passed as a `go` argument rather than captured. In
`testdata/src/callbacks/callbacks.go`: `Wait` after `WaitGroup.Go` and after `errgroup.Group.Go`, and a
`time.AfterFunc` callback that closes a channel the parent receives from. Rejected there: `Wait` on a
different WaitGroup, and a callback that keeps the memory in a package variable.

Still reported as `SAFE003-forkjoin`. Every item has a case, except `sync.Cond` and channels or
WaitGroups held in struct fields:

- `Done` before the goroutine's last write, or a write in a defer registered before `defer wg.Done()`
- `Wait` on a different WaitGroup, `Add` inside the goroutine, `Add` totals that do not match the
  number of `Done` goroutines, and WaitGroup reuse across several `Add`/`Wait` rounds
- several goroutines sending on one channel, or a parent that also sends on it
- a channel that is a parameter or is passed to a function. Fields, conversions and any other use
  of the channel or WaitGroup are rejected the same way: the rule accepts only the operations
  listed above.
- a goroutine that moves the memory on, calls a closure over it after its release, or hands such a
  closure to a callback API (`time.AfterFunc`)
- `go` statements and callbacks in a loop, except for the `Wait` join above, and closure values
  started twice (these are also plain `SAFE003`)
- `select` receive cases as joins, mutex and `sync.Cond` hand-offs, and `go f(x)` with a named
  function

Two assumptions remain. The memory-model facts above are taken from the Go specification, not
re-derived. And a deferred use registered *before* a move is still reported even when every path
joins, because a panic between the move and the join would run it early.

## Measured on the Go race detector's corpus

`go run ./cmd/gocap-corpus -out build` copies `$GOROOT/src/runtime/race/testdata` into `build/` (never
committed), runs gocap unannotated, and writes `build/corpus-report.json`. A test function counts as
flagged if gocap reports anything in it or in a same-package function it calls.

go1.27.3, 22 of 24 files. Skipped: `cgo_test.go` imports `internal/testenv` and `synctest_test.go`
imports `internal/synctest`; neither can build outside GOROOT.

| | With callbacks | Predicted before that run | With fork-join | Predicted before that run | MVP (`b42b26c`) | Predicted before the MVP run |
|---|---|---|---|---|---|---|
| `TestRace*` flagged | 229 / 234 (97.9%) | 229 | 228 / 234 (97.4%) | 228, unchanged | 228 / 234 (97.4%) | at least 85% |
| `TestNoRace*` clean | 38 / 129 (29.5%) | 38 | 40 / 129 (31.0%) | 40 | 30 / 129 (23.3%) | 15 to 40% |
| `TestNoRace*` flagged only for fork-join | 7 | 7 | 7 | 7 | 17 | expected to dominate the misses; **it did not** |

The callbacks run moved exactly the three predicted tests. `TestRaceAfterFunc3` is now flagged: two
`time.AfterFunc` callbacks capture one variable. Two `TestNoRace*` tests became flagged.
`TestNoRaceAfterFuncReset` writes after `Timer.Stop`, which gocap does not model: in general `Stop`
returning false means the callback already started. `TestNoRaceAfterFunc1` is a closure stored in a
variable that respawns itself, so it does not own its own captured variable (SAFE002). It is not a
goroutine body by the rule above, since its closure value reaches the spawn through a variable.
`TestNoRaceAfterFunc` stayed clean: its callback hands the memory back through a channel it alone
sends on, which is the [fork-join](#fork-join) rule applied to a callback.

The fork-join run cleared exactly the ten predicted tests: `TestNoRaceChanSync`, `ChanAsync`,
`ChanAsyncCloseRecv2`/`3`, `ChanSyncCloseRecv2`/`3`, `ChanReadWriteAsync`, `Select3`, `WaitGroup2`
and `WaitGroupTransitive`. No `TestRace*` test became unflagged. The nine `TestRace*` tests flagged
only for fork-join stay flagged, each for the reason the rule gives. For example,
`TestRaceChanWrongSend` has two senders on one channel, and `TestRaceChanReadWriteAsync` receives
from a channel the parent itself sent on. `SAFE003-forkjoin` inside `TestRace*` fell from 17
to 11 because of `TestRaceBlockCall1`-`6`. There the reads after `<-done` are race-free and are no
longer reported, while the racing read before `<-done` is still plain `SAFE003`. The seven remaining
fork-join-only `TestNoRace*` tests use `select` receives, mutexes, `sync.Cond`, or two goroutines
taking turns on one channel (`TestNoRaceIntRWClosures`).

Read these numbers with care:

- **Weak discrimination.** gocap flags nearly every test that shares a mutable captured variable,
  whether or not the sharing is synchronised. Rust's borrow checker would reject most of these
  `TestNoRace*` programs too, until they were rewritten with `Mutex` or `Arc`. That makes the 97%
  catch rate partly a consequence of how strict the rule is, not of precision.
- **"Flagged" means a diagnostic in the test or its callees**, not a check that the diagnostic
  points at the racing access.
- **What makes `TestNoRace*` tests fail, by sampling** (not an exhaustive classification):
  - atomics on plain variables (`TestNoRaceAtomic*`)
  - mutex-guarded sharing (`TestNoRaceMutex`, `TestNoRaceRWMutex*`)
  - different elements or fields of one object, which a field- and index-insensitive analysis
    cannot tell apart (`TestNoRaceSlice*`, `TestNoRaceStructField*`)
  - branches that never run (`TestNoRaceAnd`)
  - fork-join shapes outside the [join rule](#fork-join) (`select` receives, mutex and cond hand-offs)

**Five missed races**, each an unmodelled class:
- send-or-close races on a channel: `TestRaceChanCloseSend`, `TestRaceChanSendClose`,
  `TestRaceChanSendSelectClose`
- reassigning a mutex variable: `TestRaceMutexOverwrite`
- WaitGroup misuse: `TestRaceWaitGroupWrongAdd`

## Measuring the escape hatch on a module

```bash
go run ./cmd/gocap-corpus -module ~/src/service [-tests] [packages]
```

This analyses any module (patterns default to `./...`) and prints, per package and in total, the
transfers gocap checked, how many needed `//safe:unsafe` (a transfer counts once, and only if the
directive actually muted a finding), the findings muted by any rule, and the findings that remain.
The JSON goes to `build/module-report.json`. With `-tests`, each package is counted once through its
test variant, so no file is counted twice. It reads `ownership.Stats`, which the analyzer returns for
every package, so a driver never parses diagnostic text.

The hatch ratio, suppressed transfers over transfers, is the measurand of the abandon criterion
under [Next](#next). Callbacks count as transfers, so a `net/http` service's handlers are in the
denominator. Goroutines started by a library function missing from the spawner table are not.

## Known limits

- **Fork-join covers channel and WaitGroup joins only.** The [shapes](#fork-join) outside it keep
  what they were given for good.
- **Lock discipline is not modelled.** Mutex-guarded sharing needs `//safe:unsafe`.
- **Only listed callback functions are goroutine boundaries.** A library function that runs its
  argument on another goroutine but is not in `spawn.go` (a worker pool, a scheduler) is an ordinary
  call to gocap. Its callback is checked only through the callee's summary, which treats it as
  retained.
- **A handler racing with itself is not checked.** A handler that writes memory it captured races
  with the same handler on the next request. gocap reports that only when the handler moves the
  memory on (SAFE002) or the memory is a package variable (SAFE006), not for a plain write. Whether
  to forbid writes to captured state in handlers is an open design question.
- **`Timer.Stop` and `context.AfterFunc`'s stop function are not modelled.** Stopping a callback does
  not hand its memory back, which is right when `Stop` returns false and over-reports when it
  returns true.
- **SAFE006 is in-package only.** It follows static calls only, and misses globals mutated through
  methods.
- **Imm types are minimal.** Only struct types with unexported, shareable fields qualify: no slices
  or maps. Shared imm values must be built with `&T{...}`.
- **Trailing directives attach forward.** A `//safe:unsafe` trailing the closing line of a
  multi-line statement attaches to the next statement; put it on the line before.
- **Iterators lose ownership.** Range-over-func loops keep ownership only when the iterator function
  is statically known. An iterator obtained as a func value (for example `slices.All(s)`) is treated
  as retaining what its loop body captures.

## Next

This README is the design of record: gocap checks memory where it crosses goroutines, and leaves
aliasing within one goroutine alone. In build order:

Done: callback functions as goroutine boundaries, and SAFE001 for dot imports of `unsafe` and
functions without a Go body.

1. A function override table beside the type table, and summaries that record a call linking two
   arguments (`root.Add(child)`) instead of reporting an escape.
2. The hatch ratio measured on the reference service: ScholarLM's `backend/go_orchestrator`, pinned
   at ScholarLM commit `bb60b53`, measured with `-tests` on go1.27.3. Untriaged baselines, both
   with 0 `//safe:unsafe`:

   | | Transfers | SAFE001 | SAFE002 | SAFE003 | SAFE003-forkjoin | SAFE004 | SAFE006 | Findings |
   |---|---|---|---|---|---|---|---|---|
   | With callbacks | 1,642 | 104 | 784 | 685 | 202 | 347 | 130 | 2,252 |
   | Before callbacks (`538bbe2`) | 1,258 | 104 | 511 | 458 | 193 | 263 | 123 | 1,652 |

   SAFE001 is mostly generated protobuf code and tests. The 384 new transfers are mostly HTTP
   handlers: `internal/api` went from 365 to 633. The ratio is read against the callbacks
   denominator, after item 1, by adding overrides until every remaining finding is a real race or
   a policy call.
3. A guarded-by rule, `//safe:guarded_by(mu)`, with SAFE006 checked where the package variable is
   declared.
4. Ownership of the channel's send end; joins through `select`; a differential test of the summaries
   against `-gcflags=-m`.

The project should be abandoned if a `net/http` reference service has a hatch ratio above 20% (see
[Measuring the escape hatch](#measuring-the-escape-hatch-on-a-module)) once the type and function
override tables together hold 100 entries.

## Tests

```bash
go test ./...
```

Each rule has `analysistest` cases with `// want` expectations, so a disabled rule fails its test.
`passes/ownership/testdata/src/user` checks that summaries cross package boundaries, including from
the standard library. `passes/ownership/testdata/src/supp` checks the reach of `//safe:unsafe` and
the exact `Stats` counts. `passes/ownership/testdata/src/callbacks` covers each callback function,
the `Wait` join, handlers, and `sync.Once.Do` as a non-spawn; `errgroup` there is a stub under
`testdata/src/golang.org/x/sync`, so `x/sync` stays out of the module's direct dependencies.
