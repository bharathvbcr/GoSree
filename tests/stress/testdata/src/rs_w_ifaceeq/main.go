// WITNESS: panic
//
// Equality that the type system allows and the runtime refuses: == on two interfaces holding
// slices panics. Rust's == needs PartialEq, which Vec<T> has and which is checked statically; a
// type without it does not compile. gocap has no rule.
package main

import "fmt"

func changed(old, cur any) bool {
	return old != cur // MISS(NORULE): panics when both hold the same uncomparable type
}

func main() {
	fmt.Println(changed([]int{1}, []int{2}))
}
