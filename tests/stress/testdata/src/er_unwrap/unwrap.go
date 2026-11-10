// Package er_unwrap stress-tests ERR002: Go (T, error) virtual Result<T, E> companion unwrapping
// across diamond CFGs, loops, switch, slice/map/chan operations, and valid recovery patterns.
package er_unwrap

import (
	"errors"
	"io"
	"strconv"
)

type Item struct{ Value int }

var errNotFound = errors.New("not found")

func fetchItem(k string) (*Item, error) {
	if k == "" {
		return nil, errNotFound
	}
	return &Item{Value: len(k)}, nil
}

func fetchCount(k string) (int, error) {
	return strconv.Atoi(k)
}

func fetchMap(ok bool) (map[string]int, error) {
	if !ok {
		return nil, errors.New("bad map")
	}
	return map[string]int{"a": 1}, nil
}

// 1. Blanked error with companion dereferenced.
func blankedPtrDeref(k string) int {
	it, _ := fetchItem(k) // HIT(ERR002)
	return it.Value
}

// 2. Blanked error with companion used in make() capacity/length.
func blankedMakeLen(s string) []byte {
	n, _ := fetchCount(s) // HIT(ERR002)
	return make([]byte, n)
}

// 3. Unchecked error logged without early exit, then companion dereferenced.
func logAndFallthrough(k string) int {
	it, err := fetchItem(k)
	if err != nil {
		println("fetch failed:", err.Error())
	}
	return it.Value // HIT(ERR002)
}

// 4. Diamond CFG merge: coupled on one branch, clean on the other -> Phi remains coupled.
func diamondMerge(cond bool, k string) int {
	var it *Item
	var err error
	if cond {
		it, err = fetchItem(k)
	} else {
		it = &Item{Value: 7}
	}
	_ = err
	return it.Value // HIT(ERR002)
}

// 5. Inverted guard: companion used inside the err != nil branch!
func invertedIf(k string) int {
	it, err := fetchItem(k)
	if err != nil {
		return it.Value // HIT(ERR002)
	}
	return it.Value
}

// 6. Map range over unchecked companion map.
func uncheckedRange(ok bool) int {
	m, err := fetchMap(ok)
	_ = err
	sum := 0
	for _, v := range m { // HIT(ERR002)
		sum += v
	}
	return sum
}

// 7. Channel send of unchecked companion value.
func uncheckedChanSend(ch chan *Item, k string) {
	it, err := fetchItem(k)
	_ = err
	ch <- it // HIT(ERR002)
}

// --- Clean controls & idiomatic recovery (must produce zero diagnostics) ---

// 8. Clean: early return on err != nil proves err == nil below.
func guardedEarlyReturn(k string) (int, error) {
	it, err := fetchItem(k)
	if err != nil {
		return 0, err
	}
	return it.Value, nil
}

// 9. Clean: fallback assignment on err != nil kills coupling on all paths.
func fallbackOnError(k string) int {
	it, err := fetchItem(k)
	if err != nil {
		it = &Item{Value: 0}
	}
	return it.Value
}

// 10. Clean: errors.Is selective fallback, otherwise return error.
func errorsIsFallback(k string) (int, error) {
	it, err := fetchItem(k)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return it.Value, nil
}

// 11. Clean: io.Reader.Read idiom processes buf[:n] under n > 0 before checking err.
func readerIdiom(r io.Reader) (int, error) {
	buf := make([]byte, 64)
	total := 0
	for {
		n, err := r.Read(buf)
		if n > 0 {
			total += len(buf[:n])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return total, nil
			}
			return total, err
		}
	}
}

// 12. Clean: non-error comma-ok forms are not error tuples.
func commaOkClean(m map[string]int, a any) int {
	v, _ := m["k"]
	s, _ := a.(string)
	return v + len(s)
}
