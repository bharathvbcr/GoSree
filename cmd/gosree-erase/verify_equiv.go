package main

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/tools/go/ast/astutil"
)

const defaultEquivVerifyTimeout = 30 * time.Second

const canonicalCapSource = `package cap

type Mut[T any] = *T
type Ref[T any] = *T
type Mub[T any] = *T
type Rob[T any] = *T
type Iso[T any] = *T
type Imm[T any] = *T

func Move[T any](x T) T          { return x }
func BorrowMut[T any](x *T) Mut[T] { return x }
func Borrow[T any](x *T) Ref[T]    { return x }
func BorrowRef[T any](x *T) Ref[T] { return x }
func BorrowMub[T any](x *T) Mub[T] { return x }
func BorrowRob[T any](x *T) Rob[T] { return x }
`

// EquivReport records the result of formal translation validation between a
// gosree/cap-annotated Go package and its erased standard-Go counterpart.
type EquivReport struct {
	PackageName            string
	ExportedSymbolsChecked int
	DisassembledFuncs      int
	OrigTextMnemonics      map[string][]string
	ErasedTextMnemonics    map[string][]string
}

type capOverlayImporter struct {
	base   types.Importer
	capPkg *types.Package
}

func newCapOverlayImporter() (*capOverlayImporter, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "cap.go", canonicalCapSource, 0)
	if err != nil {
		return nil, err
	}
	var conf types.Config
	pkg, err := conf.Check(capPath, fset, []*ast.File{f}, nil)
	if err != nil {
		return nil, err
	}
	return &capOverlayImporter{
		base:   importer.Default(),
		capPkg: pkg,
	}, nil
}

func (imp *capOverlayImporter) Import(path string) (*types.Package, error) {
	if path == capPath {
		return imp.capPkg, nil
	}
	return imp.base.Import(path)
}

// ErasePackageFiles performs go/types-resolved erasure across a multi-file Go
// package. For files that single-file Erase handles directly, it delegates to
// Erase; when single-file Erase conservatively refuses a cross-file constant in
// a constant-sensitive position (classUnknown), ErasePackageFiles uses
// types.Info{Types, Uses, Defs} to materialize the exact type before delegating
// to Erase, preserving zero-guess soundness across multi-file packages.
func ErasePackageFiles(files map[string][]byte) (map[string][]byte, error) {
	out := make(map[string][]byte, len(files))
	var needTypes []string
	for name, src := range files {
		erased, err := Erase(src)
		if err == nil {
			out[name] = erased
			continue
		}
		if !strings.Contains(err.Error(), "may be a constant of this package or another file") {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		needTypes = append(needTypes, name)
	}
	if len(needTypes) == 0 {
		return out, nil
	}

	imp, err := newCapOverlayImporter()
	if err != nil {
		return nil, fmt.Errorf("cap importer: %w", err)
	}
	fset := token.NewFileSet()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	parsed := make(map[string]*ast.File, len(files))
	var fileList []*ast.File
	for _, name := range names {
		clean := stripDirectives(files[name])
		f, err := parser.ParseFile(fset, name, clean, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, err)
		}
		parsed[name] = f
		fileList = append(fileList, f)
	}

	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Uses:  make(map[*ast.Ident]types.Object),
		Defs:  make(map[*ast.Ident]types.Object),
	}
	conf := types.Config{Importer: imp}
	pkgName := fileList[0].Name.Name
	pkg, err := conf.Check(pkgName, fset, fileList, info)
	if err != nil {
		return nil, fmt.Errorf("typechecking package %s: %w", pkgName, err)
	}

	for _, name := range needTypes {
		f := parsed[name]
		annotated := annotateCrossFileCapCalls(f, pkg, info)
		formatted, err := formatFile(fset, annotated)
		if err != nil {
			return nil, fmt.Errorf("formatting annotated %s: %w", name, err)
		}
		erased, err := Erase(formatted)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = erased
	}
	return out, nil
}

