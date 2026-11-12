package ownership

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Three standing checks on this package's own source.
//
// Every miss found in the walks that follow a value backwards had one shape: a walk stopped (out of
// budget, too deep, or at code it could not see) and the code that asked took "stopped" for
// "nothing there". Tests of behaviour catch that one case at a time. These read the source, so that
// the next walk, the next limit or the next check that asks cannot be added without being looked at:
//
//   - TestWalkAnswersAreNotDropped: a walk answers with what it found and whether that is all. The
//     second part may be thrown away only at the places listed here, each with its reason. The
//     walk that says which functions a called value may be (callTargets) is one of them: its
//     second answer is what else the value may be.
//   - TestCallersThatAskForAnyTargetAreListed: the two helpers that hand on callTargets' list
//     without its second answer may be called only from the places listed here.
//   - TestEveryBoundOnAWalkIsListed: every fixed number that can end a walk or a fixpoint early is
//     listed here with what the code does when it is reached.
//   - TestEveryQuestionIsRead: a check that asks the walks something (state.ask) reads whether the
//     answer was cut at a limit, or is listed here with the reason it need not.
//
// Each fails when the source and its list disagree in either direction, so a list entry cannot
// outlive the code it describes.

// ownSource parses the package's files that are not tests.
func ownSource(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if len(files) < 10 {
		t.Fatalf("parsed %d files of the package; the test is not looking at the package", len(files))
	}
	return fset, files
}

// answersWhetherThatIsAll reports whether a function's results are a list of what a walk found
// and a bool that says the list may not be all: ([]leafValue, bool) or ([]types.Type, bool).
func answersWhetherThatIsAll(ft *ast.FuncType) bool {
	if ft.Results == nil || len(ft.Results.List) != 2 {
		return false
	}
	first, second := ft.Results.List[0], ft.Results.List[1]
	if len(first.Names) != 0 || len(second.Names) != 0 {
		return false
	}
	arr, ok := first.Type.(*ast.ArrayType)
	if !ok || arr.Len != nil {
		return false
	}
	if b, ok := second.Type.(*ast.Ident); !ok || b.Name != "bool" {
		return false
	}
	switch elt := arr.Elt.(type) {
	case *ast.Ident:
		return elt.Name == "leafValue"
	case *ast.SelectorExpr:
		pkg, ok := elt.X.(*ast.Ident)
		return ok && pkg.Name == "types" && elt.Sel.Name == "Type"
	}
	return false
}

