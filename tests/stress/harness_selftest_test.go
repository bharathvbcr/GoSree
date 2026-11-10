package stress_test

import (
	"strings"
	"testing"
)

// TestHarnessRejectsWrongMarkers feeds verify markers that disagree with the findings, in each of
// the four ways they can, and requires a problem for each. Without it a harness that accepted
// everything would make the whole suite pass.
func TestHarnessRejectsWrongMarkers(t *testing.T) {
	at := func(line int) lineKey { return lineKey{"f.go", line} }
	set := func(code string) map[string]bool { return map[string]bool{code: true} }
	found := func(line int, code string) finding {
		return finding{lineKey: at(line), code: code, message: code + ": x"}
	}
	cases := []struct {
		name string
		fx   fixture
		want string // substring of the single expected problem; "" for none
	}{
		{"hit reported", fixture{
			hit:      map[lineKey]map[string]bool{at(1): set("SAFE003")},
			findings: []finding{found(1, "SAFE003")}}, ""},
		{"miss stays silent", fixture{
			miss: map[lineKey]map[string]bool{at(1): set("SAFE003")}}, ""},
		{"false positive reported", fixture{
			fp:       map[lineKey]map[string]bool{at(1): set("SAFE002")},
			findings: []finding{found(1, "SAFE002")}}, ""},
		{"hit not reported", fixture{
			hit: map[lineKey]map[string]bool{at(1): set("SAFE003")}}, "a regression"},
		{"hit reported as another rule", fixture{
			hit:      map[lineKey]map[string]bool{at(1): set("SAFE003")},
			findings: []finding{found(1, "SAFE003"), found(1, "SAFE004")}}, "unexpected SAFE004"},
		{"miss now reported", fixture{
			miss:     map[lineKey]map[string]bool{at(1): set("SAFE003")},
			findings: []finding{found(1, "SAFE003")}}, "promote the marker to HIT"},
		{"false positive gone", fixture{
			fp: map[lineKey]map[string]bool{at(1): set("SAFE002")}}, "remove the marker"},
		{"unmarked finding", fixture{
			findings: []finding{found(7, "BORROW002")}}, "unexpected BORROW002"},
		{"finding on the wrong line", fixture{
			hit:      map[lineKey]map[string]bool{at(1): set("SAFE003")},
			findings: []finding{found(2, "SAFE003")}}, "a regression"},
		{"unmarked analyzer failure", fixture{
			failures: []string{"safeownership: panic"}}, "analyzer failed"},
		{"marked crash that did not happen", fixture{crash: true}, "remove the marker"},
		{"marked crash", fixture{crash: true, failures: []string{"safeownership: panic"}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := &ledger{byCode: map[string][3]int{}}
			problems := c.fx.verify(l)
			if c.want == "" {
				if len(problems) != 0 {
					t.Fatalf("want no problem, got %q", problems)
				}
				return
			}
			var matched bool
			for _, p := range problems {
				matched = matched || strings.Contains(p, c.want)
			}
			if !matched {
				t.Fatalf("want a problem containing %q, got %q", c.want, problems)
			}
		})
	}
}

// TestMarkersNeedReasons checks that a hole cannot be recorded without saying why it is one.
func TestMarkersNeedReasons(t *testing.T) {
	for text, want := range map[string]bool{
		"// MISS(SAFE003): the value was published":        true,
		"// HIT(SAFE002) MISS(SAFE003): the alias is live": true,
		"// MISS(SAFE003)":     false,
		"// MISS(SAFE003):":    false,
		"// MISS(SAFE003):   ": false,
	} {
		if got := markerHasReason(text, "MISS(SAFE003)"); got != want {
			t.Errorf("markerHasReason(%q) = %v, want %v", text, got, want)
		}
	}
}
