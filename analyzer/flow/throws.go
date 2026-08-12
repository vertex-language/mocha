package flow

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// Checked exceptions, §11.2.
//
// Everything a construct can throw propagates outward: a throw statement
// contributes its operand's type, an invocation contributes its callee's
// throws clause, and a try filters what its body produced through each catch
// before passing on the remainder. At the method boundary whatever is left
// must be covered by the declared throws.
//
// Unchecked exceptions are excluded by supertype test rather than by name,
// because a user class extending RuntimeException is unchecked too.

// throwType records what a `throw` statement contributes.
func (cx *ctx) throwType(x ast.Expr, pos, end token.Pos) {
	t := cx.a.info.Type(x)
	if types.IsError(t) {
		return
	}
	cx.raise(t, pos, end)
}

// callThrows records what an invocation's callee declares.
func (cx *ctx) callThrows(n ast.Node) {
	m, _ := cx.a.info.Use(n).(*sym.MethodSym)
	if m == nil {
		return
	}
	mt := cx.a.types.MethodType(m)
	if mt == nil {
		return
	}
	for _, t := range mt.Throws {
		cx.raise(t, n.Pos(), n.End())
	}
}

func (cx *ctx) constructorThrows(n *ast.ConstructorCall) { cx.callThrows(n) }

// raise reports a checked exception that nothing handles, and records it for
// an enclosing try to see.
func (cx *ctx) raise(t types.Type, pos, end token.Pos) {
	if !cx.a.isChecked(t) {
		return
	}
	cx.thrown = append(cx.thrown, t)

	if cx.handled(t) {
		return
	}
	cx.a.errorf(pos, end,
		"unreported exception %s; must be caught or declared to be thrown", t)
}

// handled reports whether any enclosing catch or the method's throws clause
// covers a type.
func (cx *ctx) handled(t types.Type) bool {
	for _, layer := range cx.caught {
		for _, c := range layer {
			if cx.a.types.IsSubtype(t, c) {
				return true
			}
		}
	}
	for _, d := range cx.declared {
		if cx.a.types.IsSubtype(t, d) {
			return true
		}
	}
	return false
}

// tryStmt analyses a try, its resources, its catches and its finally.
func (cx *ctx) tryStmt(n *ast.TryStmt, st state) bool {
	// Resources are in scope for the body and are implicitly final.
	for _, r := range n.Resources {
		cx.resource(r, st)
	}

	// Push this try's catch types so anything the body throws sees them.
	var layer []types.Type
	for _, c := range n.Catches {
		for _, xt := range c.Types {
			if t := cx.a.info.Type(xt); !types.IsError(t) {
				layer = append(layer, t)
			}
		}
	}
	cx.caught = append(cx.caught, layer)

	outer := cx.thrown
	cx.thrown = nil
	bodyState := st.clone()
	bodyAlive := cx.block(n.Body, bodyState)
	bodyThrew := cx.thrown

	// close() throws too (§14.20.3.2), and it runs on the same paths the body
	// does, so it belongs to the same set.
	for _, r := range n.Resources {
		cx.closeThrows(r)
	}
	bodyThrew = append(bodyThrew, cx.thrown...)

	cx.caught = cx.caught[:len(cx.caught)-1]
	cx.thrown = outer

	// A catch clause runs after an arbitrary prefix of the body, so nothing
	// the body assigned is definitely assigned inside it.
	catchStates := make([]state, 0, len(n.Catches))
	anyCatchAlive := false

	for _, c := range n.Catches {
		cs := st.clone()
		if c.Name != nil {
			if v, ok := cx.a.info.Use(c.Name).(*symVar); ok && v != nil {
				i := cx.declare(v, false)
				cs.da.grow(i)
				cs.du.grow(i)
				cs.assign(i)
			}
		}
		cx.checkReachableCatch(c, bodyThrew)
		if cx.block(c.Body, cs) {
			anyCatchAlive = true
			catchStates = append(catchStates, cs)
		}
	}

	// Anything the body threw that no catch covers propagates outward.
	for _, t := range bodyThrew {
		if !coveredBy(cx, t, layer) {
			cx.thrown = append(cx.thrown, t)
		}
	}

	if n.Finally != nil {
		// A finally runs on every path, so what it assigns is definitely
		// assigned afterwards regardless of how the try exited — the one
		// place assignments flow out of a construct that may have aborted.
		fs := st.clone()
		finallyAlive := cx.block(n.Finally, fs)
		if !finallyAlive {
			// A finally that cannot complete normally swallows every path
			// through the try, including the exceptions.
			copyInto(st, fs)
			return false
		}
		copyInto(st, fs)
		return bodyAlive || anyCatchAlive
	}

	merged := bodyState
	if !bodyAlive && len(catchStates) > 0 {
		merged = catchStates[0]
		catchStates = catchStates[1:]
	}
	for _, cs := range catchStates {
		merged = join(merged, cs)
	}
	if bodyAlive || anyCatchAlive {
		copyInto(st, merged)
	}
	return bodyAlive || anyCatchAlive
}

