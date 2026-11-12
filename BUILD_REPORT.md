# Build report: GitPulse backlog run, 2026-11-11

Repository `gocap` (module `github.com/bharathvbcr/gosree`).

- **Starting point:** `main` at `9161d81`. Main moved to `7b739fc` during the run; that was another session's commit, not this run's.
- **Where the work is:** branch `run/integ-1111`, at `ab202f2`.
- **Not done:** nothing was merged to `main`, nothing was pushed, and no remote branch was touched.

Labels used below: **verified** means a command was run and its output read. **Inferred** means read from code or from an agent's report without re-running it. **Unverified** means it could not be checked.

## 1. Outcome

| Task | Status now | Branch |
|---|---|---|
| ft-d323b7dd0b328a61d6520b47bec24786: safeerr wrong diagnostics, unbounded solver memory | **review** (rev 4) | `run/d323-solver`, `run/d323-frontend` |
| ft-8e7947bad32ad012d13f77fda5bf9181: gosree-corpus, gosree-erase, acceptance script | **review** (rev 4) | `run/8e79-cmd` |
| ft-b6a2a427e41d1e84e41c83538e4919eb: ownership/borrow false positives, caps, nondeterminism | **review** (rev 4) | `run/b6a2-ownership` |
| ft-3b3c483abe2170060cf568b8edc238a5: ERR005 for a closure closing a captured resource (filed this run) | **review** (rev 3) | `run/3b3c-errcheck` |
| ft-f138c1b5ed039f7e77ec140fdaa3e667: census edges through generics, `[]*T`, `map[K]*T` (filed this run) | **review** (rev 3) | `run/f138-census` |

No task was moved to **done**. Done needs `dev verify`, and in this repository it returns "DevCouncil state is unavailable in this directory": there is no task store, and `dev doctor` finds no `state.sqlite`. A gate that could not run is not treated as one that passed.

Every task followed the same steps:
1. Opus wrote a brief.
2. A Sonnet lane implemented it in its own worktree, writing tests that fail on the code before the fix.
3. A separate Sonnet adversary attacked the result, and every break went back to the lane.
4. Opus reviewed the diff.
5. The work was merged into `run/integ-1111`.
6. The full suite ran on the union of all merged work.

### Final verification (verified)

Run on `run/integ-1111` at `25779ca`, with every lane merged, on 2026-11-11 from 16:12 to 16:18, at load average 55–84.

| Check | Result |
|---|---|
| `go vet ./...` | clean |
| `cargo test --release` (rust/) | ok, 21 tests |
| `go test ./... -count=1`, cgo (Rust engine via gusset) | all 15 packages with tests `ok` |
| `CGO_ENABLED=0 go test ./... -count=1` (pure-Go oracle) | all 15 packages with tests `ok` |
| Stress ledger (`go test ./tests/stress -run '^TestStress$' -v`) | PASS: 602 hits locked, 187 known misses, 46 known false positives, 0 crashes, in 142 packages and 133 roots |
| Checker fingerprint (`bash tests/stress/fingerprint.sh`) | `77f03b52f18b13af` |
| `bash tests/run_acceptance_tests.sh` | PASSED, 12/12 tests, 64 `want` lines |

Earlier runs of the same suite:
- `6b401b9` (three lanes merged): one failure, `passes/ownership` `TestReleaseSummariesRecursiveAndClosureDiamondFast` ("took 3.330249459s (want < 2s)"). That package was unchanged on the branch, and the test passed 3 of 3 times alone on both the branch and the base. It was a wall-clock assertion under load, and ft-b6a2 replaces it with step bounds.
- `dfa778d` (five lanes merged): all ok.

My card summaries for ft-d323, ft-8e79, ft-3b3c and ft-f138 say "16/16 packages". The correct figure is 15 packages with tests.

The stress ledger changed only in ERR005: 5/0/0 became 7/1/1, all from the new root `er_w_rangecapture`. No existing marker changed. `tests/stress/FINDINGS.md` was re-counted at this tip (`ab202f2`).

## 2. What each task changed

### ft-d323: safeerr

