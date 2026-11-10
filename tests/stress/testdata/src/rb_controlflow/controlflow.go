// Package rb_controlflow feeds the analyzers unusual control flow: goto spaghetti, irreducible
// loops, fallthrough, select with many cases and nil channels, defer in loops, recover, and named
// results rewritten by deferred closures.
package rb_controlflow

type Job struct{ ID int }

// irreducible: two entries into one loop (a goto into the middle of a cycle).
func irreducible(ch chan *Job, c bool) {
	j := &Job{}
	if c {
		goto B
	}
A:
	j.ID++ // HIT(SAFE003)
B:
	if j.ID < 3 {
		ch <- j
		goto A
	}
}

// spaghetti: labeled break/continue/goto across nested loops.
func spaghetti(ch chan *Job) {
	j := &Job{}
outer:
	for i := 0; i < 3; i++ {
	inner:
		for k := 0; k < 3; k++ {
			switch {
			case k == i:
				continue outer
			case k > i:
				break inner
			case i == 2:
				goto done
			}
		}
	}
	ch <- j
	return
done:
	j.ID = 1
}

// fallthroughSend: the send is reached by fallthrough.
func fallthroughSend(ch chan *Job, n int) {
	j := &Job{}
	switch n {
	case 0:
		j.ID = 1
		fallthrough
	case 1:
		ch <- j
		fallthrough
	case 2:
		j.ID = 2 // HIT(SAFE003)
	}
}

// manySelect: a select with many cases, some on nil channels.
func manySelect(a, b, c, d chan *Job, e chan int) {
	j := &Job{}
	var nilch chan *Job
	select {
	case a <- j:
	case b <- j:
	case nilch <- j:
	case v := <-c:
		_ = v
	case <-d:
	case e <- 1:
	default:
	}
	j.ID = 1 // HIT(SAFE003)
}

// deferInLoop: a deferred call per iteration captures the moved value.
func deferInLoop(ch chan *Job) {
	for i := 0; i < 3; i++ {
		j := &Job{}
		ch <- j
		defer func() { j.ID = i }() // HIT(SAFE003)
	}
}

// recovered: the send happens before a panic that is recovered.
func recovered(ch chan *Job) (err error) {
	j := &Job{}
	defer func() { // HIT(SAFE003): reported at the defer, not at the write inside it
		if r := recover(); r != nil {
			j.ID = 9
		}
	}()
	ch <- j
	panic("boom")
}

// loopWithGotoBack: a backward goto forms the loop.
func loopWithGotoBack(ch chan *Job) {
	j := &Job{}
	i := 0
top:
	if i < 3 {
		i++
		j.ID = i // HIT(SAFE003)
		ch <- j
		goto top
	}
}

// switchType with many cases.
func switchType(ch chan any, v any) {
	j := &Job{}
	switch x := v.(type) {
	case int, int8, int16, int32, int64:
		_ = x
		ch <- j
	case string:
	case nil:
	default:
	}
	j.ID = 1 // HIT(SAFE003)
}
