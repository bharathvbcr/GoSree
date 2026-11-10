# GoSree (`gosree` / `gocap`) — Second-Class Borrows & Non-Lexical Lifetimes (NLL)

[← README](../README.md) | [Architecture](ARCHITECTURE.md) | [Rules](RULES.md) | [Directives](DIRECTIVES.md) | [Borrow & NLL](BORROW_CHECKING_AND_NLL.md) | [Concurrency](CONCURRENCY_MODEL.md) | [Error Discipline](ERROR_DISCIPLINE.md) | [Tooling](TOOLING_AND_ERASURE.md)

---

A central innovation in **GoSree** (`gosree`) is bringing Rust-grade memory safety and borrow checking to Go **without requiring explicit lifetime parameters** (such as `'a`, `'b`, or `T: 'a`). 

This document explains the theory of second-class borrows, the Non-Lexical Lifetime (NLL) analysis on SSA intermediate representation, storage constraints, and the transient stack struct exception.

---

## 1. Why Second-Class Borrows?

In Rust, references (`&T` and `&mut T`) are **first-class values**: they can be placed in struct fields, returned across function boundaries, and stored in collections. However, to guarantee memory safety without a garbage collector, Rust requires every struct, function signature, and trait holding a reference to declare explicit lifetime parameters:

```rust
// Rust: explicit lifetime annotations required
struct BufferView<'a> {
    data: &'a [u8],
}

fn longest<'a>(x: &'a str, y: &'a str) -> &'a str { ... }
```

In Go, garbage collection already guarantees that memory is never accessed after being freed (no use-after-free or dangling heap pointers). Go programmers do not want or need complex lifetime syntax.

`gocap` solves this by treating borrowed references as **second-class values**:
- A second-class borrow may be passed into functions and used across local operations.
- A second-class borrow **cannot be stored into first-class heap memory** (struct fields on the heap, package globals, or sent across channels).
- Because borrows never escape the stack frame, **all lifetimes are local**. The compiler can infer all borrow expirations automatically with zero syntactic annotations!

```mermaid
flowchart TD
    subgraph RustModel ["Rust: First-Class References"]
        R1["References stored in structs & heap"] --> R2["Lifetimes escape local frame"]
        R2 --> R3["Requires explicit lifetime parameters: 'a, 'b"]
    end

    subgraph GocapModel ["gocap: Second-Class Borrows"]
        G1["Borrows confined to stack frame"] --> G2["Storage banned in heap structs & globals"]
        G2 --> G3["NLL infers lifetimes automatically\nZero lifetime annotations needed!"]
    end
```

---

## 2. Storage Constraints (`BORROW001`)

The storage checker in [`passes/borrow/storage.go`](../passes/borrow/storage.go) enforces that second-class borrows never outlive their activation frame.

```mermaid
flowchart LR
    Borrow["Second-Class Borrow\n(&mut T, cap.Mut[T])"]
    
    Borrow -.->|Store into| Field["Struct Field\n(Heap or Long-Lived)"]
    Field --> Err1["❌ BORROW001 Violation"]
    
    Borrow -.->|Store into| Global["Package-Level Global"]
    Global --> Err2["❌ BORROW001 Violation"]
    
    Borrow -.->|Put into| StackTuple["Transient Stack Struct\n(Never Escapes Frame)"]
    StackTuple --> OK["✅ Permitted (Stack Tuple)"]
```

### The Transient Stack Struct Exception
Often in Go, developers package multiple values or pointers into a temporary struct on the stack (for instance, to pass a pair of values or iterate):

```go
type StackTuple struct {
    p *int
}

func Process() {
    x := 10
    // b is an active second-class mutable borrow
    b := &x //gocap:mut
    
    // Storing a borrow into a struct field would normally trigger BORROW001.
    // However, StackTuple is allocated on the stack and never escapes:
    t := StackTuple{p: b}
    *t.p = 20 // OK: Transient stack struct exception
}
```

`gocap`'s storage checker performs escape analysis on local structs. If a struct containing borrow-like pointers never leaves the current activation frame, it is treated as a safe transient stack tuple (`passes/borrow/storage.go:1705`). The same struct is rejected as soon as it is heap-allocated or leaves the frame, for example `h := new(StackTuple); h.p = b` reports `BORROW001: borrow stored in struct field 'p' escapes stack frame` (`testdata/src/challenger_m4_coverage/coverage.go:454-456`).

