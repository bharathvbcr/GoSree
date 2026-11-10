// Package cap provides zero-cost generic marker type aliases and helper intrinsics
// for gosree's reference capabilities and second-class borrow checker.
//
// All type aliases in this package expand directly to standard Go pointer types (*T)
// at compile time with zero runtime memory, indirection, or instruction overhead.
// All helper expressions inline directly to identity operations.
package cap

// Mut represents a second-class mutable borrow (&mut T / \mub).
// In standard Go compilation, it is a zero-cost generic alias for *T.
type Mut[T any] = *T

// Ref represents a second-class read-only borrow (&T / \rob).
// In standard Go compilation, it is a zero-cost generic alias for *T.
type Ref[T any] = *T

// Mub is an alias for Mut, matching the \mub capability notation.
type Mub[T any] = *T

// Rob is an alias for Ref, matching the \rob capability notation.
type Rob[T any] = *T

// Iso represents an isolated, uniquely-owned pointer reference (\iso *T).
// In standard Go compilation, it is a zero-cost generic alias for *T.
type Iso[T any] = *T

// Imm represents a deeply immutable pointer reference (\imm *T).
// In standard Go compilation, it is a zero-cost generic alias for *T.
type Imm[T any] = *T

// Move explicitly marks an ownership transfer of an isolated value.
// It is a zero-cost compiler intrinsic that inlines to an identity transfer.
func Move[T any](x T) T {
	return x
}

// BorrowMut explicitly creates a second-class mutable borrow from a pointer.
func BorrowMut[T any](x *T) Mut[T] {
	return x
}

// Borrow explicitly creates a second-class read-only borrow from a pointer.
func Borrow[T any](x *T) Ref[T] {
	return x
}

// BorrowRef is an alias for Borrow.
func BorrowRef[T any](x *T) Ref[T] {
	return x
}

// BorrowMub is an alias for BorrowMut.
func BorrowMub[T any](x *T) Mub[T] {
	return x
}

// BorrowRob is an alias for Borrow.
func BorrowRob[T any](x *T) Rob[T] {
	return x
}
