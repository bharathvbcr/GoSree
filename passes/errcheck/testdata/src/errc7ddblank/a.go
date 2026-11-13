package errc7ddblank

import "io"

// ---- ERR003 on an explicit blank discard in a loop ----

func s6BlankInLoop(w io.WriteCloser) {
	for i := 0; i < 2; i++ {
		_, _ = w.Write(nil)
	}
}

func s6BlankInLoopClosure(w io.WriteCloser) func() {
	return func() {
		for i := 0; i < 2; i++ {
			_, _ = w.Write(nil)
		}
	}
}

func s6ReassignInLoop(w io.WriteCloser) {
	var err error
	for i := 0; i < 2; i++ {
		_, err = w.Write(nil) // want "ERR003"
	}
	_ = err
}

func s6ReassignInLoopRead(w io.WriteCloser) error {
	var err error
	for i := 0; i < 2; i++ {
		_, err = w.Write(nil)
		if err != nil {
			return err
		}
	}
	return nil
}