**Solver lane** (`run/d323-solver`: a6693fc, 3c9abd8, f208c7c, 3ca416b)
- **C1, verified.** Reproduced. Each function now has a state budget, `MaxStateCells = 2^23` (blocks × slots). The encoder and both decoders enforce it. A Rust allocation failure becomes `SolveError::AllocationFailed` instead of an abort. At the budget, the Rust engine uses about 0.9 GiB and the oracle 1.3–1.6 GiB.
- **H3.** Tuples that meet in one phi and are bound in mutually unreachable blocks share one identity, at most 64 per class.
- **M2.** The no-cgo `Solve` runs encode → SolveFrame → decode, so the limits hold in both builds.
- **L2.** The call-graph sweep cap is the function count + 1, with dirty-flag re-solving. The cap error is kept as an invariant.
- **L3.** The generator is extended, `FuzzDecoders` is added, and Rust gets random frames.
- **H1.** Fixed in the same lane; the parity test covers it.

**Front-end lane** (`run/d323-frontend`: fd69d9a … fb3fb14)
- **H2.** Range-over-func bodies are scanned.
- **H4.** An implicit return, including in yield functions, counts as a return for deferred closes.
- **M1.** A concrete error pointer is observed at both its root slot and its own slot.
- **M3.** The shadow scan and block lookup are linear: N=1000 visits dropped from quadratic, and a bounded-work test guards it.
- **L1.** Same-line matching is deterministic and restricted to calls that can be the AST entry.

**Docs.** `docs/ERROR_DISCIPLINE.md` states which limits hold without cgo.

### ft-8e79: corpus, erase and acceptance tools (`run/8e79-cmd`, 11 commits)
- **Census.** Type cycles resolve as one Tarjan component, so row order cannot change a class.
- **`-annotate` and archive extraction.** Both go through `os.Root`. A path is refused if it is absolute, has backslashes, `.`/`..`/empty elements, is not local, passes through any symlink element, or is not a regular file.
- **`gosree-erase`:**
  - `-w`/`-o` combined with `-l`/`-d` exits 2. Stdin panics are recovered (exit 1).
  - Erasing an inline directive never joins two tokens. CRLF block ends and lone-CR line directives are handled.
  - `-verify` gets a 30 s deadline per directory, can be interrupted, names excluded and `_test.go` files as "not compile-verified", and keeps rc/devel language versions.
- **`gosree`:**
  - Unit-checker mode needs exactly one positional `.cfg` regular file.
  - A wildcard that matches no package gets a stderr warning, from one bounded `go list` that honours `-tags`.
- **Acceptance script.** A tier that ran nothing fails the run, `--json -v` keeps stdout pure, and `GOSREE_ACCEPTANCE_FILTER` is added.
- **Docs.** The documented CLI behaviour was observed with real runs before it was written (`1a81492`).

### ft-b6a2: ownership and borrow (`run/b6a2-ownership`, 12 commits)
- **CAP003.** Bound closures are looked up by `*types.Var`, memoised. Shadowed variables, field selectors and sibling scopes stay silent, and SAFE002/003/004 are no longer hidden behind a false CAP003.
- **Determinism.** Map-ordered first matches are sorted; 20 runs gave byte-identical output.
- **imm and handlers.** The imm 16-type cap is removed, and `handlerRoots` follows every return. A 24-deep imm chain and a handler factory with 9 early returns are now found; the base missed both.
- **Lockset caps.** A test seam (`ObserveRelease`) shows the 8-step and 32-release caps being reached. The wall-clock assertion is gone.
- **Fact decoder.** Bounded, with a fuzz test. The adversary's 22 corrupt payloads cause no panic (the base panics on 2); fuzzing ran 2.3M executions.
- **Borrow findings with no position** are reported at an enclosing position.
- **Shared-load hatch use** no longer depends on call depth. Of 10 synthetic-function shapes tested, none lost a HIT.
- **Init-only package variables whose memory escapes** are no longer exempt.
  - The adversary found a false positive here: a table that is only logged or printed lost its init-only status. It is fixed in `b1c8585`.
  - Printing calls now count as escapes only when a type they reach has `String`, `Error`, `Format`, `GoString` or `Write`, or is an interface.
  - I checked four probes with real runs: a plain printed map is clean; an element type with `String` and a global used as an `Fprintf` writer still escape (fail closed); a locally built `[]any{g}...` is clean.

