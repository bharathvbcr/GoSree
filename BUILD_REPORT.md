# Build report: GitPulse backlog run, 2026-11-11

Repository `gocap` (module `github.com/bharathvbcr/gosree`).

- **Starting point:** `main` at `9161d81`. Main moved to `7b739fc` during the run; that was another session's commit, not this run's.
- **Where the work is:** branch `run/integ-1111`, at `e34e37c`.
- **Merged since:** another session merged `run/integ-1111`, every lane branch and both second-round adversary branches into local `main` (merge commits `afde37e` 18:37 to `c147b2a` 19:21). It also fixed two problems in this run's work; see section 10. This run did not merge anything into `main`.
- **Not done:** nothing was pushed (`origin/main` is still `7b739fc`; local `main` is 80 commits ahead), and no remote branch was touched.
- **This revision of the report** is on branch `run/report-1111`, which starts at `main` `78f0879`. Section 10 and the corrections marked "Corrected" were added after the merge.

Labels used below: **verified** means a command was run and its output read. **Inferred** means read from code or from an agent's report without re-running it. **Unverified** means it could not be checked.

## 1. Outcome

| Task | Status now | Branch |
|---|---|---|
| ft-d323b7dd0b328a61d6520b47bec24786: safeerr wrong diagnostics, unbounded solver memory | **review** (rev 5) | `run/d323-solver`, `run/d323-frontend` |
| ft-8e7947bad32ad012d13f77fda5bf9181: gosree-corpus, gosree-erase, acceptance script | **review** (rev 4) | `run/8e79-cmd` |
| ft-b6a2a427e41d1e84e41c83538e4919eb: ownership/borrow false positives, caps, nondeterminism | **review** (rev 6) | `run/b6a2-ownership` |
| ft-3b3c483abe2170060cf568b8edc238a5: ERR005 for a closure closing a captured resource (filed this run) | **review** (rev 5) | `run/3b3c-errcheck` |
| ft-f138c1b5ed039f7e77ec140fdaa3e667: census edges through generics, `[]*T`, `map[K]*T` (filed this run) | **review** (rev 3) | `run/f138-census` |

All five branches are now contained in local `main` (section 10). No task was moved to **done**. Done needs `dev verify`, and in this repository it returns "DevCouncil state is unavailable in this directory": there is no task store, and `dev doctor` finds no `state.sqlite`. A gate that could not run is not treated as one that passed.

Every task followed the same steps:
1. Opus wrote a brief.
2. A Sonnet lane implemented it in its own worktree, writing tests that fail on the code before the fix.
3. A separate Sonnet adversary attacked the result, and every break went back to the lane.
4. Opus reviewed the diff.
5. The work was merged into `run/integ-1111`.
6. The full suite ran on the union of all merged work.

### Verification at `main` `78f0879` after the merge (verified)

Run in a scratch worktree at `78f0879` on 2026-11-11 from 23:05 to 2026-11-12 00:00, at load average 50–106.

| Check | Result |
|---|---|
| `cargo build --release && cargo test --release` (rust/) | ok, 21 tests |
| `go vet ./...` | clean |
| `go test ./... -count=1`, cgo | 15 of 17 packages with tests `ok`. `passes/ownership` hit `go test`'s default 10-minute timeout. `tests/stress` failed `TestScale/send/use_pairs_in_one_function`. |
| `CGO_ENABLED=0 go test ./... -count=1` | 16 of 17 `ok`; `passes/ownership` hit the same timeout |
| `go test ./passes/ownership -count=1 -timeout 60m` | ok in both engines (571 s cgo, 548 s without). Main's FINDINGS.md also runs with `-timeout 60m`. |
| `TestScale` repeated | Passes alone in both engines with a growth exponent of about 2. It failed 1 of 3 further runs (12.4× from N=200 to N=400, bound 10×). Over 8 interleaved rounds against the base `9161d81`, N=400 took 85–119 ms on `main` and 88–104 ms on the base, and every round passed. In 4 further interleaved rounds of the whole `TestScale` at load 54–63, both passed every round, with N=400 at 80–108 ms on `main` and 80–120 ms on the base. **Inferred:** a wall-clock ratio that spikes under contention. The base was never seen to fail, but I could not reproduce the failing condition on demand, so a small regression is not ruled out. Noted on ft-a0d4. |
| Checker fingerprint | `ea4d532b03a3c903`, the same as main's FINDINGS.md |

The acceptance tiers were not run at `78f0879`; their last run is the one below.

