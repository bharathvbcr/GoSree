package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// unmatchedWildcards names the wildcard patterns among the positional arguments that match no
// package. The driver is silent about one of them when another pattern matches (and fails only when
// none does), so a typo in `./a ./b/...` would silently check less than was asked. The answer is a
// warning only: the exit status is the driver's. list reports the packages a pattern matches; a
// pattern it cannot list is not reported, since "could not ask" is not "matched nothing".
func unmatchedWildcards(args []string, list func(pattern string) ([]string, error)) []string {
	positional := args[flagEnd(args):]
	if len(positional) < 2 { // alone, an empty pattern is the driver's own error
		return nil
	}
	for _, a := range args[:flagEnd(args)] {
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch name {
		case "V", "flags", "help", "h":
			return nil // answered without analysing anything
		}
	}
	var out []string
	for _, pat := range positional {
		if !strings.Contains(pat, "...") {
			continue
		}
		if pkgs, err := list(pat); err == nil && len(pkgs) == 0 {
			out = append(out, pat)
		}
	}
	return out
}

// goList lists the import paths a pattern matches, as the go command (honouring GOFLAGS, so -tags)
// resolves it. -e keeps a package with errors in the list: only "no package at all" counts.
func goList(pattern string) ([]string, error) {
	out, err := exec.Command("go", "list", "-e", "-f", "{{.ImportPath}}", pattern).Output()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %w", pattern, err)
	}
	return strings.Fields(string(out)), nil
}
