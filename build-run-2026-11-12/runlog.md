# Run log — gocap backlog run, 2026-11-12 (session a76482b2)

## Pre-wave orientation (~20:10–20:30 CST)
- No AUDIT_REPORT.md, so ordering falls back to priority → severity → dependencies. 28 cards: 8 review, 10 backlog, 10 inbox.
- gitpulse_insights: the collisions facet came back ok:false (deadline expired, 0 of 6 worktrees scanned). The substitute was a direct git measurement of each worktree, labelled as such. gitpulse_collision_risk on each planned path was later clean (6 of 6 worktrees scanned, no holders).
- Held paths:
  - Antigravity swarm/arch (5 ahead) and swarm/integrate (mid-merge, UU, live pid 18234) hold passes/ownership/**, internal/kinds/** and stress testdata.
  - swarm/census holds cmd/gosree-corpus/census.go.
  - Session 45c62196 holds uncommitted PROJECT.md, README.md, docs/ARCHITECTURE.md and docs/TOOLING_AND_ERASURE.md in the main checkout.
  - gocap-55 (b3738d58) is committing ownership work on main (last 17:54).
- devmap_status: fresh, generation 521, 2,416 symbols. Impact for lower.go was 166 in total with 80 shown (truncated). For wire.go: 783, 80 shown (truncated). Unknown, so not safe. For refservice.go: 31/31, complete.
- dev doctor: no task store, gate mode off, so `dev verify` cannot run. Review is the ceiling.
- Disk: data volume 76 GiB free of 926 (8.2%) at the start, then 67 GiB at 20:25. Go build cache 42 GiB (live), huggingface 30 GiB (user's).
- Gate warnings: writing /Users/bharath/Code/devtools/.gocap-run2 is outside the project root. GitPulse would refuse it; it went through only because of bypass mode. Worktrees must live outside the main checkout, which is why it was needed.

## Fable rulings (20:30)
1. Disk: do not wipe GOCACHE. Proceed on the 40 GB floor and record the 15% miss. At most two worktrees at once. Remove each card's cargo target when it reaches review. Trigger: below 50 GB before a wave, or below 40 GB at any time, means clean this run's artifacts; still under 40 GB means halt.
2. Stack A: ft-5e61 → ft-bc5b. Stack B (on main f0dea98): ft-420d → ft-c7dd → ft-8f3c. One safeerr card at a time. Test both engines on a throwaway merge of the two tips before review. Inbox cards are reproduced first.
3. ft-f2e5: all three f138keep files are in scope (the 3.8 MB in the report = 3,979,429 bytes summed). census-after2 is the reference. The before/after census at today's code must be byte-identical; report the skip count; a fixture must fail before the fix.
4. ft-a0d4: split. Run the exit-code table, RB-JSONDUP and ER-23 now if disjoint. Timing items wait for an idle machine. errflow items are obsolete.
5. Vetoes:
   - wiping GOCACHE, huggingface or build/ref-item4;
   - edits under held paths;
   - timing claims tonight;
   - run records written to main;
   - calling any card done.

## Wave 1 (started 20:35)
- Infrastructure: run dir /Users/bharath/Code/devtools/.gocap-run2, with a gusset symlink and base worktree (detached f0dea98). Baseline cargo build/test and vet passed (rc=0). The full go test was running.
- ft-5e61 → in_progress (rev 3). Worktree w-5e61, branch run2/5e61. Sonnet lane.
- ft-f2e5 → in_progress (rev 2). Worktree w-f2e5, branch run2/f2e5 (rust/target symlinked to base). Sonnet lane.
- ft-a0d4 split: a read-only exit-code table lane against base, with no worktree.
  - RB-JSONDUP already has a regression lock (tests/stress/vetparity_test.go:502); the baseline suite checks it.
  - ER-23 is deferred: its fix lands in docs/TOOLING_AND_ERASURE.md and docs/ARCHITECTURE.md, held by 45c62196.
- ft-420d reproduced on f0dea98 in both engines (scratchpad/repro420). ERR002 fires at the closure's store (p.go:20:11), not at use(v) as the card says. Unchecked reports at the same site; Direct is clean. Brief ready at briefs/420d.md.

## ft-a0d4 exit-code item (read-only on f0dea98)
- Verified, 108 runs: gosree, 6 targets × 9 modes. The cgo and CGO_ENABLED=0 builds give identical statuses. There is no contradiction with README §4, RULES.md:35, TOOLING §1.3.1/§1.3.2 or the preflight.go constants.
  - plain/-json: clean 0, findings 3, load failure 1, missing package 1.
  - -fix/-diff: 2.
  - -tags: 3 when the tag is present, 0 when absent.
  - -test=false and -NAME=false warn on stderr.
  - go vet -vettool: 0/1/1.
- gosree-gate under `go build -toolexec`: the build exits 1 because go rewrites the gate's 3. The (uncommitted) TOOLING §1.6 in the main checkout already says this. No new card.
- Table: scratchpad/exitcodes/table.md (+ results.tsv).
- Card NOT updated: gitpulse_complete_task accepts only in_progress/review/done, and moving a mostly-deferred card to in_progress would misstate it. The result lives here and in BUILD_REPORT. Tool gap to report.

## ft-f2e5 implementation returned (time not recorded, before 21:00)
- Commit c3fa5f9 on run2/f2e5. Six failing-before tests are verified by the lane; the cmd/gosree-corpus suite passes in both engines (lane-verified).
- Opus review of the code: approved.
  - skippedAncestor refuses entries at or beneath a skipped link.
  - The trap test is unchanged and passes.
  - Signature changes: archiveRev gains a diag io.Writer; extractTar returns an extraction struct.
- Held-file violation: the lane edited README.md and docs/TOOLING_AND_ERASURE.md, which 45c62196 holds uncommitted.
  - A 3-way merge-file against their uncommitted text found 0 conflicts. Even so, the edits were reverted by rule in a6827a5, with the sentence recorded in the commit message.
  - The cause was my brief, which did not list those files as held. common.md was fixed for later lanes.
- Criterion 2 (orchestrator-verified):
  - ScholarLM bb60b53 backend/go_orchestrator has 0 link entries, so the skip count is 0 and the change is a no-op on this archive.
  - census.json is not byte-identical across runs even of IDENTICAL code (after vs after-b).
  - by_worst totals, with_post_construction_writes 264, unresolved 18, and per-type sorted closure and own-class kinds all hash identically (9fe0b1bc5582) across before, after, after-b and f138keep/census-after2.json.
  - Only the witness text and the order within reason lists vary.
- New card ft-32cd98a3df42498c7c1be93cf379a521 (inbox, p3): census witness and order nondeterminism. It is separate from ft-8e79, which covers class order-independence.
- Adversarial pass started (same worktree).

## Disk event (before 21:00)
- Free fell to 47 GiB (Fable trigger <50). This run held ~0.4 GB total. Removed exitcodes/bin, exitcodes/mod and mf/ (~70 MB). The drop was driven by other sessions (go-build 42→45 GiB, other projects' scratchpads up to 8.5 GB). Continuing: still above 40 GB.

## Baseline suite (f0dea98, 20:20–20:51)
- cargo build/test, go vet and go test -timeout 60m -count=1 ./... with cgo: all 17 packages ok (passes/ownership 1836 s, tests/stress 429 s, tests/gates 589 s). Log: scratchpad/base/gotest.log. No failures, so no pre-existing flakes in this run.
- RB-JSONDUP (ft-a0d4): TestVetParity runs (skipped only under -short) and PASSES. It asserts that standalone -json reports no finding twice (vetparity_test.go:502-510). Verdict: fixed and locked; the item closes.

## ft-f2e5 adversarial pass (time not recorded, before 21:00)
- The adversary committed 0c266e0 (tests only). 4 breaks, none a safety violation: (1) skippedAncestor is quadratic on long PAX names; (2) a link at the subdir root is uncounted (rel=="" continue); (3) a link cleaning to "." does not guard its children; (4) a nil diag panics. 48 other top-level tests pass in both engines.
- Defended attacks: spellings (28 subcases), case-only differences, NFC/NFD, links with bodies, PAX/GNU long names, special types, prefix-not-path, 100k links (linear allocation, ratio 4.00), truncation, a failing diag writer, link-free archive parity with f0dea98.
- Opus: sent back to the implementer. Fix: a name-length bound plus a single index walk; root links counted and guarding; nil diag is an error. The CPU-ratio test is replaced with deterministic tests: timing is not evidence tonight.

## HALT (before 21:00; exact time not recorded)
- Free disk fell to 38 GiB (below the 40 GB floor). This run holds ~350 MB (base 213M, w-5e61 79M, w-f2e5 ~5M, scratch 56M), so cleaning it cannot restore 40 GB. Others: go-build 45G, /private/tmp/claude-501 35G across sessions.
- Per the Fable ruling: halted. No new work. Both lanes (5e61 implementing, f2e5 fixing adversary breaks) were told to commit WIP on their own branches and stop builds. Nothing was deleted. Resume when free space is >= 40 GB (preferably >= 50 GB) or on the owner's decision.

- f2e5 lane halted: 46e34ad "WIP/untested" on run2/f2e5. All 4 breaks written; nothing compiled or tested. Review points on resume: (a) the 4096 bound vs macOS PATH_MAX 1024, where the at-bound extraction test may fail, so lower the bound rather than weaken the test; (b) the TestAdvBreakLinkAtRootUncounted expectation was changed to require an error. Check that the archive has entries after the root link, so the change matches the rule rather than a convenience.

- 5e61 lane halted: 80c50af WIP on run2/5e61. A–F written. Go-oracle tests pass (CGO_ENABLED=0). Rust untested: fmt/clippy/test/release-build pending, then the cgo go test and interleaved benches.
  - Work counter (oracle), hub-and-chain per function: f0dea98 4,101 @4,096 and 16,389 @16,384 (quadratic). After: 3.0 at 4,096, 16,384 and 65,536 (linear).
  - Budget 64x package size (floor 1,048,576). Max observed work/size ratio: 0.050 over 269 repo packages and 319 stdlib packages; 1.10 on synthetic random cyclic graphs.
  - MaxDecodedBytes 2 GiB. The encoder sizes each function before appending.
  - Test expectations changed (review on resume): the callgraph-cap tests now drive the cap with a cycle; TestHeapSweepsMatchScanSweeps became TestSCCPhase1MatchesScanSummaries and drops the old sweep-count pin.
  - Gaps the lane named: the Rust test-only encode_package has no encoder bound, and the native response cap is untestable without a 1 GiB response.

## RESUME 21:02
- Free space recovered to 56 GiB (the background watch fired). Collision risk re-checked on all 7 planned paths: 9 worktrees scanned, no holders; main still f0dea98. Both lanes resumed from their WIP commits. Threshold monitor re-armed.
- Earlier timestamps in this log written as estimates were wrong and have been replaced with "not recorded".

## ft-f2e5 fix round (after resume)
- e5d1387 fixes all 4 breaks. Opus accepted the root-link expectation change (the archive has svc/a.go after the link). The cgo package run: ok 178 s.
- No-cgo whole-package run: 5 census tests failed with "bad checksum" / "internal/profilerecord is not in std". Rerun alone, they pass. Orchestrator check: go1.27.3, GOTOOLCHAIN=auto, go.mod go 1.27, and stdlib export data for internal/profilerecord and runtime now builds cleanly. Inference: cache entries truncated during the disk-full episode, detected by checksum and rebuilt; not persistent corruption.
- Sent back: (1) a depth bound, since 2048-component names cost O(depth^2) os.Root walks (97 s for one test), a stall vector the length bound does not stop; (2) a whole-package no-cgo rerun, with a private GOCACHE if the errors recur.

- f2e5: Opus found a silent no-op (non-dir entry cleaning to root dropped). Fixed in 4215d6e; the test failed before (files=0, err=nil). Both engines pass the whole package (no-cgo 37.4 s, cgo 34.7 s). Bounds: maxEntryName 4096, maxEntryDepth 128. The depth cost predates the change (34 s for one 2048-component entry, link-free path). Opus approved. Full go vet + go test ./... on run2/f2e5 started in the background (DoD) before moving the card to review.

- ft-f2e5 → REVIEW (rev 3) after the full suite went green on run2/f2e5 (vet ok, all 17 packages ok, 21:19–21:42). Worktree w-f2e5 removed (clean; only the rust/target symlink to base). Branch run2/f2e5 kept (in review, unmerged).

## Holdings re-check after the f2e5 review move (~21:45)
- swarm/integrate committed its merge 9b10405 (clean, active ~4 min earlier); swarm/arch is clean with 5 unmerged commits; the main checkout still has 4 uncommitted docs (45c62196); main is still f0dea98. The ownership/census/doc holdings stand, so no other card is eligible. Next: ft-420d once ft-5e61 leaves the safeerr slot (Fable: one safeerr card at a time).

## ft-5e61 implementation returned
- 65e2cd4 (one commit, replaces the WIP). 10 files, +2006/-144. All 4 criteria met (lane-verified): cargo fmt/clippy/test (21 unit + 14 integration), release build, go vet, gusseterr+errcheck in both engines, hostile-frame cross-engine agreement and fuzz seeds under cgo.
- Work counter: f0dea98 4,101 and 16,389 units per function (quadratic) → 3.0 at 4,096, 16,384 and 65,536. Budget factor 64 / floor 1,048,576. Max ratios: 0.050 real (269 repo + 319 stdlib packages), 1.10 synthetic.
- Bench allocations (interleaved, 6 each): Rust unchanged (8 allocs; B/op +1 byte at 200 funcs); oracle allocs/op -1.7
## ft-5e61 implementation returned
- 65e2cd4 (one commit, replaces the WIP). 10 files, +2006/-144. All 4 criteria met (lane-verified): cargo fmt/clippy/test (21 unit + 14 integration), release build, go vet, gusseterr+errcheck in both engines, hostile-frame cross-engine agreement and fuzz seeds under cgo.
- Work counter: f0dea98 4,101 and 16,389 units per function (quadratic) -> 3.0 at 4,096, 16,384 and 65,536. Budget factor 64 / floor 1,048,576. Max ratios: 0.050 real (269 repo + 319 stdlib packages), 1.10 synthetic.
- Bench allocations (interleaved, 6 each): Rust unchanged; oracle allocs/op down 1.7 and 5.4 percent, B/op down 1.6 and 4.8 percent. Oracle wall-clock indicative 3.8->5.0 ms and 0.70->0.91 ms under load: unexplained, so CPU-profile attribution was put in the adversarial pass.
- Opus review of the core loop: correct. Later-SCC callers keep dirty=true and are not queued early; budget checked per solve; cap per SCC.
- Open items: the phase-1 budget error reaches errcheck as a wrapped string on the native path; errcheck.go:116 has no deadline (out of scope); the test-only Rust encode_package has no frame bound.
- Fable ruling: keep MaxDecodedBytes at 2 GiB (1 GiB would refuse the card's own 100 MiB/1,139 MiB case; the ~2.9 GiB peak is below the 8.7 GB real runs reach). Conditions before review: (1) both engines refuse at the same modelled count, tested at the bound and one over; (2) the refusal is loud and names the package and the bound; (3) docs state the peak as decoded program + largest function state; (4) record frame size + modelled decoded bytes for ScholarLM internal/wisdev with tests, and go back to Fable if that exceeds 1 GiB.
- Adversarial pass started on w-5e61.

## ft-5e61 adversarial pass
- Commits 14ad9d2 (Go) and cd32415 (Rust), tests only.
- Defended: fixpoint equivalence (1,500 graphs; byte-identical engines; never more work than the old scan); cap soundness (600 graphs, never optimistic, never refuses what the old cap accepted); budget boundaries and overflow; decoder hostile claims and 1,200 mutated frames with engine agreement; encoder; Tarjan cancellation on the 262,144 chain.
- Perf: the 3.8->5.0 ms oracle figure was NOISE. New phase-1 code is at most 0.4 percent of CPU samples; decodePackage rose about 1 point from the charge calls; -count 10 medians are equal within spread; allocations down 1.6-1.7 percent.
- Breaks: (A) a closed hub-and-chain is refused from n=1024 although f0dea98 accepted it (real-source gosree exit 0 -> 1); (B) the work unit ignores slots (157-604x cost spread at equal units); (C) native and oracle error text differ, and errors.Is is unusable; (D) the decoded charge omits slice-growth slack (live heap 2.16x the charge in Go, 1.41x in Rust). Soft spot: cancel latency is a whole sweep inside a giant cycle.
- Opus disposition, all sent back: (A) order SCC members callee-first by Tarjan finish rank, which is the root cause; the budget stays as the backstop; (B) unit = ops + blocks*max(slots,1), re-measure, re-pick the factor; (C) typed sentinels in both engines; (D) preallocate decoded slices to their exact counts, not pad the model; (E) poll cancellation per solve. Plus the 4 Fable conditions, including the package path in the errcheck.go:117 wrap (a one-line touch in passes/errcheck, a file nobody holds) and the wisdev frame measurement.

## ft-5e61 fix round and the Go 1.27.4 toolchain break (00:00-00:30, 2026-11-13)
- c2081c6 fixes A-E in both engines:
  - A: in-SCC order by reverse DFS finish rank; the closed hub-and-chain is now linear (work/size 1.33 at n=1024/4096/16384).
  - B: unit = ops + blocks*max(slots,1); real max ratios 0.004 (repo, std) and 0.437 (ScholarLM 922 packages), synthetic 1.0; factor 64 / floor 2^20 kept.
  - C: exported sentinels; identical text in both engines; protocol code via a one-line rust/gusset-engine/src/lib.rs change.
  - D: exact-capacity decode; live/charge 1.04 (Go) and 0.71 (Rust).
  - E: cancellation polled per solve.
  - Still quadratic: hubLadder (each member calls both neighbours and the hub); also quadratic on f0dea98; refused from about 1,000 functions. Criterion 1 allows failing closed.
- Fable 2 GiB conditions met: golden model parity (30,389/5,785/299); errcheck.go:117 names the package; docs state the peak (3.7 GiB Go oracle, 2.3 GiB Rust at the extreme); wisdev with tests = 18,973 funcs, 5,761,732-byte frame, 24,511,516 modelled decoded bytes (0.023 GiB, 1.1 percent of the bound).
- Opus review: approved. Every adversary expectation change carries an EXPECTATION CHANGED reason and still asserts a bound (2x per graph and <=1x in total, flips+1 cap, per-unit cost ratio). The test unit mirror matches production (ops added at line 53).
- TOOLCHAIN: Homebrew moved /opt/homebrew/bin/go to 1.27.4 at 23:19 on 2026-11-12. go1.27.4 security.go scans every #cgo -L dir recursively and refuses LTO sections; libgusset.a carries __LLVM,__bitcode (810 otool lines), so every cgo build of gosree fails, f0dea98 included (verified). Filed ft-eddc14e5 (p1, high).
- Fable ruling: verify cgo with /opt/homebrew/Cellar/go/1.27.3/libexec/bin/go by absolute path, recording go version; no bypass of the check; this run does not attempt the gusset fix (owner and gusset lane; pin bump needs approval); cards may go to review labelled "cgo verified on go1.27.3; go1.27.4 cgo blocked by ft-eddc14e5", cleared before any merge.
- ft-f2e5 labelled (rev 4); its suite ran 21:19-21:42 on go1.27.3, before the upgrade.
- Full suite on run2/5e61 started: cargo release, go1.27.3 vet and test ./... with cgo, plus CGO_ENABLED=0 gusseterr+errcheck on go1.27.4.

## Wave 1 closed / wave 2 started (~00:35, 2026-11-13)
- ft-5e61 -> REVIEW (rev 4) after the full gate on c2081c6: go1.27.3 vet and test ./... with cgo, 17/17 ok; go1.27.4 CGO_ENABLED=0 gusseterr+errcheck ok. Labelled cgo-on-1.27.3.
- Cleanup: w-5e61 cargo clean (337.6 MiB, 3,007 files) and the worktree removed; branches run2/5e61 and run2/f2e5 kept (in review). Free 95 GiB.
- Post-wave checks: insights collisions ok (7/7 scanned, 0 overlapping), DevMap fresh (gen 526), collision risk clean on lower.go, errcheck_test.go and testdata. Main moved f0dea98 -> 8df1d77 (3 commits by another lane: census.go reach-test bound, adv2_reach_test.go, FINDINGS.md; no errcheck/gusseterr/rust change, verified by git diff --quiet).
- common.md gains the Fable toolchain rule (go1.27.3 by absolute path for cgo).
- Wave 2: ft-420d -> in_progress (rev 2). Worktree w-420d on main 8df1d77, branch run2/420d, rust/target symlinked to base. Sonnet lane spawned. ft-bc5b waits (one safeerr card at a time).

## ft-420d implementation returned
- 4b7ffd3 on run2/420d (lower.go + freevar_cells_1111_test.go + fixture errclosurepair; +572/-2). Lane-verified: criterion 1 (fixture failed on unmodified code in both engines), 2 (Unchecked still reports, now at use(v) p.go:33:12), 3 (TestDump er_*/rs_errors 45 diagnostics identical; TestStress ledger 627/173/46 identical); self-run on the repo byte-identical (187 cgo / 179 nocgo lines). Rust/oracle unchanged.
- Opus review: deviation 1 (closure in a variable, called once, tracked via SSA promotion) accepted; deviation 2 (conditionally called closure stays reported at the store, a residual FP that fails closed) accepted and to be documented. The removed reports for ClosureNeverUsed/ClosureReassigned match their silent no-closure twins. The lane's ownership timeout was the default 10 min; the package is unchanged and the orchestrator gate will run -timeout 60m. The commit trailer names Sonnet (truthful; left as is).
- New card ft-6d5a784f (p3): ERR003 message double-quotes local names (describeSSAVal returns quoted names, errcheck.go:282 quotes again; verified by reading). Closure-twin wording difference folded in.
- Adversarial pass started on w-420d (focus: false negatives the fix could introduce).

