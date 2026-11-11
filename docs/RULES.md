# GoSree (`gosree`) Rules & Diagnostics Reference

[← README](../README.md) | [Architecture](ARCHITECTURE.md) | [Rules](RULES.md) | [Directives](DIRECTIVES.md) | [Borrow & NLL](BORROW_CHECKING_AND_NLL.md) | [Concurrency](CONCURRENCY_MODEL.md) | [Error Discipline](ERROR_DISCIPLINE.md) | [Tooling](TOOLING_AND_ERASURE.md)

---

This document provides a comprehensive reference for all diagnostic codes emitted by **GoSree** (`gosree`). Each rule includes its formal semantics, concurrency rationale, failing code examples, and idiomatic remediation patterns.

---

## Diagnostic Code Summary

| Code | Subsystem | Description |
|---|---|---|
| **[SAFE001](#safe001-unsafe-operations-ban)** | `passes/unsafeban` | Use of `unsafe`, assembly, cgo, or unsafe reflection |
| **[SAFE002](#safe002-unowned-value-crossing-boundary)** | `passes/ownership` | Unowned memory crossing to another goroutine |
| **[SAFE003](#safe003-use-after-move)** | `passes/ownership` | Value used after ownership was moved |
| **[SAFE004](#safe004-moved-value-escapes-elsewhere)** | `passes/ownership` | Moved value also escapes into globals, returns, or callees |
| **[SAFE005](#safe005-deep-immutability-violation)** | `passes/ownership` | Immutability invariant violated on `//gosree:imm` type |
| **[SAFE006](#safe006-package-global-mutation)** | `passes/ownership` | Package variable mutated outside `init()` without mutex guard |
| **[SAFE007](#safe007-malformed-directive)** | `internal/annot` | Malformed, mispositioned, unknown, or unresolvable directive (and, only when the `safestale` analyzer is run, a stale `//gosree:unsafe`) |
| **[SAFE008](#safe008-self-racing-handler-writes)** | `passes/ownership` | HTTP handler writes shared receiver or captured memory |
| **[SAFE009](#safe009-guarded-memory-access-violation)** | `passes/ownership` | Guarded memory accessed without holding required mutex |
| **[BORROW001](#borrow001-second-class-borrow-storage-violation)** | `passes/borrow` | Second-class borrow stored in struct field or package global |
| **[BORROW002](#borrow002-conflicting-borrows)** | `passes/borrow` | Conflicting simultaneous mutable or aliased borrows (NLL) |
| **[BORROW003](#borrow003-owner-mutation-during-active-borrow)** | `passes/borrow` | Owner mutated or moved while active borrow exists |
| **[BORROW004](#borrow004-dangling-stack-reference-return)** | `passes/borrow` | Function returns reference to local stack allocation |
| **[CAP003](#cap003-stack-scoped-borrow-concurrency-escape)** | `passes/ownership` | Stack-scoped borrow crosses goroutine or channel boundary |
| **[ERR001](#err001-must-use-error-return-discarded)** | `passes/errcheck` + `rust/gosree-errsolve` | Error return silently discarded as statement, `go`, or `defer` (`#[must_use]`) |
| **[ERR002](#err002-unchecked-companion-result-unwrap)** | `passes/errcheck` + `rust/gosree-errsolve` | Companion value of `(T, error)` used after `_` discard or before proving `err == nil` |
| **[ERR003](#err003-shadowed-overwritten-or-inverted-error)** | `passes/errcheck` + `rust/gosree-errsolve` | Error lost via inner `err :=` shadow, unread overwrite, or `if err != nil { return nil }` |
| **[ERR004](#err004-typed-nil-concrete-error-interface-boxing)** | `passes/errcheck` + `rust/gosree-errsolve` | Concrete `*T` error pointer that may be `nil` boxed into an `error` interface |
| **[ERR005](#err005-writable-resource-close-error-discarded)** | `passes/errcheck` + `rust/gosree-errsolve` | `Close()` / `defer f.Close()` on writable or written resource discards flush error |

Every finding makes the `gosree` driver exit with status 3 (`cmd/gosree/preflight.go`: 0 clean, 1 failed, 2 usage, 3 findings); diagnostics carry no separate severity levels.

---

## SAFE001: Unsafe Operations Ban

### Semantics
`SAFE001` flags any operation that bypasses Go's type or memory safety:
- `unsafe.Pointer`, `unsafe.Add`, `unsafe.Slice`, `unsafe.SliceData`, `unsafe.String`, `unsafe.StringData`, including via dot-import (`. "unsafe"`). `unsafe.Sizeof`, `Alignof` and `Offsetof` are compile-time constants and stay allowed.
- Cgo invocations (`import "C"`). When the package is loaded, cgo has already replaced the import with generated files, so in a run against a cgo package the findings (`//go:linkname`, `//go:noescape`, functions without a body, `unsafe.Pointer`) are reported at positions inside cgo's generated files in the Go build cache, not at the `import "C"` line. The `cgo (import "C") bypasses Go's memory safety` message exists in `passes/unsafeban` but was not observed in this run.
- Compiler directives that alter linkage or escape analysis (`//go:linkname`, `//go:noescape`).
- Assembly functions declared without a Go body (`func asmFunc()`).
- Reflection that writes or exposes raw memory: `reflect.NewAt`, `Copy`, `Swapper`, `SliceAt`, `SliceHeader`/`StringHeader`, and the `reflect.Value` methods `Set*`, `UnsafeAddr`, `UnsafePointer`, `Pointer`, `Addr`, `Clear`, `Grow`, `Send`, `TrySend`.

Unsafe memory operations bypass static capability guarantees and permit arbitrary memory corruption and type confusion.

### Failing Example
```go
package example

import "unsafe"

func Cast(p *int) *float64 {
    // SAFE001: unsafe.Pointer bypasses Go's memory safety; wrap it in //gosree:unsafe <reason>
    return (*float64)(unsafe.Pointer(p))
}
```

### Passing Remediation
```go
package example

import "math"

func Cast(p *int) uint64 {
    // Standard type-safe conversions or explicit bit conversions
    return math.Float64bits(float64(*p))
}
```

---

## SAFE002: Unowned Value Crossing Boundary

### Semantics
A value crossing to another goroutine (via `go f(x)`, channel send `ch <- x`, or call to a parameter marked `//gosree:iso`) must be **uniquely owned**. A value is not owned if it:
- Aliases an incoming parameter of the caller.
- Aliases a package-level global variable.
- Aliases a return value that the callee may still reference.

### Failing Example
```go
package example

type Job struct{ ID int }

func Forward(ch chan *Job, j *Job) {
    // SAFE002: parameter j is sent on a channel but is not owned here: the caller
    // still holds it (mark the parameter //gosree:iso to take ownership)
    ch <- j
}
```

### Passing Remediation
```go
package example

type Job struct{ ID int }

//gosree:iso j
func Forward(ch chan *Job, j *Job) {
    // Parameter j is uniquely owned by Forward; caller transfers ownership
    ch <- j
}
```

---

## SAFE003: Use After Move

### Semantics
An owned value transferred to another goroutine (via `go`, channel send, or `//gosree:iso` call) has moved. Any subsequent read, write, or call on that value in the sender is rejected as use-after-move.

> [!NOTE]
> **Diagnostic Category: `SAFE003-forkjoin`**
> Emitted with `Diagnostic.Category = "SAFE003-forkjoin"` when a use occurs after a synchronization operation (`sync.WaitGroup.Wait`, channel receive, or mutex unlock) that fails to meet all formal requirements of a [join point](CONCURRENCY_MODEL.md#3-fork-join-synchronization-recovery). The emitted diagnostic message retains the canonical `SAFE003: ` prefix and ends with a note, e.g. `SAFE003: variable j is used after it was captured by a goroutine started by (*sync.WaitGroup).Go (line N) (after a synchronisation point, but not one that joins the goroutine it was moved to)` (`passes/ownership/check.go`, `passes/ownership/testdata/src/callbacks/callbacks.go`).

### Failing Example
```go
package example

type Config struct{ Port int }

func RunWorker(ch chan *Config) {
    cfg := &Config{Port: 8080}
    ch <- cfg       // cfg moved to receiver
    println(cfg.Port) // SAFE003: composite literal is used after it was sent on a channel (line N)
}
```

### Passing Remediation
```go
package example

type Config struct{ Port int }

func RunWorker(ch chan *Config) {
    cfg := &Config{Port: 8080}
    port := cfg.Port // Read before moving
    ch <- cfg        // cfg moved
    println(port)    // OK: primitive value copied
}
```

---

## SAFE004: Moved Value Escapes Elsewhere

### Semantics
A value moved to another goroutine must not simultaneously escape through other paths in the current function:
- Stored into a package global variable.
- Retained by a callee function.
- Returned to the caller.

### Failing Example
```go
package example

type Item struct{ Data string }

var cache *Item

func Process(ch chan *Item) *Item {
    item := &Item{Data: "fresh"}
    ch <- item      // Moved to goroutine
    cache = item    // SAFE004: composite literal is sent on a channel but also escapes: stored in package variable cache
    return item
}
```

> [!NOTE]
> The same function also reports `SAFE002` (the literal may alias `cache`), `SAFE003` on the later use, and `SAFE006` at `cache`'s declaration, because those rules each see the same defect. The `return item` line produced no separate diagnostic when run.

### Passing Remediation
```go
package example

type Item struct{ Data string }

func Process(ch chan *Item) {
    item := &Item{Data: "fresh"}
    ch <- item // Moved cleanly; no secondary escape
}
```

---

## SAFE005: Deep Immutability Violation

### Semantics
Types declared `//gosree:imm` must be deeply immutable:
- Every field must be unexported (otherwise `field Host is exported, so other packages could write it`).
- Field types must be pointer-free primitives, strings, channels of shareable types, or pointers to other verified `//gosree:imm` types.
- Slices, maps, and interface fields are disallowed unless guarded by `//gosree:guarded_by`.
- Values of an `imm` type are only written by their composite literal; any other field write is reported as `SAFE005`.

### Failing Example
```go
package example

//gosree:imm
type ServerConfig struct {
    host string
    tags []string // SAFE005: //gosree:imm type ServerConfig is not immutable: field tags has type []string, which can hold shared mutable memory (...)
}
```

### Passing Remediation
```go
package example

//gosree:imm
type ServerConfig struct {
    host string
    port int
    // Unexported, pointer-free and string fields only
}
```

---

## SAFE006: Package Global Mutation

### Semantics
Package-level global variables must not be mutated outside package `init()` functions unless protected by a declared mutex guard (`//gosree:guarded_by <mu>`). The diagnostic is reported once at the global variable's declaration, with all mutation sites listed as related positions.

### Failing Example
```go
package example

// SAFE006: package variable counter is written outside init
var counter int

func Increment() {
    counter++
}
```

### Passing Remediation
```go
package example

import "sync"

var mu sync.Mutex
var counter int //gosree:guarded_by mu

func Increment() {
    mu.Lock()
    counter++
    mu.Unlock()
}
```

---

## SAFE007: Malformed Directive

### Semantics
Emitted when a `//gosree:` directive (see [DIRECTIVES.md](DIRECTIVES.md)) is syntactically invalid, misplaced, or refers to nonexistent symbols. A malformed directive has no effect at all:
- Unknown verb (`unknown directive //gosree:isoo (known: unsafe, iso, imm, guarded_by, holds, mub, rob, mut, ref)`).
- Near-miss spelling: a comment whose text after the marker starts with `gosree:` in any letter case but is not a valid directive (`// gosree:iso p`, `//Gosree:holds mu`, `/* gosree:mut */`). The message is `"// gosree:iso p" is not a gosree directive, so it has no effect: write it as //gosree:iso p (no space after the slashes, lower case)`. Prose such as `// gosree: this lock is held` is left alone (`internal/annot/nearmiss.go`).
- `unsafe` without a reason, or with a reason that has no letter or digit.
- `iso`/`mub`/`rob` naming a name that is not a parameter, or two conflicting capabilities on one variable.
- A borrow directive attached to something other than a function or assignment (for example a struct field).
- Stale `//gosree:unsafe` (it mutes no finding): produced by the `safestale` analyzer (`internal/annot/stale`), which `cmd/gosree` does not register, so it is emitted only when that analyzer is run (its own test).
- Guarded-by referencing a nonexistent mutex, a non-mutex, or guarding an exported field or variable.
- Guarded-by target variable is not in scope.
- Directive attached to a node it does not support (`//gosree:iso` above a `var`: `must be in a function's doc comment`; `//gosree:imm` outside a type declaration; `//gosree:unsafe` with nothing to attach to: `is not attached to a statement or declaration`).
- `//gosree:holds` naming a selector path (`in.mu`): `//gosree:holds needs one mutex name`.

### Failing Example
```go
package example

import "sync"

type Store struct {
    mu sync.Mutex
    // SAFE007: //gosree:guarded_by names "mu_nonexistent", which is not a field of this struct
    data map[string]int //gosree:guarded_by mu_nonexistent
}
```

### Passing Remediation
```go
package example

import "sync"

type Store struct {
    mu   sync.Mutex
    data map[string]int //gosree:guarded_by mu
}
```

---

## SAFE008: Self-Racing Handler Writes

### Semantics
HTTP request handlers run concurrently across incoming requests. The check fires at the handler's **registration** (`http.Handle`, `http.HandleFunc`, `(*ServeMux).Handle/HandleFunc`, ...), not at a `ServeHTTP` declaration with no registration. A write to memory shared across invocations (the receiver, or variables the handler closure captured) is flagged as `SAFE008` unless the field is `//gosree:guarded_by` (then `SAFE009` governs). Passing shared memory to an interface method or a dynamic call is reported too (`captured variable count is passed to a dynamic function call, which may write it, by a handler registered with ...`). A write to a package variable is `SAFE006`, not `SAFE008`; an atomic update through a captured `atomic.Int64` is not flagged.

### Failing Example
```go
package example

import "net/http"

type MetricsHandler struct {
    requestCount int
}

func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // SAFE008: receiver h is written here by a handler registered with
    // (*net/http.ServeMux).Handle (line N), which shares it with every concurrent request
    h.requestCount++
    _, _ = w.Write([]byte("OK"))
}

func Register(mux *http.ServeMux) {
    mux.Handle("/m", &MetricsHandler{}) // registration site: the finding's related position
}
```

### Passing Remediation
```go
package example

import (
    "net/http"
    "sync"
)

type MetricsHandler struct {
    mu           sync.Mutex
    requestCount int //gosree:guarded_by mu
}

func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    h.mu.Lock()
    h.requestCount++
    h.mu.Unlock()
    _, _ = w.Write([]byte("OK"))
}
// (register it with mux.Handle as above)
```

---

## SAFE009: Guarded Memory Access Violation

### Semantics
Memory marked with `//gosree:guarded_by <mu>` may only be accessed while `<mu>` is held:
- Reads require at least `mu.RLock()` (or `mu.Lock()`).
- Writes require `mu.Lock()`.
- Functions marked `//gosree:holds <mu>` must be invoked with `<mu>` held.
- Unlocked references to non-shareable guarded memory must not escape the critical section.

### Failing Example
```go
package example

import "sync"

type SafeMap struct {
    mu   sync.RWMutex
    data map[string]int //gosree:guarded_by mu
}

func (s *SafeMap) Get(key string) int {
    // SAFE009: field data is guarded by mu but is read here without holding it (lock mu, or mark Get //gosree:holds mu)
    return s.data[key]
}
```

### Passing Remediation
```go
package example

import "sync"

type SafeMap struct {
    mu   sync.RWMutex
    data map[string]int //gosree:guarded_by mu
}

func (s *SafeMap) Get(key string) int {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.data[key] // OK: s.mu held via RLock
}
```

---

## BORROW001: Second-Class Borrow Storage Violation

### Semantics
Second-class borrows (`cap.Mut[T]`, `cap.Ref[T]` and their `Mub`/`Rob` aliases, `//gosree:mub`, `//gosree:rob`) are strictly activation-frame scoped. Verified to be rejected:
- Struct fields declared with a `cap.Mut`/`cap.Ref` type.
- Package-level global variables.
- Channel sends (`channel send cannot store second-class borrow`, reported together with `CAP003`).
- Map stores (`map cannot store second-class borrow`).
- A borrow placed in a composite literal such as `[]*int{p}` or `H{p: p}` (`borrow stored in struct field 'p' escapes stack frame`: the wording says "struct field" even for a slice literal).
- `go` statements that capture a borrow (`goroutine cannot capture second-class borrow`, reported together with `CAP003`).

Other sinks are not covered by this section's examples; see `passes/borrow/storage.go`.

### Failing Example
```go
package example

import "github.com/bharathvbcr/gosree/cap"

type Holder struct {
    // BORROW001: struct field 'ptr' cannot have second-class borrow type: borrows are second-class and cannot be stored in struct fields
    ptr cap.Ref[int]
}

// BORROW001: package variable 'globalRef' cannot store second-class borrow
var globalRef cap.Mut[int]
```

A `//gosree:rob` or `//gosree:mub` written on a struct field or package variable instead of a `cap` type is itself reported as `SAFE007` (`//gosree:rob must be attached to a function declaration or an assignment statement`), because the directive only attaches to functions and assignments. The field or variable is nevertheless also reported as `BORROW001`: `var globalRef *int //gosree:mub` and `ptr *int //gosree:rob` each printed both `SAFE007` and `BORROW001` in this run.

### Passing Remediation
```go
package example

// Borrows passed directly as function arguments instead of stored in structs
func Process(val *int /*gosree:rob*/) int {
    return *val
}
```

---

## BORROW002: Conflicting Borrows

### Semantics
Enforces the fundamental Rust borrowing rule using Non-Lexical Lifetimes (NLL):
- Multiple simultaneous mutable borrows of the same allocation are rejected.
- A mutable borrow cannot coexist with an active read-only borrow of the same allocation.

### Failing Example
```go
package example

func Conflict() {
    x := 10
    b1 := &x //gosree:mut
    b2 := &x //gosree:mut // BORROW002: cannot borrow 'x' as mutable more than once at a time: already borrowed as mutable at line N
    *b1 = 20
    *b2 = 30
}
```

### Passing Remediation
```go
package example

func Sequential() {
    x := 10
    b1 := &x //gosree:mut
    *b1 = 20 // Last use of b1 (NLL lifetime expires here)
    
    b2 := &x //gosree:mut // OK: b1 is no longer live
    *b2 = 30
}
```

---

## BORROW003: Owner Mutation During Active Borrow

### Semantics
While a second-class borrow is active, the underlying owner variable cannot be directly mutated or moved.

### Failing Example
```go
package example

func Invalidation() {
    x := 100
    b := &x //gosree:mut
    x = 200 // BORROW003: cannot assign to 'x' because it is borrowed at line N (the borrow's line)
    *b = 300
}
```

### Passing Remediation
```go
package example

func ValidOrder() {
    x := 100
    b := &x //gosree:mut
    *b = 300 // Last use of b
    
    x = 200 // OK: borrow b has expired
}
```

---

## BORROW004: Dangling Stack Reference Return

### Semantics
A function cannot return a pointer or reference to a local stack-allocated variable, as the memory ceases to exist when the stack frame unwinds.

### Failing Example
```go
package example

func Dangling() *int {
    x := 42
    return &x // BORROW004: cannot return reference to local stack variable 'x'
}
```

### Passing Remediation
```go
package example

func SafeAlloc() *int {
    // Heap allocation or return by value
    p := new(int)
    *p = 42
    return p
}
```

---

## CAP003: Stack-Scoped Borrow Concurrency Escape

### Semantics
A second-class stack-scoped borrow (`\mub` or `\rob`) cannot cross a concurrency boundary:
- Cannot be sent across a channel (`ch <- b`).
- Cannot be captured inside a goroutine closure (`go func() { ... b ... }()`).
- Cannot be passed as an argument to a `go` statement (`go worker(b)`).

The three messages end in `cannot be sent on a channel`, `cannot be captured by a goroutine` and `cannot be passed as argument to go statement`.

### Failing Example
```go
package example

func Escape(ch chan *int, b *int /*gosree:mub*/) {
    // CAP003: stack-scoped borrow 'b' (\mub) cannot cross goroutine boundary:
    // cannot be sent on a channel
    // (BORROW001 also reports the send: channel send cannot store second-class borrow)
    ch <- b
}
```

### Passing Remediation
```go
package example

// Transfer owned isolated value instead of a stack borrow
//gosree:iso val
func Transfer(ch chan *int, val *int) {
    ch <- val // OK: val is uniquely owned (\iso)
}
```

---

## ERR001: Must-Use Error Return Discarded

### Semantics
Models Go `error` returns with Rust `#[must_use] Result<T, E>` discipline (`passes/errcheck`, analyzer `safeerr`, backed in-process by the Rust `gosree-errsolve` CFG/SSA monotone fixpoint solver via Gusset). Any call whose return signature ends in `error` must bind its error or explicitly discard it with `_ = ...`:
- Bare expression statements (`os.WriteFile(...)`), `go fallible()`, and `defer fallible()` are flagged.
- Curated infallible/stdout writers (`fmt.Print*`, `fmt.Fprint*` on `os.Stdout`/`os.Stderr`/`io.Discard`/`bytes.Buffer`/`strings.Builder`, `bytes.Buffer.Write*`, `strings.Builder.Write*`, and `hash.Hash.Write`) are exempt.
- A `Close() error` method call as a statement or `defer` is never `ERR001`: it is `ERR005` when the resource is writable, and silent otherwise. `go f.Close()` is still `ERR001`.
- `bufio.Writer.Flush()` or any other error-returning method dropped as a statement is `ERR001` (the message names the callee as `pkg.Method()`, without the receiver).
- Limit: the check is per call statement; `_ = f()` is the explicit-discard escape and is accepted. Closure bodies are analyzed as their own functions.

### Failing Example
```go
package example

import "os"

func Save(path string, data []byte) {
    // ERR001: error return from os.WriteFile() is discarded without handling (bind and check err, or assign to _ to discard explicitly)
    os.WriteFile(path, data, 0o600)
}
```

### Passing Remediation
```go
package example

import "os"

func Save(path string, data []byte) error {
    if err := os.WriteFile(path, data, 0o600); err != nil {
        return err
    }
    return nil
}
```

---

## ERR002: Unchecked Companion Result Unwrap

### Semantics
Treats Go `(T, error)` multi-returns as a coupled sum type `Result<T, E>` rather than independent product values:
- Blanking the error while using the companion value (`n, _ := strconv.Atoi(s); use(n)`) is rejected, reported at the call as `ERR002: companion value from strconv.Atoi() is used after discarding its error with '_'`. A fully blanked or companion-blank assignment with no bound companion is not flagged.
- Reading, dereferencing, ranging over (covered by `tests/stress/testdata/src/er_unwrap`), sending, using as a `make` length, or returning `T` alongside a `nil` error on any CFG path where `err == nil` has not been established (e.g., logging `if err != nil { log.Println(err) }` without returning or assigning a fallback value to `T`) is rejected.
- Idiomatic `io.Reader.Read` partial reads (`if n > 0 { ... }` and `if err != nil && err != io.EOF { return err }`) refine the companion byte count cleanly.

### Failing Example
```go
package example

import "os"

func FileSize(path string) int64 {
    fi, err := os.Stat(path)
    if err != nil {
        println("stat failed") // Missing return!
    }
    // ERR002: result of os.Stat() from fallible call is used before proving its companion error is nil
    return fi.Size()
}
```

### Passing Remediation
```go
package example

import "os"

func FileSize(path string) (int64, error) {
    fi, err := os.Stat(path)
    if err != nil {
        return 0, err
    }
    return fi.Size(), nil
}
```

---

## ERR003: Shadowed, Overwritten, or Inverted Error

### Semantics
Prevents silent loss of captured `error` values across intra-procedural control flow:
- **Shadowing (`err :=`)**: An inner block short-variable declaration `x, err := ...` shadows an outer `err` (or named error result) without terminating on `err != nil`, causing the outer scope to observe stale `nil`.
- **Unread Overwrite (`err = ...`)**: Assigning a new value (or `return nil` over a named result) to an `error` variable that holds an uninspected error from an earlier call on some incoming CFG path.
- **Inverted Nil Return (`nilerr`)**: Returning literal `nil` in a block guarded by `if err != nil` without inspecting or translating `err`.

Messages: shadow `ERR003: inner 'err' declaration shadows outer 'err', losing the inner error on fallthrough`; overwrite `ERR003: assignment to 'err' overwrites an earlier unchecked error`; nilerr `ERR003: returning nil error on a path where an active error was just checked non-nil (nilerr)`. Not flagged: an inner `err :=` whose block is followed by an `if err != nil { return/panic/Fatal/os.Exit }` guard, accumulator patterns, and `errors.Is` sentinel checks (all in `passes/errcheck/testdata/src/errbasic`). The shadow scan is syntactic and checks the outer variable's reads in the same function only.

### Failing Example
```go
package example

import "os"

func Load(path string) (n int, err error) {
    if path != "" {
        // ERR003: inner 'err' declaration shadows outer 'err', losing the inner error on fallthrough
        data, err := os.ReadFile(path)
        if err == nil {
            n = len(data)
        }
    }
    return n, err
}
```

### Passing Remediation
```go
package example

import "os"

func Load(path string) (int, error) {
    if path == "" {
        return 0, nil
    }
    data, err := os.ReadFile(path)
    if err != nil {
        return 0, err
    }
    return len(data), nil
}
```

---

## ERR004: Typed-Nil Concrete Error Interface Boxing

### Semantics
In Go, boxing a `nil` concrete pointer (`var e *MyErr = nil`) into an `error` interface (or any sub-interface implementing `error`, or via `any` + `.(error)`) produces a **non-nil** interface header `(tab=*MyErr, data=nil)` for which `err != nil` evaluates to `true` and `err.Error()` panics. `ERR004` tracks concrete `*T` nilability in the solver (`gosree-errsolve`, or its pure-Go oracle) across the functions of a package, and across packages through `errcheck.FuncFact`, exported only for exported functions whose single result is a concrete pointer type implementing `error`. It rejects boxing where `*T` is not proven non-nil, including laundering through `any` followed by `.(error)` (`tests/stress/testdata/src/er_typednil`).

Two messages: `ERR004: concrete pointer *T may be nil when boxed into error interface; guard with '!= nil' and return untyped nil`, and, when the pointer is definitely nil, `ERR004: nil concrete pointer *T is boxed into a non-nil error interface (typed-nil hazard)`.

### Failing Example
```go
package example

type ValidationError struct{ Field string }

func (e *ValidationError) Error() string { return "invalid " + e.Field }

func validate(name string) *ValidationError {
    if name == "" {
        return &ValidationError{Field: "name"}
    }
    return nil
}

func Register(name string) error {
    // ERR004: concrete pointer *ValidationError may be nil when boxed into error interface
    return validate(name)
}
```

### Passing Remediation
```go
package example

func Register(name string) error {
    if err := validate(name); err != nil {
        return err
    }
    return nil
}
```

---

## ERR005: Writable Resource Close Error Discarded

### Semantics
A resource is writable when it comes from `os.Create`, `os.CreateTemp`, `os.OpenFile` (any non-zero constant flag, or a non-constant flag), `gzip`/`zlib`/`flate` `NewWriter*`, `zip`/`tar` `NewWriter`, or when any value with a `Close` method has `Write`, `WriteString`, `WriteByte`, `WriteRune`, `WriteAt`, `ReadFrom`, `Flush` or `Sync` called on it. Dropping `f.Close()` or `defer f.Close()` on such a resource is `ERR005` (`ERR005: Close() on writable resource f discards flush error`, or `deferred Close()` for the defer form). Read-only handles (`os.Open`, `os.OpenFile(..., os.O_RDONLY, ...)`) stay clean. `bufio.NewWriter` is marked writable but `bufio.Writer` has no `Close`, so a dropped `Flush()` is reported as `ERR001`. `_ = f.Close()` and a handled `f.Close()` are accepted.

### Failing Example
```go
package example

import "os"

func WriteReport(path string, body []byte) error {
    f, err := os.Create(path)
    if err != nil {
        return err
    }
    defer f.Close() // ERR005: deferred Close() on writable resource f discards flush error
    _, err = f.Write(body)
    return err
}
```

### Passing Remediation
```go
package example

import "os"

func WriteReport(path string, body []byte) (err error) {
    f, err := os.Create(path)
    if err != nil {
        return err
    }
    defer func() {
        if cerr := f.Close(); err == nil {
            err = cerr
        }
    }()
    _, err = f.Write(body)
    return err
}
```

---

## Suppressing ERR findings

`safeerr` honors the same escape hatch as every other pass: a `//gosree:unsafe <reason>` covering the finding's position mutes it (`passes/errcheck/errcheck.go`, `ix.Suppressed` then `ix.Mute`), counted in `Summary.FindingsMuted`. Findings are deduplicated per line, rule and sub-kind. Fixture: `suppressedUnsafe` in `passes/errcheck/testdata/src/errbasic/basic.go`.