### Final verification of the integration branch (verified)

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
- **H1. Corrected:** not fixed by this run. H1 does not reproduce at the starting point `9161d81`, because the earlier commit `87e48f5` had already fixed it. I checked this with real runs on the base and on the integration branch in both engines. The lane's parity test passes on the base too, so it guards H1 but does not prove a fix.

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
  - **Second adversary (`404bb00`), one BREAK.** A table stored into a `[]any` through a slice of its array (`s := []any{1, 2}; s[0] = g; fmt.Println(s...)`) went unreported, though `aa6d62f` reported it. `formatOperands` read only stores made through the array's own element addresses. The other session fixed it after the merge (`9823ff0`, section 10). Its 50-case table of shapes that must still report or must stay clean passes.
  - **Pinned as pre-existing, not fixed:** a direct `keep = g` to another package variable is never reported. Because the print no longer marks `g` as mutated, `fmt.Println(g); keep = g` went from reported to silent; the root miss predates this run. A helper that stores into the slice before the spread is also missed.
  - **Speed fix, corrected.** `60968b7` skipped the escaped-variable scan in any package with no static call to a freezing publisher. I approved it as sound, and I was wrong: a freezing publish through an interface has no static callee, so in such a package the scan was skipped and SAFE002/SAFE004 were lost. The other session found this and replaced the gate with a scan built on first use (`16b0506`). I reproduced the failure: `TestEscapedTablePublishedByEveryRoute/interface` fails at `0cc9243` (the merge with `60968b7`).

### ft-3b3c: ERR005 through captured resources (`run/3b3c-errcheck`)
- **Fix.** Inside a closure, every load through a captured variable (a pointer `*ssa.FreeVar`) shares one written flag. Nilness stays per load, so captured `err` variables are unaffected: ERR001–ERR004 output is identical before and after, in both engines.
- **Adversary break, fixed.** A false positive after reassigning a captured resource. A direct store now resets the flag (`9244bb2`).
- **Second adversary (`95fa488`), one BREAK, fixed.** `9244bb2`'s reset lost a true positive: `w` written, `other` written, `w = other`, then `w.Close()` with a write after it. A store through a captured variable now carries the stored value's written state; error-typed values keep the plain reset (`dbef4bf`). The same change fixes four misses that predate this run, all now `want "ERR005"`: a written value stored into a never-written `w`, and an `os.Create`/`os.OpenFile` result stored into a captured variable and closed unwritten.
- **Self-probe of `dbef4bf` (verified, both engines).** A captured read-only `os.Open` result with `defer Close`, a captured parameter that is closed, and a captured `get()` result with `defer Close` give no ERR005. The only finding is a pre-existing ERR002 on `f, _ = os.Open(p)`, which is correct.
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
| ft-d71ccd659d9277a9e1cc00c3b454cfb0: 57 misses and 24 false positives | Its ownership and borrow files carry uncommitted edits in the main checkout from another session, so they count as held by another lane. Its ERR misses share `lower.go` with ft-3b3c. Three subtasks were filed under it (below); ft-e203 belongs to ft-f138. |

## 4. New tasks filed

| Task | Parent | What it covers |
|---|---|---|
| ft-f138c1b5ed039f7e77ec140fdaa3e667 | ft-8e79 | Census edges (worked this run) |
| ft-3b3c483abe2170060cf568b8edc238a5 | none | ERR005 through captured resources (worked this run) |
| ft-420d34acafa7bb07f8a719084e83a460 | ft-d71c | ERR002 false positive when a closure assigns a captured `(v, err)` and the caller checks `err` |
| ft-c7dd9d94a3f958ad6ed0e0dee42804e4 | ft-d71c | Existing ERR005 misses across closures and captured pointers; ERR003 false positive on `_, _ = w.Write(nil)` in loops. (The captured `os.Create` shape it also listed is fixed by `dbef4bf`.) |
| ft-e203e72763917672ec6ce037874fdbb7 | ft-f138 | Census: hatch roots that are not structs, and module interfaces inside containers |
| ft-e69ea44b06cb32ef3b1cafe6a698e46e | ft-d71c | Existing ownership and borrow misses and false positives from the b6a2 adversary; validation when the consumer reads a decoded FuncFact |
| ft-1f846b0057d8860989a544ac56143014 | none | Make the escaped-variable scan cheaper without losing interface publishes (the +6.2% in section 5) |

Note added to ft-a0d4 (flaky tests): the `TestScale/send/use` wall-clock ratio, and `passes/ownership` exceeding the default 10-minute timeout under load.

## 5. Benchmarks

Benchmark baseline: `wt/base` at `9161d81`, kept for this comparison.

