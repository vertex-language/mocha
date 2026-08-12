package flow

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
)

// Definite assignment, §16.
//
// Two facts per variable, and they are not complements. "Definitely assigned"
// gates a read; "definitely unassigned" gates an assignment to a blank final.
// A variable may be neither — after one arm of an if-else assigns it and the
// other does not — and that state makes both operations errors, which is
// exactly what the language intends.
//
// The subtle half is boolean expressions. A condition yields two states rather
// than one, because &&, ||, ! and ?: all distribute assignment facts
// differently along the true and false paths. Without that split the idiom
// every Java program uses fails:
//
//	int x;
//	if (cond && (x = f()) > 0) { use(x); }
//
// Here x is definitely assigned only when the condition was true, and only the
// two-state form can say so.

// expr walks an expression for its assignment effects and its reads.
func (cx *ctx) expr(x ast.Expr, st state) {
	if x == nil {
		return
	}
	switch n := x.(type) {
	case *ast.Ident:
		cx.read(x, st)

	case *ast.Name:
		cx.read(x, st)

	case *ast.ParenExpr:
		cx.expr(n.X, st)

	case *ast.AssignExpr:
		cx.assignExpr(n, st)

	case *ast.UnaryExpr:
		if n.Op == token.INC || n.Op == token.DEC {
			cx.read(n.X, st)
			cx.write(n.X, st)
			return
		}
		cx.expr(n.X, st)

	case *ast.PostfixExpr:
		cx.read(n.X, st)
		cx.write(n.X, st)

	case *ast.BinaryExpr:
		// The short-circuit operators do not evaluate the right side
		// unconditionally, so they go through condition() to keep the two
		// states apart. Everything else evaluates both.
		if n.Op == token.LAND || n.Op == token.LOR {
			cx.condition(x, st)
			return
		}
		cx.expr(n.X, st)
		cx.expr(n.Y, st)

	case *ast.CondExpr:
		c := cx.condition(n.Cond, st)
		t := c.whenTrue
		e := c.whenFalse
		cx.expr(n.Then, t)
		cx.expr(n.Else, e)
		copyInto(st, join(t, e))

	case *ast.InstanceOfExpr:
		cx.expr(n.X, st)
		if n.Pattern != nil {
			cx.patternBindings(n.Pattern, st)
		}

	case *ast.CallExpr:
		if n.X != nil {
			cx.expr(n.X, st)
		}
		for _, arg := range n.Args {
			cx.expr(arg, st)
		}
		cx.callThrows(n)

	case *ast.NewExpr:
		if n.Outer != nil {
			cx.expr(n.Outer, st)
		}
		for _, arg := range n.Args {
			cx.expr(arg, st)
		}
		cx.callThrows(n)
		if n.Body != nil {
			// An anonymous class body is a separate analysis space whose
			// captures belong to this method.
			cx.a.anonymousIn(n, cx)
		}

	case *ast.NewArrayExpr:
		for _, d := range n.DimExprs {
			cx.expr(d.X, st)
		}
		if n.Init != nil {
			cx.initializer(n.Init, st)
		}

	case *ast.ArrayInit:
		cx.initializer(n, st)

	case *ast.IndexExpr:
		cx.expr(n.X, st)
		cx.expr(n.Index, st)

	case *ast.SelectorExpr:
		cx.expr(n.X, st)

	case *ast.CastExpr:
		cx.expr(n.X, st)

	case *ast.LambdaExpr:
		cx.lambda(n, st)

	case *ast.MethodRef:
		if e, ok := n.X.(ast.Expr); ok {
			cx.expr(e, st)
		}

	case *ast.SwitchExpr:
		cx.switchExpr(n, st)
	}
}

func (cx *ctx) initializer(init ast.Node, st state) {
	switch n := init.(type) {
	case ast.Expr:
		cx.expr(n, st)
	case *ast.ArrayInit:
		for _, e := range n.Elts {
			cx.initializer(e, st)
		}
	}
}

