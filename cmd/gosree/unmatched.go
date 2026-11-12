package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// goListTimeout bounds the one go list the wildcard check runs.
const goListTimeout = 30 * time.Second

// unmatchedWildcards names the wildcard patterns among the positional arguments that match no
// package. The driver is silent about one of them when another pattern matches (and fails only when
// none does), so a typo in `./a ./b/...` would silently check less than was asked. The answer is a
// warning only: the exit status is the driver's. matches reports which of the given patterns match
// at least one package; when it cannot answer (an error, a timeout) nothing is reported, since
// "could not ask" is not "matched nothing".
func unmatchedWildcards(args []string, matches func(patterns []string) (map[string]bool, error)) []string {
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
	var wild []string
	for _, pat := range positional {
		if strings.Contains(pat, "...") {
			wild = append(wild, pat)
		}
	}
	if len(wild) == 0 {
		return nil
	}
	matched, err := matches(positional)
	if err != nil {
		return nil
	}
	var out []string
	for _, pat := range wild {
		if !matched[pat] {
			out = append(out, pat)
		}
	}
	return out
}

// goListMatches runs one `go list -e` over all patterns (as the go command resolves them, honouring
// GOFLAGS, so -tags) and reads each package's Match field, the patterns that matched it, so every
// result is attributed to its pattern by go itself. -e keeps a package with errors in the list:
// only "no package at all" leaves a pattern unmatched.
func goListMatches(patterns []string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), goListTimeout)
	defer cancel()
	args := append([]string{"list", "-e", "-f", `{{join .Match "\n"}}`, "--"}, patterns...)
	out, err := exec.CommandContext(ctx, "go", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err) // includes a timeout
	}
	matched := map[string]bool{}
	for _, m := range strings.Fields(string(out)) {
		matched[m] = true
	}
	return matched, nil
}
