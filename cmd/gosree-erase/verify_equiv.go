package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
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

	return comparePackageScopes(origPkg, erasedPkg, "equivprobe")
}

func comparePackageScopes(origPkg, erasedPkg *types.Package, pkgPath string) (int, error) {
	checked := 0
	scopeOrig := origPkg.Scope()
	scopeErased := erasedPkg.Scope()
	for _, name := range scopeOrig.Names() {
		objOrig := scopeOrig.Lookup(name)
		objErased := scopeErased.Lookup(name)
		if objErased == nil {
			return 0, fmt.Errorf("symbol %s disappeared after erasure", name)
		}
		if !typesEquivalentAcrossPackages(objOrig.Type(), objErased.Type(), pkgPath, map[typePair]bool{}) {
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
		if !ok || ua.IsComparable() != ub.IsComparable() || ua.NumMethods() != ub.NumMethods() || ua.NumEmbeddeds() != ub.NumEmbeddeds() {
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
		if ua.Obj().Name() != ub.Obj().Name() || ua.TypeArgs().Len() != ub.TypeArgs().Len() || ua.TypeParams().Len() != ub.TypeParams().Len() {
			return false
		}
		pa, pb := ua.Obj().Pkg(), ub.Obj().Pkg()
		if (pa == nil) != (pb == nil) {
			return false
		}
		if pa != nil && pa.Path() != pb.Path() {
			return false
		}
		for i := 0; i < ua.TypeParams().Len(); i++ {
			if !typesEquivalentAcrossPackages(ua.TypeParams().At(i).Constraint(), ub.TypeParams().At(i).Constraint(), pkgPath, seen) {
				return false
			}
		}
		for i := 0; i < ua.TypeArgs().Len(); i++ {
			if !typesEquivalentAcrossPackages(ua.TypeArgs().At(i), ub.TypeArgs().At(i), pkgPath, seen) {
				return false
			}
		}
		if pa == nil || pa.Path() != pkgPath {
			return true
		}
		if ua.NumMethods() != ub.NumMethods() {
			return false
		}
		for i := 0; i < ua.NumMethods(); i++ {
			ma, mb := ua.Method(i), ub.Method(i)
			if ma.Name() != mb.Name() || !typesEquivalentAcrossPackages(ma.Type(), mb.Type(), pkgPath, seen) {
				return false
			}
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
	erasedFiles := make(map[string][]byte, len(files))
	for name, src := range files {
		erased, err := eraseFn(src)
		if err != nil {
			return nil, fmt.Errorf("erasing %s: %w", name, err)
		}
		erasedFiles[name] = erased
		if err := os.WriteFile(filepath.Join(erasedDir, name), erased, 0o644); err != nil {
			return nil, err
		}
	}

	synthBudgets := computeSyntheticClosureBudgets(files, erasedFiles)

	origMeta, err := buildGoPackageArchive(ctx, origDir, "")
	if err != nil {
		return nil, fmt.Errorf("compiling original package archive: %w", err)
	}
	erasedMeta, err := buildGoPackageArchive(ctx, erasedDir, "")
	if err != nil {
		return nil, fmt.Errorf("compiling erased package archive: %w", err)
	}

	origText, erasedText, err := compareArchiveTextEquivalence(ctx, origMeta, erasedMeta, concreteFuncs, synthBudgets)
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
// mutating workspace files. Build constraints (`//go:build`, OS/arch filename
// suffixes) are honoured identically to `go build`. Self-contained packages use
// the hermetic probe module; packages importing sibling or third-party modules
// inside an enclosing `go.mod` workspace use `go/packages` + `go list -overlay`.
func VerifyDirectoryEquivalence(dir string, files map[string][]byte, eraseFn func([]byte) ([]byte, error)) (*EquivReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultEquivVerifyTimeout)
	defer cancel()
	return verifyDirectoryWithSharedOverlay(ctx, dir, files, eraseFn, nil)
}

func canonicalPackageDir(dir string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if realDir, evalErr := filepath.EvalSymlinks(absDir); evalErr == nil {
		return filepath.Clean(realDir), nil
	}
	return filepath.Clean(absDir), nil
}

// VerifyDirectoriesEquivalence verifies multiple package directories together
// under a shared erased-file overlay so cross-package workspace erasure (`./...`)
// typechecks and compiles downstream packages against erased upstream packages.
func VerifyDirectoriesEquivalence(byDir map[string]map[string][]byte, eraseFn func([]byte) ([]byte, error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultEquivVerifyTimeout)
	defer cancel()

	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	sharedOverlay := make(map[string][]byte)
	activeByDir := make(map[string]map[string][]byte, len(dirs))
	canonByDir := make(map[string]string, len(dirs))
	for _, dir := range dirs {
		canonDir, err := canonicalPackageDir(dir)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		canonByDir[dir] = canonDir
		active, err := filterActiveGoFiles(canonDir, byDir[dir])
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		activeByDir[dir] = active
		for name, src := range active {
			erased, err := eraseFn(src)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", dir, name, err)
			}
			sharedOverlay[filepath.Join(canonDir, name)] = erased
		}
	}

	for _, dir := range dirs {
		active := activeByDir[dir]
		if len(active) == 0 {
			continue
		}
		if _, err := verifyDirectoryWithSharedOverlay(ctx, canonByDir[dir], active, eraseFn, sharedOverlay); err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
	}
	return nil
}

func verifyDirectoryWithSharedOverlay(
	ctx context.Context,
	dir string,
	files map[string][]byte,
	eraseFn func([]byte) ([]byte, error),
	sharedOverlay map[string][]byte,
) (*EquivReport, error) {
	absDir, err := canonicalPackageDir(dir)
	if err != nil {
		return nil, err
	}
	activeFiles, err := filterActiveGoFiles(absDir, files)
	if err != nil {
		return nil, err
	}
	if len(activeFiles) == 0 {
		for name, src := range files {
			if _, err := eraseFn(src); err != nil {
				return nil, fmt.Errorf("erasing %s: %w", name, err)
			}
		}
		return &EquivReport{}, nil
	}
	if !hasNonStdlibImports(activeFiles) || findEnclosingGoMod(absDir) == "" {
		return VerifyCompiledTextEquivalenceContext(ctx, activeFiles, eraseFn)
	}
	return verifyWorkspaceOverlayEquivalence(ctx, absDir, activeFiles, eraseFn, sharedOverlay)
}

func filterActiveGoFiles(absDir string, files map[string][]byte) (map[string][]byte, error) {
	cleanDir := filepath.Clean(absDir)
	bctx := build.Default
	bctx.OpenFile = func(path string) (io.ReadCloser, error) {
		cleanPath := filepath.Clean(path)
		if filepath.Dir(cleanPath) == cleanDir {
			if src, ok := files[filepath.Base(cleanPath)]; ok {
				return io.NopCloser(bytes.NewReader(src)), nil
			}
		}
		return os.Open(path)
	}
	active := make(map[string][]byte, len(files))
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") || !strings.HasSuffix(name, ".go") {
			continue
		}
		match, err := bctx.MatchFile(cleanDir, name)
		if err != nil {
			return nil, fmt.Errorf("evaluating build constraints for %s: %w", name, err)
		}
		if match {
			active[name] = files[name]
		}
	}
	return active, nil
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

func verifyWorkspaceOverlayEquivalence(
	ctx context.Context,
	absDir string,
	files map[string][]byte,
	eraseFn func([]byte) ([]byte, error),
	sharedOverlay map[string][]byte,
) (*EquivReport, error) {
	pkgName, concreteFuncs, err := inspectPackageAST(files)
	if err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "gosree-erase-overlay-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	erasedLocal := make(map[string][]byte, len(files))
	overlayBytesMap := make(map[string][]byte, len(files)+len(sharedOverlay))
	for absPath, data := range sharedOverlay {
		overlayBytesMap[absPath] = data
	}
	for name, src := range files {
		fullPath := filepath.Join(absDir, name)
		erased, ok := sharedOverlay[fullPath]
		if !ok {
			var eraseErr error
			erased, eraseErr = eraseFn(src)
			if eraseErr != nil {
				return nil, fmt.Errorf("erasing %s: %w", name, eraseErr)
			}
		}
		erasedLocal[name] = erased
		overlayBytesMap[fullPath] = erased
	}

	checkedSyms, err := verifyWorkspaceTypesEquivalence(ctx, absDir, overlayBytesMap)
	if err != nil {
		return nil, err
	}

	replace := make(map[string]string, len(overlayBytesMap))
	paths := make([]string, 0, len(overlayBytesMap))
	for p := range overlayBytesMap {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for idx, fullPath := range paths {
		dst := filepath.Join(tmpDir, fmt.Sprintf("%04d_%s", idx, filepath.Base(fullPath)))
		if err := os.WriteFile(dst, overlayBytesMap[fullPath], 0o644); err != nil {
			return nil, err
		}
		replace[fullPath] = dst
	}
	overlayJSON, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: replace})
	if err != nil {
		return nil, err
	}
	overlayPath := filepath.Join(tmpDir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlayJSON, 0o644); err != nil {
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
	forbidGosreeRoot := !strings.HasPrefix(erasedMeta.importPath, rootPath+"/") && erasedMeta.importPath != rootPath
	for _, imp := range erasedMeta.imports {
		if imp == capPath || (forbidGosreeRoot && (imp == rootPath || strings.HasPrefix(imp, rootPath+"/"))) {
			return nil, fmt.Errorf("erased package retained forbidden import %s", imp)
		}
	}

	synthBudgets := computeSyntheticClosureBudgets(files, erasedLocal)
	origText, erasedText, err := compareArchiveTextEquivalence(ctx, origMeta, erasedMeta, concreteFuncs, synthBudgets)
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

func verifyWorkspaceTypesEquivalence(ctx context.Context, absDir string, overlay map[string][]byte) (int, error) {
	loadPkg := func(ov map[string][]byte) (*types.Package, error) {
		cfg := &packages.Config{
			Context: ctx,
			Mode:    packages.NeedName | packages.NeedTypes | packages.NeedImports,
			Dir:     absDir,
			Env:     append(os.Environ(), "GOWORK=off"),
			Overlay: ov,
		}
		pkgs, err := packages.Load(cfg, ".")
		if err != nil {
			return nil, err
		}
		if len(pkgs) != 1 {
			return nil, fmt.Errorf("expected 1 package in %s, got %d", absDir, len(pkgs))
		}
		if len(pkgs[0].Errors) > 0 {
			return nil, fmt.Errorf("%s", pkgs[0].Errors[0].Error())
		}
		if pkgs[0].Types == nil {
			return nil, fmt.Errorf("missing go/types package for %s", absDir)
		}
		return pkgs[0].Types, nil
	}

	origPkg, err := loadPkg(nil)
	if err != nil {
		return 0, fmt.Errorf("typechecking original workspace package: %w", err)
	}
	erasedPkg, err := loadPkg(overlay)
	if err != nil {
		return 0, fmt.Errorf("typechecking erased workspace package: %w", err)
	}
	forbidGosreeRoot := !strings.HasPrefix(erasedPkg.Path(), rootPath+"/") && erasedPkg.Path() != rootPath
	for _, impPkg := range erasedPkg.Imports() {
		if impPkg.Path() == capPath || (forbidGosreeRoot && (impPkg.Path() == rootPath || strings.HasPrefix(impPkg.Path(), rootPath+"/"))) {
			return 0, fmt.Errorf("erased package retained forbidden import %s", impPkg.Path())
		}
	}
	return comparePackageScopes(origPkg, erasedPkg, origPkg.Path())
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

func hasNonStdlibImports(files map[string][]byte) bool {
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
			if path == capPath || path == "unsafe" || path == "C" {
				continue
			}
			pkg, findErr := build.Default.Import(path, "", build.FindOnly)
			if findErr != nil || !pkg.Goroot {
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

// computeSyntheticClosureBudgets bounds, for each top-level AST scope (`FuncDecl`
// symbol key or `"init"` for package-level initializers), how many synthetic
// constant-wrapper closures (`func() T { return c }()`) `Erase` legitimately added.
func computeSyntheticClosureBudgets(origFiles, erasedFiles map[string][]byte) map[string]int {
	origLits, origCapUses := countScopeFuncLitsAndCapSelectors(origFiles)
	erasedLits, _ := countScopeFuncLitsAndCapSelectors(erasedFiles)
	budgets := make(map[string]int)
	for scope, eCount := range erasedLits {
		delta := eCount - origLits[scope]
		if delta <= 0 {
			continue
		}
		capUses := origCapUses[scope]
		if capUses <= 0 {
			continue
		}
		if delta > capUses {
			delta = capUses
		}
		budgets[scope] = delta
	}
	return budgets
}

func countScopeFuncLitsAndCapSelectors(files map[string][]byte) (funcLits map[string]int, capUses map[string]int) {
	funcLits = make(map[string]int)
	capUses = make(map[string]int)
	fset := token.NewFileSet()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := parser.ParseFile(fset, name, files[name], 0)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			scope := "init"
			var root ast.Node = decl
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if fn.Body == nil || fn.Name.Name == "_" {
					continue
				}
				scope = astFuncScopeKey(fn)
				root = fn.Body
			}
			ast.Inspect(root, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.FuncLit:
					funcLits[scope]++
				case *ast.SelectorExpr:
					if helperFuncs[x.Sel.Name] {
						capUses[scope]++
					}
				}
				return true
			})
		}
	}
	return funcLits, capUses
}

func astFuncScopeKey(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		if fn.Name.Name == "init" {
			return "init"
		}
		return fn.Name.Name
	}
	t := fn.Recv.List[0].Type
	ptr := false
	if star, ok := t.(*ast.StarExpr); ok {
		ptr = true
		t = star.X
	}
	base := recvTypeIdent(t)
	if ptr {
		return "(*" + base + ")." + fn.Name.Name
	}
	return base + "." + fn.Name.Name
}

