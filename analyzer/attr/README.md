# attr

`package attr` is attribution: it resolves every name in a method body, gives
every expression a type, and reports what [`sym`](../../sym) and
[`types`](../../types) deliberately left undone.

```
import "github.com/vertex-language/mocha/analyzer/attr"
```

This is javac's `Attr`, `Resolve` and `Infer`. `sym` says what is declared,
`types` says what it is, and `attr` is the first phase that reads a method body
at all.

---

## What this package inherits

Every deferral written into the two packages below lands here. The list is not
a roadmap — it is the specification:

| Deferred by | What |
| --- | --- |
| `sym.Scope.conflict` | two methods sharing a name *and* an erased signature |
| `sym.Unit.FindType` | ambiguous on-demand imports, reported against the use site |
| `sym.Unit.FindStatic` | which candidate owner actually declares the member |
| `sym.Enter` | local and anonymous classes; enum constants with bodies |
| `sym.sourceCompleter` | a compact constructor's parameter list |
| `sym.ClassSym.Lookup` | inherited members — supertype visibility is a resolution rule |
| `types.IsSubtype` | generic containment and inference |
| `types.fromAST` | `var`, which needs the initialiser's type |
| `sym.VarSym.Const` | folding a source constant expression |

---

## Invariants

**The tree is not mutated.** `ast` nodes hold no type field and the parser's
arena invalidates every node on `Release`, so attribution returns an
[`Info`](#info) of side tables instead. `Info` is valid exactly as long as the
tree is — parse, enter, attribute, lower, release.

**Every expression gets a type, including a broken one.** A failed resolution
yields `types.ErrorType`, never nil, and `types.IsSubtype` treats an error as
compatible with everything. One bad name costs one diagnostic, not one per use.

**One diagnostic per site.** The parser's rule, carried up: after reporting
against a node, attribution does not report again at the same span, and an
error type propagating outward is silent.

**Resolution order is §6.5's, not a search.** Locals shadow fields shadow
inherited members shadow static imports. Each step is a distinct lookup with a
distinct failure, so "cannot find symbol" can say which step ran out.

**A static context is a property of the environment, not a check after the
fact.** `env.static` is set at the method boundary and consulted at every
`this`, `super`, and instance-member reference.

---

## `Info`

```go
type Info struct {
	Types  map[ast.Node]types.Type    // every expression and resolved type node
	Uses   map[ast.Node]sym.Symbol    // name → what it resolved to
	Consts map[ast.Node]types.Constant // folded compile-time constants
	Local  map[ast.Decl]*sym.ClassSym  // local and anonymous classes entered here
	Diags  []token.Diagnostic
}

func Attr(tt *types.Table, u *sym.Unit) *Info
```

`Uses` is what `lower` and `gen` read to emit an invocation: the `*sym.MethodSym`
overload resolution picked, the `*sym.VarSym` a name denoted, the `*sym.ClassSym`
a type name meant. `Types` is what `flow` reads to know a `boolean` from an
`int`.

---

## Files

| File | §  | Does |
| --- | --- | --- |
| `attr.go` | — | entry point, `Info`, the class and member walk |
| `env.go` | 6.3 | the scope chain: class body, method, block, pattern bindings |
| `resolve.go` | 6.5 | expression, type and method names; reclassification |
| `expr.go` | 15 | expression attribution, the operator switches |
| `stmt.go` | 14 | statements, local classes, labels |
| `apply.go` | 15.12.2 | applicability by phase, most-specific |
| `infer.go` | 18 | constraint gathering and bound resolution |
| `convert.go` | 5 | assignment, invocation and casting contexts; boxing |
| `fold.go` | 15.29 | constant expressions |
| `error.go` | — | diagnostics, one per site |

---

## Two prerequisites

**`types` needs three exported wrappers.** Attribution resolves `ast.Type` nodes
constantly — cast targets, local declarations, `instanceof`, catch clauses,
explicit type arguments — and `types`' resolver is unexported. Three additions,
each a wrapper over what is already there:

```go
func (t *Table) ResolveType(x ast.Type, u *sym.Unit, c *sym.ClassSym, tps []*TypeVar) Type
func (t *Table) Named(binary string) Type
func (t *Table) ClassOf(c *sym.ClassSym, args []Type, outer *ClassType) Type
```

**`sym` needs the `Signature` fields** already described in `types`'
`signature_fields.go`. Without them generic binary methods erase, and overload
resolution against a generic library method sees the erased signature. Correct
for `android.jar`; lossy for anything with real generics.

---

## What this package deliberately does not do

- **Definite assignment and reachability.** `flow`'s, and javac splits them the
  same way. Attribution produces a fully typed tree; `flow` walks it.
- **Style and lint warnings.** `analyzer/warn`.
- **Desugaring.** No tree rewrites. `lower` reads `Info` and builds new nodes.
- **Bridge methods and synthetic accessors.** `lower`'s, for the same reason.