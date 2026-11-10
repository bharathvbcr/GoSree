//go:build gocap_off

package dx_buildtag

import "unsafe"

func punTag(p *int) *float64 { return (*float64)(unsafe.Pointer(p)) }
