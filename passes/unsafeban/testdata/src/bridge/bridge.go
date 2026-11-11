package bridge

import "unsafe"

//gosree:unsafe FFI bridge exports raw pointer spellings
type RawAlias = unsafe.Pointer

//gosree:unsafe FFI bridge defined unsafe pointer type
type RawDef unsafe.Pointer

//gosree:unsafe FFI bridge helper returning unsafe.Pointer
func Leak(p *int) unsafe.Pointer { return unsafe.Pointer(p) }
