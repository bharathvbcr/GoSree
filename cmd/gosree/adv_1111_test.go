package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	advBin  string
	advOnce sync.Once
	advErr  error
)

func getGosreeBin(t *testing.T) string {
	t.Helper()
	advOnce.Do(func() {
		tmp, err := os.MkdirTemp("", "gosree-adv-*")
		if err != nil {
			advErr = err
			return
		}
		advBin = filepath.Join(tmp, "gosree")
		if out, err := exec.Command("go", "build", "-o", advBin, "github.com/bharathvbcr/gosree/cmd/gosree").CombinedOutput(); err != nil {
			advErr = errors.New(string(out))
		}
	})
	if advErr != nil {
		t.Fatal(advErr)
	}
	return advBin
}

func advModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["go.mod"] = "module example.com/m\n\ngo 1.24\n"
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func advRun(t *testing.T, dir string, args ...string) (stderr string, code int) {
	t.Helper()
	cmd := exec.Command(getGosreeBin(t), args...)
	cmd.Dir = dir
	var eb bytes.Buffer
	cmd.Stderr = &eb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return eb.String(), code
}

// With -tags foo the driver loads ./tagged (its only file is //go:build foo), but the unmatched
// wildcard check runs `go list` before GOFLAGS carries -tags, so it warns that nothing was checked.
func TestAdvUnmatchedWildcardHonoursTags(t *testing.T) {
	dir := advModule(t, map[string]string{
		"ok/ok.go":         "package ok\n",
		"tagged/tagged.go": "//go:build foo\n\npackage tagged\n",
	})
	stderr, code := advRun(t, dir, "-tags", "foo", "./ok", "./tagged/...")
	if strings.Contains(stderr, "matched no packages") {
		t.Errorf("false warning under -tags foo (exit %d): %s", code, stderr)
	}
}

// A directory literally named x.cfg is a package pattern, not a go vet config file.
func TestAdvPackageDirectoryNamedDotCfg(t *testing.T) {
	dir := advModule(t, map[string]string{"x.cfg/x.go": "package xcfg\n"})
	stderr, code := advRun(t, dir, "./x.cfg")
	if code != 0 || strings.Contains(stderr, "is a directory") {
		t.Errorf("gosree ./x.cfg: exit %d, stderr %q", code, stderr)
	}
}