// answersWhatElse reports whether a function's results are the functions a called value may be and
// what else it may be: ([]callTarget, []ssa.Value), or ([]lockTarget, bool) for whether the list is
// all of them.
func answersWhatElse(ft *ast.FuncType) bool {
	if ft.Results == nil || len(ft.Results.List) != 2 {
		return false
	}
	first, second := ft.Results.List[0], ft.Results.List[1]
	if len(first.Names) > 1 || len(second.Names) > 1 {
		return false
	}
	arr, ok := first.Type.(*ast.ArrayType)
	if !ok || arr.Len != nil {
		return false
	}
	if elt, ok := arr.Elt.(*ast.Ident); !ok || (elt.Name != "callTarget" && elt.Name != "lockTarget") {
		return false
	}
	switch rest := second.Type.(type) {
	case *ast.Ident:
		return rest.Name == "bool"
	case *ast.ArrayType:
		sel, ok := rest.Elt.(*ast.SelectorExpr)
		if !ok || rest.Len != nil {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "ssa" && sel.Sel.Name == "Value"
	}
	return false
}

// answersInTwoParts is either form: a list, and whether or what the list leaves out.
func answersInTwoParts(ft *ast.FuncType) bool {
	return answersWhetherThatIsAll(ft) || answersWhatElse(ft)
}

func calledName(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// droppedWalkAnswers lists, by the function it is in, every call that throws away a walk's "this
// may not be all", with how many there are and why each is sound.
var droppedWalkAnswers = map[string]struct {
	n   int
	why string
}{
	"fieldBehind":              {1, "the branch reads the stores to a field anywhere in the package and answers incomplete whatever each store's walk says"},
	"elemsBehind":              {2, "it reads the stores to an array's elements, through every name of the array, and answers incomplete whatever the walk of the pointer or of a store says: which element is read is not followed"},
	"fieldByType":              {1, "it reads the stores to a field anywhere in the package and answers incomplete whatever each store's walk says"},
	"looked":                   {1, "a map lookup always answers incomplete: what the map holds is not all in sight"},
	"fieldOfBase":              {2, "the branch reads the stores to a field anywhere in the package, and the struct values stored whole, for a struct whose address has gone out of sight, and has already answered incomplete for that reason"},
	"writtenThroughPointer":    {1, "it reads the functions stored through a pointer anywhere in the package for a variable or field whose address has gone out of sight; its callers have answered incomplete for that reason"},
	"checkSharedLoadWrites":    {1, "the callees of a dynamic call: a callee that is not in sight is not reported, by the rule in docs/RULES.md (SAFE005-incomplete); a walk that was cut is, through the question's cut, which this call site reads"},
	"propagateSharedToMethods": {1, "the receivers of an interface call: as for checkSharedLoadWrites, a cut walk is reported through the question's cut and a receiver that is not in sight is not"},
	"closureCandidates":        {1, "the helper for a caller that asks what any closure a value may be does; its callers are listed, each with its reason, in anyTargetCallers"},
	"lockCallTargets":          {1, "the helper for a caller that asks what any function a call may run does to a lock; its callers are listed, each with its reason, in anyTargetCallers"},
}

func TestWalkAnswersAreNotDropped(t *testing.T) {
	fset, files := ownSource(t)
	walks := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && answersInTwoParts(fd.Type) {
				walks[fd.Name.Name] = true
			}
		}
	}
	for _, name := range []string{"follow", "origins", "resolveInterfaceTypes", "callTargets", "lockCallTargetsAll"} {
		if !walks[name] {
			t.Fatalf("%s is not recognised as a walk; the test no longer sees what it is about", name)
		}
	}
	if len(walks) < 16 {
		t.Fatalf("%d functions that answer in two parts recognised, fewer than the package had when this was written (16: the walk, its entry and its eleven steps, resolveInterfaceTypes, callTargets, lockCallTargetsAll)", len(walks))
	}

	dropped := map[string]int{}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			var stack []ast.Node
			ast.Inspect(fd, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				call, ok := n.(*ast.CallExpr)
				if !ok || !walks[calledName(call)] {
					return true
				}
				at := fset.Position(call.Pos())
				switch parent := stack[len(stack)-2].(type) {
				case *ast.ReturnStmt:
					// Handed up whole: allowed where the function itself answers the same way.
					if len(parent.Results) != 1 || !answersInTwoParts(fd.Type) {
						t.Errorf("%s: %s returns the answer of %s in a function that does not answer whether that is all", at, fd.Name.Name, calledName(call))
					}
				case *ast.AssignStmt:
					if len(parent.Lhs) != 2 || len(parent.Rhs) != 1 {
						t.Errorf("%s: the answer of %s is assigned in a form this test does not know", at, calledName(call))
						return true
					}
					if id, ok := parent.Lhs[1].(*ast.Ident); ok && id.Name == "_" {
						dropped[fd.Name.Name]++
					}
				default:
					t.Errorf("%s: the answer of %s is used in a form this test does not know (%T)", at, calledName(call), parent)
				}
				return true
			})
		}
	}
	for fn, n := range dropped {
		want, listed := droppedWalkAnswers[fn]
		switch {
		case !listed:
			t.Errorf("%s throws away a walk's \"this may not be all\" %d time(s) and is not in droppedWalkAnswers: handle the answer, or list it with the reason it is sound", fn, n)
		case n != want.n:
			t.Errorf("%s throws away a walk's \"this may not be all\" %d time(s), droppedWalkAnswers says %d: look at the new one", fn, n, want.n)
		}
	}
	for fn := range droppedWalkAnswers {
		if dropped[fn] == 0 {
			t.Errorf("droppedWalkAnswers lists %s, which no longer throws an answer away: remove the entry", fn)
		}
	}
}