## ft-420d adversarial pass
- cd804ed: adv_420d_test.go plus fixtures errclosurepairadv (about 130 expected diagnostics) and errclosurepairbreak.
- Defended: dominance evasion (goto, labels, select, fallthrough, panic/recover, zero-trip loops, early return); non-load uses of v; aliases and crossed errs; reassignment and second closures; generics and method values; value types; determinism (20 runs per engine, identical; engines byte-identical, 135 lines).
- BREAK (false negative, removed a true positive): named results. func f() (v *T, err error) { func(){v, err = workV()}(); return v, nil } is silent after the fix; base and the twin report ERR002. buildClosurePlan accepts result cells; Return lowering does not flag cell-loaded errs.
- BREAK (perf): buildClosurePlan rescans the parent per closure (quadratic; +47/155/805 ms at 2k/4k/8k closures, indicative).
- Sent back: exclude result-parameter cells fail-closed (or prove a general Return fix on all named-result shapes); index the parent once per function, with a deterministic work counter showing linear cost.

- ft-420d fix round 90fa6e9: named-result cells excluded by name (fail closed; brk.go cases report at the store as on base; residual FPs NamedChecked and C_Named documented). Parent indexed once per function (MakeClosure sites, named results, a block dominating every return); plan work is 21 per closure at 500, 2,000 and 8,000 (deterministic counter, TestClosurePlanWorkIsLinear). Both engines pass passes/errcheck; ledger and dump identical; self-run identical except one line-number shift. Opus approved.
- Gates started (background): full go1.27.3 vet and test ./... on run2/420d plus nocgo errcheck/gusseterr; then a throwaway merge of run2/420d with run2/5e61 (merge-tree clean; errcheck.go touched by both) tested on errcheck, gusseterr and stress in both engines; the merge worktree is then removed and cargo-cleaned.

