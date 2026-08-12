package types

import (
	"github.com/vertex-language/mocha/jvm/desc"
	"github.com/vertex-language/mocha/sym"
)

// fromDescriptor resolves a field descriptor. Generics never appear here —
// a descriptor is already erased, which is exactly why it is the fallback
// whenever a Signature is absent or unparseable.
func (t *Table) fromDescriptor(s string) Type {
	if s == "" {
		return errorType("")
	}
	d, err := desc.ParseField(s)
	if err != nil {
		return errorType(s)
	}
	return t.fromDescType(d)
}

func (t *Table) fromDescType(d desc.Type) Type {
	var base Type
	switch d.Kind {
	case desc.Void:
		base = Void
	case desc.Boolean:
		base = Boolean
	case desc.Byte:
		base = Byte
	case desc.Char:
		base = Char
	case desc.Short:
		base = Short
	case desc.Int:
		base = Int
	case desc.Long:
		base = Long
	case desc.Float:
		base = Float
	case desc.Double:
		base = Double
	case desc.Object:
		base = t.named(d.Name)
	default:
		return errorType("")
	}
	return arrayOf(base, d.Dims)
}

// fromMethodDescriptor builds a MethodType from the erased descriptor alone.
func (t *Table) fromMethodDescriptor(m *sym.MethodSym) *MethodType {
	mt := &MethodType{Result: Void}
	d, err := desc.ParseMethod(m.Descriptor)
	if err != nil {
		mt.Result = errorType(m.Descriptor)
		return mt
	}
	for _, p := range d.Params {
		mt.Params = append(mt.Params, t.fromDescType(p))
	}
	mt.Result = t.fromDescType(d.Ret)
	for _, x := range m.Throws {
		mt.Throws = append(mt.Throws, t.named(x))
	}
	return mt
}

// fromSig resolves a parsed signature node against an environment.
func (t *Table) fromSig(e *env, s sigType) Type {
	switch n := s.(type) {
	case nil:
		return errorType("")

	case *sigBase:
		switch n.ch {
		case 'V':
			return Void
		case 'Z':
			return Boolean
		case 'B':
			return Byte
		case 'C':
			return Char
		case 'S':
			return Short
		case 'I':
			return Int
		case 'J':
			return Long
		case 'F':
			return Float
		case 'D':
			return Double
		}
		return errorType("")

	case *sigArray:
		return &ArrayType{Elem: t.fromSig(e, n.elem)}

	case *sigVar:
		if tv := e.lookupVar(n.name); tv != nil {
			return tv
		}
		// A signature naming a variable no enclosing declaration declares is
		// malformed. Erasing to Object is what a runtime does; so do we.
		return t.Object()

	case *sigClass:
		return t.fromSigClass(e, n)
	}
	return errorType("")
}

// fromSigClass walks a ClassTypeSignature and its suffix chain. Each suffix is
// one nesting step, and each carries its own arguments independent of the
// enclosing one — which is where ClassType.Outer comes from on this side.
func (t *Table) fromSigClass(e *env, n *sigClass) Type {
	c := t.syms.Class(n.name)
	if c == nil {
		return errorType(n.name)
	}
	cur := &ClassType{Sym: c, Args: t.sigArgs(e, n.args, c)}

	for _, suf := range n.suffix {
		binary := sym.NestedBinary(cur.Sym.Binary, suf.name)
		next := t.syms.Class(binary)
		if next == nil {
			return errorType(binary)
		}
		var outer *ClassType
		if !next.Flags.Has(sym.FlagStatic) {
			outer = cur
		}
		cur = &ClassType{Sym: next, Args: t.sigArgs(e, suf.args, next), Outer: outer}
	}
	return cur
}

func (t *Table) sigArgs(e *env, args []sigArg, owner *sym.ClassSym) []Type {
	if len(args) == 0 {
		return nil
	}
	out := make([]Type, 0, len(args))
	for _, a := range args {
		switch a.wild {
		case '*':
			out = append(out, &Wildcard{Wild: Unbounded})
		case '+':
			out = append(out, &Wildcard{Wild: Extends, Bound: t.fromSig(e, a.typ)})
		case '-':
			out = append(out, &Wildcard{Wild: Super, Bound: t.fromSig(e, a.typ)})
		default:
			out = append(out, t.fromSig(e, a.typ))
		}
	}
	return out
}