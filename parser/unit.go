package parser

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
)

// parseCompilationUnit decides among the three shapes of §7.3.
//
// The modular form is unmistakable: imports followed by `module` or `open
// module`. The other two are separated by content, not by a prefix — a unit is
// compact only if it contains a method declaration among its top-level members,
// so a unit of nothing but type declarations is ordinary.
func (p *parser) parseCompilationUnit() *ast.File {
	f := alloc[ast.File](p.arena)
	lo := p.pos()

	if p.at(token.AT) || p.at(token.PACKAGE) {
		if pd := p.tryPackageDecl(); pd != nil {
			f.Package = pd
		}
	}
	for p.at(token.IMPORT) {
		f.Imports = append(f.Imports, p.parseImportDecl())
	}

	if p.atCtx(token.CtxModule) || (p.atCtx(token.CtxOpen) && p.peek(1).Ctx == token.CtxModule) {
		f.Module = p.parseModuleDecl()
		f.Span = ast.At(lo, p.prevEnd())
		return f
	}

	if p.mode&HeaderOnly != 0 {
		f.Span = ast.At(lo, p.prevEnd())
		return f
	}

	for !p.atEOF() {
		d := p.parseTopLevelDecl()
		if d != nil {
			f.Decls = append(f.Decls, d)
		}
	}
	f.Compact = hasMethodMember(f.Decls)
	f.Span = ast.At(lo, p.prevEnd())
	return f
}

// hasMethodMember reports whether the top level contains a member that only a
// compact compilation unit can have. A FieldDeclaration at the top level is
// also compact-only, but the grammar makes MethodDeclaration the required one.
func hasMethodMember(decls []ast.Decl) bool {
	for _, d := range decls {
		switch d.(type) {
		case *ast.MethodDecl, *ast.VarDecl, *ast.InitializerDecl, *ast.ConstructorDecl:
			return true
		}
	}
	return false
}

// tryPackageDecl speculates, because a leading annotation may belong to a
// package declaration or to the first type declaration.
func (p *parser) tryPackageDecl() *ast.PackageDecl {
	pd, _ := spec(p, func() (*ast.PackageDecl, bool) {
		lo := p.pos()
		anns := p.parseAnnotations()
		if !p.at(token.PACKAGE) {
			return nil, false
		}
		d := alloc[ast.PackageDecl](p.arena)
		d.Annotations = anns
		d.PackagePos = p.next().Pos
		d.Name = p.parseName()
		p.expectSemi()
		d.Span = ast.At(lo, p.prevEnd())
		return d, true
	})
	return pd
}

func (p *parser) parseImportDecl() *ast.ImportDecl {
	d := alloc[ast.ImportDecl](p.arena)
	d.ImportPos = p.expect(token.IMPORT)

	switch {
	case p.at(token.STATIC):
		p.next()
		d.Static = true
	case p.atCtx(token.CtxModule):
		// §7.5.5. `module` is contextual, so `import module.foo.Bar;` is a
		// type import: it is a module import only when the next token is not a
		// dot continuing a package name.
		if p.peek(1).Kind == token.IDENT {
			p.next()
			d.Module = true
		}
	}

	d.Name = p.parseName()
	if !d.Module {
		if p.at(token.PERIOD) && p.peek(1).Kind == token.MUL {
			p.next()
			d.OnDemand = true
			d.StarPos = p.next().Pos
		}
	}
	p.expectSemi()
	d.Span = ast.At(d.ImportPos, p.prevEnd())
	return d
}

func (p *parser) parseModuleDecl() *ast.ModuleDecl {
	d := alloc[ast.ModuleDecl](p.arena)
	lo := p.pos()
	d.Annotations = p.parseAnnotations()
	if t, ok := p.acceptCtx(token.CtxOpen); ok {
		d.OpenPos = t.Pos
	}
	t, _ := p.acceptCtx(token.CtxModule)
	d.ModulePos = t.Pos
	d.Name = p.parseName()
	d.Lbrace = p.expect(token.LBRACE)

	for !p.at(token.RBRACE) && !p.atEOF() {
		dir := p.parseModuleDirective()
		if dir == nil {
			p.advanceTo(token.SEMICOLON, token.RBRACE)
			p.accept(token.SEMICOLON)
			continue
		}
		d.Directives = append(d.Directives, dir)
	}
	d.Rbrace = p.expect(token.RBRACE)
	d.Span = ast.At(lo, p.prevEnd())
	return d
}

func (p *parser) parseModuleDirective() *ast.ModuleDirective {
	lo := p.pos()
	dir := alloc[ast.ModuleDirective](p.arena)

	switch {
	case p.atCtx(token.CtxRequires):
		dir.Kind, dir.KwPos = token.CtxRequires, p.next().Pos
		mods := alloc[ast.Modifiers](p.arena)
		mlo := p.pos()
		for p.at(token.STATIC) || p.atCtx(token.CtxTransitive) {
			t := p.next()
			m := alloc[ast.Modifier](p.arena)
			m.Span = ast.At(t.Pos, t.End)
			m.Kind = t.Kind
			mods.List = append(mods.List, m)
		}
		if len(mods.List) > 0 {
			mods.Span = ast.At(mlo, p.prevEnd())
			dir.Mods = mods
		}
		dir.Name = p.parseName()

	case p.atCtx(token.CtxExports), p.atCtx(token.CtxOpens):
		dir.Kind, dir.KwPos = p.tok().Ctx, p.next().Pos
		dir.Name = p.parseName()
		if _, ok := p.acceptCtx(token.CtxTo); ok {
			for {
				dir.To = append(dir.To, p.parseName())
				if _, ok := p.accept(token.COMMA); !ok {
					break
				}
			}
		}

	case p.atCtx(token.CtxUses):
		dir.Kind, dir.KwPos = token.CtxUses, p.next().Pos
		dir.Name = p.parseName()

	case p.atCtx(token.CtxProvides):
		dir.Kind, dir.KwPos = token.CtxProvides, p.next().Pos
		dir.Name = p.parseName()
		if _, ok := p.acceptCtx(token.CtxWith); !ok {
			p.errorExpected("'with'")
		}
		for {
			dir.With = append(dir.With, p.parseName())
			if _, ok := p.accept(token.COMMA); !ok {
				break
			}
		}

	default:
		p.errorExpected("module directive")
		return nil
	}

	dir.Semi = p.expectSemi()
	dir.Span = ast.At(lo, p.prevEnd())
	return dir
}