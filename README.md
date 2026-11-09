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
capture on is SAFE002), never hands anything back, and must not write memory it shares with its
other invocations (SAFE008).

## Rules

| ID | Rule |
|---|---|
| SAFE001 | `unsafe.Pointer`/`Add`/`Slice`/`String*` (however `unsafe` is imported, dot imports included), cgo, `//go:linkname`, functions declared without a Go body (assembly), `reflect.Value.Set*`/`UnsafeAddr`/`UnsafePointer`, `reflect.NewAt` |
| SAFE002 | a value crossing to another goroutine is not owned: it may alias a parameter, a package variable, or a result the callee may still reference |
| SAFE003 | a value is used after it was moved; `SAFE003-forkjoin` marks uses reached only after a channel receive, `select`, `WaitGroup.Wait` or `Lock` that is not a [join](#fork-join) for that move |
| SAFE004 | a moved value also escapes elsewhere (stored in a package variable, kept by a callee, returned) |
| SAFE005 | a `//safe:imm` type is malformed, or is written after its composite literal |
| SAFE006 | a package variable is written outside initialisation and is not `//safe:guarded_by`; reported once, at the declaration, with each write as a related position |
| SAFE007 | a malformed `//safe:` directive |
| SAFE008 | a handler (a callback that runs concurrently with itself) writes memory it shares with its other invocations: what it captured, or its receiver |
| SAFE009 | `//safe:guarded_by` memory is accessed without its mutex held, a `//safe:holds` function is called without it, or a value read from guarded memory leaves the lock |

### SAFE008: handlers racing with themselves

Two requests can run the same handler at once, and the code that registered it cannot see that
race, so the transfer rules do not catch it. SAFE008 starts from what every invocation shares (a
closure's captured variables, a method value's receiver, a `Handler` value's `ServeHTTP` receiver)
and follows it through field, index, slice, conversion and load steps to any store, map update, or
`delete`/`clear`/`copy`/`append` into it. It follows the shared memory into functions of this
package the handler calls and into closures it calls directly. It stops at:
- goroutines the handler starts, which the transfer rules check;
- interface and dynamic calls, and calls into other packages (stated gaps);
- handler values it cannot resolve to a body.

There is no exemption by type: `s.count++` on an `int` races. Atomic and mutex methods are calls,
not stores, so they are never reported. A write under a lock is reported unless the memory is
`//safe:guarded_by`; then SAFE009 checks it instead. A `//safe:unsafe` on the registration mutes
every SAFE008 of that handler and counts as a suppressed transfer. One on a write mutes that line
only. One finding is reported per source line.

### SAFE009: guarded memory

A field, package variable or local variable annotated `//safe:guarded_by mu` may be read only while `mu` is held
(`RLock` is enough for a `sync.RWMutex`), and written only while it is locked. A function marked
`//safe:holds mu` may be called only with `mu` held, and must not unlock it (SAFE007). Nothing
non-shareable read from guarded memory may leave the lock. That covers being returned, sent,
stored outside memory the same mutex guards, handed to a goroutine or callback, or passed to a
function whose summary says it may keep it.

"Held" is a forward must-analysis over each function's control-flow graph:
- **Identity:** a mutex is identified by its path, a root SSA value plus field steps. A load is a
  step only from a variable assigned once before the load.
- **Lock operations:** `Lock`/`RLock` add a path and `Unlock`/`RUnlock` remove it. A `TryLock`
  adds it on the branch where it succeeded. A deferred `Unlock` changes nothing until return.
- **Calls:** a function of this package releases the parameter mutexes it unlocks (summarised).
  Any call into another package, interface method or dynamic call releases every mutex in what it
  receives, including through `fmt.Println(c)`'s variadic slice.
- **Closures:** a closure called in place starts with its caller's locks. Any other function starts
  with none, plus its `//safe:holds`.
- **Locals:** a guarded local is a cell: its `Alloc` in the declaring function and, in each closure
  that captures it, the free variable bound to it. Its mutex is found the same way, so a goroutine
  that locks its captured `mu` holds the guard of its captured `x`. A closure that does not capture
  `mu` cannot hold it. A guarded local is not moved by the goroutines that capture it (no SAFE003),
  because SAFE009 checks every access to it instead.

Exempt: package variables during initialisation; a guarded local in its own function until a closure
captures it or its address goes anywhere but a direct load or store; and an object this function
allocated that nothing else can see yet (a constructor filling it in before it is returned, stored
or moved).

## Annotations

```go
//safe:iso j            // on a func: j is owned; passing an argument to it moves the argument
func forward(ch chan *Job, j *Job) { ch <- j }

//safe:imm              // on a struct: deeply immutable, so it may be shared freely
type Config struct{ name string; port int }

//safe:unsafe <reason>  // exempts the next statement, or the whole function from its doc comment

type Server struct {
	mu   sync.Mutex
	hits map[string]int //safe:guarded_by mu   // every access must hold mu
}

//safe:holds mu         // on a func: its callers must hold mu (a receiver field or package variable)
func (s *Server) evictLocked() { clear(s.hits) }
```

`//safe:guarded_by mu` goes on an unexported struct field (mu is a field of the same struct; an
embedded mutex is named `Mutex`), on a package variable (mu is a package variable), or on a local
variable declared with `var` or `:=` (mu is a variable in scope there: a local of this or an
enclosing function, or a package variable). It guards every name the declaration introduces; a `:=`
that redeclares an existing name is rejected. The mutex is a `sync.Mutex` or `sync.RWMutex` held by
value. Pointers to mutexes, `sync.Locker` values and exported fields or package variables are
rejected as SAFE007. These are checked claims, like `imm` and `iso`, so they count toward neither
the hatch ratio nor the override budget. SAFE009 enforces them, and a write to a guarded package
variable, or to a guarded field of one, is exempt from SAFE006.

```go
var mu sync.Mutex
x := 0 //safe:guarded_by mu
go func() { mu.Lock(); x++; mu.Unlock() }()
mu.Lock(); x++; mu.Unlock()
```

To share a struct between goroutines, make it `//safe:imm` and guard its mutable fields. An imm
type's guarded fields may have any type, and SAFE005 leaves them to SAFE009. Every other field still
never changes after the composite literal, so a `*Server` holding a guarded map can be captured by
every handler:

```go
//safe:imm
type Server struct {
	name string
	mu   sync.Mutex
	hits map[string]int //safe:guarded_by mu
}
```

Guarding a field of a type that is not imm does not make it shareable: moving it to another
goroutine is still SAFE002/003.

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
committed), runs gocap unannotated, and writes `build/corpus-report.json`. It then scores a second
copy carrying the `//safe:guarded_by` directives listed in
[annotations.go](cmd/gocap-corpus/annotations.go) and writes `build/corpus-report-annotated.json`
([below](#the-annotated-copy)). A test function counts as
flagged if gocap reports anything in it or in a same-package function it calls. A finding also
counts where its related positions are, so a declaration-site SAFE006 counts for the functions whose
writes make the variable mutable.

go1.27.3, 22 of 24 files. Skipped: `cgo_test.go` imports `internal/testenv` and `synctest_test.go`
imports `internal/synctest`; neither can build outside GOROOT.

| | SAFE006 at the declaration | Predicted before that run | With callbacks | Predicted before that run | With fork-join | Predicted before that run | MVP (`b42b26c`) | Predicted before the MVP run |
|---|---|---|---|---|---|---|---|---|
| `TestRace*` flagged | 229 / 234 (97.9%) | 229 (needs related positions) | 229 / 234 (97.9%) | 229 | 228 / 234 (97.4%) | 228, unchanged | 228 / 234 (97.4%) | at least 85% |
| `TestNoRace*` clean | 36 / 129 (27.9%) | at most 38; the design review said 38 | 38 / 129 (29.5%) | 38 | 40 / 129 (31.0%) | 40 | 30 / 129 (23.3%) | 15 to 40% |
| `TestNoRace*` flagged only for fork-join | 7 | 7 | 7 | 7 | 7 | 7 | 17 | expected to dominate the misses; **it did not** |

Moving SAFE006 to the declaration would have dropped five races caught only by it
(`TestRaceIssue12664`…`_4`, `TestRaceIntRWGlobalFuncs`): the finding left the test functions. Scoring
related positions keeps them. Two race-free tests became flagged because they write a package
variable outside init: `TestNoRaceFinGlobal` (its mutex-guarded counter needs
`//safe:guarded_by Mutex`) and `TestNoRaceRpcChan` (a counter reset on one goroutine).

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

### The annotated copy

The unannotated numbers measure gocap with no claims, and lock discipline is a claim the programmer
makes. The annotated copy adds the comment a programmer would write who believes each test's locking
protects a variable. That is 11 `//safe:guarded_by` directives: comments only, no code changes. Every
corpus test whose sharing is a lock discipline is annotated, together with the race tests of the
same shape, so the run also shows whether an annotation hides a race. Each directive anchors on one
exact line inside one function or declaration, and the run fails if any anchor does not match.

| | Annotated (11 directives) | Predicted before that run | Unannotated, same run |
|---|---|---|---|
| `TestRace*` flagged | 229 / 234 (97.9%) | 229 | 229 / 234 (97.9%) |
| `TestNoRace*` clean | 40 / 129 (31.0%) | 40 (the design review said 42: its base of 38 plus Mutex, RWMutex, RWMutexMultipleReaders and RWMutexTransitive) | 36 / 129 (27.9%) |
| `TestNoRace*` flagged only for fork-join | 6 | not predicted | 7 |

Per annotated test, the categories without the directives and with them, as predicted:

| Test | Without | With |
|---|---|---|
| `TestNoRaceMutex` | SAFE003 | clean |
| `TestNoRaceRWMutexMultipleReaders` | SAFE003 | clean |
| `TestNoRaceRWMutexTransitive` | SAFE003, SAFE003-forkjoin | clean |
| `TestNoRaceFinGlobal` | SAFE006 | clean |
| `TestNoRaceFuncUnlock` | SAFE003 | SAFE009 |
| `TestNoRaceAsFunc4` | SAFE003-forkjoin | SAFE009 |
| `TestRaceMutex`, `TestRaceMutex2`, `TestRaceMutexRWMutex`, `TestRaceAsFunc3` | SAFE003 or SAFE003-forkjoin | SAFE009 |
| `TestRaceRWMutexMultipleReaders` | SAFE003, SAFE003-forkjoin | SAFE009 |

- **No race was hidden.** Every annotated race test is still flagged, now by SAFE009, not by a move.
  The scorer records categories only, so the lines were checked separately (`go vet -vettool` on
  `build/racecorpus-annotated`). Each SAFE009 sits on an unlocked or wrongly locked access:
  - the writes before `Lock` in `TestRaceMutex`;
  - the writes under the other mutex in `TestRaceMutex2` and `TestRaceMutexRWMutex`;
  - the writes of `y` under `RLock` in `TestRaceRWMutexMultipleReaders`, plus its final `_ = y`, which
    is unlocked;
  - the argument read outside the lock in `TestRaceAsFunc3`.
- **Two race-free tests stay flagged, by SAFE009.** `TestNoRaceFuncUnlock` and `TestNoRaceAsFunc4`
  take the lock inside a function they call and release it in the caller. The lock analysis models
  releases inside callees, not acquisitions.
- **`TestNoRaceRWMutex` is not annotated.** It declares `var x, y int64 = 0, 1` and writes `y` under
  `RLock`. A directive on that line guards both names, so guarding `x` alone would take a code change.
  This is where the design review's prediction differs from the result. It counted `TestNoRaceRWMutex`
  among its four, but that test stays flagged. `TestNoRaceFinGlobal`, which the review did not count,
  cleared instead. The review's 42 is otherwise its older base of 38 against today's 36.
- **`TestNoRaceCond` is not annotated.** Its `x` is handed over through a `sync.Cond` signal, not
  accessed under the mutex.

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
- **Lock discipline is declared, not inferred.** SAFE009 checks memory annotated
  `//safe:guarded_by`. A struct with guarded fields crosses goroutines only if it is also
  `//safe:imm`. A directive guards every name its declaration introduces.
- **SAFE009 does not see a lock taken inside a callee.** A function that locks a mutex and returns
  with it held leaves the caller believing it is not held (`TestNoRaceFuncUnlock`). A closure that
  does not capture the mutex cannot hold it, even when it is called in place under the lock.
- **Only listed callback functions are goroutine boundaries.** A library function that runs its
  argument on another goroutine but is not in `spawn.go` (a worker pool, a scheduler) is an ordinary
  call to gocap. Its callback is checked only through the callee's summary, which treats it as
  retained.
- **SAFE008 follows in-package calls only.** A handler that hands shared memory to another
  package's function that writes it (`sort.Ints(s)`, `json.Unmarshal(b, &cfg)`), or to an interface
  method, is not reported: summaries record retention, not writes.
- **SAFE009 identifies a mutex by its path.** The same mutex reached another way (an alias, or
  through a load of a pointer field) is a different path, which over-reports and never
  under-reports. A `TryLock` counts only when its result is the branch condition, or that
  condition's negation.
- **SAFE009 treats any call into another package as a possible unlock** of every mutex in what it
  receives. Its "leaves the lock" check uses callee summaries, so passing guarded memory to a
  function that retains it (`fmt` included) is a finding.
- **`Timer.Stop` and `context.AfterFunc`'s stop function are not modelled.** Stopping a callback does
  not hand its memory back, which is right when `Stop` returns false and over-reports when it
  returns true.
- **SAFE006 sees direct writes only.** A store, map update or `copy`/`delete`/`clear` into the
  variable counts. A write through a method of another package, or through a pointer that escaped
  the package, does not. Initialisation is `init`, variable initialisers, and helpers every caller of
  which is initialisation and whose value is never taken. A variable reassigned only by tests (a
  test seam) is a finding; accept it with `//safe:unsafe` on the declaration.
- **Imm types are minimal.** Only struct types with unexported fields qualify. Each field must be
  shareable or `//safe:guarded_by`, so an unguarded slice or map disqualifies the type. Shared imm
  values must be built with `&T{...}`.
- **Trailing directives attach forward.** A `//safe:unsafe` trailing the closing line of a
  multi-line statement attaches to the next statement; put it on the line before.
- **Iterators lose ownership.** Range-over-func loops keep ownership only when the iterator function
  is statically known. An iterator obtained as a func value (for example `slices.All(s)`) is treated
  as retaining what its loop body captures.

## Next

This README is the design of record: gocap checks memory where it crosses goroutines, and leaves
aliasing within one goroutine alone. In build order:

Done:
- callback functions as goroutine boundaries;
- SAFE001 for dot imports of `unsafe` and functions without a Go body;
- SAFE008 for handlers that write shared memory.

1. A guarded-by rule. It comes before item 2, since recording writes in summaries turns calls like
   `h.cache.Set(...)` into SAFE008 findings that need it. The guarded SAFE008 findings on the
   reference service are its acceptance cases. Five steps, from the 2026-11-09 design ruling:
   - **Done:** parse `//safe:guarded_by mu` and `//safe:holds mu`.
   - **Done:** SAFE006 checked where the package variable is declared. Race corpus: 229/234, and
     38 → 36 race-free tests clean. `TestNoRaceFinGlobal` (fixed by `//safe:guarded_by Mutex`) and
     `TestNoRaceRpcChan` write package variables outside init.
   - **Done:** lock tracking and SAFE009, with SAFE008 leaving guarded writes to SAFE009. No
     annotations exist in the corpus or the reference service, so neither moved. On a scratch copy
     of the reference service with 8 `guarded_by` and 1 `holds` added:
     - SAFE008 went from 13 to 1 (the unguarded test).
     - SAFE009 found 2: `paper.go:759` returns the guarded `*TTLCache` (a policy call: accept it, or
       override the type), and a stress test reads `len(windows)` unlocked.

     The design review predicted 3. Its third, `client_telemetry_test.go:335`, reads under
     `limiter.mu.Lock()`, so no finding is right.
   - **Done:** `//safe:imm` types may have guarded fields of any type. The same change closes a
     hole: the address of a field or element of a shared value (`&cfg.port` on an imm `*Config`)
     used to look owned and could be moved to a goroutine. It is now foreign (SAFE002). The race
     corpus and the reference service did not move, as predicted.
   - **Done:** guarded local variables, and the corpus scored a second time on an annotated copy:
     229 / 40, as predicted before the run ([the annotated copy](#the-annotated-copy)). The design
     review predicted 229 / 42 from the earlier base of 38. A write to a guarded field of a package
     variable is no longer SAFE006. The reference service did not move (no annotations).
2. A function override table beside the type table; summaries that record a call linking two
   arguments (`root.Add(child)`) instead of reporting an escape; and a writes bit in summaries, so
   SAFE008 follows calls into other packages.
3. The hatch ratio measured on the reference service: ScholarLM's `backend/go_orchestrator`, pinned
   at ScholarLM commit `bb60b53`, measured with `-tests` on go1.27.3. Untriaged baselines, each
   with 0 `//safe:unsafe`:

   | | Transfers | SAFE001 | SAFE002 | SAFE003 | SAFE003-forkjoin | SAFE004 | SAFE006 | SAFE008 | Findings |
   |---|---|---|---|---|---|---|---|---|---|
   | SAFE006 at the declaration | 1,642 | 104 | 784 | 685 | 202 | 347 | 238 | 13 | 2,373 |
   | With SAFE008 (`8add3ad`) | 1,642 | 104 | 784 | 685 | 202 | 347 | 130 | 13 | 2,265 |
   | With callbacks (`b56b798`) | 1,642 | 104 | 784 | 685 | 202 | 347 | 130 | - | 2,252 |
   | Before callbacks (`538bbe2`) | 1,258 | 104 | 511 | 458 | 193 | 263 | 123 | - | 1,652 |

   SAFE001 is mostly generated protobuf code and tests. The 384 transfers callbacks added are mostly
   HTTP handlers: `internal/api` went from 365 to 633. The 13 SAFE008 findings are on 6 handler
   registrations:
   - `paper.go` ×7 and `client_telemetry.go` ×3, mostly reached through helper calls;
   - `login.go:181` and `mcp_wiring.go:46`;
   - one test.

   Every non-test write is under a mutex: checked line by line in the 2026-11-09 design review, plus
   `client_telemetry.go:119`, which that review's prototype missed. The ratio is read against this
   denominator,
   after items 1 and 2, by adding overrides until every remaining finding is a real race or a
   policy call.
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
the `Wait` join, handlers, and `sync.Once.Do` as a non-spawn. `testdata/src/handlers` covers
SAFE008: each kind of write, aliases and loads, bound methods, `ServeHTTP`, calls followed within the
package, and what is not a finding (per-request memory, atomics and locks, reads, interface and
cross-package calls, a goroutine the handler starts). `testdata/src/guarded` covers SAFE009:
locked, `RLock`, `TryLock` (both branch forms), `defer Unlock`, `//safe:holds` helpers, closures
called in place, embedded mutexes, guarded package variables and `init`, and constructors. It also
covers unlocked writes, writes under `RLock`, reads after `Unlock`, the wrong mutex, an unlock by
another package's function, values leaking by return, by store and through a single result slot,
closures not called in place, and a `holds` helper that unlocks; `errgroup` there is a stub under
`testdata/src/golang.org/x/sync`, so `x/sync` stays out of the module's direct dependencies.
