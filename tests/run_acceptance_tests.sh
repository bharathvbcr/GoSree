#!/usr/bin/env bash
# ==============================================================================
# E2E acceptance test runner for gosree (tests/e2e, tiers 1-4).
#
# Every number it prints is measured on this run: top-level tests passed, failed and
# skipped per tier come from `go test -v`, and expectations are the `// want` lines in
# each tier's testdata. The status is PASSED only when go test exits 0 and reported no
# failure; otherwise the go test output is printed and the script exits non-zero.
# ==============================================================================
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

TIER="all"
VERBOSE=false
JSON_OUTPUT=false

usage() {
  echo "Usage: $0 [--tier <1|2|3|4|all>] [-v|--verbose] [--json]"
  echo ""
  echo "  --tier <1|2|3|4|all>  run one acceptance tier (default: all)"
  echo "  -v, --verbose         stream go test -v output"
  echo "  --json                print the summary as JSON"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tier)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      TIER="$2"
      shift 2
      ;;
    -v|--verbose) VERBOSE=true; shift ;;
    --json) JSON_OUTPUT=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

case "${TIER}" in
  1|2|3|4) TEST_FILTER="^TestTier${TIER}_" ; TIERS=("${TIER}") ;;
  all) TEST_FILTER="^TestTier[1-4]_" ; TIERS=(1 2 3 4) ;;
  *) echo "invalid tier '${TIER}': choose 1, 2, 3, 4 or all" >&2; exit 2 ;;
esac

cd "${REPO_DIR}"
OUT="$(mktemp -t gosree-e2e.XXXXXX)"
trap 'rm -f "${OUT}"' EXIT

# testdata directories of each tier: tier 1 is t1_*, tiers 2-4 are t2_*, t3_*, t4_*.
want_lines() {
  local n=0 d
  for d in tests/e2e/testdata/src/t"$1"_*; do
    [[ -d "$d" ]] || continue
    n=$((n + $(grep -r --include='*.go' -c '// want' "$d" | awk -F: '{s += $NF} END {print s + 0}')))
  done
  echo "$n"
}

count() { grep -c -E "$1" "${OUT}" || true; }

SECONDS=0
if [[ "${VERBOSE}" == true ]]; then
  go test -count=1 -v -run "${TEST_FILTER}" ./tests/e2e 2>&1 | tee "${OUT}"
  TEST_EXIT_CODE=${PIPESTATUS[0]}
else
  go test -count=1 -v -run "${TEST_FILTER}" ./tests/e2e > "${OUT}" 2>&1
  TEST_EXIT_CODE=$?
fi
DURATION_S=${SECONDS}

TOTAL_PASS=0; TOTAL_FAIL=0; TOTAL_SKIP=0; TOTAL_WANT=0
declare -a ROWS=()
for t in "${TIERS[@]}"; do
  p=$(count "^--- PASS: TestTier${t}_")
  f=$(count "^--- FAIL: TestTier${t}_")
  s=$(count "^--- SKIP: TestTier${t}_")
  w=$(want_lines "$t")
  ROWS+=("$t $p $f $s $w")
  TOTAL_PASS=$((TOTAL_PASS + p)); TOTAL_FAIL=$((TOTAL_FAIL + f))
  TOTAL_SKIP=$((TOTAL_SKIP + s)); TOTAL_WANT=$((TOTAL_WANT + w))
done

if [[ "${TEST_EXIT_CODE}" -eq 0 && "${TOTAL_FAIL}" -eq 0 && "${TOTAL_PASS}" -gt 0 ]]; then
  STATUS="PASSED"
else
  STATUS="FAILED"
fi

if [[ "${JSON_OUTPUT}" == true ]]; then
  printf '{\n  "status": "%s",\n  "tier": "%s",\n  "go_test_exit_code": %d,\n  "duration_s": %d,\n  "tiers": [' \
    "${STATUS}" "${TIER}" "${TEST_EXIT_CODE}" "${DURATION_S}"
  sep=""
  for row in "${ROWS[@]}"; do
    read -r t p f s w <<< "${row}"
    printf '%s\n    {"tier": %d, "tests_passed": %d, "tests_failed": %d, "tests_skipped": %d, "want_lines": %d}' \
      "${sep}" "$t" "$p" "$f" "$s" "$w"
    sep=","
  done
  printf '\n  ],\n  "totals": {"tests_passed": %d, "tests_failed": %d, "tests_skipped": %d, "want_lines": %d}\n}\n' \
    "${TOTAL_PASS}" "${TOTAL_FAIL}" "${TOTAL_SKIP}" "${TOTAL_WANT}"
  [[ "${STATUS}" == "PASSED" ]] || exit 1
  exit 0
fi

echo "gosree acceptance tests (tests/e2e), tier ${TIER}, $(go version)"
printf '%-8s %8s %8s %8s %12s\n' "Tier" "Passed" "Failed" "Skipped" "want lines"
for row in "${ROWS[@]}"; do
  read -r t p f s w <<< "${row}"
  printf '%-8s %8d %8d %8d %12d\n' "$t" "$p" "$f" "$s" "$w"
done
printf '%-8s %8d %8d %8d %12d\n' "Total" "${TOTAL_PASS}" "${TOTAL_FAIL}" "${TOTAL_SKIP}" "${TOTAL_WANT}"
echo "Duration: ${DURATION_S}s"

if [[ "${STATUS}" == "PASSED" ]]; then
  echo "PASSED"
  exit 0
fi
echo "FAILED (go test exit code ${TEST_EXIT_CODE})"
[[ "${VERBOSE}" == true ]] || cat "${OUT}"
exit 1
