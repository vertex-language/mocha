# ast

`package ast` defines the syntax tree mocha's parser builds.

```
import "github.com/vertex-language/mocha/ast"
```

```
go get github.com/vertex-language/mocha/ast
```

Four hierarchies — `Expr`, `Stmt`, `Decl`, `Type` — plus `Pattern`, which §14.30 makes its own grammatical category.

---

## Invariants

**Every node embeds a `Span`.** `Pos` and `End` are *stored*, not derived. A node built during error recovery still has a real, non-empty extent (invariant 3), which a fold over possibly-nil children could not guarantee.

**Nodes hold no text** (invariant 1). An `Ident` is two positions and a `token.Ctx`. A literal is two positions and a `token.Kind`. Decoding `1_024`, stripping a text block's incidental whitespace, and deciding whether a `var` spelling is a keyword all belong to phases above this one.

```go
type Node interface {
	Pos() token.Pos // first byte
	End() token.Pos // one past the last byte
}

type Span struct{ Lo, Hi token.Pos }

func At(lo, hi token.Pos) Span // the parser widens a node over its children
```

The marker methods (`exprNode`, `stmtNode`, `declNode`, `typeNode`, `patternNode`) are unexported, so the five hierarchies are closed: nothing outside this package can put an unrelated node in an expression position.

---

## Usage

```go
package main

import (
	"fmt"
	"os"

	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/parser"
	"github.com/vertex-language/mocha/token"
)

func main() {
	src, _ := os.ReadFile("A.java")

	unit := token.NewFile("A.java", src)
	file, diags := parser.Parse(unit)
	defer file.Release()

	for _, d := range diags {
		p := unit.Position(d.Pos)
		fmt.Printf("%s:%d:%d: %s\n", p.Filename, p.Line, p.Column, d.Msg)
	}

	// Every method name in the unit, with its source location.
	ast.Inspect(file, func(n ast.Node) bool {
		m, ok := n.(*ast.MethodDecl)
		if !ok {
			return true
		}
		p := unit.Position(m.Name.Pos())
		fmt.Printf("%d:%d\t%s\n", p.Line, p.Column, m.Name.Name(unit))
		return true
	})

	ast.Fdump(os.Stdout, unit, file)
}
```

Because the tree holds no strings, anything that reads spelling takes the `*token.File` that produced the tree: `ident.Name(f)`, `f.Slice(lit.Lo, lit.Hi)`, `ast.Fdump(w, f, n)`.

---

## `File` — one compilation unit

Exactly one shape is populated (§7.3):

| Shape | Fields |
| --- | --- |
| ordinary | `Package` optional, `Decls` holds top-level type declarations |
| compact | `Compact` true, `Decls` holds class members with no enclosing class |
| modular | `Module` non-nil |

`File.Unit` is the position space every span in the tree resolves through.

### Release

```go
file, diags := parser.Parse(unit)
defer file.Release()
```

`ast.Releaser` is implemented by whatever owns the tree's backing storage — the parser implements it with its arena, and `ast` neither knows nor imports that. `Release` is safe on a tree that has no releaser and safe to call twice. **Every node in the tree is invalid afterwards**, so do not hold node pointers across it.

---

## What the tree deliberately does not distinguish

The shape follows the JLS where the distinction is syntactic and collapses it where the distinction belongs to a later phase.

- **`Ident`** is an `Identifier`, a `TypeIdentifier` and an `UnqualifiedMethodIdentifier` all at once. Which restriction applied is a property of the production the parser was in, and it has already enforced it. `Ctx` is non-zero whenever the spelling is one of the seventeen contextual keywords — *whether or not this occurrence is one*.
- **`Name`** is a dotted name: `ModuleName`, `PackageName`, `TypeName`, `ExpressionName`, `PackageOrTypeName` or `AmbiguousName`. Which one is resolution's business, so the tree keeps only the parts.
- **`NamedType`** is a `ClassType`, an `InterfaceType` or a `TypeVariable`.
- **The `Unann*` nonterminals do not appear.** They exist only to keep an annotation from being read as part of an enclosing construct — a parsing concern. A `NamedType` with no annotations is what an `UnannClassType` produced.
- **`VarDecl`** is a field, a constant and a local variable declaration. The three differ in permitted modifiers and in where they appear, not in shape. `Semi` is `NoPos` where the declaration is not a statement (a for-init, a resource).
- **`TypePattern`** flattens the `LocalVariableDeclaration` the grammar spells: exactly one declarator, no initializer.
- **The `NoShortIf` variants** are a disambiguation device with no tree consequence; `IfStmt` covers both forms.

