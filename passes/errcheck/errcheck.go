// Package errcheck enforces Rust-grade Result<T, E> / #[must_use] error
// discipline on Go code (ERR001-ERR005), backed by the in-process Rust CFG/SSA
// abstract-interpretation solver over Gusset (internal/gusseterr).
//
// Rules:
//
//	ERR001  an error-returning call's result is silently discarded (bare call, go, or defer)
//	ERR002  a companion value from (T, error) is used when err was blanked (_) or not proven nil
//	ERR003  an error is lost via inner `err :=` shadowing, overwriting an unchecked err, or `if err != nil { return nil }`
//	ERR004  a concrete pointer type implementing error that may be nil is boxed into an error interface (typed-nil error)
//	ERR005  Close() error is dropped on a writable or written-to resource (flush data-loss hazard)
package errcheck

import (
	"cmp"
	"context"
	"fmt"
	"go/token"
	"reflect"
	"slices"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gosree/internal/annot"
	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

// Rule identifiers.
const (
	UnhandledError    = "ERR001"
	UncheckedUnwrap   = "ERR002"
	LostOrShadowedErr = "ERR003"
	TypedNilError     = "ERR004"
	WritableCloseDrop = "ERR005"
)

// FuncFact records interprocedural error-return properties exported across
// package boundaries (gob-serialized by go vet / multichecker).
type FuncFact struct {
	// MayReturnNilConcrete reports whether a function whose result is a concrete
	// pointer type *T implementing error has any reachable return path that can
	// yield nil.
	MayReturnNilConcrete bool
}

// AFact marks FuncFact as a go/analysis fact.
func (*FuncFact) AFact() {}

func (f *FuncFact) String() string {
	if f.MayReturnNilConcrete {
		return "may_return_nil_concrete_err"
	}
	return "non_nil_concrete_err"
}

// Summary reports per-package solver counts for harnesses and tests.
type Summary struct {
	FunctionsAnalyzed int
	FindingsReported  int
	FindingsMuted     int
	UsedRustGusset    bool
}

// Analyzer is the GoSree error-discipline analyzer ("safeerr").
var Analyzer = &analysis.Analyzer{
	Name:       "safeerr",
	Doc:        "enforce Rust-style #[must_use] Result<T, E> error discipline, typed-nil prevention, and writable Close checks (ERR001-ERR005)",
	Run:        run,
	Requires:   []*analysis.Analyzer{annot.Analyzer, buildssa.Analyzer},
	FactTypes:  []analysis.Fact{new(FuncFact)},
	ResultType: reflect.TypeFor[*Summary](),
}

type siteMeta struct {
	pos    token.Pos
	detail string
	aux    string
}

type diagItem struct {
	pos      token.Pos
	position token.Position
	category string
	msg      string
}

func run(pass *analysis.Pass) (any, error) {
	ix := pass.ResultOf[annot.Analyzer].(*annot.Index)
	ssaRes := pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA)

	var extraRoots []*ssa.Function
	if ssaRes.Pkg != nil {
		if pkgInit := ssaRes.Pkg.Func("init"); pkgInit != nil {
			extraRoots = pkgInit.AnonFuncs
		}
	}
	funcs := collectPackageFuncs(ssaRes.SrcFuncs, extraRoots...)
	summary := &Summary{
		FunctionsAnalyzed: len(funcs),
		UsedRustGusset:    gusseterr.NativeActive(),
	}
	if len(funcs) == 0 {
		return summary, nil
	}

	b := newPackageBuilder(pass, funcs)
	irFuncs := b.lowerAll()

	res, err := gusseterr.Solve(context.Background(), irFuncs)
	if err != nil {
		return nil, fmt.Errorf("safeerr solver failed: %w", err)
	}

	// Export cross-package FuncFacts for exported concrete-error-pointer functions so
	// downstream packages distinguish proven-non-nil from may-return-nil and from
	// unanalyzed external stubs.
	for idx, fn := range funcs {
		if idx < len(res.MayReturnNil) && fn.Synthetic == "" && sigReturnsConcreteErrPtr(fn.Signature) {
			if obj := fn.Object(); obj != nil && obj.Pkg() == pass.Pkg && obj.Exported() {
				pass.ExportObjectFact(obj, &FuncFact{MayReturnNilConcrete: res.MayReturnNil[idx]})
			}
		}
	}

	// Format, deduplicate per (line, rule_code, detail), honor //gosree:unsafe,
	// and sort deterministically.
	type dedupKey struct {
		file string
		line int
		code uint8
		sub  uint8
	}
	seenLineRule := make(map[dedupKey]bool, len(res.Findings))
	var diags []diagItem

	for _, f := range res.Findings {
		site := b.site(f.SiteID)
		if !site.pos.IsValid() {
			continue
		}
		// Blanked companion findings attribute to the tuple's assignment site once.
		if f.RuleCode == gusseterr.RuleERR002 && f.SubKind == gusseterr.SubERR002Blanked {
			if callPos, callDesc, ok := b.blankedCallSite(f.FuncIdx, f.SiteID); ok && callPos.IsValid() {
				site = siteMeta{pos: callPos, detail: callDesc, aux: site.detail}
			}
		}

		posn := pass.Fset.Position(site.pos)
		if !posn.IsValid() || posn.Line == 0 {
			continue
		}
		dk := dedupKey{file: posn.Filename, line: posn.Line, code: f.RuleCode, sub: f.SubKind}
		if seenLineRule[dk] {
			continue
		}
		seenLineRule[dk] = true

		if ix.Suppressed(site.pos) {
			ix.Mute(site.pos)
			summary.FindingsMuted++
			continue
		}

		cat, msg := formatDiagnostic(f.RuleCode, f.SubKind, site)
		diags = append(diags, diagItem{
			pos:      site.pos,
			position: posn,
			category: cat,
			msg:      msg,
		})
	}

	slices.SortFunc(diags, func(a, b diagItem) int {
		if c := cmp.Compare(a.position.Filename, b.position.Filename); c != 0 {
			return c
		}
		if c := cmp.Compare(a.position.Line, b.position.Line); c != 0 {
			return c
		}
		if c := cmp.Compare(a.position.Column, b.position.Column); c != 0 {
			return c
		}
		return cmp.Compare(a.msg, b.msg)
	})

	for _, d := range diags {
		pass.Report(analysis.Diagnostic{
			Pos:      d.pos,
			Category: d.category,
			Message:  d.msg,
		})
		summary.FindingsReported++
	}

	return summary, nil
}