## Wave 2 closed / wave 3 started (~01:50)
- ft-420d -> REVIEW (rev 3). Gates 01:24-01:45: go1.27.3 vet ok; go1.27.3 test ./... with cgo 17/17 ok; go1.27.4 nocgo errcheck+gusseterr ok. Fable stack rule: the throwaway merge run2/420d x run2/5e61 (865eada, errcheck.go auto-merged) passed errcheck, gusseterr and stress with cgo, and errcheck and gusseterr nocgo; the merge worktree was removed and cargo-cleaned.
- w-420d removed (only the rust/target symlink was untracked); branch kept. w-c7dd created on run2/c7dd from run2/420d tip 90fa6e9.
- ft-c7dd -> in_progress (rev 4). The fixtures advfv3b3c/advfvrange3b3c pin the ERR003 blank-discard FP as want (r.go:30, a.go:68). Docs ruling (RULES.md:614 explicit discard accepted; :690 overwrite concerns error variables) makes it a FP to fix. Brief demands Step 0 reproduction, pin flips with written reasons, ERR005 fail-closed toward reporting. Sonnet lane spawned.

## ft-c7dd implementation returned
- 567e3d5 (ERR003: all-blank calls recorded by exact Lparen in checkBlankedTupleAssign; root cause NOT the loop-header join the brief guessed), 69eb7b6 (ERR005: resourceSlot keyed by root + field/deref path; misses existed WITHOUT closures, another brief correction), a065635 (ERR005 across closures: per-closure effect summaries replayed in the creator; closureeffects.go). Lane-verified: both engines pass errcheck+gusseterr; 15 missing ERR005 wants + 5 ERR003 FPs on the tip; determinism 10x/engine; work counter linear (53/resource at 500/2k/8k); self-run -1 FP (fuzz_test.go:98 ERR003), +0.
- Blocker: TestStress fails on er_w_rangecapture/main.go:40 MISS(ERR005), now correctly reported. The orchestrator checked: NOT held (collision risk clean over 8 worktrees; no lane branch or working tree touches it). The blanket tests/stress/testdata hold in common.md was over-broad and has been narrowed to a per-file check. FINDINGS.md is also unheld.
- Sent back: promote the marker and update FINDINGS.md totals/fingerprint/run row; test (not infer) the 3 untracked shapes (captured-var store in closure, field-path across closures, double-called closer dedup); run the DevMap impact/affected_tests the lane skipped; re-run vet and both engines after the final edits.

