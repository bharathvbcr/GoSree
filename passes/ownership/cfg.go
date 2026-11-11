package ownership

import (
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

func indexIn(ins ssa.Instruction) int {
	for i, x := range ins.Block().Instrs {
		if x == ins {
			return i
		}
	}
	return -1
}

func reachableBlockMask(fn *ssa.Function, roots []*ssa.BasicBlock) []bool {
	if fn == nil {
		return nil
	}
	seen := make([]bool, len(fn.Blocks))
	var stackBuf [16]*ssa.BasicBlock
	stack := stackBuf[:0]
	for _, r := range roots {
		if r != nil && r.Index >= 0 && r.Index < len(seen) && !seen[r.Index] {
			seen[r.Index] = true
			stack = append(stack, r)
		}
	}
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, succ := range b.Succs {
			if succ.Index >= 0 && succ.Index < len(seen) && !seen[succ.Index] {
				seen[succ.Index] = true
				stack = append(stack, succ)
			}
		}
	}
	return seen
}

func blockInMask(seen []bool, b *ssa.BasicBlock) bool {
	return b != nil && b.Index >= 0 && b.Index < len(seen) && seen[b.Index]
}

// reachableAfter returns a predicate reporting whether an instruction can execute after s on some
// path (including later iterations of a loop that contains s).
func reachableAfter(s ssa.Instruction) func(ssa.Instruction) bool {
	start := s.Block()
	sIdx := indexIn(s)
	seen := reachableBlockMask(start.Parent(), start.Succs)
	return func(t ssa.Instruction) bool {
		if blockInMask(seen, t.Block()) {
			return true
		}
		return t.Block() == start && indexIn(t) > sIdx
	}
}

func reachableBlocksForTransfer(t transfer) []bool {
	if t.caseBlock != nil {
		return reachableBlockMask(t.caseBlock.Parent(), []*ssa.BasicBlock{t.caseBlock})
	}
	start := t.instr.Block()
	return reachableBlockMask(start.Parent(), start.Succs)
}

func reachableAfterTransferWithMask(g *graph, t transfer, seen []bool) func(ssa.Instruction) bool {
	if t.caseBlock != nil {
		return func(target ssa.Instruction) bool {
			return blockInMask(seen, target.Block())
		}
	}
	start := t.instr.Block()
	sIdx := g.idxOf(t.instr)
	return func(target ssa.Instruction) bool {
		if blockInMask(seen, target.Block()) {
			return true
		}
		return target.Block() == start && g.idxOf(target) > sIdx
	}
}

func reachableAfterTransfer(t transfer) func(ssa.Instruction) bool {
	return reachableAfterTransferWithMask(nil, t, reachableBlocksForTransfer(t))
}

func usesValue(ins ssa.Instruction, w ssa.Value) bool {
	var buf [8]*ssa.Value
	for _, op := range ins.Operands(buf[:0]) {
		if op != nil && *op == w {
			return true
		}
	}
	return false
}

// syncNames are calls after which another goroutine's earlier writes are visible.
var syncNames = map[string]bool{
	"(*sync.WaitGroup).Wait": true,
	"(*sync.Mutex).Lock":     true,
	"(*sync.RWMutex).Lock":   true,
	"(*sync.RWMutex).RLock":  true,
	"(*sync.Cond).Wait":      true,
}

func isSync(ins ssa.Instruction) bool {
	switch ins := ins.(type) {
	case *ssa.UnOp:
		return ins.Op == token.ARROW
	case *ssa.Select:
		return true
	case *ssa.Call:
		if fn := ins.Common().StaticCallee(); fn != nil {
			if obj, ok := fn.Object().(*types.Func); ok {
				return syncNames[obj.FullName()]
			}
		}
	}
	return false
}

type use struct {
	at     ssa.Instruction
	pos    token.Pos
	synced bool
}

// useAfter finds a use of w that can execute after s without w being redefined first. It prefers a
// path with no synchronisation (channel receive, select, WaitGroup.Wait, Lock) between s and the use;
// if every such use is preceded by synchronisation, the returned use has synced set. A join (see
// forkjoin.go) hands the moved memory back, so paths stop there like at a redefinition.
func useAfter(s ssa.Instruction, cb *ssa.BasicBlock, w ssa.Value, joins map[ssa.Instruction]bool) (use, bool) {
	return useAfterForGraph(nil, s, cb, w, joins, nil)
}

