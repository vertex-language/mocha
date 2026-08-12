package attr

import (
	"strconv"
	"strings"

	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// Constant expressions, §15.29.
//
// Folding is not an optimisation here. Three things require it: a case label
// must be a constant, a static final field of primitive or String type becomes
// a ConstantValue attribute, and §5.2's implicit narrowing applies only to a
// constant expression. gen would fold anyway; this is where the language says
// it must.
//
// The literal is decoded here rather than in the scanner because scanner keeps
// every literal raw on purpose — normalising a text block and interpreting
// escapes are separate transformations that happen in a fixed order, and none
// of them belongs in tokenisation.

func (a *attributor) fold(e *env, x ast.Expr) {
	k, ok := a.constOf(e, x)
	if ok && k.IsValid() {
		a.info.Consts[x] = k
	}
}

func (a *attributor) constOf(e *env, x ast.Expr) (types.Constant, bool) {
	t := a.info.Type(x)

	switch n := x.(type) {
	case *ast.BasicLit:
		return a.literalValue(e, n, t)

	case *ast.ParenExpr:
		k, ok := a.info.Const(n.X)
		return k, ok

	case *ast.Ident, *ast.Name, *ast.SelectorExpr:
		// A reference to a constant variable is itself a constant expression.
		if v, ok := a.info.Use(x).(interface{ Base() *symBase }); ok {
			_ = v
		}
		return a.constOfVar(x)

	case *ast.UnaryExpr:
		return a.foldUnary(n, t)

	case *ast.BinaryExpr:
		return a.foldBinary(n, t)

	case *ast.CondExpr:
		c, ok := a.info.Const(n.Cond)
		if !ok {
			return types.Constant{}, false
		}
		b, ok := c.Int()
		if !ok {
			return types.Constant{}, false
		}
		if b != 0 {
			return a.info.Const(n.Then)
		}
		return a.info.Const(n.Else)

	case *ast.CastExpr:
		// §15.29 admits a cast to a primitive or String in a constant
		// expression, and nothing else.
		if !isConstantVarType(t) {
			return types.Constant{}, false
		}
		k, ok := a.info.Const(n.X)
		if !ok {
			return types.Constant{}, false
		}
		return a.coerce(k, t), true
	}
	return types.Constant{}, false
}

// literalValue decodes a literal's raw spelling. Underscores were validated by
// the scanner but not removed, so they come out here.
func (a *attributor) literalValue(e *env, n *ast.BasicLit, t types.Type) (types.Constant, bool) {
	f := e.file()
	if f == nil {
		return types.Constant{}, false
	}
	raw := f.Slice(n.Pos(), n.End())

	switch n.Kind {
	case token.TRUE:
		return types.Constant{Type: types.Boolean, Value: true}, true
	case token.FALSE:
		return types.Constant{Type: types.Boolean, Value: false}, true
	case token.NULL:
		return types.Constant{}, false

	case token.INT:
		v, ok := parseIntLit(raw)
		if !ok {
			return types.Constant{}, false
		}
		if t.Kind() == types.KindLong {
			return types.Constant{Type: types.Long, Value: v}, true
		}
		return types.Constant{Type: types.Int, Value: int32(v)}, true

	case token.FLOAT:
		s := strings.TrimRight(strings.ReplaceAll(raw, "_", ""), "fFdD")
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return types.Constant{}, false
		}
		if t.Kind() == types.KindFloat {
			return types.Constant{Type: types.Float, Value: float32(v)}, true
		}
		return types.Constant{Type: types.Double, Value: v}, true

	case token.CHAR:
		r, ok := parseCharLit(raw)
		if !ok {
			return types.Constant{}, false
		}
		return types.Constant{Type: types.Char, Value: int32(r)}, true

	case token.STRING:
		s, ok := parseStringLit(raw)
		if !ok {
			return types.Constant{}, false
		}
		return types.Constant{Type: t, Value: s}, true

	case token.TEXTBLOCK:
		s, ok := parseTextBlock(raw)
		if !ok {
			return types.Constant{}, false
		}
		return types.Constant{Type: t, Value: s}, true
	}
	return types.Constant{}, false
}

