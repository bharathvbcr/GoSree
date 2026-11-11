# ScholarLM `go_orchestrator` at `bb60b53`: hatch-ratio triage log

The annotation table is [`scholarlm-bb60b53.tsv`](scholarlm-bb60b53.tsv): one row per directive, with
the finding it answers and its verdict. This file records each batch's prediction, written before its
run, and the result. Runs:

    go run ./cmd/gosree-corpus -git ~/Code/scholarlm -rev bb60b53 -subdir backend/go_orchestrator \
        -annotate cmd/gosree-corpus/refservice/scholarlm-bb60b53.tsv -list -out build/ref/<batch> [-tests]

The B4 rows, the test-stratum sample and the SAFE006 sort came from a throwaway tool kept, uncommitted,
in `build/ref/_tools/triage` (gitignored), next to the final reports in `build/ref/final`.

The plan is the 2026-11-09 design ruling for README item 3: two strata (production, the headline,
and `-tests`), hatches only on transfers or on non-transfer sites (a SAFE001 node, a SAFE006
declaration), never on a use or write line, and every `//gosree:unsafe` must mute something.

Verdicts: `RACE`, `GUARDED`, `IMM`, `ISO`, `OVERRIDE`, `FP:<class>`, `POLICY:<why>`,
`IMM_BLOCKED:<type>.<field>:<kind>`.

## Baseline (no annotations)

Prediction (design ruling): `-tests` on the archive reproduces the README row exactly, 1,642
transfers and 2,598 findings. Production: transfers 480-560, SAFE008 95-100.

Result (GoSree 47fd801 plus the item-3 counters):

| Stratum | Transfers | Flagged | Clean | SAFE001 | SAFE002 | SAFE003 | SAFE003-fj | SAFE004 | SAFE006 | SAFE008 | Findings |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `-tests` | 1,642 | 906 | 736 | 104 | 644 | 811 | 286 | 414 | 238 | 101 | 2,598 |
| production | 339 | 230 | 109 | 19 | 221 | 220 | 1 | 111 | 73 | 98 | 743 |

- `-tests` reproduced the README row exactly, so `bb60b53` is the commit the README measured (the
  checkout, at `6241734`, differs in four test files).
- Production transfers **missed** (339, predicted 480-560): the send estimate was grep-derived and
  over-counted. SAFE008 hit (98, predicted 95-100).

## B0: spawn wrappers own their functions

`//gosree:iso` on the function parameters of `safego.SafeGo`, `SafeGoWG`, `SafeGoOnPanic`,
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
- 8 `//gosree:holds` were added on the `*Locked` helpers (`RuntimeJournal` x6,
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

`//gosree:imm` on the two that qualify, and the missing `holds` on `rewriteFileEntriesLocked`.

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

Every flagged production transfer gets one `//gosree:unsafe` on its own line above the transfer, its
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
code around each write position GoSree reports):

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
func-typed or exported fields, and are written only while they are built. GoSree has no claim for
"mutable type, frozen after construction", so each sharing transfer needs a hatch. The ratio is
far above the 20% abandon line while the tables hold 42 entries, and more entries would not move it:
no flagged transfer is blocked by a third-party type. The next checked claim has to be about the
dependency containers themselves, an imm rule that accepts interface and func-typed fields whose
values are set only during construction.

## Item 4 (2026-11-10)

The plan is the 2026-11-09 design ruling for README item 4: re-measure on the merged main, gate on a
`-gcflags=-m` differential, then a census of what blocks each `IMM_BLOCKED` hatch, which decides
whether the frozen-after-construction imm rule is worth building. Work is on branch `item4`, in a
worktree off `3c9677c`, because another session was editing `passes/ownership` in the main checkout.

### Step 1: re-measure on `3c9677c`

Prediction (ruling): production 476 / 345 / 72.5%, `-tests` 1,798 / 450 / 25.0%, 0 stale, all 476 rows
anchor; finding counts unchanged except message wording.

