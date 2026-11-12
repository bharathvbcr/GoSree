# Roadmap

[← README](../README.md) | [Architecture](ARCHITECTURE.md) | [Rules](RULES.md) | [Directives](DIRECTIVES.md) | [Borrow & NLL](BORROW_CHECKING_AND_NLL.md) | [Concurrency](CONCURRENCY_MODEL.md) | [Error Discipline](ERROR_DISCIPLINE.md) | [Tooling](TOOLING_AND_ERASURE.md) | [Roadmap](ROADMAP.md)

---

The README and `docs/` are the design of record: GoSree checks memory where it crosses goroutines, and leaves
aliasing within one goroutine alone. In build order:

Done:
- callback functions as goroutine boundaries;
- SAFE001 for dot imports of `unsafe`, cross-package `unsafe.Pointer` laundering, and functions without a Go body;
- SAFE008 for handlers that write shared memory;
- the error-discipline pass: ERR001-ERR005 (`safeerr`), solved by a Rust solver hosted in-process over
  Gusset, with a pure-Go oracle that must return the same bytes ([Error Discipline](ERROR_DISCIPLINE.md));
- one `cmd/gosree` driver, whose preflight sets the exit status (0 clean, 1 no verdict, 2 usage,
  3 findings), refuses `-fix` and `-diff`, and hands `-tags` to the go command;
- standard-library concurrent container capability contracts (`capContracts`: `atomic.Pointer`, `atomic.Value`,
  `sync.Map`, `sync.Pool`, `context.WithValue`), preventing publication races and torn-header corruption statically;
- lockset fixpoint convergence with path-prefix widening and DAG post-order worklist, eliminating self-hosting CPU hangs;
- alias graph union-by-size and deterministic SSA value tie-breaking (`compareSSAValOrder`), resolving `RB-ORDER`;
- NLL same-line, loop-carried back-edge, and backward-`goto` borrow tracking;
- translation validation via `cmd/gosree-erase -verify`, and a reference model of the container hook rules (`internal/formal`; the Lean sketch in `specs/` is not machine-checked);
- items 1, 2 and 3 below;
- item 4, closed with the reference service failing the thesis at its pin.

