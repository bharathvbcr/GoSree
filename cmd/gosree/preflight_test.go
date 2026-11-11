package main

import (
	"slices"
	"strings"
	"testing"
)

var testAnalyzers = []string{"safeannot", "safeunsafe", "safeownership", "borrow", "safeerr"}

func TestParseCommandLine(t *testing.T) {
	for _, c := range []struct {
		name     string
		args     []string
		want     []string // arguments left for the driver
		goflags  string
		json     bool
		warnWith []string // a substring of each warning, in order
	}{
		{name: "plain", args: []string{"./..."}, want: []string{"./..."}},
		{name: "tags equals", args: []string{"-tags=a,b", "./..."}, want: []string{"./..."}, goflags: " -tags=a,b"},
		{name: "tags space", args: []string{"-tags", "a", "-c", "2", "./..."}, want: []string{"-c", "2", "./..."}, goflags: " -tags=a"},
		{name: "json", args: []string{"-json", "./..."}, want: []string{"-json", "./..."}, json: true},
		{name: "json false", args: []string{"-json=false", "./..."}, want: []string{"-json=false", "./..."}},
		{name: "tests off", args: []string{"-test=false", "./..."}, want: []string{"-test=false", "./..."}, warnWith: []string{"-test=false"}},
		{name: "one pass off", args: []string{"-safeunsafe=false", "./..."}, want: []string{"-safeunsafe=false", "./..."}, warnWith: []string{"-safeunsafe (SAFE001"}},
		{name: "one pass named", args: []string{"-borrow", "./..."}, want: []string{"-borrow", "./..."}, warnWith: []string{"-safeannot"}},
		{name: "pass on", args: []string{"-borrow=true", "-safeannot=true", "-safeunsafe=true", "-safeownership=true", "-safeerr=true", "./..."}, want: []string{"-borrow=true", "-safeannot=true", "-safeunsafe=true", "-safeownership=true", "-safeerr=true", "./..."}},
		{name: "fix off", args: []string{"-fix=false", "./..."}, want: []string{"-fix=false", "./..."}},
		{name: "flags after the pattern are packages", args: []string{"./...", "-fix"}, want: []string{"./...", "-fix"}},
		{name: "version", args: []string{"-V=full"}, want: []string{"-V=full"}},
		{name: "flags", args: []string{"-flags"}, want: []string{"-flags"}},
		{name: "flags false keeps checking", args: []string{"-flags=false", "-json", "-tags=a", "./..."}, want: []string{"-flags=false", "-json", "./..."}, goflags: " -tags=a", json: true},
	} {
		p, err := parseCommandLine(c.args, testAnalyzers)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !slices.Equal(p.args, c.want) || p.goflags != c.goflags || p.json != c.json {
			t.Errorf("%s: args %q goflags %q json %v; want %q %q %v", c.name, p.args, p.goflags, p.json, c.want, c.goflags, c.json)
		}
		if len(p.warnings) != len(c.warnWith) {
			t.Errorf("%s: warnings %q, want %d", c.name, p.warnings, len(c.warnWith))
			continue
		}
		for i, w := range c.warnWith {
			if !strings.Contains(p.warnings[i], w) {
				t.Errorf("%s: warning %q lacks %q", c.name, p.warnings[i], w)
			}
		}
	}
}

func TestParseCommandLineRefusals(t *testing.T) {
	for _, args := range [][]string{
		{"-fix", "./..."}, {"--fix", "./..."}, {"-fix=true", "./..."}, {"-diff", "./..."}, {"-fix", "-diff", "./..."},
		{"-tags", "a b", "./..."},
		{"-flags=false", "-fix", "./..."}, {"--flags=0", "-diff", "./..."},
	} {
		if _, err := parseCommandLine(args, testAnalyzers); err == nil {
			t.Errorf("%q was accepted", args)
		}
	}
}

func TestJSONExit(t *testing.T) {
	for _, c := range []struct {
		name string
		out  string
		want int
	}{
		{"empty", "", exitClean},
		{"no packages", "{}", exitClean},
		{"empty list", `{"p":{"safeunsafe":[]}}`, exitClean},
		{"finding", `{"p":{"safeunsafe":[{"posn":"a:1:1","message":"SAFE001: x"}]}}`, exitFindings},
		{"error", `{"p":{"safeunsafe":{"error":"boom"}}}`, exitFailed},
		{"finding and error", `{"p":{"safeunsafe":[{"posn":"a:1:1","message":"m"}],"borrow":{"error":"boom"}}}`, exitFailed},
		{"not json", "garbage", exitFailed},
	} {
		if got := jsonExit([]byte(c.out)); got != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, got, c.want)
		}
	}
}

func TestDedupJSONUnit(t *testing.T) {
	in := `{
	"m/b": {
		"safeownership": [
			{"posn": "b.go:3:5", "message": "SAFE006: global"}
		]
	},
	"m/b [m/b.test]": {
		"safeownership": [
			{"posn": "b.go:3:5", "message": "SAFE006: global"},
			{"posn": "b_test.go:9:2", "message": "SAFE003: test"}
		],
		"borrow": {"error": "boom"}
	}
}`
	out := string(dedupJSON([]byte(in)))
	if got := strings.Count(out, "SAFE006: global"); got != 1 {
		t.Errorf("SAFE006 count = %d, want 1; out:\n%s", got, out)
	}
	if got := strings.Count(out, "SAFE003: test"); got != 1 {
		t.Errorf("SAFE003 count = %d, want 1; out:\n%s", got, out)
	}
	if !strings.Contains(out, `"error": "boom"`) && !strings.Contains(out, `"error":"boom"`) {
		t.Errorf("analyzer error object lost; out:\n%s", out)
	}
	if code := jsonExit([]byte(out)); code != exitFailed {
		t.Errorf("jsonExit = %d, want %d", code, exitFailed)
	}
}
