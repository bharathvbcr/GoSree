// Package mv_spawn probes moves the caller cannot see: a callee in another package that runs part
// of its work on a goroutine it starts, and a one-shot callback that is not one-shot.
package mv_spawn

import (
	"bytes"
	"os/exec"
	"strings"
	"time"
)

type Job struct{ ID int }

// os/exec copies a non-file Stdout on a goroutine Start launches. The go statement that moves the
// buffer is inside os/exec, where a SAFE002 would be reported against the standard library and is
// never shown; the caller sees an ordinary call (summary: leaks/writes, no transfer), so reading
// the buffer before Wait is unchecked (WITNESS mv_w_exec).
func execStdout() int {
	var buf bytes.Buffer
	cmd := exec.Command("echo", "hi")
	cmd.Stdout = &buf
	_ = cmd.Start()
	n := buf.Len() // MISS(SAFE003): Start moved &buf to the goroutine that copies the child's stdout into it
	_ = cmd.Wait()
	return n
}

// the same for Stdin: exec reads it on its own goroutine.
func execStdin() {
	in := strings.NewReader("hi")
	cmd := exec.Command("cat")
	cmd.Stdin = in
	_ = cmd.Start()
	_, _ = in.ReadByte() // MISS(SAFE003): Start moved in to the goroutine that copies it to the child's stdin
	_ = cmd.Wait()
}

// control: a spawner in the table moves its callback, and a use after it is reported.
func afterFunc() {
	j := &Job{}
	time.AfterFunc(time.Hour, func() { j.ID++ })
	j.ID = 1 // HIT(SAFE003)
}

// time.AfterFunc is modelled as a one-shot spawn whose closure owns its captures
// (cfg.go closureIsGoroutineBody), but Reset re-arms the timer and runs the same closure again, on
// a new goroutine, possibly while the first run is still going (WITNESS mv_w_reset).
func afterFuncReset() {
	j := &Job{}
	t := time.AfterFunc(time.Hour, func() { j.ID++ })
	t.Reset(0) // MISS(SAFE003): Reset starts the moved closure again; two runs write j with no ordering between them
}
