package attr

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// expr attributes an expression and returns its type.
//
// want is the target type where one exists (§5.1's assignment context, a
// method argument, a return) and nil where the expression stands alone. It is
// threaded down rather than checked afterwards because poly expressions —
// lambdas, method references, the conditional operator, diamond `new` — have
// no type at all without it.
func (a *attributor) expr(e *env, x ast.Expr, want types.Type) types.Type {
	t := a.exprRaw(e, x, want)
	if t == nil {
		t = errType
	}
	a.info.Types[x] = t
	a.fold(e, x)
	if want != nil {
		t = a.assignable(e, x, t, want)
	}
	return t
}

func (a *attributor) exprRaw(e *env, x ast.Expr, want types.Type) types.Type {
	switch n := x.(type) {
	case *ast.BasicLit:
		return a.literal(e, n)

	case *ast.Ident:
		s, t := a.resolveExprName(e, identText(n, e.file()), n)
		if s != nil {
			a.info.Uses[n] = s
		}
		return t

	case *ast.Name:
		s, t := a.resolveName(e, n)
		if s != nil {
			a.info.Uses[n] = s
		}
		return t

	case *ast.ParenExpr:
		return a.expr(e, n.X, want)

	case *ast.This:
		return a.this(e, n)

	case *ast.Super:
		// `super` is never a value on its own; it only qualifies a field
		// access, a method call, a method reference or a constructor call,
		// and each of those handles it. Reaching here means the parser
		// admitted it somewhere it does not belong.
		a.errorf(n.Pos(), n.End(), "'super' cannot be used here")
		return errType

	case *ast.ClassLit:
		return a.classLit(e, n)

	case *ast.SelectorExpr:
		return a.selector(e, n)

	case *ast.IndexExpr:
		return a.index(e, n)

	case *ast.CallExpr:
		return a.call(e, n, want)

	case *ast.NewExpr:
		return a.newExpr(e, n, want)

	case *ast.NewArrayExpr:
		return a.newArray(e, n)

	case *ast.UnaryExpr:
		return a.unary(e, n)

	case *ast.PostfixExpr:
		return a.postfix(e, n)

	case *ast.BinaryExpr:
		return a.binary(e, n)

	case *ast.InstanceOfExpr:
		return a.instanceOf(e, n)

	case *ast.CondExpr:
		return a.conditional(e, n, want)

	case *ast.AssignExpr:
		return a.assign(e, n)

	case *ast.CastExpr:
		return a.cast(e, n)

	case *ast.LambdaExpr:
		return a.lambda(e, n, want)

	case *ast.MethodRef:
		return a.methodRef(e, n, want)

	case *ast.SwitchExpr:
		return a.switchExpr(e, n, want)

	case *ast.ArrayInit:
		return a.arrayInit(e, n, want)

	case *ast.BadExpr:
		return errType
	}
	return errType
}

// literal types a literal from its Kind. The spelling is still undecoded —
// scanner keeps it raw on purpose — so this is also where the value is parsed,
// via fold.
func (a *attributor) literal(e *env, n *ast.BasicLit) types.Type {
	switch n.Kind {
	case token.INT:
		// §3.10.1: an integer literal is int unless it has an L suffix. The
		// suffix is the last byte of the raw span, which is why the literal
		// arrives undecoded.
		if hasLongSuffix(e.file(), n) {
			return types.Long
		}
		return types.Int
	case token.FLOAT:
		if hasFloatSuffix(e.file(), n) {
			return types.Float
		}
		return types.Double
	case token.CHAR:
		return types.Char
	case token.STRING, token.TEXTBLOCK:
		return a.types.String_()
	case token.TRUE, token.FALSE:
		return types.Boolean
	case token.NULL:
		return types.Null
	}
	return errType
}

func (a *attributor) this(e *env, n *ast.This) types.Type {
	if n.Qualifier != nil {
		// TypeName.this names an enclosing instance (§15.8.4).
		t := a.resolveTypeName(e, lastPart(n.Qualifier, e.file()), n.Qualifier)
		return t
	}
	if e.static {
		a.errorf(n.Pos(), n.End(), "'this' cannot be referenced from a static context")
		return errType
	}
	if e.class == nil {
		return errType
	}
	return a.types.ClassOf(e.class, nil, nil)
}

// classLit types `T.class` as Class<T>, or raw Class when java.lang.Class is
// not on the path.
func (a *attributor) classLit(e *env, n *ast.ClassLit) types.Type {
	var arg types.Type = types.Void
	if n.Type != nil {
		arg = arrayOf(a.resolveType(e, n.Type), len(n.Dims))
	}
	cls := a.syms.Class(sym.ClassName)
	if cls == nil {
		return errType
	}
	// A primitive cannot be a type argument, so int.class is Class<Integer>.
	if arg.Kind().IsPrimitive() || arg.Kind() == types.KindVoid {
		arg = a.boxed(arg)
	}
	return a.types.ClassOf(cls, []types.Type{arg}, nil)
}