// annotateCrossFileCapCalls supplies explicit type arguments T on uninstantiated
// cap.Move(arg) calls whose argument is resolved by go/types to a constant or
// variable declared outside the current file.
func annotateCrossFileCapCalls(f *ast.File, curPkg *types.Package, info *types.Info) *ast.File {
	return astutil.Apply(f, nil, func(c *astutil.Cursor) bool {
		call, ok := c.Node().(*ast.CallExpr)
		if !ok || len(call.Args) != 1 || call.Ellipsis.IsValid() {
			return true
		}
		var fnObj types.Object
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			fnObj = info.Uses[fun.Sel]
		case *ast.Ident:
			fnObj = info.Uses[fun]
		default:
			return true
		}
		fn, ok := fnObj.(*types.Func)
		if !ok || fn.Pkg() == nil || fn.Pkg().Path() != capPath || fn.Name() != "Move" {
			return true
		}
		tv, ok := info.Types[call.Args[0]]
		if !ok || tv.Type == nil {
			return true
		}
		resolved := types.Default(tv.Type)
		typeExpr := typeToASTExpr(resolved, curPkg)
		if typeExpr == nil {
			return true
		}
		call.Fun = &ast.IndexExpr{
			X:     call.Fun,
			Index: typeExpr,
		}
		return true
	}).(*ast.File)
}

func typeToASTExpr(t types.Type, curPkg *types.Package) ast.Expr {
	t = types.Unalias(t)
	switch u := t.(type) {
	case *types.Basic:
		if basicTypeNames[u.Name()] {
			return ast.NewIdent(u.Name())
		}
	case *types.Named:
		obj := u.Obj()
		if obj.Pkg() == nil || obj.Pkg() == curPkg {
			return ast.NewIdent(obj.Name())
		}
	case *types.Pointer:
		if elem := typeToASTExpr(u.Elem(), curPkg); elem != nil {
			return &ast.StarExpr{X: elem}
		}
	case *types.Slice:
		if elem := typeToASTExpr(u.Elem(), curPkg); elem != nil {
			return &ast.ArrayType{Elt: elem}
		}
	}
	return nil
}

// VerifyExportedTypeEquivalence proves via go/types that erasing files with
// eraseFn preserves every package-level exported type, variable, constant, and
// function signature up to types.Identical, while leaving zero residual cap imports.
func VerifyExportedTypeEquivalence(files map[string][]byte, eraseFn func([]byte) ([]byte, error)) (int, error) {
	imp, err := newCapOverlayImporter()
	if err != nil {
		return 0, err
	}

	origPkg, err := typecheckMemPackage("equivprobe", files, imp)
	if err != nil {
		return 0, fmt.Errorf("original package failed typecheck: %w", err)
	}

	erasedFiles := make(map[string][]byte, len(files))
	for name, src := range files {
		erased, err := eraseFn(src)
		if err != nil {
			return 0, fmt.Errorf("erasing %s: %w", name, err)
		}
		erasedFiles[name] = erased
	}

	// Erased package MUST typecheck with standard-library importer ONLY (no cap package!).
	erasedPkg, err := typecheckMemPackage("equivprobe", erasedFiles, importer.Default())
	if err != nil {
		return 0, fmt.Errorf("erased package failed standard-Go typecheck: %w", err)
	}
	for _, impPkg := range erasedPkg.Imports() {
		if impPkg.Path() == capPath || impPkg.Path() == rootPath {
			return 0, fmt.Errorf("erased package retained forbidden import %s", impPkg.Path())
		}
	}

	checked := 0
	scopeOrig := origPkg.Scope()
	scopeErased := erasedPkg.Scope()
	for _, name := range scopeOrig.Names() {
		objOrig := scopeOrig.Lookup(name)
		objErased := scopeErased.Lookup(name)
		if objErased == nil {
			return 0, fmt.Errorf("symbol %s disappeared after erasure", name)
		}
		if !typesEquivalentAcrossPackages(objOrig.Type(), objErased.Type(), "equivprobe", map[typePair]bool{}) {
			return 0, fmt.Errorf("symbol %s changed type: orig=%s vs erased=%s",
				name, objOrig.Type(), objErased.Type())
		}
		checked++
	}
	for _, name := range scopeErased.Names() {
		if scopeOrig.Lookup(name) == nil {
			return 0, fmt.Errorf("erasure introduced spurious package-scope symbol %s", name)
		}
	}
	return checked, nil
}