func hasReachableReferrer(g *graph, s ssa.Instruction, cb *ssa.BasicBlock, w ssa.Value, reachMask []bool) bool {
	refs := w.Referrers()
	if refs == nil {
		return true
	}
	if len(*refs) == 0 {
		return false
	}
	sBlock := s.Block()
	sIdx := g.idxOf(s)
	for _, ref := range *refs {
		if _, dbg := ref.(*ssa.DebugRef); dbg {
			continue
		}
		if phi, ok := ref.(*ssa.Phi); ok {
			preds := phi.Block().Preds
			for pi, edge := range phi.Edges {
				if edge != w || pi >= len(preds) {
					continue
				}
				pred := preds[pi]
				if cb != nil {
					if blockInMask(reachMask, pred) {
						return true
					}
				} else if pred == sBlock || blockInMask(reachMask, pred) {
					return true
				}
			}
			continue
		}
		rb := ref.Block()
		if cb != nil {
			if blockInMask(reachMask, rb) {
				return true
			}
		} else if blockInMask(reachMask, rb) || (rb == sBlock && g.idxOf(ref) > sIdx) {
			return true
		}
	}
	return false
}

func useAfterForGraph(g *graph, s ssa.Instruction, cb *ssa.BasicBlock, w ssa.Value, joins map[ssa.Instruction]bool, reachMask []bool) (use, bool) {
	def, _ := w.(ssa.Instruction)
	fn := s.Parent()
	startBlock := s.Block()
	if cb != nil {
		startBlock = cb
	}
	// By SSA dominance, every use of an instruction w is dominated by def; if def has not yet
	// executed on all paths reaching the search start, any forward path to a use must hit def first.
	if def != nil && def.Block() != nil && fn != nil && startBlock != fn.Recover {
		if cb != nil {
			if def.Block() == cb || !def.Block().Dominates(cb) {
				return use{}, false
			}
		} else if def != s && !g.dominates(def, s) {
			return use{}, false
		}
	}
	if reachMask != nil && !hasReachableReferrer(g, s, cb, w, reachMask) {
		return use{}, false
	}

	var found *use
	var visited []uint8
	var visitedFallback map[*ssa.BasicBlock]uint8
	if fn != nil {
		visited = make([]uint8, len(fn.Blocks))
	}
	markVisited := func(b *ssa.BasicBlock, synced bool) bool {
		bit := uint8(1)
		if synced {
			bit = 2
		}
		if b.Index >= 0 && b.Index < len(visited) {
			if visited[b.Index]&bit != 0 {
				return false
			}
			visited[b.Index] |= bit
			return true
		}
		if visitedFallback == nil {
			visitedFallback = map[*ssa.BasicBlock]uint8{}
		}
		if visitedFallback[b]&bit != 0 {
			return false
		}
		visitedFallback[b] |= bit
		return true
	}

	type item struct {
		b      *ssa.BasicBlock
		from   int
		synced bool
	}
	var qBuf [16]item
	queue := qBuf[:0]
	if cb != nil {
		queue = append(queue, item{cb, 0, false})
	} else {
		queue = append(queue, item{s.Block(), g.idxOf(s) + 1, false})
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		synced := it.synced
		killed := false
		for i := it.from; i < len(it.b.Instrs); i++ {
			ins := it.b.Instrs[i]
			if ins == def {
				killed = true
				break
			}
			if _, isPhi := ins.(*ssa.Phi); !isPhi {
				if _, dbg := ins.(*ssa.DebugRef); !dbg && usesValue(ins, w) {
					u := use{at: ins, pos: posOfWithGraph(g, ins, s), synced: synced}
					if !synced {
						return u, true
					}
					if found == nil {
						found = &u
					}
					killed = true
					break
				}
			}
			if joins[ins] {
				if _, isIf := ins.(*ssa.If); !isIf {
					killed = true
					break
				}
			}
			if isSync(ins) {
				synced = true
			}
		}
		if killed {
			continue
		}
		for si, succ := range it.b.Succs {
			if iff, ok := lastInstr(it.b).(*ssa.If); ok && joins[iff] {
				_, negated := unwrapBoolCond(iff.Cond)
				if (si == 0) != negated {
					continue
				}
			}
			// A phi in the successor reads w on the edge from this block.
			for pi, pred := range succ.Preds {
				if pred != it.b {
					continue
				}
				for _, ins := range succ.Instrs {
					phi, ok := ins.(*ssa.Phi)
					if !ok {
						break
					}
					if phi.Edges[pi] == w {
						u := use{at: phi, pos: posOfWithGraph(g, lastInstr(it.b), s), synced: synced}
						if !synced {
							return u, true
						}
						if found == nil {
							found = &u
						}
					}
				}
			}
			if markVisited(succ, synced) {
				queue = append(queue, item{succ, 0, synced})
			}
		}
	}
	if found != nil {
		return *found, true
	}
	return use{}, false
}

