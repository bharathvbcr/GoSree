# Build report: GitPulse backlog run, 2026-11-12 → 13 (session a76482b2)

> **Status: DRAFT, run in progress.** Sections marked *(pending)* are filled in when the run ends.

This is the second backlog run on gocap (GoSree). The first is recorded in the BUILD_REPORT.md already on `main` (70ae4a8).

- **Branches:** this run's work is on local, unpushed branches `run2/*`. Nothing was merged to `main` and nothing was pushed. Merging is the owner's call.
- **Report location:** this file lives on branch `run2/report`, so the report on `main` is untouched.
- **Sources:** every claim is labelled *verified* or *inferred*. The full chronological log is the session scratchpad's `runlog.md`.

## 1. Cards moved to review

None moved to done. `dev verify` cannot run here: `dev doctor` reports no DevCouncil task store and gate mode off (verified on 2026-11-12). So review is the ceiling.

| Card | Branch (tip) | Base | Summary |
|---|---|---|---|
| ft-f2e5a81c: gosree-corpus extractTar skips links | `run2/f2e5` (4215d6e) | f0dea98 | Symlink and hard-link entries skipped and counted on stderr; anything at or beneath a skipped link refused; names bounded at 4096 bytes and 128 components; nil diag writer is an error. |
| ft-5e614fe8: safeerr at 262,144 functions | `run2/5e61` (c2081c6) | f0dea98 | Phase 1 runs callee-first by SCC (linear on hub-and-chain); deterministic work budget fails closed; decoded-memory bound of 2 GiB (Fable); encoder pre-sizing; oracle response cap; typed refusal sentinels. |
| ft-420d34ac: ERR002 FP, closure-assigned (v, err) | `run2/420d` (90fa6e9) | main 8df1d77 | Tuple identity carried across the closure boundary under a per-closure plan; untracked shapes keep the tip's report. |
| ft-c7dd9d94: ERR003 FP on `_, _ = w.Write(nil)` in loops *(pending gates)* | `run2/c7dd` (9c2f16f+) | run2/420d | ERR003 half fixed. ERR005 half moved to ft-fe6eb6d8 by Fable ruling (§4). |

Every card in review carries the label **"cgo verified on go1.27.3; go1.27.4 cgo blocked by ft-eddc14e5"**, which must be cleared before merge (§5).

### Evidence per card (verified unless marked)

**ft-f2e5**
- 6 failing-before tests.
- Adversarial pass: 4 breaks fixed, plus 1 silent no-op found in Opus review and fixed.
- Full suite green on go1.27.3 (17/17, 21:19–21:42 on 2026-11-12).
- Criterion 2: the ScholarLM bb60b53 archive has 0 link entries, so the fix is a no-op there. The census is not byte-identical even between two runs of identical code. Class totals and per-type classes do match across before, after and f138keep/census-after2.json. Filed as ft-32cd98a3.

**ft-5e61**
- Work counter (load-independent): hub-and-chain cost per function was 4,101 / 16,389 (quadratic) and is now 3.0 at 4k/16k/65k.
- Adversarial pass: 4 breaks and 1 soft spot fixed.
- Fable's four conditions for 2 GiB met: wisdev with tests models 0.023 GiB.
- Full suite green on go1.27.3 (17/17).

**ft-420d**
- Reproduction corrected the card: ERR002 fired at the closure store, not at `use(v)`.
- Adversarial pass: 1 false negative (named results) and quadratic planning, both fixed.
- Full suite green on go1.27.3.
- Stack A×B throwaway merge green in both engines.

## 2. Benchmark deltas

Wall-clock numbers were vetoed as evidence for this run (Fable): load averages were very high. The evidence is deterministic work counters and allocations.

