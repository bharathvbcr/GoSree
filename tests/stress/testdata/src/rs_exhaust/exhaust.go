// Package rs_exhaust: Rust's match must cover every variant of an enum and every type a value can
// take, and a downcast returns Option. Go's switch has no exhaustiveness check, type switches do
// not either, and x.(T) panics. gocap has no rule for any of it; go vet has none either (the
// "exhaustive" linter is third-party).
package rs_exhaust

type State int

const (
	Idle State = iota
	Running
	Stopped
	Failed // added later; the switches below were not updated
)

// 1. non-exhaustive switch over an enum of constants.
func Label(s State) string {
	switch s { // MISS(NORULE): Failed is not handled; Rust's match on an enum would not compile
	case Idle:
		return "idle"
	case Running:
		return "running"
	case Stopped:
		return "stopped"
	}
	return ""
}

// 2. type switch missing a case of a closed set.
type Shape interface{ area() float64 }

type Square struct{ S float64 }
type Circle struct{ R float64 }
type Tri struct{ B, H float64 }

func (q Square) area() float64 { return q.S * q.S }
func (c Circle) area() float64 { return 3.14159 * c.R * c.R }
func (t Tri) area() float64    { return t.B * t.H / 2 }

func Name(s Shape) string {
	switch s.(type) { // MISS(NORULE): Tri is missing; a Rust enum match would not compile
	case Square:
		return "square"
	case Circle:
		return "circle"
	}
	return ""
}

// 3. unchecked type assertion.
func Side(s Shape) float64 {
	return s.(Square).S // MISS(NORULE): panics for a Circle; Rust's downcast returns Option
}

// Near-twin: the comma-ok form.
func SideOK(s Shape) (float64, bool) {
	q, ok := s.(Square)
	return q.S, ok
}
