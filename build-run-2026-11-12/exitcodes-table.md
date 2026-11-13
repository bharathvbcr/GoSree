# gosree exit codes, baseline f0dea98 (go1.27.3 darwin/arm64)

Raw 108 rows (2 engines x 6 targets x 9 modes): results.tsv in this dir. cgo and CGO_ENABLED=0 rows are byte-identical in exit and first stderr line (diff verified).
Targets: clean, find (BORROW004), tagged (finding only under //go:build foo), bad (type error), nope (nonexistent), "find bad" (both).

| mode | clean | find | tagged | bad | nope | find+bad |
|---|---|---|---|---|---|---|
| plain | 0 | 3 | 0 | 1 `cannot use "x" ...` | 1 `-: stat nope: directory not found` | 1 (type error line) |
| -json | 0 | 3 | 0 | 1 | 1 | 1 |
| -fix | 2 `gosree: -fix is not supported...` | 2 | 2 | 2 | 2 | 2 |
| -diff | 2 `gosree: -diff is not supported...` | 2 | 2 | 2 | 2 | 2 |
| -tags=foo | 0 | 3 | 3 (g_foo.go:7 BORROW004) | 1 | 1 | 1 |
| -tags=bar (absent) | 0 | 3 | 0 | 1 | 1 | 1 |
| -test=false | 0 + WARNING | 3 + WARNING | 0 + WARNING | 1 | 1 | 1 |
| -borrow=false | 0 + WARNING | 0 + WARNING (finding hidden) | 0 | 1 | 1 | 1 |
| go vet -vettool | 0 | 1 | 0 (tag not passed to vet) | 1 `# scratch/bad` | 1 `stat nope: directory not found` | 1 |

Extra runs: `-tags foo,bar` (separate arg) and `-tags=foo,bar` exit 3 on tagged; `-tags 'foo bar'` exit 2 `gosree: -tags takes a comma-separated list...`; `-nosuchflag` exit 2 `flag provided but not defined`; `clean ./e/...` (empty dir) exit 0 + `gosree: WARNING: ./e/... matched no packages`; `-json clean nope` exit 1; `-json -borrow=false find` exit 0 stdout `{}`; `-json -tags=foo tagged` exit 3.
gosree-gate (go build -toolexec): clean 0; find 1; bad 1; tagged with GOFLAGS=-tags=foo 1, without 0; no args 2 `Usage: gosree-gate <tool> [args...]`; `gosree-gate echo hi` 0.
