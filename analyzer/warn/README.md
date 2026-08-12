# warn

`package warn` is the last frontend phase: the checks that need everything
resolved but belong to no earlier phase, plus the warnings a compiler owes its
user.

```
import "github.com/vertex-language/mocha/analyzer/warn"
```

Despite the name it emits both severities. Several of these are hard errors in
Java — a `transient` method, an unimplemented abstract member, a subclass
missing from a `permits` clause — they simply have no natural home in
[`attr`](../attr) or [`flow`](../flow). Inventing a fifth analyzer package to
separate them by severity would split checks that share one walk.

---

## What this package inherits

Every phase below left something here, and said so:

| Deferred by | What |
| --- | --- |
| `parser` | modifier legality — *"a worse diagnostic than the phase that knows what a method is"* |
| `parser` | compilation unit shape; `Compact` is set and not validated |
| `parser` | a resource must declare one variable with an initializer |
| `parser` | a `TypeArgument` may not be a primitive |
| `parser` | lambda parameter forms may not be mixed |
| `ast.AnnotationDecl` | `sealed` and `non-sealed`, *"syntactically admissible in Mods and rejected later"* |
| `sym` | `FlagDeprecated`, carried from both source and class files, never consulted |
| `types` | sealed `Permits`, resolved and never checked |

---

## Invariants

**Nothing is resolved here.** Types come from `attr.Info`, the supertype graph
from `types.Table`, captures from `flow.Flow`. A name that failed to resolve was
reported twice already; this package stays quiet.

**Errors before warnings, per declaration.** A class missing an abstract method
is an error; the same class having an unused import is not. Reporting the second
first buries the first.

**A warning is suppressible; an error is not.** `@SuppressWarnings` is honoured
for the checks that emit `SevWarning` and ignored for the rest, which is what
the annotation means.

**One diagnostic per site**, held from the parser down.

---

## `Warn`

```go
type Warn struct {
	Diags []token.Diagnostic
}

func Check(in *attr.Info, fl *flow.Flow, tt *types.Table, u *sym.Unit) *Warn
```

Takes all three prior results because the checks genuinely need all three:
override correctness needs `types`, unused-local needs `flow`'s view of reads,
and everything needs `attr`.

---

## Files

| File | Emits | Does |
| --- | --- | --- |
| `warn.go` | — | entry point, the declaration walk, `@SuppressWarnings` |
| `modifiers.go` | error | §8.1.1, §8.3.1, §8.4.3, §9 — which modifiers a declaration admits |
| `override.go` | both | `@Override`, return covariance, abstract completeness, final overriding |
| `sealed.go` | error | §8.1.1.2 — `permits` conformance, and who may extend |
| `shape.go` | error | the parser's leftovers: units, resources, lambdas, patterns |
| `unused.go` | warning | unused locals, unused imports, unused private members |
| `deprecate.go` | warning | use of a deprecated type or member |
| `switch.go` | both | exhaustiveness over an enum or sealed type; duplicate labels |

---

## Modifier legality

The table nobody wants to write and every compiler needs. §8 and §9 give each
declaration position its own admissible set, and the interesting entries are the
prohibitions rather than the permissions:

- An interface method may not be `synchronized`, `native`, `final`, or
  `protected` — but may be `default`, `static` or `private` since 9.
- A field may not be both `final` and `volatile` (§8.3.1.4): the combination is
  contradictory, since `final` already forbids the writes `volatile` orders.
- A class may not be both `final` and `abstract`, or both `final` and `sealed`.
- `strictfp` is meaningless from 17 (JEP 306 made everything FP-strict) and is
  accepted silently rather than warned about, since the source is still legal.
- An annotation interface admits neither `sealed` nor `non-sealed`, which is why
  `ast` lets them through and says so.

Access modifiers are checked as a group: at most one of `public`, `private`,
`protected`, since absent means package access and has no keyword.

---

## Override checking

Four things, all needing the supertype graph:

- **`@Override` names nothing.** The annotation is on a method that overrides no
  supertype method. Error, not warning — §9.6.4.4.
- **A concrete class has an unimplemented abstract method.** Walk every
  supertype, collect abstracts, subtract what is implemented.
- **An override narrows access or returns an incompatible type.** Return
  covariance is permitted (§8.4.8.3); narrowing visibility is not.
- **An override of a `final` method.** Error.

Erased signatures are the matching key throughout, the same key
`attr.checkOverloads` uses — two methods override each other exactly when the
JVM would consider one to replace the other.

---

## Exhaustiveness

A `switch` over an enum without a `default` that omits a constant is a warning
in statement form and an **error** in expression form, because a switch
expression must produce a value on every path. The same asymmetry applies to a
sealed hierarchy: §14.11.2 makes the pattern switch exhaustive or invalid.

---

## What this package deliberately does not do

- **Re-resolve or re-check types.** `attr` did.
- **Dataflow.** `flow` did — unused-local here means "never read", which
  `flow` already knows.
- **Rewrite.** Emitting the missing bridge or the default constructor is
  `lower`'s.
- **Enforce naming conventions.** Not the compiler's business.