### What else counts as storage
Beyond direct stores, the checker follows a borrow through calls, so these are also `BORROW001` (`testdata/src/marker_soundness/marker_soundness.go`, Tests 32 and 35):
- passing a borrow to an unannotated helper whose body stores that parameter in a package variable (`calleeStoresParamInGlobal`, `storage.go:1636`);
- laundering a borrow through a helper that returns its parameter: `globalIntPtr = idIntPtr(b)` still reports `package variable 'globalIntPtr' cannot store second-class borrow` (`calleeReturnsParam`, `storage.go:1660`);
- sending a borrow, or a value derived from one through a phi, on a channel inside a `select` case, which reports `channel send cannot store second-class borrow`;
- calling the generic `cap.Borrow*` helpers: instantiated names such as `BorrowMut[int]` are matched by their base name.

A callee parameter that is itself annotated `mub`/`rob` is exempt from the callee-body checks, since the callee is bound by the same rules.

---

## 3. Non-Lexical Lifetimes (NLL) in SSA

In early versions of Rust, a borrow's lifetime lasted until the end of its enclosing syntactic block (`{ ... }`). This frequently caused false positive errors when two sequential operations touched the same variable.

`gocap` implements **Non-Lexical Lifetimes (NLL)** directly on Go's Static Single Assignment (SSA) form ([`passes/borrow/check.go`](../passes/borrow/check.go)).

### Lexical Scope vs. Non-Lexical Lifetime

```mermaid
sequenceDiagram
    autonumber
    participant Code as Control Flow (SSA)
    participant B1 as Borrow 1 (b1)
    participant B2 as Borrow 2 (b2)

    Code->>B1: b1 := &x (Borrow 1 starts)
    Code->>B1: *b1 = 42 (LAST USE of b1)
    Note over B1: NLL Kill Point: b1 lifetime expires here!
    Note over Code: Under Lexical Scoping, b1 would live until end of block!
    Code->>B2: b2 := &x (Borrow 2 starts - ACCEPTED under NLL)
    Code->>B2: *b2 = 100 (b2 used)
```

In `gocap`, a borrow's live range is computed as the set of SSA instructions from which the borrow (or any value derived from it) can reach a future read or write:

$$\mathcal{Live}(b) = \{ i \in \text{SSA Instructions} \mid \exists \text{ path from } i \text{ to a read/write of } b \}$$

Once the last instruction in $\mathcal{Live}(b)$ executes, $b$ is dead, and the underlying allocation may be borrowed again immediately.

---

## 4. Borrow Conflict Matrix (`BORROW002`)

Within overlapping live ranges, `gocap` enforces the fundamental aliasing XOR mutability invariant:

$$\text{Active}(x) \implies (\text{Read-Only}^N) \oplus (\text{Mutable}^1)$$

```mermaid
stateDiagram-v2
    [*] --> Unborrowed: Variable x declared

    Unborrowed --> ReadOnlyBorrows: b := &x (\rob)
    ReadOnlyBorrows --> ReadOnlyBorrows: r2 := &x (\rob) [Allowed: Multiple Readers]
    ReadOnlyBorrows --> Unborrowed: All \rob lifetimes expire

    Unborrowed --> MutableBorrow: b := &mut x (\mub)
    MutableBorrow --> Unborrowed: \mub lifetime expires

    ReadOnlyBorrows --> Conflict: b_mut := &mut x [BORROW002]
    MutableBorrow --> Conflict: b2_mut := &mut x [BORROW002]
    MutableBorrow --> Conflict: b_ref := &x [BORROW002]
```

| Existing Active Borrow | Attempted New Borrow | Allowed? | Diagnostic |
|---|---|---|---|
| None | `&T` (`\rob`) | **Yes** | — |
| None | `&mut T` (`\mub`) | **Yes** | — |
| `&T` (`\rob`) | `&T` (`\rob`) | **Yes** (Multiple simultaneous readers) | — |
| `&T` (`\rob`) | `&mut T` (`\mub`) | **No** (Cannot mutate while read-only borrow active) | `BORROW002` |
| `&mut T` (`\mub`) | `&T` (`\rob`) | **No** (Cannot alias active mutable borrow) | `BORROW002` |
| `&mut T` (`\mub`) | `&mut T` (`\mub`) | **No** (Exclusive mutable ownership required) | `BORROW002` |

The owner of a borrow is resolved through aliases, so a conflict is not hidden by an intermediate name. The resolver follows up to 8 hops of pointer copies (`q := p`), sub-slices (`t := s[1:]`) and type assertions (`c := i.(*T)`), and also looks through `*p`, `s[i:j]` and composite literals (`check.go:481-487`, `check.go:515-612`):

```go
x := 10
p := &x
q := p
b1 := cap.BorrowMut(p)
b2 := cap.BorrowMut(q) // BORROW002: cannot borrow 'x' as mutable more than once at a time
```
(`testdata/src/marker_soundness/marker_soundness.go:295-317`, Test 28.) Parameters are also matched by annotation: passing an existing read-only borrow, or an alias of an owner that has a live read-only borrow, to a `mub` parameter is reported as `cannot borrow 'x' as mutable because it is also borrowed as read-only`, and the reverse for `rob` parameters (Tests 29 and 33, lines 319 and 386).

