package errbasic

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

var errSentinel = errors.New("sentinel")

type item struct {
	val int
}

func doStep() error {
	return errSentinel
}

func fetchItem(ok bool) (*item, error) {
	if !ok {
		return nil, errSentinel
	}
	return &item{val: 7}, nil
}

func parseLen(ok bool) (int, error) {
	if !ok {
		return -1, errSentinel
	}
	return 8, nil
}

// --- ERR001: unhandled error return ---

func err001Bad() {
	doStep()       // want "ERR001"
	defer doStep() // want "ERR001"
	go doStep()    // want "ERR001"
}

func err001Clean() {
	_ = doStep()
	fmt.Println("hello")
	var buf bytes.Buffer
	buf.WriteString("infallible")
	_, _ = fetchItem(true)
}

// --- ERR002: companion value used without proving err == nil ---

func err002UncheckedField(ok bool) int {
	it, err := fetchItem(ok)
	_ = err
	return it.val // want "ERR002"
}

func err002PartialBranch(ok bool) int {
	it, err := fetchItem(ok)
	if err != nil {
		fmt.Println(err)
	}
	return it.val // want "ERR002"
}

func err002Blanked(ok bool) int {
	it, _ := fetchItem(ok) // want "ERR002"
	return it.val
}

func err002MakeSliceUnchecked(ok bool) []byte {
	n, err := parseLen(ok)
	_ = err
	return make([]byte, n) // want "ERR002"
}

func err002ReturnWithNil(ok bool) (*item, error) {
	it, err := fetchItem(ok)
	_ = err
	return it, nil // want "ERR002"
}

func err002CleanChecked(ok bool) (int, error) {
	it, err := fetchItem(ok)
	if err != nil {
		return 0, err
	}
	return it.val, nil
}

func err002CleanFallback(ok bool) int {
	it, err := fetchItem(ok)
	if err != nil {
		it = &item{val: 99}
	}
	return it.val
}

func err002CleanIOReader(r io.Reader) ([]byte, error) {
	buf := make([]byte, 32)
	n, err := r.Read(buf)
	if n > 0 {
		out := append([]byte(nil), buf[:n]...)
		if err != nil && !errors.Is(err, io.EOF) {
			return out, err
		}
		return out, nil
	}
	return nil, err
}

// --- ERR003: shadowed, overwritten, or nilerr ---

func err003Shadowed(cond bool) error {
	err := doStep()
	if err != nil {
		return err
	}
	if cond {
		err := doStep() // want "ERR003"
		if err != nil {
			fmt.Println("nested failed")
		}
	}
	return err
}

func err003OverwrittenSequential() error {
	err := doStep()
	err = doStep() // want "ERR003"
	return err
}

func err003OverwrittenBranch(cond bool) error {
	var err error
	if cond {
		err = doStep()
	}
	err = doStep() // want "ERR003"
	return err
}

func err003OverwrittenLoop(n int) error {
	var err error
	for i := 0; i < n; i++ {
		err = doStep() // want "ERR003"
	}
	return err
}

func err003NamedReturnOverwrite() (err error) {
	err = doStep()
	return nil // want "ERR003"
}

func err003Nilerr() error {
	err := doStep()
	if err != nil {
		return nil // want "ERR003"
	}
	return nil
}

func err003CleanRecovery() error {
	err := doStep()
	if err != nil {
		err2 := doStep()
		if err2 == nil {
			return nil
		}
		return fmt.Errorf("retry failed: %w", err2)
	}
	return nil
}

func err003CleanSentinelIgnore() error {
	err := doStep()
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func err003CleanAccumulator(n int) error {
	var lastErr error
	for i := 0; i < n; i++ {
		if err := doStep(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// --- ERR004: typed-nil concrete *T -> error interface ---

type customErr struct {
	msg string
}

func (e *customErr) Error() string { return e.msg }

func err004ZeroConcrete() error {
	var ce *customErr
	return ce // want "ERR004"
}

func err004PhiMayNil(fail bool) error {
	var ce *customErr
	if fail {
		ce = &customErr{msg: "boom"}
	}
	return ce // want "ERR004"
}

func mayNilConcreteHelper(fail bool) *customErr {
	if fail {
		return &customErr{msg: "boom"}
	}
	return nil
}

func nonNilConcreteHelper() *customErr {
	return &customErr{msg: "always"}
}

func err004IntraPkgMayNil(fail bool) error {
	return mayNilConcreteHelper(fail) // want "ERR004"
}

func err004CleanGuarded(fail bool) error {
	ce := mayNilConcreteHelper(fail)
	if ce != nil {
		return ce
	}
	return nil
}

func err004CleanNonNil() error {
	return nonNilConcreteHelper()
}

// --- ERR005: writable Close() error dropped ---

func err005DeferCreate(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close() // want "ERR005"
	_, err = f.Write([]byte("hello"))
	return err
}

func err005WriteCloserWritten(wc io.WriteCloser) error {
	defer wc.Close() // want "ERR005"
	_, err := wc.Write([]byte("payload"))
	return err
}

func err005CleanReadOnlyOpen(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func err005CleanHandledClose(path string) (retErr error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()
	_, err = f.Write([]byte("hello"))
	return err
}

// --- Annotation suppression ---

func suppressedUnsafe() {
	//gocap:unsafe reason="best-effort fire-and-forget in test"
	doStep()
	//gosree:unsafe reason="explicitly tolerated"
	doStep()
}
