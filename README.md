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
goroutine: a `go` statement (its arguments and the closure it starts), a channel send, and an
argument for a `//safe:iso` parameter. A closure's captured variables cross only when the closure
itself does. Inside one goroutine, aliasing is left alone.

## Rules

| ID | Rule |
|---|---|
| SAFE001 | `unsafe.Pointer`/`Add`/`Slice`/`String*`, cgo, `//go:linkname`, `reflect.Value.Set*`/`UnsafeAddr`/`UnsafePointer`, `reflect.NewAt` |
| SAFE002 | a value crossing to another goroutine is not owned: it may alias a parameter, a package variable, or a result the callee may still reference |
| SAFE003 | a value is used after it was moved; `SAFE003-forkjoin` marks uses reached only after a channel receive, `select`, `WaitGroup.Wait` or `Lock` |
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

## Measured on the Go race detector's corpus

`go run ./cmd/gocap-corpus -out build` copies `$GOROOT/src/runtime/race/testdata` into `build/` (never
committed), runs gocap unannotated, and writes `build/corpus-report.json`. A test function counts as
flagged if gocap reports anything in it or in a same-package function it calls.

go1.27.3, 22 of 24 files. Skipped: `cgo_test.go` imports `internal/testenv` and `synctest_test.go`
imports `internal/synctest`; neither can build outside GOROOT.

| | Result | Predicted before the run |
|---|---|---|
| `TestRace*` flagged | 228 / 234 (97.4%) | at least 85% |
| `TestNoRace*` clean | 30 / 129 (23.3%) | 15 to 40% |
| `TestNoRace*` flagged only for fork-join | 17 | expected to dominate the misses; **it did not** |

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
  - fork-join

**Six missed races**, each an unmodelled class:
- callbacks that run on another goroutine: `TestRaceAfterFunc3` (`time.AfterFunc`)
- send-or-close races on a channel: `TestRaceChanCloseSend`, `TestRaceChanSendClose`,
  `TestRaceChanSendSelectClose`
- reassigning a mutex variable: `TestRaceMutexOverwrite`
- WaitGroup misuse: `TestRaceWaitGroupWrongAdd`

## Known limits

- **Fork-join is not modelled.** A goroutine keeps what it was given for good.
- **Lock discipline is not modelled.** Mutex-guarded sharing needs `//safe:unsafe`.
- **Callbacks are not treated as goroutine boundaries.** A closure passed to `time.AfterFunc`,
  `http.HandleFunc` and similar can run on another goroutine, and gocap does not check what it
  captures.
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

1. Fork-join, to cut the largest class of `TestNoRace*` false positives.
2. A guarded-by rule: `//safe:guarded_by(mu)`.
3. Callback functions as goroutine boundaries.
4. Ownership of the channel's send end, so closes and sends are checked.
5. A differential test of the summaries against `-gcflags=-m`.

The project should be abandoned if a `net/http` reference service needs `//safe:unsafe` on more than
20% of its goroutine spawns and sends once the override table holds 100 entries.

## Tests

```bash
go test ./...
```

Each rule has `analysistest` cases with `// want` expectations, so a disabled rule fails its test.
`passes/ownership/testdata/src/user` checks that summaries cross package boundaries, including from
the standard library.
