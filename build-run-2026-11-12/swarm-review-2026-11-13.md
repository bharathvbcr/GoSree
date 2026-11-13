# Review of merge a43c90a (swarm/integrate, ownership v5), and why it was reverted

**Dates.** The review is dated 2026-11-13. The merge was reverted on main in c6d8dad.

**Who.** The merge came from an antigravity lane and landed unreviewed: its session's Opus and Fable calls failed on quota. Session a76482b2 then ran an independent Opus review. Fable ruled to revert, not to fix forward.

**Where the work is kept:**
- `refs/backup/2026-11-13/swarm/integrate` (tip 9b10405), with `swarm/arch` and `swarm/census` under the same prefix.
- The antigravity bundles in `~/.gemini/antigravity/brain/c7c101ff-…/scratch/backups/`.

**Re-landing.** To re-land the work, revert c6d8dad, or bring the changes in as fresh commits; git will not re-merge the old ones.

## How it was tested

- Two builds, both with `CGO_ENABLED=0` (the ownership passes do not use the Rust solver): cb79d14, before the merge, and 82ea584, after it.
- Each probe package was run as `GOPATH=<dir> GO111MODULE=off gosree -safeownership <pkg>`.
- The probes are in [`testdata/swarm-review-probes/src/`](testdata/swarm-review-probes/src/), in a GOPATH layout.

## CRITICAL findings (verified)

**C1: races in loops are no longer reported.**
- The shape: a goroutine started in a loop reaches shared memory through an alias made fresh in each iteration (`it := &items[0]`, `k := j`, `ip := &j.ID`). This happens with or without a join.
- `go run -race` convicts `wit` shapes e1, e2, e4 and e6, 3 runs out of 3.
- Before the merge, the checker reports them (SAFE003, exit 3). After the merge it exits 0 with no findings.
- The diff also shows `fp3` N2, `fp4` L1–L4 and L7, and `pe` P2 going silent.
- **Mechanism (inferred from the code):**
  - `childOf` (`footprint.go:1929–1966`) returns `selfConcurrent=false` for every `go` and `wg.Go`, so the child-against-child check never runs on a spawn the loop repeats.
  - The next iteration's `&items[k]` or `k := j` is not an access, so `refineMove` judges the move benign.
  - `check.go:168–170` therefore skips the report.

**C2: handing a value to another goroutine over a channel is lost when a join follows.**
- The shape (`fp5` H4): `go read(j)`; then `ch <- j` to a visible receiver goroutine that writes; then `<-done`.
- Before the merge this is SAFE003 at `fp5.go:22:8`. After the merge it is silent.
- The same shape, `wit` d12, is convicted by `-race` 3 runs out of 3.

**Checked after the revert (c6d8dad, CGO_ENABLED=0)** — SAFE lines per probe package:

| Probe package | Merged | Reverted |
|---|---|---|
| wit | 0 | 5 |
| pe | 0 | 2 |
| fp4 | 5 | 10 |
| fp5 | 3 | 5 |
| fp3 | 13 | 14 |

## Marker and fixture changes the merge made

| Change | Class |
|---|---|
| `mv_idiom`: three clone FP markers removed (`bytes.Clone`, `slices.Clone`, `append(nil,…)`) | Genuine FP fix. Clones that carry pointers or maps still report. |
| `mv_idiom`: `perElementGo` FP removed | Cleared through C1, not through a proof that the elements are disjoint. Its racy twin with a loop-invariant index is cleared the same way. |
| `sp_forkjoin` `loopGoOneWait`: the read-only `_ = j.ID` HIT(SAFE003) became a clean control, and the fixture code was rewritten to a write so the HIT survives | Changes what SAFE003 means: a read/read capture in a loop is now clean. That conflicts with RULES.md:121 ("any subsequent read, write, or call … is rejected") and the fixture's own "one owner at a time, by design". Needs an owner ruling. |
| `t4_scenarios` `Attack1DoubleMove`; `passes/ownership/testdata/user.go:98` `want` removed | The same read/read semantics change. |
| `sp_table/errgroup`: `HIT(SAFE002)` deleted, with no MISS marker | Breaks the suite's rule that "no HIT is removed to make a change pass". The cleared shape, which is fully lock-guarded, is probably race-free (inferred). |
| `rb_deadcode`: FP moved from line 98 to 100 | Neutral. |

## What held up

- Clone modelling is right.
- Cross-package facts hold: a lazily-writing getter is still caught.
- Init produces the same SAFE006 output.
- Output is deterministic: 5 runs gave byte-identical results.
- The footprint budget fails closed.
- No panics.
- On 82ea584, cgo on go1.27.3: `go vet` passed, and `passes/ownership`, `tests/stress` and `tests/e2e` were all ok.

## Minor

- `fpBudget` is missing from `walkBounds`.
- With only `-safeownership`, the reflect.SetInt race is silent (SAFE001 still fires when all passes run).
- FINDINGS.md §3 and its Run row were not updated.
- The commit's claim of "+5 real use-after-move bugs caught" did not show up in the whole-suite diff.

## Not verified

- The removal of the `http.Client` override.
- The generic AutoSync-interface check.
- The race-corpus and ScholarLM figures.
- Lean, Rust, and amd64 or Linux.
- The exact code path behind C2.