func (a *attributor) foldUnary(n *ast.UnaryExpr, t types.Type) (types.Constant, bool) {
	k, ok := a.info.Const(n.X)
	if !ok {
		return types.Constant{}, false
	}
	switch n.Op {
	case token.ADD:
		return a.coerce(k, t), true
	case token.SUB:
		return negate(k, t)
	case token.TILDE:
		if v, ok := k.Int(); ok && t.Kind() == types.KindInt {
			return types.Constant{Type: types.Int, Value: ^v}, true
		}
		if v, ok := k.Value.(int64); ok {
			return types.Constant{Type: types.Long, Value: ^v}, true
		}
	case token.NOT:
		if b, ok := k.Value.(bool); ok {
			return types.Constant{Type: types.Boolean, Value: !b}, true
		}
	}
	return types.Constant{}, false
}

func (a *attributor) foldBinary(n *ast.BinaryExpr, t types.Type) (types.Constant, bool) {
	lk, lok := a.info.Const(n.X)
	rk, rok := a.info.Const(n.Y)
	if !lok || !rok {
		return types.Constant{}, false
	}

	// String concatenation of two constants is itself constant, which is what
	// makes `static final String X = "a" + "b";` a ConstantValue.
	if a.isString(t) {
		ls, lo := stringOf(lk)
		rs, ro := stringOf(rk)
		if lo && ro {
			return types.Constant{Type: t, Value: ls + rs}, true
		}
		return types.Constant{}, false
	}

	if lb, ok := lk.Value.(bool); ok {
		rb, ok2 := rk.Value.(bool)
		if !ok2 {
			return types.Constant{}, false
		}
		switch n.Op {
		case token.LAND, token.AND:
			return types.Constant{Type: types.Boolean, Value: lb && rb}, true
		case token.LOR, token.OR:
			return types.Constant{Type: types.Boolean, Value: lb || rb}, true
		case token.XOR:
			return types.Constant{Type: types.Boolean, Value: lb != rb}, true
		case token.EQL:
			return types.Constant{Type: types.Boolean, Value: lb == rb}, true
		case token.NEQ:
			return types.Constant{Type: types.Boolean, Value: lb != rb}, true
		}
		return types.Constant{}, false
	}

	// Integral arithmetic is done in int64 and truncated to the promoted type,
	// which reproduces Java's wrapping without a special case per width.
	l, lo := integerOf(lk)
	r, ro := integerOf(rk)
	if !lo || !ro {
		return a.foldFloat(n, lk, rk, t)
	}

	var v int64
	switch n.Op {
	case token.ADD:
		v = l + r
	case token.SUB:
		v = l - r
	case token.MUL:
		v = l * r
	case token.QUO:
		if r == 0 {
			return types.Constant{}, false // not constant; it throws
		}
		v = l / r
	case token.REM:
		if r == 0 {
			return types.Constant{}, false
		}
		v = l % r
	case token.AND:
		v = l & r
	case token.OR:
		v = l | r
	case token.XOR:
		v = l ^ r
	case token.SHL:
		v = l << shiftCount(r, t)
	case token.SHR:
		v = l >> shiftCount(r, t)
	case token.USHR:
		if t.Kind() == types.KindLong {
			v = int64(uint64(l) >> shiftCount(r, t))
		} else {
			v = int64(uint32(int32(l)) >> shiftCount(r, t))
		}
	case token.EQL:
		return types.Constant{Type: types.Boolean, Value: l == r}, true
	case token.NEQ:
		return types.Constant{Type: types.Boolean, Value: l != r}, true
	case token.LSS:
		return types.Constant{Type: types.Boolean, Value: l < r}, true
	case token.GTR:
		return types.Constant{Type: types.Boolean, Value: l > r}, true
	case token.LEQ:
		return types.Constant{Type: types.Boolean, Value: l <= r}, true
	case token.GEQ:
		return types.Constant{Type: types.Boolean, Value: l >= r}, true
	default:
		return types.Constant{}, false
	}

	if t.Kind() == types.KindLong {
		return types.Constant{Type: types.Long, Value: v}, true
	}
	return types.Constant{Type: types.Int, Value: int32(v)}, true
}

// shiftCount masks the distance as §15.19 requires: the low five bits for an
// int shift, the low six for a long. This is the rule that makes `1 << 32`
// equal 1 rather than 0.
func shiftCount(r int64, t types.Type) uint {
	if t.Kind() == types.KindLong {
		return uint(r & 63)
	}
	return uint(r & 31)
}

