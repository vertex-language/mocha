# parser

`package parser` turns a `*token.File` into an `*ast.File` plus a sorted diagnostic slice.

```
import "github.com/vertex-language/mocha/parser"
```

```
go get github.com/vertex-language/mocha/parser
```

Recursive descent for declarations and statements, precedence climbing for expressions, one mark/rollback mechanism for the three genuinely ambiguous prefixes.

The parser interprets nothing. It decides which production applies and where each node begins and ends; it does not decode literals, resolve names, or check that a contextual keyword was used in a sensible place beyond the production admitting it.

**A partial parse is a usable one.** Every entry point returns a node — a `Bad*` placeholder if it has to — so consumers read a tree, not a success flag.

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
	src, err := os.ReadFile("A.java")
	if err != nil {
		panic(err)
	}

	unit := token.NewFile("A.java", src)
	file, diags := parser.ParseFile(unit, parser.DefaultMode)
	defer file.Release()

	for _, d := range diags {
		p := unit.Position(d.Pos)
		fmt.Printf("%s:%d:%d: %s: %s\n", p.Filename, p.Line, p.Column, d.Severity, d.Msg)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		if m, ok := n.(*ast.MethodDecl); ok {
			fmt.Println(m.Name.Name(unit))
		}
		return true
	})
}
```

`ParseFile` runs the scanner itself, so the caller never touches `scanner` directly. The returned tree is **never nil**, and diagnostics from escape translation, scanning and parsing arrive merged and sorted in one slice.

### Modes

```go
const (
	ParseComments Mode = 1 << iota
	HeaderOnly
	Tolerant
)

const DefaultMode Mode = 0
```

| Mode | Effect |
| --- | --- |
| `ParseComments` | Retains comment tokens on the tree's `File`. Without it comments are trivia, recoverable from spans via `token.File.Between`. |
| `HeaderOnly` | Stops after the package declaration, the imports and any module directives. Type bodies are skipped balanced, not parsed. |
| `Tolerant` | Keeps going past the resync budget instead of abandoning the rest of the unit. Useful for editors, wasteful for batch builds. |

A note on `HeaderOnly`: in Java the result is a **lower bound** on the dependency graph, never the graph. Same-package types need no import, on-demand imports name a package, module imports name a module, and a fully qualified name can appear inline in any expression. Treat it as a fast pre-pass, not as an answer.

---

## Lifetime

Nodes come from an arena that batches allocation into per-type chunks and hands the whole set back at once, so a whole-program build does not hold every tree it has ever parsed. The arena is unexported: `ast` declares the one-method `Releaser` interface and the arena implements it, which costs `ast` no import.

```go
file, diags := parser.ParseFile(unit, parser.DefaultMode)
defer file.Release()
```

**Release is a promise, not a check.** Every node in the tree is invalid afterwards, and nothing detects a caller that kept a pointer. Consumers that need a node past that lifetime copy what they need — a span and a string, usually.

---

## Contextual keywords

The scanner tags; the parser decides. `p.atCtx(c)` is the whole policy, and it is called exactly where a production admits the keyword — which is what keeps the spelling usable as a name everywhere else.

```java
record Point(int x, int y) {}   // a declaration
record = 3;                     // an assignment

yield x + 1;                    // a yield statement
yield(1);                       // a method call
yield = 2;                      // an assignment

