// Package types resolves mocha's type model.
//
// sym answers what is declared, under what name, in what scope. This package
// answers what it is: a descriptor string, an ast.Type or an unparsed generic
// Signature becomes a Type — a class with type arguments, an array, a type
// variable with a bound, a wildcard.
//
// # Closed hierarchy
//
// Type is closed by an unexported marker method, exactly as ast's hierarchies
// are. Consumers switch on Kind. Primitives, void and null are package-level
// singletons and compare with ==; everything else compares with Identical.
//
// # Two sources, one model
//
// A ClassType built from a class file's Signature and one built from an
// ast.ClassDecl are the same shape. Nothing above this package branches on
// which it got, which is the same promise sym makes one layer down.
package types

import (
	"strings"

	"github.com/vertex-language/mocha/sym"
)

// Kind classifies a Type. The eight primitive kinds are separate because
// attribution needs to tell int from long for widening; collapsing them into a
// single numeric kind would only move that switch into every caller.
type Kind uint8

const (
	// KindError is the kind of a type that could not be resolved. It exists
	// for the same reason sym.KindError does: a consumer gets a type, not a
	// nil, so one unresolvable name costs one diagnostic and no cascade.
	KindError Kind = iota
	KindVoid
	KindNull
	KindBoolean
	KindByte
	KindShort
	KindChar
	KindInt
	KindLong
	KindFloat
	KindDouble
	KindClass
	KindArray
	KindTypeVar
	KindWildcard
	KindIntersection
)

var kindStrings = [...]string{
	KindError:        "error",
	KindVoid:         "void",
	KindNull:         "null",
	KindBoolean:      "boolean",
	KindByte:         "byte",
	KindShort:        "short",
	KindChar:         "char",
	KindInt:          "int",
	KindLong:         "long",
	KindFloat:        "float",
	KindDouble:       "double",
	KindClass:        "class",
	KindArray:        "array",
	KindTypeVar:      "type variable",
	KindWildcard:     "wildcard",
	KindIntersection: "intersection",
}

func (k Kind) String() string {
	if int(k) < len(kindStrings) {
		return kindStrings[k]
	}
	return "Kind(?)"
}

// IsPrimitive reports whether the kind is one of the eight primitive types of
// §4.2. void and null are not primitives.
func (k Kind) IsPrimitive() bool { return k >= KindBoolean && k <= KindDouble }

// IsNumeric reports whether the kind is one of the seven numeric types.
func (k Kind) IsNumeric() bool { return k >= KindByte && k <= KindDouble }

// IsIntegral reports whether the kind is one of the five integral types.
func (k Kind) IsIntegral() bool { return k >= KindByte && k <= KindLong }

// Type is the interface every type satisfies. The unexported method closes the
// hierarchy: nothing outside this package can introduce a kind.
type Type interface {
	Kind() Kind
	String() string
	typeNode()
}

// --- primitives, void, null -------------------------------------------------

// Basic is a primitive type, void, or the null type. Every Basic is one of the
// package-level singletons below, so two Basics compare with ==.
type Basic struct {
	kind Kind
	name string
}

func (b *Basic) Kind() Kind     { return b.kind }
func (b *Basic) String() string { return b.name }
func (*Basic) typeNode()        {}

// The primitive types of §4.2, plus void and the null type of §4.1.
var (
	Boolean = &Basic{KindBoolean, "boolean"}
	Byte    = &Basic{KindByte, "byte"}
	Short   = &Basic{KindShort, "short"}
	Char    = &Basic{KindChar, "char"}
	Int     = &Basic{KindInt, "int"}
	Long    = &Basic{KindLong, "long"}
	Float   = &Basic{KindFloat, "float"}
	Double  = &Basic{KindDouble, "double"}

	// Void is the result type of a void method. It is not a type in §4's
	// sense and is the supertype of nothing.
	Void = &Basic{KindVoid, "void"}

	// Null is the type of the null literal: assignable to every reference
	// type and to no primitive.
	Null = &Basic{KindNull, "null"}
)

// PrimitiveOf returns the singleton for a primitive kind, or nil.
func PrimitiveOf(k Kind) *Basic {
	switch k {
	case KindBoolean:
		return Boolean
	case KindByte:
		return Byte
	case KindShort:
		return Short
	case KindChar:
		return Char
	case KindInt:
		return Int
	case KindLong:
		return Long
	case KindFloat:
		return Float
	case KindDouble:
		return Double
	case KindVoid:
		return Void
	case KindNull:
		return Null
	}
	return nil
}

