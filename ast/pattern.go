package ast

import "github.com/vertex-language/mocha/token"

// Patterns are their own hierarchy. §14.30 gives them their own nonterminal,
// and the two places they appear — a case label and the right side of
// `instanceof` — admit nothing else, so the marker method is doing real work.

type (
	// TypePattern is `{VariableModifier} Type Identifier`. The grammar spells
	// it as a LocalVariableDeclaration, but exactly one declarator with no
	// initializer, so the tree flattens it.
	TypePattern struct {
		Span
		Mods *Modifiers
		Type Type
		Name *Ident
	}

	// RecordPattern is `ReferenceType ( [ComponentPatternList] )`.
	RecordPattern struct {
		Span
		Type   Type
		Lparen token.Pos
		Elts   []Pattern
		Rparen token.Pos
	}

	// MatchAllPattern is the `_` component pattern.
	MatchAllPattern struct {
		Span
	}

	// BadPattern marks a pattern the parser could not read.
	BadPattern struct {
		Span
	}
)

func (*TypePattern) patternNode()     {}
func (*RecordPattern) patternNode()   {}
func (*MatchAllPattern) patternNode() {}
func (*BadPattern) patternNode()      {}