// resource declares a try-with-resources resource.
func (cx *ctx) resource(r *ast.Resource, st state) {
	if r.X != nil {
		cx.expr(r.X, st)
		return
	}
	if r.Decl != nil {
		cx.localVars(r.Decl, st)
	}
}

// closeThrows adds what a resource's close() declares.
func (cx *ctx) closeThrows(r *ast.Resource) {
	var t types.Type
	switch {
	case r.X != nil:
		t = cx.a.info.Type(r.X)
	case r.Decl != nil && len(r.Decl.Names) > 0:
		t = cx.a.info.Type(r.Decl.Names[0])
	default:
		return
	}
	ct, ok := t.(*types.ClassType)
	if !ok || ct.Sym == nil {
		return
	}
	for _, m := range ct.Sym.Methods("close") {
		mt := cx.a.types.MethodType(m)
		if mt == nil || len(mt.Params) != 0 {
			continue
		}
		for _, x := range mt.Throws {
			if cx.a.isChecked(x) && !cx.handled(x) {
				cx.thrown = append(cx.thrown, x)
			}
		}
	}
}

// checkReachableCatch implements §11.2.3: catching a checked exception the
// body cannot throw is an error. Exception and Throwable are exempt, because
// catching them is how you catch the unchecked ones too.
func (cx *ctx) checkReachableCatch(c *ast.CatchClause, bodyThrew []types.Type) {
	for _, xt := range c.Types {
		t := cx.a.info.Type(xt)
		if types.IsError(t) || !cx.a.isChecked(t) {
			continue
		}
		if cx.a.isBroadCatch(t) {
			continue
		}
		reachable := false
		for _, thrown := range bodyThrew {
			// Either direction: catching IOException covers a thrown
			// FileNotFoundException, and catching FileNotFoundException is
			// reachable when IOException is thrown.
			if cx.a.types.IsSubtype(thrown, t) || cx.a.types.IsSubtype(t, thrown) {
				reachable = true
				break
			}
		}
		if !reachable {
			cx.a.errorf(xt.Pos(), xt.End(),
				"exception %s is never thrown in the body of the corresponding try statement", t)
		}
	}
}

func coveredBy(cx *ctx, t types.Type, layer []types.Type) bool {
	for _, c := range layer {
		if cx.a.types.IsSubtype(t, c) {
			return true
		}
	}
	return false
}

// isChecked reports whether a type is a checked exception: a Throwable that is
// neither a RuntimeException nor an Error.
func (a *analyzer) isChecked(t types.Type) bool {
	if t == nil || types.IsError(t) {
		return false
	}
	th := a.syms.Class(sym.ThrowableName)
	if th == nil {
		return false // nothing on the path declares it; do not invent errors
	}
	if !a.types.IsSubtype(t, a.types.ClassOf(th, nil, nil)) {
		return false
	}
	for _, name := range []string{"java/lang/RuntimeException", "java/lang/Error"} {
		if c := a.syms.Class(name); c != nil {
			if a.types.IsSubtype(t, a.types.ClassOf(c, nil, nil)) {
				return false
			}
		}
	}
	return true
}

// isBroadCatch reports whether a catch type is Exception or Throwable, which
// §11.2.3 exempts from the reachability check.
func (a *analyzer) isBroadCatch(t types.Type) bool {
	ct, ok := t.(*types.ClassType)
	if !ok || ct.Sym == nil {
		return false
	}
	return ct.Sym.Binary == sym.ThrowableName || ct.Sym.Binary == "java/lang/Exception"
}