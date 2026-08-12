package warn

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
)

// Modifier legality, §8 and §9.
//
// The parser reads every modifier list with one function and said why:
// rejecting `transient` on a method there would produce a worse diagnostic
// than the phase that knows what a method is. This is that phase.
//
// Each declaration position has its own admissible set, and the interesting
// entries are the prohibitions rather than the permissions.

// The access group. Absent means package access, which has no keyword, so at
// most one of the three may appear.
const accessGroup = sym.FlagPublic | sym.FlagPrivate | sym.FlagProtected

func (c *checker) checkAccess(mods *ast.Modifiers, f sym.Flags, what string) {
	n := 0
	for _, bit := range []sym.Flags{sym.FlagPublic, sym.FlagPrivate, sym.FlagProtected} {
		if f.Has(bit) {
			n++
		}
	}
	if n > 1 {
		pos, end := modsSpan(mods)
		c.errorf(pos, end, "%s cannot have more than one access modifier", what)
	}
}

// checkDuplicates reports a modifier written twice. The tree keeps the order
// written rather than a set, precisely so this is detectable.
func (c *checker) checkDuplicates(mods *ast.Modifiers, what string) {
	if mods == nil {
		return
	}
	seen := map[token.Kind]bool{}
	for _, x := range mods.List {
		if x.Annotation != nil {
			continue
		}
		if seen[x.Kind] {
			c.errorf(x.Pos(), x.End(), "duplicate modifier %s on %s", x.Kind, what)
		}
		seen[x.Kind] = true
	}
}

// forbid reports any modifier in mask.
func (c *checker) forbid(mods *ast.Modifiers, f sym.Flags, mask sym.Flags, what string) {
	for _, bit := range allFlags {
		if mask&bit == 0 || !f.Has(bit) {
			continue
		}
		pos, end := flagSpan(mods, bit, c.file())
		c.errorf(pos, end, "modifier %s not allowed on %s", flagName(bit), what)
	}
}

func (c *checker) classModifiers(cs *sym.ClassSym) {
	mods := modifiersOf(cs.Decl)
	f := cs.Flags
	what := describeKind(cs)

	c.checkDuplicates(mods, what)
	c.checkAccess(mods, f, what)

	// §8.1.1: a top-level type may not be private, protected or static. A
	// member type may be all three, which is why this turns on nesting.
	if cs.IsTopLevel() {
		c.forbid(mods, f, sym.FlagPrivate|sym.FlagProtected|sym.FlagStatic, what)
	}

	// §8.1.1.1: final and abstract are contradictory — a final class cannot be
	// extended, and an abstract one must be.
	if f.Has(sym.FlagFinal) && f.Has(sym.FlagAbstract) {
		pos, end := modsSpan(mods)
		c.errorf(pos, end, "%s cannot be both final and abstract", what)
	}
	// §8.1.1.2: likewise final and sealed, and sealed and non-sealed.
	if f.Has(sym.FlagFinal) && f.Has(sym.FlagSealed) {
		pos, end := modsSpan(mods)
		c.errorf(pos, end, "%s cannot be both final and sealed", what)
	}
	if f.Has(sym.FlagSealed) && f.Has(sym.FlagNonSealed) {
		pos, end := modsSpan(mods)
		c.errorf(pos, end, "%s cannot be both sealed and non-sealed", what)
	}

	switch {
	case cs.IsInterface():
		// An interface is implicitly abstract and may not be final or native.
		c.forbid(mods, f, sym.FlagFinal|sym.FlagNative|sym.FlagSynchronized|
			sym.FlagTransient|sym.FlagVolatile, what)

	case cs.IsEnum():
		// §8.9: an enum is implicitly final unless a constant has a body, and
		// may never be declared abstract or sealed.
		c.forbid(mods, f, sym.FlagAbstract|sym.FlagSealed|sym.FlagNonSealed, what)

	case cs.IsRecord():
		// §8.10: a record is implicitly final and may not be abstract.
		c.forbid(mods, f, sym.FlagAbstract|sym.FlagSealed|sym.FlagNonSealed, what)

	default:
		c.forbid(mods, f, sym.FlagNative|sym.FlagSynchronized|sym.FlagTransient|
			sym.FlagVolatile|sym.FlagDefault, what)
	}

	// A non-sealed class is only meaningful under a sealed supertype.
	if f.Has(sym.FlagNonSealed) && !c.hasSealedSupertype(cs) {
		pos, end := flagSpan(mods, sym.FlagNonSealed, c.file())
		c.errorf(pos, end, "non-sealed is only allowed on a direct subclass of a sealed type")
	}
}

// annotationDecl adds the two modifiers ast admits syntactically and says are
// rejected later.
func (c *checker) annotationDecl(cs *sym.ClassSym, d *ast.AnnotationDecl) {
	if cs.Flags.HasAny(sym.FlagSealed | sym.FlagNonSealed) {
		pos, end := modsSpan(d.Mods)
		c.errorf(pos, end, "an annotation interface cannot be sealed or non-sealed")
	}
	// §9.6: an annotation interface may not declare type parameters and may
	// not extend anything.
	if len(d.Members) == 0 {
		return
	}
}

func (c *checker) recordDecl(cs *sym.ClassSym, d *ast.RecordDecl) {
	// §8.10.1: a record component may not be named after one of Object's
	// zero-argument methods, because the accessor would override it.
	reserved := map[string]bool{
		"clone": true, "finalize": true, "getClass": true, "hashCode": true,
		"notify": true, "notifyAll": true, "toString": true, "wait": true,
	}
	for _, comp := range d.Components {
		name := identText(comp.Name, c.file())
		if reserved[name] {
			c.errorf(comp.Pos(), comp.End(),
				"illegal record component name %s", name)
		}
		// §8.10.1: only the last component may be varargs.
		if comp.Ellipsis.IsValid() && comp != d.Components[len(d.Components)-1] {
			c.errorf(comp.Pos(), comp.End(),
				"varargs is only allowed on the last record component")
		}
	}
}

