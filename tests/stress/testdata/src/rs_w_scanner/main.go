// WITNESS: panic
//
// A dangling borrow in safe Go: bufio.Scanner.Bytes returns a view into the scanner's buffer that
// is valid only until the next Scan. Keeping it is a borrow that outlives what it borrows; the GC
// keeps the memory alive, but the scanner moves the next line over it. Rust's lending API ties
// the slice's lifetime to the scanner, so keeping it across the next call does not compile. gocap
// has no rule for it. The panic is this program's own check of what it kept.
//
// The lines are 3000 bytes so the second Scan shifts unread data to the front of its 4 KiB
// buffer; with short lines the kept views happen to survive.
package main

import (
	"bufio"
	"fmt"
	"strings"
)

func main() {
	input := strings.Repeat("a", 3000) + "\n" + strings.Repeat("b", 3000) + "\n"
	sc := bufio.NewScanner(strings.NewReader(input))
	var lines [][]byte
	for sc.Scan() {
		lines = append(lines, sc.Bytes()) // MISS(NORULE): the bytes are reused by the next Scan
	}
	if first := string(lines[0]); first != strings.Repeat("a", 3000) {
		panic(fmt.Sprintf("dangling borrow: the first line was 3000 'a's, the kept slice now starts %q", first[:8]))
	}
}