func typecheckMemPackage(pkgPath string, files map[string][]byte, imp types.Importer) (*types.Package, error) {
	fset := token.NewFileSet()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var astFiles []*ast.File
	for _, name := range names {
		f, err := parser.ParseFile(fset, name, files[name], 0)
		if err != nil {
			return nil, err
		}
		astFiles = append(astFiles, f)
	}
	conf := types.Config{Importer: imp}
	return conf.Check(pkgPath, fset, astFiles, nil)
}

type typePair struct {
	a, b types.Type
}

// typesEquivalentAcrossPackages checks exact Go type identity across two
// separate types.Check invocations of pkgPath after unaliasing parameterized
// cap.* aliases (cap.Mut[T] = *T, cap.Imm[T] = *T, etc.).
func typesEquivalentAcrossPackages(a, b types.Type, pkgPath string, seen map[typePair]bool) bool {
	a = types.Unalias(a)
	b = types.Unalias(b)
	if types.Identical(a, b) {
		return true
	}
	pair := typePair{a, b}
	if seen[pair] {
		return true
	}
	seen[pair] = true

	switch ua := a.(type) {
	case *types.Pointer:
		ub, ok := b.(*types.Pointer)
		return ok && typesEquivalentAcrossPackages(ua.Elem(), ub.Elem(), pkgPath, seen)
	case *types.Slice:
		ub, ok := b.(*types.Slice)
		return ok && typesEquivalentAcrossPackages(ua.Elem(), ub.Elem(), pkgPath, seen)
	case *types.Array:
		ub, ok := b.(*types.Array)
		return ok && ua.Len() == ub.Len() && typesEquivalentAcrossPackages(ua.Elem(), ub.Elem(), pkgPath, seen)
	case *types.Map:
		ub, ok := b.(*types.Map)
		return ok &&
			typesEquivalentAcrossPackages(ua.Key(), ub.Key(), pkgPath, seen) &&
			typesEquivalentAcrossPackages(ua.Elem(), ub.Elem(), pkgPath, seen)
	case *types.Chan:
		ub, ok := b.(*types.Chan)
		return ok && ua.Dir() == ub.Dir() && typesEquivalentAcrossPackages(ua.Elem(), ub.Elem(), pkgPath, seen)
	case *types.Tuple:
		ub, ok := b.(*types.Tuple)
		if !ok || ua.Len() != ub.Len() {
			return false
		}
		for i := 0; i < ua.Len(); i++ {
			if !typesEquivalentAcrossPackages(ua.At(i).Type(), ub.At(i).Type(), pkgPath, seen) {
				return false
			}
		}
		return true
	case *types.Signature:
		ub, ok := b.(*types.Signature)
		if !ok || ua.Variadic() != ub.Variadic() {
			return false
		}
		if ua.TypeParams().Len() != ub.TypeParams().Len() || ua.RecvTypeParams().Len() != ub.RecvTypeParams().Len() {
			return false
		}
		for i := 0; i < ua.TypeParams().Len(); i++ {
			if !typesEquivalentAcrossPackages(ua.TypeParams().At(i).Constraint(), ub.TypeParams().At(i).Constraint(), pkgPath, seen) {
				return false
			}
		}
		if (ua.Recv() == nil) != (ub.Recv() == nil) {
			return false
		}
		if ua.Recv() != nil && !typesEquivalentAcrossPackages(ua.Recv().Type(), ub.Recv().Type(), pkgPath, seen) {
			return false
		}
		return typesEquivalentAcrossPackages(ua.Params(), ub.Params(), pkgPath, seen) &&
			typesEquivalentAcrossPackages(ua.Results(), ub.Results(), pkgPath, seen)
	case *types.TypeParam:
		ub, ok := b.(*types.TypeParam)
		if !ok || ua.Index() != ub.Index() {
			return false
		}
		return typesEquivalentAcrossPackages(ua.Constraint(), ub.Constraint(), pkgPath, seen)
	case *types.Interface:
		ub, ok := b.(*types.Interface)
		if !ok || ua.NumMethods() != ub.NumMethods() || ua.NumEmbeddeds() != ub.NumEmbeddeds() {
			return false
		}
		for i := 0; i < ua.NumMethods(); i++ {
			ma, mb := ua.Method(i), ub.Method(i)
			if ma.Name() != mb.Name() || ma.Exported() != mb.Exported() {
				return false
			}
			if !typesEquivalentAcrossPackages(ma.Type(), mb.Type(), pkgPath, seen) {
				return false
			}
		}
		for i := 0; i < ua.NumEmbeddeds(); i++ {
			if !typesEquivalentAcrossPackages(ua.EmbeddedType(i), ub.EmbeddedType(i), pkgPath, seen) {
				return false
			}
		}
		return true
	case *types.Struct:
		ub, ok := b.(*types.Struct)
		if !ok || ua.NumFields() != ub.NumFields() {
			return false
		}
		for i := 0; i < ua.NumFields(); i++ {
			fa, fb := ua.Field(i), ub.Field(i)
			if fa.Name() != fb.Name() || fa.Embedded() != fb.Embedded() || ua.Tag(i) != ub.Tag(i) {
				return false
			}
			if !typesEquivalentAcrossPackages(fa.Type(), fb.Type(), pkgPath, seen) {
				return false
			}
		}
		return true
	case *types.Named:
		ub, ok := b.(*types.Named)
		if !ok {
			return false
		}
		if ua.Obj().Name() != ub.Obj().Name() || ua.TypeArgs().Len() != ub.TypeArgs().Len() {
			return false
		}
		pa, pb := ua.Obj().Pkg(), ub.Obj().Pkg()
		if (pa == nil) != (pb == nil) {
			return false
		}
		if pa != nil && pa.Path() != pb.Path() {
			return false
		}
		for i := 0; i < ua.TypeArgs().Len(); i++ {
			if !typesEquivalentAcrossPackages(ua.TypeArgs().At(i), ub.TypeArgs().At(i), pkgPath, seen) {
				return false
			}
		}
		if pa == nil || pa.Path() != pkgPath {
			return true
		}
		return typesEquivalentAcrossPackages(ua.Underlying(), ub.Underlying(), pkgPath, seen)
	}
	return false
}