func lastInstr(b *ssa.BasicBlock) ssa.Instruction { return b.Instrs[len(b.Instrs)-1] }

func posOfWithGraph(g *graph, ins ssa.Instruction, fallback ssa.Instruction) token.Pos {
	if p := ins.Pos(); p.IsValid() {
		return p
	}
	if b := ins.Block(); b != nil {
		for _, x := range b.Instrs[g.idxOf(ins)+1:] {
			if p := x.Pos(); p.IsValid() {
				return p
			}
		}
	}
	return fallback.Pos()
}

// posOf finds a source position for ins: its own, else the next positioned instruction in its block,
// else fallback's.
func posOf(ins ssa.Instruction, fallback ssa.Instruction) token.Pos {
	return posOfWithGraph(nil, ins, fallback)
}

// reachableWithoutKill reports whether target can execute after start along some CFG path that
// does not execute kill first. When kill is a heap *ssa.Alloc, executing kill allocates a fresh
// cell, so subsequent stores to kill do not mutate the earlier cell instance.
func reachableWithoutKill(start, target, kill ssa.Instruction) bool {
	sb := start.Block()
	sIdx := indexIn(start)
	for i := sIdx + 1; i < len(sb.Instrs); i++ {
		ins := sb.Instrs[i]
		if ins == kill {
			return false
		}
		if ins == target {
			return true
		}
	}
	fn := sb.Parent()
	var visited []bool
	if fn != nil {
		visited = make([]bool, len(fn.Blocks))
	}
	var stackBuf [16]*ssa.BasicBlock
	stack := stackBuf[:0]
	for _, succ := range sb.Succs {
		if succ.Index >= 0 && succ.Index < len(visited) && !visited[succ.Index] {
			visited[succ.Index] = true
			stack = append(stack, succ)
		}
	}
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		killed := false
		for _, ins := range b.Instrs {
			if ins == kill {
				killed = true
				break
			}
			if ins == target {
				return true
			}
		}
		if killed {
			continue
		}
		for _, succ := range b.Succs {
			if succ.Index >= 0 && succ.Index < len(visited) && !visited[succ.Index] {
				visited[succ.Index] = true
				stack = append(stack, succ)
			}
		}
	}
	return false
}

