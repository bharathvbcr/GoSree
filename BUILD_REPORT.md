# Build report: GitPulse backlog run, 2026-11-12 → 13 (session a76482b2)

This is the second backlog run on gocap (GoSree). The first run's report, `BUILD_REPORT.md` on `main` (70ae4a8), is kept unchanged on this branch as `BUILD_REPORT-2026-11-11.md`.

- **Branches:** the work is on local, unpushed branches named `run2/*`. Nothing was merged to `main` and nothing was pushed; merging is the owner's call.
- **Labels:** every claim below is labelled **verified** (with the command or evidence) or **inferred**.
- **Run log:** the full chronological log is committed beside this report as [`build-run-2026-11-12/runlog.md`](build-run-2026-11-12/runlog.md). The ft-a0d4 exit-code table is [`build-run-2026-11-12/exitcodes-table.md`](build-run-2026-11-12/exitcodes-table.md), and its raw rows are in `exitcodes-results.tsv`.

## 0. Summary

- **Cards moved to review (8):** ft-f2e5, ft-5e61, ft-420d, ft-c7dd, ft-8f3c, ft-fe6e, ft-c37e, and ft-6d5a *(see §1 for its gate)*.
  - Done: none. `dev verify` cannot run here: `dev doctor` reports no DevCouncil task store and gate mode off (verified 2026-11-12). Review is the ceiling, as Fable ruled.
- **New cards filed (7):**
  - ft-32cd: census nondeterminism
  - ft-eddc: Go 1.27.4 cgo break, p1
  - ft-6d5a: ERR003 quoting, then fixed in this run
  - ft-fe6e: ERR005 design, then worked in one bounded round
  - ft-c37e: gosree_commit provenance, then fixed
  - ft-79c7: replay depth redesign
  - ft-e92e: path keying and the remaining ERR005 shapes
- **Environment break found mid-run.** Homebrew moved `go` to 1.27.4 at 23:19 on 2026-11-12. On 1.27.4 every cgo build of gosree fails, unmodified `main` included. All cgo verification in this run used go1.27.3 by absolute path (Fable ruling). See §5.
- **A brief-induced regression was caught and undone.** ft-c7dd's first ERR005 attempt added 10 false positives and 3 misses. The cause was the orchestrator's brief. It was attributed per commit, split out, and redone under the correct invariant. See §4.

## 1. Cards in review

Every card here carries this label, which must be cleared before merge: **"cgo verified on go1.27.3; go1.27.4 cgo blocked by ft-eddc14e5"**.

| Card | Branch (tip) | Base | What changed |
|---|---|---|---|
| ft-f2e5: extractTar skips links | `run2/f2e5` (4215d6e) | f0dea98 | Symlink and hard-link entries are skipped and counted on stderr. Anything at or beneath a skipped link is refused, and so are non-directory entries naming the root. Names are bounded at 4096 bytes and 128 components. A nil diag writer is an error. |
| ft-5e61: safeerr at 262,144 functions | `run2/5e61` (c2081c6) | f0dea98 | Phase 1 is callee-first by SCC (Tarjan), with rank order inside each SCC. A deterministic work budget (ops + blocks×slots, 64× package size) fails closed. Decoded memory is bounded at 2 GiB (Fable) with exact-capacity decoding. The encoder pre-sizes its output, the oracle response is capped, and refusals are typed sentinels. |
| ft-c37e: gosree_commit provenance | `run2/c37e` (1e216e9) | main 8df1d77 | The commit is read from the binary's own vcs build stamp (validated hex, VCS prefix, fail-closed dirty flag), never from the working directory. |
| ft-420d: ERR002 FP, closure-assigned (v, err) | `run2/420d` (db244aa) | main 8df1d77 | Tuple identity crosses the closure boundary under a per-closure plan, and untracked shapes keep the tip's report. Two later fixes: the Recover block is excluded from return dominance, and nil stays sticky (§4b). |
| ft-c7dd: ERR003 FP on `_, _ = w.Write(nil)` in loops | `run2/c7dd` (e049a2c) | run2/420d | ERR003 only. The 14 ERR005 shapes are recorded as `MISS(ERR005)` stress markers that point to ft-fe6e. |
| ft-8f3c: ERR005 FP before os.Exit / log.Fatal | `run2/8f3c` (290f3fc) | run2/c7dd | A deferred Close is dropped only when the block reaches a process exit and no return, panic or unwind. Pure infinite loops keep their report (a scope limit). |
| ft-fe6e: ERR005 closure replay, one bounded round | `run2/fe6e` (24d21ac) | run2/8f3c | Writes and Closes are replayed at the single dominating call of a tracked closure, capped at 16 nesting levels. Path keying is parked. |
| ft-6d5a: ERR003 double-quoted names | `run2/6d5a` (55b1e86) | run2/fe6e | One quoting owner. Closure pairs and loop twins name the error variable. Message text only. |