func recvTypeIdent(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return recvTypeIdent(x.X)
	case *ast.IndexListExpr:
		return recvTypeIdent(x.X)
	default:
		return types.ExprString(expr)
	}
}

func splitCompilerClosureSym(relSym string) (baseScope string, suffixNums []int, isClosure bool) {
	searchFrom := 0
	for {
		idx := strings.Index(relSym[searchFrom:], ".func")
		if idx < 0 {
			return relSym, nil, false
		}
		pos := searchFrom + idx
		tail := relSym[pos+len(".func"):]
		if nums, ok := parseDotNumbers(tail); ok {
			return relSym[:pos], nums, true
		}
		searchFrom = pos + len(".func")
	}
}

func parseDotNumbers(s string) ([]int, bool) {
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil, false
		}
		n := 0
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return nil, false
			}
			n = n*10 + int(p[i]-'0')
		}
		nums = append(nums, n)
	}
	return nums, true
}

func canonicalScopeKey(baseSym string) string {
	if baseSym == "init" {
		return "init"
	}
	if strings.HasPrefix(baseSym, "init.") {
		if _, ok := parseDotNumbers(strings.TrimPrefix(baseSym, "init.")); ok {
			return "init"
		}
	}
	return baseSym
}

func normalizeClosureRefs(ops []string, symPrefix, baseScope string) []string {
	re := regexp.MustCompile(regexp.QuoteMeta(symPrefix+baseScope) + `\.func\d+(?:\.\d+)*`)
	repl := symPrefix + baseScope + ".func#"
	out := make([]string, len(ops))
	for i, op := range ops {
		out[i] = re.ReplaceAllString(op, repl)
	}
	return out
}