// finalCells returns the captured variable cells (heap Allocs, and Phis selecting exclusively
// among such heap Allocs, such as Go 1.22+ 3-clause per-iteration loop cells) that hold a
// shareable value and are never written on the same cell instance after a closure captures them.
func finalCells(st *state, fn *ssa.Function) map[ssa.Value]bool {
	out := map[ssa.Value]bool{}
	cand := map[ssa.Value]bool{}
	var phis []*ssa.Phi
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			switch x := ins.(type) {
			case *ssa.Alloc:
				if !x.Heap {
					continue
				}
				if ptr, ok := x.Type().Underlying().(*types.Pointer); ok && st.cls.Shareable(ptr.Elem()) {
					cand[x] = true
				}
			case *ssa.Phi:
				if ptr, ok := x.Type().Underlying().(*types.Pointer); ok && len(x.Edges) > 0 && st.cls.Shareable(ptr.Elem()) {
					cand[x] = true
					phis = append(phis, x)
				}
			}
		}
	}
	if len(cand) == 0 {
		return out
	}
	for changed := true; changed; {
		changed = false
		for _, phi := range phis {
			if !cand[phi] {
				continue
			}
			for _, e := range phi.Edges {
				if !cand[e] {
					delete(cand, phi)
					changed = true
					break
				}
			}
		}
	}

	adj := map[ssa.Value][]ssa.Value{}
	var members []ssa.Value
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			v, ok := ins.(ssa.Value)
			if !ok || !cand[v] {
				continue
			}
			members = append(members, v)
			if phi, isPhi := v.(*ssa.Phi); isPhi {
				for _, e := range phi.Edges {
					adj[phi] = append(adj[phi], e)
					adj[e] = append(adj[e], phi)
				}
			}
		}
	}

	seen := map[ssa.Value]bool{}
	for _, root := range members {
		if seen[root] {
			continue
		}
		comp := []ssa.Value{root}
		inComp := map[ssa.Value]bool{root: true}
		seen[root] = true
		for head := 0; head < len(comp); head++ {
			cur := comp[head]
			for _, nb := range adj[cur] {
				if !seen[nb] {
					seen[nb] = true
					inComp[nb] = true
					comp = append(comp, nb)
				}
			}
		}
		if cellGroupIsFinal(comp, inComp) {
			for _, v := range comp {
				out[v] = true
			}
		}
	}
	return out
}

func cellGroupIsFinal(comp []ssa.Value, inComp map[ssa.Value]bool) bool {
	var captures []*ssa.MakeClosure
	var stores []*ssa.Store
	for _, v := range comp {
		refs := v.Referrers()
		if refs == nil {
			return false
		}
		for _, r := range *refs {
			switch r := r.(type) {
			case *ssa.MakeClosure:
				captures = append(captures, r)
				for j, bnd := range r.Bindings {
					if bnd == v && freeVarWritten(r.Fn.(*ssa.Function).FreeVars[j]) {
						return false
					}
				}
			case *ssa.Store:
				if r.Addr != v {
					return false
				}
				stores = append(stores, r)
			case *ssa.UnOp:
				if r.Op != token.MUL {
					return false
				}
			case *ssa.Phi:
				if !inComp[r] {
					return false
				}
			case *ssa.DebugRef:
			default:
				// The variable's address goes somewhere that may write through it.
				return false
			}
		}
	}
	if len(captures) == 0 {
		return false
	}
	for _, mc := range captures {
		var after func(ssa.Instruction) bool
		for _, s := range stores {
			if alloc, isAlloc := s.Addr.(*ssa.Alloc); isAlloc {
				if reachableWithoutKill(mc, s, alloc) {
					return false
				}
			} else {
				if after == nil {
					after = reachableAfter(mc)
				}
				if after(s) {
					return false
				}
			}
		}
	}
	return true
}

func freeVarWritten(fv *ssa.FreeVar) bool {
	for _, r := range *fv.Referrers() {
		switch r := r.(type) {
		case *ssa.UnOp:
			if r.Op != token.MUL {
				return true
			}
		case *ssa.MakeClosure:
			for j, bnd := range r.Bindings {
				if bnd == fv && freeVarWritten(r.Fn.(*ssa.Function).FreeVars[j]) {
					return true
				}
			}
		case *ssa.DebugRef:
		default:
			return true
		}
	}
	return false
}

// closureIsGoroutineBody reports whether fn is an anonymous function whose every closure value is
// started directly by a go statement or by a one-shot callback spawner (time.AfterFunc,
// WaitGroup.Go). Such a closure owns what it captured: the parent's transfer check proves the parent
// no longer touches it. A handler is never one: its invocations run concurrently with each other.
func closureIsGoroutineBody(fn *ssa.Function) bool {
	parent := fn.Parent()
	if parent == nil || len(fn.FreeVars) == 0 {
		return false
	}
	seen := false
	for _, b := range parent.Blocks {
		for _, ins := range b.Instrs {
			mc, ok := ins.(*ssa.MakeClosure)
			if !ok || mc.Fn != fn {
				continue
			}
			seen = true
			for _, r := range *mc.Referrers() {
				if _, dbg := r.(*ssa.DebugRef); dbg {
					continue
				}
				sp, ok := spawnOf(r)
				if !ok || sp.fn != mc || sp.repeated || usedAsArg(r, mc, sp.arg) {
					return false
				}
			}
		}
	}
	return seen
}
