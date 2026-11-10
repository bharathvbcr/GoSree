// Package rb_closures feeds the analyzers closures nested five deep, mutually recursive closures,
// method values and method expressions on generic, embedded and interface types, func-typed fields
// and variadic calls.
package rb_closures

type Job struct{ ID int }

func (j *Job) Bump() { j.ID++ }

// deep: the send is five closures in, the use is in the outermost function.
func deep(ch chan *Job) {
	j := &Job{}
	func() {
		func() {
			func() {
				func() {
					func() {
						ch <- j // HIT(SAFE002): the hazard is real, but it is the use below that should be reported
					}()
				}()
			}()
		}()
	}()
	j.ID = 1 // MISS(SAFE003): the transfer happened inside an immediately-invoked (same-goroutine) closure; the move is not propagated to the enclosing function
}

// deepGo: a goroutine started five closures in.
func deepGo() {
	j := &Job{}
	func() {
		func() {
			func() {
				func() {
					go func() { j.ID = 2 }() // HIT(SAFE002): the race with line 39 is real; it is reported here under SAFE002
				}()
			}()
		}()
	}()
	j.ID = 1 // MISS(SAFE003): the transfer happened inside an immediately-invoked (same-goroutine) closure; the move is not propagated to the enclosing function
}

// deepNoUse: the same five-deep send, and the enclosing function never touches j again.
func deepNoUse(ch chan *Job) {
	j := &Job{}
	func() {
		func() {
			func() {
				func() {
					func() {
						ch <- j // FP(SAFE002): j is owned by deepNoUse and never used after the send; an immediately-invoked closure is judged as if its captures were held by an unknown caller
					}()
				}()
			}()
		}()
	}()
}

// mutual: two closures that call each other through variables.
func mutual(ch chan *Job) {
	j := &Job{}
	var even, odd func(int) bool
	even = func(n int) bool {
		if n == 0 {
			ch <- j // HIT(SAFE002): the hazard is real, but it is the use below that should be reported
			return true
		}
		return odd(n - 1)
	}
	odd = func(n int) bool {
		if n == 0 {
			return false
		}
		return even(n - 1)
	}
	even(4)
	j.ID = 1 // MISS(SAFE003): the transfer happened inside an immediately-invoked (same-goroutine) closure; the move is not propagated to the enclosing function
}

// Generic types with methods for method values and expressions.
type G[T any] struct{ V T }

func (g *G[T]) Set(v T) { g.V = v }

type Base struct{ N int }

func (b *Base) Inc() { b.N++ }

type Left struct{ *Base }
type Right struct{ *Base }

// Diamond embeds Base twice through Left and Right; Inc is ambiguous at depth 2, so it is selected
// explicitly.
type Diamond struct {
	Left
	Right
}

type Bumper interface{ Bump() }

func methodValues(ch chan *Job) {
	g := &G[*Job]{}
	set := g.Set
	j := &Job{}
	set(j)
	ch <- j  // HIT(SAFE004)
	j.ID = 1 // HIT(SAFE003)

	k := &Job{}
	var b Bumper = k
	bump := b.Bump
	go bump()
	k.ID = 2 // HIT(SAFE003)

	expr := (*Job).Bump
	m := &Job{}
	go expr(m)
	m.ID = 3 // HIT(SAFE003)

	gexpr := (*G[int]).Set
	gi := &G[int]{}
	go gexpr(gi, 1)
	gi.V = 2 // HIT(SAFE003)

	d := Diamond{Left{&Base{}}, Right{&Base{}}}
	inc := d.Left.Inc
	go inc()
	d.Left.N = 1 // HIT(SAFE003)
}

// FuncHolder has a func-typed field.
type FuncHolder struct{ F func(*Job) }

func funcField(ch chan *Job) {
	h := FuncHolder{F: func(j *Job) { ch <- j }} // HIT(SAFE002)
	j := &Job{}
	h.F(j)
	j.ID = 1
}

func variadicSend(ch chan *Job, js ...*Job) {
	for _, j := range js { // FP(SAFE003): ranging a slice of pointers after sending one element is reported as a use of the sent value
		ch <- j // HIT(SAFE002)
	}
}

func variadicCaller(ch chan *Job) {
	a, b := &Job{}, &Job{}
	variadicSend(ch, a, b)
	a.ID = 1
	s := []*Job{{}, {}}
	variadicSend(ch, s...)
	s[0].ID = 1
}

// returnsClosure returns a closure that sends what it captured.
func returnsClosure(ch chan *Job, j *Job) func() {
	return func() { ch <- j } // HIT(SAFE002)
}

func closureFactory(ch chan *Job) {
	j := &Job{}
	f := returnsClosure(ch, j)
	go f()
	j.ID = 1 // HIT(SAFE003)
}
