package flow

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// Reachability, §14.22, computed together with definite assignment because
// they need the same walk and the same fixpoint.
//
// Every statement method returns whether the statement can complete normally.
// The caller uses that to decide whether the next statement is reachable, and
// reports the ones that are not — in Java an unreachable statement is an
// error, not a warning, which is why this is here and not in warn.
//
// The state is threaded by pointer so a statement's assignments are visible to
// what follows it. A branch that clones does so explicitly.

// block analyses a statement list, reporting unreachable statements as it
// goes. Returns whether control can reach the closing brace.
func (cx *ctx) block(b *ast.Block, st state) bool {
	if b == nil {
		return true
	}
	alive := true
	for _, s := range b.Stmts {
		if !alive {
			// One report per block: the first unreachable statement is the
			// useful one, and marking the rest adds nothing a user can act on.
			cx.a.errorf(s.Pos(), s.End(), "unreachable statement")
			cx.a.out.Unreachable[s] = true
			// Keep walking so errors inside it are still found, but do not
			// let its assignments affect the state that follows.
			cx.stmt(s, st.clone())
			continue
		}
		alive = cx.stmt(s, st)
	}
	return alive
}

// stmt analyses one statement, mutating st, and reports whether control can
// fall out the bottom.
func (cx *ctx) stmt(s ast.Stmt, st state) bool {
	switch n := s.(type) {
	case *ast.Block:
		inner := st
		return cx.block(n, inner)

	case *ast.EmptyStmt, *ast.BadStmt:
		return true

	case *ast.ExprStmt:
		cx.expr(n.X, st)
		return true

	case *ast.DeclStmt:
		return cx.declStmt(n, st)

	case *ast.LabeledStmt:
		// A labelled statement completes normally if its body does, or if any
		// break targets the label. Tracking which breaks name which label is
		// what the labels stack does; the conservative answer here is true,
		// because a labelled block that cannot be broken out of is rare and
		// the cost of being wrong is a missed error, not a wrong one.
		cx.stmt(n.Stmt, st)
		return true

	case *ast.IfStmt:
		return cx.ifStmt(n, st)

	case *ast.WhileStmt:
		return cx.whileStmt(n, st)

	case *ast.DoStmt:
		return cx.doStmt(n, st)

	case *ast.ForStmt:
		return cx.forStmt(n, st)

	case *ast.RangeStmt:
		return cx.rangeStmt(n, st)

	case *ast.SwitchStmt:
		return cx.switchStmt(n, st)

	case *ast.BreakStmt, *ast.ContinueStmt:
		// Neither completes normally: control leaves for the loop's exit or
		// its next iteration.
		return false

	case *ast.ReturnStmt:
		if n.Result != nil {
			cx.expr(n.Result, st)
		}
		cx.checkBlankFinals(n.Pos(), n.End(), st)
		return false

	case *ast.YieldStmt:
		cx.expr(n.X, st)
		return false

	case *ast.ThrowStmt:
		cx.expr(n.X, st)
		cx.throwType(n.X, n.Pos(), n.End())
		return false

	case *ast.SyncStmt:
		cx.expr(n.X, st)
		return cx.block(n.Body, st)

	case *ast.AssertStmt:
		// An assertion may be disabled at runtime, so §14.10 makes it
		// complete normally regardless of its condition, and nothing it
		// assigns is definitely assigned afterwards.
		c := cx.condition(n.X, st)
		if n.Msg != nil {
			cx.expr(n.Msg, c.whenFalse)
		}
		return true

	case *ast.TryStmt:
		return cx.tryStmt(n, st)

	case *ast.ConstructorCall:
		for _, arg := range n.Args {
			cx.expr(arg, st)
		}
		cx.constructorThrows(n)
		return true
	}
	return true
}

func (cx *ctx) declStmt(n *ast.DeclStmt, st state) bool {
	switch d := n.Decl.(type) {
	case *ast.VarDecl:
		cx.localVars(d, st)
	case *ast.ClassDecl, *ast.InterfaceDecl, *ast.EnumDecl, *ast.RecordDecl:
		// A local class body is its own analysis space, but its captures
		// belong to the enclosing method.
		if c := cx.a.info.Local[d]; c != nil {
			cx.a.classIn(c, cx)
		}
	}
	return true
}