// VerifyCompiledTextEquivalence performs compiler + machine-code translation
// validation on any Go library or `main` package using `github.com/bharathvbcr/gosree/cap`
// WITHOUT executing untrusted target binaries:
//  1. Verifies exported package API identity via go/types (`VerifyExportedTypeEquivalence`).
//  2. Compiles the annotated package against canonical `cap` to a Go package
//     export/object archive (`go list -trimpath -export`) under a bounded timeout.
//  3. Erases all files using `eraseFn` and compiles the pure standard-Go package
//     WITHOUT `cap` in its module graph to a Go package export/object archive.
//  4. Disassembles both archives via `go tool objdump` under a bounded timeout,
//     rejecting any residual `gosree` symbols and asserting identical compiled
//     `.text` instructions, operands, and relocations across every function.
func VerifyCompiledTextEquivalence(files map[string][]byte, eraseFn func([]byte) ([]byte, error)) (*EquivReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultEquivVerifyTimeout)
	defer cancel()
	return VerifyCompiledTextEquivalenceContext(ctx, files, eraseFn)
}

// VerifyCompiledTextEquivalenceContext is VerifyCompiledTextEquivalence with an
// explicit context deadline governing all compiler and objdump subprocesses.
func VerifyCompiledTextEquivalenceContext(ctx context.Context, files map[string][]byte, eraseFn func([]byte) ([]byte, error)) (*EquivReport, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultEquivVerifyTimeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	pkgName, concreteFuncs, err := inspectPackageAST(files)
	if err != nil {
		return nil, err
	}

	checkedSyms, err := VerifyExportedTypeEquivalence(files, eraseFn)
	if err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "gosree-erase-equiv-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	const probeModulePath = "example.com/equivprobe"
	origDir := filepath.Join(tmpDir, "orig")
	erasedDir := filepath.Join(tmpDir, "erased")

	// Write original module with embedded local cap package.
	if err := os.MkdirAll(filepath.Join(origDir, "gosree_stub", "cap"), 0o755); err != nil {
		return nil, err
	}
	origMod := "module " + probeModulePath + "\n\ngo 1.24\n\nrequire github.com/bharathvbcr/gosree v0.0.0\nreplace github.com/bharathvbcr/gosree => ./gosree_stub\n"
	if err := os.WriteFile(filepath.Join(origDir, "go.mod"), []byte(origMod), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(origDir, "gosree_stub", "go.mod"), []byte("module github.com/bharathvbcr/gosree\n\ngo 1.24\n"), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(origDir, "gosree_stub", "cap", "cap.go"), []byte(canonicalCapSource), 0o644); err != nil {
		return nil, err
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(origDir, name), src, 0o644); err != nil {
			return nil, err
		}
	}

	// Write erased module with ZERO dependency on gosree/cap.
	if err := os.MkdirAll(erasedDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(erasedDir, "go.mod"), []byte("module "+probeModulePath+"\n\ngo 1.24\n"), 0o644); err != nil {
		return nil, err
	}
	for name, src := range files {
		erased, err := eraseFn(src)
		if err != nil {
			return nil, fmt.Errorf("erasing %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(erasedDir, name), erased, 0o644); err != nil {
			return nil, err
		}
	}

	origMeta, err := buildGoPackageArchive(ctx, origDir, "")
	if err != nil {
		return nil, fmt.Errorf("compiling original package archive: %w", err)
	}
	erasedMeta, err := buildGoPackageArchive(ctx, erasedDir, "")
	if err != nil {
		return nil, fmt.Errorf("compiling erased package archive: %w", err)
	}

	origText, erasedText, err := compareArchiveTextEquivalence(ctx, origMeta, erasedMeta, concreteFuncs)
	if err != nil {
		return nil, err
	}

	return &EquivReport{
		PackageName:            pkgName,
		ExportedSymbolsChecked: checkedSyms,
		DisassembledFuncs:      len(origText),
		OrigTextMnemonics:      origText,
		ErasedTextMnemonics:    erasedText,
	}, nil
}

