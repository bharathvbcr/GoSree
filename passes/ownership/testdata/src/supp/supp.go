package supp

import "net/http"

type Job struct{ ID int }

// A //safe:unsafe on a handler registration covers every shared write in the handler (SAFE008).
func suppressedHandler() {
	hits := map[string]int{}
	//safe:unsafe the test only checks suppression
	http.HandleFunc("/s", func(_ http.ResponseWriter, r *http.Request) { hits[r.URL.Path]++ })
}

// One on a write covers that write only, and does not count as a suppressed transfer.
func writeLineHatch() {
	var a, b int
	http.HandleFunc("/w", func(http.ResponseWriter, *http.Request) {
		//safe:unsafe a is a best-effort counter in this test
		a++
		b++ // want `SAFE008: captured variable b is written here`
	})
}

func use(*Job) {}

// A //safe:unsafe on the transfer covers every finding that transfer produces: the parameter is not
// owned (SAFE002) and is used after the move (SAFE003).
func suppressedGo(p *Job) {
	//safe:unsafe the caller hands p over and never touches it again
	go use(p)
	p.ID = 1
}

// The same over a channel send, with a deferred call registered before the move.
func suppressedSend(ch chan *Job) {
	j := &Job{}
	defer func() { j.ID = 1 }()
	//safe:unsafe the receiver is done with j before this function returns
	ch <- j
	j.ID = 2
}

// Suppressing one transfer does not mute another one.
func otherTransferStillReported(p *Job) {
	//safe:unsafe the caller hands p over and never touches it again
	go use(p)
	q := &Job{}
	go use(q)
	q.ID = 2 // want `SAFE003: composite literal is used after it was passed to a goroutine`
}

// Suppression at the use site still works.
func useSiteSuppression() {
	j := &Job{}
	go use(j)
	//safe:unsafe the goroutine only reads ID, which this write does not race with in the test
	j.ID = 3
}

// A transfer inside an unsafe scope that has nothing to report is not counted as suppressed.
func cleanTransferInScope() {
	//safe:unsafe nothing to suppress here
	go use(&Job{})
}
