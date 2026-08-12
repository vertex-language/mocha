package warn

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// Switch checks, §14.11.
//
// The asymmetry worth knowing: a non-exhaustive switch *statement* over an
// enum is a warning, because control simply falls past it. The same switch as
// an *expression* is an error, because an expression must produce a value on
// every path and there is no value to produce. Same analysis, two severities.

func (c *checker) switchStmt(n *ast.SwitchStmt) {
	if n.Block == nil {
		return
	}
	c.duplicateLabels(n.Block)
	c.exhaustive(n.Tag, n.Block, false)
}

func (c *checker) switchExpr(n *ast.SwitchExpr) {
	if n.Block == nil {
		return
	}
	c.duplicateLabels(n.Block)
	c.exhaustive(n.Tag, n.Block, true)
}

// exhaustive reports a switch that omits cases. isExpr selects the severity.
func (c *checker) exhaustive(tag ast.Expr, b *ast.SwitchBlock, isExpr bool) {
	if hasDefault(b) {
		return
	}
	t := c.info.Type(tag)
	if types.IsError(t) {
		return
	}

	switch {
	case c.isEnumType(t):
		c.enumExhaustive(tag, b, t, isExpr)
	case c.isSealedType(t):
		c.sealedExhaustive(tag, b, t, isExpr)
	case isExpr:
		// A switch expression over anything else needs a default outright:
		// there is no finite set of cases to enumerate.
		c.errorf(tag.Pos(), tag.End(),
			"the switch expression does not cover all possible input values")
	}
}

func (c *checker) enumExhaustive(tag ast.Expr, b *ast.SwitchBlock, t types.Type, isExpr bool) {
	ct, ok := t.(*types.ClassType)
	if !ok || ct.Sym == nil {
		return
	}
	covered := c.labelNames(b)

	var missing []string
	ct.Sym.Members.Each(func(s sym.Symbol) bool {
		v, ok := s.(*sym.VarSym)
		if !ok || v.Var != sym.VarEnumConstant {
			return true
		}
		if !covered[v.Name] {
			missing = append(missing, v.Name)
		}
		return true
	})
	if len(missing) == 0 {
		return
	}
	if isExpr {
		c.errorf(tag.Pos(), tag.End(),
			"the switch expression does not cover all input values: missing %s",
			join(missing))
		return
	}
	c.warnf("fallthrough", tag.Pos(), tag.End(),
		"switch does not cover all enum constants: missing %s", join(missing))
}

func (c *checker) sealedExhaustive(tag ast.Expr, b *ast.SwitchBlock, t types.Type, isExpr bool) {
	subs := c.permittedSubtypes(t)
	if len(subs) == 0 {
		return
	}
	covered := c.patternTypes(b)

	var missing []string
	for _, sub := range subs {
		hit := false
		subType := c.types.ClassOf(sub, nil, nil)
		for _, ct := range covered {
			if c.types.IsSubtype(subType, ct) {
				hit = true
				break
			}
		}
		if !hit {
			missing = append(missing, sym.SimpleName(sub.Binary))
		}
	}
	if len(missing) == 0 {
		return
	}
	if isExpr {
		c.errorf(tag.Pos(), tag.End(),
			"the switch expression does not cover all input values: missing %s",
			join(missing))
		return
	}
	c.warnf("fallthrough", tag.Pos(), tag.End(),
		"switch does not cover all permitted subtypes: missing %s", join(missing))
}

// duplicateLabels reports the same constant appearing twice (§14.11.1).
func (c *checker) duplicateLabels(b *ast.SwitchBlock) {
	seen := map[any]ast.Node{}
	defaults := 0

	eachLabel(b, func(l *ast.SwitchLabel) {
		if l.Default {
			defaults++
			if defaults > 1 {
				c.errorf(l.Pos(), l.End(), "duplicate default label")
			}
		}
		for _, cs := range l.Cases {
			x, ok := cs.(ast.Expr)
			if !ok {
				continue
			}
			k, has := c.info.Const(x)
			if !has {
				// An enum constant is not a constant expression but is still
				// a legal label, matched by name.
				if v, ok := c.info.Use(x).(*sym.VarSym); ok && v.Var == sym.VarEnumConstant {
					if prev := seen[v.Name]; prev != nil {
						c.errorf(x.Pos(), x.End(), "duplicate case label %s", v.Name)
						continue
					}
					seen[v.Name] = x
				}
				continue
			}
			if prev := seen[k.Value]; prev != nil {
				c.errorf(x.Pos(), x.End(), "duplicate case label")
				continue
			}
			seen[k.Value] = x
		}
	})
}

// labelNames collects the enum constant names a switch covers.
func (c *checker) labelNames(b *ast.SwitchBlock) map[string]bool {
	out := map[string]bool{}
	eachLabel(b, func(l *ast.SwitchLabel) {
		for _, cs := range l.Cases {
			x, ok := cs.(ast.Expr)
			if !ok {
				continue
			}
			if v, ok := c.info.Use(x).(*sym.VarSym); ok && v.Var == sym.VarEnumConstant {
				out[v.Name] = true
			}
		}
	})
	return out
}

// patternTypes collects the types a switch's patterns match.
func (c *checker) patternTypes(b *ast.SwitchBlock) []types.Type {
	var out []types.Type
	eachLabel(b, func(l *ast.SwitchLabel) {
		// A guarded pattern does not contribute to exhaustiveness: the guard
		// may be false, so the case is not guaranteed to fire.
		if l.Guard != nil {
			return
		}
		for _, cs := range l.Cases {
			p, ok := cs.(ast.Pattern)
			if !ok {
				continue
			}
			if t := c.patternType(p); t != nil {
				out = append(out, t)
			}
		}
	})
	return out
}

func (c *checker) patternType(p ast.Pattern) types.Type {
	switch n := p.(type) {
	case *ast.TypePattern:
		return c.info.Type(n.Type)
	case *ast.RecordPattern:
		return c.info.Type(n.Type)
	}
	return nil
}

func hasDefault(b *ast.SwitchBlock) bool {
	found := false
	eachLabel(b, func(l *ast.SwitchLabel) {
		if l.Default {
			found = true
		}
	})
	return found
}

// eachLabel visits every label of either switch form. ast populates exactly
// one of Rules and Groups; Labels holds trailing colon-form labels that govern
// no statements, and they count for exhaustiveness too.
func eachLabel(b *ast.SwitchBlock, f func(*ast.SwitchLabel)) {
	if b == nil {
		return
	}
	for _, r := range b.Rules {
		if r.Label != nil {
			f(r.Label)
		}
	}
	for _, g := range b.Groups {
		for _, l := range g.Labels {
			f(l)
		}
	}
	for _, l := range b.Labels {
		f(l)
	}
}

func join(names []string) string {
	s := ""
	for i, n := range names {
		if i > 0 {
			s += ", "
		}
		s += n
	}
	return s
}