// anyTargetCallers lists, by the function it is in, every call of closureCandidates and of
// lockCallTargets: the two that hand on the functions a called value may be without what else it
// may be. Each entry says how many calls there are and why what was left out does not change the
// answer there.
var anyTargetCallers = map[string]struct {
	n   int
	why string
}{
	"goStmt":                       {1, "one closure stands for all the value may be, resolved or not: a phi or a cell joins them into one class and the transfer is of the class (TestGoOfVariableThatMayBeTwoClosures)"},
	"checkBorrowEscape":            {1, "asks only whether a closure is in sight before looking for the borrows it captures"},
	"identifyCapturedBorrowsSpawn": {1, "what any closure in sight borrows crosses; when none is found it goes on to read the syntax at the spawn, so nothing rests on the list being all"},
	"checkSharedLoadWrites":        {1, "the closures an argument may be are followed with what they capture; what else the argument may be is a callee that is not in sight, which is not reported (docs/RULES.md, SAFE005-incomplete)"},
	"checkHandlerWrites":           {1, "the closures an argument may be are followed with what they capture; what else the argument may be travels as the argument's own value into the callee's parameter, and the callee's call of that parameter reads callTargets' second answer"},
	"callStep":                     {1, "a go statement: what any closure it may start captures may be unlocked; what else it may start is a call that is not known, which may unlock what it is handed, and the arguments are dropped just before"},
	"hasLockOps":                   {1, "decides only whether the package takes any lock that could be credited; what a function not in sight takes is never credited"},
	"releaseSummaries":             {1, "a summary holds the releases in sight: a release by any function the call may run is one. What else the call may run is, in a summary, as a call nothing is known about"},
	"acquireSummaries":             {1, "decides only whether the function is worth evaluating; the evaluation goes through callStep, which reads whether the list is all"},
	"checkGuardedFunc":             {1, "reports a //gosree:holds helper for an unlock by any function in sight; one that is not in sight cannot be shown to unlock"},
}

func TestCallersThatAskForAnyTargetAreListed(t *testing.T) {
	_, files := ownSource(t)
	helpers := map[string]bool{"closureCandidates": true, "lockCallTargets": true}
	declared := map[string]bool{}
	calls := map[string]int{}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if helpers[fd.Name.Name] {
				declared[fd.Name.Name] = true
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && helpers[calledName(call)] {
					calls[fd.Name.Name]++
				}
				return true
			})
		}
	}
	for name := range helpers {
		if !declared[name] {
			t.Fatalf("%s is not declared; the test no longer sees what it is about", name)
		}
		if _, listed := droppedWalkAnswers[name]; !listed {
			t.Fatalf("%s is not in droppedWalkAnswers: it is here because it drops callTargets' second answer", name)
		}
	}
	for fn, n := range calls {
		want, listed := anyTargetCallers[fn]
		switch {
		case !listed:
			t.Errorf("%s asks %d time(s) for the functions a value may be without what else it may be, and is not in anyTargetCallers: read the second answer (callTargets, lockCallTargetsAll), or list it with the reason it does not matter there", fn, n)
		case n != want.n:
			t.Errorf("%s asks %d time(s) for the functions a value may be without what else it may be, anyTargetCallers says %d: look at the new one", fn, n, want.n)
		}
	}
	for fn := range anyTargetCallers {
		if calls[fn] == 0 {
			t.Errorf("anyTargetCallers lists %s, which no longer asks: remove the entry", fn)
		}
	}
}

