package types

import "github.com/vertex-language/mocha/classfile"

// Constant is a compile-time constant value together with its type.
//
// It is one shape whether the value came from a class file's ConstantValue
// attribute or from folding a source expression. Folding a source expression
// is attr's job, not this package's — sym says as much where it declares
// VarSym.Const. This package supplies the shape and the accessor for the
// binary case.
type Constant struct {
	Type  Type
	Value any // int32, int64, float32, float64, string, or bool
}

// FromClassfile converts a ConstantValue payload.
//
// Only the five tags §4.7.2 admits for a field constant can appear here, and
// classfile has already rejected the rest before a ConstantValue attribute is
// built — so a tag outside that set means the caller passed something from an
// ldc operand instead, and gets a zero Constant rather than a guess.
//
// A boolean, byte, short or char constant is stored as CONSTANT_Integer, so
// the declared field type is what recovers the narrower type. Pass it as
// declared; it is used only to reinterpret an integer payload.
func (t *Table) FromClassfile(c *classfile.Const, declared Type) Constant {
	if c == nil {
		return Constant{}
	}
	switch c.Tag {
	case classfile.TagInteger:
		v := c.Int()
		switch declared.Kind() {
		case KindBoolean:
			return Constant{Type: Boolean, Value: v != 0}
		case KindByte:
			return Constant{Type: Byte, Value: v}
		case KindShort:
			return Constant{Type: Short, Value: v}
		case KindChar:
			return Constant{Type: Char, Value: v}
		}
		return Constant{Type: Int, Value: v}

	case classfile.TagLong:
		return Constant{Type: Long, Value: c.Long()}

	case classfile.TagFloat:
		return Constant{Type: Float, Value: c.Float()}

	case classfile.TagDouble:
		return Constant{Type: Double, Value: c.Double()}

	case classfile.TagString:
		return Constant{Type: t.String_(), Value: c.Str}
	}
	return Constant{}
}

// IsValid reports whether the constant carries a value.
func (c Constant) IsValid() bool { return c.Value != nil }

// Int returns the value as an int32, for the five integral-ish types that are
// stored as CONSTANT_Integer.
func (c Constant) Int() (int32, bool) {
	switch v := c.Value.(type) {
	case int32:
		return v, true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// Str returns the value as a string.
func (c Constant) Str() (string, bool) {
	s, ok := c.Value.(string)
	return s, ok
}