func isPureSyntheticWrapperClosure(ops []string) bool {
	for _, op := range ops {
		fields := strings.Fields(op)
		if len(fields) == 0 {
			continue
		}
		mn := fields[0]
		if mn == "CALL" || mn == "BL" {
			if !strings.Contains(op, "runtime.morestack") {
				return false
			}
		}
	}
	return true
}

func compareArchiveTextEquivalence(
	ctx context.Context,
	origMeta, erasedMeta builtPackageArchive,
	concreteFuncs int,
	synthBudgets map[string]int,
) (map[string][]string, map[string][]string, error) {
	forbidGosreeRoot := !strings.HasPrefix(erasedMeta.importPath, rootPath+"/") && erasedMeta.importPath != rootPath
	origText, err := disassemblePackageText(ctx, origMeta.archivePath, origMeta.symbolPrefix(), false, false)
	if err != nil {
		return nil, nil, fmt.Errorf("disassembling original archive: %w", err)
	}
	erasedText, err := disassemblePackageText(ctx, erasedMeta.archivePath, erasedMeta.symbolPrefix(), true, forbidGosreeRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("disassembling erased archive: %w", err)
	}

	if concreteFuncs > 0 && len(origText) == 0 {
		return nil, nil, fmt.Errorf("disassembly extracted 0 %s* functions from %s (expected >= %d)",
			origMeta.symbolPrefix(), origMeta.archivePath, concreteFuncs)
	}

	type closureSym struct {
		name string
		base string
		nums []int
	}
	origClosures := make(map[string][]closureSym)
	erasedClosures := make(map[string][]closureSym)
	var origTop []string
	origTopSet := make(map[string]bool)

	for fnName := range origText {
		if base, nums, ok := splitCompilerClosureSym(fnName); ok {
			origClosures[base] = append(origClosures[base], closureSym{name: fnName, base: base, nums: nums})
		} else {
			origTop = append(origTop, fnName)
			origTopSet[fnName] = true
		}
	}
	sort.Strings(origTop)

	for fnName := range erasedText {
		if base, nums, ok := splitCompilerClosureSym(fnName); ok {
			erasedClosures[base] = append(erasedClosures[base], closureSym{name: fnName, base: base, nums: nums})
		} else if !origTopSet[fnName] {
			return nil, nil, fmt.Errorf("erased archive introduced unexpected compiled function %s", fnName)
		}
	}

	symPrefix := origMeta.symbolPrefix()
	for _, fnName := range origTop {
		origOps := origText[fnName]
		erasedOps, ok := erasedText[fnName]
		if !ok {
			return nil, nil, fmt.Errorf("compiled function %s missing from erased archive", fnName)
		}
		budget := synthBudgets[canonicalScopeKey(fnName)]
		cmpOrig, cmpErased := origOps, erasedOps
		if budget > 0 {
			cmpOrig = normalizeClosureRefs(origOps, symPrefix, fnName)
			cmpErased = normalizeClosureRefs(erasedOps, symPrefix, fnName)
		}
		if strings.Join(cmpOrig, "\n") != strings.Join(cmpErased, "\n") {
			return nil, nil, fmt.Errorf("compiled .text instruction mismatch in %s:\norig:   %v\nerased: %v",
				fnName, origOps, erasedOps)
		}
	}

	closureBasesSet := make(map[string]bool)
	for base := range origClosures {
		closureBasesSet[base] = true
	}
	for base := range erasedClosures {
		closureBasesSet[base] = true
	}
	closureBases := make([]string, 0, len(closureBasesSet))
	for base := range closureBasesSet {
		closureBases = append(closureBases, base)
	}
	sort.Strings(closureBases)

	sortClosureSlice := func(xs []closureSym) {
		sort.Slice(xs, func(i, j int) bool {
			a, b := xs[i].nums, xs[j].nums
			for k := 0; k < len(a) && k < len(b); k++ {
				if a[k] != b[k] {
					return a[k] < b[k]
				}
			}
			return len(a) < len(b)
		})
	}

	for _, base := range closureBases {
		ocs := origClosures[base]
		ecs := erasedClosures[base]
		sortClosureSlice(ocs)
		sortClosureSlice(ecs)
		budget := synthBudgets[canonicalScopeKey(base)]

		if budget == 0 {
			if len(ocs) != len(ecs) {
				return nil, nil, fmt.Errorf("closure count mismatch in %s: orig=%d vs erased=%d", base, len(ocs), len(ecs))
			}
			for i := range ocs {
				if ocs[i].name != ecs[i].name {
					return nil, nil, fmt.Errorf("closure symbol mismatch in %s: orig=%s vs erased=%s", base, ocs[i].name, ecs[i].name)
				}
				origOps := origText[ocs[i].name]
				erasedOps := erasedText[ecs[i].name]
				if strings.Join(origOps, "\n") != strings.Join(erasedOps, "\n") {
					return nil, nil, fmt.Errorf("compiled .text instruction mismatch in %s:\norig:   %v\nerased: %v",
						ocs[i].name, origOps, erasedOps)
				}
			}
			continue
		}

		if len(ecs) < len(ocs) {
			return nil, nil, fmt.Errorf("compiled closures missing in %s: orig=%d vs erased=%d", base, len(ocs), len(ecs))
		}
		if len(ecs)-len(ocs) > budget {
			return nil, nil, fmt.Errorf("erased archive introduced %d extra closures in %s (synthetic budget %d)",
				len(ecs)-len(ocs), base, budget)
		}

		matchedErased := make([]bool, len(ecs))
		ej := 0
		for _, oc := range ocs {
			origNorm := strings.Join(normalizeClosureRefs(origText[oc.name], symPrefix, base), "\n")
			found := false
			for ej < len(ecs) {
				erasedNorm := strings.Join(normalizeClosureRefs(erasedText[ecs[ej].name], symPrefix, base), "\n")
				if origNorm == erasedNorm {
					matchedErased[ej] = true
					ej++
					found = true
					break
				}
				ej++
			}
			if !found {
				return nil, nil, fmt.Errorf("compiled .text closure mismatch for %s (no matching closure in erased %s)", oc.name, base)
			}
		}
		for idx, ec := range ecs {
			if matchedErased[idx] {
				continue
			}
			if !isPureSyntheticWrapperClosure(erasedText[ec.name]) {
				return nil, nil, fmt.Errorf("unmatched erased closure %s contains non-trivial CALL instructions: %v",
					ec.name, erasedText[ec.name])
			}
		}
	}

	return origText, erasedText, nil
}

var (
	anyTextHeaderRe = regexp.MustCompile(`^TEXT\s+([^\s(]+)\(SB\)`)
	stmpCounterRe   = regexp.MustCompile(`\.\.stmp_\d+`)
)

func disassemblePackageText(ctx context.Context, archivePath, symPrefix string, forbidCapSymbols, forbidGosreeRoot bool) (map[string][]string, error) {
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
			if forbidCapSymbols && (strings.HasPrefix(fullSym, capPath+".") || (forbidGosreeRoot && strings.HasPrefix(fullSym, rootPath+"/"))) {
				return nil, fmt.Errorf("erased archive retained forbidden gosree symbol %s", fullSym)
			}
			if !strings.HasPrefix(fullSym, symPrefix) {
				curFn = ""
				continue
			}
			curFn = strings.TrimPrefix(fullSym, symPrefix)
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