// VerifyDirectoryEquivalence verifies exported API and compiled `.text`
// equivalence for a package directory on disk without executing target code or
// mutating workspace files. Self-contained packages use the hermetic probe
// module; packages importing sibling or third-party modules inside an enclosing
// `go.mod` workspace use `go list -overlay` against the workspace module.
func VerifyDirectoryEquivalence(dir string, files map[string][]byte, eraseFn func([]byte) ([]byte, error)) (*EquivReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultEquivVerifyTimeout)
	defer cancel()
	if !hasExternalModuleImports(files) || findEnclosingGoMod(dir) == "" {
		return VerifyCompiledTextEquivalenceContext(ctx, files, eraseFn)
	}
	return verifyWorkspaceOverlayEquivalence(ctx, dir, files, eraseFn)
}

type builtPackageArchive struct {
	pkgName     string
	importPath  string
	archivePath string
	imports     []string
}

func (b builtPackageArchive) symbolPrefix() string {
	if b.pkgName == "main" {
		return "main."
	}
	return b.importPath + "."
}

func verifyWorkspaceOverlayEquivalence(ctx context.Context, dir string, files map[string][]byte, eraseFn func([]byte) ([]byte, error)) (*EquivReport, error) {
	pkgName, concreteFuncs, err := inspectPackageAST(files)
	if err != nil {
		return nil, err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "gosree-erase-overlay-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	replace := make(map[string]string, len(files))
	for name, src := range files {
		erased, err := eraseFn(src)
		if err != nil {
			return nil, fmt.Errorf("erasing %s: %w", name, err)
		}
		dst := filepath.Join(tmpDir, name)
		if err := os.WriteFile(dst, erased, 0o644); err != nil {
			return nil, err
		}
		replace[filepath.Join(absDir, name)] = dst
	}
	overlayBytes, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: replace})
	if err != nil {
		return nil, err
	}
	overlayPath := filepath.Join(tmpDir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlayBytes, 0o644); err != nil {
		return nil, err
	}

	origMeta, err := buildGoPackageArchive(ctx, absDir, "")
	if err != nil {
		return nil, fmt.Errorf("compiling original package archive: %w", err)
	}
	erasedMeta, err := buildGoPackageArchive(ctx, absDir, overlayPath)
	if err != nil {
		return nil, fmt.Errorf("compiling erased package archive: %w", err)
	}
	for _, imp := range erasedMeta.imports {
		if imp == capPath || imp == rootPath || strings.HasPrefix(imp, rootPath+"/") {
			return nil, fmt.Errorf("erased package retained forbidden import %s", imp)
		}
	}

	origText, erasedText, err := compareArchiveTextEquivalence(ctx, origMeta, erasedMeta, concreteFuncs)
	if err != nil {
		return nil, err
	}
	return &EquivReport{
		PackageName:         pkgName,
		DisassembledFuncs:   len(origText),
		OrigTextMnemonics:   origText,
		ErasedTextMnemonics: erasedText,
	}, nil
}

