# Project: GoSree (`gosree` / `gocap`) — Rust-Style Safety, Reference Capabilities & Second-Class Borrows

## Architecture & Overview
`gosree` (Go + श्री *Śrī* · **S**tatic **R**ace & **E**scape **E**nforcer, formerly `gocap`) is a static analysis suite for Go built on `golang.org/x/tools/go/analysis` and `golang.org/x/tools/go/ssa`. It enforces Rust-style memory safety, reference capabilities, borrow checking and `Result<T, E>`-style error discipline (`ERR001`–`ERR005`) with zero-cost erasure to standard Go code. The error-discipline solver is a Rust engine linked in-process through Gusset (`rust/gosree-errsolve`, `internal/gusseterr`), with a pure-Go oracle used when cgo is off. The product was renamed from `gocap`, so the module path (`github.com/bharathvbcr/gocap`) and some directories and binaries (`cmd/gocap`, `cmd/gocap-erase`, `cmd/gocap-corpus`) keep the old name.

Detailed documentation is available in the [`docs/`](docs/) directory:
- [Architecture & Subsystem Design](docs/ARCHITECTURE.md)
- [Diagnostic Rules & Violations Reference](docs/RULES.md)
- [Directives & Capability Markers Reference](docs/DIRECTIVES.md)
- [Second-Class Borrows & Non-Lexical Lifetimes (NLL)](docs/BORROW_CHECKING_AND_NLL.md)
- [Concurrency Model & Goroutine Boundaries](docs/CONCURRENCY_MODEL.md)
- [Tooling, Erasure & Verification Guide](docs/TOOLING_AND_ERASURE.md)
- [Error Discipline: `safeerr`, `ERR001`–`ERR005`, wire format, Rust engine and oracle](docs/ERROR_DISCIPLINE.md)
- [Roadmap & Abandon Criterion](docs/ROADMAP.md)

---

## Architectural Subsystems & Data Flow

```mermaid
flowchart TD
    subgraph Directives ["1. Directive & Annotation Engine (internal/annot/)"]
        D1["Parse //gosree:, //sree:, //gocap: and //safe: line comments"]
        D2["Extract capabilities: \\iso, \\imm, \\mub, \\rob, guarded_by, holds"]
        D3["Construct *annot.Index"]
        D1 --> D2 --> D3
    end

    subgraph Borrows ["2. Second-Class Borrow Checker (passes/borrow/)"]
        B1["Storage constraints: struct fields & globals (BORROW001)"]
        B2["SSA flow-sensitive Non-Lexical Lifetime (NLL) analysis"]
        B3["Conflict detection: concurrent &mut or &mut / & (BORROW002)"]
        B4["Owner invalidation during borrow (BORROW003)"]
        B5["Dangling local stack return prevention (BORROW004)"]
        B1 --> B2 --> B3 & B4 & B5
    end

    subgraph Ownership ["3. Reference Capabilities & Concurrency (passes/ownership/)"]
        O1["\\iso (unique owned): affine move on channel send / go spawn (SAFE003)"]
        O2["Fork-join synchronization recovery (sync.WaitGroup, channel join)"]
        O3["\\imm (deeply immutable): transitive verification & lock-free sharing (SAFE005)"]
        O4["\\mub / \\rob: thread-confinement & escape ban (CAP003)"]
        O5["Mutex lockset must-analysis (SAFE009) & handler writes (SAFE008)"]
        O1 --> O2
        O3 --> O4 --> O5
    end

    subgraph Toolchain ["4. Zero-Cost Erasure & Tooling (cmd/gosree, cmd/gocap, cmd/gocap-erase, cap/)"]
        T1["cap/ generic zero-cost type aliases (type Mut[T] = *T)"]
        T2["gocap-erase: AST rewriting tool stripping markers & imports"]
        T3["cmd/gosree and cmd/gocap multichecker drivers (go vet -vettool)"]
        T4["cmd/gocap preflight: exit codes 0/1/2/3, refuses -fix and -diff"]
    end

    subgraph Errors ["5. Error Discipline (passes/errcheck/, internal/gusseterr/, rust/)"]
        E1["safeerr: lower SSA CFGs and AST facts into a binary wire frame"]
        E2["Solve: Rust engine via Gusset (unix and cgo) or pure-Go oracle"]
        E3["ERR001 discarded error, ERR002 unchecked unwrap, ERR003 lost or shadowed error"]
        E4["ERR004 typed-nil error, ERR005 writable Close error dropped"]
        E1 --> E2 --> E3 & E4
    end

    D3 --> B1
    D3 --> O1
    D3 --> E1
    B5 --> T3
    O5 --> T3
    E4 --> T3
```

---

## Feature Inventory

Every feature identified during development is inventoried and assigned to a milestone:

