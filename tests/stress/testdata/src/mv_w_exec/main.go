// WITNESS: race
//
// os/exec copies a non-file Stdout on a goroutine that Start launches. The go statement that moves
// the buffer is inside the standard library, so its SAFE002 is never reported to the user, and the
// caller sees an ordinary call with no transfer: reading the buffer between Start and Wait is
// unchecked. The summary is computed (graph.call, summary from facts), but a summary records
// leaks and writes, never "moves this argument to another goroutine".
package main

import (
	"bytes"
	"os/exec"
)

func main() {
	var out bytes.Buffer
	cmd := exec.Command("echo", "hello")
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	n := 0
	for i := 0; i < 1000; i++ {
		n += out.Len() // MISS(SAFE003): Start moved &out to the goroutine that copies the child's output into it
	}
	_ = cmd.Wait()
	println(n > 0)
}