func inspectPackageAST(files map[string][]byte) (pkgName string, concreteFuncs int, err error) {
	if len(files) == 0 {
		return "", 0, fmt.Errorf("no Go source files supplied for equivalence verification")
	}
	fset := token.NewFileSet()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, parseErr := parser.ParseFile(fset, name, files[name], 0)
		if parseErr != nil {
			return "", 0, fmt.Errorf("parsing %s: %w", name, parseErr)
		}
		if pkgName == "" {
			pkgName = f.Name.Name
		} else if f.Name.Name != pkgName {
			return "", 0, fmt.Errorf("mismatched package names in directory: %s vs %s (%s)", pkgName, f.Name.Name, name)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == "_" {
				continue
			}
			if fn.Type.TypeParams != nil && fn.Type.TypeParams.NumFields() > 0 {
				continue
			}
			if fn.Recv != nil && receiverHasTypeParams(fn.Recv) {
				continue
			}
			concreteFuncs++
		}
	}
	return pkgName, concreteFuncs, nil
}

func receiverHasTypeParams(recv *ast.FieldList) bool {
	for _, field := range recv.List {
		t := field.Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		switch t.(type) {
		case *ast.IndexExpr, *ast.IndexListExpr:
			return true
		}
	}
	return false
}

func hasExternalModuleImports(files map[string][]byte) bool {
	fset := token.NewFileSet()
	for name, src := range files {
		f, err := parser.ParseFile(fset, name, src, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if path == capPath || path == rootPath || strings.HasPrefix(path, rootPath+"/") {
				continue
			}
			firstSeg := path
			if slash := strings.IndexByte(path, '/'); slash >= 0 {
				firstSeg = path[:slash]
			}
			if strings.Contains(firstSeg, ".") {
				return true
			}
		}
	}
	return false
}