// --- classes ----------------------------------------------------------------

// ClassType is a use of a class, interface, enum, record or annotation
// interface.
//
// A raw use and a parameterized use are the same kind: Args is nil for the
// former. §4.8 makes a raw type a degenerate parameterization rather than a
// separate thing, and giving it its own Kind would force every switch to
// handle two cases that behave identically everywhere except display.
type ClassType struct {
	// Sym is the identity. Two ClassTypes over the same Sym describe the same
	// class; Args is what makes List<String> and List<Integer> differ.
	Sym *sym.ClassSym

	// Args are the type arguments, or nil for a raw or non-generic use.
	Args []Type

	// Outer is the enclosing instance's parameterization, set only for a
	// non-static member type of a generic class: Map<String,Integer>.Entry.
	// A top-level or static nested class always has nil here.
	Outer *ClassType
}

func (*ClassType) Kind() Kind { return KindClass }
func (*ClassType) typeNode()  {}

// Binary is the internal-form name of the class, or "" for an unbound type.
func (c *ClassType) Binary() string {
	if c.Sym == nil {
		return ""
	}
	return c.Sym.Binary
}

// IsRaw reports whether the class declares type parameters that this use did
// not supply. A non-generic class is never raw.
func (c *ClassType) IsRaw() bool { return len(c.Args) == 0 }

func (c *ClassType) String() string {
	var sb strings.Builder
	if c.Outer != nil {
		sb.WriteString(c.Outer.String())
		sb.WriteByte('.')
		sb.WriteString(sym.SimpleName(c.Binary()))
	} else {
		sb.WriteString(sym.Dotted(c.Binary()))
	}
	writeArgs(&sb, c.Args)
	return sb.String()
}

func writeArgs(sb *strings.Builder, args []Type) {
	if len(args) == 0 {
		return
	}
	sb.WriteByte('<')
	for i, a := range args {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(a.String())
	}
	sb.WriteByte('>')
}

// --- arrays -----------------------------------------------------------------

// ArrayType is an array type. Dimensions nest one at a time — String[][] is an
// ArrayType whose Elem is an ArrayType — because subtyping and erasure both
// recurse a layer per step.
type ArrayType struct{ Elem Type }

func (*ArrayType) Kind() Kind      { return KindArray }
func (*ArrayType) typeNode()       {}
func (a *ArrayType) String() string { return a.Elem.String() + "[]" }

// NewArray returns an array type with the given element type.
func NewArray(elem Type) *ArrayType { return &ArrayType{Elem: elem} }

// arrayOf wraps elem in n dimensions.
func arrayOf(elem Type, n int) Type {
	for i := 0; i < n; i++ {
		elem = &ArrayType{Elem: elem}
	}
	return elem
}

// --- type variables ---------------------------------------------------------

// TypeVar is a type parameter of a class or method, as used.
//
// Bound is never nil once the declaring class has been completed: a parameter
// written without a bound gets java.lang.Object, and several bounds collapse
// into one Intersection rather than a slice here. One bound field, uniformly.
type TypeVar struct {
	Sym   *sym.TypeParamSym
	Bound Type
}

func (*TypeVar) Kind() Kind { return KindTypeVar }
func (*TypeVar) typeNode()  {}

func (v *TypeVar) String() string {
	if v.Sym == nil {
		return "?"
	}
	return v.Sym.Name
}

// --- wildcards --------------------------------------------------------------

// WildcardKind selects among the three forms of §4.5.1.
type WildcardKind uint8

const (
	Unbounded WildcardKind = iota
	Extends
	Super
)

// Wildcard is a type argument of the form ?, ? extends T, or ? super T. It
// appears only inside ClassType.Args: a wildcard is never the type of an
// expression.
type Wildcard struct {
	Wild  WildcardKind
	Bound Type // nil when Unbounded
}

func (*Wildcard) Kind() Kind { return KindWildcard }
func (*Wildcard) typeNode()  {}

func (w *Wildcard) String() string {
	switch w.Wild {
	case Extends:
		return "? extends " + w.Bound.String()
	case Super:
		return "? super " + w.Bound.String()
	}
	return "?"
}

// --- intersections ----------------------------------------------------------

