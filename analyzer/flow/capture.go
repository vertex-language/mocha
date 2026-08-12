package flow

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// Capture and effective finality, §4.12.4 and §15.27.2.
//
// A lambda or an inner class may read a local of an enclosing method only if
// that local is final or effectively final — never reassigned after its
// initialisation. The reason is that the capture is by value: lower copies the
// variable into a synthetic field, and a later write to the original would not
// be visible through the copy.
//
// So this file answers two questions at once. Which locals does a nested body
// read from outside itself, which is what lower needs; and were any of them
// reassigned, which is what makes the capture an error.

// lambda analyses a lambda body in its own context, with this one as the
// enclosing scope so reads of outer locals are recognised as captures.
func (cx *ctx) lambda(n *ast.LambdaExpr, st state) {
	inner := cx.a.newCtx(cx.class, cx.method, cx)

	// A lambda's parameters are its own; everything else it names belongs to
	// an enclosing scope.
	for _, p := range n.Params {
		if v, ok := cx.a.info.Use(p).(*symVar); ok && v != nil {
			inner.declare(v, false)
		}
	}
	// A lambda body cannot see the enclosing method's throws clause: a
	// checked exception must be compatible with the functional interface's
	// own method, which attr resolved. Clearing it here means an uncovered
	// throw inside a lambda is reported against the lambda.
	inner.declared = nil

	ist := inner.newState()
	switch body := n.Body.(type) {
	case *ast.Block:
		inner.block(body, ist)
	case ast.Expr:
		inner.expr(body, ist)
	}
	inner.finish()
}

// switchExpr analyses a switch expression's arms.
func (cx *ctx) switchExpr(n *ast.SwitchExpr, st state) {
	if n.Block == nil {
		return
	}
	cx.expr(n.Tag, st)

	var ends []state
	for _, r := range n.Block.Rules {
		s := st.clone()
		cx.switchLabel(r.Label, s)
		switch b := r.Body.(type) {
		case *ast.Block:
			if cx.block(b, s) {
				ends = append(ends, s)
			}
		case ast.Expr:
			cx.expr(b, s)
			ends = append(ends, s)
		case *ast.ThrowStmt:
			cx.stmt(b, s)
		}
	}
	for _, g := range n.Block.Groups {
		s := st.clone()
		for _, l := range g.Labels {
			cx.switchLabel(l, s)
		}
		alive := true
		for _, gs := range g.Stmts {
			if !alive {
				break
			}
			alive = cx.stmt(gs, s)
		}
		if alive {
			ends = append(ends, s)
		}
	}
	// A switch expression must yield on every path, so every arm's
	// assignments are definitely assigned afterwards.
	if len(ends) > 0 {
		merged := ends[0]
		for _, e := range ends[1:] {
			merged = join(merged, e)
		}
		copyInto(st, merged)
	}
}

// noteCapture records that a nested body read a variable from an enclosing
// method, and checks that it may.
func (cx *ctx) noteCapture(v *sym.VarSym, x ast.Expr) {
	if cx.outer == nil || v == nil {
		return
	}
	// Walk outward to find whose local this is. A field is not a capture:
	// the inner class reaches it through the enclosing instance.
	owner := cx.outer
	for owner != nil {
		if owner.indexOf(v) >= 0 {
			break
		}
		owner = owner.outer
	}
	if owner == nil {
		return
	}
	if v.Var == sym.VarField {
		return
	}

	if !cx.a.mayCapture(v, owner) {
		cx.a.errorf(x.Pos(), x.End(),
			"local variables referenced from a lambda expression or inner class "+
				"must be final or effectively final: %s", v.Name)
		return
	}
	if cx.method != nil {
		cx.a.out.Captured[cx.method] = appendUnique(cx.a.out.Captured[cx.method], v)
	}
}

// noteWrite records an assignment to something that is not a local of the
// current context — a field, or a captured variable. Writing a captured
// variable is what disqualifies it.
func (cx *ctx) noteWrite(v *sym.VarSym, x ast.Expr) {
	if v == nil || cx.outer == nil {
		return
	}
	owner := cx.outer
	for owner != nil {
		if owner.indexOf(v) >= 0 {
			owner.writes[v]++
			cx.a.errorf(x.Pos(), x.End(),
				"local variables referenced from a lambda expression or inner class "+
					"must be final or effectively final: %s", v.Name)
			return
		}
		owner = owner.outer
	}
}

// mayCapture reports whether a variable is final or effectively final in the
// context that declares it. A variable written more than once is not — its
// initialiser counts as the one permitted write.
func (a *analyzer) mayCapture(v *sym.VarSym, owner *ctx) bool {
	if v.Flags.Has(sym.FlagFinal) {
		return true
	}
	return owner.writes[v] <= 1
}

// classIn analyses a local class declared inside a method body, with the
// method's context as the enclosing scope.
func (a *analyzer) classIn(c *sym.ClassSym, outer *ctx) {
	if c == nil || !c.FromSource() {
		return
	}
	switch d := c.Decl.(type) {
	case *ast.ClassDecl:
		a.members(c, d.Members, outer)
	case *ast.InterfaceDecl:
		a.members(c, d.Members, outer)
	case *ast.EnumDecl:
		a.members(c, d.Members, outer)
	case *ast.RecordDecl:
		a.members(c, d.Members, outer)
	}
}

// anonymousIn analyses an anonymous class body, with the enclosing method's
// context available so its captures are recognised.
func (a *analyzer) anonymousIn(n *ast.NewExpr, outer *ctx) {
	if n.Body == nil {
		return
	}
	// attr entered the anonymous class; its symbol is not keyed on the
	// NewExpr, so the body is walked against the enclosing class instead.
	// What matters here is only that the outer context is threaded through:
	// the capture check does not need the anonymous class's own symbol.
	a.members(outer.class, n.Body, outer)
}

func appendUnique(list []*sym.VarSym, v *sym.VarSym) []*sym.VarSym {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

var _ = types.KindVoid