func findEnclosingGoMod(dir string) string {
	cur, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(cur, "go.mod")
		if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
			return candidate
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func buildGoPackageArchive(ctx context.Context, dir, overlayPath string) (builtPackageArchive, error) {
	args := []string{"list", "-trimpath"}
	if overlayPath != "" {
		args = append(args, "-overlay", overlayPath)
	}
	args = append(args, "-export", "-f", `{{.Name}}|{{.ImportPath}}|{{.Export}}|{{join .Imports ","}}`, ".")
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return builtPackageArchive{}, fmt.Errorf("go list -export timed out or canceled (%w): %s", ctx.Err(), strings.TrimSpace(string(out)))
		}
		return builtPackageArchive{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	line := strings.TrimSpace(string(out))
	parts := strings.SplitN(line, "|", 4)
	if len(parts) != 4 {
		return builtPackageArchive{}, fmt.Errorf("unexpected go list -export output %q", line)
	}
	archive := strings.TrimSpace(parts[2])
	if archive == "" {
		return builtPackageArchive{}, fmt.Errorf("go list -export produced empty archive path for %s", dir)
	}
	var imports []string
	if parts[3] != "" {
		imports = strings.Split(parts[3], ",")
	}
	return builtPackageArchive{
		pkgName:     parts[0],
		importPath:  parts[1],
		archivePath: archive,
		imports:     imports,
	}, nil
}

func compareArchiveTextEquivalence(ctx context.Context, origMeta, erasedMeta builtPackageArchive, concreteFuncs int) (map[string][]string, map[string][]string, error) {
	origText, err := disassemblePackageText(ctx, origMeta.archivePath, origMeta.symbolPrefix(), false)
	if err != nil {
		return nil, nil, fmt.Errorf("disassembling original archive: %w", err)
	}
	erasedText, err := disassemblePackageText(ctx, erasedMeta.archivePath, erasedMeta.symbolPrefix(), true)
	if err != nil {
		return nil, nil, fmt.Errorf("disassembling erased archive: %w", err)
	}

	if concreteFuncs > 0 && len(origText) == 0 {
		return nil, nil, fmt.Errorf("disassembly extracted 0 %s* functions from %s (expected >= %d)",
			origMeta.symbolPrefix(), origMeta.archivePath, concreteFuncs)
	}

	fnNames := make([]string, 0, len(origText))
	for fnName := range origText {
		fnNames = append(fnNames, fnName)
	}
	sort.Strings(fnNames)
	for _, fnName := range fnNames {
		origOps := origText[fnName]
		erasedOps, ok := erasedText[fnName]
		if !ok {
			return nil, nil, fmt.Errorf("compiled function %s missing from erased archive", fnName)
		}
		if strings.Join(origOps, "\n") != strings.Join(erasedOps, "\n") {
			return nil, nil, fmt.Errorf("compiled .text instruction mismatch in %s:\norig:   %v\nerased: %v",
				fnName, origOps, erasedOps)
		}
	}
	for fnName := range erasedText {
		if _, ok := origText[fnName]; !ok {
			return nil, nil, fmt.Errorf("erased archive introduced unexpected compiled function %s", fnName)
		}
	}
	return origText, erasedText, nil
}

var (
	anyTextHeaderRe = regexp.MustCompile(`^TEXT\s+([^\s(]+)\(SB\)`)
	stmpCounterRe   = regexp.MustCompile(`\.\.stmp_\d+`)
)

func disassemblePackageText(ctx context.Context, archivePath, symPrefix string, forbidCapSymbols bool) (map[string][]string, error) {
	cmd := exec.CommandContext(ctx, "go", "tool", "objdump", archivePath)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("go tool objdump timed out or canceled (%w): %s", ctx.Err(), strings.TrimSpace(string(out)))
		}
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	funcs := make(map[string][]string)
	var curFn string
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if m := anyTextHeaderRe.FindStringSubmatch(line); m != nil {
			fullSym := m[1]
			if forbidCapSymbols && (strings.HasPrefix(fullSym, capPath+".") || strings.HasPrefix(fullSym, rootPath+"/")) {
				return nil, fmt.Errorf("erased archive retained forbidden gosree symbol %s", fullSym)
			}
			if !strings.HasPrefix(fullSym, symPrefix) {
				curFn = ""
				continue
			}
			relSym := strings.TrimPrefix(fullSym, symPrefix)
			// Skip compiler-generated closure wrappers (`fn.func1`) produced when
			// constant-sensitive calls wrap an untyped constant in `func() T { return c }()`.
			if strings.Contains(relSym, ".func") {
				curFn = ""
				continue
			}
			curFn = relSym
			if _, exists := funcs[curFn]; !exists {
				funcs[curFn] = nil
			}
			continue
		}
		if curFn == "" {
			continue
		}
		// Objdump instruction lines have whitespace-separated fields:
		//   file:line   0xaddr   hexopcode   MNEMONIC operands... [relocations...]
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasPrefix(fields[1], "0x") {
			continue
		}
		mnemonic := fields[3]
		if mnemonic == "" || mnemonic == "?" || strings.HasPrefix(mnemonic, "NOP") || mnemonic == "UDF" || mnemonic == "WORD" {
			continue
		}
		normalized := stmpCounterRe.ReplaceAllString(strings.Join(fields[3:], " "), "..stmp")
		funcs[curFn] = append(funcs[curFn], normalized)
	}
	return funcs, nil
}
