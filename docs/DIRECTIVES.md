# GoSree (`gosree` / `gocap`) Directives & Capability Markers Reference

[← README](../README.md) | [Architecture](ARCHITECTURE.md) | [Rules](RULES.md) | [Directives](DIRECTIVES.md) | [Borrow & NLL](BORROW_CHECKING_AND_NLL.md) | [Concurrency](CONCURRENCY_MODEL.md) | [Error Discipline](ERROR_DISCIPLINE.md) | [Tooling](TOOLING_AND_ERASURE.md)

---

**GoSree** (`gosree`) offers two syntactic mechanisms for expressing safety capabilities and second-class borrows in Go code:
1. **Comment Directives (`//gosree:...`, `//sree:...`, `//gocap:...`, and `//safe:...`)**: Native zero-cost annotations embedded directly in comments. Recognized by GoSree analyzers, invisible to the Go compiler.
2. **Generic Marker Types (`cap.Mut[T]`, `cap.Ref[T]`, `cap.Iso[T]`, `cap.Imm[T]`)**: Type-level expressions provided by package `github.com/bharathvbcr/gocap/cap`. The marker scanner also recognizes the path `github.com/bharathvbcr/gosree/cap` and any package named `cap` or whose path ends in `/cap` (`internal/annot/annot.go:731`), but this repository ships only the `gocap/cap` package.

---

## 1. Syntax & Lexing Rules

All GoSree comment directives adhere to standard Go comment conventions:
- **No whitespace** between the leading slashes and the directive prefix (`//gosree:`, `//sree:`, `//gocap:`, or `//safe:`).
- Directives may appear as line comments (`//gosree:...`) or inline block comments (`/*gosree:...*/`).
- **All four prefixes are equivalent and accept every verb.** There is no precedence between them: each comment is parsed independently, and the prefix used only changes how diagnostics echo it (for example `//gocap:imm` vs `//safe:imm` in `SAFE005`; some hints always print `//safe:` or `//gocap:`).
- A trailing `// note` after a directive is commentary, not arguments. The verb ends at any whitespace.
- An unknown verb, or a near-miss such as `// gocap:iso p`, `//Safe:holds mu` or `/* gocap:mut */`, is never silently dropped: it is reported as `SAFE007`. The near-miss scan checks only the `gocap:` and `safe:` spellings (`internal/annot/nearmiss.go`), so a misspelled `//gosree:`/`//sree:` is caught only as an unknown verb.

```mermaid
flowchart TD
    Comment["Comment: //gocap:mub dst, src\nor //safe:guarded_by mu"] --> Scanner{"Lexical Scanner\n(internal/annot)"}
    Scanner --> MatchPrefix{"Prefix Match?\n(//gosree: //sree: //gocap: //safe:)"}
    MatchPrefix -- Matched --> StripLead["Extract Verb & Arguments\n(Both prefixes accept all verbs)"]
    StripLead --> ParseVerb["Parse Verb:\nmub, rob, mut, ref, iso, imm, guarded_by, holds, unsafe"]
    ParseVerb --> Attach["Resolve AST Attachment\n(Doc, Preceding Line, Trailing Line)"]
    Attach --> Index["Populate annot.Index"]
```

### 1.1 Attachment Resolution
A directive attaches to AST nodes based on its position:
1. **Doc Comments:** Comments in a `FuncDecl` or `TypeDecl` doc comment attach to that declaration.
2. **Preceding Line:** A comment placed on the line immediately preceding a statement, variable specification, or struct field attaches to it.
3. **Trailing Same Line:** A comment placed at the end of the line attaches to the statement, field, or specification on that same line.

---

## 2. Capability Directives Reference

### 2.1 `//gocap:iso` / `//safe:iso` (Isolated Ownership)

Declares that a function parameter receives **isolated, unique ownership** (`\iso`).

```go
//gocap:iso job
func Worker(job *Job) {
    // Worker now holds exclusive ownership of job.
    // Caller cannot access job after this call.
}
```