// selector is a field access: X.name. X may be a *ast.Super.
func (a *attributor) selector(e *env, n *ast.SelectorExpr) types.Type {
	name := identText(n.Sel, e.file())

	if sup, ok := n.X.(*ast.Super); ok {
		recv := a.superType(e, sup)
		v, _ := a.fieldOf(recv, name)
		if v == nil {
			a.errorf(n.Sel.Pos(), n.Sel.End(), "cannot find symbol: variable %s", name)
			return errType
		}
		a.info.Uses[n] = v
		return a.varType(v)
	}

	recv := a.expr(e, n.X, nil)

	// §10.7: length is a field of every array and of nothing else.
	if _, isArr := recv.(*types.ArrayType); isArr && name == "length" {
		return types.Int
	}
	if types.IsError(recv) {
		return errType
	}

	v, owner := a.fieldOf(recv, name)
	if v == nil {
		a.errorf(n.Sel.Pos(), n.Sel.End(),
			"cannot find symbol: variable %s in %s", name, recv)
		return errType
	}
	_ = owner
	a.info.Uses[n] = v
	return a.substitute(recv, a.varType(v))
}

func (a *attributor) index(e *env, n *ast.IndexExpr) types.Type {
	arr := a.expr(e, n.X, nil)
	idx := a.expr(e, n.Index, nil)
	if !isIntegral(types.Promote(a.unboxed(idx))) && !types.IsError(idx) {
		a.errorf(n.Index.Pos(), n.Index.End(), "array index must be an integer, not %s", idx)
	}
	at, ok := arr.(*types.ArrayType)
	if !ok {
		if !types.IsError(arr) {
			a.errorf(n.X.Pos(), n.X.End(), "%s is not an array", arr)
		}
		return errType
	}
	return at.Elem
}

// unary handles the prefix operators of §15.15 and the increment forms.
func (a *attributor) unary(e *env, n *ast.UnaryExpr) types.Type {
	t := a.expr(e, n.X, nil)
	if types.IsError(t) {
		return errType
	}
	switch n.Op {
	case token.NOT:
		if a.unboxed(t).Kind() != types.KindBoolean {
			a.errorf(n.Pos(), n.End(), "operator ! cannot be applied to %s", t)
			return errType
		}
		return types.Boolean

	case token.TILDE:
		p := types.Promote(a.unboxed(t))
		if !isIntegral(p) {
			a.errorf(n.Pos(), n.End(), "operator ~ cannot be applied to %s", t)
			return errType
		}
		return p

	case token.ADD, token.SUB:
		p := types.Promote(a.unboxed(t))
		if !p.Kind().IsNumeric() {
			a.errorf(n.Pos(), n.End(), "operator %s cannot be applied to %s", n.Op, t)
			return errType
		}
		return p

	case token.INC, token.DEC:
		a.checkAssignable(e, n.X)
		if !a.unboxed(t).Kind().IsNumeric() {
			a.errorf(n.Pos(), n.End(), "operator %s cannot be applied to %s", n.Op, t)
			return errType
		}
		// §15.15.1: the result of a prefix increment keeps the operand's own
		// type, not the promoted one — ++ on a byte yields a byte.
		return t
	}
	return errType
}

func (a *attributor) postfix(e *env, n *ast.PostfixExpr) types.Type {
	t := a.expr(e, n.X, nil)
	a.checkAssignable(e, n.X)
	if !types.IsError(t) && !a.unboxed(t).Kind().IsNumeric() {
		a.errorf(n.Pos(), n.End(), "operator %s cannot be applied to %s", n.Op, t)
		return errType
	}
	return t
}

