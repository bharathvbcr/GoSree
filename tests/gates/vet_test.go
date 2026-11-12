package gates_test

import (
	"strings"
	"testing"
)

func TestGoVetCleanPackage(t *testing.T) {
	_, gosreeBin := getGateAndGosree(t)
	repoRoot := getRepoRoot(t)

	res := runCommand(t, repoRoot, nil, "go", "vet", "-vettool="+gosreeBin, "./tests/e2e/testdata/src/t1_valid")
	if res.code != 0 {
		t.Fatalf("expected go vet exit 0 on clean package, got %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
	if len(strings.TrimSpace(res.stderr)) != 0 {
		t.Errorf("expected clean stderr, got: %s", res.stderr)
	}
}

func TestGoVetVulnerablePackages(t *testing.T) {
	_, gosreeBin := getGateAndGosree(t)
	repoRoot := getRepoRoot(t)

	cases := []struct {
		name        string
		relPath     string
		wantFinding string
	}{
		{
			name:        "witness sp_w_error",
			relPath:     "./tests/stress/testdata/src/sp_w_error",
			wantFinding: "SAFE003",
		},
		{
			name:        "witness rs_torn_slice",
			relPath:     "./tests/stress/testdata/src/rs_torn_slice",
			wantFinding: "SAFE005",
		},
		{
			name:        "witness rs_torn_iface",
			relPath:     "./tests/stress/testdata/src/rs_torn_iface",
			wantFinding: "SAFE005",
		},
		{
			name:        "witness rs_w_mapwrites",
			relPath:     "./tests/stress/testdata/src/rs_w_mapwrites",
			wantFinding: "SAFE003",
		},
		{
			name:        "unsafeban u",
			relPath:     "./passes/unsafeban/testdata/src/u",
			wantFinding: "SAFE001",
		},
		{
			name:        "borrow rules",
			relPath:     "./passes/borrow/testdata/src/borrows",
			wantFinding: "BORROW",
		},
		{
			name:        "errcheck rules",
			relPath:     "./passes/errcheck/testdata/src/errbasic",
			wantFinding: "ERR001",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runCommand(t, repoRoot, nil, "go", "vet", "-vettool="+gosreeBin, tc.relPath)

			// go vet standard: exit 1 on findings
			if res.code != 1 {
				t.Fatalf("expected go vet exit 1 on %s, got %d\nstdout: %s\nstderr: %s", tc.name, res.code, res.stdout, res.stderr)
			}

			combined := res.stdout + res.stderr
			if !strings.Contains(combined, tc.wantFinding) {
				t.Errorf("expected %s in go vet output for %s, got:\n%s", tc.wantFinding, tc.name, combined)
			}
		})
	}
}