- **Scope:** Function and method declarations (receiver included); parameters may also be typed `cap.Iso[T]`.
- **Multiple Arguments:** `//gocap:iso p, q` marks multiple parameters.
- **Caller Effect:** The caller's argument binding is moved; subsequent caller access triggers `SAFE003`.
- **Callee Effect:** The callee can move the value onto a channel or into a spawned goroutine without triggering `SAFE002`.

---

### 2.2 `//gocap:imm` / `//safe:imm` (Deep Immutability)

Declares that a struct type is **deeply immutable** (`\imm`).

```go
//gocap:imm
type ServerConfig struct {
    Host string
    Port int
}
```

- **Scope:** A defined struct `type` declaration, with no arguments (`//gocap:imm` takes none; repeating it is `SAFE007`). `type X cap.Imm[...]` is also recorded as an imm candidate.
- **Verification (`SAFE005`):**
  - Every field must be unexported.
  - Fields must be pointer-free types, strings, channels of shareable types, or pointers to other verified `//gocap:imm` types.
  - Slices, maps, and interfaces are forbidden unless the field is `//safe:guarded_by`.
- **Effect:** Pointers to `\imm` types can be shared across arbitrary goroutines and channels without triggering ownership moves (`SAFE003`) or invalidations.

---

### 2.3 `//gocap:mub` / `//gocap:mut` (Mutable Second-Class Borrow)

Declares a **mutable second-class borrow** (`\mub` / `&mut T`).

#### On Function Parameters:
```go
//gocap:mub val
func Increment(val *int) {
    *val++
}
```
*Inline form:*
```go
func Increment(val *int /*gocap:mub*/) {
    *val++
}
```

#### On Local Variable Bindings:
```go
x := 10
b := &x //gocap:mut
*b = 20
```

- **Invariants:**
  - Cannot be stored in struct fields or globals (`BORROW001`).
  - Cannot coexist with active mutable or read-only borrows of the same memory (`BORROW002`).
  - Cannot escape across channels or goroutines (`CAP003`).

---

### 2.4 `//gocap:rob` / `//gocap:ref` (Read-Only Second-Class Borrow)

Declares a **read-only second-class borrow** (`\rob` / `&T`).

#### On Function Parameters:
```go
//gocap:rob val
func Read(val *int) int {
    return *val
}
```
*Inline form:*
```go
func Read(val *int /*gocap:rob*/) int {
    return *val
}
```

#### On Local Variable Bindings:
```go
x := 10
r := &x //gocap:rob
println(*r)
```

- **Invariants:**
  - Multiple simultaneous `\rob` borrows are permitted.
  - No `\mub` borrow can be active simultaneously (`BORROW002`).
  - Cannot escape across goroutines or channels (`CAP003`).

---

### 2.5 `//safe:guarded_by <mu>` (Mutex-Protected Memory)

Declares that a field, package global, or local variable is protected by a named mutex (`sync.Mutex` or `sync.RWMutex`).

```go
type SafeCounter struct {
    mu    sync.Mutex
    count int //safe:guarded_by mu
}
```

- **Scope:** Unexported struct fields, unexported package variables, local variables. The mutex must be a sibling field, a package variable, or a local in scope, of type `sync.Mutex`/`sync.RWMutex`. Guarding an exported name, guarding the mutex itself, or a second guard on one variable is `SAFE007`.
- **Several names on one declaration:** extra names after the mutex (`//safe:guarded_by mu a`) restrict the guard to those names.
- **Access Discipline (`SAFE009`):**
  - Read access requires `mu.Lock()` or `mu.RLock()`.
  - Write access requires `mu.Lock()`.
  - Global variable writes protected by `guarded_by` are exempt from `SAFE006`.

---

### 2.6 `//safe:holds <mu>` (Callee Mutex Precondition)

Declares that callers of a method or function must already hold the specified mutex upon invocation. The argument must be a single identifier naming a field of the receiver's struct (or a package-level mutex), not a selector path.

```go
//safe:holds mu
func (s *SafeCounter) incrementLocked() {
    s.count++ // OK: s.mu verified held by caller
}
```

- **Scope:** Method and function declarations.
- **Enforcement (`SAFE009`):** Forward must-analysis ensures all call sites hold `mu` along all incoming CFG paths.