| Change | Measurement | Verdict |
|---|---|---|
| d323 solver (state budget, tuple merge, dirty flags) | The lane's `BenchmarkSolvePackageAB` arms spread up to 8× under load, so they are unreadable. The adversary re-ran both versions on identical frames, alternating runs, and got **+2% on the fastest run**, inside the **12–20% noise between repeated runs of the same build**. | No regression over 5% shown. A +2% figure inside 12–20% noise is a justification, not a measured pass. |
| d323 shadow scan (M3) | `BenchmarkShadowScan`, fastest of 6 pairs: N=100 2.0 → 0.11 ms, N=1000 231 → 0.57 ms, N=5000 8938 → 4.5 ms. The spread was 51–482% under load 90–180. | Faster by orders of magnitude. The per-run timings are unverified; the deterministic visit-count test is the evidence. |
| ft-3b3c | Not benchmarked: one type assertion at write and close sites only, nothing per load. | Inferred no regression. |
| ft-8e79, ft-f138 | No benchmark covers these paths (CLI and corpus tools). | Not claimed. |
| Waves 1 and 2 before b6a2 (`6b401b9`, `dfa778d`) | CPU of the stress ledger (method below), 8 rounds: −1.8% and +0.6%; A/A −0.1% | No regression |
| ft-b6a2 as reviewed (`b1c8585`) | 10 rounds at load 60–90: +5.8% paired median, slower in 10/10, A/A 2.5%. Bisect, 8 rounds: `e32af00` −0.4%, `cb22145` +1.0%, `d9d8f3a` +9.6%, `b1c8585` +8.0%. The lane profile puts the cost in the escaped-variable scan, which builds about 352 package graphs, about 0.38 s per run. A re-measure at load about 14 gave +2.5%. | Over 5% at high load; the cause was found |
| `60968b7` (lane speed fix) | −2% against the base at load about 14 | **Withdrawn:** unsound, removed on `main` by `16b0506` (section 10) |
| `9823ff0`: the merge with both review fixes. It also contains the main checkout's checkpoint `2fd93f0`, about 7,400 lines of another session's ownership and borrow work. | First series, 10 rounds, 2026-11-12 00:05, load 50–100: +6.2% paired median (range −0.4 to +8.9%, slower in 9/10); A/A −0.5% (range −6.4 to +5.5%). Second series (below): +5.1%, slower in 10/10. | Not this run's alone; see the next two rows |
| **This run only (`e34e37c`, `run/integ-1111`, no gate)** | Second series, 10 rounds, 00:16–00:27, load 40–60, five arms per round (base, `e34e37c`, `2fd93f0`, `9823ff0`, base): **+6.2% paired median** (range +1.7 to +11.6%, slower in 10/10). Medians: base 5.88 s and 5.77 s, `e34e37c` 6.30 s. A/A −2.0% (range −5.7 to +1.5%). | **Regression over 5%.** Justification and follow-up below. |
| The other session's checkpoint alone (`2fd93f0`) | Same series: +3.5% (range −3.2 to +6.4%, slower in 8/10) | Not this run's work; recorded so the merged figure can be read |

**Method.** CPU time (user + sys) of `go test -c ./tests/stress` binaries running `-test.run '^TestStress$'`. Every arm runs from `wt/base/tests/stress`, so the input is identical; the newer binaries report FAIL there because their markers differ, but they analyse the same packages. Wall-clock time was unusable at this load. Arms are interleaved round by round, with a second base arm (A/A) in each round. Logs: `logs/bench-*-cpu.txt` in this run's scratchpad.

**Justification for the +6.2%.** The measured part is that this run's own work (`e34e37c`) costs +6.2% on the stress ledger. That the cost is the escaped-variable scan is **inferred**, not measured at these commits. The evidence is the 2026-11-11 bisect (`d9d8f3a`, which adds the scan, +9.6%; the commits before it within ±1%), the lane profile (about 352 package graph builds, about 0.38 s of a 5.8 s run, about 6.5%), and `60968b7` measuring −2% when it skipped the scan. The scan is what makes ft-b6a2's init-only fix sound: a package table whose memory escapes is no longer exempt. The one cheaper version measured, `60968b7`, lost findings for publishes through an interface. Speeding the scan up without losing findings is filed as ft-1f846b0057d8860989a544ac56143014. Whether the regression may stand until then is listed in section 7.

## 6. Fable (advisor) decisions