// condition evaluates a boolean expression, returning the assignment facts
// that hold on each branch. §16.1.
func (cx *ctx) condition(x ast.Expr, st state) cond {
	if x == nil {
		return unconditional(st)
	}
	switch n := x.(type) {
	case *ast.ParenExpr:
		return cx.condition(n.X, st)

	case *ast.UnaryExpr:
		if n.Op == token.NOT {
			// §16.1.4: negation swaps the two states.
			c := cx.condition(n.X, st)
			return cond{whenTrue: c.whenFalse, whenFalse: c.whenTrue}
		}

	case *ast.BinaryExpr:
		switch n.Op {
		case token.LAND:
			// §16.1.2: the right side is evaluated only when the left was
			// true, so whenTrue accumulates both and whenFalse is the merge
			// of "left false" and "left true, right false".
			l := cx.condition(n.X, st)
			r := cx.condition(n.Y, l.whenTrue)
			return cond{
				whenTrue:  r.whenTrue,
				whenFalse: join(l.whenFalse, r.whenFalse),
			}
		case token.LOR:
			// §16.1.3, the mirror: the right side runs only when the left was
			// false.
			l := cx.condition(n.X, st)
			r := cx.condition(n.Y, l.whenFalse)
			return cond{
				whenTrue:  join(l.whenTrue, r.whenTrue),
				whenFalse: r.whenFalse,
			}
		}

	case *ast.CondExpr:
		// §16.1.5.
		c := cx.condition(n.Cond, st)
		t := cx.condition(n.Then, c.whenTrue)
		e := cx.condition(n.Else, c.whenFalse)
		return cond{
			whenTrue:  join(t.whenTrue, e.whenTrue),
			whenFalse: join(t.whenFalse, e.whenFalse),
		}

	case *ast.InstanceOfExpr:
		cx.expr(n.X, st)
		out := unconditional(st)
		if n.Pattern != nil {
			// A pattern binding is definitely assigned exactly where the
			// instanceof is true. That asymmetry is the reason patterns need
			// the two-state form at all.
			cx.patternBindings(n.Pattern, out.whenTrue)
		}
		return out
	}

	// A constant condition still carries its operands' assignments, and a
	// constant false makes the true branch vacuously assign everything —
	// which is how `if (false) { }` avoids complaining about anything.
	cx.expr(x, st)
	c := unconditional(st)
	if cx.isConstTrue(x) {
		c.whenFalse = fullyAssigned(cx, st)
	} else if cx.isConstFalse(x) {
		c.whenTrue = fullyAssigned(cx, st)
	}
	return c
}

// fullyAssigned is the state on a path that cannot be taken. Everything is
// vacuously definitely assigned there, so no error is reported down a dead
// branch.
func fullyAssigned(cx *ctx, st state) state {
	s := st.clone()
	for i := range cx.order {
		s.da.set(i)
		s.du.set(i)
	}
	return s
}

// read checks that a variable is definitely assigned before use.
func (cx *ctx) read(x ast.Expr, st state) {
	v := cx.varOf(x)
	if v == nil {
		return
	}
	i := cx.indexOf(v)
	if i < 0 {
		// Not a local of this method: a field, or a capture from an enclosing
		// one. Fields are always definitely assigned by their default value;
		// a capture is checked where it is captured.
		cx.noteCapture(v, x)
		return
	}
	if !st.da.has(i) {
		cx.a.errorf(x.Pos(), x.End(), "variable %s might not have been initialized", v.Name)
		// Suppress the cascade: treat it as assigned from here on, so one
		// uninitialised variable produces one diagnostic and not one per use.
		st.assign(i)
	}
}

// write records an assignment, reporting a second write to a blank final.
func (cx *ctx) write(x ast.Expr, st state) {
	v := cx.varOf(x)
	if v == nil {
		return
	}
	cx.writes[v]++

	i := cx.indexOf(v)
	if i < 0 {
		// A field, or a captured local. Assigning a captured local is what
		// makes it not effectively final, which capture.go reports.
		cx.noteWrite(v, x)
		return
	}
	if cx.blanks[i] && !st.du.has(i) {
		cx.a.errorf(x.Pos(), x.End(),
			"variable %s might already have been assigned", v.Name)
	}
	st.assign(i)
}

func (cx *ctx) assignExpr(n *ast.AssignExpr, st state) {
	// A compound assignment reads before it writes; a simple one does not,
	// which is why `int x; x = 1;` is legal and `int x; x += 1;` is not.
	if n.Op != token.ASSIGN {
		cx.read(n.LHS, st)
	}
	cx.expr(n.RHS, st)

	switch lhs := n.LHS.(type) {
	case *ast.Ident, *ast.Name:
		cx.write(n.LHS, st)
	case *ast.IndexExpr:
		cx.expr(lhs.X, st)
		cx.expr(lhs.Index, st)
	case *ast.SelectorExpr:
		cx.expr(lhs.X, st)
	case *ast.ParenExpr:
		inner := *n
		inner.LHS = lhs.X
		cx.assignExpr(&inner, st)
	}
}

// patternBindings declares the variables a pattern introduces and marks them
// assigned. Their scope is where the match succeeded, which the caller has
// already selected by passing the right state.
func (cx *ctx) patternBindings(p ast.Pattern, st state) {
	switch n := p.(type) {
	case *ast.TypePattern:
		if v, ok := cx.a.info.Use(n).(*symVar); ok && v != nil {
			i := cx.declare(v, false)
			st.da.grow(i)
			st.du.grow(i)
			st.assign(i)
		}
	case *ast.RecordPattern:
		for _, elt := range n.Elts {
			cx.patternBindings(elt, st)
		}
	}
}

// varOf recovers the variable an expression denotes, or nil.
func (cx *ctx) varOf(x ast.Expr) *sym.VarSym {
	switch n := x.(type) {
	case *ast.ParenExpr:
		return cx.varOf(n.X)
	case *ast.Ident, *ast.Name, *ast.SelectorExpr:
		v, _ := cx.a.info.Use(x).(*sym.VarSym)
		if v != nil && (v.Var == sym.VarLocal || v.Var == sym.VarParam ||
			v.Var == sym.VarBinding || v.Var == sym.VarResource ||
			v.Var == sym.VarExceptionParam || v.Var == sym.VarField) {
			return v
		}
		_ = n
	}
	return nil
}

// symVar is a local alias so the type switches above read cleanly.
type symVar = sym.VarSym