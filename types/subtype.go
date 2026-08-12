package types

import "github.com/vertex-language/mocha/sym"

// IsSubtype reports whether sub is a subtype of sup.
//
// This is the nominal half of §4.10: the class and interface hierarchy, array
// covariance, primitive widening, and null. Two class types over the same
// symbol compare as if raw — type arguments are ignored entirely.
//
// Generic containment (List<String> is a subtype of List<? extends Object> but
// not of List<Object>) and inference are deliberately absent. Both need a
// constraint solver over type arguments, and that solver belongs next to
// overload resolution and target typing, which live in attr.
func (t *Table) IsSubtype(sub, sup Type) bool {
	if sub == nil || sup == nil {
		return false
	}
	if Identical(sub, sup) {
		return true
	}
	// An error type is compatible with everything, so one unresolved name does
	// not produce a second diagnostic at every use.
	if IsError(sub) || IsError(sup) {
		return true
	}

	if sub.Kind() == KindNull {
		return IsReference(sup)
	}
	if sub.Kind().IsPrimitive() || sup.Kind().IsPrimitive() {
		return Widens(sub, sup)
	}

	switch s := sub.(type) {
	case *ArrayType:
		return t.arraySubtype(s, sup)

	case *TypeVar:
		// A variable is a subtype of whatever its bound is.
		return s.Bound != nil && t.IsSubtype(s.Bound, sup)

	case *Intersection:
		for _, b := range s.Bounds {
			if t.IsSubtype(b, sup) {
				return true
			}
		}
		return false

	case *Wildcard:
		if s.Wild == Extends && s.Bound != nil {
			return t.IsSubtype(s.Bound, sup)
		}
		return sup.Kind() == KindClass && isObject(sup)

	case *ClassType:
		sc, ok := sup.(*ClassType)
		if !ok {
			return false
		}
		return t.classSubtype(s, sc, make(map[*sym.ClassSym]bool))
	}
	return false
}

// arraySubtype implements §10.8 and §4.10.3: arrays are covariant in their
// element type, and every array is a subtype of Object, Cloneable and
// Serializable and of nothing else.
func (t *Table) arraySubtype(s *ArrayType, sup Type) bool {
	if a, ok := sup.(*ArrayType); ok {
		// Primitive element types are invariant: int[] is not a byte[].
		if s.Elem.Kind().IsPrimitive() || a.Elem.Kind().IsPrimitive() {
			return Identical(s.Elem, a.Elem)
		}
		return t.IsSubtype(s.Elem, a.Elem)
	}
	c, ok := sup.(*ClassType)
	if !ok || c.Sym == nil {
		return false
	}
	switch c.Sym.Binary {
	case sym.ObjectName, "java/lang/Cloneable", sym.SerializableName:
		return true
	}
	return false
}

// classSubtype walks the supertype graph. The visited set is the cycle guard:
// a hierarchy in which A extends B and B extends A is malformed, but it
// reaches this package as data and must not loop.
func (t *Table) classSubtype(sub, sup *ClassType, seen map[*sym.ClassSym]bool) bool {
	if sub.Sym == nil || sup.Sym == nil {
		return false
	}
	if sub.Sym == sup.Sym {
		return true
	}
	if seen[sub.Sym] {
		return false
	}
	seen[sub.Sym] = true

	// Every reference type is a subtype of Object, including an interface,
	// which has no superclass to walk to.
	if sup.Sym.Binary == sym.ObjectName {
		return true
	}

	if s := t.Supertype(sub.Sym); s != nil {
		if sc, ok := s.(*ClassType); ok && t.classSubtype(sc, sup, seen) {
			return true
		}
	}
	for _, i := range t.Interfaces(sub.Sym) {
		if ic, ok := i.(*ClassType); ok && t.classSubtype(ic, sup, seen) {
			return true
		}
	}
	return false
}

func isObject(t Type) bool {
	c, ok := t.(*ClassType)
	return ok && c.Sym != nil && c.Sym.Binary == sym.ObjectName
}

// Supers returns every supertype of a class, transitively, in breadth-first
// order and without repeats. attr uses it for inherited member lookup, which
// this package deliberately does not do itself.
func (t *Table) Supers(c *sym.ClassSym) []*ClassType {
	if c == nil {
		return nil
	}
	var out []*ClassType
	seen := map[*sym.ClassSym]bool{c: true}
	queue := []*sym.ClassSym{c}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		visit := func(x Type) {
			ct, ok := x.(*ClassType)
			if !ok || ct.Sym == nil || seen[ct.Sym] {
				return
			}
			seen[ct.Sym] = true
			out = append(out, ct)
			queue = append(queue, ct.Sym)
		}
		if s := t.Supertype(cur); s != nil {
			visit(s)
		}
		for _, i := range t.Interfaces(cur) {
			visit(i)
		}
	}
	return out
}

// Widens implements the widening primitive conversions of §5.1.2, plus the
// identity conversion. It is false for anything involving a reference type.
//
// byte to char is deliberately absent: the two are both one-byte-ish and look
// symmetric, but §5.1.2 does not admit it, because char is unsigned.
func Widens(from, to Type) bool {
	if from == nil || to == nil {
		return false
	}
	if from == to {
		return true
	}
	f, tk := from.Kind(), to.Kind()
	if !f.IsPrimitive() || !tk.IsPrimitive() {
		return false
	}
	switch f {
	case KindByte:
		return tk == KindShort || tk == KindInt || tk == KindLong ||
			tk == KindFloat || tk == KindDouble
	case KindShort, KindChar:
		return tk == KindInt || tk == KindLong || tk == KindFloat || tk == KindDouble
	case KindInt:
		return tk == KindLong || tk == KindFloat || tk == KindDouble
	case KindLong:
		return tk == KindFloat || tk == KindDouble
	case KindFloat:
		return tk == KindDouble
	}
	return false
}

// Promote implements unary numeric promotion (§5.6.1): byte, short and char
// become int, and everything else numeric keeps its type.
func Promote(t Type) Type {
	switch t.Kind() {
	case KindByte, KindShort, KindChar:
		return Int
	}
	return t
}

// PromoteBinary implements binary numeric promotion (§5.6.2).
func PromoteBinary(a, b Type) Type {
	ka, kb := a.Kind(), b.Kind()
	switch {
	case ka == KindDouble || kb == KindDouble:
		return Double
	case ka == KindFloat || kb == KindFloat:
		return Float
	case ka == KindLong || kb == KindLong:
		return Long
	}
	return Int
}