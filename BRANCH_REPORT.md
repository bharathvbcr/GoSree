# Branch Consolidation & Cleanup Report (`2026-11-13`)

- **Repository**: `/Users/bharath/Code/devtools/gocap` (`github.com/bharathvbcr/gosree`)
- **Pre-consolidation `main` tip**: `8df1d77b628e7b7a6bab28d48091959081f801d2`
- **Post-consolidation `main` tip**: `82ea5846cad3f1386efb384e2afefbff92cbda13` (`ahead 66` of `origin/main`; **not pushed**)
- **Checker fingerprint on final `main`**: `7bb6c3826665ab6c` (`bash tests/stress/fingerprint.sh`)
- **Stress ledger on final `main`**: `643` hits locked, `183` known misses, `42` known false positives, `0` known crashes (`146` packages in `136` roots)

## Verification & Governance Disclosures

- **Live Fable / Claude Opus model calls (`unverified` live — quota exhausted; recorded rulings used)**: Anthropic (`claude-opus-5-5-*`, `claude-sonnet-5-5-*`) and `gpt-oss-120b-medium` quotas were at `0%` throughout this consolidation (`resets 2026-11-19T15:07:05Z`). Live Fable classification review and live Opus conflict review could not execute; instead, we followed Fable's committed classification and merge order in `BUILD_REPORT.md` (where all 8 `run2/*` review cards had already passed Opus + adversarial review, and `swarm/integrate` passed adversarial verification `#55`), manually audited every overlapping file against its merge base and both sides, and kept/flagged all post-report or spec branches for owner review.
- **Go toolchain (`verified` workaround for `ft-eddc14e5`)**: Default `/opt/homebrew/bin/go` (`go1.27.4`) rejects `__LLVM,__bitcode` sections inside `libgusset.a` under its new cgo linker-input security check even on unmodified `main`. Per `BUILD_REPORT.md` (`ft-eddc14e5`), all `ci:local` builds and tests ran under `go1.27.3` (`/opt/homebrew/Cellar/go/1.27.3/libexec/bin/go`) with `GOWORK=off` against a clean `git archive` of pinned `gusset` (`0ab48cae95fe126b94cc02189b88f47f36da19f1`).
- **Lean 4 CI step (`unverified` locally — skipped with reason)**: `elan`, `lake`, and `lean4checker` are not installed on this machine. Zero merged or kept branches modified `specs/`.
- **Transient dynamic `-race` flake observed once (`verified` on rerun)**: On the first parallel `CGO_ENABLED=1` `go test ./...` sweep of Merge 5 (`a43c90a`) while live session `a76482b2` was running concurrently, `TestWitnesses/lk_wit_maplookup/main.go` failed once (`stress_test.go:200: the runtime did not convict this program of a race: it witnesses nothing`, pre-existing dynamic `-race` witness flake tracked under `ft-a0d4836d`). `tests/stress` passed under `CGO_ENABLED=0` (`118.14s`), passed on immediate `CGO_ENABLED=1` `-count=1` rerun (`82.77s`), and passed in the full Merge 5 (`130.22s`), Merge 6 (`123.67s`), and final Phase 6 (`90.29s`) `go test ./...` runs.
- **Zero disruption to live sessions (`verified`)**: All `--no-ff` merges and `ci:local` runs were staged in an isolated clean worktree and applied to `/Users/bharath/Code/devtools/gocap` via atomic `git merge --ff-only`. All 4 uncommitted doc files in `/Users/bharath/Code/devtools/gocap` (`PROJECT.md`, `README.md`, `docs/ARCHITECTURE.md`, `docs/TOOLING_AND_ERASURE.md`) matched their pre-consolidation SHA-256 checksums byte-for-byte after completion, and `gitpulse_insights` confirmed all 3 live Claude sessions (`45c62196`, `b3738d58`, `a76482b2`) remained intact on `main`.

---

## 1. Inventory Table (All 17 Local Branches Observed)

Author on every commit across all 17 branches: `BHARATH CHANDRA VADDARAM <bharath.vbcr@gmail.com>`.
Ahead/behind counts are relative to pre-consolidation `main` (`8df1d77b628e7b7a6bab28d48091959081f801d2`).