Result: the ratios, flagged counts and stale count reproduce exactly. Finding counts do not:
- **A merge bug.** `reportRelated` keyed de-duplication on the message, which names the transfer, so one
  use was reported once per transfer reaching it: production SAFE003 without hatches went from 364 to
  1,795 (up to 133 at one position, `router.go:563`), all at hatched positions. Fixed: SAFE003,
  SAFE003-forkjoin and SAFE008 are keyed on position and rule again (a use site is one defect); other
  findings keep the message, since two arguments moved at one call, or an address taken and a field
  written, are distinct defects. Test `useAfterTwoMoves` (moves.go) fails on `3c9677c`.
- After the fix, against item 3's binary (`074fd05`) with every hatch removed: no new flagged position
  in either stratum. Removed: 5 production (4 test) uses after a send in `select`, which the merge's
  per-case join excludes; these are the 5 fewer muted findings (972 to 967). Added at positions item 3
  already flagged: distinct arguments moved by one call (+13 SAFE002, +6 SAFE004), now reported apart.

Prediction missed: `-tests` remaining findings 1,879 against the predicted 1,702 ± 10, before the
de-duplication was narrowed to the use-site rules.

### Step 2: `-gcflags=-m` differential

`TestEscapeOracleNoFreshOrWritesOracle` compares every exported function GoSree summarises in `bytes`,
`strings`, `strconv`, `sort`, `bufio`, `io`, `encoding/json` and `net/http` against `-gcflags=-m=2`:
each parameter the compiler says leaks must be leaked, linked, or Unknown in the FuncFact.

Prediction: 0 violations (ruling); under 5% of compared parameters (mine). Both wrong on the first run:
75 of 480. After reading the compiler's flow for each:
- excused by rule, counted not compared: 22 through a lock or atomic inside a sync or sync/atomic
  function (the compiler cannot see past the runtime semaphore; GoSree counts locks as synchronisation),
  8 stores back into the parameter's own memory (`b.buf = append(b.buf, c)`), 21 string fields;
  results of shareable types (`error`, `string`, `context.Context`) are skipped;
- 15 reviewed by hand, callee source read (string data through `pathAddrs`, `findHandler`,
  `serveFile`, `Cookie.String` and so on), listed in the test with reasons; an entry that stops
  disagreeing fails the test;
- 2 real: `http2ClientConn.RoundTrip` and `http2RoundTripper.RoundTrip` pass a bound interface method
  to a function that calls it through a function value. GoSree applied a closure body's retention of
  its captures only where the closure is called statically; through any other call the retention was
  lost. Reading `graph.call` showed the general hole (not in the ruling): a closure that stores a
  capture in a package variable, handed to a function that calls it dynamically, left the capture
  owned. Fixed (`closureRetains`): the retention is charged where the closure is made unless every use
  is a static call, a defer, a go statement or a spawner or iso argument. Tests in `moves/dynamic.go`
  fail on `3c9677c`.

Narrowed when item 4 merged with main: a returned closure is no longer charged. It reaches the caller through
the result, which main's fresh-result summaries link to the captures. Charging it made every closure
factory (`http.StripPrefix`, `sync.OnceFunc`) leak its arguments: 14 false findings across main's
stress fixtures, and `factoryResultIsOwned` in `moves/dynamic.go` fails without the narrowing. What a
returned closure's body keeps when the caller calls it through a function value is still charged
nowhere.

Final: 409 compared, 0 unreviewed violations. Race corpus 229/36 and 229/42, unchanged. ScholarLM with
the closure fix (the baseline for what follows): production 476 / 345 / 72.5% unchanged, 18 more muted
findings at hatched transfers, no new remaining finding; `-tests` 1,798 / 1,020 flagged / 451 / 25.1%.

### Step 3: census

`gosree-corpus -census` resolves each production `IMM_BLOCKED` row's named value to a type and walks
every module struct it reaches (fields, and the types stored into interface fields), classing what
stops each from being imm under the frozen rule (classes in `census.go`; fixture test
`TestCensusClasses`).

