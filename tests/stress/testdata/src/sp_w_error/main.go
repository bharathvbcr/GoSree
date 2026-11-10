// WITNESS: race
//
// A pointer boxed as error is classified shareable by the override table ("error": an unchecked
// convention that error values are not mutated), so passing it to a goroutine is no transfer and
// the parent keeps writing the memory the error carries.
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
	j.ID = 1 // MISS(SAFE003): j crossed inside an error value, which the checker treats as shareable
	<-done
}