// binary covers every left-associative operator level.
//
// Op may be token.SHR or token.USHR, which no scanned token carries: the
// parser assembled them from adjacent `>` tokens. They are in the synthetic
// range, so Kind.IsOperator is false for them and any switch must list them
// explicitly.
func (a *attributor) binary(e *env, n *ast.BinaryExpr) types.Type {
	lt := a.expr(e, n.X, nil)
	rt := a.expr(e, n.Y, nil)
	if types.IsError(lt) || types.IsError(rt) {
		return errType
	}
	lu, ru := a.unboxed(lt), a.unboxed(rt)

	switch n.Op {
	case token.ADD:
		// §15.18.1: + is string concatenation when either operand is a String,
		// and that case admits every type including void-less null.
		if a.isString(lt) || a.isString(rt) {
			return a.types.String_()
		}
		return a.arith(n, lt, rt, lu, ru)

	case token.SUB, token.MUL, token.QUO, token.REM:
		return a.arith(n, lt, rt, lu, ru)

	case token.SHL, token.SHR, token.USHR:
		// §15.19: the operands are promoted *separately*. A long shift
		// distance does not make the result long, which is the trap.
		if !isIntegral(types.Promote(lu)) || !isIntegral(types.Promote(ru)) {
			a.errorf(n.Pos(), n.End(), "operator %s cannot be applied to %s, %s", n.Op, lt, rt)
			return errType
		}
		return types.Promote(lu)

	case token.LSS, token.GTR, token.LEQ, token.GEQ:
		if !lu.Kind().IsNumeric() || !ru.Kind().IsNumeric() {
			a.errorf(n.Pos(), n.End(), "operator %s cannot be applied to %s, %s", n.Op, lt, rt)
		}
		return types.Boolean

	case token.EQL, token.NEQ:
		a.checkComparable(n, lt, rt, lu, ru)
		return types.Boolean

	case token.AND, token.OR, token.XOR:
		// §15.22: these are bitwise on integers and logical on booleans, and
		// mixing the two is an error rather than a coercion.
		if lu.Kind() == types.KindBoolean && ru.Kind() == types.KindBoolean {
			return types.Boolean
		}
		if isIntegral(types.Promote(lu)) && isIntegral(types.Promote(ru)) {
			return types.PromoteBinary(types.Promote(lu), types.Promote(ru))
		}
		a.errorf(n.Pos(), n.End(), "operator %s cannot be applied to %s, %s", n.Op, lt, rt)
		return errType

	case token.LAND, token.LOR:
		if lu.Kind() != types.KindBoolean || ru.Kind() != types.KindBoolean {
			a.errorf(n.Pos(), n.End(), "operator %s cannot be applied to %s, %s", n.Op, lt, rt)
		}
		return types.Boolean
	}
	return errType
}

func (a *attributor) arith(n *ast.BinaryExpr, lt, rt, lu, ru types.Type) types.Type {
	if !lu.Kind().IsNumeric() || !ru.Kind().IsNumeric() {
		a.errorf(n.Pos(), n.End(), "operator %s cannot be applied to %s, %s", n.Op, lt, rt)
		return errType
	}
	return types.PromoteBinary(types.Promote(lu), types.Promote(ru))
}

// checkComparable implements §15.21: numeric, boolean, or reference — and the
// two reference operands must be cast-compatible in one direction or the other.
func (a *attributor) checkComparable(n *ast.BinaryExpr, lt, rt, lu, ru types.Type) {
	switch {
	case lu.Kind().IsNumeric() && ru.Kind().IsNumeric():
	case lu.Kind() == types.KindBoolean && ru.Kind() == types.KindBoolean:
	case types.IsReference(lt) && types.IsReference(rt):
		if !a.types.IsSubtype(lt, rt) && !a.types.IsSubtype(rt, lt) {
			a.errorf(n.Pos(), n.End(), "incomparable types: %s and %s", lt, rt)
		}
	default:
		a.errorf(n.Pos(), n.End(), "incomparable types: %s and %s", lt, rt)
	}
}

// instanceOf takes a type or a pattern on the right, never an expression, and
// exactly one of the two fields is non-nil.
func (a *attributor) instanceOf(e *env, n *ast.InstanceOfExpr) types.Type {
	xt := a.expr(e, n.X, nil)
	if n.Type != nil {
		t := a.resolveType(e, n.Type)
		a.info.Types[n.Type] = t
		if !types.IsReference(xt) && !types.IsError(xt) {
			a.errorf(n.X.Pos(), n.X.End(), "instanceof requires a reference type, not %s", xt)
		}
		return types.Boolean
	}
	// A pattern introduces a binding whose scope is where the instanceof is
	// definitely true. Computing that scope is flow's; the binding itself is
	// declared here so the true branch can see it.
	a.pattern(e, n.Pattern, xt)
	return types.Boolean
}