Predictions: `*llm.Client` ends the chain of at least 188 hatches and at most 20 are interface, func or
exported only (ruling); at least 50% reach a post-construction write (mine). Refuter (ruling): at least
40% of hatches reach a post-construction write.

Result, 324 hatches, 18 unresolved (`(caller's parameter)`, closures):

| Worst class | Hatches | Share |
|---|---|---|
| W-lock: written under a lock, read in a function that takes none | 216 | 66.7% |
| W: written after construction, no lock | 41 | 12.7% |
| C: unguarded map or slice | 18 | 5.6% |
| X: external type | 29 | 9.0% |
| OK | 2 | 0.6% |
| unresolved | 18 | 5.6% |

257 hatches (79.3%) reach a post-construction write; 217 (67.0%) reach `*llm.Client`. All three
predictions hold, and the refuter fires. The 79.3% is an upper bound: W counts builder setters
(`UnifiedResearchRuntime.WithClaimStore`) that a `//gosree:init` claim (proposed, not implemented) could clear. Read by hand,
`llm.Client.ensureClient` dials lazily and writes `c.client` once, nil-checked, under `c.mu` at the
first gRPC call, and `EmbedBatch` reads it without the lock after calling `ensureClient` on the same
goroutine. `SetClient` replaces it, but has no production caller (82 test files call it). So in
production this is a one-time publication under a lock, race-free, but after construction: no
construction-time claim covers it.

Correction (re-plan ruling, verified): the worst class is not the lever. Allowing classes cumulatively
over the 306 resolved hatches, OK and G clear 2; adding W-lock and W still 2; adding X 37; only adding C
reaches 306. 215 closures carry C, G, OK, W, W-lock and X together, so no single claim moves them. C is
added for any unguarded map or slice field whether or not it is written after construction
(`census.go`), so it is an upper bound, and its slack is what decides. The frozen rule as designed
clears 2 of 324 (72.5% to 72.1%); the 62-68% prediction is withdrawn.

Committed separately: the step 1-2 checker fixes (`741e714`).

### Step 3b: census v2

Re-plan ruling: split C by whether the collection's contents are written after construction, and
follow module interfaces to their implementations instead of calling them external. `mutates` follows
each post-construction load of an unguarded collection field (and each `Field` of a struct held by
value): map updates, element stores, `append`, `copy` into it, `delete`, `clear`, storing it, returning
it, boxing it, or passing it to a call whose GoSree summary writes or keeps it are writes; unexported
module callees are followed into their bodies three levels deep. A collection whose every load and
reassignment holds a lock is guardable (G), like a field. Fixture cases for each, every one failing on
the first version; the summary path and the lock propagation were each checked by breaking them.

Pre-registered (ruling): if C-written is in at least 50% of the big closures, the service fails the
thesis at this pin whatever claims are added; refuter: closures whose collections are only frozen make
up at least 60% of those with any. Mine: C-written in at least 90% of the big closures; after the
guardable split, still at least 50%.

Result (306 resolved hatches; 219 big closures, six or more classes):
- C-written in 215 of 219 big closures, 212 on content writes alone (map update, append, delete,
  element store, copy, clear, reassignment). Frozen-only: 20 of 239 hatches with any collection (8%).
  The ruling's failure condition fires; the refuter does not. Both of my predictions hold.
- Waterfall, allowing classes cumulatively: OK 2, G 12, C-frozen 15, E 15, X 45, W 73, W-lock 87; only
  C-written reaches 306.

Read by hand, the content writes in the big closures come from two types:
- `wisdev.ADKRuntime.toolToPlug` (196 hatches reach it): filled by `registerPlugins`, which
  `loadADKRuntime` calls on the runtime it has just built, before returning it (`adk_runtime.go:258-264`).
  Construction in practice; a `//gosree:init` claim (proposed, not implemented) would cover it.
