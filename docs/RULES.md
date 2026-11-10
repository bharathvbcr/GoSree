# GoSree (`gosree` / `gocap`) Rules & Diagnostics Reference

[← README](../README.md) | [Architecture](ARCHITECTURE.md) | [Rules](RULES.md) | [Directives](DIRECTIVES.md) | [Borrow & NLL](BORROW_CHECKING_AND_NLL.md) | [Concurrency](CONCURRENCY_MODEL.md) | [Tooling](TOOLING_AND_ERASURE.md)

---

This document provides a comprehensive reference for all diagnostic codes emitted by **GoSree** (`gosree`). Each rule includes its formal semantics, concurrency rationale, failing code examples, and idiomatic remediation patterns.

---

## Diagnostic Code Summary

| Code | Subsystem | Description | Default Severity |
|---|---|---|---|
| **[SAFE001](#safe001-unsafe-operations-ban)** | `passes/unsafeban` | Use of `unsafe`, assembly, cgo, or unsafe reflection | Error |
| **[SAFE002](#safe002-unowned-value-crossing-boundary)** | `passes/ownership` | Unowned memory crossing to another goroutine | Error |
| **[SAFE003](#safe003-use-after-move)** | `passes/ownership` | Value used after ownership was moved | Error |
| **[SAFE004](#safe004-moved-value-escapes-elsewhere)** | `passes/ownership` | Moved value also escapes into globals, returns, or callees | Error |
| **[SAFE005](#safe005-deep-immutability-violation)** | `passes/ownership` | Immutability invariant violated on `//gocap:imm` type | Error |
| **[SAFE006](#safe006-package-global-mutation)** | `passes/ownership` | Package variable mutated outside `init()` without mutex guard | Error |
| **[SAFE007](#safe007-malformed-directive)** | `internal/annot` | Malformed, mispositioned, or unresolvable directive | Error |
| **[SAFE008](#safe008-self-racing-handler-writes)** | `passes/ownership` | HTTP handler writes shared receiver or captured memory | Error |
| **[SAFE009](#safe009-guarded-memory-access-violation)** | `passes/ownership` | Guarded memory accessed without holding required mutex | Error |
| **[BORROW001](#borrow001-second-class-borrow-storage-violation)** | `passes/borrow` | Second-class borrow stored in struct field or package global | Error |
| **[BORROW002](#borrow002-conflicting-borrows)** | `passes/borrow` | Conflicting simultaneous mutable or aliased borrows (NLL) | Error |
| **[BORROW003](#borrow003-owner-mutation-during-active-borrow)** | `passes/borrow` | Owner mutated or moved while active borrow exists | Error |
| **[BORROW004](#borrow004-dangling-stack-reference-return)** | `passes/borrow` | Function returns reference to local stack allocation | Error |
| **[CAP003](#cap003-stack-scoped-borrow-concurrency-escape)** | `passes/ownership` | Stack-scoped borrow crosses goroutine or channel boundary | Error |

---

## SAFE001: Unsafe Operations Ban

### Semantics
`SAFE001` flags any operation that bypasses Go's type or memory safety:
- Any symbol from package `unsafe` (`unsafe.Pointer`, `unsafe.Add`, `unsafe.Slice`, `unsafe.String`, etc.), including when imported via dot-import (`. "unsafe"`).
- Cgo invocations (`import "C"`).
- Compiler directives that alter symbol linkage (`//go:linkname`).
- Assembly functions declared without a Go body (`func asmFunc()`).
- Unsafe reflection APIs (`reflect.Value.UnsafePointer`, `reflect.Value.UnsafeAddr`, `reflect.NewAt`, `reflect.Value.Set*`).

Unsafe memory operations bypass static capability guarantees and permit arbitrary memory corruption and type confusion.

### Failing Example
```go
package example

import "unsafe"

func Cast(p *int) *float64 {
    // SAFE001: use of unsafe.Pointer
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
A value crossing to another goroutine (via `go f(x)`, channel send `ch <- x`, or call to a parameter marked `//gocap:iso`) must be **uniquely owned**. A value is not owned if it:
- Aliases an incoming parameter of the caller.
- Aliases a package-level global variable.
- Aliases a return value that the callee may still reference.

### Failing Example
```go
package example

type Job struct{ ID int }

func Forward(ch chan *Job, j *Job) {
    // SAFE002: j is sent on a channel but is not owned here:
    // the caller still holds it (mark parameter //gocap:iso to take ownership)
    ch <- j
}
```

### Passing Remediation
```go
package example

//gocap:iso j
func Forward(ch chan *Job, j *Job) {
    // Parameter j is uniquely owned by Forward; caller transfers ownership
    ch <- j
}
```

---

## SAFE003: Use After Move

### Semantics
An owned value transferred to another goroutine (via `go`, channel send, or `//gocap:iso` call) has moved. Any subsequent read, write, or call on that value in the sender is rejected as use-after-move.

> [!NOTE]
> **Diagnostic Category: `SAFE003-forkjoin`**
> Emitted with `Diagnostic.Category = "SAFE003-forkjoin"` when a use occurs after a synchronization operation (`sync.WaitGroup.Wait`, channel receive, or mutex unlock) that fails to meet all formal requirements of a [join point](CONCURRENCY_MODEL.md#3-fork-join-synchronization-recovery). The emitted diagnostic message retains the canonical `SAFE003: ` prefix (e.g., `SAFE003: <var> is used after synchronization that did not prove ownership restored`).

### Failing Example
```go
package example

type Config struct{ Port int }

func RunWorker(ch chan *Config) {
    cfg := &Config{Port: 8080}
    ch <- cfg       // cfg moved to receiver
    println(cfg.Port) // SAFE003: cfg is used after it was sent on a channel
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
    cache = item    // SAFE004: item is sent on a channel but also escapes: stored in package variable
    return item     // SAFE004: also escapes via return
}
```

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
Types declared `//gocap:imm` (or `//safe:imm`) must be deeply immutable:
- Struct fields must only contain pointer-free primitive types, strings, channels, or pointers to other verified `//gocap:imm` types.
- Slices, maps, and interface fields are disallowed unless guarded by `//safe:guarded_by`.
- Values of an `imm` type cannot be mutated after their composite literal initialization.

### Failing Example
```go
package example

//gocap:imm
type ServerConfig struct {
    Host string
    Port int
    Tags []string // SAFE005: struct field Tags has mutable type []string
}
```

### Passing Remediation
```go
package example

//gocap:imm
type ServerConfig struct {
    Host string
    Port int
    // Pointer-free and string fields only
}
```

---

## SAFE006: Package Global Mutation

### Semantics
Package-level global variables must not be mutated outside package `init()` functions unless protected by a declared mutex guard (`//safe:guarded_by <mu>`). The diagnostic is reported once at the global variable's declaration, with all mutation sites listed as related positions.

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
var counter int //safe:guarded_by mu

func Increment() {
    mu.Lock()
    counter++
    mu.Unlock()
}
```

---

## SAFE007: Malformed Directive

### Semantics
Emitted when a `//gocap:...` or `//safe:...` directive is syntactically invalid, misplaced, or refers to nonexistent symbols:
- Guarded-by referencing a nonexistent or exported mutex.
- Guarded-by target variable is not in scope.
- Directive attached to an unsupported AST node.

### Failing Example
```go
package example

import "sync"

type Store struct {
    // SAFE007: mutex mu_nonexistent does not exist in struct Store
    data map[string]int //safe:guarded_by mu_nonexistent
}
```

### Passing Remediation
```go
package example

import "sync"

type Store struct {
    mu   sync.Mutex
    data map[string]int //safe:guarded_by mu
}
```

---

## SAFE008: Self-Racing Handler Writes

### Semantics
HTTP request handlers (`net/http.Handler` or `http.HandlerFunc`) run concurrently across incoming HTTP requests. Any write to memory shared across handler invocations (the handler struct's receiver fields or variables captured by the handler closure) is flagged as `SAFE008` unless guarded by `//safe:guarded_by`.

### Failing Example
```go
package example

import "net/http"

type MetricsHandler struct {
    requestCount int
}

func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // SAFE008: handler writes shared receiver field requestCount
    h.requestCount++
    w.Write([]byte("OK"))
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
    requestCount int //safe:guarded_by mu
}

func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    h.mu.Lock()
    h.requestCount++
    h.mu.Unlock()
    w.Write([]byte("OK"))
}
```

---

## SAFE009: Guarded Memory Access Violation

### Semantics
Memory marked with `//safe:guarded_by <mu>` may only be accessed while `<mu>` is held:
- Reads require at least `mu.RLock()` (or `mu.Lock()`).
- Writes require `mu.Lock()`.
- Functions marked `//safe:holds <mu>` must be invoked with `<mu>` held.
- Unlocked references to non-shareable guarded memory must not escape the critical section.

### Failing Example
```go
package example

import "sync"

type SafeMap struct {
    mu   sync.RWMutex
    data map[string]int //safe:guarded_by mu
}

func (s *SafeMap) Get(key string) int {
    // SAFE009: field data is accessed without holding s.mu
    return s.data[key]
}
```

### Passing Remediation
```go
package example

import "sync"

type SafeMap struct {
    mu   sync.RWMutex
    data map[string]int //safe:guarded_by mu
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
Second-class borrows (`&mut T`, `&T`, `cap.Mut[T]`, `cap.Ref[T]`, `//gocap:mub`, `//gocap:rob`) are strictly activation-frame scoped. They cannot be stored into:
- Struct fields (unless the struct is a transient stack-scoped tuple that never escapes).
- Package-level global variables.
- Slices or maps on the heap.

### Failing Example
```go
package example

type Holder struct {
    // BORROW001: struct field 'ptr' cannot have second-class borrow type: borrows are second-class and cannot be stored in struct fields
    ptr *int //gocap:rob
}

var globalRef *int //gocap:mub // BORROW001: package variable 'globalRef' cannot store second-class borrow
```

### Passing Remediation
```go
package example

// Borrows passed directly as function arguments instead of stored in structs
func Process(val *int /*gocap:rob*/) int {
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
    b1 := &x //gocap:mut
    b2 := &x //gocap:mut // BORROW002: cannot borrow 'x' as mutable more than once at a time
    *b1 = 20
    *b2 = 30
}
```

### Passing Remediation
```go
package example

func Sequential() {
    x := 10
    b1 := &x //gocap:mut
    *b1 = 20 // Last use of b1 (NLL lifetime expires here)
    
    b2 := &x //gocap:mut // OK: b1 is no longer live
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
    b := &x //gocap:mut
    x = 200 // BORROW003: cannot assign to 'x' because it is borrowed at line 453
    *b = 300
}
```

### Passing Remediation
```go
package example

func ValidOrder() {
    x := 100
    b := &x //gocap:mut
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

### Failing Example
```go
package example

func Escape(ch chan *int, b *int /*gocap:mub*/) {
    // CAP003: stack-scoped borrow 'b' (\mub) cannot cross goroutine boundary:
    // cannot be sent on a channel
    ch <- b
}
```

### Passing Remediation
```go
package example

// Transfer owned isolated value instead of a stack borrow
//gocap:iso val
func Transfer(ch chan *int, val *int) {
    ch <- val // OK: val is uniquely owned (\iso)
}
```
