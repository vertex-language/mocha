# flow

`package flow` walks an attributed tree and answers the four questions Java
makes errors rather than warnings: is this variable assigned, is this statement
reachable, is this exception handled, and is this capture legal.

```
import "github.com/vertex-language/mocha/analyzer/flow"
```

This is javac's `Flow`, and it runs after [`attr`](../attr) for the reason javac
splits them: every one of these questions needs types already resolved, and none
of them can be answered while resolving.

---

## What this package inherits

| Deferred by | What |
| --- | --- |
| `attr.checkAssignable` | whether a blank final was *already* assigned (§16.9) |
| `attr.returnStmt` | that a non-void method returns on every path |
| `attr` `MethodType.Throws` | comparing what is thrown against what is caught or declared |
| `lower` (needs, doesn't defer) | which locals a lambda captures, and whether they may be |

`attr` waves through any assignment to a final inside a constructor, because
telling a first assignment from a second is a dataflow question. This is where
that gets decided.

---

## Invariants

**Nothing is resolved here.** Every type and every symbol comes from
`attr.Info`. If a name did not resolve, `attr` already reported it and this
package stays silent — an `ErrorType` is not a second diagnostic.

**Definite assignment is a fixpoint over bitsets, not a walk.** A loop body is
analysed until its entry state stops changing. One bit per local, indexed
densely per method, so a `while` with twenty locals is twenty bits and not
twenty map lookups.

**Reachability is computed bottom-up and read top-down.** Each statement
reports whether it can complete normally; the caller uses that to decide
whether the next one is reachable. Constant conditions matter — §14.21 makes
`while (true)` and `if (false)` behave differently on purpose, and the folded
values `attr` recorded are what distinguishes them.

**A checked exception is thrown until something catches it.** Throw sets
propagate outward through `try`, get filtered by each `catch`, and hit the
method boundary where they must be covered by `throws`.

**One diagnostic per site**, the same rule the parser and `attr` hold.

---

## `Flow`

```go
type Flow struct {
	// Captured is every local a lambda or inner class reads from an
	// enclosing method. lower reads this to know what to copy into
	// synthetic fields.
	Captured map[*sym.MethodSym][]*sym.VarSym

	// EffectivelyFinal is every local never reassigned after initialisation.
	// A capture is legal only for a variable in this set (§4.12.4).
	EffectivelyFinal map[*sym.VarSym]bool

	// Unreachable marks statements §14.22 makes an error, so lower can drop
	// them rather than emit code gen would reject.
	Unreachable map[ast.Stmt]bool

	Diags []token.Diagnostic
}

func Analyze(in *attr.Info, tt *types.Table, u *sym.Unit) *Flow
```

---

## Files

| File | §  | Does |
| --- | --- | --- |
| `flow.go` | — | entry point, `Flow`, the per-method walk |
| `alive.go` | 14.22 | reachability; can-complete-normally |
| `assign.go` | 16 | definite assignment and definite unassignment |
| `bits.go` | — | the dense bitset the fixpoint runs on |
| `throws.go` | 11.2 | thrown sets, catch filtering, `throws` coverage |
| `capture.go` | 4.12.4 | effectively final, and what a lambda closes over |

---

## Definite assignment

§16 tracks two facts per variable at every point, and they are not
complements: a variable can be neither definitely assigned nor definitely
unassigned, which is exactly the state that makes a second assignment to a
blank final an error and a read of it an error too.

The awkward part is boolean expressions. `if (a && b)` assigns differently on
the true and false branches, so a condition produces *two* states rather than
one, and §16.1 spells out the rule for every operator. `assign.go` implements
that split — `whenTrue` and `whenFalse` — because the alternative is failing
the idiom every Java program uses:

```java
int x;
if (cond && (x = f()) > 0) { use(x); }   // x is DA in the then-branch only
```

---

## Reachability

A statement's analysis returns whether it *can complete normally*. The rules
that surprise people are all constant-condition rules:

- `while (true)` cannot complete normally; the statement after it is
  unreachable. `while (cond)` always can, even if `cond` is always true at
  runtime — only a constant expression counts.
- `if (false) s;` does **not** make `s` unreachable. §14.21 carves out `if`
  specifically so that conditional compilation with a `static final boolean`
  keeps working.
- A `switch` with a `default` and no fall-out is not completable normally.

---

## Checked exceptions

Unchecked ones are excluded by supertype test: anything under `RuntimeException`
or `Error`. What remains must be caught by an enclosing `catch` whose type is a
supertype, or declared in the enclosing method's `throws`.

Two rules worth naming:

- **A `catch` that cannot fire is an error** (§11.2.3) — for a `catch` of a
  checked type the body cannot throw. `Exception` and `Throwable` are exempt.
- **try-with-resources throws from `close()`** as well as from the body, and
  §14.20.3.2 gives those the same treatment as anything in the block.

---

## What this package deliberately does not do

- **Report style problems.** An unused local, a dead store, a missing
  `@Override` — `analyzer/warn`'s.
- **Rewrite anything.** `Unreachable` is a marking; dropping the code is
  `lower`'s.
- **Constant folding.** `attr.Info.Consts` already has it.
- **Null analysis.** Java does not specify one, so neither does this.