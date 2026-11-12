// WITNESS: race
//
// A pointer boxed as error was classified shareable by the override table ("error": an unchecked
// convention that error values are not mutated), so passing it to a goroutine was no transfer.
// Reported since 2026-11-11: an interface value is tracked when the value boxed in it is.
package main

type Job struct{ ID int }

type jobErr struct{ j *Job }

func (e *jobErr) Error() string { return "job" }

func main() {
	j := &Job{}
	done := make(chan struct{})
	go func(e error) {
		e.(*jobErr).j.ID = 2
		close(done)
	}(&jobErr{j: j})
	j.ID = 1 // HIT(SAFE003)
	<-done
}
