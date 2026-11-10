// Package rs_torn_slice_caught is the control for rs_torn_slice: the same torn-slice race, with the
// table shared through a plain package variable instead of an atomic.Pointer. The variable is
// assigned only in its declaration; the checker still counts a write through it, and reports.
package rs_torn_slice_caught

type table struct{ rows []uint64 }

var current = &table{rows: []uint64{1}} // HIT(SAFE006)

func refresh() {
	short := []uint64{1}
	big := make([]uint64, 4096)
	for {
		current.rows = short
		current.rows = big
	}
}

func Read() uint64 {
	go refresh()
	rows := current.rows
	rows = rows[:cap(rows)]
	return rows[len(rows)-1]
}
