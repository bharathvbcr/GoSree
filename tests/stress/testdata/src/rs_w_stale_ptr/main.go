// WITNESS: panic
//
// Iterator invalidation in safe Go: a pointer into a slice survives an append that reallocates,
// and the write through it lands in the old backing array. The update is lost silently. Rust's
// borrow checker rejects `items.push(..)` while `p` borrows `items[0]`. gocap's borrow rules apply
// only to annotated borrows, so it says nothing. The panic is this program's own assertion: the
// language does not notice.
package main

import "fmt"

type Order struct{ Qty int }

func main() {
	orders := make([]Order, 1, 1)
	first := &orders[0]
	orders = append(orders, Order{Qty: 5}) // MISS(NORULE): reallocates while first borrows orders[0]
	first.Qty = 10                         // MISS(NORULE): writes the old array
	if orders[0].Qty != 10 {
		panic(fmt.Sprintf("lost update: wrote Qty=10 through a pointer to orders[0], orders[0].Qty is %d", orders[0].Qty))
	}
}