- ft-c7dd round 2: 359cc65 (field-path effects replayed across closures; edge fixtures errc7ddedge) and 498984f (er_w_rangecapture:40 MISS->HIT). Ledger 628/172/46, ERR005 row 8/0/1, fingerprint 5f12ce77897ea191, FINDINGS.md totals/fingerprint/run rows updated. (a) captured-var store in a closure stays a MISS with its reason (fixing it would contradict the advfvbreak3b3c clean pin; it was also a miss on the tip). (b) field paths across closures were misses and are now fixed. (c) a double-called closer reports once per line (dedupKey per line+rule). DevMap: the index predates closureeffects.go, so new symbols are unsearchable (a recorded GAP); resourceSlot and checkBlankedTupleAssign impact walks are truncated (walk_incomplete, 16 unbound calls); affected tests all run. Both engines pass errcheck, gusseterr and stress; vet ok; self-run -1 FP +0. Opus approved; adversarial pass started.

## ft-c7dd adversarial pass, attribution, split (~03:00)
- Adversary beb8a61 vs 90fa6e9: 10 new FPs, 3 new misses, 3 duplicate diagnostics, quadratic nesting work; ERR003 fully defended; determinism and engine parity defended.
- Attribution (orchestrator, scratch archives, CGO_ENABLED=0): 567e3d5 ERR003 = 0 FP / 0 miss (clean); 69eb7b6 path keying = 5 FP + 3 misses (its own tests do not compile: duplicate TestC7ddBlankDiscardInLoop); a065635 replay = +5 FP, dup, quadratic nesting; 359cc65/498984f unchanged. Pre-existing on 90fa6e9: nested FieldAddr chains, dup line 32.
- Root cause of the regression class: MY BRIEF (untracked shapes must keep reporting where a write may have happened). Correct invariant: untracked = identical to the tip in both directions (as in ft-420d). Advisor consulted.
- Fable ruling: no reset (history rewrite is ask-first); rename to wip and cut a fresh branch; one bounded replay round on its own branch, belonging to the new card, with hard stops (new FP/miss, duplicates, non-linear nesting); path keying parked; criterion 1 needs MISS(ERR005) stress markers (fixture comments do not count), checking ft-d71c for double-counting; c7dd goes to review with the ERR005 half explicitly unfixed.
- Done: run2/c7dd -> run2/c7dd-err005-wip (beb8a61, kept); new run2/c7dd = 90fa6e9 + cherry-pick 567e3d5 = 9c2f16f. New card ft-fe6eb6d8 (p2, medium) with the wip adversary fixtures as spec. The lane is finishing: ERR003 adversary tests ported, MISS(ERR005) markers, FINDINGS.md.
- Fingerprint check (advisor item): FINDINGS.md records 84db323e0c9300ae on every branch; computed on run2/5e61 c2081c6 = be551257f30972dc and on run2/420d 90fa6e9 = 20a350770bc97205. The ledgers were re-run and unchanged on both; only the fingerprint row is stale. To be noted on those cards.

