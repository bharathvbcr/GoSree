# GoSree (`gosree` / `gocap`) Architecture Specification

[← README](../README.md) | [Architecture](ARCHITECTURE.md) | [Rules](RULES.md) | [Directives](DIRECTIVES.md) | [Borrow & NLL](BORROW_CHECKING_AND_NLL.md) | [Concurrency](CONCURRENCY_MODEL.md) | [Tooling](TOOLING_AND_ERASURE.md)

---

**GoSree** (`gosree`, formerly `gocap`) is a static analysis suite for ordinary Go that enforces Rust-style memory safety, reference capabilities, second-class borrow checking with Non-Lexical Lifetimes (NLL), and mutex lock discipline, with zero runtime cost and clean erasure to standard Go code.

This document describes the architectural subsystems, static analysis pipelines, mathematical formulations, and data structures implemented in `gocap`.

---

## 1. High-Level System Architecture

`gocap` operates as a set of modular analyzers built on the official Go analysis framework ([`golang.org/x/tools/go/analysis`](file:///Users/bharath/Code/devtools/gocap/go.mod#L5)) and SSA intermediate representation ([`golang.org/x/tools/go/ssa`](file:///Users/bharath/Code/devtools/gocap/passes/borrow/check.go#L11)).

```mermaid
flowchart TD
    subgraph Input ["Go Source Files (.go)"]
        Src["Go Source Code\nwith //gocap: directives\nor cap.Mut[T] markers"]
    end

    subgraph FrontEnd ["Front-End & AST/SSA Ingestion"]
        Parser["go/parser & go/types"]
        Annot["Directive Parser\n(internal/annot)\nIndex construction"]
        SSA["SSA IR Builder\n(golang.org/x/tools/go/ssa)"]
    end

    subgraph Passes ["gocap Static Analysis Passes"]
        UnsafeBan["Unsafe Ban Pass\n(passes/unsafeban)\nSAFE001"]
        Borrow["Second-Class Borrow Checker\n(passes/borrow)\nBORROW001 - BORROW004"]
        Ownership["Ownership & Capabilities Pass\n(passes/ownership)\nSAFE002 - SAFE009, CAP003"]
    end

    subgraph CoreEngines ["Core Analysis Engines"]
        Steensgaard["Steensgaard Alias Graph\n(passes/ownership/graph.go)"]
        NLL["Non-Lexical Lifetimes (NLL)\n(passes/borrow/check.go)"]
        Locksets["Forward Must-Lockset\n(passes/ownership/lockset.go)"]
        ForkJoin["Fork-Join Join Points\n(passes/ownership/forkjoin.go)"]
        ImmValidator["Transitive Deep Immutability\n(passes/ownership/imm.go)"]
    end

    subgraph Tooling ["Toolchain & Drivers"]
        Driver["Multichecker Driver\n(cmd/gocap)"]
        Eraser["AST Erasure Tool\n(cmd/gocap-erase)"]
        Vet["go vet -vettool=gocap"]
    end

    Src --> Parser
    Parser --> Annot
    Parser --> SSA
    Annot --> Borrow
    Annot --> Ownership
    SSA --> Borrow
    SSA --> Ownership
    Parser --> UnsafeBan

    Borrow --> NLL
    Ownership --> Steensgaard
    Ownership --> Locksets
    Ownership --> ForkJoin
    Ownership --> ImmValidator

    UnsafeBan --> Driver
    Borrow --> Driver
    Ownership --> Driver
    Driver --> Vet
    Src --> Eraser
```

---

## 2. Pipeline Execution Sequence

When `gocap` analyzes a package, execution proceeds across strict dependency boundaries:

```mermaid
sequenceDiagram
    autonumber
    participant Driver as cmd/gocap (multichecker)
    participant Annot as internal/annot
    participant SSA as buildssa
    participant Unsafe as passes/unsafeban
    participant Borrow as passes/borrow
    participant Own as passes/ownership

    Driver->>Annot: Parse AST comments & build annot.Index
    Annot-->>Driver: Return *annot.Index
    Driver->>SSA: Construct SSA form of package
    SSA-->>Driver: Return *buildssa.SSA
    
    par Independent Initial Passes
        Driver->>Unsafe: Run SAFE001 checks on AST & imports
        Unsafe-->>Driver: Diagnostics (unsafe.Pointer, assembly, reflect)
    and Second-Class Borrow Checks
        Driver->>Borrow: Check storage constraints & SSA NLL borrows
        Borrow->>Borrow: Phase 1: Struct & global declaration checks (BORROW001)
        Borrow->>Borrow: Phase 2: Flow-sensitive SSA CFG borrow analysis (BORROW002-004)
        Borrow-->>Driver: Diagnostics (BORROW001-BORROW004)
    end

    Note over Driver,Own: passes/ownership requires passes/unsafeban (ordering dependency for stats & suppression)
    Driver->>Own: Run safeownership analysis (Requires: annot, unsafeban, buildssa)
    Own->>Own: Compute Steensgaard alias unification graph
    Own->>Own: Verify transitive deep immutability (SAFE005)
    Own->>Own: Compute CFG must-locksets (SAFE009)
    Own->>Own: Check goroutine transfers & channel sends (SAFE002-004, CAP003)
    Own->>Own: Check self-racing HTTP handlers (SAFE008)
    Own->>Own: Check fork-join synchronization points
    Own-->>Driver: Diagnostics & Summary Facts

    Driver->>Driver: Filter diagnostics with annot.Index (//gocap:unsafe)
    Driver-->>Driver: Emit final diagnostics to stderr / go vet
```

---

## 3. Subsystem Breakdown

### 3.1 Directive & Annotation Engine ([`internal/annot`](file:///Users/bharath/Code/devtools/gocap/internal/annot/annot.go#L1-L75))

The directive engine extracts safety claims, capabilities, and suppression scopes without altering Go syntax or semantics.

- **Comment Group Parsing:** Traverses [`ast.File.Comments`](file:///Users/bharath/Code/devtools/gocap/internal/annot/annot.go#L198), mapping comment tokens to AST declarations, statements, and fields.
- **Prefix Support:**
  - Modern canonical prefix: `//gocap:...`
  - Legacy backward-compatible prefix: `//safe:...`
  - Inline comment annotations: `/*gocap:mut*/`, `/*gocap:rob*/`
- **Parsed Directives:**
  - Capability parameters: `//gocap:iso p, q`, `//gocap:mub p`, `//gocap:rob p`
  - Deep immutability: `//gocap:imm`
  - Concurrency locks: `//safe:guarded_by <mu>`, `//safe:holds <mu>`
  - Local borrow bindings: `//gocap:mut`, `//gocap:rob`
  - Scoped suppressions: `//gocap:unsafe <reason>`, `//safe:unsafe <reason>`
- **Output Data Structure:** Produces an [`*annot.Index`](file:///Users/bharath/Code/devtools/gocap/internal/annot/annot.go#L71-L96) exported as an `analysis.Pass` result.

### 3.2 Second-Class Borrow Checker ([`passes/borrow`](file:///Users/bharath/Code/devtools/gocap/passes/borrow/borrow.go#L1-L36))

Implements second-class references inspired by academic formulations of second-class values (references that cannot be stored into first-class heap memory).

```mermaid
flowchart LR
    subgraph Storage ["Storage Constraint Pass (storage.go)"]
        A["AST Struct Fields"] --> B{"Contains Borrow Type?\n(cap.Mut / cap.Ref or //gocap:mut / rob)"}
        B -- Yes --> C{"Is Transient Stack Struct?"}
        C -- No --> D["Report BORROW001\n(Struct field storage)"]
        C -- Yes --> E["Permit Stack Tuple"]
        B -- No --> Pass1["OK"]
        
        G["AST Global Vars"] --> H{"Borrow Type or Directive?"}
        H -- Yes --> I["Report BORROW001\n(Global var storage)"]
        H -- No --> Pass2["OK"]
    end
```

- **`BORROW001` (Storage Violation):** Second-class borrows cannot be placed in struct fields or package globals ([`passes/borrow/storage.go`](file:///Users/bharath/Code/devtools/gocap/passes/borrow/storage.go#L17-L77)).
  - *Transient Stack Struct Exception:* Local structs allocated on the stack that never escape the current activation frame may hold second-class borrows as lightweight tuples.
- **`BORROW002` (Conflicting Borrows):** Simultaneous mutable borrows of the same memory allocation or mutable-immutable aliasing within overlapping live ranges ([`passes/borrow/check.go`](file:///Users/bharath/Code/devtools/gocap/passes/borrow/check.go#L17-L22)).
- **`BORROW003` (Owner Invalidation):** Mutation or moving of the underlying owner while an active borrow is in scope.
- **`BORROW004` (Dangling Stack Return):** Returning a pointer or borrow to a local stack-allocated variable beyond the enclosing activation frame.

### 3.3 Reference Capabilities & Concurrency Boundaries ([`passes/ownership`](file:///Users/bharath/Code/devtools/gocap/passes/ownership/ownership.go#L1-L65))

Tracks memory crossing goroutine boundaries and enforces strict reference capability invariants.

```mermaid
stateDiagram-v2
    [*] --> Unowned: Parameter / Callee / Package Var
    [*] --> Owned_Iso: Fresh Allocation (&T{}, make, new) / Channel Recv / //gocap:iso Param

    state Owned_Iso {
        [*] --> Active: Single Owner
        Active --> Moved: Channel Send (ch <- x) / go worker(x)
        Moved --> Active: Fork-Join Sync (wg.Wait / join channel)
        Active --> Borrowed: Local Borrow b := &x
        Borrowed --> Active: NLL Borrow Expired (Last Use)
    }

    state Deeply_Immutable_Imm {
        [*] --> Frozen: Verified transitive pointer-free or //gocap:imm
        Frozen --> Shared: Free goroutine & channel sharing without lock
    }

    state Stack_Scoped_Borrow {
        [*] --> StackConfined: Local activation frame
        StackConfined --> Rejected: Crosses goroutine (go / chan) [CAP003]
    }
```

- **Reference Capabilities:**
  - `\iso` (Isolated): Unique ownership root. Cannot alias foreign memory. Moving an `\iso` value over a channel or to a goroutine invalidates the sender's binding. Subsequent access is flagged as `SAFE003`.
  - `\imm` (Immutable): Transitive immutability. Structs whose fields are pointer-free, strings, channels, or pointers to `\imm` types. Can be shared freely across goroutines without invalidation (`SAFE005`).
  - `\mub` / `\rob` (Mutable / Read-Only Second-Class Borrows): Confined to the stack frame. Cannot cross goroutine boundaries via channel sends or `go` statements (`CAP003`).
- **Mutex Lockset Forward Must-Analysis (`SAFE009`):**
  - Path-sensitive forward flow analysis tracking the set of held mutexes along CFG edges ([`passes/ownership/lockset.go`](file:///Users/bharath/Code/devtools/gocap/passes/ownership/lockset.go)).
  - Enforces that memory marked `//safe:guarded_by mu` is never accessed unless `mu` is held.
  - Verifies that functions annotated `//safe:holds mu` are invoked with `mu` locked.
- **Self-Racing Handler Analysis (`SAFE008`):**
  - HTTP handlers registered with `net/http` execute concurrently across requests ([`passes/ownership/handlerwrite.go`](file:///Users/bharath/Code/devtools/gocap/passes/ownership/handlerwrite.go)).
  - Flags writes to shared receiver state or captured variables unless protected by `//safe:guarded_by`.

---

## 4. Algorithmic Formulations

### 4.1 Steensgaard Points-To & Alias Analysis

In [`passes/ownership/graph.go`](file:///Users/bharath/Code/devtools/gocap/passes/ownership/graph.go), alias relationships within each function are computed using a Steensgaard-style unification algorithm:

- Equivalence classes are represented as disjoint union-find sets with path compression:
  $$\text{find}(n) = \begin{cases} n & \text{if } n.\text{parent} = \text{nil} \\ \text{find}(n.\text{parent}) & \text{otherwise} \end{cases}$$
- For an assignment $x = y$, their pointee nodes are unified:
  $$\text{unify}(\text{pointee}(x), \text{pointee}(y))$$
- **Pointer Depths & Region Linking:** To prevent false positives when values are copied by value (e.g. `cp := *c; return &cp`), `gocap` tracks the relative pointer depth of alias links. Two regions meet at a specific pointer depth rather than unifying the entire parent structure.

### 4.2 Non-Lexical Lifetimes (NLL) on SSA CFGs

In [`passes/borrow/check.go`](file:///Users/bharath/Code/devtools/gocap/passes/borrow/check.go), lifetimes are not lexical scopes. Instead, they are defined by the forward reachability of the last SSA instruction that reads or writes the borrow:

```mermaid
flowchart TD
    Alloc["x := 42 (Owner Allocation)"] --> Borrow1["b1 := &mut x (Borrow Begins)"]
    Borrow1 --> Use1["*b1 = 100 (Last Use of b1)"]
    Use1 --> KillPoint["--- b1 LIFETIME EXPIRES (NLL Kill Point) ---"]
    KillPoint --> Borrow2["b2 := &x (New Borrow Permitted)"]
    Borrow2 --> Use2["println(*b2)"]
```

1. **Borrow Live Range:** $\mathcal{L}(b) = \{ \text{inst} \in \text{CFG} \mid b \text{ is live at inst} \}$.
2. **Conflict Rule:** Two borrows $b_1, b_2$ of the same memory allocation conflict if $\mathcal{L}(b_1) \cap \mathcal{L}(b_2) \neq \emptyset$ and at least one is mutable (`\mub`).
3. **Owner Invalidation Rule:** If an instruction mutates or moves the owner $x$ while $\text{inst} \in \mathcal{L}(b)$, `BORROW003` is emitted.

### 4.3 Mutex Lockset Forward Must-Analysis

In [`passes/ownership/lockset.go`](file:///Users/bharath/Code/devtools/gocap/passes/ownership/lockset.go), lock tracking is a forward dataflow must-analysis:

$$\mathcal{In}(B) = \bigcap_{P \in \text{pred}(B)} \mathcal{Out}(P)$$

$$\mathcal{Out}(B) = (\mathcal{In}(B) \setminus \text{Unlocked}(B)) \cup \text{Locked}(B)$$

- Mutex identity is represented by canonical SSA root paths (e.g., `s.mu`).
- `Lock()` / `RLock()` add the path to the held set.
- `Unlock()` / `RUnlock()` remove the path from the held set.
- Calls to functions in other packages are assumed to potentially release any mutexes passed to them, unless explicitly proven otherwise.

---

## 5. Concurrency Model & Fork-Join Recovery

When a goroutine is launched with moved ownership, standard Rust-style semantics would permanently transfer the value. Go programs, however, commonly employ fork-join concurrency patterns (`sync.WaitGroup` or rendezvous channels).

In [`passes/ownership/forkjoin.go`](file:///Users/bharath/Code/devtools/gocap/passes/ownership/forkjoin.go), `gocap` models fork-join recovery:

```mermaid
sequenceDiagram
    participant Parent as Parent Goroutine
    participant Child as Spawned Goroutine (go func)

    Parent->>Parent: x := &Job{} (Owner)
    Parent->>Parent: wg.Add(1)
    Parent->>Child: go func() { ... x.Work() ... wg.Done() }()
    Note over Parent: x is MOVED (SAFE003 if accessed here)
    Parent->>Parent: wg.Wait() (JOIN POINT)
    Note over Parent: x OWNERSHIP RESTORED to Parent
    Parent->>Parent: use(x) (ACCEPTED - No SAFE003)
```

### 5.1 Join Point Invariants
A join point safely restores ownership to the parent if and only if:
1. **Single Start:** The goroutine closure is started exactly once (not in a loop).
2. **Release Follows Use:** The release (`wg.Done()` or channel send/close) strictly follows the last access of the moved memory in the child.
3. **No Secondary Escape:** The child does not leak the memory into globals, return values, or secondary goroutines.
4. **Local Synchronization Object:** The `sync.WaitGroup` or channel is locally created and controlled within the parent function scope.

---

## 6. Zero-Cost Erasure Architecture

`gocap` offers two zero-cost paths to standard Go compatibility:

```mermaid
flowchart LR
    subgraph PathA ["Path A: Comment Directives (Native 0-Cost)"]
        SrcA["//gocap:iso\n//gocap:imm\n//gocap:mub\n//safe:guarded_by"] --> GoBuildA["Standard go build / go test"]
        GoBuildA --> BinA["Binary (0 bytes, 0 instructions added)"]
    end

    subgraph PathB ["Path B: Marker Types & AST Eraser"]
        SrcB["func Process(x cap.Mut[T]) {\n    b := cap.BorrowMut(x)\n}"] --> Eraser["gocap-erase\n(cmd/gocap-erase)"]
        Eraser --> ErasedSrc["func Process(x *T) {\n    b := x\n}"]
        ErasedSrc --> GoBuildB["Standard go build / go test"]
        GoBuildB --> BinB["Binary (Bit-for-bit instruction parity)"]
    end
```

- **Comment Directives:** Directives are parsed as standard Go comments. The official compiler (`go build`) ignores them completely.
- **Generic Marker Types ([`cap/cap.go`](file:///Users/bharath/Code/devtools/gocap/cap/cap.go)):**
  ```go
  type Mut[T any] = *T
  type Ref[T any] = *T
  type Iso[T any] = *T
  type Imm[T any] = *T
  ```
  Type aliases are resolved at compile time with zero indirection. Helper functions (`BorrowMut`, `Move`) are trivial identity operations that inline completely.
- **AST Eraser ([`cmd/gocap-erase`](file:///Users/bharath/Code/devtools/gocap/cmd/gocap-erase/main.go)):** Automatically transforms code using `cap.*` markers back into idiomatic raw Go pointers and removes unnecessary imports.

---

## 7. Package Dependency Structure

The following diagram illustrates internal package imports across `gocap`:

```mermaid
flowchart TD
    cmd_gocap["cmd/gocap"] --> internal_annot["internal/annot"]
    cmd_gocap --> passes_unsafeban["passes/unsafeban"]
    cmd_gocap --> passes_borrow["passes/borrow"]
    cmd_gocap --> passes_ownership["passes/ownership"]

    passes_borrow --> internal_annot
    passes_ownership --> internal_annot
    passes_ownership --> internal_kinds["internal/kinds"]
    passes_ownership --> passes_unsafeban

    subgraph passes_borrow_internals ["passes/borrow (Internal Modules)"]
        passes_borrow_storage["storage.go (BORROW001)"]
        passes_borrow_check["check.go (BORROW002-004 NLL)"]
    end
    passes_borrow -.-> passes_borrow_internals

    subgraph passes_ownership_internals ["passes/ownership (Internal Modules)"]
        passes_ownership_graph["graph.go (Points-to graph)"]
        passes_ownership_cfg["cfg.go (Goroutine transfers)"]
        passes_ownership_lockset["lockset.go (Must-locksets)"]
        passes_ownership_forkjoin["forkjoin.go (Join points)"]
        passes_ownership_imm["imm.go (Deep immutability)"]
    end
    passes_ownership -.-> passes_ownership_internals

    cmd_gocap_erase["cmd/gocap-erase"] -.->|rewrites| cap_pkg["cap"]
```