sealed interface Shape {}       // a modifier
int sealed = 0;                 // a variable
```

Three of these need a rule rather than a single token of lookahead:

- **`record`** is a declaration only when followed by an identifier and then `(` or `<`.
- **`yield`** is a statement only when the next token could *not* continue an expression begun by an identifier — `startsExprContinuation` covers `(`, `.`, `=`, `[`, `::`, `++`, `--`, `;` and every binary operator.
- **`module`** in an import is a module import only when an identifier follows, so `import module.foo.Bar;` stays a type import.

`§3.8`'s restricted identifiers are enforced at the two call sites that need them: `parseTypeIdent` rejects `permits`, `record`, `sealed`, `var` and `yield`; `parseMethodIdent` rejects `yield`. Both report and then parse the identifier anyway, so a bad name costs one diagnostic and no cascade.

---

## Speculation

The token buffer is immutable and the cursor is an integer, so a checkpoint is a struct copy and a rollback is an assignment. Nodes allocated during a failed attempt stay in the arena as garbage until `Release` — cheap, and much simpler than unwinding allocation.

```go
v, ok := spec(p, func() (*ast.Thing, bool) { ... })   // value-producing
ok := p.speculate(func() bool { ... })                // predicate
```

Rollback restores the cursor **and discards any diagnostics the attempt produced**. Discarding is the point: a failed speculation is not an error, and reporting one would violate the one-diagnostic rule with noise the user cannot act on. The mark also saves `quiet`, `lastErr`, `resyncs` and `depth`, so an abandoned attempt cannot spend the recovery budget.

Three sites genuinely need it, and only three:

- `(` opening a **cast** versus a parenthesized expression
- `(` opening a **lambda parameter list** versus either of the above
- `<` opening **type arguments** versus less-than

A fourth case looks like speculation and is not: a local variable declaration versus an expression statement is decided by *trying* the declaration, which uses the same mechanism but commits on the declarator identifier. A `var` or a modifier settles it immediately; otherwise a type followed by an identifier or `_` is a declaration, and anything else was an expression all along.

### The two casts

A cast to a primitive type takes any `UnaryExpression`, so `(int) -x` is a cast. A cast to a reference type takes a `UnaryExpressionNotPlusMinus`, so `(a) - b` is a subtraction. An intersection cast is always a reference cast.

---

## The `>` rule, from the other side

`scanner` never merges `>` with a following `>`. The parser is where that pays off, and it pays off twice:

**Type arguments need no special handling at all.** `List<List<String>>` closes on two separate tokens and each level consumes exactly one. Nothing in `type.go` calls `token.Join`.

**Shift operators are rejoined on demand**, in `binaryOp` and `assignOp`, and only where a shift is admissible:

```go
if joined, n := token.Join(p.toks[p.i:]); n > 0 {
	// joined is SHR, USHR, SHR_ASSIGN or USHR_ASSIGN; n is 2 or 3
}
```

`token.Join` checks adjacency, so a non-adjacent `a > > b` joins nothing and is left to fail as a comparison against a comparison — the error the user wants to see. `binaryOp` deliberately returns nothing for the two `*_ASSIGN` forms, leaving them to `assignOp`. Both record `OpPos` and `OpEnd` on the node, since a joined operator spans more than one token.

`instanceof` is the other escape from plain precedence climbing: it shares the relational level but takes a type or a pattern on its right, so the operand loop breaks out for it.

---

## Errors and recovery

Invariant 4, restated for this phase: **one recoverable diagnostic, never a cascade.**

After an error the parser goes quiet until it successfully consumes a token, and it never reports twice at the same position. `advanceTo` resyncs to a follow set, stepping *over* balanced bracket groups so a `;` inside a nested block does not look like the statement terminator being searched for. Past `maxResync` (100) attempts the parser stops reporting and runs to EOF, unless `Tolerant` is set.

Two structural guards:

- **`maxDepth` (1000)** caps nesting in declarations, statements, types and expressions. Deeply nested generated sources exist; unbounded recursion on hostile input does not have to.
- **Progress checks.** Every list loop — members, block statements, switch groups — compares the cursor before and after and forces a resync if nothing was consumed, so a malformed member cannot spin.

`Bad*` placeholders (`BadExpr`, `BadStmt`, `BadDecl`, `BadType`, `BadPattern`) cover the tokens the parser gave up on, and each span is non-empty even when nothing was consumed (invariant 3).

---

## Deliberate non-decisions

The parser stops at the grammar. These all parse and are left for a later phase:

- **Modifier legality.** All modifier lists are read with one function. Rejecting `transient` on a method here would only produce a worse diagnostic than the phase that knows what a method is.
- **Compilation unit shape.** A unit is compact only if it contains a member that only a compact unit can have; the parser reads top-level members either way and sets `Compact` afterwards.
- **`TypeArgument` primitives.** A `TypeArgument` is a `ReferenceType`, so a primitive is admissible only as an array element type. The shape is a type; the check is semantic.
- **Resource declarations.** That a declaring resource must declare exactly one variable with an initializer is semantic.
- **Pattern variables.** That a multi-pattern label may declare none, and that its guard governs the whole label, are semantic.
- **Lambda parameter uniformity.** Concise and normal forms cannot be mixed, but both produce an `ast.Param` here, with `Type` nil for the concise one.

One thing the parser *does* enforce, because the grammar does: a `try` with no catch, no finally and no resources is reported.

### Names stay names

A dotted name stays an `*ast.Name` for as long as it can. Resolution decides whether `a.b.c` is a package, a type or a field chain; collapsing it into `SelectorExpr` chains here would discard what the parser knew.

## Relationship to the other packages

[`token`](../token) defines the vocabulary and position space, [`scanner`](../scanner) produces tokens, [`ast`](../ast) defines the tree. `parser` imports all three and is imported by nothing below it.