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
| `testdata/src/<area>_<topic>/` | the fixtures: `mv` moves, `sp` spawners and fork-join, `bw` borrows, `im` immutability and globals, `lk` locks and handlers, `dx` unsafe, directives and the hatch, `rb` robustness, `rs` distance to Rust |
| `harness_test.go`, `stress_test.go` | the marker checker, the witness runner, the crash probe |
| `harness_selftest_test.go` | proof the harness rejects wrong markers, clean witnesses and clean crash probes |
| `scale_test.go` | generated programs of growing size; bounds the growth, records the known blowups |
| `vetparity_test.go` | standalone `gocap` against `go vet -vettool`, and the CLI contract |

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

The analyzers run exactly as `cmd/gocap` runs them (annot, unsafeban, ownership, borrow).
