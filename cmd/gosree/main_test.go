package main_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGoSreeCLI(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "gosree")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("failed to build gosree: %v\n%s", err, string(out))
	}

	cmd := exec.Command(bin, "-flags")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gosree -flags failed: %v", err)
	}
	var flags []struct {
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(out, &flags); err != nil {
		t.Fatalf("unmarshal -flags output: %v", err)
	}
	want := map[string]bool{
		"borrow":        false,
		"safeannot":     false,
		"safeerr":       false,
		"safeownership": false,
		"safeunsafe":    false,
	}
	for _, f := range flags {
		if _, ok := want[f.Name]; ok {
			want[f.Name] = true
		}
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("gosree missing analyzer flag -%s", name)
		}
	}
}