| # | Feature | Description | Milestone | Status |
|---|---------|-------------|-----------|--------|
| 1 | `//gocap:iso` Parameter Directive | Marks function parameter as uniquely owned; caller transfers ownership | M2 | **DONE** |
| 2 | `//gocap:imm` Type Directive | Marks struct type as deeply immutable | M2 | **DONE** |
| 3 | `//gocap:mub` / `//gocap:mut` Param Directive | Marks parameter as second-class mutable borrow (`&mut T` / `\mub`) | M1 | **DONE** |
| 4 | `//gocap:rob` / `//gocap:ref` Param Directive | Marks parameter as second-class read-only borrow (`&T` / `\rob`) | M1 | **DONE** |
| 5 | `//gocap:mut` Local Binding Directive | Marks local pointer binding as active second-class mutable borrow | M1 | **DONE** |
| 6 | `//gocap:rob` Local Binding Directive | Marks local pointer binding as active second-class read-only borrow | M1 | **DONE** |
| 7 | `//gocap:unsafe <reason>` Escape Hatch | Suppresses diagnostics for attached declaration or statement | M1 | **DONE** |
| 8 | Legacy `//safe:*` Directive Aliases | Backward compatibility alias for `//safe:iso`, `//safe:imm`, `//safe:unsafe` | M1 | **DONE** |
| 9 | Generic Marker Type `cap.Mut[T]` / `Mub[T]` | Type expression representing `&mut T` / `\mub` | M3 | **DONE** |
| 10 | Generic Marker Type `cap.Ref[T]` / `Rob[T]` | Type expression representing `&T` / `\rob` | M3 | **DONE** |
| 11 | Generic Marker Type `cap.Iso[T]` | Type expression representing `\iso` unique ownership | M3 | **DONE** |
| 12 | Generic Marker Type `cap.Imm[T]` | Type expression representing `\imm` deep immutability | M3 | **DONE** |
| 13 | Dummy Expressions `BorrowMut` / `Borrow` / `BorrowRef` / `BorrowMub` / `BorrowRob` | Helper expressions for explicit borrowing | M3 | **DONE** |
| 14 | Dummy Expression `Move(x)` | Helper expression for explicit ownership transfer | M3 | **DONE** |
| 15 | Struct Field Borrow Prohibition | Rejects borrow types/annotations in struct fields (`BORROW001`) | M1 | **DONE** |
| 16 | Package Global Borrow Prohibition | Rejects borrow types/annotations in package globals (`BORROW001`) | M1 | **DONE** |
| 17 | Transient Stack Struct Exception | Permits non-escaping stack tuples to hold stack borrows | M1 | **DONE** |
| 18 | Simultaneous Mutable Borrow Detection | Rejects multiple concurrent mutable borrows of same allocation (`BORROW002`) | M1 | **DONE** |
| 19 | Mutable-Immutable Aliasing Detection | Rejects mutable borrow when read-only borrow is active, or vice-versa (`BORROW002`) | M1 | **DONE** |
| 20 | Owner Invalidation During Borrow | Rejects mutation or move of owner allocation while borrowed (`BORROW003`) | M1 | **DONE** |
| 21 | Local Stack Return Prohibition | Rejects returning reference to local stack allocation (`BORROW004`) | M1 | **DONE** |
| 22 | Non-Lexical Lifetime (NLL) Analysis | Resolves borrow expiry at last SSA use, permitting sequential reuse without lifetimes | M1 | **DONE** |
| 23 | `\iso` Channel Send Move Invalidation | Invalidates sender binding upon channel send; reports use-after-move (`SAFE003`) | M2 | **DONE** |
| 24 | `\iso` Goroutine Move Invalidation | Invalidates sender binding upon `go` spawn; reports use-after-move (`SAFE003`) | M2 | **DONE** |
| 25 | Fork-Join Synchronization Join Point | Restores ownership to parent if goroutine joined via `sync.WaitGroup` or channel | M2 | **DONE** |
| 26 | Deep Immutability Transitive Check | Verifies all struct fields are pointer-free, strings, or pointers to `\imm` (`SAFE005`) | M2 | **DONE** |
| 27 | `\imm` Goroutine Sharing | Permits verified `\imm` pointers to be shared across goroutines without move invalidation | M2 | **DONE** |
| 28 | `\mub` / `\rob` Channel Escape Ban | Rejects sending stack-scoped borrow over a channel (`CAP003`) | M2 | **DONE** |
| 29 | `\mub` / `\rob` Goroutine Escape Ban | Rejects capturing stack-scoped borrow in a `go` closure or argument (`CAP003`) | M2 | **DONE** |
| 30 | Native Comment Erasure Verification | Directives erase natively with 0 bytes and 0 instructions overhead | M3 | **DONE** |
| 31 | AST Stripping Erasure Tool (`gocap-erase`) | Rewrites marker types to standard Go types and strips imports | M3 | **DONE** |
| 32 | Comprehensive Acceptance Test Suite | Dual-track 4-tier E2E acceptance suite (`tests/e2e`: 12 top-level Go tests over 12 `analysistest` packages; tier 1 follows AC1–AC7) | M4 | **DONE** |
| 33 | Error-Discipline Pass `safeerr` | `passes/errcheck` reports discarded errors, unchecked `(T, error)` unwraps, lost or shadowed errors, typed-nil errors and dropped writable `Close` errors (`ERR001`–`ERR005`) | M5 | **DONE** |
| 34 | Rust Error Solver over Gusset | `rust/gosree-errsolve` solves the lowered wire frame in-process (unix and cgo); `internal/gusseterr` holds the wire format and a pure-Go oracle used otherwise | M5 | **DONE** |
| 35 | `cmd/gocap` Preflight | Exit codes 0 clean, 1 failed, 2 usage, 3 findings; refuses `-fix` and `-diff`; forwards `-tags`; warns when a flag switches passes off | M5 | **DONE** |

