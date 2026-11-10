// Package er_close stress-tests ERR005: dropping Close() flush errors on writable or written
// files/writers, including post-defer mutation where Write() occurs after `defer wc.Close()`.
package er_close

import (
	"compress/gzip"
	"io"
	"os"
)

// 1. os.Create + defer f.Close() drops flush error.
func createDeferClose(path string, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close() // HIT(ERR005)
	_, err = f.Write(data)
	return err
}

// 2. os.OpenFile with O_WRONLY + bare f.Close() drops flush error.
func openFileWriteClose(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	f.Close() // HIT(ERR005)
	return nil
}

// 3. gzip.NewWriter + defer gz.Close() drops trailer/flush error!
func gzipDeferClose(w io.Writer, data []byte) error {
	gz := gzip.NewWriter(w)
	defer gz.Close() // HIT(ERR005)
	_, err := gz.Write(data)
	return err
}

// 4. Generic io.WriteCloser where Write() happens AFTER `defer wc.Close()` registration:
// tests the Rust solver's post-defer writability check at function exit!
func writeAfterDefer(wc io.WriteCloser, data []byte) error {
	defer wc.Close() // HIT(ERR005)
	_, err := wc.Write(data)
	return err
}

// --- Clean controls / FP probes (must produce zero diagnostics) ---

// 5. Clean: read-only os.Open + defer f.Close() is idiomatic and safe.
func readOnlyOpen(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 16)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

// 6. Clean: os.OpenFile with O_RDONLY + defer f.Close().
func readOnlyOpenFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return nil
}

// 7. Clean: deferred closure captures Close() error into named return.
func createCheckedClose(path string, data []byte) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = f.Write(data)
	return err
}
