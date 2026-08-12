package attr

import (
	"fmt"

	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// One diagnostic per site. The parser's rule, and for the same reason: a
// cascade tells the user nothing they can act on, and attribution has more
// opportunities to cascade than parsing does, because a single unresolved name
// flows into every enclosing expression.
//
// The other half of the discipline is types.ErrorType, which types.IsSubtype
// treats as compatible with everything, so an already-reported failure passes
// every downstream check silently.

func (a *attributor) errorf(pos, end token.Pos, format string, args ...any) {
	if a.reported == nil {
		a.reported = map[token.Pos]bool{}
	}
	if a.reported[pos] {
		return
	}
	a.reported[pos] = true

	if end <= pos {
		end = pos + 1 // invariant 3: every span is non-empty
	}
	a.info.Diags = append(a.info.Diags, token.Diagnostic{
		Pos:      pos,
		End:      end,
		Severity: token.SevError,
		Msg:      fmt.Sprintf(format, args...),
	})
}

// --- small shared helpers ---------------------------------------------------

func identText(id *ast.Ident, f *token.File) string {
	if id == nil || f == nil {
		return ""
	}
	if id.Underscore {
		return "_"
	}
	return id.Name(f)
}

func lastPart(n *ast.Name, f *token.File) string {
	if n == nil || len(n.Parts) == 0 {
		return ""
	}
	return identText(n.Parts[len(n.Parts)-1], f)
}

func arrayOf(t types.Type, n int) types.Type {
	for i := 0; i < n; i++ {
		t = types.NewArray(t)
	}
	return t
}

func isIntegral(t types.Type) bool { return t.Kind().IsIntegral() }

func isSuper(x ast.Expr) bool {
	_, ok := x.(*ast.Super)
	return ok
}

// isPoly reports whether an expression has no type without a target (§15.2).
// A lambda and a method reference never do; a diamond and a conditional
// sometimes do, and are treated as poly whenever they might be.
func isPoly(x ast.Expr) bool {
	switch n := x.(type) {
	case *ast.LambdaExpr, *ast.MethodRef:
		return true
	case *ast.ParenExpr:
		return isPoly(n.X)
	case *ast.NewExpr:
		return n.Type != nil && n.Type.TypeArgs != nil && n.Type.TypeArgs.Diamond
	case *ast.CondExpr:
		return isPoly(n.Then) || isPoly(n.Else)
	}
	return false
}

func typeList(ts []types.Type) string {
	s := ""
	for i, t := range ts {
		if i > 0 {
			s += ", "
		}
		if t == nil {
			s += "<poly>"
			continue
		}
		s += t.String()
	}
	return s
}

func paramList(mt *types.MethodType) string {
	if mt == nil {
		return "()"
	}
	return "(" + typeList(mt.Params) + ")"
}

// declName is the simple name of any of the five declaration nodes that can be
// a type. It mirrors sym's own describe, which is unexported.
func declName(d ast.Decl, f *token.File) string {
	switch t := d.(type) {
	case *ast.ClassDecl:
		return identText(t.Name, f)
	case *ast.InterfaceDecl:
		return identText(t.Name, f)
	case *ast.AnnotationDecl:
		return identText(t.Name, f)
	case *ast.EnumDecl:
		return identText(t.Name, f)
	case *ast.RecordDecl:
		return identText(t.Name, f)
	}
	return ""
}

// resolveType resolves an ast.Type in an environment, recording the result.
func (a *attributor) resolveType(e *env, x ast.Type) types.Type {
	if x == nil {
		return errType
	}
	t := a.types.ResolveType(x, e.unit, e.class, e.tparams)
	if t == nil {
		t = errType
	}
	a.info.Types[x] = t
	return t
}

// superType is the type `super` denotes: the enclosing class's superclass, or
// a named superinterface for the TypeName.super form.
func (a *attributor) superType(e *env, n *ast.Super) types.Type {
	if e.class == nil {
		return errType
	}
	if n.Qualifier != nil {
		return a.resolveTypeName(e, lastPart(n.Qualifier, e.file()), n.Qualifier)
	}
	if e.static {
		a.errorf(n.Pos(), n.End(), "'super' cannot be referenced from a static context")
		return errType
	}
	if s := a.types.Supertype(e.class); s != nil {
		return s
	}
	return a.types.Object()
}