func (a *attributor) foldFloat(n *ast.BinaryExpr, lk, rk types.Constant, t types.Type) (types.Constant, bool) {
	l, lo := floatOf(lk)
	r, ro := floatOf(rk)
	if !lo || !ro {
		return types.Constant{}, false
	}
	var v float64
	switch n.Op {
	case token.ADD:
		v = l + r
	case token.SUB:
		v = l - r
	case token.MUL:
		v = l * r
	case token.QUO:
		// Division by zero is not an error for floating point: it yields an
		// infinity, and the expression stays constant.
		v = l / r
	case token.EQL:
		return types.Constant{Type: types.Boolean, Value: l == r}, true
	case token.NEQ:
		return types.Constant{Type: types.Boolean, Value: l != r}, true
	case token.LSS:
		return types.Constant{Type: types.Boolean, Value: l < r}, true
	case token.GTR:
		return types.Constant{Type: types.Boolean, Value: l > r}, true
	case token.LEQ:
		return types.Constant{Type: types.Boolean, Value: l <= r}, true
	case token.GEQ:
		return types.Constant{Type: types.Boolean, Value: l >= r}, true
	default:
		return types.Constant{}, false
	}
	if t.Kind() == types.KindFloat {
		return types.Constant{Type: types.Float, Value: float32(v)}, true
	}
	return types.Constant{Type: types.Double, Value: v}, true
}

// coerce reinterprets a constant at a narrower or wider type, which is what a
// cast in a constant expression does.
func (a *attributor) coerce(k types.Constant, to types.Type) types.Constant {
	switch to.Kind() {
	case types.KindByte:
		if v, ok := integerOf(k); ok {
			return types.Constant{Type: types.Byte, Value: int32(int8(v))}
		}
	case types.KindShort:
		if v, ok := integerOf(k); ok {
			return types.Constant{Type: types.Short, Value: int32(int16(v))}
		}
	case types.KindChar:
		if v, ok := integerOf(k); ok {
			return types.Constant{Type: types.Char, Value: int32(uint16(v))}
		}
	case types.KindInt:
		if v, ok := integerOf(k); ok {
			return types.Constant{Type: types.Int, Value: int32(v)}
		}
	case types.KindLong:
		if v, ok := integerOf(k); ok {
			return types.Constant{Type: types.Long, Value: v}
		}
	case types.KindFloat:
		if v, ok := floatOf(k); ok {
			return types.Constant{Type: types.Float, Value: float32(v)}
		}
	case types.KindDouble:
		if v, ok := floatOf(k); ok {
			return types.Constant{Type: types.Double, Value: v}
		}
	}
	return k
}

func negate(k types.Constant, t types.Type) (types.Constant, bool) {
	if v, ok := integerOf(k); ok {
		if t.Kind() == types.KindLong {
			return types.Constant{Type: types.Long, Value: -v}, true
		}
		return types.Constant{Type: types.Int, Value: int32(-v)}, true
	}
	if v, ok := floatOf(k); ok {
		if t.Kind() == types.KindFloat {
			return types.Constant{Type: types.Float, Value: float32(-v)}, true
		}
		return types.Constant{Type: types.Double, Value: -v}, true
	}
	return types.Constant{}, false
}

func integerOf(k types.Constant) (int64, bool) {
	switch v := k.Value.(type) {
	case int32:
		return int64(v), true
	case int64:
		return v, true
	}
	return 0, false
}

func floatOf(k types.Constant) (float64, bool) {
	switch v := k.Value.(type) {
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

func stringOf(k types.Constant) (string, bool) {
	switch v := k.Value.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case int32:
		return strconv.FormatInt(int64(v), 10), true
	case int64:
		return strconv.FormatInt(v, 10), true
	}
	return "", false
}

// parseIntLit decodes §3.10.1, honouring the four radices and stripping the
// underscores the scanner validated but left in place.
func parseIntLit(raw string) (int64, bool) {
	s := strings.ReplaceAll(raw, "_", "")
	s = strings.TrimRight(s, "lL")
	base := 10
	switch {
	case strings.HasPrefix(s, "0x"), strings.HasPrefix(s, "0X"):
		s, base = s[2:], 16
	case strings.HasPrefix(s, "0b"), strings.HasPrefix(s, "0B"):
		s, base = s[2:], 2
	case len(s) > 1 && s[0] == '0':
		s, base = s[1:], 8
	}
	// ParseUint, not ParseInt: 0x80000000 and 9223372036854775808L are both
	// legal literals whose values do not fit a signed parse, and the sign is
	// applied by the enclosing unary minus.
	v, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		return 0, false
	}
	return int64(v), true
}