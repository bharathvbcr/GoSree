package moves

var kept *Job // want `SAFE006: package variable kept is written outside init`

// run calls f through a function value, so its own summary cannot say what f retains.
func run(f func()) { f() }

// A closure that stores its capture in a package variable retains it whenever it runs, however it
// is called: handing the closure to run, which calls it dynamically, still leaves j shared through
// kept, so the goroutine does not own j.
func closureRetainsThroughDynamicCall() {
	j := &Job{}
	run(func() { kept = j })
	go func() { // want `SAFE004: the closure is captured by a goroutine but also escapes`
		j.ID = 1
	}()
}

type keeper2 interface{ keep() }

type jobRef struct{ j *Job }

func (jobRef) keep() {}

// A bound method value of an interface carries its receiver; whoever calls it hands the receiver to
// a method gocap cannot see, which may retain it, and with it j.
func boundMethodRetains() {
	j := &Job{}
	var k keeper2 = jobRef{j}
	run(k.keep)
	go func() { // want `SAFE004: the closure is captured by a goroutine but also escapes`
		j.ID = 1
	}()
}

// A closure that only reads its capture retains nothing, so passing it to run leaves j owned.
func closureReadsThroughDynamicCall() {
	j := &Job{}
	n := 0
	run(func() { n = j.ID })
	_ = n
	go func() {
		j.ID = 1
	}()
}
