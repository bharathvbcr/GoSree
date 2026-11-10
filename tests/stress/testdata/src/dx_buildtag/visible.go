// Package dx_buildtag shows that gocap checks one build configuration: the one it runs under. Files
// a build constraint excludes are never loaded, so they can hold any amount of unsafe and the run
// reports nothing. The excluded files sit next to this one:
//
//	hidden_ignore.go   //go:build ignore        unsafe.Pointer type pun
//	hidden_windows.go  GOOS suffix              unsafe.Pointer type pun, compiled into every Windows build
//	hidden_tag.go      //go:build gocap_off     a custom tag a release build can set
//
// The harness reads markers only from loaded files, so the misses are recorded here, one per file.
// A sound tool would analyse every GOOS/GOARCH and tag combination the module builds for, or refuse
// to pass a package that has files outside the configuration it checked.
package dx_buildtag

import "unsafe"

func visible(p *int) uintptr {
	return uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
}

var hiddenIgnore = 0 // MISS(SAFE001): hidden_ignore.go converts *int to *float64 through unsafe.Pointer; //go:build ignore keeps it out of every analysis

var hiddenWindows = 0 // MISS(SAFE001): hidden_windows.go does the same; it ships in every GOOS=windows build and gocap on darwin/linux never loads it

var hiddenTag = 0 // MISS(SAFE001): hidden_tag.go does the same behind a custom build tag; go build -tags gocap_off ships unchecked code