1. **Done:** a guarded-by rule. It came before item 2, since recording writes in summaries turns calls like
   `h.cache.Set(...)` into SAFE008 findings that need it. The guarded SAFE008 findings on the
   reference service are its acceptance cases. Five steps, from the 2026-11-09 design ruling:
   - **Done:** parse `//gosree:guarded_by mu` and `//gosree:holds mu`.
   - **Done:** SAFE006 checked where the package variable is declared. Race corpus: 229/234, and
     38 → 36 race-free tests clean. `TestNoRaceFinGlobal` (fixed by `//gosree:guarded_by Mutex`) and
     `TestNoRaceRpcChan` write package variables outside init.
   - **Done:** lock tracking and SAFE009, with SAFE008 leaving guarded writes to SAFE009. No
     annotations exist in the corpus or the reference service, so neither moved. On a scratch copy
     of the reference service with 8 `guarded_by` and 1 `holds` added:
     - SAFE008 went from 13 to 1 (the unguarded test).
     - SAFE009 found 2: `paper.go:759` returns the guarded `*TTLCache` (a policy call: accept it, or
       override the type), and a stress test reads `len(windows)` unlocked.

     The design review predicted 3. Its third, `client_telemetry_test.go:335`, reads under
     `limiter.mu.Lock()`, so no finding is right.
   - **Done:** `//gosree:imm` types may have guarded fields of any type. The same change closes a
     hole: the address of a field or element of a shared value (`&cfg.port` on an imm `*Config`)
     used to look owned and could be moved to a goroutine. It is now foreign (SAFE002). The race
     corpus and the reference service did not move, as predicted.
   - **Done:** guarded local variables, and the corpus scored a second time on an annotated copy:
     229 / 40, as predicted before the run ([the annotated copy](../README.md#the-annotated-copy)). The design
     review predicted 229 / 42 from the earlier base of 38. A write to a guarded field of a package
     variable is no longer SAFE006. The reference service did not move (no annotations).
2. **Done:** summaries that record writes, a function override table, and summaries that link
   arguments and results, plus summaries of the locks a function returns holding. Four steps, from
   the 2026-11-09 design ruling, each measured against a prediction made before its run:
   - **Writes bit.** SAFE008 follows static calls into other packages through the callee's summary.
     Reference service: SAFE008 13 → 101, as predicted, and no other category moved. 88 of the 101
     are calls: handlers in `internal/api` passing a captured singleton
     (`*wisdev.IdempotencyStore`, `*RuntimeStateStore`, `*RuntimeJournal`, `*AgentGateway`,
     `*llm.Client`) to a method that writes it under a mutex nothing declares guards it. Three
     callees are outside the module, one finding each: `websocket.Upgrader.Upgrade`,
     `require.NotEmpty` and `t.Fatalf`. The corpus did not move.
   - **Function overrides** (`internal/kinds/funcs.go`) and `net.Listener` as a type override.
     SAFE002 784 → 736. The prediction was a drop of 30 to 43, so it **missed**. The 48 break down
     as follows:
     - 37 from the entries' own causes: `context.WithCancel` 27, `context.WithTimeout` 8,
       `io.ReadAll` and `json.Marshal` one each;
     - 5 from `net.Listen`: 7 findings, of which 2 now name their next cause, `grpc.NewServer`;
     - 5 from three in-module functions that return a `WithCancel` or `json.Marshal` result, which
       became fresh too;
     - 1 whose cause was a parameter merged with a listener.

     The prediction left two effects out. Freshness is transitive. And as a type override,
     `net.Listener` also stopped `ln.Close()` after a goroutine took the listener from counting as
     a use or an escape (SAFE003 −12, SAFE004 −6).
   - **Links.** The prediction was SAFE004 −24 to −30, with SAFE002 falling and SAFE003 rising. It
     **missed on SAFE004**, which rose. Two corrections to the ruling's design came first:
     - **Pointer depth.** A link records the pointer depth where the two values meet, not only that
       they meet. Joining them at the top made `(*llm.Client).WithTimeout`, which writes a copy of
       its receiver, a writer of the receiver: 4 false SAFE008 findings.
     - **Every event.** The summary records links from every event in a region, not only the events
       before the first leak. Stopping early made the recorded links depend on map order, and
       could drop the link of a result that is also not fresh.

     Measured with both: SAFE002 736 → 644, SAFE003 673 → 811, SAFE003-forkjoin 202 → 286 and
     SAFE004 341 → 414. Transfers with a SAFE002 or SAFE004 fell from 784 to 695: 95 cleared and 6
     newly flagged. 113 of the 114 new SAFE004 positions are on transfers that were already flagged.
     A constructor's result that used to be foreign is now joined to its argument (a handler built
     from `cfg`), and that argument's own escape (`cfg` passed to `ensureAgentGateway`) is now
     visible on the handler. The SAFE003 rise is the second and later captures of one linked struct,
     as predicted. Two corpus race tests changed categories ([in the README](../README.md#measured-on-the-go-race-detectors-corpus)).
   - **Lock-acquire summaries.** Annotated corpus 229 / 40 → 229 / 42, as predicted. Reference
     service unchanged (no annotations).

   GoSree has no summary for `sync/atomic`'s functions (they have no Go body), so the writes bit
   counted `atomic.AddInt64(&h.n, 1)` in a handler as a write. That contradicts the README's rule that
   atomic calls are not stores, so `sync/atomic` functions write nothing. Like `lockOps`, this is
   vocabulary, not a budget entry. Only SAFE008 reads writes, and it stayed at 101 with and without
   the rule.
3. The hatch ratio measured on the reference service: ScholarLM's `backend/go_orchestrator`, pinned
   at ScholarLM commit `bb60b53`, measured with `-tests` on go1.27.3. Untriaged baselines, each
   with 0 `//gosree:unsafe`:

   | | Transfers | SAFE001 | SAFE002 | SAFE003 | SAFE003-forkjoin | SAFE004 | SAFE006 | SAFE008 | Findings |
   |---|---|---|---|---|---|---|---|---|---|
   | Links and lock-acquire summaries | 1,642 | 104 | 644 | 811 | 286 | 414 | 238 | 101 | 2,598 |
   | Function overrides | 1,642 | 104 | 736 | 673 | 202 | 341 | 238 | 101 | 2,395 |
   | Writes bit | 1,642 | 104 | 784 | 685 | 202 | 347 | 238 | 101 | 2,461 |
   | SAFE006 at the declaration | 1,642 | 104 | 784 | 685 | 202 | 347 | 238 | 13 | 2,373 |
   | With SAFE008 (`8add3ad`) | 1,642 | 104 | 784 | 685 | 202 | 347 | 130 | 13 | 2,265 |
   | With callbacks (`b56b798`) | 1,642 | 104 | 784 | 685 | 202 | 347 | 130 | - | 2,252 |
   | Before callbacks (`538bbe2`) | 1,258 | 104 | 511 | 458 | 193 | 263 | 123 | - | 1,652 |

   SAFE001 is mostly generated protobuf code and tests. The 384 transfers callbacks added are mostly
   HTTP handlers: `internal/api` went from 365 to 633. The 13 SAFE008 findings are on 6 handler
   registrations:
   - `paper.go` ×7 and `client_telemetry.go` ×3, mostly reached through helper calls;
   - `login.go:181` and `mcp_wiring.go:46`;
   - one test.

   Every non-test write is under a mutex: checked line by line in the 2026-11-09 design review, plus
   `client_telemetry.go:119`, which that review's prototype missed.

   **Done: the triage.** The triage log is
   [`refservice/scholarlm-bb60b53.md`](../cmd/gosree-corpus/refservice/scholarlm-bb60b53.md). It has
   each batch's prediction, made before its run, and its result. The ledger,
   [`scholarlm-bb60b53.tsv`](../cmd/gosree-corpus/refservice/scholarlm-bb60b53.tsv), has one row per
   directive with its verdict. Both strata are measured on a `git archive` of `bb60b53`: the
   `-tests` baseline reproduced the row above exactly. The production stratum (no `-tests`) is the
   headline, since the abandon criterion is about a service, not its tests.

   | Stratum | Transfers | Flagged | Hatched | Hatch ratio | Races found |
   |---|---|---|---|---|---|
   | production | 476 | 345 | 345 (all) | **72.5%** | 0 |
   | `-tests` | 1,798 | 1,014 | 450: the production 345, plus 105 transfers at a seeded sample of 100 of the 575 test-only positions | 25.0% measured (sample only); 56.4% if every flagged transfer takes a hatch, as every sampled one did | 0 in the sample |

   The design review predicted 50-70% and 30-50%: both **missed** high (the `-tests` figure by its
   extrapolation). These are the figures at the end of the triage; item 4 below records how they
   moved when the branch merged `main`.

   "Races found" is what the reading found, not a proof of absence. 12 production transfers and 17
   sampled test transfers were read by hand; the other hatches were classified by rules over each
   transfer's findings, and the SAFE008 callees were sampled (7).

   Beside the ratio:
   - checked claims: 14 `guarded_by`, 9 `holds`, 1 imm, 8 iso;
   - 0 stale `//gosree:unsafe`;
   - overrides 42 of 100. This item added `context.CancelFunc`, whose documentation says it may be
     called by several goroutines at once.

   What changed the count, in order:
   - **Spawn wrappers.** `//gosree:iso` on the service's spawn wrappers (`safego.SafeGo*`,
     `detachedTasks.spawn*`) turned 137 production escapes into transfers. The design review had
     found that its 113 `SafeGo*` calls were missing from the denominator.
   - **`guarded_by` on the mutex-protected singletons.** SAFE008 went from 98 to 75. The rest are
     writes the writes bit over-counts: interface and func-field calls (19), and writes to fresh or
     local memory unified with the receiver (56). None of the 7 callees sampled writes shared memory
     outside a lock.
   - **Imm census.** Of the dependency types handlers and goroutines share, only `IdempotencyStore`
     can be imm.
   - **Hatches.** One hatch per flagged transfer. 324 of the 333 are `IMM_BLOCKED`: the transfer
     shares a long-lived dependency (the router's `ServerConfig`, the `wisdevServer`, the
     `AgentGateway`, a handler struct) whose type has interface, func-typed or exported fields.

   Of the 73 production SAFE006, a reading of the source sorts 51 as mutex-protected, 13 as test
   seams, 7 as `sync.Once` and 2 as generated. Item 4 annotated the mutex-protected ones (B9).

   The abandon criterion applies at 100 table entries, and this item ends at 42. So the measurement
   does not decide abandonment, but the diagnosis says more entries would not help: no flagged
   transfer is blocked by a third-party type. The ratio is set by dependency containers that are
   mutable in type and frozen in practice. GoSree needs a checked claim for them: an imm rule that
   accepts interface and func-typed fields set only during construction.
4. **Closed: the reference service fails the thesis at `bb60b53`.** The 2026-11-09 design ruling
   planned three steps: a re-measure, a differential test against `-gcflags=-m`, and a census of what
   blocks each `IMM_BLOCKED` hatch. The census was to decide whether to build the
   frozen-after-construction imm rule. Each step's prediction and result is in
   [the triage log](../cmd/gosree-corpus/refservice/scholarlm-bb60b53.md#item-4-2026-11-10).
   - **Re-measure** on `3c9677c`: both ratios reproduce. It found a merge bug: de-duplication was
     keyed on the message, which names the transfer, so one use was reported once per transfer
     reaching it. Production SAFE003 went from 364 to 1,795, all at hatched positions. Fixed in
     `741e714`.
   - **Differential test** (`TestEscapeOracleNoFreshOrWritesOracle`). It compares 409 parameters
     from eight standard-library packages with `-gcflags=-m=2`. 0 violations are left unreviewed; 15
     were reviewed by hand. It found one real hole: a closure's retention of its captures was lost
     when the closure was called through a function value. Fixed in `741e714`. After the fix, the
     race corpus is unchanged, production is unchanged and `-tests` is 25.1%.
   - **Census** (`gosree-corpus -census`). The frozen rule as designed clears 2 of 324 hatches (72.5%
     → 72.1%), so it was not built. Census v2 splits collections by whether their contents are
     written after construction. Collections written that way are in 215 of 219 big closures: the
     pre-registered failure condition. Its refuter, that at least 60% of hatches with a collection
     have only frozen ones, did not fire (20 of 239). Granting every other claim, the production ratio
     stays at or above 53.2%.

     Two race-free types carry the content writes: a map filled during initialisation and a
     goroutine-confined circuit breaker. Claims covering both would bound the ratio at 11.6%, but
     only if collections reached through aliases are per-request state. A census that walks types,
     not instances, cannot tell. The owner took the pre-registered branch: record the failure. A
     later ScholarLM pin may be measured only as a labelled second row.

     Re-measured at the same pin (`bb60b53`) on 2026-11-11, after the census was made to follow
     generic type arguments and the elements of arrays, slices, maps, channels and pointers (it had
     missed `Box[B]`, `[]*B` and `map[K]*B` edges), and to give every type in a reference cycle one
     class:
     - The frozen rule clears 1 of 324 hatches, not 2.
     - Collections written after construction are in 240 of 244 big closures, so the failure
       condition still holds.
     - The refuter still does not fire (18 of 261).
     - 264 of 324 hatches (81.5%) reach a post-construction write, against 257 (79.3%) before.

     The 53.2% and 11.6% bounds were derived by hand from census v2 and have not been re-derived.
   - **B9:** `guarded_by` on 50 of the 51 mutex-protected package variables. SAFE006 went from 73 to
     23 and SAFE009 from 8 to 36, with the ratio unchanged at 72.5%. Of the 28 new SAFE009, 23 are
     values read under a lock that leave it, reported by design, and 5 are lockset false positives.
     `globalSteeringBus` stays unclaimed, because claiming its fields gives 11 false SAFE009.
   - **Re-measured after the merge.** The figures above are from the item-4 branch before it merged
     `main` (`ffedea6` reproduces them exactly). The merge brought in `ffc95cf`'s ownership rewrite.
     On `f9b5915` the production stratum is 516 transfers, 386 flagged, 345 hatched: 66.9%, or 74.8%
     if the 41 newly flagged transfers each take a hatch; SAFE006 is 117. After the container
     contracts and the cross-package unsafe rule (`6b12630`) it is 562 transfers, 415 flagged, 344
     hatched: 61.2%, or 73.8% if every flagged transfer takes a hatch; SAFE001 remaining is 370.
     `-tests` failed closed on the error solver's 16,384-function limit until `80a766b` raised it to
     262,144. It now completes: 4,805 transfers, 9.2% hatched, untriaged (2026-11-12). None of this is triaged,
     and the failure at `bb60b53` stands
     ([the triage log](../cmd/gosree-corpus/refservice/scholarlm-bb60b53.md#re-measured-on-gosree-2026-11-10)).
   - **Reopened as one experiment (2026-11-12): a parameter shared for reading.** Pre-registered,
     with a gate before any build: under 69 of the 343 hatched transfers cleared, the claim is not
     built. A scratch probe on `9167e27` ran the write walk from every parameter and checked every
     call site. The claim would clear 1 hatched transfer (17 with the caller side waived and
     escapes ignored), so the ratio would stay at 59.2%. It was not built, and the checker is
     unchanged. The write check binds, not provenance: of the 164 claims the hand-overs need, the
     walk finds a write, or cannot rule one out, through 118, at a callee's summary (55), a direct
     write (30), a dynamic call (17) or an interface method (16)
     ([the triage log](../cmd/gosree-corpus/refservice/scholarlm-bb60b53.md#item-4-reopened-as-one-experiment-a-parameter-shared-for-reading-2026-11-12)).
5. Open:
   - ownership of the channel's send end; joins through `select`;
   - `SafeGoWG`'s join (43 SAFE003-forkjoin findings);
   - the writes bit's two over-counts (75 SAFE008);
   - SAFE006 for `sync.Once`-published variables, and B9's five lockset false positives;
   - claims for initialisation-time and goroutine-confined state. Item 4 showed these are what a
     later pin would need.
   - what a called function does to memory it is handed: the write check at a module function's
     summary, an interface method and a function value. The reopened experiment showed that no
     claim about sharing clears a hatch until this is answered.

The project should be abandoned if a `net/http` reference service has a hatch ratio above 20% (see
[Measuring the escape hatch](../README.md#measuring-the-escape-hatch-on-a-module)) once the type and function
override tables together hold 100 entries. Item 4 applied a sharper test to the reference service: it
fails if census v2's fully annotated bound cannot reach 20% in production. At `bb60b53` the bound is
53.2%, so it fails at that pin with 42 of the 100 entries used.

