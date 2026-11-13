// Package mv_idiom runs common Go concurrency idioms past the move check. Every function is correct
// Go a careful reviewer would accept; reports on them are false positives, each with its mechanism.
package mv_idiom

import (
	"bytes"
	"iter"
	"slices"
	"strconv"
	"sync"
)

type Item struct {
	ID  int
	Out string
}

// control: worker pool. Workers receive owned items and send them on; the parent collects.
func workerPool(n int) []string {
	jobs := make(chan *Item)
	results := make(chan *Item)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range jobs {
				it.Out = strconv.Itoa(it.ID)
				results <- it
			}
		}()
	}
	go func() {
		for i := 0; i < n; i++ {
			jobs <- &Item{ID: i}
		}
		close(jobs)
	}()
	go func() { wg.Wait(); close(results) }()
	out := make([]string, n)
	for it := range results {
		out[it.ID] = it.Out
	}
	return out
}

// distinct elements of one slice handed to one goroutine each, joined by a WaitGroup. The alias
// graph is index-insensitive (graph.go IndexAddr joins the slice's class), so the next iteration's
// &items[i] -- an address computation, no memory access -- is a use of the moved slice.
func perElement(n int) []Item {
	items := make([]Item, n)
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		go func(it *Item) {
			defer wg.Done()
			it.ID = i
		}(&items[i]) // FP(SAFE003): each goroutine gets a different element; the loop only computes the next address
	}
	wg.Wait()
	return items
}

// the same with WaitGroup.Go; the closure captures one element pointer per iteration.
func perElementGo(n int) []Item {
	items := make([]Item, n)
	var wg sync.WaitGroup
	for i := range items {
		it := &items[i] // FP(SAFE003): a different element each iteration; Wait below joins them all
		wg.Go(func() { it.ID = i })
	}
	wg.Wait()
	return items
}

// batching: send the full batch, count it, start a fresh one. len reads the sender's own slice
// header, a value; it touches no memory the receiver can write.
func batcher(in <-chan int, out chan<- []int) int {
	total := 0
	batch := make([]int, 0, 8)
	for v := range in {
		batch = append(batch, v)
		if len(batch) == cap(batch) {
			out <- batch
			total += len(batch) // FP(SAFE003): len of the local slice header, not the shared backing array
			batch = make([]int, 0, 8)
		}
	}
	return total
}

// request/reply by value: the struct is copied into the channel, and the sender reads the reply
// channel from its own copy.
type request struct {
	key   *string
	reply chan int
}

func askByValue(reqs chan<- request, key string) int {
	r := request{key: &key, reply: make(chan int, 1)}
	reqs <- r
	return <-r.reply // FP(SAFE003): r is the sender's own copy; only its chan field is read
}

// control: the same with the reply channel read before the send.
func askByValueFirst(reqs chan<- request, key string) int {
	reply := make(chan int, 1)
	reqs <- request{key: &key, reply: reply}
	return <-reply
}

// the clone idiom: append to a nil slice copies the bytes. append is joined with every argument
// (graph.go builtin "append"), so the copy is taken to alias the parameter.
func sendClone(ch chan<- []byte, b []byte) {
	ch <- append([]byte(nil), b...) // FP(SAFE002): the bytes are copied into a new array; nothing aliases b
}

// the library clone has the same summary: its result is linked to its argument.
func sendBytesClone(ch chan<- []byte, b []byte) {
	ch <- bytes.Clone(b) // FP(SAFE002): bytes.Clone returns a new array
}

func sendSlicesClone(ch chan<- []int, b []int) {
	ch <- slices.Clone(b) // FP(SAFE002): slices.Clone of a pointer-free slice shares nothing
}

// control: cloning a slice of pointers does share the pointees.
func sendCloneOfPtrs(ch chan<- []*Item, b []*Item) {
	ch <- append([]*Item(nil), b...) // HIT(SAFE002)
}

// a goroutine that receives a value as an argument owns it and sends it on. Parameters always get
// a foreign event (graph.go buildGraph); only free variables of a goroutine body are owned
// (cfg.go closureIsGoroutineBody), so the classic pre-Go-1.22 form is rejected.
func goArgForward(out chan<- *Item) {
	for i := 0; i < 3; i++ {
		it := &Item{ID: i}
		go func(it *Item) {
			it.Out = "done"
			out <- it // FP(SAFE002): it was moved into this goroutine by the go statement
		}(it)
	}
}

// control: the same with a captured variable is accepted.
func goCaptureForward(out chan<- *Item) {
	for i := 0; i < 3; i++ {
		it := &Item{ID: i}
		go func() {
			it.Out = "done"
			out <- it
		}()
	}
}

// range-over-func: the loop body is a synthetic closure whose loop variable is a parameter, so a
// fresh value yielded by the iterator is foreign in the body.
func fresh(n int) iter.Seq[*Item] {
	return func(yield func(*Item) bool) {
		for i := 0; i < n; i++ {
			if !yield(&Item{ID: i}) {
				return
			}
		}
	}
}

func forwardFresh(out chan<- *Item) {
	for it := range fresh(3) {
		out <- it // FP(SAFE002): every value fresh yields is a new allocation nothing else holds
	}
}

// control: the same values from a slice built here.
func forwardSlice(out chan<- *Item) {
	for i := 0; i < 3; i++ {
		out <- &Item{ID: i}
	}
}

// control: hand off, then nil the variable.
func handOffNil(ch chan<- *Item) {
	it := &Item{}
	ch <- it
	it = nil
	_ = it
}

// control: fork-join through a done channel, then reuse.
func forkJoin() int {
	it := &Item{}
	done := make(chan struct{})
	go func() {
		it.ID = 42
		close(done)
	}()
	<-done
	return it.ID
}

// control: future.
func future(n int) <-chan []int {
	ch := make(chan []int, 1)
	buf := make([]int, n)
	go func() {
		for i := range buf {
			buf[i] = i
		}
		ch <- buf
	}()
	return ch
}