func collectPackageFuncs(roots []*ssa.Function, extras ...*ssa.Function) []*ssa.Function {
	var out []*ssa.Function
	seen := make(map[*ssa.Function]bool, (len(roots)+len(extras))*2)
	var visit func(fn *ssa.Function)
	visit = func(fn *ssa.Function) {
		if fn == nil || seen[fn] {
			return
		}
		seen[fn] = true
		out = append(out, fn)
		for _, anon := range fn.AnonFuncs {
			visit(anon)
		}
	}
	for _, r := range roots {
		visit(r)
	}
	for _, r := range extras {
		visit(r)
	}
	return out
}

func formatDiagnostic(rule uint8, sub uint8, site siteMeta) (string, string) {
	target := site.detail
	if target == "" {
		target = "value"
	}
	switch rule {
	case gusseterr.RuleERR001:
		return UnhandledError, fmt.Sprintf("%s: error return from %s is discarded without handling (bind and check err, or assign to _ to discard explicitly)", UnhandledError, target)
	case gusseterr.RuleERR002:
		if sub == gusseterr.SubERR002Blanked {
			return UncheckedUnwrap, fmt.Sprintf("%s: companion value from %s is used after discarding its error with '_'", UncheckedUnwrap, target)
		}
		return UncheckedUnwrap, fmt.Sprintf("%s: %s from fallible call is used before proving its companion error is nil", UncheckedUnwrap, target)
	case gusseterr.RuleERR003:
		switch sub {
		case gusseterr.SubERR003Shadowed:
			return LostOrShadowedErr, fmt.Sprintf("%s: inner '%s' declaration shadows outer '%s', losing the inner error on fallthrough", LostOrShadowedErr, target, target)
		case gusseterr.SubERR003Nilerr:
			return LostOrShadowedErr, fmt.Sprintf("%s: returning nil error on a path where an active error was just checked non-nil (nilerr)", LostOrShadowedErr)
		default:
			return LostOrShadowedErr, fmt.Sprintf("%s: assignment to '%s' overwrites an earlier unchecked error", LostOrShadowedErr, target)
		}
	case gusseterr.RuleERR004:
		if sub == gusseterr.SubERR004MustNil {
			return TypedNilError, fmt.Sprintf("%s: nil concrete pointer %s is boxed into a non-nil error interface (typed-nil hazard)", TypedNilError, target)
		}
		return TypedNilError, fmt.Sprintf("%s: concrete pointer %s may be nil when boxed into error interface; guard with '!= nil' and return untyped nil", TypedNilError, target)
	case gusseterr.RuleERR005:
		if sub == gusseterr.SubERR005DeferClose {
			return WritableCloseDrop, fmt.Sprintf("%s: deferred Close() on writable resource %s discards flush error", WritableCloseDrop, target)
		}
		return WritableCloseDrop, fmt.Sprintf("%s: Close() on writable resource %s discards flush error", WritableCloseDrop, target)
	default:
		return UnhandledError, fmt.Sprintf("%s: unhandled error hazard at %s", UnhandledError, target)
	}
}
