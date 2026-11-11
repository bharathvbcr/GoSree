package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// TestSharedLoadHatchCoversOnlyItsLoad: a //gosree:unsafe on one Load must not mute the write that
// a second, unhatched Load of the same container reaches through a shared callee. The value in
// mutate is reached from both Loads, so it is checked as unmuted.
func TestSharedLoadHatchCoversOnlyItsLoad(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"hatchscope/hatchscope.go": `package hatchscope

import "sync/atomic"

type Config struct{ Limit int }

var live atomic.Pointer[Config]

func a() {
	//gosree:unsafe the write in mutate was reviewed for this caller only
	c := live.Load()
	mutate(c)
}

func b() {
	c := live.Load()
	mutate(c)
}

func mutate(c *Config) {
	c.Limit = 1 // want "write through shared value loaded from"
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "hatchscope")
}
