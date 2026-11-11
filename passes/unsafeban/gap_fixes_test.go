package unsafeban_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/unsafeban"
)

func TestRecursiveNamedPointerNoStackOverflow(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"lib/lib.go": `package lib

type PtrRec *PtrRec
type SliceRec []SliceRec
type ChanRec chan ChanRec

func Use(p PtrRec, s SliceRec, c ChanRec) PtrRec {
	return p
}
`,
		"user/user.go": `package user

import "lib"

func Call(p lib.PtrRec, s lib.SliceRec, c lib.ChanRec) lib.PtrRec {
	return lib.Use(p, s, c)
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, unsafeban.Analyzer, "user")
}

func TestCrossPackageFuncMapInterfaceUnsafePointerSmuggling(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"bridge/bridge.go": `package bridge

import "unsafe"

var Fn func(*int) unsafe.Pointer
var Table map[int]unsafe.Pointer

type RawProvider interface {
	Raw() unsafe.Pointer
}
`,
		"user/user.go": `package user

import "bridge"

func ViaFuncVar(p *int) *float64 {
	return (*float64)(bridge.Fn(p)) // want "SAFE001"
}

func ViaMapVar() *float64 {
	return (*float64)(bridge.Table[0]) // want "SAFE001"
}

func ViaInterface(r bridge.RawProvider) *float64 { // want "SAFE001"
	return (*float64)(r.Raw()) // want "SAFE001"
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, unsafeban.Analyzer, "user")
}
