package types

import "github.com/vertex-language/mocha/sym"

// The generic signature of a symbol read from a class file.
//
// classfile decodes the Signature attribute and hoists it onto Class, Field
// and Method, but sym does not carry it forward yet, and sym.Table.load is
// unexported — so this package cannot fetch it independently without
// duplicating class path traffic per class.
//
// Three fields are needed on sym, populated where binary.go already reads the
// other hoisted attributes:
//
//	ClassSym.Signature  string  // from classfile.Class.Signature
//	MethodSym.Signature string  // from classfile.Method.Signature
//	VarSym.Signature    string  // from classfile.Field.Signature
//
// Until they land these accessors return "", which makes every non-generic
// class exact — most of android.jar — and silently erases the rest, exactly
// as though no Signature had been present. Switch each body to return the
// field once it exists; nothing else in this package changes.

func classSignature(c *sym.ClassSym) string {
	if c == nil {
		return ""
	}
	return "" // return c.Signature
}

func methodSignature(m *sym.MethodSym) string {
	if m == nil {
		return ""
	}
	return "" // return m.Signature
}

func fieldSignature(v *sym.VarSym) string {
	if v == nil {
		return ""
	}
	return "" // return v.Signature
}