---

## Milestones Summary

| # | Name | Scope | Status |
|---|------|-------|--------|
| **M1** | Second-Class Borrows & Local Borrow Checker | Directive parsing for `mub`/`rob`/`mut`/`ref` in `internal/annot/`; storage checks (`BORROW001`); SSA flow-sensitive borrow checking (`BORROW002`, `BORROW003`, `BORROW004`); NLL analysis | **DONE** |
| **M2** | Reference Capabilities Across Concurrency Boundaries | `\iso` move invalidation across channel sends and `go` closures (`SAFE003`); fork-join recovery; `\imm` transitive deep immutability (`SAFE005`); stack-scoped borrow escape ban (`CAP003`) | **DONE** |
| **M3** | Toolchain Compatibility & Zero-Cost Erasure | Native directive zero-cost erasure; `cap/` generic marker types and aliases; `cmd/gocap-erase` AST tool; CLI multi-checker wiring | **DONE** (zero-instruction erasure parity is checked in tier 1) |
| **M4** | Dual-Track E2E Acceptance & Hardening | Dual-track 4-tier E2E acceptance test suite (`tests/e2e`, 12 top-level Go tests across Tiers 1-4); adversarial hardening (`tests/stress`) | **DONE** (the E2E suite passes; the whole repository does not: `passes/ownership` `TestEscapeOracleNoFreshOrWritesOracle` fails) |
| **M5** | Error Discipline & Driver Preflight | `passes/errcheck` (`ERR001`–`ERR005`), the Rust solver and pure-Go oracle, `cmd/gocap` preflight | **DONE** |

---

## Code Layout

```text
.
├── cmd/
│   ├── gosree/             # Multichecker CLI entrypoint (go vet -vettool)
│   ├── gocap/              # Multichecker CLI with exit-code and flag preflight
│   ├── gocap-erase/        # AST zero-cost erasure tool
│   └── gocap-corpus/       # Race corpus & real-world module evaluation engine
├── cap/                    # Zero-cost generic type aliases (type Mut[T] = *T)
├── internal/
│   ├── annot/              # Directive parser (gosree:, sree:, gocap: and safe: prefixes); stale/ reports unused escape hatches
│   ├── gusseterr/          # Error-solver wire format, Gusset engine (unix and cgo), pure-Go oracle
│   └── kinds/              # Type classification & immutability helpers
├── passes/
│   ├── unsafeban/          # SAFE001 ban on unsafe operations
│   ├── borrow/             # Second-class borrow checker pass (BORROW001-004)
│   ├── ownership/          # Reference capabilities & concurrency pass (SAFE002-009, CAP003)
│   └── errcheck/           # Error-discipline pass safeerr (ERR001-005)
├── rust/                   # Cargo workspace: gosree-errsolve (solver), gusset-engine (libgusset.a for Gusset)
├── tests/
│   ├── e2e/                # Dual-track 4-tier acceptance test suite (12 top-level Go tests)
│   ├── stress/             # Adversarial stress & runtime witness suite
│   └── run_acceptance_tests.sh  # Master acceptance test runner
└── docs/                   # Architectural & user documentation
    ├── ARCHITECTURE.md
    ├── RULES.md
    ├── DIRECTIVES.md
    ├── BORROW_CHECKING_AND_NLL.md
    ├── CONCURRENCY_MODEL.md
    ├── ROADMAP.md
    ├── ERROR_DISCIPLINE.md
    └── TOOLING_AND_ERASURE.md
```

## Build Prerequisites

With cgo on a unix target, build the Rust archive before the Go binary, with the gusset checkout as a sibling directory (`../gusset`, per the `replace` in `go.mod`):

```bash
(cd rust && cargo build --release)   # rust/target/release/libgusset.a
go build -o build/gosree ./cmd/gosree
```

Skipping the first step fails at link time (`Undefined symbols ... _gosree_gusset_init`). With `CGO_ENABLED=0`, or on a non-unix target, no Rust is needed and the pure-Go oracle runs instead.
