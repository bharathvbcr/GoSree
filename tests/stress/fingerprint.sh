#!/usr/bin/env bash
# Prints one hash over the checker's non-test sources: the passes, the directive parser, the type
# classifier, both commands and the cap package. A stress-suite result is a statement about the
# checker at one fingerprint; FINDINGS.md records the one its numbers were taken at.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${root}"
find passes internal cap cmd/gosree cmd/gocap-erase -name '*.go' ! -name '*_test.go' ! -path '*/testdata/*' -print0 |
  LC_ALL=C sort -z | xargs -0 shasum -a 256 | shasum -a 256 | cut -c1-16