// walkBounds lists every fixed number in this package that can end a walk or a fixpoint before it
// is done, as "function: bound", with what the code does when the number is reached.
var walkBounds = map[string]string{
	"follow: originDepth":                  "answers incomplete and marks its question as cut; the checks that asked report SAFE002-incomplete or SAFE005-incomplete",
	"follow: originBudget":                 "as originDepth",
	"anotherName: cellViewLimit":           "the memory is taken as open: the trace answers that what it holds may not be all, and callTargets lists the load among what it could not resolve",
	"spend: cellWalkBudget":                "as cellViewLimit, for the names read by a walk and every keptIn walk it asks for together",
	"rootHere: rootPathLimit":              "the value is lost: the call is taken as one that may release what it was handed, and a write after it to memory that lock guards is reported (TestLockHelperRecursingOnAFieldEnds)",
	"evalValueAsCallable: depth > 6":       "marks its question as cut and answers that no contract was found, so the call is reported as one whose callees could not all be found (not reached today: the nested call passes no arguments, so nesting stops at one)",
	"releaseSummaries: maxReleaseSteps":    "the summary is cut to that many steps and the release is taken as one that may happen: errs toward reporting (docs/RULES.md)",
	"releaseSummaries: maxReleasesPerFn":   "past that many releases the function is taken to release: errs toward reporting (docs/RULES.md)",
	"acquireSummaries: maxAcquireRounds":   "the acquisition is not credited: errs toward reporting (docs/RULES.md)",
	"sharedLoadRoot: for range 16":         "only the search for a reason not to check the write stops (a shareable type, a guarded field); the write is then checked like any other, so past 16 steps the check reports more, never less",
	"sharedRootSeen: rootWalkBudget":       "the walk reports where it stopped (SAFE008-incomplete or SAFE005-incomplete, by the check that asked)",
	"checkHandlerWrites: handlerStepLimit": "the handler is reported as not fully examined (SAFE008-incomplete)",
	"root: globalWalkBudget":               "the walk reports where it stopped (SAFE006-incomplete) and the write is not attributed either way",
	"immReturns: immMaxRounds":             "the result that has not settled is used as it stands and reported (SAFE005-incomplete)",
	"GobDecode: for range 4":               "not a walk: a function fact holds four lists of bits and each is decoded",
}

var boundName = regexp.MustCompile(`(Budget|Depth|Limit|Rounds)$|^max[A-Z]`)

// countedName names the variables a bound is compared with.
var countedName = map[string]bool{"depth": true, "rounds": true, "steps": true, "iter": true, "work": true, "budget": true}