// localVars declares each local and records whether it starts assigned.
func (cx *ctx) localVars(d *ast.VarDecl, st state) {
	for _, decl := range d.Names {
		v, _ := cx.a.info.Use(decl).(*symVar)
		if v == nil {
			continue
		}
		blank := decl.Init == nil
		i := cx.declare(v, blank)
		st.da.grow(i)
		st.du.grow(i)

		if decl.Init != nil {
			cx.initializer(decl.Init, st)
			st.assign(i)
			cx.writes[v]++
		} else {
			st.da.clear(i)
			st.du.set(i)
		}
	}
}

func (cx *ctx) ifStmt(n *ast.IfStmt, st state) bool {
	c := cx.condition(n.Cond, st)

	// §14.21 exempts `if` from constant-condition unreachability on purpose:
	// `if (DEBUG) { ... }` with a static final false must keep compiling, so
	// neither branch is ever unreachable no matter what the condition folds to.
	thenState := c.whenTrue
	thenAlive := cx.stmt(n.Then, thenState)

	if n.Else == nil {
		merged := join(thenState, c.whenFalse)
		copyInto(st, merged)
		return true
	}
	elseState := c.whenFalse
	elseAlive := cx.stmt(n.Else, elseState)

	switch {
	case thenAlive && elseAlive:
		copyInto(st, join(thenState, elseState))
	case thenAlive:
		copyInto(st, thenState)
	case elseAlive:
		copyInto(st, elseState)
	}
	return thenAlive || elseAlive
}

func (cx *ctx) whileStmt(n *ast.WhileStmt, st state) bool {
	c := cx.condition(n.Cond, st)
	entry := c.whenTrue

	// The body runs an unknown number of times, so its entry state is the
	// fixpoint of (entry ∧ whatever the body leaves). Iterating to a fixpoint
	// rather than analysing once is what makes a variable assigned only in a
	// later iteration correctly *not* definitely assigned.
	for {
		body := entry.clone()
		cx.stmt(n.Body, body)
		next := join(entry, body)
		if next.equal(entry) {
			break
		}
		entry = next
	}
	cx.stmt(n.Body, entry.clone())

	// §14.21: while(true) with no reachable break cannot complete normally.
	if cx.isConstTrue(n.Cond) {
		if !cx.hasBreak(n.Body) {
			return false
		}
		copyInto(st, entry)
		return true
	}
	copyInto(st, join(st, c.whenFalse))
	return true
}

func (cx *ctx) doStmt(n *ast.DoStmt, st state) bool {
	// The body always runs at least once, so what it assigns *is* definitely
	// assigned at the condition — the one loop shape where that holds.
	alive := cx.stmt(n.Body, st)
	c := cx.condition(n.Cond, st)

	entry := st.clone()
	for {
		body := entry.clone()
		cx.stmt(n.Body, body)
		next := join(entry, body)
		if next.equal(entry) {
			break
		}
		entry = next
	}
	if cx.isConstTrue(n.Cond) && !cx.hasBreak(n.Body) {
		return false
	}
	copyInto(st, c.whenFalse)
	return alive || true
}

func (cx *ctx) forStmt(n *ast.ForStmt, st state) bool {
	for _, init := range n.Init {
		cx.stmt(init, st)
	}
	// An absent condition is `true` (§14.14.1.1), which is how `for(;;)`
	// becomes an infinite loop.
	infinite := n.Cond == nil || cx.isConstTrue(n.Cond)

	var c cond
	if n.Cond != nil {
		c = cx.condition(n.Cond, st)
	} else {
		c = unconditional(st)
	}

	entry := c.whenTrue
	for {
		body := entry.clone()
		cx.stmt(n.Body, body)
		for _, p := range n.Post {
			cx.expr(p, body)
		}
		next := join(entry, body)
		if next.equal(entry) {
			break
		}
		entry = next
	}
	cx.stmt(n.Body, entry.clone())

	if infinite && !cx.hasBreak(n.Body) {
		return false
	}
	copyInto(st, c.whenFalse)
	return true
}

func (cx *ctx) rangeStmt(n *ast.RangeStmt, st state) bool {
	cx.expr(n.X, st)
	if n.Decl != nil {
		cx.localVars(n.Decl, st)
	}
	// The sequence may be empty, so nothing the body assigns is definitely
	// assigned afterwards, and a for-each always completes normally.
	entry := st.clone()
	cx.stmt(n.Body, entry)
	return true
}