- ft-c7dd ERR003-only finished: 73faed8 (ERR003 adversary cases ported, errc7ddblankadv) and 96827ec (14 MISS(ERR005) markers in a new unheld root er_closureclose naming ft-fe6eb6d8; ft-d71c overlap clean; ledger 627/187/46, delta +14 misses; ERR005 row 7/15/1; fingerprint b182dec1ca9fc4b4). Lane-verified: both engines pass errcheck, gusseterr and stress; vet ok; self-run delta only fuzz_test.go:98 ERR003 removed.
- Flake: TestWitnesses/lk_wit_maplookup failed once in a full cgo stress run ("runtime did not convict"); passed 3/3 alone and on a full rerun. The same class as the intermittent TestWitnesses failure on ft-a0d4. Not attributable to errcheck changes (race witnesses run real programs under -race).
- Adversarial step for the ERR003 scope: satisfied by the earlier pass; the attribution showed 567e3d5 clean against all Break fixtures.
- Gates started: full go1.27.3 suite on run2/c7dd plus nocgo, then a throwaway merge run2/c7dd x run2/5e61 in both engines.

- ft-8f3c reproduced (orchestrator, baseline binaries, both engines; scratchpad/repro8f3c): ERR005 false positives on defer Close + os.Exit (p.go:16) and + log.Fatal (p.go:27); panic, Goexit, log.Panic and exit-on-one-path report correctly (38/49/60/71). Likely root cause: isNoReturnCall (lower.go ~2446) lumps process-exit (defers skipped) with unwind (defers run). No existing stress fixture has the shape. Brief written (briefs/8f3c.md); it starts once ft-c7dd reaches review.

## Wave 3 closed / wave 4 started (~03:15)
- ft-c7dd -> REVIEW (rev 5). Gates 02:57-03:10: go1.27.3 vet ok; cgo test ./... 17/17 ok; nocgo errcheck+gusseterr+stress ok; recorded fingerprint b182dec1ca9fc4b4 = computed; throwaway merge c7dd x 5e61 (8c5c983) ok in both engines, merge worktree removed and cargo-cleaned.
- Pre-wave: free 71 GiB; insights collisions ok (8/8 scanned, 0 overlapping); DevMap fresh gen 526; collision risk clean on lower.go, er_exitdefer and FINDINGS.md; main still 8df1d77.
- w-c7dd removed (branch kept); w-8f3c on run2/8f3c from run2/c7dd 96827ec; ft-8f3c -> in_progress (rev 2); Sonnet lane spawned.

## ft-8f3c implementation returned
- 97cfac2 (stress markers: new er_exitdefer root; ledger 640/187/56: +13 hits for unwind/mixed shapes, +10 FP markers) and 811903a (fix; ledger 640/187/46; fingerprint 33fb6ad7ba44695d). Root cause, sharper than the brief: ERR005 for a deferred Close is reported at REGISTRATION (writableAtReg), whatever the exits. The fix classifies calls (callReturns / callExitsProcess / callUnwinds) and drops the deferred-Close op in blocks with no path to a return, panic or unwinding call. Lane-verified: 10 exit shapes failed on the tip in both engines; errcheck, gusseterr and stress pass in both; self-run identical (8 ERR005); stdlib differential identical (partial load).
- Changed expectation: errdeferend createThenExit (its fixture comment already called the pin questionable); the panic twin still reports. Accepted.
- Opus concern sent to the adversary first: "no path to return, panic or unwind" is broader than "every path ends in a process exit" (infinite serve loops, select{}, unknown exit wrappers). A possible NEW MISS against the identical-to-tip invariant.

