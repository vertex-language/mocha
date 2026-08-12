package lower

import (
	"github.com/vertex-language/mocha/jvm/op"
	"github.com/vertex-language/mocha/types"
)

// Erasure before desugaring. javac splits TransTypes from Lower and cannot
// reorder them; the same ordering holds inside pass two, where a type is erased
// at the point it is consulted for an opcode and never before.

// convert emits the conversion from `from` to `to` for a value on top of the
// stack. Every conversion attr recorded as implicit becomes an explicit opcode
// here — §5 does not leave any of them to the VM.
func (e *emitter) convert(from, to types.Type) {
	if from == nil || to == nil || to.Kind() == types.KindVoid {
		return
	}
	fp, tp := from.Kind().IsPrimitive(), to.Kind().IsPrimitive()

	switch {
	case fp && tp:
		e.primitive(from.Kind(), to.Kind())
	case fp && !tp:
		e.box(from)
		// A boxed value may still need a widening reference conversion, but
		// Integer to Number needs no opcode: the verifier accepts it.
	case !fp && tp:
		e.unbox(to)
	default:
		e.refConvert(from, to)
	}
}

// primitive implements the widening (§5.1.2) and narrowing (§5.1.3) primitive
// conversions as single opcodes. byte, short and char reach the stack already
// promoted to int, so a conversion out of one is a conversion out of int.
func (e *emitter) primitive(from, to types.Kind) {
	if from == to {
		return
	}
	// Everything narrower than int lives on the stack as an int.
	src := from
	switch src {
	case types.KindBoolean, types.KindByte, types.KindShort, types.KindChar:
		src = types.KindInt
	}

	switch src {
	case types.KindInt:
		switch to {
		case types.KindLong:
			e.c.Op(op.I2l)
		case types.KindFloat:
			e.c.Op(op.I2f)
		case types.KindDouble:
			e.c.Op(op.I2d)
		case types.KindByte:
			e.c.Op(op.I2b)
		case types.KindChar:
			e.c.Op(op.I2c)
		case types.KindShort:
			e.c.Op(op.I2s)
		case types.KindInt, types.KindBoolean:
			// nothing: an int is an int, and a boolean is an int 0 or 1
		default:
			bug("no int conversion to %s", to)
		}

	case types.KindLong:
		switch to {
		case types.KindInt:
			e.c.Op(op.L2i)
		case types.KindFloat:
			e.c.Op(op.L2f)
		case types.KindDouble:
			e.c.Op(op.L2d)
		case types.KindByte, types.KindShort, types.KindChar:
			// §5.1.3 narrows through int, in two steps.
			e.c.Op(op.L2i)
			e.primitive(types.KindInt, to)
		default:
			bug("no long conversion to %s", to)
		}

	case types.KindFloat:
		switch to {
		case types.KindInt:
			e.c.Op(op.F2i)
		case types.KindLong:
			e.c.Op(op.F2l)
		case types.KindDouble:
			e.c.Op(op.F2d)
		case types.KindByte, types.KindShort, types.KindChar:
			e.c.Op(op.F2i)
			e.primitive(types.KindInt, to)
		default:
			bug("no float conversion to %s", to)
		}

	case types.KindDouble:
		switch to {
		case types.KindInt:
			e.c.Op(op.D2i)
		case types.KindLong:
			e.c.Op(op.D2l)
		case types.KindFloat:
			e.c.Op(op.D2f)
		case types.KindByte, types.KindShort, types.KindChar:
			e.c.Op(op.D2i)
			e.primitive(types.KindInt, to)
		default:
			bug("no double conversion to %s", to)
		}

	default:
		bug("no conversion from %s", from)
	}
}

// boxes maps a primitive kind to its wrapper and the descriptor of the valueOf
// that produces it. valueOf rather than the constructor: it is what javac emits
// from 5 on, and it hits the Integer cache.
var boxes = map[types.Kind]struct{ owner, desc, unbox, unboxDesc string }{
	types.KindBoolean: {"java/lang/Boolean", "(Z)Ljava/lang/Boolean;", "booleanValue", "()Z"},
	types.KindByte:    {"java/lang/Byte", "(B)Ljava/lang/Byte;", "byteValue", "()B"},
	types.KindShort:   {"java/lang/Short", "(S)Ljava/lang/Short;", "shortValue", "()S"},
	types.KindChar:    {"java/lang/Character", "(C)Ljava/lang/Character;", "charValue", "()C"},
	types.KindInt:     {"java/lang/Integer", "(I)Ljava/lang/Integer;", "intValue", "()I"},
	types.KindLong:    {"java/lang/Long", "(J)Ljava/lang/Long;", "longValue", "()J"},
	types.KindFloat:   {"java/lang/Float", "(F)Ljava/lang/Float;", "floatValue", "()F"},
	types.KindDouble:  {"java/lang/Double", "(D)Ljava/lang/Double;", "doubleValue", "()D"},
}

func (e *emitter) box(t types.Type) {
	b, ok := boxes[t.Kind()]
	if !ok {
		bug("cannot box %s", t)
	}
	e.c.InvokeStatic(b.owner, "valueOf", b.desc)
}

// unbox narrows a reference to the wrapper `to` demands and calls its accessor.
// The checkcast is what makes unboxing from Object legal.
func (e *emitter) unbox(to types.Type) {
	b, ok := boxes[to.Kind()]
	if !ok {
		bug("cannot unbox to %s", to)
	}
	e.c.CheckCast(b.owner)
	e.c.InvokeVirtual(b.owner, b.unbox, b.unboxDesc)
}

// refConvert emits a checkcast where one is needed. A widening reference
// conversion needs no instruction — the verifier accepts a subtype wherever a
// supertype is expected — so this fires only for a narrowing cast.
func (e *emitter) refConvert(from, to types.Type) {
	if to.Kind() == types.KindNull || types.IsError(to) {
		return
	}
	if from != nil && (from.Kind() == types.KindNull || types.IsError(from)) {
		return
	}
	if e.tt.IsSubtype(from, to) {
		return
	}
	if name := castTarget(to); name != "" {
		e.c.CheckCast(name)
	}
}

// castTarget renders the operand of a checkcast: an internal class name, or an
// array descriptor for an array type.
func castTarget(t types.Type) string {
	switch t.Kind() {
	case types.KindClass:
		if ct, ok := t.(*types.ClassType); ok {
			return ct.Binary()
		}
	case types.KindArray:
		return types.Descriptor(t).String()
	case types.KindTypeVar, types.KindIntersection:
		// A cast to a type variable is a cast to its erasure.
		return castTarget(types.Erase(t))
	}
	return ""
}