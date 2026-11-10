# gocap stress suite

Programs written to get past the checker. Each one is real Go that compiles; each line records what
gocap does with it today and whether that is right.

```bash
go test ./tests/stress            # markers, determinism, runtime witnesses, scale, driver parity
go test ./tests/stress -short     # markers only (about 3 s)
go test ./tests/stress -run TestStress -v   # the ledger
bash tests/stress/fingerprint.sh  # hash of the checker sources a result belongs to
```

The run logs a ledger on every pass: hits locked, known misses, known false positives, known
crashes, per rule. A green run means "the checker behaves exactly as recorded", not "no holes".
The holes are listed in [FINDINGS.md](FINDINGS.md), with the fingerprint its numbers were taken at.

| File | What it holds |
|---|---|
| `testdata/src/<area>_<topic>/` | the fixtures, one directory per topic; the area prefixes are in the next section |
| `harness_test.go`, `stress_test.go` | the marker checker, the witness runner, the crash probe |
| `harness_selftest_test.go` | proof the harness rejects wrong markers, clean witnesses and clean crash probes |
| `scale_test.go` | generated programs of growing size; bounds the growth, records the known blowups |
| `vetparity_test.go` | standalone `gocap` against `go vet -vettool`, and the CLI contract |

## Fixture families

The prefix is the area. `GOCAP_STRESS_ROOTS` matches on it.

| Prefix | Covers |
|---|---|
| `mv_` | moves: alias graph, flow, idioms, `//gocap:iso`, spawn-through-callee |
| `sp_` | spawners and fork-join: the goroutine-boundary table, publication without a spawn or send |
| `bw_` | borrows: BORROW001 to BORROW004, CAP003, non-lexical lifetimes |
| `im_` | immutability and globals: SAFE005, SAFE006, constructors, laundering through conversions |
| `lk_` | locks and handlers: SAFE008, SAFE009, `holds` contracts |
| `dx_` | unsafe, directive lexing, `//line`, build tags and the escape hatch |
| `rb_` | robustness: odd control flow, generics, range-over-func, closures, directives on every declaration |
| `rs_` | distance to Rust: guarantees Rust enforces that gocap does not |
| `er_` | error discipline (`safeerr`): `er_close` ERR005, `er_mustuse` ERR001, `er_shadow` ERR003, `er_typednil` ERR004, `er_unwrap` ERR002, `er_xpkg` ERR004 across packages |
| `adv_` | cross-cutting programs: `adv_break` mixes borrow, move, immutability, lock and error rules in one package; `adv_w_race` is a spawner-fact witness |
| `seed_` | worked examples for writing a fixture |

`_w_` (and the older `_wit_`, `_witness`, `_race_` spellings) names a fixture that is a runtime
witness. The harness gives the name no meaning: a fixture is a witness only if a file carries a
`// WITNESS:` marker (`witnessRE`, `harness_test.go:61`; `TestWitnesses`, `stress_test.go:156`).
A witness may carry `MISS` markers (the checker accepts a program the runtime convicts) or `HIT`
markers (the checker convicts it statically and the runtime agrees), as `er_w_typednil`,
`er_w_unchecked` (both `WITNESS: panic`, ERR004 and ERR002) and `adv_w_race` do.

The `er_` and `adv_` fixtures run under the same analyzer set as the other families; `safeerr`
is part of it.

The erase tool's suite is `cmd/gocap-erase/stress_erase_test.go`. In the two generated-input files
and there, a known bug is a test that asserts the bug is present and fails with "appears fixed"
when it is not.

## Markers

A marker goes in a trailing comment on the line the diagnostic is, or should be, reported on.

| Marker | Meaning | The suite fails when |
|---|---|---|
| `HIT(CODE)` | gocap reports CODE here, and should | it stops reporting (a regression) |
| `MISS(CODE): why` | gocap is silent here, and should report CODE | it starts reporting (promote to `HIT`) |
| `FP(CODE): why` | gocap reports CODE here, and should not | it stops reporting (remove the marker) |
| *(none)* | the line must be clean | anything is reported |

`CODE` is a rule id (`SAFE003`, `BORROW002`, `CAP003`, ...) or `NORULE` for a hazard no rule claims
to cover. Several markers may share a line. `MISS` and `FP` need a reason.

File-level markers, on a comment line of their own:

| Marker | Meaning |
|---|---|
| `// CRASH: why` | an analyzer fails on this package instead of reporting |
| `// WITNESS: race` | the file is a `package main` in which `go run -race` reports a data race |
| `// WITNESS: panic` | the file is a `package main` that `go run` ends in a panic or fatal error |

A witness is the evidence that a `MISS` is a real bug and not a matter of taste: the same file that
gocap accepts is convicted by the runtime. It is built in the GOPATH the analyzers load from, so it
may import the cap package and sub-packages of its own root.

A panicking analyzer takes its process with it, so a root with a `CRASH` line is analyzed in a
child process, and its other markers are not checked. Markers are matched on physical lines:
`//line` directives do not move them.

## Writing a fixture

1. Create `testdata/src/<area>_<topic>/` with one mechanism per function. Imports: the standard
   library and `github.com/bharathvbcr/gocap/cap` (the harness supplies the real package).
2. See what gocap says:
   ```bash
   GOCAP_STRESS_DUMP=<area>_<topic> go test ./tests/stress -run TestDump -v
   ```
3. Mark every reported line `HIT` or `FP`, and every line that should have been reported `MISS`.
   Put an accepted near-twin next to each miss, so the hole is pinned to one mechanism.
4. Check the area: `GOCAP_STRESS_ROOTS=<area>_ go test ./tests/stress`. A run with
   `GOCAP_STRESS_ROOTS` set is partial and says so.

The analyzers run in the order of `cmd/gocap/main.go`: annot, unsafeban, ownership, borrow, errcheck
(`cliAnalyzers`, `harness_test.go:53`).

## Running

```bash
go test ./tests/stress                          # everything, including witnesses and scale
go test ./tests/stress -short                   # markers and harness self-tests only
go test ./tests/stress -run TestStress -v       # markers and the ledger
go test ./tests/stress -run TestWitnesses -v    # `go run` / `go run -race` each WITNESS file
GOCAP_STRESS_ROOTS=er_,adv_ go test ./tests/stress -run TestStress   # a partial run, comma-separated prefixes
```

| Variable | Read by | Effect |
|---|---|---|
| `GOCAP_STRESS_ROOTS` | markers, witnesses | only fixture roots with one of these prefixes; the run logs `PARTIAL RUN` |
| `GOCAP_STRESS_DUMP` | `TestDump` | comma-separated roots whose diagnostics are printed unchecked |
| `GOCAP_SCALE_EXPLORE` | `TestScaleExplore` | size multiplier; the test skips when unset |
| `GOCAP_SCALE_ONLY`, `GOCAP_SCALE_MAXN` | `TestScaleExplore` | restrict to shapes whose name contains the value; cap the size |

`-short` skips `TestDeterministic`, `TestWitnesses`, the scale tests and the vet-parity tests, which
need the `gocap` binary built from `cmd/gocap`. Witnesses need a Go toolchain and the race detector
(cgo).