- ft-8f3c adversarial pass (2747369): BREAK, a new miss in 10 shapes (infinite loops, select-free blocking loops, goto cycles, generic, goroutine-closure loop, Goexit-defer + for{}, loops whose only exit is os.Exit but which can spin, accept loops). Cause: blocksWhereDefersRun seeds only returns and unwinds, so closed cycles are marked "defers do not run" (lower.go ~2520, drop at ~1664), which confirms the Opus pre-review concern. Defended: all process-exit shapes (if/switch/loop/goto/labelled-break exits, log.New().Fatal, init, generics, method-value exit, recover then exit); mixed paths keep findings; unwinds keep them; determinism 10x/engine and engines byte-identical; ledger 640/187/46, fingerprint matches; createThenExit is correct. Remaining FPs (sound): die() wrapper, syscall.Exit, interface Fatalf.
- Sent back: invert to exitOnly as a least fixpoint (true only if the block ends in a process exit, or has successors that are all exitOnly), with a deterministic work counter.
- Adversary slip: briefly created a symlink at /Users/bharath/Code/devtools/gusset/gusset and removed it; the orchestrator verified it is absent. The other gusset changes belong to the session active there.

- ft-8f3c round 2 (ed0cd9d): two reverse-reachability passes. Drop only if the block reaches a process exit AND reaches no return/panic/unwind; pure infinite loops keep (8 of 10 breaks fixed). Work counter 2.50 per block at 1,401 and 21,001 blocks. Ledger 640/187/46; fingerprint e3ba8798f5fb27e1 (FINDINGS.md updated). Self-run identical to 96827ec. Both engines: only LoopOnlyExit and AcceptLoop still fail. The lane showed the least fixpoint regresses 4 defended shapes with the same graph (LabelledBreakExit, GotoThenExit, DeferInLoopExit, ExitEarlier...): the adversary expectations contradict each other. Opus reading: every terminating path ends in a process exit, so silence fits the ruling; the 2 expectations should change with a written reason. Asked Fable.

- Fable (ft-8f3c): CONFIRMED. LoopOnlyExit and AcceptLoop are silenced (H4: every terminating path exits; the spin path never runs defers). The adversary split had no graph basis (all six shapes share one graph). ed0cd9d rule stands. Pure infinite loops keep ERR005 as a scope limit, not a true-positive claim. Pin all six shapes + 8 no-exit loops. Lane instructed to flip the 2 expectations with the ruling text, pin, add the scope note, re-verify.

- ft-8f3c final lane commit 1ba7ef7: LoopOnlyExit/AcceptLoop silenced with the Fable ruling comment; all 6 one-graph shapes silenced and 8 no-exit loops reporting, all pinned; scope note in FINDINGS.md and the breaks.go header; both engines pass errcheck+stress; vet ok; ledger 640/187/46; fingerprint e3ba8798f5fb27e1 unchanged. Final gates started (full suite + merge 8f3c x 5e61).

- Fable: DEFER ft-bc5b. Criteria 1-2 measure ownership code the next merge moves; the record is rewritten by live session gocap-55. Resume trigger: swarm/arch and swarm/integrate land on main, and gocap-55 goes quiet on the record. A read-only criterion-3 check on run2/5e61 is approved (time, peak RSS labelled under load, commit, go version; no timing claim; no state move, no record edit). It is queued after the ft-8f3c gates.

## Stack B complete (~04:00)
- ft-8f3c -> REVIEW (rev 3). Gates 03:41-03:51: vet ok; cgo test ./... 16/17 + tests/stress failed only TestWitnesses/lk_wit_maplookup ("runtime did not convict"); isolated 30/30 pass on base f0dea98 and 30/30 on run2/8f3c (load ~14); full tests/stress cgo rerun ok. Second occurrence of the ft-a0d4 load flake this run. nocgo errcheck+gusseterr+stress ok; merge 8f3c x 5e61 (417b04b) ok in both engines.
- w-8f3c removed (branch kept). Stale attribution archives removed. Free 88 GiB. Only the base worktree remains.
- Read-only criterion-3 check for ft-bc5b started on a c2081c6 archive (go1.27.3, cgo): gosree-corpus -git scholarlm bb60b53 -tests, under /usr/bin/time -l.

## ft-bc5b criterion-3 read-only check (03:54-03:55)
- Binary built from a c2081c6 archive (run2/5e61 tip) with go1.27.3 (cgo, native solver). gosree-corpus -git ~/Code/scholarlm -rev bb60b53 -subdir backend/go_orchestrator -tests: rc 0, 39.74 s real (under load 13.7-13.9; not a timing claim), max RSS 8.41 GB, peak footprint 14.41 GB (under load).
- internal/wisdev [wisdev.test] analysed: 1,529 transfers, 947 flagged, findings present, no error. Totals 4,805 transfers / 3,106 flagged. Criterion 3 holds on run2/5e61. The card stays deferred (backlog; the tool cannot log to backlog). Extract and binary deleted; module-report.json kept (68K).
- Found: module-report gosree_commit read 8df1d77-dirty because gosreeCommit runs git describe in the CWD (module.go:266-273). New card ft-c37ee4e9f079ad107d749cb55d17b0ab (p3).

## Wave 5 started (~04:10)
- Pre-wave: free 86 GiB; collisions ok (7/7 scanned, 0 overlapping); DevMap fresh; no holders on lower.go or module.go; main still 8df1d77. gosreeCommit impact complete (module.go, main.go).
- ft-fe6e -> in_progress (rev 2): ONE bounded round (Fable) of gated closure replay on run2/fe6e (stacked on run2/8f3c 1ba7ef7). Spec = the wip adversary fixtures; hard stops: new FP, new miss, duplicates, non-linear nesting, engine mismatch. Path keying excluded.
- ft-c37e -> in_progress (rev 2): gosree_commit from runtime/debug build info, on run2/c37e (main 8df1d77). Independent of safeerr, so it runs in parallel. ft-6d5a (safeerr messages) waits for ft-fe6e.

- ft-c37e implementation: a19b20b (gosreeCommit reads vcs.revision/vcs.modified via an injectable readBuildInfo; 7-char + -dirty; no stamp gives "unknown (no vcs stamp)"; never the CWD). Failing-before verified (old code returned the CWD describe). Both engines pass cmd/gosree-corpus; vet and gofmt clean. Real binary from another dir reports its own commit. JSON contract unchanged. Refservice header not edited (gocap-55 wrote it within 24 h; the sentence is in the lane report). merge-tree with run2/f2e5 clean. Opus doc nit: "same shape as git describe" overclaims (tags; untracked files count toward -dirty). Short adversarial pass started.

