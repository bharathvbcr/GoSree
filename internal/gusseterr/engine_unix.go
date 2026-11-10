//go:build unix && cgo

package gusseterr

/*
#cgo noescape gosree_gusset_init
#cgo nocallback gosree_gusset_init
#cgo LDFLAGS: -L${SRCDIR}/../../rust/target/release
int gosree_gusset_init(void);
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bharathvbcr/gusset"
)

const (
	opcodeSolve         uint32 = 0
	opcodeSelfTestPanic uint32 = 0x7fff_0001
	maxInlineFrame             = 4096
	poolSize                   = 4
)

var (
	registerOnce sync.Once
	registerErr  error

	engineMu  sync.Mutex
	current   atomic.Pointer[gusset.Handle]
	closed    atomic.Bool
	opens     atomic.Int64
	crossings atomic.Int64

	// bufferMem orders reads/writes of Rust-owned Buffer slices against Handle.Close.
	bufferMem sync.RWMutex
)

func register() error {
	if n := C.gosree_gusset_init(); n != 0 {
		return fmt.Errorf("gusseterr: %d engine opcodes failed to register", int(n))
	}
	return nil
}

// NativeActive reports whether the in-process Rust Gusset engine is linked.
func NativeActive() bool { return true }

// Crossings reports the total number of FFI crossings performed on the shared handle.
func Crossings() int64 { return crossings.Load() }

// Opens reports the total number of shared handles opened in this process.
func Opens() int64 { return opens.Load() }

func sharedHandle() (*gusset.Handle, error) {
	if h := current.Load(); h != nil {
		return h, nil
	}
	engineMu.Lock()
	defer engineMu.Unlock()
	if closed.Load() {
		return nil, ErrClosed
	}
	if h := current.Load(); h != nil {
		return h, nil
	}
	registerOnce.Do(func() { registerErr = register() })
	if registerErr != nil {
		return nil, registerErr
	}
	h, err := gusset.Open(gusset.WithPoolSize(poolSize))
	if err != nil {
		return nil, err
	}
	opens.Add(1)
	current.Store(h)
	return h, nil
}

func retire(h *gusset.Handle) {
	if current.CompareAndSwap(h, nil) {
		go func() { _ = closeHandle(h) }()
	}
}

func closeHandle(h *gusset.Handle) error {
	bufferMem.Lock()
	defer bufferMem.Unlock()
	return h.Close()
}

func poisoned(err error) bool {
	return errors.Is(err, gusset.ErrPanic) || errors.Is(err, gusset.ErrPoisoned)
}

func withBuffer(b *gusset.Buffer, fn func([]byte) bool) bool {
	bufferMem.RLock()
	defer bufferMem.RUnlock()
	return fn(b.Bytes())
}

func roundTrip(ctx context.Context, h *gusset.Handle, frame []byte) ([]byte, error) {
	if len(frame) <= maxInlineFrame {
		return h.Call(ctx, frame)
	}
	in, err := h.NewBuffer(len(frame))
	if err != nil {
		return nil, err
	}
	defer in.Free()
	if !withBuffer(in, func(dst []byte) bool {
		if len(dst) != len(frame) {
			return false
		}
		copy(dst, frame)
		return true
	}) {
		return nil, fmt.Errorf("gusseterr: input buffer unavailable: %w", gusset.ErrClosed)
	}
	out, err := h.CallBuffer(ctx, in)
	if err != nil {
		return nil, err
	}
	defer out.Free()
	var result []byte
	if !withBuffer(out, func(src []byte) bool {
		if src == nil {
			return false
		}
		result = append([]byte(nil), src...)
		return true
	}) {
		return nil, fmt.Errorf("gusseterr: output buffer unavailable: %w", gusset.ErrClosed)
	}
	return result, nil
}

// SolveFrame sends a GSE1 request frame to the Rust Gusset engine and returns
// the GSR1 response frame, automatically retiring a poisoned handle and
// retrying once if a concurrent handle close raced the call.
func SolveFrame(ctx context.Context, frame []byte) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("gusseterr: nil context")
	}
	// Pin opcode 0 so no caller context carrying opcodeSelfTestPanic can reach the shared handle.
	sctx := gusset.ContextWithOpcode(ctx, opcodeSolve)
	for attempt := range 2 {
		h, err := sharedHandle()
		if err != nil {
			return nil, err
		}
		crossings.Add(1)
		out, err := roundTrip(sctx, h, frame)
		if err == nil {
			return out, nil
		}
		if poisoned(err) {
			retire(h)
			return nil, err
		}
		if attempt == 0 && errors.Is(err, gusset.ErrClosed) && !closed.Load() {
			continue
		}
		return nil, err
	}
	return nil, ErrClosed
}

// Solve encodes funcs into one batched GSE1 frame, solves the entire package in
// Rust over Gusset in a single FFI crossing, and decodes the GSR1 response.
func Solve(ctx context.Context, funcs []FuncIR) (PackageResult, error) {
	req, err := EncodePackage(funcs)
	if err != nil {
		return PackageResult{}, err
	}
	resp, err := SolveFrame(ctx, req)
	if err != nil {
		return PackageResult{}, err
	}
	return DecodeResult(resp)
}

// Check verifies the linked libgusset.a umbrella answers both empty and
// non-trivial CFG frames accurately without triggering any panic.
func Check(ctx context.Context) error {
	if ctx == nil {
		return errors.New("gusseterr: nil context")
	}
	sample := []FuncIR{
		{
			Flags:     FuncReturnsError,
			SlotFlags: []uint8{SlotIsError},
			Blocks: []BlockIR{
				{
					Ops:  []OpIR{{Kind: OpUnhandledErr, SiteID: 42}},
					Term: Terminator{Kind: TermReturn, SiteID: 43, RetSlot: NoneU32},
				},
			},
		},
	}
	got, err := Solve(ctx, sample)
	if err != nil {
		return fmt.Errorf("gusseterr check solve: %w", err)
	}
	want, err := SolveOracle(ctx, sample)
	if err != nil {
		return fmt.Errorf("gusseterr check oracle: %w", err)
	}
	if len(got.Findings) != len(want.Findings) || got.Findings[0] != want.Findings[0] {
		return fmt.Errorf("gusseterr check mismatch: got %+v, want %+v", got, want)
	}
	return nil
}

// SelfTest runs Check, induces a real Rust panic inside libgusset.a on a
// throwaway handle (proving ErrPanic -> ErrPoisoned containment), and verifies
// the shared handle still solves cleanly afterward.
func SelfTest(ctx context.Context) error {
	if err := Check(ctx); err != nil {
		return err
	}
	if _, err := sharedHandle(); err != nil {
		return err
	}
	h, err := gusset.Open(gusset.WithPoolSize(1), gusset.WithOpcode(opcodeSelfTestPanic))
	if err != nil {
		return fmt.Errorf("gusseterr self-test open: %w", err)
	}
	defer h.Close()
	if _, err := h.Call(ctx, nil); !errors.Is(err, gusset.ErrPanic) {
		return fmt.Errorf("gusseterr self-test panic returned %v, want ErrPanic", err)
	}
	if _, err := h.Call(ctx, nil); !errors.Is(err, gusset.ErrPoisoned) {
		return fmt.Errorf("gusseterr post-panic call returned %v, want ErrPoisoned", err)
	}
	return Check(ctx)
}

// DrainLogs drains Gusset's internal Rust log ring into w.
func DrainLogs(w io.Writer) (int, error) {
	buf := make([]byte, 64<<10)
	total := 0
	for {
		n := gusset.DrainLogs(buf)
		if n == 0 {
			return total, nil
		}
		wrote, err := w.Write(buf[:n])
		total += wrote
		if err != nil {
			return total, err
		}
	}
}

// RustAllocatedBytes returns the current live heap bytes tracked by the
// Counting<System> global allocator inside libgusset.a.
func RustAllocatedBytes() uint64 {
	return gusset.Stats().LiveBytes
}

// Close closes the shared handle and rejects subsequent calls with ErrClosed.
func Close() error {
	engineMu.Lock()
	defer engineMu.Unlock()
	closed.Store(true)
	h := current.Swap(nil)
	if h == nil {
		return nil
	}
	return closeHandle(h)
}

// Shutdown drains Gusset process-wide within drain and closes the shared handle.
func Shutdown(drain time.Duration) error {
	engineMu.Lock()
	closed.Store(true)
	engineMu.Unlock()
	if err := gusset.Shutdown(drain); err != nil {
		current.Store(nil)
		return err
	}
	return Close()
}