// conditional is §15.25. The reference case is the one worth naming: the
// result is lub(T1,T2), and mocha does not compute least upper bounds — it
// takes the target type when there is one and falls back to the first branch.
func (a *attributor) conditional(e *env, n *ast.CondExpr, want types.Type) types.Type {
	ct := a.expr(e, n.Cond, nil)
	if a.unboxed(ct).Kind() != types.KindBoolean && !types.IsError(ct) {
		a.errorf(n.Cond.Pos(), n.Cond.End(), "condition must be boolean, not %s", ct)
	}
	tt := a.expr(e, n.Then, want)
	et := a.expr(e, n.Else, want)

	if want != nil {
		return want
	}
	if types.Identical(tt, et) {
		return tt
	}
	tu, eu := a.unboxed(tt), a.unboxed(et)
	if tu.Kind().IsNumeric() && eu.Kind().IsNumeric() {
		return types.PromoteBinary(types.Promote(tu), types.Promote(eu))
	}
	if tt.Kind() == types.KindNull {
		return et
	}
	if et.Kind() == types.KindNull {
		return tt
	}
	if a.types.IsSubtype(et, tt) {
		return tt
	}
	if a.types.IsSubtype(tt, et) {
		return et
	}
	// A real lub would intersect the supertype sets. Object is the safe
	// approximation, and the only cost is a checkcast lower would have
	// emitted anyway.
	return a.types.Object()
}

func (a *attributor) assign(e *env, n *ast.AssignExpr) types.Type {
	lt := a.expr(e, n.LHS, nil)
	a.checkAssignable(e, n.LHS)

	if n.Op == token.ASSIGN {
		a.expr(e, n.RHS, lt)
		return lt
	}
	// A compound assignment has an implicit narrowing cast back to the
	// left-hand type (§15.26.2), so `byte b; b += 1;` compiles where
	// `b = b + 1` does not. That is not a licence to skip the check on the
	// right: the operator still has to apply.
	rt := a.expr(e, n.RHS, nil)
	if types.IsError(lt) || types.IsError(rt) {
		return lt
	}
	if n.Op == token.ADD_ASSIGN && a.isString(lt) {
		return lt
	}
	lu, ru := a.unboxed(lt), a.unboxed(rt)
	switch n.Op {
	case token.AND_ASSIGN, token.OR_ASSIGN, token.XOR_ASSIGN:
		if lu.Kind() == types.KindBoolean && ru.Kind() == types.KindBoolean {
			return lt
		}
		fallthrough
	case token.SHL_ASSIGN, token.SHR_ASSIGN, token.USHR_ASSIGN:
		if !isIntegral(types.Promote(lu)) || !isIntegral(types.Promote(ru)) {
			a.errorf(n.Pos(), n.End(), "operator %s cannot be applied to %s, %s", n.Op, lt, rt)
		}
	default:
		if !lu.Kind().IsNumeric() || !ru.Kind().IsNumeric() {
			a.errorf(n.Pos(), n.End(), "operator %s cannot be applied to %s, %s", n.Op, lt, rt)
		}
	}
	return lt
}

// checkAssignable reports an assignment to something that is not a variable.
// Whether a blank final has already been assigned is definite-assignment and
// therefore flow's; that it is a variable at all is this package's.
func (a *attributor) checkAssignable(e *env, x ast.Expr) {
	switch n := x.(type) {
	case *ast.Ident, *ast.Name, *ast.IndexExpr:
		if s := a.info.Use(x); s != nil {
			if v, ok := s.(*sym.VarSym); ok && v.Flags.Has(sym.FlagFinal) && v.Var != sym.VarLocal {
				if !e.ctor {
					a.errorf(x.Pos(), x.End(), "cannot assign a value to final variable %s", v.Name)
				}
			}
		}
	case *ast.SelectorExpr:
		if v, ok := a.info.Use(n).(*sym.VarSym); ok && v.Flags.Has(sym.FlagFinal) && !e.ctor {
			a.errorf(x.Pos(), x.End(), "cannot assign a value to final variable %s", v.Name)
		}
	case *ast.ParenExpr:
		a.checkAssignable(e, n.X)
	default:
		a.errorf(x.Pos(), x.End(), "unexpected type: required a variable")
	}
}

func (a *attributor) cast(e *env, n *ast.CastExpr) types.Type {
	target := a.resolveType(e, n.Type)
	if len(n.Bounds) > 0 {
		bounds := []types.Type{target}
		for _, b := range n.Bounds {
			bounds = append(bounds, a.resolveType(e, b))
		}
		target = &types.Intersection{Bounds: bounds}
	}
	a.info.Types[n.Type] = target
	src := a.expr(e, n.X, nil)
	a.checkCast(n, src, target)
	return target
}

func (a *attributor) newArray(e *env, n *ast.NewArrayExpr) types.Type {
	elt := a.resolveType(e, n.Elt)
	for _, d := range n.DimExprs {
		dt := a.expr(e, d.X, nil)
		if !isIntegral(types.Promote(a.unboxed(dt))) && !types.IsError(dt) {
			a.errorf(d.X.Pos(), d.X.End(), "array dimension must be an integer, not %s", dt)
		}
	}
	t := arrayOf(elt, len(n.DimExprs)+len(n.Dims))
	if n.Init != nil {
		a.arrayInit(e, n.Init, t)
	}
	return t
}