// switchStmt handles both the arrow and colon forms; ast populates exactly one
// of Rules and Groups, and they cannot be mixed.
func (cx *ctx) switchStmt(n *ast.SwitchStmt, st state) bool {
	if n.Block == nil {
		return true
	}
	cx.expr(n.Tag, st)

	hasDefault := false
	var ends []state
	alive := false

	for _, r := range n.Block.Rules {
		if r.Label != nil && r.Label.Default {
			hasDefault = true
		}
		s := st.clone()
		cx.switchLabel(r.Label, s)
		// An arrow rule never falls through, which is the whole point of the
		// form: each is independent and each completes on its own.
		switch b := r.Body.(type) {
		case *ast.Block:
			if cx.block(b, s) {
				alive = true
				ends = append(ends, s)
			}
		case ast.Expr:
			cx.expr(b, s)
			alive = true
			ends = append(ends, s)
		case *ast.ThrowStmt:
			cx.stmt(b, s)
		}
	}

	for _, g := range n.Block.Groups {
		for _, l := range g.Labels {
			if l.Default {
				hasDefault = true
			}
			cx.switchLabel(l, st)
		}
		// A colon group falls through into the next, so the state carries
		// across rather than resetting.
		s := st.clone()
		groupAlive := true
		for _, gs := range g.Stmts {
			if !groupAlive {
				cx.a.errorf(gs.Pos(), gs.End(), "unreachable statement")
				cx.a.out.Unreachable[gs] = true
				break
			}
			groupAlive = cx.stmt(gs, s)
		}
		if groupAlive {
			alive = true
			ends = append(ends, s)
		}
	}

	// Without a default the switch can be skipped entirely, so it always
	// completes normally and nothing inside it is definitely assigned after.
	if !hasDefault {
		return true
	}
	if len(ends) > 0 {
		merged := ends[0]
		for _, e := range ends[1:] {
			merged = join(merged, e)
		}
		copyInto(st, merged)
	}
	return alive
}

func (cx *ctx) switchLabel(l *ast.SwitchLabel, st state) {
	if l == nil {
		return
	}
	for _, c := range l.Cases {
		if x, ok := c.(ast.Expr); ok {
			cx.expr(x, st)
		}
	}
	if l.Guard != nil {
		cx.expr(l.Guard, st)
	}
}

// isConstTrue reports whether a condition folded to the constant true. attr
// already did the folding; §14.21's rules turn entirely on whether the value
// is a *constant expression*, not on whether it happens to be true at runtime.
func (cx *ctx) isConstTrue(x ast.Expr) bool {
	k, ok := cx.a.info.Const(x)
	if !ok {
		return false
	}
	b, isBool := k.Value.(bool)
	return isBool && b
}

func (cx *ctx) isConstFalse(x ast.Expr) bool {
	k, ok := cx.a.info.Const(x)
	if !ok {
		return false
	}
	b, isBool := k.Value.(bool)
	return isBool && !b
}

// hasBreak reports whether a statement contains a break that could target the
// enclosing loop. A break inside a nested loop or switch targets that one
// instead, so the walk does not descend into them — which is exactly the
// distinction that makes `while(true) { switch(x) { case 1: break; } }` an
// infinite loop.
func (cx *ctx) hasBreak(s ast.Stmt) bool {
	found := false
	var walk func(n ast.Node, depth int)
	walk = func(n ast.Node, depth int) {
		if found || n == nil {
			return
		}
		switch t := n.(type) {
		case *ast.BreakStmt:
			if depth == 0 || t.Label != nil {
				found = true
			}
			return
		case *ast.WhileStmt, *ast.DoStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt:
			depth++
		case *ast.LambdaExpr, *ast.ClassDecl, *ast.InterfaceDecl,
			*ast.EnumDecl, *ast.RecordDecl:
			// A break cannot cross a method or class boundary.
			return
		}
		ast.Inspect(n, func(c ast.Node) bool {
			if c == n {
				return true
			}
			walk(c, depth)
			return false
		})
	}
	walk(s, 0)
	return found
}

// copyInto overwrites dst's bits with src's, so a caller holding a state by
// value sees what a branch concluded.
func copyInto(dst, src state) {
	dst.da.w = dst.da.w[:0]
	dst.da.w = append(dst.da.w, src.da.w...)
	dst.du.w = dst.du.w[:0]
	dst.du.w = append(dst.du.w, src.du.w...)
}

// checkBlankFinals reports a blank final that is not definitely assigned where
// it must be. §8.3.1.2 requires it at the end of every constructor.
func (cx *ctx) checkBlankFinals(pos, end token.Pos, st state) {
	for i, v := range cx.order {
		if cx.blanks[i] && !st.da.has(i) {
			cx.a.errorf(pos, end, "variable %s might not have been initialized", v.Name)
		}
	}
}

var _ = types.KindVoid
var _ = token.NoPos