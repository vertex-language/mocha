package lower

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// String concatenation, §15.18.1.
//
// No StringConcatFactory: invokedynamic needs a BootstrapMethods attribute,
// which needs class file 51, which needs a StackMapTable. So this is the
// StringBuilder chain javac emitted before 9 — and dex has no
// invoke-polymorphic below API 26 either, so one lowering serves both targets.

const (
	sbName = "java/lang/StringBuilder"
	sbInit = "()V"
)

// concat emits `a + b` where at least one operand is a String. The tree is
// left-associative, so the spine is flattened into one builder rather than one
// per operator: `a + b + c` is a single StringBuilder, not two.
func (e *emitter) concat(n *ast.BinaryExpr) {
	e.c.New(sbName)
	e.c.Op(dup)
	e.c.InvokeSpecial(sbName, sym.InitName, sbInit)

	var walk func(x ast.Expr)
	walk = func(x ast.Expr) {
		if p, ok := x.(*ast.ParenExpr); ok {
			// Parentheses do not regroup a concatenation whose result is
			// already a String, but they do stop the flattening: `a + (b + c)`
			// really is two appends of one argument each.
			walk(p.X)
			return
		}
		if b, ok := x.(*ast.BinaryExpr); ok && b.Op == token.ADD && e.isConcat(b) {
			walk(b.X)
			walk(b.Y)
			return
		}
		e.append(x)
	}
	walk(n.X)
	walk(n.Y)

	e.c.InvokeVirtual(sbName, "toString", "()Ljava/lang/String;")
}

// isConcat reports whether a `+` is a concatenation rather than an addition.
// The rule is the result type, which attr already recorded.
func (e *emitter) isConcat(n *ast.BinaryExpr) bool {
	t := e.in.Type(n)
	ct, ok := t.(*types.ClassType)
	return ok && ct.Binary() == sym.StringName
}

// append emits one argument and the StringBuilder.append overload that takes
// it. §15.18.1 picks the overload by the operand's type, not by a conversion to
// String first — which is what makes `"" + c` for a char produce "a" rather
// than "97".
func (e *emitter) append(x ast.Expr) {
	t := e.in.Type(x)
	e.expr(x)

	desc := ""
	switch t.Kind() {
	case types.KindBoolean:
		desc = "(Z)Ljava/lang/StringBuilder;"
	case types.KindChar:
		desc = "(C)Ljava/lang/StringBuilder;"
	case types.KindByte, types.KindShort, types.KindInt:
		e.convert(t, types.Int)
		desc = "(I)Ljava/lang/StringBuilder;"
	case types.KindLong:
		desc = "(J)Ljava/lang/StringBuilder;"
	case types.KindFloat:
		desc = "(F)Ljava/lang/StringBuilder;"
	case types.KindDouble:
		desc = "(D)Ljava/lang/StringBuilder;"
	case types.KindNull:
		desc = "(Ljava/lang/String;)Ljava/lang/String;"
		desc = "(Ljava/lang/String;)Ljava/lang/StringBuilder;"
	default:
		if ct, ok := t.(*types.ClassType); ok && ct.Binary() == sym.StringName {
			desc = "(Ljava/lang/String;)Ljava/lang/StringBuilder;"
		} else {
			// append(Object) calls String.valueOf, which is null-safe — the
			// reason `null + ""` is "null" and not an NPE.
			desc = "(Ljava/lang/Object;)Ljava/lang/StringBuilder;"
		}
	}
	e.c.InvokeVirtual(sbName, "append", desc)
}