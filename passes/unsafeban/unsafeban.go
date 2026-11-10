// Package unsafeban reports uses of escape hatches that defeat Go's memory safety: package unsafe
// (however it is imported), cgo, //go:linkname, functions without a Go body (assembly), and reflect
// calls that write through or expose raw memory (SAFE001).
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
		if ix.Mute(n.Pos()) {
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
				} else if strings.HasPrefix(c.Text, "//go:noescape") {
					report(c, "//go:noescape disables escape analysis and bypasses Go's memory safety")
				}
			}
		}
		calledSel := map[*ast.SelectorExpr]bool{}
		handledIdent := map[*ast.Ident]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				switch fun := ast.Unparen(x.Fun).(type) {
				case *ast.SelectorExpr:
					handledIdent[fun.Sel] = true
					if reflectWrite(pass, x) != "" {
						calledSel[fun] = true
					}
				case *ast.Ident:
					if reflectSymbol(pass.TypesInfo.Uses[fun]) != "" {
						handledIdent[fun] = true
					}
				}
			case *ast.SelectorExpr:
				handledIdent[x.Sel] = true
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Ident:
				// Matching the identifier, not a selector, also catches a dot import of unsafe or reflect.
				if obj, ok := pass.TypesInfo.Uses[n]; ok && obj.Pkg() != nil && obj.Pkg().Path() == "unsafe" && unsafeMembers[obj.Name()] {
					report(n, "unsafe."+obj.Name()+" bypasses Go's memory safety; wrap it in //safe:unsafe <reason>")
				} else if !handledIdent[n] {
					if name := reflectSymbol(pass.TypesInfo.Uses[n]); name != "" {
						report(n, name+" writes or exposes memory through reflection")
					}
				}
			case *ast.FuncDecl:
				if n.Body == nil {
					report(n, "function "+n.Name.Name+" has no Go body: it is implemented in assembly or linked in, which bypasses Go's memory safety")
				}
			case *ast.CallExpr:
				if name := reflectWrite(pass, n); name != "" {
					report(n, name+" writes or exposes memory through reflection")
				}
			case *ast.SelectorExpr:
				if !calledSel[n] {
					if name := reflectSymbol(pass.TypesInfo.Uses[n.Sel]); name != "" {
						report(n, name+" writes or exposes memory through reflection")
					} else if recvType := pass.TypesInfo.TypeOf(n.X); recvType != nil && types.IsInterface(recvType) {
						if isReflectValueInterfaceMethod(n.Sel.Name) && reflectValueImplements(pass.Pkg, recvType) {
							report(n, "reflect.Value."+n.Sel.Name+" writes or exposes memory through reflection")
						}
					}
				}
			}
			return true
		})
	}
	return nil, nil
}

// reflectWrite names a call to reflect.NewAt/Copy/Swapper or to a reflect.Value method that writes or exposes
// raw memory, or returns "".
func reflectWrite(pass *analysis.Pass, call *ast.CallExpr) string {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		if s := reflectSymbol(pass.TypesInfo.Uses[fun.Sel]); s != "" {
			return s
		}
		if recvType := pass.TypesInfo.TypeOf(fun.X); recvType != nil && types.IsInterface(recvType) {
			if isReflectValueInterfaceMethod(fun.Sel.Name) && reflectValueImplements(pass.Pkg, recvType) {
				return "reflect.Value." + fun.Sel.Name
			}
		}
	case *ast.Ident:
		return reflectSymbol(pass.TypesInfo.Uses[fun])
	}
	return ""
}

func reflectValueImplements(pkg *types.Package, recvType types.Type) bool {
	if pkg == nil || recvType == nil {
		return false
	}
	iface, ok := recvType.Underlying().(*types.Interface)
	if !ok {
		return false
	}
	for _, imp := range pkg.Imports() {
		if imp.Path() == "reflect" {
			if obj := imp.Scope().Lookup("Value"); obj != nil && obj.Type() != nil {
				return types.Implements(obj.Type(), iface)
			}
		}
	}
	return false
}

func isReflectValueInterfaceMethod(name string) bool {
	switch name {
	case "Set", "SetInt", "SetUint", "SetFloat", "SetString", "SetBool", "SetBytes", "SetComplex",
		"SetPointer", "SetIterKey", "SetIterValue", "SetLen", "SetCap", "SetMapIndex", "SetZero",
		"UnsafeAddr", "UnsafePointer", "Pointer", "Addr", "Clear", "Grow", "Send", "TrySend":
		return true
	}
	return false
}

func reflectSymbol(obj types.Object) string {
	if obj == nil || obj.Pkg() == nil || obj.Pkg().Path() != "reflect" {
		return ""
	}
	if tn, ok := obj.(*types.TypeName); ok {
		if tn.Name() == "SliceHeader" || tn.Name() == "StringHeader" {
			return "reflect." + tn.Name()
		}
		return ""
	}
	fn, ok := obj.(*types.Func)
	if !ok {
		return ""
	}
	sig := fn.Signature()
	if sig.Recv() == nil {
		if fn.Name() == "NewAt" || fn.Name() == "Copy" || fn.Name() == "Swapper" || fn.Name() == "SliceAt" {
			return "reflect." + fn.Name()
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
	case strings.HasPrefix(name, "Set"),
		name == "UnsafeAddr",
		name == "UnsafePointer",
		name == "Pointer",
		name == "Addr",
		name == "Clear",
		name == "Grow",
		name == "Send",
		name == "TrySend":
		return "reflect.Value." + name
	}
	return ""
}
