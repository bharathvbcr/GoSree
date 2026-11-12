package ownership_test

import (
	"strings"
	"testing"
)

// A call that publishes an argument and hands something back: LoadOrStore returns the value now in
// the map, Swap the one that was there. The result may be the argument, so the two are one piece
// of memory to the checker, and the result is not known to be fresh. That made the argument "not
// owned" at the very call that publishes it: it "may alias the result" of that call. The result
// does not exist before the call, so it says nothing about who held the argument when it was
// published. It does when the call can run again: the argument may then be what an earlier round
// handed back.
const publishOwnResultHead = `package p

import "sync"

type Config struct{ Limit int }

var sm sync.Map
`

func publishOwnResultDiags(t *testing.T, fn string) []string {
	t.Helper()
	return advDiagLines(t, publishOwnResultHead+"\n"+fn+"\n", "is not owned here")
}

func TestPublishingCallsOwnResultDoesNotDisownItsArgument(t *testing.T) {
	for name, fn := range map[string]string{
		"LoadOrStore, the result kept":             "func use() any {\n\tc := &Config{Limit: 1}\n\tactual, _ := sm.LoadOrStore(\"k\", c)\n\treturn actual\n}",
		"Swap, the result kept":                    "func use() any {\n\tc := &Config{Limit: 1}\n\tprev, _ := sm.Swap(\"k\", c)\n\treturn prev\n}",
		"LoadOrStore, the result read":             "func use() bool {\n\tc := &Config{Limit: 1}\n\tactual, loaded := sm.LoadOrStore(\"k\", c)\n\treturn loaded && actual != nil\n}",
		"control: LoadOrStore, the result dropped": "func use() {\n\tc := &Config{Limit: 1}\n\tsm.LoadOrStore(\"k\", c)\n}",
	} {
		if got := publishOwnResultDiags(t, fn); len(got) != 0 {
			t.Errorf("%s: the value is this function's own when it is published, and it was reported: %s", name, strings.Join(got, "; "))
		}
	}
	for name, fn := range map[string]string{
		// What a round hands back is published by the next: that is a value out of the map.
		"in a loop, the result published again": "func use() any {\n\tvar v any = &Config{Limit: 1}\n\tfor i := 0; i < 2; i++ {\n\t\tv, _ = sm.LoadOrStore(i, v)\n\t}\n\treturn v\n}",
		"the result published by a second call": "func use() {\n\tc := &Config{Limit: 1}\n\tactual, _ := sm.LoadOrStore(\"k\", c)\n\tsm.Store(\"j\", actual)\n}",
		"the caller's value":                    "func use(c *Config) any {\n\tactual, _ := sm.LoadOrStore(\"k\", c)\n\treturn actual\n}",
		"a value out of the map":                "func use() any {\n\told, _ := sm.Load(\"j\")\n\tactual, _ := sm.LoadOrStore(\"k\", old)\n\treturn actual\n}",
	} {
		if got := publishOwnResultDiags(t, fn); len(got) == 0 {
			t.Errorf("%s: the value published is not this function's own, and that was not reported", name)
		}
	}
}
