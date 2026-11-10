package dx_buildtag

import "unsafe"

func punWindows(p *int) *float64 { return (*float64)(unsafe.Pointer(p)) }