- `wisdev.endpointBreaker.failures` (192): updated by `record` after each webhook delivery
  (`downstream_events.go:184-197`), on the one worker goroutine `DownstreamEventSink` starts (line 262).
  Runtime state, but confined to that goroutine; no GoSree claim says so.

Without those two, 16 of the 219 big closures keep a content write. As bounds on the production ratio,
assuming every other claim (guarded_by rows, frozen collections, init, published-once, overrides for
every external type):
- with both left as they are: at most 92 of 306 hatches clear, ratio at least 53.2%; the service fails
  at this pin;
- with both covered (an init claim and a goroutine-confinement claim): at most 290 clear, ratio at
  least 11.6%, under the 20% line.

Both readings checked: `registerPlugins` has one caller, `loadADKRuntime`; every use of the breaker
(`allow` at line 444, `record` at 474, 498, 537) is in `deliverTo`, which only the worker's `run` and
`drain` reach (lines 405-439), so the confinement is real and the breaker is race-free.

The 11.6% bound also assumes something the list above does not name: that a collection returned from a
method, or stored into another struct, is not written through that alias. Counting those as writes
too, the bound is at least 53.6% whether or not the two types are covered (90 of 306 clear). What
carries it: `BeliefState.Beliefs`, stored into a proposal by `AutonomousLoop.buildStateResearchProposal`
(199 hatches reach it), and three `*int` limits of the ADK config copied by `decodeADKConfigStrict`
while the config is decoded (198; construction in practice). `Beliefs` looks like per-session state
reached through the type graph, not something the long-lived containers hold; not verified.

Limits of the census, all leaning towards clearing more than a checker could prove: it walks types,
not instances, so per-request state whose type a long-lived field mentions counts as part of the
container (the case above); a struct held by value (`ServerConfig`) and changed through a local copy
is seen as construction; a field with no recorded load counts as all-locked. The fixture covers pointer
receivers only.

Against the pre-registration: the refuter did not fire (20 of 239 frozen-only), so the re-plan's branch
is to record the failure at this pin and close item 4 with B9. The case for deviating is that the
content writes the condition counts come from an init-time map and a goroutine-confined one, both
race-free, and that covering them could bring the bound under 20%; it holds only if aliased
collections such as `Beliefs` are request state, which the census cannot tell because it does not see
instances. That is the owner's gate (step 3 of the re-plan).

### Decision: record the failure at `bb60b53` (owner, 2026-11-10)

The owner took the pre-registered branch. Recorded: **ScholarLM's `go_orchestrator` at `bb60b53` fails
the thesis.** Its production hatch ratio is 72.5% with 42 of 100 override entries, and census v2's
bound on what any set of construction-time claims could reach is 53.2% (at least 53.6% if aliased
collections count as written): above the 20% line. The two types behind that bound are an init-time
map and a goroutine-confined one, both race-free; whether claims for them would bring the bound under
20% depends on instance-level facts the census cannot see (above). Item 4 builds no new claim. A later
pin of ScholarLM may be measured only as a labelled second row beside this one.

### B9: guarded_by on the mutex-protected package variables

The 51 package variables item 3 sorted as mutex-protected get `//gosree:guarded_by` rows: 40 generated
from item 3's triage (each anchored at the declaration position the triage recorded and checked to
name the variable; each guard checked to be a package `sync.Mutex` or `sync.RWMutex`), 10 by hand (4
declarations whose text repeats as a reset inside a function, anchored after their mutex; 6 struct
variables guarded by their own `mu` or embedded mutex, a row per field, 8 rows), plus `//gosree:holds`
on two `*Locked` helpers. The 51st, `globalSteeringBus`, is left unclaimed (below). Dropped: a `holds` on the test hook `MockProjectIDForTest`, whose callers take no
lock, and the 3 rows for `steeringBus`'s fields (below).

Predictions and results (production). In all three runs the ratio is unchanged at 72.5%, with `-tests`
at 25.1%. Every row applied, because an anchor that matches nothing stops the run, and no directive
muted nothing.

