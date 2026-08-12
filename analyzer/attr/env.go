package attr

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// env is the environment one node is attributed in: the enclosing class and
// method, the scope chain for locals, the type parameters in scope, and the
// handful of facts a statement needs about where it sits.
//
// It is copied by child() rather than pushed and popped. A walk that forgets
// to restore a field then corrupts one subtree instead of everything after it,
// which is the difference between a localized bug and a mystery.
type env struct {
	a *attributor

	unit   *sym.Unit
	class  *sym.ClassSym
	method *sym.MethodSym

	// scope holds locals, parameters, exception parameters, resources and
	// pattern bindings. Fields are not in it: they are found through the class,
	// because §6.5.6.1 looks at the scope chain first and the class hierarchy
	// second, and merging the two would lose that order.
	scope *sym.Scope

	tparams []*types.TypeVar

	// static is true in a static method, a static initialiser, or a static
	// field's initialiser. It is what makes `this` and every instance member
	// an error rather than a silent capture.
	static bool

	// ctor is true inside a constructor body, where `this(...)` and
	// `super(...)` are admissible and a blank final may be assigned.
	ctor bool

	// ret is the enclosing method's declared result, or nil in an initialiser
	// where `return` takes no value.
	ret types.Type

	// labels are the enclosing labeled statements, innermost last.
	labels []string

	// loop and swtch record whether an unlabeled continue or break has a
	// target. Reachability is flow's; having a target at all is a resolution
	// question and therefore this package's.
	loop  bool
	swtch bool

	// yield is the type a switch expression's arms must produce, or nil
	// outside one.
	yield types.Type
}

func (a *attributor) classEnv(c *sym.ClassSym) *env {
	return &env{
		a:       a,
		unit:    a.unit,
		class:   c,
		scope:   sym.NewScope(c, nil),
		tparams: a.types.TypeParams(c),
	}
}

func (a *attributor) methodEnv(parent *env, m *sym.MethodSym, mt *types.MethodType) *env {
	e := parent.child()
	e.method = m
	e.static = m.Flags.Has(sym.FlagStatic)
	e.ret = types.Type(nil)
	if mt != nil {
		e.ret = mt.Result
		e.tparams = append(append([]*types.TypeVar{}, mt.TypeParams...), e.tparams...)
	}
	e.scope = sym.NewScope(m, parent.scope)
	e.loop, e.swtch, e.yield = false, false, nil

	// Parameters are in scope throughout the body and shadow fields.
	for i, p := range m.Params {
		var pt types.Type = errType
		if mt != nil && i < len(mt.Params) {
			pt = mt.Params[i]
		}
		e.declare(p, pt)
	}
	return e
}

// child returns a copy sharing the same scope. Callers that open a new
// declaration space call block() instead.
func (e *env) child() *env {
	n := *e
	return &n
}

// block returns a child with a fresh scope nested inside this one.
func (e *env) block(owner sym.Symbol) *env {
	n := e.child()
	if owner == nil {
		owner = e.method
	}
	n.scope = sym.NewScope(owner, e.scope)
	return n
}

func (e *env) file() *token.File {
	if e.unit == nil {
		return nil
	}
	return e.unit.Src
}

// declare enters a variable in the current scope and records its type,
// reporting a redeclaration. §6.4 forbids shadowing a local with a local, but
// permits shadowing a field, which falls out of the scope chain rather than
// needing a check.
func (e *env) declare(v *sym.VarSym, t types.Type) {
	if v == nil {
		return
	}
	e.a.info.Types[declNode(v)] = t
	if prev := e.scope.Enter(v); prev != nil {
		e.a.errorf(v.Pos, v.End, "variable %s is already defined", v.Name)
	}
}

// lookupVar walks the scope chain for a variable. Returns nil if no enclosing
// block declares the name; the caller then tries fields.
func (e *env) lookupVar(name string) *sym.VarSym {
	s := e.scope.ResolveKind(name, sym.KindVar)
	if v, ok := s.(*sym.VarSym); ok {
		return v
	}
	return nil
}

// hasLabel reports whether a labeled statement with this name encloses the
// current position.
func (e *env) hasLabel(name string) bool {
	for _, l := range e.labels {
		if l == name {
			return true
		}
	}
	return false
}

// declNode recovers the ast node a variable was declared from, for keying
// Info. A binary symbol has none.
func declNode(v *sym.VarSym) ast.Node {
	if v == nil || v.Decl == nil {
		return nil
	}
	return v.Decl
}