1. **Wave 1 layout.** ft-d323 split into solver and front-end lanes, plus ft-b6a2 and ft-8e79, on branches with no shared files. The integrator owns `FINDINGS.md`, `RULES.md` and `README.md`. The full suite runs on one integration branch before anything moves to review.
2. **ft-9a58.** Work on errcheck anyway, because `passes/errflow` does not exist.
3. **ft-bc5b.** Deferred, because another lane holds it. `MaxFuncs` was left untouched.
4. **ft-8e79 exit codes.** Changes made as its acceptance criteria prescribe. The `-json` exit status was left alone (ft-7fe7 ruling).
5. **b6a2 overlap.** b6a2 continued to review on its branch, even though the main checkout has uncommitted edits to the same files. Merge order is the owner's call.
6. **Wave 2 scope.** Two lanes: ft-3b3c (only `lower.go`, because `errcheck.go` is dirty on main) and ft-f138. Wave 1 was closed on the verified integration tip first. ft-a0d4 and ft-d71c were deferred for the reasons in section 3.
7. **Range-over-func after-loop write.** Check the Go spec first. Because the defer runs at the enclosing function's return, the miss is recorded as a MISS, not documented as intended.
8. **After the merge.** Do not rewrite `main` or `run/integ-1111`. The corrected report goes on its own branch, `run/report-1111`, starting at `main`. The 5% check is re-measured on the merged work, and the commits that are not this run's are measured separately. A regression over 5% is written up with its cause and a follow-up task; it is not reverted here, because reverting would bring back the escaped-table soundness bug.
9. **Before declaring done.** No card summary claims a suite result I did not see. The `TestScale` comparison runs the full test, not one subtest. The benchmark separates this run's commits from the main checkout's checkpoint (`2fd93f0`) that the merge also brought in.

## 7. Owner decisions pending

1. **b6a2 overlap with uncommitted work in the main checkout. Settled by the merge:** the other session committed that work as `2fd93f0` before merging this run's branches. The original note follows.
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
7. **Merging. Corrected:** another session has since merged `run/integ-1111` into local `main`. It first checkpointed the main checkout's uncommitted work (`2fd93f0`), so item 1's overlap was settled there. Pushing `main` (80 commits ahead of `origin/main`) is still your decision.
8. **Cards in the backlog awaiting rulings:** ft-a2b3, ft-9a58, ft-7fe7, ft-264e, ft-238f.
9. **The +6.2% CPU regression** (section 5): accept it until ft-1f84 lands, or hold a push until a sound speed-up is in.
10. **This report's branch** `run/report-1111`: merging it into `main` is your decision.

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

After the merge into `main` (2026-11-11 23:10), free space was 55 GB (about 6%), still above 40 GB. These worktrees were removed; each was clean, and its branch is contained in `main`: `3b3c-adv2`, `base-b6a2`, `base-3b3c`, `8e79-cmd`, `b6a2-ownership`, `d323-frontend`, `d323-solver`, `3b3c-errcheck`, `f138-census` and `integ`. Stale test binaries were also deleted. The scratchpad went from 1.1 GB to 321 MB. `bin/` and `scratch/` (about 165 MB of benchmark binaries and profiles) are still there, because the storage hook asks before any `rm -r`.

Kept:
- Every `run/*` branch. They are local and unpushed, and all are contained in `main`.
- `wt/base` (the benchmark baseline) and `wt/b6a2-adv2` (checked out at `run/report-1111`). The benchmark worktrees `bench-9823`, `bench-e34e` and `bench-2fd9`, along with their test binaries, were removed after the measurements.
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
- **Unprobed by an adversary:** `16b0506` and `9823ff0` were written by another session after the merge. I checked them with the full suite at `78f0879` and five extra slice shapes (below), not with a separate adversary pass.
- **Post-wave checks (2026-11-12 00:30).** `devmap_status`: fresh, generation 511, 220 files, 2,244 symbols, 8,074 edges. `gitpulse_insights`: every facet `ok`, 13 worktrees scanned, none truncated. Session `45c62196` (the one that merged) is still editing `passes/ownership`, so nothing here touches `main`.

## 10. After the merge into `main`

The other session's commits on `main` that change this run's work:

| Commit | What it does | My check |
|---|---|---|
| `16b0506` | Removes `60968b7`'s `hasFreezeSite` gate. The escaped-variable set is built the first time `isInitOnlyImmutableGlobal` reads it. Adds `globals_escape_routes_test.go` (direct call, method value, interface, method expression). | **Verified:** the interface route fails at `0cc9243`, the merge commit that still has the gate, so my approval of `60968b7` was wrong. All routes pass at `78f0879`. |
| `9823ff0` | A slice of a spread array counts as built in place only when it is the printing call's argument and that call is its only use; otherwise it is judged by its type, and `[]any` fails closed. Fixes the b6a2 second adversary's BREAK. | **Verified:** `TestAdv2SliceStoreThenSpreadBreak` passes. Five further shapes are all reported: `copy` into the spread slice, `append` into a shared backing array, a map holding `[]any`, a `make` slice store, and a store through a sub-slice. A plain map handled the same way is also reported. That is a cautious false positive, which predates b6a2 because `aa6d62f` reported every print. |

The later `main` commits (`d754bb7`, `6ca7e09`, `9e5db27`, `b5e77e1`, `78f0879`) come from the other session's own work. They are not part of this run and were not reviewed here, beyond passing the suite above.