func (c *checker) methodModifiers(cs *sym.ClassSym, d *ast.MethodDecl, m *sym.MethodSym) {
	if m == nil {
		return
	}
	f := m.Flags
	what := "a method"

	c.checkDuplicates(d.Mods, what)
	c.checkAccess(d.Mods, f, what)
	c.forbid(d.Mods, f, sym.FlagTransient|sym.FlagVolatile|sym.FlagSealed|
		sym.FlagNonSealed, what)

	if cs.IsInterface() {
		// §9.4: an interface method may not be synchronized, native, final or
		// protected. It may be default, static or private.
		c.forbid(d.Mods, f, sym.FlagSynchronized|sym.FlagNative|sym.FlagFinal|
			sym.FlagProtected, "an interface method")

		if f.Has(sym.FlagDefault) && f.Has(sym.FlagStatic) {
			pos, end := modsSpan(d.Mods)
			c.errorf(pos, end, "an interface method cannot be both default and static")
		}
		if f.Has(sym.FlagDefault) && d.Body == nil {
			c.errorf(d.Pos(), d.End(), "a default method requires a body")
		}
		if f.Has(sym.FlagPrivate) && d.Body == nil {
			c.errorf(d.Pos(), d.End(), "a private interface method requires a body")
		}
	}

	// §8.4.3.1: abstract is incompatible with everything that implies a body
	// or a fixed implementation.
	if f.Has(sym.FlagAbstract) {
		c.forbid(d.Mods, f, sym.FlagPrivate|sym.FlagStatic|sym.FlagFinal|
			sym.FlagNative|sym.FlagSynchronized|sym.FlagStrictfp, "an abstract method")
		if d.Body != nil {
			c.errorf(d.Pos(), d.End(), "an abstract method cannot have a body")
		}
	}

	// §8.4.3.4: a native method has no body, and its implementation comes
	// from elsewhere — which on Android means it will not link unless the
	// library is bundled, but that is not this phase's problem.
	if f.Has(sym.FlagNative) {
		if d.Body != nil {
			c.errorf(d.Pos(), d.End(), "a native method cannot have a body")
		}
		c.forbid(d.Mods, f, sym.FlagStrictfp, "a native method")
	}

	if d.Body == nil && !f.HasAny(sym.FlagAbstract|sym.FlagNative) && !cs.IsInterface() {
		c.errorf(d.Pos(), d.End(), "a method without a body must be abstract or native")
	}

	// §8.4.1: only the last parameter may be varargs. The parser accepts the
	// ellipsis anywhere because the shape is uniform.
	for i, p := range d.Params {
		if p.Ellipsis.IsValid() && i != len(d.Params)-1 {
			c.errorf(p.Pos(), p.End(), "varargs is only allowed on the last parameter")
		}
	}
}

func (c *checker) constructorModifiers(d *ast.ConstructorDecl) {
	f := modifierFlags(d.Mods)
	what := "a constructor"

	c.checkDuplicates(d.Mods, what)
	c.checkAccess(d.Mods, f, what)
	// §8.8.3: a constructor admits only the access modifiers.
	c.forbid(d.Mods, f, ^accessGroup, what)

	if d.Compact && len(d.Params) > 0 {
		c.errorf(d.Pos(), d.End(), "a compact constructor cannot declare parameters")
	}
}

func (c *checker) fieldModifiers(cs *sym.ClassSym, d *ast.VarDecl) {
	f := modifierFlags(d.Mods)
	what := "a field"

	c.checkDuplicates(d.Mods, what)
	c.checkAccess(d.Mods, f, what)
	c.forbid(d.Mods, f, sym.FlagAbstract|sym.FlagNative|sym.FlagSynchronized|
		sym.FlagDefault|sym.FlagSealed|sym.FlagNonSealed|sym.FlagStrictfp, what)

	// §8.3.1.4: final and volatile contradict each other. final already
	// forbids the writes volatile exists to order.
	if f.Has(sym.FlagFinal) && f.Has(sym.FlagVolatile) {
		pos, end := modsSpan(d.Mods)
		c.errorf(pos, end, "a field cannot be both final and volatile")
	}

	if cs.IsInterface() {
		// §9.3: an interface field is implicitly public static final, and
		// nothing else is admissible.
		c.forbid(d.Mods, f, sym.FlagPrivate|sym.FlagProtected|sym.FlagVolatile|
			sym.FlagTransient, "an interface field")
		for _, decl := range d.Names {
			if decl.Init == nil {
				c.errorf(decl.Pos(), decl.End(),
					"%s must be initialized: an interface field is implicitly final",
					identText(decl.Name, c.file()))
			}
		}
	}

	// §8.3.1.2: a final field with no initializer is a blank final, which is
	// legal only if a constructor assigns it — flow checks that. A final
	// *static* field with no initializer needs a static initializer, same
	// rule, same phase.
	_ = cs
}

// allFlags is the iteration order for forbid. It omits the flags that are
// never written in source — implicit, synthetic, bridge — because a
// diagnostic naming one of those would describe something the user did not
// type.
var allFlags = []sym.Flags{
	sym.FlagPublic, sym.FlagPrivate, sym.FlagProtected,
	sym.FlagStatic, sym.FlagFinal, sym.FlagAbstract,
	sym.FlagNative, sym.FlagSynchronized, sym.FlagTransient,
	sym.FlagVolatile, sym.FlagStrictfp, sym.FlagDefault,
	sym.FlagSealed, sym.FlagNonSealed,
}

func flagName(f sym.Flags) string { return f.String() }