// Intersection is T1 & T2 & …: a type parameter with several bounds, or an
// intersection cast target.
type Intersection struct{ Bounds []Type }

func (*Intersection) Kind() Kind { return KindIntersection }
func (*Intersection) typeNode()  {}

func (x *Intersection) String() string {
	parts := make([]string, len(x.Bounds))
	for i, b := range x.Bounds {
		parts[i] = b.String()
	}
	return strings.Join(parts, " & ")
}

// intersect collapses a bound list: none is nil, one is itself, more is an
// Intersection. Callers pass the class bound first, per §4.4.
func intersect(bounds []Type) Type {
	switch len(bounds) {
	case 0:
		return nil
	case 1:
		return bounds[0]
	}
	return &Intersection{Bounds: bounds}
}

// --- errors -----------------------------------------------------------------

// ErrorType stands in for a type that could not be resolved. Handing one back
// rather than nil is what keeps one bad import from producing an error at
// every use of the type it failed to name.
type ErrorType struct {
	// Sought is the name that was looked up, for diagnostics.
	Sought string
}

func (*ErrorType) Kind() Kind { return KindError }
func (*ErrorType) typeNode()  {}

func (e *ErrorType) String() string {
	if e.Sought == "" {
		return "<error>"
	}
	return "<error: " + sym.Dotted(e.Sought) + ">"
}

// errorType returns an error type for a name.
func errorType(sought string) *ErrorType { return &ErrorType{Sought: sought} }

// IsError reports whether t is an error type. Every consumer should check this
// before trusting a resolution result.
func IsError(t Type) bool {
	if t == nil {
		return true
	}
	return t.Kind() == KindError
}

// IsReference reports whether t is a reference type: a class, an array, a type
// variable, an intersection, or null.
func IsReference(t Type) bool {
	switch t.Kind() {
	case KindClass, KindArray, KindTypeVar, KindIntersection, KindNull:
		return true
	}
	return false
}

// --- methods ----------------------------------------------------------------

// MethodType is a method's signature: its own type parameters, its parameter
// types, its result, and its declared exceptions.
//
// Result is Void for a void method, never nil.
type MethodType struct {
	TypeParams []*TypeVar
	Params     []Type
	Result     Type
	Throws     []Type
}

func (mt *MethodType) String() string {
	var sb strings.Builder
	if len(mt.TypeParams) > 0 {
		sb.WriteByte('<')
		for i, p := range mt.TypeParams {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(p.String())
		}
		sb.WriteString("> ")
	}
	sb.WriteString(mt.Result.String())
	sb.WriteByte('(')
	for i, p := range mt.Params {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(p.String())
	}
	sb.WriteByte(')')
	if len(mt.Throws) > 0 {
		sb.WriteString(" throws ")
		for i, x := range mt.Throws {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(x.String())
		}
	}
	return sb.String()
}

// --- identity ---------------------------------------------------------------

// Identical reports whether two types are the same type (§4.3.4). Type
// arguments are compared, so List<String> and List<Integer> are not identical
// and neither is identical to raw List.
func Identical(a, b Type) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil || a.Kind() != b.Kind() {
		return false
	}
	switch x := a.(type) {
	case *Basic:
		return x == b // singletons

	case *ClassType:
		y := b.(*ClassType)
		if x.Sym != y.Sym || len(x.Args) != len(y.Args) {
			return false
		}
		for i := range x.Args {
			if !Identical(x.Args[i], y.Args[i]) {
				return false
			}
		}
		if (x.Outer == nil) != (y.Outer == nil) {
			return false
		}
		if x.Outer != nil {
			return Identical(x.Outer, y.Outer)
		}
		return true

	case *ArrayType:
		return Identical(x.Elem, b.(*ArrayType).Elem)

	case *TypeVar:
		return x.Sym == b.(*TypeVar).Sym

	case *Wildcard:
		y := b.(*Wildcard)
		if x.Wild != y.Wild {
			return false
		}
		if x.Bound == nil || y.Bound == nil {
			return x.Bound == y.Bound
		}
		return Identical(x.Bound, y.Bound)

	case *Intersection:
		y := b.(*Intersection)
		if len(x.Bounds) != len(y.Bounds) {
			return false
		}
		for i := range x.Bounds {
			if !Identical(x.Bounds[i], y.Bounds[i]) {
				return false
			}
		}
		return true

	case *ErrorType:
		return x.Sought == b.(*ErrorType).Sought
	}
	return false
}