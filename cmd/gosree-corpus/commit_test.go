package main

import (
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"testing"
)

// TestGosreeCommitIgnoresWorkingDirectory runs gosreeCommit from inside an unrelated git checkout.
// The test binary carries no vcs stamp, so the answer must be the no-stamp marker, never that
// checkout's describe output.
func TestGosreeCommitIgnoresWorkingDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "x"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	want, err := func() ([]byte, error) {
		cmd := exec.Command("git", "describe", "--always", "--dirty")
		cmd.Dir = dir
		return cmd.Output()
	}()
	if err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	got := gosreeCommit()
	if strings.Contains(got, strings.TrimSpace(string(want))) {
		t.Fatalf("gosreeCommit() = %q names the working directory's checkout %q", got, strings.TrimSpace(string(want)))
	}
	if got != noVCSStamp {
		t.Fatalf("gosreeCommit() = %q, want %q for a binary without a vcs stamp", got, noVCSStamp)
	}
}

func stamped(settings ...debug.BuildSetting) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Settings: settings}, true }
}

func TestGosreeCommitFromBuildStamp(t *testing.T) {
	const rev = "8df1d77a1b2c3d4e5f60718293a4b5c6d7e8f901"
	cases := []struct {
		name string
		read func() (*debug.BuildInfo, bool)
		want string
	}{
		{"clean", stamped(
			debug.BuildSetting{Key: "vcs.revision", Value: rev},
			debug.BuildSetting{Key: "vcs.modified", Value: "false"}), "8df1d77"},
		{"dirty", stamped(
			debug.BuildSetting{Key: "vcs.revision", Value: rev},
			debug.BuildSetting{Key: "vcs.modified", Value: "true"}), "8df1d77-dirty"},
		{"short revision kept whole", stamped(
			debug.BuildSetting{Key: "vcs.revision", Value: "abc"}), "abc"},
		{"no build info", func() (*debug.BuildInfo, bool) { return nil, false }, noVCSStamp},
		{"build info without vcs settings", stamped(
			debug.BuildSetting{Key: "-buildvcs", Value: "false"}), noVCSStamp},
		{"empty revision", stamped(
			debug.BuildSetting{Key: "vcs.revision", Value: ""},
			debug.BuildSetting{Key: "vcs.modified", Value: "true"}), noVCSStamp},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			old := readBuildInfo
			readBuildInfo = c.read
			t.Cleanup(func() { readBuildInfo = old })
			if got := gosreeCommit(); got != c.want {
				t.Fatalf("gosreeCommit() = %q, want %q", got, c.want)
			}
		})
	}
}
