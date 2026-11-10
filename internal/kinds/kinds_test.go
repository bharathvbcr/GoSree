package kinds

import (
	"go/types"
	"testing"
)

func TestChanShareable(t *testing.T) {
	c := NewClassifier(nil)
	intType := types.Typ[types.Int]
	chanInt := types.NewChan(types.SendRecv, intType)

	if !c.Shareable(chanInt) {
		t.Errorf("expected chan int to be shareable")
	}
}
