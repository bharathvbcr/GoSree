// Package adv_break is a cross-cutting "new-age Go compiler" gauntlet combining borrow checking,
// NLL invalidation, ownership moves, immutability, locksets, and Result-style error discipline.
package adv_break

import (
	"errors"
	"sync"
)

type Packet struct{ Data []byte }

//gosree:imm
type FrozenCfg struct {
	limit int
	ports [2]int
}

type RawCfg FrozenCfg

type Vault struct {
	mu      sync.Mutex
	balance int //safe:guarded_by mu
}

func idPtr[T any](p *T) *T { return p }

func idVal[T any](v T) T { return v }

func openPacket(ok bool) (*Packet, error) {
	if !ok {
		return nil, errors.New("bad packet")
	}
	return &Packet{Data: []byte{1, 2, 3}}, nil
}

// 1. Generic identity helpers (*T and unconstrained T) laundering a second-class borrow out of the frame.
func genericBorrowEscape() *int {
	x := 10
	b := &x //gosree:mut
	return idPtr(b) // HIT(BORROW001)
}

func genericUnconstrainedBorrowEscape() *int {
	x := 20
	b := &x //gosree:rob
	return idVal(b) // HIT(BORROW001)
}

// 2. Sub-slice reborrow + backing-slice mutation via append into spare capacity.
func subsliceAppendInvalidate() int {
	s := make([]int, 2, 4)
	sub := s[1:]
	b := &sub[0]         //gosree:rob
	_ = append(s[:1], 9) // HIT(BORROW003)
	return *b
}

// 3. Fallible constructor + channel move + post-move use + unchecked error unwrap all in one flow!
func uncheckedMoveAndReuse(ch chan *Packet, ok bool) byte {
	pkt, err := openPacket(ok)
	_ = err
	ch <- pkt          // HIT(ERR002)
	return pkt.Data[0] // HIT(SAFE003) HIT(ERR002)
}

// 4. Immutable struct pointer converted to RawCfg and sliced to mutate fixed array field.
func launderImmArray(cfg *FrozenCfg) {
	raw := (*RawCfg)(cfg)
	view := raw.ports[:]
	view[0] = 9999 // HIT(SAFE005)
}

// 5. Method-value unlock releases mutex before guarded field write, combined with dropped error.
func unlockViaMethodValueAndDropErr(v *Vault, step func() error) {
	v.mu.Lock()
	rel := v.mu.Unlock
	rel()
	v.balance += 100 // HIT(SAFE009)
	step()           // HIT(ERR001)
}
