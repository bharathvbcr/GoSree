//go:build ignore

package dx_buildtag

import "unsafe"

func punIgnore(p *int) *float64 { return (*float64)(unsafe.Pointer(p)) }