### ft-3b3c: ERR005 through captured resources (`run/3b3c-errcheck`)
- **Fix.** Inside a closure, every load through a captured variable (a pointer `*ssa.FreeVar`) shares one written flag. Nilness stays per load, so captured `err` variables are unaffected: ERR001–ERR004 output is identical before and after, in both engines.
- **Adversary break, fixed.** A false positive after reassigning a captured resource. A direct store now resets the flag (`9244bb2`).
- **Range-over-func.** A `defer` in a range-over-func body runs at the enclosing function's return (Go spec; checked with a program). A write after the loop is therefore a real miss, recorded as `MISS(ERR005)`, not documented as intended.
- **Equivalence.** The full-root stress dump is identical to `6b401b9` in both engines.

### ft-f138: census edges (`run/f138-census`)
- **Fix.** `reach` follows generic type arguments and the elements of arrays, slices, map keys and values, channels and pointers. Opus review sent it back twice, fixing a lost X class and type arguments lost through an interface store. The adversary found no break.
- **Adversary checks:**
  - Termination: recursive generics take under 1 ms, depth 200 under 0.5 s.
  - Order: 59 row orders gave the same result.
  - 37 of 42 ordinary shapes are identical to `6b401b9`; the 5 that differ are intended.
- **ScholarLM re-measure (verified).** Re-measured at `bb60b53` from an archive of the repository, read-only. `docs/ROADMAP.md` got a dated paragraph (`dfa778d`), and the original figures stay as history.
  - 264 of 324 hatches (81.5%) reach a post-construction write, up from 257 (79.3%).
  - The frozen rule clears 1 of 324, down from 2.
  - The failure condition still holds: 240 of 244 big closures.
  - The refuter still does not fire: 18 of 261.
  - The 53.2% and 11.6% bounds were not re-derived.

## 3. Deferred or blocked

| Task | Why it was not worked |
|---|---|
| ft-a2b39db3dea40ab9a300da2dbac2d304: merge integ4 | Blocked: `build/lanes/integ4` no longer exists, and the card itself suggests closing. Owner decision. |
| ft-9a580082245452952a2f830a337a678e: errcheck vs errflow | Owner decision. `passes/errflow` exists nowhere, so this run worked on errcheck (decision 2, section 6). |
| ft-7fe7a91f9e20940d141ea2dc88673908: owner rulings | Owner decisions only. |
| ft-264efe1b263705659db4699f0709d81a: new rule families | Needs scope decisions. |
| ft-238fbb74c2baffa906e274420834bffa: publish the rename | Pushing main needs owner approval. |
| ft-bc5b3fa7611d582daa70289cfe650758: ScholarLM re-triage | Held by another lane. It committed `9161d81` on main without moving the card, and has uncommitted edits to `refservice/scholarlm-bb60b53.md`. |
| ft-a0d4836d4a0f3ee61362174f62d2b379: flaky and unverified results | Needs an idle machine; load stayed between 15 and 100 all day. Several of its checks name `passes/errflow` and `gocap`, which no longer exist. One flake it describes is removed by ft-b6a2. |
| ft-d71ccd659d9277a9e1cc00c3b454cfb0: 57 misses and 24 false positives | Its ownership and borrow files carry uncommitted edits in the main checkout from another session, so they count as held by another lane. Its ERR misses share `lower.go` with ft-3b3c, which now has the four subtasks below. |

## 4. New tasks filed

| Task | Parent | What it covers |
|---|---|---|
| ft-f138c1b5ed039f7e77ec140fdaa3e667 | ft-8e79 | Census edges (worked this run) |
| ft-3b3c483abe2170060cf568b8edc238a5 | none | ERR005 through captured resources (worked this run) |
| ft-420d34acafa7bb07f8a719084e83a460 | ft-d71c | ERR002 false positive when a closure assigns a captured `(v, err)` and the caller checks `err` |
| ft-c7dd9d94a3f958ad6ed0e0dee42804e4 | ft-d71c | Existing ERR005 misses across closures and captured pointers; ERR003 false positive on `_, _ = w.Write(nil)` in loops; a captured `os.Create` result closed without a write |
| ft-e203e72763917672ec6ce037874fdbb7 | ft-f138 | Census: hatch roots that are not structs, and module interfaces inside containers |
| ft-e69ea44b06cb32ef3b1cafe6a698e46e | ft-d71c | Existing ownership and borrow misses and false positives from the b6a2 adversary; validation when the consumer reads a decoded FuncFact |

