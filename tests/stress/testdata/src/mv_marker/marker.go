// Package mv_marker compares the cap marker forms with the comment forms.
//
// cap.Move (cap/cap.go: "explicitly marks an ownership transfer") is a transfer of its argument
// whose result is the new, owned value. The three uses of the result below were rejected as
// SAFE003 by graph.go when this fixture was first run (the result was joined into the moved
// argument's class); the working tree at the time of the final run accepts them, so they are kept
// as clean controls that lock that change in.
package mv_marker

import "github.com/bharathvbcr/gosree/cap"

type Job struct{ ID int }

func worker(j *Job) { j.ID++ }

// cap.Move written in the send it marks.
func moveInSend(ch chan *Job) {
	j := &Job{}
	ch <- cap.Move(j)
}

// cap.Move written in the go statement it marks.
func moveInGo() {
	j := &Job{}
	go worker(cap.Move(j))
}

// cap.Move into a new local owner: `let k = j` in Rust.
func moveRebind() int {
	j := &Job{}
	k := cap.Move(j)
	k.ID = 1
	return k.ID
}

// control: the old name is dead after cap.Move.
func moveOldName() {
	j := &Job{}
	_ = cap.Move(j)
	j.ID = 1 // HIT(SAFE003)
}

// control: the same send without the marker is clean.
func plainSend(ch chan *Job) {
	j := &Job{}
	ch <- j
}

// cap.Move of a parameter is a move of something not owned.
func moveParam(j *Job) {
	_ = cap.Move(j) // HIT(SAFE002)
}

type Pool struct{}

func (Pool) Put(j cap.Iso[Job]) { go worker(j) }

// control: a marker iso receiver-method parameter on a direct call.
func methodMarker(p Pool) {
	j := &Job{}
	p.Put(j)
	j.ID = 1 // HIT(SAFE003)
}

// the marker parameter reached through a method value: same hole as the comment form (mv_iso).
func methodMarkerValue(p Pool) {
	put := p.Put
	j := &Job{}
	put(j)
	j.ID = 1 // MISS(SAFE003): put is Pool.Put, whose cap.Iso parameter started a goroutine on j
}

// a local alias of the marker type is not recognised (annot.capSymbol matches the cap package or
// the bare names, not an alias declared elsewhere), so the parameter is a plain foreign parameter.
type Owned[T any] = cap.Iso[T]

func takeAliased(j Owned[Job]) { go func() { j.ID++ }() } // FP(SAFE002): j is declared cap.Iso through the alias Owned; the same signature spelled cap.Iso[Job] is accepted (Pool.Put)