| Batch | SAFE006 predicted | SAFE006 | SAFE009 predicted | SAFE009 |
|---|---|---|---|---|
| 40 generated rows | about 33 | 33 | 10-25 | **35 (missed)** |
| + 15 hand rows (12 kept) | 22 (or about 29 if field guards do not clear the variable) | 22 | not predicted | 49 |
| - steeringBus rows, + one holds | 23 | 23 | 36 | 36 |

The 28 new SAFE009, read one by one:
- 23 values read under the mutex leave it (a singleton pointer, an `io.Writer`, an observer func, a
  map or struct returned or stored): the publish pattern, reported by design. Whether each value is
  safe to share is its own type's question.
- 3 false: a write inside a `sync.Once.Do` closure while the caller holds the lock
  (`shared_engine.go:39-48`); the lockset does not carry a held lock into a closure run synchronously.
- 1 false: a slice swapped out under the lock and ranged over afterwards (`startup_diagnostics.go:91-99`).
- 1 false: an `int` boxed into a variadic `slog` argument counts as a value leaving the lock;
  `checkLeaves` tests the boxed interface's type, not the int (`lockset.go`).

Left out, also false: with rows on `steeringBus`'s fields, 11 SAFE009 fire on accesses made while
`globalSteeringBus.mu` is held; without the rows, one SAFE006 stays. The likely cause, handed over and
not confirmed in `lockset.go`: every access reloads the package pointer `globalSteeringBus`, so the
held mutex and the guarded field are not tied to the same base.

SAFE006 left: 23 = 13 test seams, 7 `sync.Once`, 2 generated, 1 `globalSteeringBus`. The `sync.Once`
exemption and the four lockset false-positive classes are in files another session is changing
(`lockset.go`, `globals.go`); they were handed to it instead of fixed here.

## Re-measured on `gosree` (2026-11-10)

The figures above were measured with the item-4 branch before it merged `main`. The merge commit
`7b2f023` brought in `ffc95cf`, which rewrote much of `passes/ownership` (lockset, globals, graph,
summaries), and the production stratum changed with it. Nobody re-recorded it then. Each row below is
a production run (no `-tests`) on a `git archive` of `bb60b53` with this ledger, on go1.27.3:

| Tool commit | Transfers | Flagged | Hatched | Hatch ratio | Remaining findings |
|---|---|---|---|---|---|
| `ffedea6` (item 4 before the merge) | 476 | 345 | 345 | 72.5% | SAFE001 19, SAFE006 23, SAFE009 36 |
| `7b2f023` (the merge) | 519 | 387 | 346 | 66.7% | SAFE001 22, SAFE002 36, SAFE003 43, SAFE004 20, SAFE006 117, SAFE008 3, SAFE009 36 |
| `f9b5915` | 516 | 386 | 345 | 66.9% | SAFE001 22, SAFE002 34, SAFE003 32, SAFE004 16, SAFE005 1, SAFE006 117, SAFE008 5, SAFE009 36 |

- `ffedea6` reproduces the B9 row exactly, so the input and toolchain are unchanged and the difference
  is the tool. `fc0a3d0`, the other commit the merge brought in, changes only documentation.
- The lower ratio is not progress. 41 more transfers are flagged and none of them has a ledger row,
  so their findings stay unmuted. If each takes a hatch, as every flagged transfer in B4 did, the
  ratio is 386 / 516 = 74.8%. SAFE006 rose from 23 to 117 with the same 66 `guarded_by` rows; which
  variables the new findings are on has not been examined. Neither change has been triaged.
- Both figures are far above the 20% threshold, so the recorded failure at `bb60b53` stands.
- On `f9b5915`, `unsafe_stale` comes from `safestale`, which runs the borrow and errcheck passes as
  dependencies: 0 of 333 directives mute nothing. On the commit before it, that run died with a stack
  overflow in `passes/borrow.canHoldBorrow` on a type that contains a slice of itself.
- Run time on `f9b5915`: 134 s wall, 7.8 GB peak RSS, under a load average near 200.
