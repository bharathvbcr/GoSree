// Package unsafeban reports uses of escape hatches that defeat Go's memory safety: package unsafe,
// cgo, //go:linkname, and reflect calls that write through or expose raw memory (SAFE001).
package unsafeban

import (
	"go/ast"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"

	"github.com/bharathvbcr/gocap/internal/annot"
)

// Category is the diagnostic category for this pass.
const Category = "SAFE001"

// Analyzer reports unsafe, cgo, linkname and raw reflect writes outside //safe:unsafe scopes.
var Analyzer = &analysis.Analyzer{
	Name:     "safeunsafe",
	Doc:      "forbid package unsafe, cgo, //go:linkname and reflect writes outside //safe:unsafe (" + Category + ")",
	Run:      run,
	Requires: []*analysis.Analyzer{annot.Analyzer},
}

// unsafeMembers are the unsafe package members that create or reinterpret pointers. Sizeof,
// Alignof and Offsetof are compile-time constants and stay allowed.
var unsafeMembers = map[string]bool{
	"Pointer": true, "Add": true, "Slice": true, "SliceData": true, "String": true, "StringData": true,
}

func run(pass *analysis.Pass) (any, error) {
	ix := pass.ResultOf[annot.Analyzer].(*annot.Index)
	report := func(n ast.Node, msg string) {
		if ix.Suppressed(n.Pos()) {
			return
		}
		pass.Report(analysis.Diagnostic{Pos: n.Pos(), End: n.End(), Category: Category, Message: Category + ": " + msg})
	}
	for _, file := range pass.Files {
		for _, imp := range file.Imports {
			if path, err := strconv.Unquote(imp.Path.Value); err == nil && path == "C" {
				report(imp, `cgo (import "C") bypasses Go's memory safety; wrap it in //safe:unsafe <reason>`)
			}
		}
		for _, cg := range file.Comments {
			for _, c := range cg.List {
				if strings.HasPrefix(c.Text, "//go:linkname") {
					report(c, "//go:linkname reaches unexported symbols and bypasses type safety")
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if obj, ok := pass.TypesInfo.Uses[n.Sel]; ok && obj.Pkg() != nil && obj.Pkg().Path() == "unsafe" && unsafeMembers[obj.Name()] {
					report(n, "unsafe."+obj.Name()+" bypasses Go's memory safety; wrap it in //safe:unsafe <reason>")
				}
			case *ast.CallExpr:
				if name := reflectWrite(pass, n); name != "" {
					report(n, name+" writes or exposes memory through reflection")
				}
			}
			return true
		})
	}
	return nil, nil
}

// reflectWrite names a call to reflect.NewAt or to a reflect.Value method that writes or exposes
// raw memory, or returns "".
func reflectWrite(pass *analysis.Pass, call *ast.CallExpr) string {
	se, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	fn, ok := pass.TypesInfo.Uses[se.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "reflect" {
		return ""
	}
	sig := fn.Signature()
	if sig.Recv() == nil {
		if fn.Name() == "NewAt" {
			return "reflect.NewAt"
		}
		return ""
	}
	recv := types.Unalias(sig.Recv().Type())
	if p, ok := recv.(*types.Pointer); ok {
		recv = p.Elem()
	}
	if n, ok := recv.(*types.Named); !ok || n.Obj().Name() != "Value" {
		return ""
	}
	switch name := fn.Name(); {
	case strings.HasPrefix(name, "Set"), name == "UnsafeAddr", name == "UnsafePointer":
		return "reflect.Value." + name
	}
	return ""
}