func TestEveryBoundOnAWalkIsListed(t *testing.T) {
	_, files := ownSource(t)
	// Named bounds: constants and variables, of the package or declared inside a function.
	named := map[string]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			gd, ok := n.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				return true
			}
			for _, spec := range gd.Specs {
				for _, n := range spec.(*ast.ValueSpec).Names {
					if boundName.MatchString(n.Name) {
						named[n.Name] = true
					}
				}
			}
			return true
		})
	}
	for _, name := range []string{"originBudget", "originDepth", "maxAcquireRounds", "maxReleaseSteps"} {
		if !named[name] {
			t.Fatalf("%s is not recognised as a bound; the test no longer sees what it is about", name)
		}
	}
	intLit := func(e ast.Expr) (int, bool) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			return 0, false
		}
		v, err := strconv.Atoi(strings.ReplaceAll(lit.Value, "_", ""))
		return v, err == nil
	}
	counted := func(e ast.Expr) (string, bool) {
		if id, ok := e.(*ast.Ident); ok && countedName[id.Name] {
			return id.Name, true
		}
		return "", false
	}
	found := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.RangeStmt:
					if v, ok := intLit(x.X); ok && v >= 2 {
						found[fd.Name.Name+": for range "+strconv.Itoa(v)] = true
					}
				case *ast.ForStmt:
					// A counted loop: for i := 0; i < 16; i++.
					if c, ok := x.Cond.(*ast.BinaryExpr); ok {
						if id, ok := c.X.(*ast.Ident); ok {
							if v, ok := intLit(c.Y); ok && v >= 2 {
								found[fd.Name.Name+": for "+id.Name+" "+c.Op.String()+" "+strconv.Itoa(v)] = true
							}
						}
					}
				case *ast.BinaryExpr:
					switch x.Op {
					case token.GTR, token.GEQ, token.LSS, token.LEQ, token.EQL, token.NEQ:
						// A named bound counts where something is compared with it; a message
						// that names the bound is not a place the walk can stop.
						for _, side := range []ast.Expr{x.X, x.Y} {
							if id, ok := side.(*ast.Ident); ok && named[id.Name] {
								found[fd.Name.Name+": "+id.Name] = true
							}
						}
						if name, ok := counted(x.X); ok && x.Op != token.EQL && x.Op != token.NEQ {
							if v, ok := intLit(x.Y); ok && v >= 2 {
								found[fd.Name.Name+": "+name+" "+x.Op.String()+" "+strconv.Itoa(v)] = true
							}
						}
						// The size of something compared with a written-out number: len(seen) >= 64.
						// Below 8 it is how many arguments or results a call has, not a limit.
						if call, ok := x.X.(*ast.CallExpr); ok && x.Op != token.EQL && x.Op != token.NEQ {
							if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "len" && len(call.Args) == 1 {
								if v, ok := intLit(x.Y); ok && v >= 8 {
									if arg, ok := call.Args[0].(*ast.Ident); ok {
										found[fd.Name.Name+": len("+arg.Name+") "+x.Op.String()+" "+strconv.Itoa(v)] = true
									} else {
										found[fd.Name.Name+": len(...) "+x.Op.String()+" "+strconv.Itoa(v)] = true
									}
								}
							}
						}
					}
				}
				return true
			})
		}
	}
	var missing, stale []string
	for k := range found {
		if _, ok := walkBounds[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range walkBounds {
		if !found[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	for _, k := range missing {
		t.Errorf("a bound is not in walkBounds: %q. Say what the code does when it is reached; if it stops without saying so, that is the defect to fix first", k)
	}
	for _, k := range stale {
		t.Errorf("walkBounds lists %q, which is no longer in the source: remove or rename the entry", k)
	}
}

// unreadQuestions lists, by the function it is in, every question that is asked and whose cut is
// not read there, with how many and why that is sound.
var unreadQuestions = map[string]struct {
	n   int
	why string
}{
	"buildContractIndex":     {1, "the index records what each call in a body was found to publish or load. The graph resolves that same call of that same function again (applyCallContract, checkSharedLoadWrites) and reports a cut there. That the two resolutions are cut together is by reading, not by a test: the index resolves while it is still being built"},
	"returnedValueFreshness": {1, "a fresh result needs every callee to return one, so a cut answer is not a fresh one: a trace that stops at a limit is incomplete and mergeCandidateOutcomes withholds fresh, and a forwarded target with no answer, cut or not, counts against it in evalFunctionOutcome (TestForwardedResultIsFreshOnlyWhenEveryReturnIs, the cases with a target that is not known)"},
}

func TestEveryQuestionIsRead(t *testing.T) {
	_, files := ownSource(t)
	asked, unread := 0, map[string]int{}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Name.Name == "ask" {
				continue
			}
			// The variables questions are bound to in this function, and whether each one's cut is read.
			bound := map[string]bool{}
			var stack []ast.Node
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "cut" {
					if id, ok := sel.X.(*ast.Ident); ok {
						if _, isQuestion := bound[id.Name]; isQuestion {
							bound[id.Name] = true
						}
					}
				}
				call, ok := n.(*ast.CallExpr)
				if !ok || calledName(call) != "ask" || len(call.Args) != 0 {
					return true
				}
				asked++
				var parent ast.Node
				if len(stack) >= 2 {
					parent = stack[len(stack)-2]
				}
				if as, ok := parent.(*ast.AssignStmt); ok && len(as.Lhs) == 1 && len(as.Rhs) == 1 {
					if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
						if _, again := bound[id.Name]; !again {
							bound[id.Name] = false
						}
						return true
					}
				}
				// Asked and used on the spot: its cut cannot be read.
				unread[fd.Name.Name]++
				return true
			})
			for _, read := range bound {
				if !read {
					unread[fd.Name.Name]++
				}
			}
		}
	}
	if asked < 5 {
		t.Fatalf("%d questions found in the source, fewer than when this was written (5); the test no longer sees what it is about", asked)
	}
	for fn, n := range unread {
		want, listed := unreadQuestions[fn]
		switch {
		case !listed:
			t.Errorf("%s asks a question %d time(s) without reading whether the answer was cut, and is not in unreadQuestions: read it, or list it with the reason it need not", fn, n)
		case n != want.n:
			t.Errorf("%s asks %d question(s) without reading cut, unreadQuestions says %d: look at the new one", fn, n, want.n)
		}
	}
	for fn := range unreadQuestions {
		if unread[fn] == 0 {
			t.Errorf("unreadQuestions lists %s, which no longer asks without reading: remove the entry", fn)
		}
	}
}
