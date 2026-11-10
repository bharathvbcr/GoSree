// Package rs_errors: Rust's Result is #[must_use]; ignoring it is a warning (an error under
// deny(warnings)), and the value is reachable only through Ok, so it cannot be used on the error
// path. Go returns (value, error) pairs, and nothing ties the value to the check. gocap has no
// rule for error handling. go vet's unusedresult covers a fixed list of pure functions
// (fmt.Sprintf, errors.New, ...), not ignored errors; errcheck is third-party.
package rs_errors

import (
	"os"
	"strconv"
)

// 1. error return dropped as a statement.
func Save(path string, data []byte) {
	os.WriteFile(path, data, 0o600) // MISS(NORULE): the error is discarded; Rust warns on an unused Result
}

// 2. error assigned to the blank identifier.
func Remove(path string) {
	_ = os.Remove(path) // MISS(NORULE): explicit discard; Rust needs `let _ =` too, so this one is a draw
}

// 3. value kept, error blanked.
func Port(s string) int {
	n, _ := strconv.Atoi(s) // MISS(NORULE): n is 0 on bad input and nothing says so
	return n
}

// 4. value used on the error path.
func Size(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		println("stat failed")
	}
	return fi.Size() // MISS(NORULE): fi is nil when err != nil; Rust's Result gives no value on Err
}

// 5. shadowed err: the inner := declares a new err, the outer one is returned as nil.
func Load(path string) (n int, err error) {
	if path != "" {
		data, err := os.ReadFile(path) // MISS(NORULE): this err shadows the result; its failure is lost
		if err == nil {
			n = len(data)
		}
	}
	return n, err
}

// 6. dropped Close error on a written file: the data may never reach the disk.
func Write(path string, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close() // MISS(NORULE): a failed flush on Close is lost; Rust's File drop also drops it, so partly a draw
	_, err = f.Write(data)
	return err
}