Where the distinction *is* real, it survives. `MethodRef.X` is an `ast.Node` because the receiver may be an expression or a type and the ambiguity is genuine. `InstanceOfExpr` has both a `Type` and a `Pattern` field, exactly one non-nil.

---

## Joined operators

`BinaryExpr.Op` may be `token.SHR` or `token.USHR`, and `AssignExpr.Op` may be `token.SHR_ASSIGN` or `token.USHR_ASSIGN` — kinds that **no scanned token ever carries**. The scanner never merges `>` with a following `>`; the parser assembled these from adjacent tokens via `token.Join`.

Both nodes therefore carry two operator positions:

```go
OpPos token.Pos // the first `>`
OpEnd token.Pos // one past the last token of the joined operator
```

The mirror of this is `TypeArgs.Gt`: the position of the single `>` that closed *this* list. Because the scanner never merges, nested arguments need no special handling — each list closes on its own token.

---

## Formatter-facing detail

The tree keeps things a compiler could discard, because a formatter cannot reconstruct them:

- **`Modifiers`** holds annotations and keyword modifiers interleaved, in the order written. The JLS's canonical order is a style rule; a formatter needs the truth. A nil `*Modifiers` means none were written — treat it as empty rather than dereference. `Has` is nil-safe.
- **`EmptyDecl` and `EmptyStmt`** preserve a stray `;`. One node each, and a formatter does not have to invent one.
- **`LambdaExpr.Paren`** records whether the parameter list was parenthesized, which a concise single parameter does not imply.
- **Trailing commas** are kept: `ArrayInit.Comma`, `ElementValueArray.Comma`, `EnumDecl.Comma`.
- **Every delimiter position** — `Lbrace`, `Rparen`, `Lbrack`, `Arrow`, `Colon` — is stored.
- **`SwitchBlock`** populates exactly one of `Rules` and `Groups`; the arrow form and the colon form cannot be mixed. `Labels` holds trailing colon-form labels that govern no statements.

Recovery nodes — `BadExpr`, `BadStmt`, `BadDecl`, `BadType`, `BadPattern` — cover the tokens the parser gave up on, so a consumer can still report a location.

---

## Traversal

```go
func Walk(v Visitor, n Node)
func Inspect(n Node, f func(Node) bool)
```

`Visitor.Visit` returns the visitor to use for a node's children, or nil to skip them, and is called once more with `nil` to signal the end of a subtree. `Inspect` wraps that: `f` returns false to skip a subtree, and is **never** called with a nil node.

Children are discovered by reflection over exported fields, so a node that gains a field is traversed without `walk.go` changing. The trade is a slower walk than a generated one — if traversal shows up in a profile, generate the switch from the node declarations and keep this as the reference implementation. Field order in the struct declarations is source order, so traversal is too.

Typed nils are handled: a `*VarDecl` nil stored in a `Decl` interface is skipped, which a plain `n == nil` would miss.

## Dumping

```go
ast.Fdump(os.Stdout, unit, node)
```

Identifiers and literals print with the text their spans resolve to — hence the `*token.File`. Positions print as `line:column` in **raw** source coordinates, so a dump of a file that used Unicode escapes lines up with what the user typed. Zero-valued fields are omitted.

## Relationship to the other packages

[`token`](../token) defines spans, kinds and the position space. [`scanner`](../scanner) produces tokens. `ast` defines the shape the parser builds and imports nothing but `token`, `reflect`, `fmt` and `io`.