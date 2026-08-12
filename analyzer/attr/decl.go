package attr

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// fileLike is *token.File under a shorter name, for the handful of signatures
// below that thread a file through without needing anything else from it.
type fileLike = *token.File

// modifierFlags converts a written modifier list. sym.Enter already does this
// for every declaration it enters; attr needs the same mapping for the
// declarations it enters itself — a local variable, an exception parameter, a
// resource — which sym.Enter never sees because a method body is not walked
// there. Delegating to sym.ModifierFlags keeps the JLS modifier table in one
// place rather than two that can drift apart.
func modifierFlags(m *ast.Modifiers) sym.Flags { return sym.ModifierFlags(m) }

// joinParts renders Parts[from:to) of a dotted name, joined by sep — the
// internal form a package-or-nested-class lookup needs. resolveName's
// reclassification tries this against progressively longer prefixes, because
// only the class path can say where the package name ends and the class name
// begins (§6.5.2).
func joinParts(n *ast.Name, from, to int, sep string, f fileLike) string {
	s := ""
	for i := from; i < to; i++ {
		if i > from {
			s += sep
		}
		s += n.Parts[i].Name(f)
	}
	return s
}

// methodSym finds the symbol sym.Enter already built for a method
// declaration. Enter runs before attribution ever sees the tree — a
// sourceCompleter.method call per *ast.MethodDecl, recorded on
// MethodSym.Decl — so this is a lookup, not a second entry.
func (a *attributor) methodSym(c *sym.ClassSym, d *ast.MethodDecl) *sym.MethodSym {
	name := identText(d.Name, a.unit.Src)
	for _, m := range c.Methods(name) {
		if m.Decl == d {
			return m
		}
	}
	return nil
}

// constructorSym is methodSym for a constructor, which sym.Enter files under
// the reserved name "<init>" regardless of the class's own name.
func (a *attributor) constructorSym(c *sym.ClassSym, d *ast.ConstructorDecl) *sym.MethodSym {
	for _, m := range c.Methods(sym.InitName) {
		if m.Decl == d {
			return m
		}
	}
	return nil
}

// fieldSym finds the symbol for one declarator of a (possibly multi-name)
// field declaration. Matching by name alone is not enough — sourceCompleter
// enters one VarSym per *ast.VarDeclarator — so this matches Decl, the same
// way methodSym does.
func (a *attributor) fieldSym(c *sym.ClassSym, decl *ast.VarDeclarator, f fileLike) *sym.VarSym {
	name := identText(decl.Name, f)
	for _, s := range c.Lookup(name) {
		if v, ok := s.(*sym.VarSym); ok && v.Decl == decl {
			return v
		}
	}
	return nil
}

// recordComponents is a record's components, in declaration order, for
// fillCompact to give a compact constructor as its implicit parameter list.
func recordComponents(c *sym.ClassSym) []*sym.VarSym {
	return c.RecordComponents()
}

// isStaticField reports whether a field is static: written explicitly, or
// implicit because §9.3 makes every interface field public static final
// whether or not any of the three was written.
func isStaticField(c *sym.ClassSym, d *ast.VarDecl) bool {
	if c.IsInterface() {
		return true
	}
	return d.Mods.Has(token.STATIC)
}

// recordThrows resolves a method's declared exceptions and reports any that
// is not a subclass of Throwable (§8.4.6), the same check tryStmt already
// makes for a catch clause. It runs in an environment that includes the
// method's own type parameters — not just the class's — because
// `<E extends Exception> void m() throws E` is legal and E is method-scoped;
// methodEnv builds the same extension for the body, just later, after the
// symbol this needs already exists.
func (a *attributor) recordThrows(e *env, m *sym.MethodSym) {
	mt := a.types.MethodType(m)
	te := e.child()
	if mt != nil {
		te.tparams = append(append([]*types.TypeVar{}, mt.TypeParams...), e.tparams...)
	}
	for _, x := range m.ThrowsExpr {
		t := a.resolveType(te, x)
		if !a.isThrowable(t) && !types.IsError(t) {
			a.errorf(x.Pos(), x.End(),
				"%s is not a subclass of java.lang.Throwable", t)
		}
	}
}