---

## 5. Owner Invalidation (`BORROW003`)

While a borrow is active, the original owner cannot be modified or moved:

```go
func InvalidationDemo() {
    x := 10
    b := &x //gocap:mut
    
    // Direct mutation of owner while borrow b is active
    x = 20  // BORROW003: cannot assign to 'x' because it is borrowed at line 154
    
    *b = 30 // Use of borrow b
}
```

```mermaid
flowchart TD
    A["x := 10 (Owner)"] --> B["b := &mut x (Active Borrow Begins)"]
    B --> C["x = 20 (Direct write to owner)"]
    C --> D["*b = 30 (Borrow accessed)"]

    C -.->|Invalidation Conflict| B
    style C fill:#ffcccc,stroke:#ff0000,stroke-width:2px
```

The same rule covers more than plain assignment. Each of these while a borrow of `x` or `s` is live is `BORROW003` (`testdata/src/marker_soundness/marker_soundness.go`, Tests 30 and 31):
- `(*p)++` where `p` is an earlier alias of `x`, and `for x = range 3`;
- the built-ins `copy(s[:], src)`, `clear(s[:])`, `delete`, and `append(s[:1], v)` (`append` only when its first argument is a slice expression; `s = append(s, v)` is an ordinary assignment to `s`);
- calling a closure that mutates the owner, whether it was bound to a variable (`m := func() { *p = 77 }`), stored in a struct field (`box.f()`), or passed on (`callClosureHelper(m)`), reported as `cannot assign to 'x' (via closure 'm') because it is borrowed`;
- passing the owner to an `//gocap:iso` parameter (`cannot move 'x' because it is borrowed`), or passing an alias of the owner to a function that is not `mub`/`rob`-annotated (`cannot borrow or take address of 'x' while it is borrowed`).

### Writing Through a Read-Only Borrow
A read-only borrow cannot be used to mutate its target. Assigning through it is also `BORROW003`, with a distinct message (`check.go:1153-1157`, `check.go:1200-1205`, Test 34, `marker_soundness.go:400-413`):

```go
x := 10
r := cap.Borrow(&x)
*r = 99      // BORROW003: cannot assign through read-only borrow 'r' (borrowed at line N)
(*r)++       // same
mutatePtr(r) // same, because mutatePtr stores through its parameter (calleeMutatesParam)

c := CounterVal{n: 1}
cr := cap.Borrow(&c)
cr.n = 2     // same
```

Because `b` is live across the assignment `x = 20`, modifying `x` invalidates the assumptions of `b`. Reordering the code so that the assignment happens after `b`'s last use resolves the diagnostic cleanly:

```go
func ValidSequence() {
    x := 10
    b := &x //gocap:mut
    *b = 30 // b's lifetime ends here
    
    x = 20  // OK: no active borrow
}
```

---

## 6. Dangling Stack Reference Detection (`BORROW004`)

When a function executes, local variables declared inside it are allocated on its stack frame. Returning a reference or pointer to a local variable means the pointer will point to deallocated or overwritten stack memory once the frame unwinds.

```mermaid
sequenceDiagram
    participant Caller
    participant StackFrame as Callee Stack Frame

    Caller->>StackFrame: Call Dangling()
    StackFrame->>StackFrame: Allocate local x := 42
    StackFrame-->>Caller: Return &x (Stack Pointer)
    Note over StackFrame: Stack Frame Deallocated!
    Note over Caller: Caller dereferences invalid pointer! (BORROW004)
```

In [`passes/borrow/check.go`](../passes/borrow/check.go), `gocap` tracks the allocation origin of all returned SSA values:
```go
func Dangling() *int {
    x := 42
    return &x // BORROW004: cannot return reference to local stack variable 'x'
}
```
If the allocation is rooted in a local stack variable, `BORROW004` is reported immediately. Heap-allocated memory (`new(T)` or `&T{}` in an escaping context) is recognized as safe.

The origin trace sees through `cap.Borrow*`/`cap.Move` and through user helpers that simply return one of their parameters (`calleeReturnedParamIndex`, `check.go:1747`; helpers in package `cap` are excluded). So laundering a local address through an identity function is still caught:

```go
func idIntPtr(p *int) *int { return p }

func Laundered() *int {
    localVar := 42
    return idIntPtr(&localVar) // BORROW004: cannot return reference to local stack variable 'localVar'
}
```
(`testdata/src/marker_soundness/marker_soundness.go:415-424`, Test 35.) A pointer-chasing loop (`p = p.next` on a cycle) does not hang or overflow the trace and still reports the returned `&root` (Test 27, line 283).
