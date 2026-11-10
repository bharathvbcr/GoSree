# ScholarLM `go_orchestrator` at `bb60b53`: hatch-ratio triage log

The annotation table is [`scholarlm-bb60b53.tsv`](scholarlm-bb60b53.tsv): one row per directive, with
the finding it answers and its verdict. This file records each batch's prediction, written before its
run, and the result. Runs:

    go run ./cmd/gocap-corpus -git ~/Code/scholarlm -rev bb60b53 -subdir backend/go_orchestrator \
        -annotate cmd/gocap-corpus/refservice/scholarlm-bb60b53.tsv -list -out build/ref/<batch> [-tests]

The B4 rows, the test-stratum sample and the SAFE006 sort came from a throwaway tool kept, uncommitted,
in `build/ref/_tools/triage` (gitignored), next to the final reports in `build/ref/final`.

The plan is the 2026-11-09 design ruling for README item 3: two strata (production, the headline,
and `-tests`), hatches only on transfers or on non-transfer sites (a SAFE001 node, a SAFE006
declaration), never on a use or write line, and every `//safe:unsafe` must mute something.

Verdicts: `RACE`, `GUARDED`, `IMM`, `ISO`, `OVERRIDE`, `FP:<class>`, `POLICY:<why>`,
`IMM_BLOCKED:<type>.<field>:<kind>`.

## Baseline (no annotations)

Prediction (design ruling): `-tests` on the archive reproduces the README row exactly, 1,642
transfers and 2,598 findings. Production: transfers 480-560, SAFE008 95-100.

Result (gocap 47fd801 plus the item-3 counters):

| Stratum | Transfers | Flagged | Clean | SAFE001 | SAFE002 | SAFE003 | SAFE003-fj | SAFE004 | SAFE006 | SAFE008 | Findings |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `-tests` | 1,642 | 906 | 736 | 104 | 644 | 811 | 286 | 414 | 238 | 101 | 2,598 |
| production | 339 | 230 | 109 | 19 | 221 | 220 | 1 | 111 | 73 | 98 | 743 |

- `-tests` reproduced the README row exactly, so `bb60b53` is the commit the README measured (the
  checkout, at `6241734`, differs in four test files).
- Production transfers **missed** (339, predicted 480-560): the send estimate was grep-derived and
  over-counted. SAFE008 hit (98, predicted 95-100).

## B0: spawn wrappers own their functions

