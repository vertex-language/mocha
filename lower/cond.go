package lower

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// A condition is a branch, not a value.
//
// `if (a && b)` never materialises a boolean. Comparisons fuse into if_icmplt
// rather than producing 0/1 and testing it, `!` inverts the branch rather than
// emitting anything, and &&/|| short-circuit into jump targets. A boolean
// becomes a value only where one is genuinely required — an assignment, an
// argument, a return — which is what condValue below is for.

// cond emits x as a branch to target, taken when x evaluates to `when`.
func (e *emitter) cond(x ast.Expr, target *classfile.Label, when bool) {
	// A folded constant condition is a goto or nothing at all. flow already
	// decided reachability on the same values.
	if k, ok := e.in.Const(x); ok {
		if b, isBool := k.Value.(bool); isBool {
			if b == when {
				e.c.Goto(target)
			}
			return
		}
	}

	switch n := x.(type) {
	case *ast.ParenExpr:
		e.cond(n.X, target, when)
		return

	case *ast.UnaryExpr:
		if n.Op == token.NOT {
			// `!` inverts the branch rather than emitting anything.
			e.cond(n.X, target, !when)
			return
		}

	case *ast.BinaryExpr:
		switch n.Op {
		case token.LAND:
			e.condAnd(n, target, when)
			return
		case token.LOR:
			e.condOr(n, target, when)
			return
		case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
			e.compare(n, target, when)
			return
		}

	case *ast.InstanceOfExpr:
		e.expr(n.X)
		if n.Type != nil {
			e.c.InstanceOf(castTarget(e.in.Type(n.Type)))
			e.branchZero(target, when)
			return
		}
		// A pattern binds, so it is not a bare instanceof. stmt.go's pattern
		// support owns it; until then this is unreachable, not silently wrong.
		bug("instanceof pattern in condition position")
	}

	// Anything else really is a boolean value: materialise it and test it.
	e.expr(x)
	e.branchZero(target, when)
}

// condAnd: `a && b` branches to target on true only when both do; on false when
// either does.
func (e *emitter) condAnd(n *ast.BinaryExpr, target *classfile.Label, when bool) {
	if when {
		// Fall through to the target only if both hold, so a false a skips.
		skip := e.c.NewLabel()
		e.cond(n.X, skip, false)
		e.cond(n.Y, target, true)
		e.c.Mark(skip)
		return
	}
	// Branch on false: either operand being false is enough.
	e.cond(n.X, target, false)
	e.cond(n.Y, target, false)
}

// condOr is condAnd's dual.
func (e *emitter) condOr(n *ast.BinaryExpr, target *classfile.Label, when bool) {
	if when {
		e.cond(n.X, target, true)
		e.cond(n.Y, target, true)
		return
	}
	skip := e.c.NewLabel()
	e.cond(n.X, skip, true)
	e.cond(n.Y, target, false)
	e.c.Mark(skip)
}

// compare fuses a relational or equality operator into a single branch.
func (e *emitter) compare(n *ast.BinaryExpr, target *classfile.Label, when bool) {
	lt := e.in.Type(n.X)
	rt := e.in.Type(n.Y)

	// Reference equality: acmp, or ifnull against a literal null.
	if types.IsReference(lt) && types.IsReference(rt) {
		if isNullLit(n.Y) {
			e.expr(n.X)
			e.branchNull(n.Op, target, when)
			return
		}
		if isNullLit(n.X) {
			e.expr(n.Y)
			e.branchNull(n.Op, target, when)
			return
		}
		e.expr(n.X)
		e.expr(n.Y)
		if eq(n.Op) == when {
			e.c.IfACmpEq(target)
		} else {
			e.c.IfACmpNe(target)
		}
		return
	}

	// Numeric: promote both operands, then either fuse into if_icmp<cond> or
	// go through lcmp/fcmp/dcmp and test the result against zero.
	p := types.PromoteBinary(lt, rt)
	e.expr(n.X)
	e.convert(lt, p)
	e.expr(n.Y)
	e.convert(rt, p)

	k := n.Op
	if !when {
		k = invert(k)
	}

	if p.Kind() == types.KindInt || p.Kind() == types.KindBoolean {
		switch k {
		case token.EQL:
			e.c.IfICmpEq(target)
		case token.NEQ:
			e.c.IfICmpNe(target)
		case token.LSS:
			e.c.IfICmpLt(target)
		case token.LEQ:
			e.c.IfICmpLe(target)
		case token.GTR:
			e.c.IfICmpGt(target)
		case token.GEQ:
			e.c.IfICmpGe(target)
		default:
			bug("not a comparison: %s", n.Op)
		}
		return
	}

	// lcmp, fcmpl/fcmpg and dcmpl/dcmpg leave -1, 0 or 1. The g/l choice is
	// what makes NaN compare false on both sides: pick the variant whose NaN
	// result fails the test being emitted.
	e.c.Op(cmpOp(p.Kind(), k))
	e.branchAgainstZero(k, target)
}

// branchZero tests a materialised boolean.
func (e *emitter) branchZero(target *classfile.Label, when bool) {
	if when {
		e.c.IfNe(target) // non-zero is true
	} else {
		e.c.IfEq(target)
	}
}

func (e *emitter) branchNull(k token.Kind, target *classfile.Label, when bool) {
	if eq(k) == when {
		e.c.IfNull(target)
	} else {
		e.c.IfNonNull(target)
	}
}

func (e *emitter) branchAgainstZero(k token.Kind, target *classfile.Label) {
	switch k {
	case token.EQL:
		e.c.IfEq(target)
	case token.NEQ:
		e.c.IfNe(target)
	case token.LSS:
		e.c.IfLt(target)
	case token.LEQ:
		e.c.IfLe(target)
	case token.GTR:
		e.c.IfGt(target)
	case token.GEQ:
		e.c.IfGe(target)
	default:
		bug("not a comparison: %s", k)
	}
}

// condValue materialises a boolean, for the positions that genuinely need one.
func (e *emitter) condValue(x ast.Expr) {
	t, f, done := e.c.NewLabel(), e.c.NewLabel(), e.c.NewLabel()
	_ = f
	e.cond(x, t, true)
	e.c.Iconst(0)
	e.c.Goto(done)
	e.c.Mark(t)
	e.c.Iconst(1)
	e.c.Mark(done)
}

func eq(k token.Kind) bool { return k == token.EQL }

func invert(k token.Kind) token.Kind {
	switch k {
	case token.EQL:
		return token.NEQ
	case token.NEQ:
		return token.EQL
	case token.LSS:
		return token.GEQ
	case token.GEQ:
		return token.LSS
	case token.GTR:
		return token.LEQ
	case token.LEQ:
		return token.GTR
	}
	bug("not a comparison: %s", k)
	return k
}

func isNullLit(x ast.Expr) bool {
	l, ok := x.(*ast.BasicLit)
	return ok && l.Kind == token.NULL
}