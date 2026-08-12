// Package flow answers the four questions Java makes compile errors rather
// than warnings: is this variable definitely assigned, is this statement
// reachable, is this checked exception handled, and may this lambda capture
// that local.
//
// It runs after attr and resolves nothing. Every type and every symbol comes
// from attr.Info; a name that failed to resolve was already reported, and this
// package stays quiet about it rather than producing a second diagnostic for
// the same mistake.
//
// # Why this is separate from attr
//
// Each of the four analyses needs types already in hand, and none can be
// answered while resolving: definite assignment needs the whole method body
// before it can say anything about a loop, and reachability needs the folded
// constants attr produced. javac splits Attr and Flow for the same reason.
package flow

import (
	"github.com/vertex-language/mocha/analyzer/attr"
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// Flow is what one compilation unit's analysis produced.
//
// Like attr.Info it is keyed on tree nodes and symbols, so it does not outlive
// ast.File.Release. The two maps lower actually needs — Captured and
// EffectivelyFinal — are keyed on symbols, which do survive.
type Flow struct {
	// Captured lists, per method, the locals of an *enclosing* method that its
	// lambdas and inner classes read. lower copies each into a synthetic field
	// or a constructor parameter; gen never sees a free variable.
	Captured map[*sym.MethodSym][]*sym.VarSym

	// EffectivelyFinal holds every local that is never reassigned after its
	// initialisation. §4.12.4 makes this the condition for capture, and it is
	// also what lets lower copy by value without worrying about aliasing.
	EffectivelyFinal map[*sym.VarSym]bool

	// Unreachable marks the statements §14.22 forbids. Marking rather than
	// deleting: dropping code is lower's, and a marked statement still has to
	// be walked so its own errors are found.
	Unreachable map[ast.Stmt]bool

	Diags []token.Diagnostic
}

func newFlow() *Flow {
	return &Flow{
		Captured:         make(map[*sym.MethodSym][]*sym.VarSym),
		EffectivelyFinal: make(map[*sym.VarSym]bool),
		Unreachable:      make(map[ast.Stmt]bool),
	}
}

// Analyze walks every method body in a unit.
func Analyze(in *attr.Info, tt *types.Table, u *sym.Unit) *Flow {
	f := newFlow()
	if in == nil || u == nil || u.Module != nil {
		return f
	}
	a := &analyzer{info: in, types: tt, syms: u.Table(), unit: u, out: f}

	for _, c := range u.Types {
		a.class(c)
	}
	token.SortDiagnostics(f.Diags)
	return f
}

// analyzer carries what every walk needs. The mutable per-method state lives
// in ctx, which is threaded through rather than stored here: two nested
// methods — a lambda body inside a method body — are analysed with two
// independent variable spaces, and sharing one would leak bits between them.
type analyzer struct {
	info  *attr.Info
	types *types.Table
	syms  *sym.Table
	unit  *sym.Unit
	out   *Flow

	reported map[token.Pos]bool
}

// ctx is one method body's analysis state.
type ctx struct {
	a *analyzer

	method *sym.MethodSym
	class  *sym.ClassSym

	// vars indexes the locals of this method densely, so definite assignment
	// is a bitset rather than a map. A local declared in a nested block still
	// gets an index here: scopes do not overlap in time, and reusing indices
	// would only save bits.
	vars  map[*sym.VarSym]int
	order []*sym.VarSym

	// blanks are the finals declared without an initialiser. §16 tracks
	// definite *un*assignment for exactly these, and for nothing else.
	blanks map[int]bool

	// caught is the stack of enclosing catch types, innermost last, plus what
	// the enclosing method declares. A throw is legal if any layer covers it.
	caught [][]types.Type

	// declared is the enclosing method's throws clause.
	declared []types.Type

	// thrown accumulates what the current construct can throw, so a try knows
	// what its body produced and a catch knows whether it can fire.
	thrown []types.Type

	// assigned tracks writes to locals so effectively-final falls out: a
	// variable written twice, or written after a read, is not one.
	writes map[*sym.VarSym]int

	// outer is the enclosing method's ctx when this is a lambda or inner
	// class body, so a capture can be recognised as such.
	outer *ctx
}

func (a *analyzer) class(c *sym.ClassSym) {
	if c == nil || !c.FromSource() {
		return
	}
	switch d := c.Decl.(type) {
	case *ast.ClassDecl:
		a.members(c, d.Members, nil)
	case *ast.InterfaceDecl:
		a.members(c, d.Members, nil)
	case *ast.AnnotationDecl:
		a.members(c, d.Members, nil)
	case *ast.EnumDecl:
		a.enumConstants(c, d.Constants)
		a.members(c, d.Members, nil)
	case *ast.RecordDecl:
		a.members(c, d.Members, nil)
	}
}

func (a *analyzer) members(c *sym.ClassSym, members []ast.Decl, outer *ctx) {
	for _, m := range members {
		switch d := m.(type) {
		case *ast.MethodDecl:
			a.method(c, d, outer)
		case *ast.ConstructorDecl:
			a.constructor(c, d, outer)
		case *ast.InitializerDecl:
			a.initializer(c, d, outer)
		case *ast.VarDecl:
			a.fieldInit(c, d, outer)
		case *ast.ClassDecl, *ast.InterfaceDecl, *ast.AnnotationDecl,
			*ast.EnumDecl, *ast.RecordDecl:
			if nested := a.nestedOf(c, d); nested != nil {
				a.class(nested)
			}
		}
	}
}

func (a *analyzer) method(c *sym.ClassSym, d *ast.MethodDecl, outer *ctx) {
	if d.Body == nil {
		return
	}
	m := a.methodFor(c, d)
	cx := a.newCtx(c, m, outer)
	cx.declareParams(m)

	st := cx.newState()
	alive := cx.block(d.Body, st)

	// §8.4.7: a method with a result must not be able to complete normally.
	// This is the check attr could not make, because it needs reachability.
	if alive && a.returnsValue(m) {
		a.errorf(d.Body.Rbrace, d.Body.Rbrace+1, "missing return statement")
	}
	cx.finish()
}

func (a *analyzer) constructor(c *sym.ClassSym, d *ast.ConstructorDecl, outer *ctx) {
	if d.Body == nil {
		return
	}
	m := a.constructorFor(c, d)
	cx := a.newCtx(c, m, outer)
	cx.declareParams(m)

	// A blank final field must be definitely assigned by the end of every
	// constructor (§8.3.1.2). Fields are not in the local bitset, so they are
	// tracked separately by name against the class.
	st := cx.newState()
	cx.block(d.Body, st)
	cx.finish()
}

func (a *analyzer) initializer(c *sym.ClassSym, d *ast.InitializerDecl, outer *ctx) {
	if d.Body == nil {
		return
	}
	cx := a.newCtx(c, nil, outer)
	st := cx.newState()
	cx.block(d.Body, st)
	cx.finish()
}

// fieldInit analyses a field's initialiser expression. There is no statement
// context, so only the expression walk runs: a field initialiser cannot
// contain a statement, but it can contain a lambda, and that lambda's captures
// still have to be found.
func (a *analyzer) fieldInit(c *sym.ClassSym, d *ast.VarDecl, outer *ctx) {
	for _, decl := range d.Names {
		if decl.Init == nil {
			continue
		}
		cx := a.newCtx(c, nil, outer)
		st := cx.newState()
		cx.initializer(decl.Init, st)
		cx.finish()
	}
}

func (a *analyzer) enumConstants(c *sym.ClassSym, consts []*ast.EnumConstant) {
	for _, k := range consts {
		for _, arg := range k.Args {
			cx := a.newCtx(c, nil, nil)
			st := cx.newState()
			cx.expr(arg, st)
			cx.finish()
		}
		if len(k.Members) > 0 {
			a.members(c, k.Members, nil)
		}
	}
}

func (a *analyzer) newCtx(c *sym.ClassSym, m *sym.MethodSym, outer *ctx) *ctx {
	cx := &ctx{
		a:      a,
		class:  c,
		method: m,
		vars:   make(map[*sym.VarSym]int),
		blanks: make(map[int]bool),
		writes: make(map[*sym.VarSym]int),
		outer:  outer,
	}
	if m != nil {
		if mt := a.types.MethodType(m); mt != nil {
			cx.declared = mt.Throws
		}
	}
	return cx
}

// declareParams gives every parameter an index and marks it assigned: a
// parameter is definitely assigned throughout the body by definition.
func (cx *ctx) declareParams(m *sym.MethodSym) {
	if m == nil {
		return
	}
	for _, p := range m.Params {
		cx.declare(p, false)
	}
}

// declare assigns a local its bit. blank marks a final with no initialiser,
// which is the only case definite *un*assignment is tracked for.
func (cx *ctx) declare(v *sym.VarSym, blank bool) int {
	if v == nil {
		return -1
	}
	if i, ok := cx.vars[v]; ok {
		return i
	}
	i := len(cx.order)
	cx.vars[v] = i
	cx.order = append(cx.order, v)
	if blank {
		cx.blanks[i] = true
	}
	return i
}

func (cx *ctx) indexOf(v *sym.VarSym) int {
	if i, ok := cx.vars[v]; ok {
		return i
	}
	return -1
}

// finish records what this method's analysis concluded about its locals.
func (cx *ctx) finish() {
	for _, v := range cx.order {
		if cx.writes[v] <= 1 {
			cx.a.out.EffectivelyFinal[v] = true
		}
	}
}

func (a *analyzer) returnsValue(m *sym.MethodSym) bool {
	if m == nil {
		return false
	}
	mt := a.types.MethodType(m)
	return mt != nil && mt.Result != nil && mt.Result.Kind() != types.KindVoid
}

func (a *analyzer) errorf(pos, end token.Pos, format string, args ...any) {
	if a.reported == nil {
		a.reported = map[token.Pos]bool{}
	}
	if a.reported[pos] {
		return
	}
	a.reported[pos] = true
	if end <= pos {
		end = pos + 1
	}
	a.out.Diags = append(a.out.Diags, token.Diagnostic{
		Pos:      pos,
		End:      end,
		Severity: token.SevError,
		Msg:      sprintf(format, args...),
	})
}