**Merge order.**
- Stack A is `run2/5e61` alone.
- Stack B is 420d → c7dd → 8f3c → fe6e → 6d5a; merge it whole, since only the 6d5a tip records the correct fingerprint.
- `run2/f2e5` and `run2/c37e` are independent. Their merge-tree with each other is clean.
- **Stack A×B test merges (Fable's stack rule).** Each Stack B card's tip was test-merged with `run2/5e61` before its review, in both engines, and every merge was clean and green: 420d 865eada, c7dd 8c5c983, 8f3c 417b04b, fe6e 45fbc54, 6d5a ff3e20b. `errcheck.go` auto-merges.
  - The *post-fix* 420d, c7dd and 8f3c tips (after the forward merges of the retDom fixes) were not separately test-merged.
  - The 6d5a tip, which contains them, was.
- **FINDINGS.md fingerprint cell:**
  - Stack B: only the 6d5a tip records its own fingerprint (`1694ac132e12f19b`). Earlier tips are stale, and each card notes its computed value.
  - **After both stacks merge, the fingerprint will be `c4888388dc458b33`** (verified: `git merge-tree --write-tree run2/6d5a run2/5e61`, then the tree archived and `fingerprint.sh` run). The cell must be set to that at merge time.

*ft-6d5a final gate (05:28–05:37): go1.27.3 vet ok, cgo test ./... 17/17 ok, CGO_ENABLED=0 errcheck+gusseterr+stress ok, 6d5a×5e61 merge (ff3e20b) ok in both engines. Its FINDINGS.md records the stack-tip fingerprint `1694ac132e12f19b` (55b1e86).*

### Evidence highlights (all verified)

**Disciplines applied to every card**
- Failing-before tests.
- An Opus review.
- A separate adversarial pass.
- A full-suite gate: go1.27.3, `go test -timeout 60m -count=1 ./...` with cgo, 17/17 packages.
  - Exception, ft-8f3c: 16/17. `tests/stress` failed only `TestWitnesses/lk_wit_maplookup` (a race the runtime did not convict). That witness passed 30/30 isolated on both base and the branch, and the full `tests/stress` rerun passed. It is the load flake tracked by ft-a0d4.
- `CGO_ENABLED=0` runs of the touched packages.

**Load-independent performance evidence**
- ft-5e61, hub-and-chain phase-1 work per function: 4,101 and 16,389 at f0dea98 (quadratic), now 3.0 at 4k/16k/65k.
- ft-420d: plan work is 21 per closure at 500/2,000/8,000 closures.
- ft-8f3c: exit-path analysis costs 2.50 units per block at 1,401 and 21,001 blocks.
- ft-fe6e: with the cap, `nestedClosedSrc` is linear (1,286 → 10,386 at depth 25 → 200).

**Adversarial breaks found and fixed (23 in total, counting each regression class once)**

| Card | Breaks |
|---|---|
| ft-f2e5 | 4, plus 1 found in review |
| ft-5e61 | 4, plus 1 soft spot |
| ft-420d | 2, including a false negative (named results) |
| ft-c7dd | the regression class (§4) |
| ft-8f3c | 1 class: a new miss in 10 infinite-loop shapes |
| ft-fe6e | the nesting stop, plus the ft-420d retDom bug |
| ft-c37e | 5 fail-open stamp cases, plus a doc overclaim |
| ft-6d5a | 1: the loop twin |

**Stress ledger at the Stack B tip (run2/6d5a):** 644 hits, 183 known misses, 46 known FPs, fingerprint 1694ac132e12f19b.
- Moves from 627/173/46: +14 MISS markers (ft-c7dd), −4 promoted HIT (ft-fe6e), +13 HIT (ft-8f3c unwind shapes), and FP markers added and then cleared (ft-8f3c).

## 2. Benchmark deltas

**Wall-clock was not used as evidence (Fable veto).** Load averages ran at 13–160 during the run.

| Card | Measure | Before | After |
|---|---|---|---|
| ft-5e61, Go oracle | allocs/op, 200 funcs | 87,126 | 85,413 |
| ft-5e61, Go oracle | allocs/op, 50 funcs | 18,977 | 17,954 |
| ft-5e61, Go oracle | B/op, 200 funcs | 5.70 MB | 5.60 MB |
| ft-5e61, Go oracle | B/op, 50 funcs | 1.24 MB | 1.18 MB |
| ft-5e61, Rust engine | allocs/op, 200 / 50 funcs | 8 / 7 | 8 / 7 |

- The benchmark is `SolvePackageAB`, interleaved, 6 runs per side.
- **Lowering changes (ft-420d, ft-c7dd, ft-8f3c, ft-fe6e, ft-6d5a): cost is unmeasured.** No lowering benchmark exists. Their only cost evidence is the deterministic work counters in §1, which show linear growth. The checker self-run output is unchanged, but its wall-clock was not compared.
- CPU-profile attribution: the new code is at most 0.4% of samples, and the indicative oracle wall-clock rise was noise.
- No other card touched a benchmarked hot path. Their work counters are listed in §1.

## 3. Deferred or blocked cards, and why

| Card(s) | Reason |
|---|---|
| ft-6021, ft-a807, ft-e69e, ft-1f84, ft-4cd8, ft-697e, ft-8462 (imm half), ft-d71c | `passes/ownership/**` and `internal/kinds/**` are held by the antigravity lanes (`swarm/arch`, 5 unmerged commits; `swarm/integrate`), and gocap-55 commits ownership work to main. |
| ft-e203, ft-32cd | `cmd/gosree-corpus/census.go` is held by `swarm/census`, and main changed it again during the run (9f4c592). |
| ft-bc5b | **Deferred (Fable).** Criteria 1–2 triage ownership output that the pending merges will move, and gocap-55 is still rewriting the record. Resume once swarm/arch and swarm/integrate land and gocap-55 goes quiet.<br>Criterion 3 was checked read-only on `run2/5e61` (go1.27.3): ScholarLM bb60b53 with tests ran with rc 0. `internal/wisdev [wisdev.test]` was analysed (1,529 transfers, no refusal). Max RSS was 8.4 GB (footprint 14.4 GB) and runtime 39.7 s, both under load (verified). |
| ft-a0d4 | Split (Fable). The exit-code table was done: 108 runs, no contradiction with the docs (verified). RB-JSONDUP is fixed and locked (TestVetParity runs and passes, verified).<br>ER-23 is deferred: its doc fix lands in files held uncommitted by session 45c62196. The timing items wait for an idle machine. The errflow items are obsolete.<br>The `lk_wit_maplookup` witness flaked twice in full-suite runs. It passed 30/30 when isolated, both on base and on run2/8f3c.<br>This card could not be logged, because `gitpulse_complete_task` accepts only in_progress/review/done. |
| ft-7fe7, ft-11eb, ft-d120 | Owner decisions. |
| ft-238f | Push: ask-first. |
| ft-264e | Roadmap: needs build/bundle/leave decisions. |
| ft-eddc (new) | gusset-level fix plus a pin bump: owner and gusset lane (Fable). |
| ft-79c7, ft-e92e (new), ft-fe6e remainder | Design follow-ups. Path keying needs per-store versioning; the replay depth needs an Euler-interval redesign. |

## 4. Two corrections the run made to its own work

**(a) The ft-c7dd regression.**
- The first ERR005 attempt was built from a brief that required untracked shapes to "keep reporting where a write may have happened". Those shapes were silent on the tip, so the rule manufactured reports.
- The adversarial pass found 10 new FPs, 3 new misses, duplicates and quadratic nesting.
- The orchestrator attributed each break to a commit by running the Break tests on archives of each commit. ERR003 was clean; path keying and replay owned the breaks.
- Fable ruled: no history rewrite. The attempt is parked as `run2/c7dd-err005-wip` (beb8a61), and ft-c7dd shipped its ERR003 half only.
- The ERR005 work was redone in ft-fe6e under the invariant that an untracked shape stays identical to the tip in both directions, with hard stops. That round shipped clean.

**(b) A latent ft-420d bug found after review.**
- The ft-fe6e lane suspected that `parentIndexFor` reset `retDom` after it climbed to nil. The orchestrator verified it with `ssadump`: go/ssa puts the Recover block (no idom, ends in `return`) before the other returns.
- A first fix (sticky nil) disabled ft-420d's plan for every function containing a defer. It was refined to exclude the Recover block. Per go/ssa's `createRecoverBlock`, its Return only loads the result allocs, which the plan already excludes.
- Tests: F is rejected, G is accepted, and G failed first.
- The fix was merged forward through the stack with merge commits. No concrete false negative from the old bug was demonstrated.

## 5. Toolchain break: Go 1.27.4 (ft-eddc14e5, p1)

- **Upgrade:** `/opt/homebrew/bin/go` moved to go1.27.4 at 23:19 on 2026-11-12 (verified: Cellar timestamps).
- **The check:** in 1.27.4, `cmd/go/internal/work/security.go` recursively scans every `#cgo -L` directory and refuses archives with LTO/bitcode sections (verified by reading the source).
- **The archive:** `libgusset.a` carries `__LLVM,__bitcode` (otool, verified). Every cgo build of gosree fails, including on unmodified `main` (verified). The bitcode probably comes from the prebuilt std rlibs (inferred).
- **CI:** it resolves the newest 1.27.x and is likely affected too (inferred, not run).
- **Fable ruling:**
  - verify with go1.27.3 by absolute path;
  - never bypass the check;
  - the fix belongs to gusset (another session is active there), and adopting it is a pin bump that needs owner approval.

## 6. New cards filed

| Card | Title |
|---|---|
| ft-32cd98a3 (p3) | census witness text and reason order differ between runs of identical code |
| ft-eddc14e5 (p1, high) | cgo builds fail under Go 1.27.4: libgusset.a carries `__LLVM,__bitcode` |
| ft-6d5a784f (p3) | ERR003 message double-quotes a local variable name. **Fixed in this run (review).** |
| ft-fe6eb6d8 (p2) | ERR005 written state across closures and field paths. **One bounded round in review; remainder open.** |
| ft-c37ee4e9 (p3) | module-report gosree_commit names the working directory's checkout. **Fixed in this run (review).** |
| ft-79c70141 (p3) | ERR005 closure replay: quadratic work in nesting depth below the 16-level cap |
| ft-e92e722c (p2) | ERR005 path keying with per-store versioning, and the remaining MISS(ERR005) closure and field shapes. Its spec is `run2/c7dd-err005-wip`. |

## 7. Fable's decisions

1. **Disk.** Do not wipe the shared GOCACHE: it reaches only 12.7% free and costs live sessions. Proceed on the 40 GB floor; halt below it. *(The run halted once, at 38 GiB, and resumed at 56 GiB.)*
2. **Stacks.** Stack A is 5e61 → bc5b; Stack B is 420d → c7dd → 8f3c. Run one safeerr card at a time. Test a throwaway merge of the two tips before review. Reproduce inbox cards first.
3. **ft-f2e5 criterion 2.** All three f138keep files are in scope, with census-after2 as the reference. *(Census verdicts reproduce; witness text does not: ft-32cd.)*
4. **ft-a0d4.** Split it; the errflow items are obsolete.
5. **Vetoes.**
   - No wiping of caches the run doesn't own.
   - No edits under held paths.
   - No timing claims.
   - No run records on main.
   - No card marked done.
6. **MaxDecodedBytes.** Keep 2 GiB, under four conditions, all met (wisdev modelled at 0.023 GiB).
7. **Go 1.27.4.** Verify with 1.27.3 and label; do not fix it in this run.
8. **ft-c7dd.** Split it with no rewrite. `MISS(ERR005)` stress markers are required (fixture comments do not count). It goes to review with its ERR005 half unfixed.
9. **ft-8f3c.** Loop shapes whose only termination is a process exit are silenced (H4). Pure infinite loops keep their report, as a scope limit.
10. **ft-bc5b.** Defer it, with a read-only criterion-3 check.
11. **ft-fe6e.** Add a nesting cap (not a redesign, not a park) and file the redesign. Fix the ft-420d retDom bug with a failing test and merge it forward.

## 8. Owner decisions pending

- **Merging the review branches** (order in §1). Each FINDINGS.md fingerprint cell is correct only on the Stack B tip. After both stacks land, set it to `c4888388dc458b33`.
- **`go run` and gosree_commit (ft-c37e).** The documented `go run ./cmd/gosree-corpus …` invocation (6 places across README, docs and the refservice record) now records `gosree_commit: "unknown (no vcs stamp)"`, because `go run` does not stamp vcs by default.
  - Verified on a temporary worktree of 1e216e9: `go run -buildvcs=true …` and `go build` record `1e216e9`.
  - Choose one: change the documented command (add `-buildvcs=true`, or build then run), or accept no-stamp and cite commits by hand.
  - Most of those docs are held by other sessions.
- **The gusset Go 1.27.4 fix** (ft-eddc14e5) and the pin bump that adopts it. Until then, nothing can be verified with cgo on the default toolchain.
- **Deleting `run2/c7dd-err005-wip`.** Keep it until ft-fe6e's remainder no longer needs it as a spec.
- **Two sentences withheld from held or recently edited files:**
  - extractTar, for README/TOOLING: the text is in commit a6827a5's message.
  - The gosree_commit convention, for the refservice record header: the text is on ft-c37e.
- **`go clean -cache`.** The shared Go build cache is about 45 GB.
- **Existing owner-decision cards:** ft-7fe7, ft-11eb, ft-d120, ft-238f.

## 9. Storage

**Free space on the data volume (926 GiB):**

| When | Free |
|---|---|
| Start, 20:15 | 76 GiB |
| 20:25 | 67 GiB |
| ~20:50 | 47 GiB (cleanup trigger) |
| Before 21:00 | 38 GiB (**run halted**) |
| 21:02 | 56 GiB (resumed) |
| 00:03 | 101 GiB |
| ~03:15 | 71 GiB |
| ~04:00 | 86–88 GiB |
| ~05:15 | 191 GiB (other sessions freed space) |

- **The 15% line (139 GiB):** free space was below it until about 05:15. Nothing the run owned could restore it. The drop came from other sessions: the Go build cache grew from 42 to 45 GiB, other projects' scratchpads held up to 8.5 GiB, and the user's 30 GiB HuggingFace cache was left untouched.
- **This run's peak footprint:** under 1 GB:
  - base worktree, about 210 MB;
  - one implementation worktree at a time with its own Rust target, up to 282 MB;
  - throwaway merge worktrees with Rust targets, cleaned after each use;
  - the scratchpad, under 1 GB.
- **Reclaimed:**
  - exit-code scratch (~70 MB);
  - `cargo clean` of w-5e61 (337.6 MiB);
  - `cargo clean` of five throwaway merge worktrees and of the bc5b-check archive build (≈280 MB each; per-run sizes not captured);
  - attribution archives;
  - ScholarLM extracts.
- **Removed worktrees:** w-f2e5, w-5e61, w-420d, w-c7dd, w-8f3c, w-fe6e, w-c37e and w-6d5a. Each was checked clean except for its untracked `rust/target` symlink. The baseline target was confirmed intact after every symlink removal.
- **Kept:** the base worktree (benchmark baseline) and every `run2/*` branch (in review, or a spec).
- **Final cleanup (05:40):** 54 scratch test binaries and the archive copies were deleted, taking the scratchpad from 827 MB to 69 MB (758 MiB reclaimed). The shared Go build cache was not cleaned (Fable). Free space at the end: 191 GiB.
- **Run total reclaimed:** about 1.2 GiB measured (758 MiB scratch + 337.6 MiB cargo + ~70 MB), plus unmeasured cargo cleans of six throwaway trees.

## 10. Process notes

- **GitPulse.**
  - The collision facet came back `ok:false` at the start (deadline expired), and later in the run it scanned every worktree. Collision risk was checked per path before every wave.
  - A blanket hold on `tests/stress/testdata/**` turned out broader than the evidence and was narrowed to a per-file check.
- **DevMap.** The `lower.go` and `wire.go` impact walks were truncated, so the safeerr blast radius was treated as unknown and those cards were serialised. The index predates code written in the run, so new symbols are not searchable (a recorded gap).
- **Write gate.** The GitPulse/MANVI write gate flags the worktree directory `/Users/bharath/Code/devtools/.gocap-run2` and the memory directory as outside the project root. Those writes went through only because the session was in bypass mode.
- **Lane slips, all corrected and recorded:**
  - an edit to held docs (reverted);
  - a brief that over-scoped the stress-testdata hold;
  - a stray symlink created in the gusset repo (removed and verified absent);
  - Python used once for a mechanical edit (it does not ship);
  - an intermediate commit (69eb7b6, parked on the wip branch) whose own tests do not compile.
- **Platforms.** Every test run was on darwin/arm64.
  - The only cross-platform check: `CGO_ENABLED=0 go vet ./...` with `GOOS=linux` and `GOOS=windows` (amd64) on the f2e5, c37e, 5e61 and 6d5a tips. All were clean (verified). That excludes cgo-only files.
  - **No Linux or Windows test run was attempted.** CI runs on Linux.
  - `extract_links_adv_unix_test.go` carries `//go:build !windows` (verified).
- **ft-420d acceptance after the Recover-block fix.** The exact change against the adversarially tested 90fa6e9 is on the card. Acceptance narrows for functions with two or more normal returns, and widens for single-return functions with a defer. The widening rests on go/ssa's `createRecoverBlock` reading only the result allocs; it got no adversarial pass of its own.
- **Corrected timestamps.** Early run-log timestamps that were estimates turned out wrong and were corrected to "not recorded".

## 11. Wave 7 (2026-11-13, 09:55–11:15, on the owner's "Next wave")

**Pre-wave state (verified).**
- Nothing had moved: main at `8df1d77`, the antigravity branches unmerged (5/9/2), session 45c62196's docs uncommitted.
- Load was 77 and rose to 140–170 during the wave. `dev verify` still had no task store.
- Collisions: 7 of 7 worktrees scanned, none overlapping.

**Fable's wave composition.**
- ft-8462: the preflight half only.
- ft-e92e: design-first, with a hard stop before any code.
- Not ft-79c7.
- Vetoes: no ft-e92e code, no timing claims, keep the go1.27.3 label, no `imm.go`.

| Card | Result |
|---|---|
| ft-8462: preflight half | **Review** (rev 4), `run2/8462` (0aafc95) off main. `isMetaCommand` lets the last `-flags` decide. A repeated `-test`/`-NAME` with different values warns. One adversary break (a value-flag argument read as a flag) was fixed at the root: a single `scanFlags` walk now serves seven callers, and the code got smaller. Two exit-status corrections align preflight with the driver; documented codes are unchanged. **Item 1 (`GOSREE_JSON_CHILD` env bypass) is an owner decision:** the variable is documented (README.md:180, TOOLING:79). A contract-preserving fix is proposed on the card. The imm items are still held. |
| ft-e92e: path keying | **Design approved (Fable), code paused** (in_progress, rev 3), `run2/e92e` (91fad0b). The change set is only tests, testdata and `DESIGN_ERR005_PATHS.md` (verified). Per-store versions of access paths, lowering only: no wire or solver change, and a hand-built IR test agrees in both engines. UNKNOWN is keyed as on the tip. 77 fixtures are pinned. Fable answered six questions: three milestones, K=32 to be measured, the receiver-only Write/Close exemption, maps and slices in the next card. Two hard stops were added. **Resume** when Stack B leaves review, or when the owner says proceed. |

**Gate note (ft-8462).**
- `tests/stress` `TestScale/N_live_//gosree:mut_borrows` failed on a wall-clock ratio: 10.3× against a bound of 10× at load 140–170.
- Isolated, it passed on base and on the branch, with identical `alloc MB` (7.7/29.2/112.8). The branch does not touch the borrow pass.
- This is a third occurrence of the ft-a0d4 load-flake class. The first two were `lk_wit_maplookup`.

**New owner decisions.**
- `GOSREE_JSON_CHILD` bypass: change the documented marker contract, or accept it.
- A sentence for README §4 / TOOLING §1.3.2 documenting the repeated-flag warning. The text is on ft-8462.

**Storage.**
- Removed: w-8462 and w-e92e (branches kept), plus 5 scratch binaries (about 72 MB).
- Free space at the end of the wave: 160 GiB.
- **Cards in review now: 9.** The 8 from §1, plus ft-8462.