| Branch | Tip SHA (Pre-Merge / Final) | Ahead / Behind `8df1d77` | Last Commit (`-0600`) | `--merged main` (at start) | Worktree & Dirty State (during run) | Live Agent? | GitPulse Task & Status | Classification |
|---|---|---|---|---|---|---|---|---|
| `main` | `8df1d77` → `82ea584` | `0 / 0` | `2026-11-12 23:03:14` | yes | `/Users/bharath/Code/devtools/gocap` (**dirty**: `4` unstaged doc files + `253` untracked `.agents/teamwork/**`) | **Yes** (`45c62196` idle, `b3738d58` shell, `a76482b2` busy→shell) | `ft-11eb6b73` (`backlog`, gate docs) | **in use (trunk)** — keep & advance via verified `--ff-only` |
| `swarm/errscale` | `80a766b` | `0 / 48` | `2026-11-12 09:21:09` | **yes** (`0ff82c0`) | `scratch/wt/errscale` (clean) | No | — | **merged** — remove worktree + `git branch -d` |
| `run2/c37e` | `1e216e9` | `4 / 0` | `2026-11-13 04:05:51` | no | none | No | `ft-c37ee4e9` (`review`) | **mergeable (#1)** |
| `run2/f2e5` | `4215d6e` | `6 / 3` | `2026-11-12 21:17:54` | no | none | No | `ft-f2e5a81c` (`review`) | **mergeable (#2)** |
| `run2/5e61` | `c2081c6` | `4 / 3` | `2026-11-13 00:13:09` | no | none | No | `ft-5e614fe8` (`review`) | **mergeable (#3, Stack A)** |
| `run2/420d` | `db244aa` | `6 / 0` | `2026-11-13 04:55:07` | no | none | No | `ft-420d34ac` (`review`) | **mergeable (ancestor of `run2/6d5a`)** |
| `run2/c7dd` | `e049a2c` | `12 / 0` | `2026-11-13 04:55:58` | no | none | No | `ft-c7dd9d94` (`review`) | **mergeable (ancestor of `run2/6d5a`)** |
| `run2/8f3c` | `290f3fc` | `20 / 0` | `2026-11-13 04:56:49` | no | none | No | `ft-8f3c2efd` (`review`) | **mergeable (ancestor of `run2/6d5a`)** |
| `run2/fe6e` | `24d21ac` | `28 / 0` | `2026-11-13 04:57:47` | no | none | No | `ft-fe6eb6d8` (`review`) | **mergeable (ancestor of `run2/6d5a`)** |
| `run2/6d5a` | `55b1e86` | `32 / 0` | `2026-11-13 05:37:48` | no | none | No | `ft-6d5a784f` (`review`) | **mergeable (#4, Stack B tip)** |
| `swarm/census` | `e718708` | `2 / 49` | `2026-11-12 15:02:37` | no | `scratch/wt/census` (clean) | No | — | **mergeable (ancestor of `swarm/integrate`)** |
| `swarm/arch` | `f9e2af0` | `5 / 49` | `2026-11-12 19:38:23` | no | `scratch/wt/arch` (clean) | No | — | **mergeable (ancestor of `swarm/integrate`)** |
| `swarm/integrate` | `9b10405` | `9 / 3` | `2026-11-12 21:39:08` | no | `scratch/wt/integrate` (clean) | No | — | **mergeable (#5)** |
| `run2/report` | `4884b13` → `906237e` | `5 / 0` | `2026-11-13 11:11:19` | no | none | No (updated by `a76482b2` wave 7 before Merge 6) | — | **mergeable (#6)** |
| `run2/c7dd-err005-wip` | `beb8a61` | `9 / 0` | `2026-11-13 02:43:58` | no (`git cherry` vs `run2/6d5a`: `1` `-`, `5` `+`) | none | Referenced by `ft-e92e722c` | `ft-e92e722c` (`in_progress` → design paused) | **abandoned/spec → keep & flag** (per Fable `BUILD_REPORT.md:184`) |
| `run2/8462` *(spawned mid-run)* | `8df1d77` → `0aafc95` | `3 / 0` | `2026-11-13 11:09:20` | no | `.gocap-run2/w-8462` (active during wave 7, then closed by `a76482b2`) | **Yes** (`a76482b2` wave 7) | `ft-8462ae87` (`in_progress` → `review` preflight half) | **in use / mid-run arrival → keep & flag** |
| `run2/e92e` *(spawned mid-run)* | `55b1e86` → `91fad0b` | `33 / 0` (`1` ahead of `6d5a`) | `2026-11-13 11:05:12` | no | `.gocap-run2/w-e92e` (active during wave 7, then closed by `a76482b2`) | **Yes** (`a76482b2` wave 7) | `ft-e92e722c` (`in_progress` → design approved, code paused) | **in use / mid-run arrival → keep & flag** |

Detached worktrees observed:
- `/Users/bharath/Code/devtools/.gocap-run2/base` (`f0dea98`, clean, `run2` reference baseline per `BUILD_REPORT.md:220`) — **kept intact**.
- `scratch/wt/arch-base` (`08bb22f`, clean, already ancestor of `main`, backed up at `refs/backup/2026-11-13/_detached/arch-base`) — **removed cleanly** via `git worktree remove`.

---

## 2. Backups Created Before Deletion (Phase 2)

1. **In-repo backup refs (`refs/backup/2026-11-13/*`)**:
   - Saved for all initial branches at start (`refs/backup/2026-11-13/<branch>`) plus latest mid-run tips (`refs/backup/2026-11-13/latest/<branch>`) and detached `arch-base` (`refs/backup/2026-11-13/_detached/arch-base`).
2. **External git bundles (`git bundle verify` passed on both)**:
   - Initial pre-merge bundle (`15` branch refs, `2.25 MiB`):
     `/Users/bharath/.gemini/antigravity/brain/c7c101ff-2ff2-4a2a-85d1-9a654a0e193c/scratch/backups/gocap-all-branches-2026-11-13.bundle`
   - Pre-deletion full `--all` bundle (`62` refs including `main` at `82ea584`, `run2/8462` at `0aafc95`, `run2/e92e` at `91fad0b`, `run2/report` at `906237e`, and all `refs/backup/*`, `2.28 MiB`):
     `/Users/bharath/.gemini/antigravity/brain/c7c101ff-2ff2-4a2a-85d1-9a654a0e193c/scratch/backups/gocap-all-branches-pre-delete-2026-11-13.bundle`
3. **Dirty worktree SHA-256 manifest**:
   - `/Users/bharath/.gemini/antigravity/brain/c7c101ff-2ff2-4a2a-85d1-9a654a0e193c/scratch/backups/main-dirty-files.sha256` (`4/4` files verified `OK` after Phase 6).
   - Phase 3 note: zero non-live worktrees had uncommitted work, so zero Phase 3 commits were needed.

---

## 3. Branches Merged onto `main` (Phase 4)

Merged sequentially with `git merge --no-ff` in Fable's dependency/blast-radius order, verifying `ci:local` after every merge:

| Order | BranchMerged | Branch Tip SHA | Merge Commit on `main` | Scope & Verification Summary |
|---|---|---|---|---|
| 1 | `run2/c37e` | `1e216e9e19d365516855865167decb75185e9f80` | `e2ada97fbd528f04c25243d5e5b5c0149a20a0e6` | `ft-c37ee4e9`: read `gosree_commit` from `runtime/debug` VCS stamp (`3` files in `cmd/gosree-corpus`). `go vet` + `17/17` Go packages green. |
| 2 | `run2/f2e5` | `4215d6e64fff79be224a54d9ea77aeccd4264530` | `d4d302c9b550d6fee941b030b34b68ae438c82d6` | `ft-f2e5a81c`: bound `extractTar` names/depth and skip/count archive links (`7` files in `cmd/gosree-corpus`). `go vet` + `17/17` Go packages green. |
| 3 | `run2/5e61` | `c2081c64f563a70edd3d7bd8081a39b8342f5709` | `474f1a2ed9189a7e347db93d68907078100ab1a9` | Stack A (`ft-5e614fe8`): callee-first SCC phase 1, state-cell work budget, exact decode bounds at 262,144 functions (`15` files across `rust/`, `internal/gusseterr`, `passes/errcheck`). Rust fmt/clippy/`45` tests/release build + `CGO_ENABLED=0` & `CGO_ENABLED=1` `17/17` Go packages green. |
| 4 | `run2/6d5a` *(incorporating `run2/420d`, `run2/c7dd`, `run2/8f3c`, `run2/fe6e`)* | `55b1e86f8d99f1617d425c53bc8b4980b8a449aa` | `cb79d141b7ba5fd23173a2985c552b65c278f34f` | Stack B (`ft-420d34ac`, `ft-c7dd9d94`, `ft-8f3c2efd`, `ft-fe6eb6d8`, `ft-6d5a784f`): closure `(v, err)` tracking, all-blank discard fix, `os.Exit`/`log.Fatal*` defer-Close suppression, single-call closure replay for `ERR005`, `ERR003` quoting (`46` files). Updated `FINDINGS.md` fingerprint to `c4888388dc458b33`. `CGO_ENABLED=0` & `CGO_ENABLED=1` `17/17` Go packages green. |
| 5 | `swarm/integrate` *(incorporating `swarm/census`, `swarm/arch`)* | `9b104056bc6775b337864829e79d71fb740044a3` | `a43c90ad278c552b955be575617c978e1047646c` | Ownership v5 (`passes/ownership` MIR place footprints, field-projected Sync, directional `SharedRead`, self-locked `AutoSync` seam unification, `slices`/`maps.Clone` flow, `cmd/gosree-corpus` `G`-field walk comment; `32` files). Updated `FINDINGS.md` fingerprint to `7bb6c3826665ab6c` and stress ledger to `643/183/42`. `CGO_ENABLED=0` & `CGO_ENABLED=1` `17/17` Go packages green. |
| 6 | `run2/report` | `906237ea4138631864c32b6c596dac23058943c4` | `82ea5846cad3f1386efb384e2afefbff92cbda13` | `BUILD_REPORT.md` (through wave 7), `BUILD_REPORT-2026-11-11.md`, and `build-run-2026-11-12/*` (`5` files). Final `ci:local` (`45` Rust tests, `17/17` Go packages, `12/12` E2E acceptance tests across Tiers 1–4) green. |

---

## 4. Conflicts & Overlapping File Resolutions

Git's `ort` strategy reported **zero merge conflicts** across all six merges. Four files had multi-branch edits or metadata updates that were manually audited against their merge base and both sides before committing:

1. **`cmd/gosree-corpus/module.go` (Merge 2: `run2/c37e` vs `run2/f2e5`)**:
   - `run2/c37e` removed `"os/exec"`, added `"runtime/debug"`, and replaced `gosreeCommit()` (lines 272–337).
   - `run2/f2e5` added `"io"` and `diag io.Writer` on `moduleOpts` and passed `o.diag` to `archiveRev(...)` (lines 57, 96).
   - **Resolution**: Clean `ort` auto-merge preserved both changes and dropped unused `"os/exec"`; verified via inspection and `go test -count=1 ./cmd/gosree-corpus`.
2. **`passes/errcheck/errcheck.go` (Merge 4: `run2/5e61` vs `run2/6d5a`)**:
   - `run2/5e61` added package-path context to solver failure formatting (`safeerr solver failed on package %s: %w` at line 118).
   - `run2/6d5a` added closure effect / pair lowering hooks in distinct hunks.
   - **Resolution**: Clean `ort` auto-merge preserved both hunks; verified under `CGO_ENABLED=0` and `CGO_ENABLED=1`.
3. **`cmd/gosree-corpus/census.go` (Merge 5: `main` `9f4c592` vs `swarm/integrate` `e718708`)**:
   - `main` (`9f4c592`) added `reachCounted` visit counting at lines 643–694.
   - `swarm/integrate` (`e718708`) documented why `own()` walks `G`-classed fields at lines 866–869.
   - **Resolution**: Clean `ort` auto-merge preserved both `reachCounted` and the `G`-walk invariant; verified with `go test -count=1 ./cmd/gosree-corpus`.
4. **`tests/stress/FINDINGS.md` (Merges 4 & 5: checker fingerprint and stress ledger synchronization)**:
   - Combining `run2/5e61` + `run2/6d5a` changed `bash tests/stress/fingerprint.sh` from `1694ac132e12f19b` (`run2/6d5a` alone) to `c4888388dc458b33` (as anticipated in `BUILD_REPORT.md:52`), and merging `swarm/integrate` changed the fingerprint to `7bb6c3826665ab6c` and moved the `TestStress` ledger from `644 / 183 / 46` to `643 / 183 / 42` (`SAFE002`: `33/0/9` → `32/0/6`; `SAFE003`: `179/44/12` → `179/44/11`).
   - **Resolution**: Updated the `Checker fingerprint`, `SAFE002`, `SAFE003`, and `Total` rows in `tests/stress/FINDINGS.md` inside Merge 4 and Merge 5 to match the exact measured `fingerprint.sh` and `TestStress` outputs.

---

## 5. Branches Deleted (`git branch -d` Only) & Worktrees Removed

All **13** deleted branches were strict ancestors of `main` (`82ea5846cad3f1386efb384e2afefbff92cbda13`) and were deleted with `git branch -d` (**zero** `git branch -D` calls were used). No remote branches were touched.

| Deleted Branch | Former Tip SHA | Backup Ref | Reachable From `main` Via |
|---|---|---|---|
| `swarm/errscale` | `80a766bfe20e69d554e7ec8dd0725ad1d75f1ab8` | `refs/backup/2026-11-13/swarm/errscale` | `0ff82c0` (already on `8df1d77`) |
| `run2/c37e` | `1e216e9e19d365516855865167decb75185e9f80` | `refs/backup/2026-11-13/run2/c37e` | `e2ada97` (Merge 1) |
| `run2/f2e5` | `4215d6e64fff79be224a54d9ea77aeccd4264530` | `refs/backup/2026-11-13/run2/f2e5` | `d4d302c` (Merge 2) |
| `run2/5e61` | `c2081c64f563a70edd3d7bd8081a39b8342f5709` | `refs/backup/2026-11-13/run2/5e61` | `474f1a2` (Merge 3) |
| `run2/420d` | `db244aa1ec742d8a449aa5d7f8b975e00f190ab7` | `refs/backup/2026-11-13/run2/420d` | `cb79d14` (Merge 4 via `run2/6d5a`) |
| `run2/c7dd` | `e049a2c3301f9e733533ab97838b9d7436afbc6d` | `refs/backup/2026-11-13/run2/c7dd` | `cb79d14` (Merge 4 via `run2/6d5a`) |
| `run2/8f3c` | `290f3fcbdf8f4ea601e5217952ad40484bdbd076` | `refs/backup/2026-11-13/run2/8f3c` | `cb79d14` (Merge 4 via `run2/6d5a`) |
| `run2/fe6e` | `24d21ac06357d530ba8e3ca87abd7aeb82c98ea6` | `refs/backup/2026-11-13/run2/fe6e` | `cb79d14` (Merge 4 via `run2/6d5a`) |
| `run2/6d5a` | `55b1e86f8d99f1617d425c53bc8b4980b8a449aa` | `refs/backup/2026-11-13/run2/6d5a` | `cb79d14` (Merge 4) |
| `swarm/census` | `e7187082ef7331bc73ff34ec296b536d31ec25e8` | `refs/backup/2026-11-13/swarm/census` | `a43c90a` (Merge 5 via `swarm/integrate`) |
| `swarm/arch` | `f9e2af057de011ec664c0b31fe5ee0c3a2502c78` | `refs/backup/2026-11-13/swarm/arch` | `a43c90a` (Merge 5 via `swarm/integrate`) |
| `swarm/integrate` | `9b104056bc6775b337864829e79d71fb740044a3` | `refs/backup/2026-11-13/swarm/integrate` | `a43c90a` (Merge 5) |
| `run2/report` | `906237ea4138631864c32b6c596dac23058943c4` | `refs/backup/2026-11-13/latest/run2/report` (and `4884b13c` at `refs/backup/2026-11-13/run2/report`) | `82ea584` (Merge 6) |

Worktrees removed via `git worktree remove` (after confirming clean `git status --porcelain` and zero live sessions):
- `/Users/bharath/.gemini/antigravity/brain/c7c101ff-2ff2-4a2a-85d1-9a654a0e193c/scratch/wt/errscale`
- `/Users/bharath/.gemini/antigravity/brain/c7c101ff-2ff2-4a2a-85d1-9a654a0e193c/scratch/wt/census`
- `/Users/bharath/.gemini/antigravity/brain/c7c101ff-2ff2-4a2a-85d1-9a654a0e193c/scratch/wt/arch`
- `/Users/bharath/.gemini/antigravity/brain/c7c101ff-2ff2-4a2a-85d1-9a654a0e193c/scratch/wt/integrate`
- `/Users/bharath/.gemini/antigravity/brain/c7c101ff-2ff2-4a2a-85d1-9a654a0e193c/scratch/wt/arch-base`

---

## 6. Branches Kept (With Reasons & Owner Flags)

No `release/*` branches exist in the repository. Four local branches remain:

| Kept Branch | Current Tip SHA | Backup Ref | Reason Kept & Owner Flag |
|---|---|---|---|
| `main` | `82ea5846cad3f1386efb384e2afefbff92cbda13` | `refs/backup/2026-11-13/main` (`8df1d77b`) | Trunk; checked out in `/Users/bharath/Code/devtools/gocap` with 3 live Claude sessions and uncommitted gate docs (`ft-11eb6b73`). |
| `run2/c7dd-err005-wip` | `beb8a61b1cf55efda2095bbe108af2ccfcf558c1` | `refs/backup/2026-11-13/run2/c7dd-err005-wip` | **Flagged for owner**: set-aside `ERR005` attempt (`10` false positives) kept per Fable's explicit ruling (`BUILD_REPORT.md:184`) as a fixture/design spec for `ft-e92e722c`. |
| `run2/8462` | `0aafc953a4d00a7ce589ee8d3dbeaa8b908dd694` | `refs/backup/2026-11-13/latest/run2/8462` | **Flagged for owner**: created and advanced mid-consolidation by live session `a76482b2` (wave 7, `ft-8462ae87` preflight scanner fix, leaving the `GOSREE_JSON_CHILD` half for owner decision per `906237e`). Kept untouched because it was active during this run and arrived after Phase 1 inventory. |
| `run2/e92e` | `91fad0b73a56d0601d660aa8bdc0504882f02d65` | `refs/backup/2026-11-13/latest/run2/e92e` | **Flagged for owner**: created and advanced mid-consolidation by live session `a76482b2` (wave 7, `ft-e92e722c`: `ERR005` path-keying design doc + pinned fixtures, with code implementation intentionally paused per `906237e`). |

---

## 7. Branches Blocked

- **None (`0` branches failed `ci:local` or conflicted)**. The three unmerged branches kept above (`run2/c7dd-err005-wip`, `run2/8462`, `run2/e92e`) were intentionally skipped per policy (spec branch / live wave-7 branches), not blocked by merge conflicts or red CI.

---

## 8. Restore Commands for Any Deleted Branch

### Option A: Restore directly from in-repo `refs/backup/2026-11-13/*`
```bash
git -C /Users/bharath/Code/devtools/gocap branch swarm/errscale  refs/backup/2026-11-13/swarm/errscale
git -C /Users/bharath/Code/devtools/gocap branch run2/c37e       refs/backup/2026-11-13/run2/c37e
git -C /Users/bharath/Code/devtools/gocap branch run2/f2e5       refs/backup/2026-11-13/run2/f2e5
git -C /Users/bharath/Code/devtools/gocap branch run2/5e61       refs/backup/2026-11-13/run2/5e61
git -C /Users/bharath/Code/devtools/gocap branch run2/420d       refs/backup/2026-11-13/run2/420d
git -C /Users/bharath/Code/devtools/gocap branch run2/c7dd       refs/backup/2026-11-13/run2/c7dd
git -C /Users/bharath/Code/devtools/gocap branch run2/8f3c       refs/backup/2026-11-13/run2/8f3c
git -C /Users/bharath/Code/devtools/gocap branch run2/fe6e       refs/backup/2026-11-13/run2/fe6e
git -C /Users/bharath/Code/devtools/gocap branch run2/6d5a       refs/backup/2026-11-13/run2/6d5a
git -C /Users/bharath/Code/devtools/gocap branch swarm/census    refs/backup/2026-11-13/swarm/census
git -C /Users/bharath/Code/devtools/gocap branch swarm/arch      refs/backup/2026-11-13/swarm/arch
git -C /Users/bharath/Code/devtools/gocap branch swarm/integrate refs/backup/2026-11-13/swarm/integrate
git -C /Users/bharath/Code/devtools/gocap branch run2/report     refs/backup/2026-11-13/latest/run2/report
```

### Option B: Restore from the external git bundle (if backup refs are ever pruned)
```bash
BUNDLE="/Users/bharath/.gemini/antigravity/brain/c7c101ff-2ff2-4a2a-85d1-9a654a0e193c/scratch/backups/gocap-all-branches-pre-delete-2026-11-13.bundle"
# Restore a single branch <branch> (e.g. swarm/integrate):
git -C /Users/bharath/Code/devtools/gocap fetch "$BUNDLE" "refs/heads/<branch>:refs/heads/<branch>"
```
