package errc7ddblank

import "io"


func marshal() ([]byte, error) { return nil, nil }

func payload() []byte { return nil }

// ft-c7dd: a call that shares its line with an all-blank assignment must not take that assignment
// as its own: marshal's error is checked on the next line and stays tracked, the blanked Write is
// silent in the loop, and the use of b before the check is still reported.
func sameLineNeighbour(w io.Writer) error {
	for i := 0; i < 2; i++ {
		b, err := marshal(); _, _ = w.Write(nil)
		if err != nil {
			return err
		}
		_ = b
	}
	return nil
}

func sameLineUseBeforeCheck(w io.Writer) error {
	for i := 0; i < 2; i++ {
		b, err := marshal(); _, _ = w.Write(b) // want "ERR002"
		if err != nil {
			return err
		}
	}
	return nil
}

func sameLineNested(w io.Writer) {
	for i := 0; i < 2; i++ {
		_, _ = w.Write(payload())
	}
}