- **ft-5e61:** `SolvePackageAB`, interleaved ×6.
  - Go oracle allocs/op 87,126 → 85,413 (200 funcs) and 18,977 → 17,954 (50 funcs).
  - Go oracle B/op 5.70 MB → 5.60 MB and 1.24 MB → 1.18 MB.
  - Rust engine unchanged (8 / 7 allocs/op).
  - CPU-profile attribution: new phase-1 code ≤ 0.4% of samples. An indicative wall-clock rise in the oracle was noise.
- **ft-420d:** plan work is a constant 21 units per closure at 500, 2,000 and 8,000 closures.
- **ft-f2e5:** not a hot path, so no bench.

## 3. Deferred or blocked cards, and why

| Card(s) | Reason |
|---|---|
| ft-6021, ft-a807, ft-e69e, ft-1f84, ft-4cd8, ft-697e, ft-8462 (imm half), ft-d71c | `passes/ownership/**` and `internal/kinds/**` are held by the antigravity lanes `swarm/arch` and `swarm/integrate`, and gocap-55 commits ownership work to main. |
| ft-e203 | `cmd/gosree-corpus/census.go` is held by `swarm/census`, and main changed it again during the run. |
| ft-7fe7, ft-11eb, ft-d120 | Owner decisions. |
| ft-238f | Push to the remote: ask-first. |
| ft-264e | Roadmap: needs build/bundle/leave decisions per guarantee. |
| ft-bc5b | Stack A after ft-5e61 (shares wire.go/lib.rs). Only one safeerr card at a time. *(pending)* |
| ft-8f3c | Stack B after ft-c7dd. *(pending)* |
| ft-a0d4 | Split per Fable. Exit-code table done (no contradiction with the docs). RB-JSONDUP verified fixed and locked. ER-23 deferred (its fix lands in docs held uncommitted by session 45c62196). Timing items wait for an idle machine. errflow items obsolete. The card could not be logged, because `gitpulse_complete_task` only accepts in_progress/review/done. |

## 4. ft-c7dd: the split

- **First attempt:** fixed ERR003 and attempted ERR005 across closures and field paths.
- **Adversarial pass against the tip:** 10 new false positives, 3 new misses, duplicate diagnostics, and work quadratic in nesting depth.
- **Attribution per commit** (orchestrator, scratch archives):
  - ERR003 (567e3d5): clean.
  - Path keying (69eb7b6): 5 FPs and the 3 misses. Its own tests do not compile.
  - Closure replay (a065635): 5 FPs, the duplicates and the nesting cost.
- **Root cause: the orchestrator's brief.** It required untracked shapes to "keep reporting where a write may have happened". The invariant that held up for ft-420d is that untracked shapes stay identical to the tip in both directions.
- **Fable ruling:**
  - No history rewrite. The old branch is kept as `run2/c7dd-err005-wip` (beb8a61), the spec for new card ft-fe6eb6d8.
  - A fresh `run2/c7dd` carries the ERR003 fix only.
  - The unfixed ERR005 shapes get `MISS(ERR005)` stress markers.
  - One bounded replay round belongs to the new card, with hard stops.

## 5. Toolchain break: Go 1.27.4 (ft-eddc14e5, p1)

- **What changed:** Homebrew moved `/opt/homebrew/bin/go` to 1.27.4 at 23:19 on 2026-11-12, during the run.
- **Why cgo fails:** 1.27.4's cgo check scans every `#cgo -L` directory and refuses archives with LLVM bitcode. `libgusset.a` carries `__LLVM,__bitcode` (otool, verified). Every cgo build of gosree fails, including unmodified `main` (verified).
- **Likely source (inferred):** the bitcode comes from the prebuilt std rlibs.
- **CI:** probably affected, since it resolves the newest 1.27.x (inferred).
- **Fable ruling:**
  - Verify with go1.27.3 by absolute path; never bypass the check.
  - The fix belongs to gusset, where another session is active.
  - Adopting it means a pin bump, which needs owner approval.

## 6. New cards filed