## 5. Benchmarks

Benchmark baseline: `wt/base` at `9161d81`, kept for this comparison.

| Change | Measurement | Verdict |
|---|---|---|
| d323 solver (state budget, tuple merge, dirty flags) | The lane's `BenchmarkSolvePackageAB` arms spread up to 8× under load, so they are unreadable. The adversary re-ran both versions on identical frames, alternating runs, and got **+2% on the fastest run**, inside the **12–20% noise between repeated runs of the same build**. | No regression over 5% shown. A +2% figure inside 12–20% noise is a justification, not a measured pass. |
| d323 shadow scan (M3) | `BenchmarkShadowScan`, fastest of 6 pairs: N=100 2.0 → 0.11 ms, N=1000 231 → 0.57 ms, N=5000 8938 → 4.5 ms. The spread was 51–482% under load 90–180. | Faster by orders of magnitude. The per-run timings are unverified; the deterministic visit-count test is the evidence. |
| ft-3b3c | Not benchmarked: one type assertion at write and close sites only, nothing per load. | Inferred no regression. |
| ft-8e79, ft-f138, ft-b6a2 | No benchmark covers these paths (CLI and corpus tools; ownership caps are tested by step counts). | Not claimed. |

## 6. Fable (advisor) decisions

1. **Wave 1 layout.** ft-d323 split into solver and front-end lanes, plus ft-b6a2 and ft-8e79, on branches with no shared files. The integrator owns `FINDINGS.md`, `RULES.md` and `README.md`. The full suite runs on one integration branch before anything moves to review.
2. **ft-9a58.** Work on errcheck anyway, because `passes/errflow` does not exist.
3. **ft-bc5b.** Deferred, because another lane holds it. `MaxFuncs` was left untouched.
4. **ft-8e79 exit codes.** Changes made as its acceptance criteria prescribe. The `-json` exit status was left alone (ft-7fe7 ruling).
5. **b6a2 overlap.** b6a2 continued to review on its branch, even though the main checkout has uncommitted edits to the same files. Merge order is the owner's call.
6. **Wave 2 scope.** Two lanes: ft-3b3c (only `lower.go`, because `errcheck.go` is dirty on main) and ft-f138. Wave 1 was closed on the verified integration tip first. ft-a0d4 and ft-d71c were deferred for the reasons in section 3.
7. **Range-over-func after-loop write.** Check the Go spec first. Because the defer runs at the enclosing function's return, the miss is recorded as a MISS, not documented as intended.

## 7. Owner decisions pending

1. **b6a2 overlap with uncommitted work in the main checkout.**
   - Another session (likely Claude Desktop pid 20696) has about 2,000 lines of uncommitted edits to the files b6a2 changes: `passes/ownership/{contracts,globals,graph,ownership,summary,check,handlerwrite,imm,lockset,spawn}.go`, `passes/borrow/{check,storage}.go`, and new `*_caps_*`, `*_nopos_*` and `locate_*` tests.
   - These look like fixes for the same card items. Which version lands is your decision, item by item, and `globals.go` will conflict.
   - Another session has also created `/Users/bharath/Code/devtools/gocap-worktree` (branch `harden/gosree-gates`) at this run's integration commit `6b401b9`, with b6a2-named test files staged. It was not touched.
