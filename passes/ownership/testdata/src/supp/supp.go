package supp

import (
	"net/http"
	"unsafe"
)

type Job struct{ ID int }

// A //gosree:unsafe on a handler registration covers every shared write in the handler (SAFE008).
func suppressedHandler() {
	hits := map[string]int{}
	//gosree:unsafe the test only checks suppression
	http.HandleFunc("/s", func(_ http.ResponseWriter, r *http.Request) { hits[r.URL.Path]++ })
}

// One on a write covers that write only, and does not count as a suppressed transfer.
func writeLineHatch() {
	var a, b int
	http.HandleFunc("/w", func(http.ResponseWriter, *http.Request) {
		//gosree:unsafe a is a best-effort counter in this test
		a++
		b++ // want `SAFE008: captured variable b is written here`
	})
}

func use(*Job) {}

// A //gosree:unsafe on the transfer covers every finding that transfer produces: the parameter is not
// owned (SAFE002) and is used after the move (SAFE003).
func suppressedGo(p *Job) {
	//gosree:unsafe the caller hands p over and never touches it again
	go use(p)
	p.ID = 1
}

// The same over a channel send, with a deferred call registered before the move.
func suppressedSend(ch chan *Job) {
	j := &Job{}
	defer func() { j.ID = 1 }()
	//gosree:unsafe the receiver is done with j before this function returns
	ch <- j
	j.ID = 2
}

// Suppressing one transfer does not mute another one.
func otherTransferStillReported(p *Job) {
	//gosree:unsafe the caller hands p over and never touches it again
	go use(p)
	q := &Job{}
	go use(q)
	q.ID = 2 // want `SAFE003: composite literal is used after it was passed to a goroutine`
}

// Suppression at the use site still works.
func useSiteSuppression() {
	j := &Job{}
	go use(j)
	//gosree:unsafe the goroutine only reads ID, which this write does not race with in the test
	j.ID = 3
}

// A scope that mutes only a SAFE001 finding (another pass's rule) is used, not stale.
func rawPointer(p *Job) uintptr {
	//gosree:unsafe the test only checks that SAFE001 uses the scope
	return uintptr(unsafe.Pointer(p))
}

// A transfer inside an unsafe scope that has nothing to report is not counted as suppressed.
func cleanTransferInScope() {
	//gosree:unsafe nothing to suppress here
	go use(&Job{})
}
