package attr

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// Conversions, §5. Three contexts matter here: assignment (§5.2), method
// invocation (§5.3) and casting (§5.5). The first two differ only in that
// assignment admits a narrowing conversion on a constant expression, which is
// why `byte b = 1;` compiles and `byte b = someInt;` does not.

// assignable checks an expression against a target and returns the target on
// success, so the caller propagates the declared type rather than the
// expression's.
func (a *attributor) assignable(e *env, x ast.Expr, from, to types.Type) types.Type {
	if to == nil || types.IsError(from) || types.IsError(to) {
		return from
	}
	if a.assignableTo(from, to) {
		return to
	}
	// §5.2: a constant expression of type int narrows implicitly to byte,
	// short or char when the value fits. This is checked last because it
	// needs the folded value, which only exists for a constant expression.
	if k, ok := a.info.Const(x); ok && fitsNarrowed(k, to) {
		return to
	}
	a.errorf(x.Pos(), x.End(),
		"incompatible types: %s cannot be converted to %s", from, to)
	return to
}

// assignableTo is the conversion test without the diagnostic: identity,
// widening primitive, widening reference, boxing then widening, or unboxing
// then widening.
func (a *attributor) assignableTo(from, to types.Type) bool {
	if from == nil || to == nil {
		return false
	}
	if types.Identical(from, to) || types.IsError(from) || types.IsError(to) {
		return true
	}
	if from.Kind() == types.KindNull {
		return types.IsReference(to)
	}
	if from.Kind().IsPrimitive() && to.Kind().IsPrimitive() {
		return types.Widens(from, to)
	}
	if from.Kind().IsPrimitive() {
		// Boxing, then widening reference: int → Integer → Number → Object.
		return a.types.IsSubtype(a.boxed(from), to)
	}
	if to.Kind().IsPrimitive() {
		// Unboxing, then widening primitive: Integer → int → long.
		u := a.unboxed(from)
		return u.Kind().IsPrimitive() && (types.Identical(u, to) || types.Widens(u, to))
	}
	return a.types.IsSubtype(from, to)
}

// checkCast implements §5.5. A cast is legal when a conversion exists in
// either direction: narrowing is the whole point.
func (a *attributor) checkCast(n *ast.CastExpr, from, to types.Type) {
	if types.IsError(from) || types.IsError(to) {
		return
	}
	if from.Kind().IsPrimitive() != to.Kind().IsPrimitive() {
		// One side primitive, one reference: legal only through boxing.
		if from.Kind().IsPrimitive() && a.types.IsSubtype(a.boxed(from), to) {
			return
		}
		if to.Kind().IsPrimitive() && a.unboxed(from).Kind().IsPrimitive() {
			return
		}
		a.errorf(n.Pos(), n.End(), "incompatible types: %s cannot be cast to %s", from, to)
		return
	}
	if from.Kind().IsPrimitive() {
		// Every primitive casts to every other except boolean, which casts
		// only to itself.
		if (from.Kind() == types.KindBoolean) != (to.Kind() == types.KindBoolean) {
			a.errorf(n.Pos(), n.End(), "incompatible types: %s cannot be cast to %s", from, to)
		}
		return
	}
	if !a.types.IsSubtype(from, to) && !a.types.IsSubtype(to, from) {
		a.errorf(n.Pos(), n.End(), "incompatible types: %s cannot be cast to %s", from, to)
	}
}

// The eight boxing pairs of §5.1.7. They are here rather than in sym's
// well-known names because nothing below attribution needs them: boxing is a
// conversion, and conversions are this package's.
var boxNames = map[types.Kind]string{
	types.KindBoolean: "java/lang/Boolean",
	types.KindByte:    "java/lang/Byte",
	types.KindShort:   "java/lang/Short",
	types.KindChar:    "java/lang/Character",
	types.KindInt:     "java/lang/Integer",
	types.KindLong:    "java/lang/Long",
	types.KindFloat:   "java/lang/Float",
	types.KindDouble:  "java/lang/Double",
	types.KindVoid:    "java/lang/Void",
}

var unboxKinds = map[string]types.Kind{
	"java/lang/Boolean":   types.KindBoolean,
	"java/lang/Byte":      types.KindByte,
	"java/lang/Short":     types.KindShort,
	"java/lang/Character": types.KindChar,
	"java/lang/Integer":   types.KindInt,
	"java/lang/Long":      types.KindLong,
	"java/lang/Float":     types.KindFloat,
	"java/lang/Double":    types.KindDouble,
}

// boxed returns the wrapper for a primitive, or the type unchanged.
func (a *attributor) boxed(t types.Type) types.Type {
	name, ok := boxNames[t.Kind()]
	if !ok {
		return t
	}
	return a.types.Named(name)
}

// unboxed returns the primitive a wrapper holds, or the type unchanged. It is
// called before every operator check, because §15's operand rules are written
// against primitives and the wrappers convert silently.
func (a *attributor) unboxed(t types.Type) types.Type {
	ct, ok := t.(*types.ClassType)
	if !ok || ct.Sym == nil {
		return t
	}
	if k, ok := unboxKinds[ct.Sym.Binary]; ok {
		if p := types.PrimitiveOf(k); p != nil {
			return p
		}
	}
	return t
}

// fitsNarrowed reports whether a constant fits a narrower integral type
// (§5.2). char is unsigned, which is why its range is not symmetric with
// short's despite both being two bytes.
func fitsNarrowed(k types.Constant, to types.Type) bool {
	v, ok := k.Int()
	if !ok {
		return false
	}
	switch to.Kind() {
	case types.KindByte:
		return v >= -128 && v <= 127
	case types.KindShort:
		return v >= -32768 && v <= 32767
	case types.KindChar:
		return v >= 0 && v <= 65535
	}
	return false
}

func (a *attributor) isString(t types.Type) bool {
	ct, ok := t.(*types.ClassType)
	return ok && ct.Sym != nil && ct.Sym.Binary == sym.StringName
}

func (a *attributor) isThrowable(t types.Type) bool {
	th := a.syms.Class(sym.ThrowableName)
	if th == nil {
		return true // nothing on the path declares it; do not invent an error
	}
	return a.types.IsSubtype(t, a.types.ClassOf(th, nil, nil))
}

// isConstantVarType reports whether §4.12.4 admits a constant variable of this
// type: a primitive or String, and nothing else.
func isConstantVarType(t types.Type) bool {
	if t.Kind().IsPrimitive() {
		return true
	}
	ct, ok := t.(*types.ClassType)
	return ok && ct.Sym != nil && ct.Sym.Binary == sym.StringName
}