| Card | Title |
|---|---|
| ft-32cd98a3 (p3) | census witness text and reason order differ between runs of identical code |
| ft-eddc14e5 (p1, high) | cgo builds fail under Go 1.27.4: libgusset.a carries `__LLVM,__bitcode` |
| ft-6d5a784f (p3) | ERR003 message double-quotes a local variable name |
| ft-fe6eb6d8 (p2) | ERR005 written state across closures and field paths needs a flow-sensitive design |

## 7. Fable's decisions

1. **Disk:** do not wipe GOCACHE (it reaches only 12.7% free and costs live sessions). Proceed on the 40 GB floor; halt below 40 GB.
2. **Stacks:** A = 5e61 → bc5b; B = 420d → c7dd → 8f3c. One safeerr card at a time. Throwaway merge of the two tips before review. Reproduce inbox cards first.
3. **ft-f2e5 criterion 2:** all three f138keep files are in scope; `census-after2` is the reference.
4. **ft-a0d4:** split it. Errflow items are obsolete.
5. **Vetoes:** wiping caches not owned by the run; editing held paths; timing claims tonight; run records on main; calling any card done.
6. **MaxDecodedBytes:** keep 2 GiB, with four conditions, all met.
7. **Go 1.27.4:** verify with 1.27.3 and label; do not fix in this run; review is allowed with the label.
8. **ft-c7dd split:** see §4.

## 8. Owner decisions pending

- Merge `run2/f2e5`, `run2/5e61`, `run2/420d` and (after its gates) `run2/c7dd`.
  - Stack B is stacked: merge 420d before c7dd.
  - The Stack A×B merge is clean; `errcheck.go` auto-merges.
  - Each needs its FINDINGS.md fingerprint row refreshed at merge. Computed vs recorded is stale on 5e61 (be551257…) and 420d (20a35077…); the ledgers are unchanged.
- The gusset Go 1.27.4 fix (ft-eddc14e5) and the pin bump that adopts it.
- `go clean -cache` if wanted. The shared Go build cache is 45 GB.
- The doc sentence for README/TOOLING on extractTar (text in a6827a5's commit message). Those files carry uncommitted edits by session 45c62196.
- The existing owner-decision cards: ft-7fe7, ft-11eb, ft-d120, ft-238f.

## 9. Storage

- **Free space on the data volume (926 GiB):**

| When | Free |
|---|---|
| Start (20:15) | 76 GiB |
| 20:25 | 67 GiB |
| Lowest (before 21:00) | 38 GiB, run halted |
| 21:02, resumed | 56 GiB |
| 00:03 | 101 GiB |
| After wave 1 | 95 GiB |

- Free space stayed below 15% (139 GiB) for the whole run. Nothing the run owned could restore it. The drop came from other sessions: Go build cache 42 → 45 GiB, other projects' scratchpads up to 8.5 GiB, and the user's 30 GiB huggingface cache, which was untouched.
- **This run's peak footprint:** about 0.7 GB (base worktree 213 MB, one implementation worktree with its own Rust target up to 282 MB, scratch under 100 MB).
- **Reclaimed:**
  - exit-code scratch, about 70 MB;
  - `cargo clean` of w-5e61, 337.6 MiB;
  - `cargo clean` of the throwaway A×B merge worktree.
- **Removed worktrees:** w-f2e5, w-5e61, w-420d and merge-AB, each clean before removal.
- *(pending: final totals)*

## 10. Process notes

- **GitPulse collision facet.** It came back `ok:false` (deadline expired) at the start. Later in the run it scanned all worktrees. Collision risk was checked per path before every wave.
- **DevMap.** The impact walks for lower.go and wire.go were truncated (80 of 166 and 80 of 783), so blast radius was treated as unknown and the safeerr cards were serialised. The index predates code written in the run, so new symbols are not searchable.
- **GitPulse write gate.** It flags `/Users/bharath/Code/devtools/.gocap-run2` (worktrees) and the memory dir as outside the project root. These writes went through only because the session was in bypass mode.
- **Corrections.** The run log's early timestamps were estimates that turned out wrong. They were corrected to "not recorded".