- ft-c37e adversarial (ad088cc, tests only): core defended (no build reads the CWD; go run, -buildvcs=false and an archive build give no-stamp; a linked worktree stamps correctly). BREAKS, all failing open: non-git VCS shown as a bare hash; non-hex/control/quote/non-ASCII revisions pass through (mid-rune slice); a vcs.modified value other than "true" reads clean; duplicate revisions are last-wins; vcs absent is still shown as git. DOC overclaim confirmed (tags, untracked files, abbreviation length). No other callers or docs mention gosree_commit. Sent back: VCS prefix, hex validation, fail closed on modified, duplicate keys give no-stamp, exact doc.

- ft-c37e fix rounds: 7734ea6 (VCS prefix for non-git; vcs absent gives no-stamp; lowercase hex >=7 validated before slicing; duplicates that conflict give no-stamp; exact doc) and 1e216e9 (absent vcs.modified gives -dirty, fail closed; the test failed before). All adversary tests pass. One adversary fixture edit: TestAdvGitVCSStaysBare gained vcs.modified=false (Go always emits it; intent unchanged). Opus approved. Full gate started.
- ft-c37e -> REVIEW. Gates 04:06-04:13: vet ok, cgo 17/17, nocgo corpus ok, merge-tree with f2e5 clean. w-c37e removed (branch kept).

## ft-fe6e round result (~04:50)
- Commits 2efea90 (wip spec fixtures ported; the base result pinned), 6186736 (replay of writes and discarded Closes at the single dominating call, gated by closureCallFor shared with the 420d plan), d4880a9 (4 of 14 er_closureclose markers promoted; ledger 644/183/46). The lane auto-switched to Opus 5.5.
- Hard stops: new FP pass (22 added lines, all tracked shapes; self-run identical); new miss pass (0 removed); duplicates pass; engine parity pass (byte-identical). Nesting: spec nestedSrc linear (17/level, = base); chain linear; STRICTER nestedClosedSrc quadratic (1,617 -> 65,492 at depth 25 -> 200; base 494 -> 3,994).
- Lane notes: replaced 4 wip tests that assume path keying with a snapshot + goldens (fixtures unchanged); 4 documented misses (P3a/b/c, P5c) now want ERR005; used python3 once for a mechanical lower.go edit (does not ship, but breaks the analysis-only rule; recorded).
- Lane flagged a possible parentIndexFor (ft-420d) bug. VERIFIED by the orchestrator with ssadump on real code: the Recover block is block 1 (no idom, ends in return) BEFORE the other returns; retDom climbs to nil, then "if retDom == nil { retDom = blk }" resets it, giving a block that does not dominate every return. The gating invariant is false (fail open); no concrete FN shown. Asked Fable: (1) is fe6e nesting a hard-stop fail; (2) a sticky-nil fix on run2/420d, merged forward.

- Fable rulings: (1) fe6e is not a pass as it stands and not parked: ONE commit adding a work/depth cap (no plan past it = tip behaviour; tests at the cap and one over), then review with the quadratic recorded as a known cost and the Euler-interval redesign filed; park if more is needed or another stop moves. (2) 420d sticky-nil fix APPROVED: failing-first test, merge forward 420d -> c7dd -> 8f3c -> fe6e, both engines on each tip, sweep for similar nil resets, note on the 420d card (no concrete FN shown). Delegated to the fe6e lane in order A (fix on 420d), B (merges), C (cap).

- fe6e lane A/B/C done: 420d 1409a1b (seenRet sticky nil; TestParentIndexRetDomIsStickyNil failed before; sweep found only lower.go:751), merges c7dd 67f9280, 8f3c 69caf43, fe6e e106a05 (conflict in parentIndexFor resolved), cap 82f4848 (maxReplayDepth=16; max function-literal nesting in repo+stdlib is 5; nestedClosedSrc now linear 1,286->10,386); all five hard stops pass; per-tip errcheck+stress ok in both engines.
- SIDE EFFECT (Opus): sticky nil makes every function WITH A DEFER get no plan (a defer creates a Recover block), silently disabling 420d coverage for defer-using code. Refinement sent: exclude parent.Recover from the return-dominance walk (its Return returns named results, which the plan already excludes), keep sticky nil otherwise; test F rejected + G (defer + dominating call) accepted, failing before; re-merge forward; report per-tip fingerprints; update the FINDINGS fingerprint only on the fe6e stack tip.

