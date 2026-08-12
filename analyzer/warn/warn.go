// Package warn is the last frontend phase: the checks that need everything
// resolved but belong to no earlier phase, plus the warnings a compiler owes
// its user.
//
// It emits both severities despite the name. A transient method, an
// unimplemented abstract member and a subclass missing from a permits clause
// are all hard errors in Java; they simply have nowhere else to live, because
// each needs the supertype graph and none is a resolution or a dataflow
// question. Splitting them into a fifth package by severity would separate
// checks that share one walk.
//
// Nothing is resolved here. Types come from attr.Info, the supertype graph
// from types.Table, and reads from flow.Flow.
package warn

import (
	"fmt"

	"github.com/vertex-language/mocha/analyzer/attr"
	"github.com/vertex-language/mocha/analyzer/flow"
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// Warn is what one unit's checks produced.
type Warn struct {
	Diags []token.Diagnostic
}

// Check runs every check over one compilation unit.
func Check(in *attr.Info, fl *flow.Flow, tt *types.Table, u *sym.Unit) *Warn {
	w := &Warn{}
	if in == nil || u == nil {
		return w
	}
	c := &checker{
		info:  in,
		flow:  fl,
		types: tt,
		syms:  u.Table(),
		unit:  u,
		out:   w,
		read:  make(map[*sym.VarSym]bool),
		used:  make(map[string]bool),
	}

	if u.Module != nil {
		return w
	}
	c.unitShape()
	c.collectUses()

	for _, t := range u.Types {
		c.class(t)
	}
	c.unusedImports()

	token.SortDiagnostics(w.Diags)
	return w
}

type checker struct {
	info  *attr.Info
	flow  *flow.Flow
	types *types.Table
	syms  *sym.Table
	unit  *sym.Unit
	out   *Warn

	// read records every variable an expression actually read, gathered in one
	// pass over Info.Uses rather than by re-walking the tree.
	read map[*sym.VarSym]bool

	// used records the simple names that resolved to an imported type, for
	// the unused-import check.
	used map[string]bool

	// suppressed is the stack of @SuppressWarnings categories in scope,
	// innermost last. It applies to warnings only — an error is not something
	// an annotation may turn off.
	suppressed []map[string]bool

	reported map[token.Pos]bool
}

// class checks one type declaration and, recursively, its member types.
func (c *checker) class(cs *sym.ClassSym) {
	if cs == nil || !cs.FromSource() {
		return
	}
	c.push(cs.Decl)
	defer c.pop()

	c.classModifiers(cs)
	c.sealedClass(cs)
	c.abstractCompleteness(cs)
	c.deprecatedSupertypes(cs)

	switch d := cs.Decl.(type) {
	case *ast.ClassDecl:
		c.members(cs, d.Members)
	case *ast.InterfaceDecl:
		c.members(cs, d.Members)
	case *ast.AnnotationDecl:
		c.annotationDecl(cs, d)
		c.members(cs, d.Members)
	case *ast.EnumDecl:
		c.members(cs, d.Members)
	case *ast.RecordDecl:
		c.recordDecl(cs, d)
		c.members(cs, d.Members)
	}
}

func (c *checker) members(cs *sym.ClassSym, members []ast.Decl) {
	for _, m := range members {
		switch d := m.(type) {
		case *ast.MethodDecl:
			c.methodDecl(cs, d)
		case *ast.ConstructorDecl:
			c.constructorDecl(cs, d)
		case *ast.VarDecl:
			c.fieldDecl(cs, d)
		case *ast.InitializerDecl:
			c.bodyChecks(cs, d.Body)
		case *ast.AnnotationElemDecl:
			c.elementDecl(cs, d)
		case *ast.ClassDecl, *ast.InterfaceDecl, *ast.AnnotationDecl,
			*ast.EnumDecl, *ast.RecordDecl:
			if nested := cs.Nested(declName(d, c.file())); nested != nil {
				c.class(nested)
			}
		}
	}
}

func (c *checker) methodDecl(cs *sym.ClassSym, d *ast.MethodDecl) {
	c.push(d)
	defer c.pop()

	m := c.methodFor(cs, d)
	c.methodModifiers(cs, d, m)
	c.overrideChecks(cs, d, m)
	c.bodyChecks(cs, d.Body)
}

func (c *checker) constructorDecl(cs *sym.ClassSym, d *ast.ConstructorDecl) {
	c.push(d)
	defer c.pop()

	c.constructorModifiers(d)
	c.bodyChecks(cs, d.Body)
}

func (c *checker) fieldDecl(cs *sym.ClassSym, d *ast.VarDecl) {
	c.push(d)
	defer c.pop()

	c.fieldModifiers(cs, d)
	for _, decl := range d.Names {
		if decl.Init != nil {
			c.exprChecks(decl.Init)
		}
	}
}

func (c *checker) elementDecl(cs *sym.ClassSym, d *ast.AnnotationElemDecl) {
	// §9.6.1: an annotation element's type is restricted to primitives,
	// String, Class, an enum, another annotation, or a one-dimensional array
	// of those. Anything else cannot be encoded in the class file.
	t := c.info.Type(d.Type)
	if !c.isElementType(t) && !types.IsError(t) {
		c.errorf(d.Type.Pos(), d.Type.End(),
			"invalid type %s for annotation interface element", t)
	}
}

// bodyChecks runs the statement-level checks over a method body.
func (c *checker) bodyChecks(cs *sym.ClassSym, b *ast.Block) {
	if b == nil {
		return
	}
	ast.Inspect(b, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.TryStmt:
			c.resources(t)
		case *ast.LambdaExpr:
			c.lambdaParams(t)
		case *ast.SwitchStmt:
			c.switchStmt(t)
		case *ast.SwitchExpr:
			c.switchExpr(t)
		case *ast.DeclStmt:
			if vd, ok := t.Decl.(*ast.VarDecl); ok {
				c.unusedLocals(vd)
			}
		case *ast.NamedType:
			c.typeArgs(t)
		case *ast.CallExpr, *ast.SelectorExpr, *ast.NewExpr, *ast.Ident, *ast.Name:
			c.deprecatedUse(n)
		}
		return true
	})
}