---

### 2.7 `//gocap:unsafe <reason>` (Scoped Escape Hatch)

Selectively suppresses diagnostics for the attached statement, spec, or function declaration.

```go
//gocap:unsafe legacy interop with external library
externalLibrary.UnsafeCall(ptr)
```

- **Requirement:** A reason with at least one letter or digit. A missing or invisible-only reason, an unattached hatch, a hatch on the line of an opening brace, or an exact duplicate scope is `SAFE007`.
- **Coverage:** On its own line above a statement, or in a declaration's doc comment, it covers the whole statement or declaration. At the end of a code line it covers that line's statement except the bodies of multi-line blocks in it.
- **Which passes honor it:** `unsafeban`, `ownership`, `borrow` (`passes/borrow/check.go:184`, `storage.go:63`) and `errcheck` (mutes `ERR001`-`ERR005`, `passes/errcheck/errcheck.go`). Note `borrow` only asks `Suppressed` and never calls `Mute`, so a hatch that only suppresses borrow findings would count as stale. All four prefixes work (`errbasic` testdata uses `//gocap:unsafe` and `//gosree:unsafe`).
- **Auditability:** `cmd/gocap-corpus` reports a hatch ratio (suppressed transfers over all transfers).
- **Stale Detection:** The `safestale` analyzer (`internal/annot/stale`) reports a hatch that mutes nothing as `SAFE007`, but neither `cmd/gosree` nor `cmd/gocap` registers it, so shipped binaries do not emit that diagnostic. The ownership pass still counts such scopes in its stats (`UnsafeStale`).

---

## 3. Generic Marker Types (`github.com/bharathvbcr/gocap/cap`)

For developers who prefer explicit type annotations over comment directives, `gocap` provides the `cap` package:

```mermaid
flowchart TD
    Raw["*T (Standard Go Pointer)"]
    
    Raw --- Mut["cap.Mut[T] = *T\n(Mutable borrow &mut T / \\mub)"]
    Raw --- Ref["cap.Ref[T] = *T\n(Read-only borrow &T / \\rob)"]
    Raw --- Iso["cap.Iso[T] = *T\n(Isolated unique owner \\iso)"]
    Raw --- Imm["cap.Imm[T] = *T\n(Deeply immutable \\imm)"]
```

### 3.1 Type Definitions
```go
package cap

// Mut represents a second-class mutable borrow (&mut T / \mub)
type Mut[T any] = *T

// Ref represents a second-class read-only borrow (&T / \rob)
type Ref[T any] = *T

// Iso represents an isolated, uniquely-owned pointer reference (\iso *T)
type Iso[T any] = *T

// Imm represents a deeply immutable pointer reference (\imm *T)
type Imm[T any] = *T
```

`cap.Mub` and `cap.Rob` are additional aliases of `Mut` and `Ref` (`cap/cap.go`).

### 3.2 Intrinsics & Helper Expressions
```go
// Explicitly transfers ownership of an isolated value
func Move[T any](x T) T { return x }

// Explicitly constructs a mutable borrow (BorrowMub is an alias)
func BorrowMut[T any](x *T) Mut[T] { return x }
func BorrowMub[T any](x *T) Mub[T] { return x }

// Explicitly constructs a read-only borrow (BorrowRef and BorrowRob are aliases)
func Borrow[T any](x *T) Ref[T]    { return x }
func BorrowRef[T any](x *T) Ref[T] { return x }
func BorrowRob[T any](x *T) Rob[T] { return x }
```

### 3.3 Usage Example
```go
package main

import "github.com/bharathvbcr/gocap/cap"

type Item struct{ Count int }

func Update(item cap.Mut[Item]) {
    item.Count++
}

func main() {
    x := Item{Count: 1}
    b := cap.BorrowMut(&x)
    Update(b)
}
```

### 3.4 Automatic Erasure
When targeting toolchains without generic type alias support or legacy Go versions, `cmd/gocap-erase` converts `cap.Mut[Item]` back to `*Item` and strips the import:

```bash
gocap-erase -w ./...
```