- Recover-block refinement done: 420d a4fb41e (parent.Recover excluded from the return dominance; go/ssa emit.go:611 createRecoverBlock loads only the result allocs, which the plan excludes; TestRecoverBlockIsLeftOutOfReturnDominance: F rejected, G accepted; G failed on 1409a1b with ERR002 at line 38). Merges: c7dd 5b96482, 8f3c 598cea3, fe6e 4120e06 (resolution DELETED mainRetDom/mainNone/domLowered); e336876 FINDINGS fingerprint ab6ba6528fdbde2b on the fe6e tip. Per-tip fingerprints: 420d ff0d5783ee6a0d73, c7dd fcc0f30174daf423, 8f3c 11c53cff8f1454f0, fe6e ab6ba6528fdbde2b. Both engines green on all tips. fe6e diff vs 1ba7ef7: 0 removed, 23 added (22 tracked + errretdom F ERR002, intended).
- Loose end: the self-run flags retdom_internal_test.go:70 ERR001 (a discarded Sscanf error in the lane's own new test). Sent: a test-only fix on 420d, merge forward, errcheck-only per tip (test files are excluded from the fingerprint), self-run on fe6e.

- Sscanf fix: 420d db244aa; merges c7dd e049a2c, 8f3c 290f3fc, fe6e 24d21ac; errcheck ok on all tips in both engines; fe6e self-run zero delta; fingerprint ab6ba6528fdbde2b. Final gates started on 24d21ac.
- Filed ft-79c70141 (p3): the replay is quadratic in nesting depth below the 16-level cap; Euler-interval redesign (Fable-required follow-up of ft-fe6e).

## Stack B tip to review; wave 6 (~05:15)
- Final gates on run2/fe6e 24d21ac (04:59-05:08): fingerprint ab6ba6528fdbde2b = FINDINGS; vet ok; cgo test ./... 17/17 ok; nocgo errcheck+gusseterr+stress ok; stress on the run2/420d tip ok; merge fe6e x 5e61 (45fbc54) ok in both engines. ft-fe6e -> REVIEW (rev 3). Update notes on 420d (rev 6), c7dd (rev 6), 8f3c (rev 4): new commits, per-tip fingerprints, FINDINGS current only on fe6e. The fe6e lane was stopped (leftover watchers).
- w-fe6e removed (branch kept). Free 191 GiB (other sessions freed space).
- Wave 6: ft-6d5a -> in_progress (rev 2) on run2/6d5a stacked on run2/fe6e; message-text-only fix; Sonnet lane spawned.
- ft-6d5a implementation 7a352ac: one quoting owner (ssaValName/cellSiteName bare, formatters quote once; describeSSAVal keeps quotes for an ERR002 form); the closure-pair bind op gets an error-cell site so the message names err like its twin. Exact-message tests failed before (captured cell, parameter, closure pair). Finding set identical (TestDump 62 lines, self-run 186 lines, TestStress passes). The code now matches RULES.md:694. Opus approved; short adversarial pass started (engine parity of messages, every format, odd names, finding-set identity).
- ft-6d5a adversarial (21750fb): defended engine parity (byte-identical over 55 roots, before and after), every message format, odd names, finding-set identity (only 3 messages changed, all toward err). BREAK vs criterion 2: the plain twin overwriting itself in a loop (OpBindTuple at lower.go ~1666) still names the call. Pre-existing, but it is what criterion 2 is about. Sent back: give OpBindTuple an error-variable site when the error is stored to a named variable.
- ft-6d5a round 2: the twin OpBindTuple now names the error variable (errVarNameAt, a lazy per-function AST walk; callErrVar untouched); the adversary test passes unedited; blank/field errors keep the call name (no reachable ERR003; reasoned, untested). Engines byte-identical over 136 stress roots (699 lines); TestDump and self-run identical. Opus approved. Final gates started (full suite + merge 6d5a x 5e61).

## Close (~05:45)
- ft-6d5a -> REVIEW (rev 3) after gates 05:28-05:37 and the FINDINGS fingerprint commit 55b1e86 (1694ac132e12f19b). Closing insights: collisions ok, holdings unchanged (antigravity branches unmerged 5/9/2; 45c62196 docs uncommitted); load 31 (not idle).
- Cleanup: w-6d5a removed; 54 scratch binaries + archive copies deleted (827 MB -> 69 MB). Base worktree kept as the benchmark baseline. Free 187-191 GiB.
- Final report committed to run2/report 1d7fd66. Memory updated.

## Advisor close-out fixes (~06:00)
- Merged-tree fingerprint (6d5a x 5e61): c4888388dc458b33 (verified via merge-tree, archive, fingerprint.sh).
- Cross-OS: CGO_ENABLED=0 go vet ./... with GOOS=linux/windows clean on the f2e5, c37e, 5e61 and 6d5a tips; no Linux/Windows test run.
- go run vs gosree_commit: default go run gives no-stamp; go run -buildvcs=true and go build give 1e216e9 (temporary worktree, removed). Owner decision recorded on ft-c37e (rev 4) and in report section 8.
- ft-420d acceptance analysis recorded on the card (rev 7): narrows with 2+ normal returns, widens for single-return defer functions (justified by createRecoverBlock).
- New card ft-e92e722c (p2): path keying + remaining ERR005 shapes; spec run2/c7dd-err005-wip.
- Report corrected: 23 breaks (not 25), the 8f3c 16/17 + flake caveat, exact merge-test coverage, lowering cost unmeasured, platforms. The run log and exit-code table are committed with the report.

## Wave 7 (~10:05, 2026-11-13; owner asked "Next wave")
- Pre-wave: nothing moved (main 8df1d77; swarm branches unmerged 5/9/2; 45c62196 docs uncommitted); load 77; free 186 GiB; insights collisions ok (7/7, 0 overlap); dev verify still has no store.
- Fable: (1) run the ft-8462 preflight half, but if GOSREE_JSON_CHILD is documented or read outside cmd/gosree, changing it goes to the owner; (2) ft-e92e design-first (design + fixtures pinned to the tip, HARD STOP before code); not ft-79c7. Vetoes: e92e code, timing claims, dropping the 1.27.3 label, imm.go.
- Precondition check: GOSREE_JSON_CHILD is documented at README.md:180 and docs/TOOLING_AND_ERASURE.md:79 (the -json child marker); no sibling repo reads it. So item 1 (the env bypass) is an OWNER DECISION, recorded on the card with a contract-preserving proposal (the child re-applies the preflight refusals). Items 2-3 proceed.
- ft-8462 -> in_progress (rev 3), w-8462 on run2/8462 off 8df1d77. ft-e92e -> in_progress (rev 2), w-e92e on run2/e92e off run2/6d5a. Two Sonnet lanes spawned.
- ft-8462 implementation c95d79e: isMetaCommand: the last -flags decides (TestIsMetaCommandLastFlagsDecides failed first); repeated -test/-NAME with different values warns, and -test=false -test drops the false "not analysed" warning (TestRepeatedFlagWarnings failed first). Both engines pass cmd/gosree; exit rows unchanged except -flags -flags=false with no packages: 1 -> 2. Opus: that is a CORRECTION, not a contract break (TOOLING:81 documents that the last -flags decides and the rest of the line is checked; 2 = documented usage). Python used for two edits (does not ship; recorded). Adversarial pass started.
- ft-8462 adversarial (b479d9a): BREAK: `gosree -flags -tags -flags=false` went 0 -> 2 (isMetaCommand walked the value consumed by -tags). Defended: driver agreement (junk/2/TRUE/yes/empty/--/two-arg/after-package/help/prefix names), warning counts, the full exit table in both engines, the -json child warning once. Sent back: skip consumed values; ideally one shared flag walk with parseCommandLine.
- ft-8462 round 2 (0aafc95, net -13 lines): one scanFlags walk now serves flagEnd, parseCommandLine, isMetaCommand, hasUnknownFlag, unmatchedWildcards and cmdFlag.truthy (the take closure removed). The same blind spot was fixed in hasUnknownFlag (`-tags -foo` was refused as an unknown -foo; it now follows the driver, which takes -foo as the tags value: a second CORRECTION exit change, 2 -> driver result) and in unmatchedWildcards. The adversary tests pass unedited; both engines ok. Full gate started.
- ft-e92e design delivered (91fad0b): verified only tests, testdata and DESIGN_ERR005_PATHS.md (372 lines) changed, so the hard stop held. Claims lowering-only (versions = slots, stores = OpAssignSlot, joins = PhiIR; the hand-built IR test is identical in both engines). 77 annotated fixtures pinned to the tip (27 report-wins silent today); snapshot byte-identical across engines. Opus: sound; tightened the Write/Close exemption to receiver == the tracked resource; agree with 3 milestones. Sent to Fable for design review (6 lane questions).
- Fable APPROVED the e92e design; answers recorded on the card (rev 3, stays in_progress: design done, code paused until Stack B leaves review or the owner says proceed). w-e92e removed, branch run2/e92e kept.
