package types

import (
	"github.com/vertex-language/mocha/jvm/desc"
)

// Erase implements the erasure of §4.6: a class type drops its arguments and
// its enclosing parameterization, a type variable erases to its bound, an
// array erases its element, and everything else erases to itself.
//
// It needs no table because TypeVar.Bound is never nil after completion — a
// parameter written without a bound already carries java.lang.Object.
func Erase(t Type) Type {
	switch n := t.(type) {
	case *ClassType:
		if n.Args == nil && n.Outer == nil {
			return n
		}
		return &ClassType{Sym: n.Sym}

	case *ArrayType:
		e := Erase(n.Elem)
		if e == n.Elem {
			return n
		}
		return &ArrayType{Elem: e}

	case *TypeVar:
		if n.Bound == nil {
			return n
		}
		return Erase(n.Bound)

	case *Intersection:
		// §4.6: the erasure of an intersection is the erasure of its leftmost
		// bound, which is why the class bound is stored first.
		if len(n.Bounds) == 0 {
			return n
		}
		return Erase(n.Bounds[0])

	case *Wildcard:
		if n.Bound != nil {
			return Erase(n.Bound)
		}
		return n
	}
	return t
}

// EraseMethod erases every part of a method signature, dropping its own type
// parameters. This is what a descriptor describes.
func EraseMethod(mt *MethodType) *MethodType {
	if mt == nil {
		return nil
	}
	out := &MethodType{Result: Erase(mt.Result)}
	for _, p := range mt.Params {
		out.Params = append(out.Params, Erase(p))
	}
	for _, x := range mt.Throws {
		out.Throws = append(out.Throws, Erase(x))
	}
	return out
}

// Descriptor converts a type to its JVM field descriptor form, bridging into
// jvm/desc. The result is the same desc.Type shape classfile.Builder validates
// against, so an erased type goes into a FieldBuilder or MethodBuilder
// unchanged.
//
// A type that cannot be described — an unresolved name, or `var` before
// inference — yields java/lang/Object rather than an invalid descriptor. attr
// has already reported the underlying failure; emitting a broken descriptor
// here would turn one diagnostic into a class file that will not load.
func Descriptor(t Type) desc.Type {
	switch n := Erase(t).(type) {
	case *Basic:
		return desc.Type{Kind: descKind(n.kind)}

	case *ClassType:
		if n.Sym == nil {
			return objectDesc()
		}
		return desc.Type{Kind: desc.Object, Name: n.Sym.Binary}

	case *ArrayType:
		inner := Descriptor(n.Elem)
		inner.Dims++
		return inner

	case *TypeVar, *Intersection, *Wildcard, *ErrorType:
		return objectDesc()
	}
	return objectDesc()
}

func objectDesc() desc.Type {
	return desc.Type{Kind: desc.Object, Name: "java/lang/Object"}
}

func descKind(k Kind) desc.Kind {
	switch k {
	case KindVoid:
		return desc.Void
	case KindBoolean:
		return desc.Boolean
	case KindByte:
		return desc.Byte
	case KindChar:
		return desc.Char
	case KindShort:
		return desc.Short
	case KindInt:
		return desc.Int
	case KindLong:
		return desc.Long
	case KindFloat:
		return desc.Float
	case KindDouble:
		return desc.Double
	}
	// The null type has no descriptor; it only ever flows into a reference
	// position, where Object is the right erasure.
	return desc.Object
}

// MethodDescriptor renders a method's erased descriptor.
func MethodDescriptor(mt *MethodType) string {
	if mt == nil {
		return "()V"
	}
	m := desc.Method{Ret: Descriptor(mt.Result)}
	for _, p := range mt.Params {
		m.Params = append(m.Params, Descriptor(p))
	}
	return m.String()
}

// Slots is the number of local or operand stack slots a type occupies: two for
// long and double, zero for void, one otherwise.
func Slots(t Type) int {
	switch t.Kind() {
	case KindVoid:
		return 0
	case KindLong, KindDouble:
		return 2
	}
	return 1
}