2. **b6a2:** the SAFE007 "unlocks it here" wording when a widened release at the lockset release cap is reported; the hatch span for a borrow conflict on the first borrow; unifying `sourceFuncs` and `allPackageFuncs` needs a new internal package.
3. **d323:** H4, where ERR005 on a function ending in `os.Exit` looks like a false positive; M1 silencing semantics.
4. **8e79:** exit 2 or exit 1 for erase `-w`/`-o` combined with `-l`/`-d`; `extractTar` still creates symlink entries, contained by `os.Root`.
5. **3b3c:** a captured resource reassigned inside another closure (`replacedByCall`) is kept as a known false positive, `FP(ERR005)`. A sound fix would have to know which calls run that closure, and resetting on every call loses real findings.
6. **f138:**
   - `Box[T]{p *T}` counts as a frozen collection even for `Box[int]`. That predates this run.
   - Intended changes to review: `chan B` and `*atomic.Pointer[B]` fields now reach B's class.
   - `cmd/gosree-corpus/refservice/scholarlm-bb60b53.md` still cites the old census figures. It is dirty in the main checkout (ScholarLM lane), so it was not edited.
7. **Merging.** `run/integ-1111` holds every change and passes the suite. Merging it to `main`, and in what order relative to the main checkout's uncommitted work, is your decision. Nothing was pushed.
8. **Cards in the backlog awaiting rulings:** ft-a2b3, ft-9a58, ft-7fe7, ft-264e, ft-238f.

## 8. Storage

Free space on `/` (926 GB) through the day:

| Time | Free |
|---|---|
| 07:04 | 179 GB |
| 07:22 | crossed 139 GB (the 15% line) |
| 08:57 | 58 GB |
| ~14:55 | 49 GB (lowest) |
| 15:03 | 92 GB |
| end | 92–101 GB (about 10–11%) |

Free space was below 15% from about 07:22 onward and never below 40 GB.
- **What drove the drop:** other sessions. The largest consumer is `/private/tmp/claude-501/-Users-bharath-Code-devtools-GitPulse` at 67 GB (08:57), which belongs to another project's sessions and was not touched.
- **This run's peak footprint:** about 1.5 GB (worktrees of about 70 MB each, Rust targets of 66–71 MB each, scratch).
- **What I can't get back:** nothing this run can delete restores 15%. I proceeded while free space stayed above 40 GB and re-checked before each suite run.

Removed by this run:
- Rust `target/` of each finished adversary worktree.
- The adversary's baseline copy `d323-base`, and the `d323s` scratch (169 MB).
- The `f138out` ScholarLM extracts (about 150 MB in total, removed by the lane).
- Doc-check scratch.
- After the run, these worktrees: `8e79-adv`, `d323-frontend-adv`, `d323-solver-adv`, `b6a2-adv`, `b6a2-base2`, `3b3c-adv`, `f138-adv`. Each was checked clean before removal.

`df` moved by well under 1 GB, because other sessions write at the same time. The scratchpad now holds 728 MB.

Kept:
- Every `run/*` branch. They are local and unpushed.
- The lane worktrees (branches in review) and `wt/integ`, which another session's branch starts from.
- `wt/base`, the benchmark baseline.
- The two ScholarLM `census.json` files in `f138keep/` (3.8 MB).
- The shared Go build cache. It is 7.4 GB now; it was 8.6 → 11 GB earlier. It is shared with other sessions, so this run's share cannot be told apart, and it was not cleaned.

Every adversary branch (`run/{3b3c,8e79,b6a2,d323-frontend,d323-solver,f138}-adv`) is fully merged by content: `git cherry` shows each of its commits already on its lane branch. They can be deleted on your word.

## 9. Gaps and caveats

- **DevMap.**
  - Every impact and affected-test walk was `walk_incomplete`, because calls through `analysis.Analyzer` dispatch cannot be bound. They give a lower bound only.
  - After wave 2, the index reported "source tree differs from the indexed generation" (gen 405), because the main checkout is being edited. Graph answers after that point are unknown.
  - Several lanes did not consult DevMap for their edits; each lane report says so.
- **Collision checks.** `gitpulse_collision_risk` was run on each lane's files before the lane started. The hooks' own collision scans were sometimes incomplete ("1 worktree(s) were not scanned").
- **One stalled agent.** The first b6a2 adversary stopped making progress for 53 minutes with no running child process and no commits. It was stopped and replaced, and its unfinished tests were reused.
- **Clock times.** The working notes give estimated times for some wave-2 steps. This report uses only times read from command output.