func (c *checker) exprChecks(x ast.Node) {
	ast.Inspect(x, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.CallExpr, *ast.SelectorExpr, *ast.NewExpr, *ast.Ident, *ast.Name:
			c.deprecatedUse(n)
		}
		return true
	})
}

// collectUses walks Info.Uses once so the unused checks do not each re-walk
// the tree. A variable in Uses was named by an expression; whether that
// expression was a read or a write is what distinguishes an unused local from
// a dead store, and only the former is reported here.
func (c *checker) collectUses() {
	for node, s := range c.info.Uses {
		switch t := s.(type) {
		case *sym.VarSym:
			c.read[t] = true
		case *sym.ClassSym:
			c.used[sym.SimpleName(t.Binary)] = true
		}
		_ = node
	}
}

// --- @SuppressWarnings ------------------------------------------------------

// push enters a declaration's suppression scope.
func (c *checker) push(d ast.Decl) {
	c.suppressed = append(c.suppressed, c.suppressionsOf(d))
}

func (c *checker) pop() {
	if n := len(c.suppressed); n > 0 {
		c.suppressed = c.suppressed[:n-1]
	}
}

// suppressionsOf reads @SuppressWarnings off a declaration's modifiers.
func (c *checker) suppressionsOf(d ast.Decl) map[string]bool {
	mods := modifiersOf(d)
	if mods == nil {
		return nil
	}
	var out map[string]bool
	for _, x := range mods.List {
		a := x.Annotation
		if a == nil || a.Name == nil || len(a.Name.Parts) == 0 {
			continue
		}
		if a.Name.Parts[len(a.Name.Parts)-1].Name(c.file()) != "SuppressWarnings" {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		for _, p := range a.Pairs {
			c.collectSuppressions(p.Value, out)
		}
	}
	return out
}

func (c *checker) collectSuppressions(v ast.Node, out map[string]bool) {
	switch n := v.(type) {
	case *ast.ElementValueArray:
		for _, e := range n.Elts {
			c.collectSuppressions(e, out)
		}
	case ast.Expr:
		if k, ok := c.info.Const(n); ok {
			if s, ok := k.Value.(string); ok {
				out[s] = true
			}
		}
	}
}

func (c *checker) isSuppressed(category string) bool {
	for _, layer := range c.suppressed {
		if layer == nil {
			continue
		}
		if layer[category] || layer["all"] {
			return true
		}
	}
	return false
}

// --- diagnostics ------------------------------------------------------------

func (c *checker) errorf(pos, end token.Pos, format string, args ...any) {
	c.report(token.SevError, "", pos, end, format, args...)
}

// warnf emits a suppressible warning. category is the @SuppressWarnings name.
func (c *checker) warnf(category string, pos, end token.Pos, format string, args ...any) {
	if c.isSuppressed(category) {
		return
	}
	c.report(token.SevWarning, category, pos, end, format, args...)
}

func (c *checker) report(sev token.Severity, category string, pos, end token.Pos,
	format string, args ...any) {

	if c.reported == nil {
		c.reported = map[token.Pos]bool{}
	}
	if c.reported[pos] {
		return
	}
	c.reported[pos] = true
	if end <= pos {
		end = pos + 1
	}
	c.out.Diags = append(c.out.Diags, token.Diagnostic{
		Pos:      pos,
		End:      end,
		Severity: sev,
		Msg:      fmt.Sprintf(format, args...),
	})
}

func (c *checker) file() *token.File {
	if c.unit == nil {
		return nil
	}
	return c.unit.Src
}