`//safe:iso` on the function parameters of `safego.SafeGo`, `SafeGoWG`, `SafeGoOnPanic`,
`SafeGoWGOnPanic` (`onPanic` and `fn`) and `detachedTasks.spawn`, `spawnAfterPersist` (`fn`). Their
calls were escapes (the callee's summary leaks `fn` into a goroutine) and become transfers.

Prediction, written before the run. Counted call arguments (production): SafeGo 27, SafeGoWG 68,
SafeGoOnPanic 10 x2, SafeGoWGOnPanic 8 x2, spawn 6, spawnAfterPersist 1 = 138, so production transfers
339 -> 470-480 if every call is static. Tests add 49 more arguments: `-tests` 1,642 -> 1,820-1,835.
The design ruling counted calls, not arguments (+118 production, +137 `-tests`). New SAFE002 at callers
70-110 (closures capturing handlers and stores), SAFE003 +30-80, SAFE003-forkjoin +5-20 after
`wg.Wait()` following `SafeGoWG` (`FP:ISO_WG_JOIN`, counted, not fixed here). SAFE004 falls where a
closure's only escape was the wrapper call.

Result: 8 iso parameters parsed (6 rows).

| Stratum | Transfers | Flagged | Clean | SAFE002 | SAFE003 | SAFE003-fj | SAFE004 | SAFE008 |
|---|---|---|---|---|---|---|---|---|
| production | 339 -> 476 | 230 -> 350 | 109 -> 126 | 221 -> 322 | 220 -> 379 | 1 -> 53 | 111 -> 160 | 98 |
| `-tests` | 1,642 -> 1,798 | 906 -> 1,036 | 736 -> 762 | 644 -> 747 | 811 -> 973 | 286 -> 343 | 414 -> 473 | 101 |

- Production transfers **hit** (+137, predicted 470-480); `-tests` **missed** slightly low (+156,
  predicted +178-193).
- SAFE002 **hit** (+101). SAFE003 **missed** high (+159, predicted 30-80), fork-join **missed** high
  (+52, predicted 5-20) and SAFE004 **missed** in sign (+49, predicted a fall): the wrapper's closures
  capture more than estimated, and an escape before the call that the leak used to hide is now
  reported on the transfer.
- Fork-join by wrapper: SafeGoWG 41, SafeGoWGOnPanic 2 (`FP:ISO_WG_JOIN`, 43), SafeGo 7 and
  SafeGoOnPanic 2 (`FP:ISO_CHAN_JOIN`: the join rule follows a `go` statement's body, not an iso
  callee's argument), 1 other. `ISO_WG_JOIN` meets the cluster rule (>= 10 and >= 2% of transfers);
  whether its fix is sound is decided in B7.
- Production SAFE002 causes: 184 handler captures that alias a parameter of the registering
  function (`cfg` 79 in `NewRouterWithShutdown`, `s` 63, `agentGateway` 22, `store` 9), 78 wrapper
  closures aliasing a parameter, 11 a package variable, 10 a captured variable.

## B1: guarded_by on the mutex-protected singletons

15 `guarded_by` on the fields the SAFE008 writes reach: `IdempotencyStore.records`,
`RuntimeStateStore.initDone/initErr` (initMu), `RuntimeJournal.index/dirtyIndexWrites`,
`llm.Client.conn/client/injected`, `PaperHandler.paperCountCache/paperCountBackoffUntil/
paperCountBackoffStatus` (paperCountMu), `clientTelemetryRateLimiter.windows`, `flow.delivered`,
`mcpDependencyProbeCache.at/status/checked`.

Prediction, written before the run: the 12 production direct writes and the 8 `IdempotencyStore`
Get/Put calls clear. Callees that write through an interface call one level down (`s.db.QueryRow`,
counted as a write by the writes bit) stay, so production SAFE008 98 -> 35-60 (the design ruling said
3-8 plus whatever the interface class holds). SAFE009 +3-10: `paperCountCacheStore` returns the
guarded cache, the stress test reads `len(windows)` unlocked, and `*Locked` helpers need `holds`.
SAFE006 unchanged (fields, not package variables). Transfers unchanged.

Result, after two corrections made on reading the first run (SAFE008 75, SAFE009 31):
- The three `llm.Client` rows were dropped. `ensureClient` dials once under `mu` and every caller
  reads `c.client` after its own `ensureClient` took the lock: lock-protected publication, not lock
  discipline, so `guarded_by` turned 9 race-free reads into SAFE009 and cleared no SAFE008.
- 8 `//safe:holds` were added on the `*Locked` helpers (`RuntimeJournal` x6,
  `RuntimeStateStore.initStorageLocked`, `clientTelemetryRateLimiter.evictExpiredLocked`).

| Stratum | SAFE008 | SAFE009 | guarded_by | holds |
|---|---|---|---|---|
| production | 98 -> 75 | 0 -> 8 | 13 | 8 |
| `-tests` | 101 -> 78 | 0 -> 9 | 13 | 8 |

- SAFE008 **missed** (75, predicted 35-60). 23 cleared: the 12 direct writes, the 8
  `IdempotencyStore` calls, `RuntimeJournal.EnforceRetention`, and 2 wrapper calls.
- What the other 75 are, measured with two diagnostic builds (not committed): with interface and
  dynamic calls not counted as writes, 56 remain, so 19 are `FP:IFACE_ONE_DOWN`. Explaining the
  writes bit of the remaining callees, every one sampled (`RuntimeStateStore.EnforceRetention`/
  `LoadQuestState`, `AgentGateway.RuntimeMetadata`, `RuntimeJournal.Append`, `llm.Client.Health`,
  `semantic.Layer.Remember`, `usage.BudgetEvaluator.Evaluate`) writes only memory it allocated or a
  local (`jobRemoved := 0` captured by a closure, the `[]any{cutoff}` argument slices, a fresh result
  map) that Steensgaard unification merged with the receiver through a closure capture, a link or an
  interface call's arguments. Class `FP:UNIFY`. No sampled write is to shared memory outside a
  mutex, so none of the sampled callees makes a SAFE008 a race (7 sampled, not all 75 read). Both classes are the writes bit's coarseness (README Known
  limits); neither has a sound fix inside this item.
- SAFE009 **missed** (8, predicted 3-10 at first, but only after the holds): `paper.go:759` returns
  the guarded `*TTLCache` (`FP:GUARDED_RETURN`: the pointer is read under the lock and the cache locks
  itself), `loadIndex` initialises the index before the journal is shared (`FP:CONSTRUCTOR_INIT`, 2),
  `persistIndexLocked` hands the index to `json.Marshal` (`FP:MARSHAL_RETAIN`: the override keeps
  Marshal's argument leaked), `compactTailBytes` keeps a slice of the index in a local while still
  holding the lock (`FP:GUARDED_ALIAS_LOCAL`), `Diagnostics` and `readIndexed` call the read-only
  `indexCoversFileLocked` under `RLock` (`FP:HOLDS_RLOCK`, 2: `holds` means the write lock), and
  `rewriteFileEntriesLocked` calls `flushIndexLocked` (a missing `holds`, added in B2). Each was read;
  none is a race.

## B2: imm census

Census of what the flagged production transfers share (from the SAFE002 causes and the types' fields):

| Shared value | Type | Production SAFE002 | First field blocking imm |
|---|---|---|---|
| `cfg` | `api.ServerConfig` | 79 | exported fields (`SearchRegistry`, `DB` ...) |
| `s` | `*api.wisdevServer` | 63 | `docService docgen.ScholarDocService`: interface |
| `agentGateway`, `gw` | `*wisdev.AgentGateway` | 24 | exported fields (`Store`, `Executor` ...) |
| `h` | `*api.PaperHandler` | 5 | `profiler PaperProfiler`: interface |
| (search handlers) | `*api.SearchHandler` | - | `openSearch OpenSearchExecutor`: interface |
| (state) | `*wisdev.RuntimeStateStore` | - | `db DBProvider`: interface |
| (journal) | `*wisdev.RuntimeJournal` | - | `db DBProvider`: interface |
| (llm) | `*llm.Client` | - | exported fields (`VertexDirect` ...) |
| (registry) | `*search.ProviderRegistry` | - | unguarded maps (`providers`) |
| (cache) | `*search.TTLCache[V]` | - | `now func() time.Time`: func |
| (limiter) | `*api.clientTelemetryRateLimiter` | - | `now func() time.Time`: func |
| (login) | `*login.flow` | - | `now func() time.Time`: func |
| (dedup) | `*wisdev.IdempotencyStore` | - | none: imm once `records` is guarded |
| (mcp) | `*wisdev.mcpDependencyProbeCache` | - | none: imm once its fields are guarded |

`//safe:imm` on the two that qualify, and the missing `holds` on `rewriteFileEntriesLocked`.

Prediction, written before the run: 0 handler types are eligible (the design ruling said 0-2), so no
transfer is cleared by imm: flagged transfers unchanged in both strata, SAFE005 0 (both types
qualify), SAFE009 production 8 -> 7.

Result: flagged transfers unchanged (350 production, 1,036 `-tests`), as predicted. Two misses:
- SAFE009 stayed at 8: `rewriteFileEntriesLocked` now holds `mu`, but a call before
  `flushIndexLocked` that receives an object containing the mutex counts as a possible unlock
  (`FP:LOCK_KILLED_BY_CALL`).
- `-tests` SAFE005 0 -> 2: two tests take the address of a local `mcpDependencyProbeCache`, which the
  imm rules refuse. The row was dropped: it cleared nothing and the tests' pattern is legitimate. One
  imm remains (`IdempotencyStore`).

## B3: overrides

Third-party causes among the production SAFE002/SAFE004 are few (`json.Decoder.Decode` 3,
`json.NewDecoder` 2, one each of `exec.CommandContext`, `sort.SliceStable`, `Timer.Stop`), and each is
a real retention or a use, so none earns an entry. Reading the transfers the triage rules could not
classify (B4) found one documented guarantee that does: a `context.CancelFunc` captured by a timer
or `context.AfterFunc` watchdog and also deferred (`vertex_stall_guard.go:258`,
`fulltext_version_links.go:296`, `research_runtime.go:4460`). The `context` documentation says a
CancelFunc may be called by multiple goroutines simultaneously. One type override,
`context.CancelFunc`; budget 41 -> 42 of 100. Test: `cancelWatchdog` in the callbacks testdata
(fails without the entry).

Prediction, written before the run: production flagged 350 -> 347 (the three), with SAFE003 falling
by 3-10 where a cancel was one of several captures; `-tests` flagged falls by 5-20. Race corpus
unchanged at 229/36 and 229/42 (its tests do not share cancel functions).

Result: race corpus unchanged, as predicted. Production flagged 350 -> 346 and SAFE003 379 -> 365:
both slightly **missed** high (-4 and -14). `-tests` flagged 1,036 -> 1,014 (-22, predicted 5-20,
slightly over; the `-tests` run also carried B4's one `guarded_by`, which clears one transfer).

## B4: hatches at the transfers

Every flagged production transfer gets one `//safe:unsafe` on its own line above the transfer, its
class first in the reason. The rows were generated from the B3 findings by the triage rules below,
with the 12 transfers the rules could not place read by hand. One local, `worst` in
`testsupport.WatchTimerLateness`, is written and read only under `mu`; it got a `guarded_by` instead.

Rules, in order, over the findings each transfer raised:
1. A verdict read by hand (12 transfers: below).
2. Only SAFE003-forkjoin: `FP:ISO_WG_JOIN` after a `SafeGoWG*` call, `FP:ISO_JOIN` after another iso
   call, `FP:JOIN_SHAPE` otherwise.
3. Only SAFE008: `FP:WRITES_COARSE` (B1).
4. A SAFE002 naming what the value aliases: `IMM_BLOCKED:<those names>`. The transfer shares a
   long-lived dependency whose type cannot be imm (B2's census).
5. A SAFE004 with no SAFE003: `IMM_BLOCKED:escape`.

Read by hand (production):
- `FP:CANCELFUNC` x3, which became B3's override.
- `IMM_BLOCKED:client`: every agent-control-plane handler shares one client.
- `FP:JOIN_IN_CLOSURE`: `startRetractionLookup` returns a closure that waits.
- `POLICY:MUX_SELF`: the catch-all handler holds its own mux.
- `FP:CONCURRENT_READ`: `DeepDiveRepos`.
- `POLICY:SELF_OWNED_MONITOR`: the governor's monitor goroutine.
- `FP:INDEX_DISJOINT`: `CheckRetractionsBatch` writes `out[i]` per goroutine.
- `FP:VALUE_COPY`: `DownstreamEventSink.Dispatch` reads its own copy after a send.
- `GUARDED`: `WatchTimerLateness`.

Fixing a placement: one transfer is a field of a composite literal (`stop: context.AfterFunc(...)` in
`hostConcurrencyTransport.RoundTrip`). Its hatch moved to the enclosing assignment, after the run
refused the table (SAFE007).

Prediction (design ruling): production ratio 50-70%, `-tests` 30-50%, at least 75% of hatches
`IMM_BLOCKED`, 0-3 real races.

Result, production:

| Transfers | Flagged | Suppressed | Clean | Hatch ratio | Hatches | Stale | Remaining findings |
|---|---|---|---|---|---|---|---|
| 476 | 345 | 345 | 131 | **72.5%** | 333 | 0 | SAFE001 19, SAFE006 73, SAFE009 8 |

- The ratio **missed** high (72.5%, predicted 50-70%).
- `IMM_BLOCKED` **hit**: 324 of 333 hatches (97%). The rest are FP x7 (`CONCURRENT_READ` 2,
  `ISO_WG_JOIN` 2, `INDEX_DISJOINT`, `JOIN_IN_CLOSURE`, `VALUE_COPY`) and POLICY x2.
- Races found **hit**: 0 (predicted 0-3). The 12 transfers above were read; the other 321 hatches
  were placed by rules 2-5, which read findings, not code. So this is "none found", not "none
  exist". Every flagged transfer is suppressed, so none is left as a `RACE`.
- The ratio without the 7 `FP:*` hatches is about 71% (338 of 476, counting one transfer per FP
  hatch). The hatches also mute all 75 remaining production SAFE008.
- 333 directives suppress 345 transfers: a goroutine or handler that captures several values is one
  transfer per value at one position. The design ruling's invariant, `UnsafeScope ==
  SuppressedTransfers + non-transfer hatches + stale`, assumed one transfer per position; the checked
  invariant is stale = 0.
- What the hatches share, by the alias the SAFE002 names: `s` (`*wisdevServer`) 80, `cfg`
  (`ServerConfig`) 80, `agentGateway`/`gw` 28, `h` (`*PaperHandler`) 16, `p` 11, a caller's parameter
  handed over 10, `store` 9.

Result, `-tests`: 1,798 transfers, 1,014 flagged, 345 suppressed (the production hatches), 784
clean, so 669 flagged test-only transfers at 575 positions. A seeded sample of 100 of the 575 (seed
20261109) was classified:
- `IMM_BLOCKED` 75 (by the rules);
- the object under test shared by its concurrency test (a limiter, memo, pool, keyed lock, store) 13,
  read from the findings and the test names, not line by line;
- `FP:JOIN_SHAPE` 8;
- `FP:CONCURRENT_READ` 4 (a `done` channel variable, a read-only result).

No sampled transfer is a race as far as the findings and test names show. The 100 sampled positions
are in the ledger (one hatch each, 105 transfers), so the `-tests` run measures them: 450 / 1,798 =
**25.0%** with 0 stale. That covers 100 of the 575 positions, not the stratum. Every sampled transfer
needed a hatch; with 0 exceptions in 100, at most about 3% of the 575 would not (95% upper bound). So
the stratum.s ratio, if every flagged test transfer takes one, is 1,014 / 1,798 = **56.4%**
(**missed**, predicted 30-50%; an extrapolation, not a measurement).

## B5: SAFE006 (package variables), production

73 findings, sorted by how each variable's writes are protected (`triage -safe006`, which reads the
code around each write position gocap reports):

| Class | Count | Rule |
|---|---|---|
| mutex | 51 | a `Lock()` earlier in the writing function (`fooMu.Lock()`): a `guarded_by` candidate |
| test seam | 13 | written only by a setter that swaps a dependency for tests (`SetX`, `MockX`, `ObserveX` returning a restore func) |
| once | 7 | written inside a `sync.Once.Do` closure |
| generated | 2 | `*.pb.go` raw descriptor caches |

These were not annotated. The mutex class is a reading of the source, not a checked claim:
`guarded_by` on each would make SAFE009 check it, which is left as a follow-up. The design ruling
predicted 50-70% seams and 15-30% guarded: **missed** (18% seams, 70% mutex). `sync.Once`-published
variables are a SAFE006 false-positive class (initialisation is only `init` and helpers called from
it).

## B6: SAFE001

Production 19: 8 in generated protobuf code, 10 `reflect.Value.Set*` in `session_deepcopy.go` (a
reflective deep copy) and 1 in `mcp_jsondecode.go`. `-tests` 104: those plus 85 in test files. SAFE001
is not a transfer and stays outside the ratio; nothing was annotated.

## B7: false-positive classes and the cluster rule

The cluster rule makes a false-positive class a checker fix inside this item when it has at least
10 instances or 2% of transfers, the fix is sound, it ships with a failing test, and the race corpus
is re-run. Counted as primary verdicts on production transfers, no FP class reaches 10: the largest
are 2. As findings on transfers already hatched for `IMM_BLOCKED`, three classes are large, but
fixing them would remove no hatch:
- `FP:ISO_WG_JOIN`: 43 SAFE003-forkjoin findings (`SafeGoWG*`). Sound only with the argument that a
  caller's `wg.Wait()` on a nil WaitGroup panics, which rules out `SafeGoWG`'s nil branch.
- `FP:WRITES_COARSE`: 75 SAFE008 findings (19 interface or func-field calls, 56 unification).
- SAFE009: 8 findings in 7 small classes.

All three go to item 4. The one class that did change the ratio, `FP:CANCELFUNC`, was a documented
guarantee and became an override (B3).

## Summary

| Stratum | Transfers | Flagged | Hatched | Hatch ratio | Races found |
|---|---|---|---|---|---|
| production | 476 | 345 | 345 (all) | 72.5% | 0 (12 read, the rest by rule) |
| `-tests` | 1,798 | 1,014 | 450 (production 345 + 105 at 100 sampled positions) | 25.0% measured on the sample; 56.4% extrapolated | 0 in the sample |

Checked claims beside the ratio: 14 `guarded_by` (13 fields, 1 local), 9 `holds`, 1 imm, 8 iso. Type
and function overrides: 42 of 100 (29 types, 4 functions, 9 spawners); this item added one.

Diagnosis: 97% of the hatches are transfers that share a long-lived dependency (a router config
passed by value, the server, the agent gateway, a handler struct). Those types have interface,
func-typed or exported fields, and are written only while they are built. gocap has no claim for
"mutable type, frozen after construction", so each sharing transfer needs a hatch. The ratio is
far above the 20% abandon line while the tables hold 42 entries, and more entries would not move it:
no flagged transfer is blocked by a third-party type. The next checked claim has to be about the
dependency containers themselves, an imm rule that accepts interface and func-typed fields whose
values are set only during construction.
