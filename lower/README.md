# lower

`package lower` is code generation: it walks an attributed, checked tree and
drives [`classfile.Builder`](../classfile) until a class file falls out.

```
import "github.com/vertex-language/mocha/lower"
```

This is javac's `TransTypes`, `Lower` and `Gen` in one package, and the reason
they are one package is that there is nothing between them to name. **The class
file is the IR.** `classfile.Builder` is the intermediate representation every
Java compiler eventually produces; building a second one to hold the same facts
on the way there would be a data structure whose only consumer is the code three
hundred lines below it.

`attr` says what everything is. `flow` says what is assigned, reachable and
captured. `warn` says whether it is legal. This package is the first that asks
what it *runs as*, and the last that knows Java existed.

> **Status: partial.** Both passes run end to end for classes, interfaces,
> fields, methods, constructors, initialisers, captures, bridges, accessors and
> lambdas. `switch` and record members are not emitted; pattern switches and
> switch expressions are refused with a diagnostic. See
> [What is not done](#what-is-not-done) for the full list and
> [Open questions](#open-questions-for-attr) for what is blocked on other
> packages.

---

## Where this sits

```
  *ast.File ─┬─ attr.Info ──┐
             ├─ flow.Flow ──┼──→ lower ──→ *classfile.Builder ──→ .class
             └─ sym.Unit ───┘                                        │
                              ══════ the waist ══════                │
                                                                     ↓
                                                                ir/builder
                                                                     ↓
                                                                    ir      ← SSA
                                              ┌──────────────────────┼──────────────────────┐
                                              ↓                      ↓                      ↓
                                       target/dalvik          target/amd64          target/arm64
```

Below the waist nothing knows Java exists. Everything downstream reads class
files and has never heard of this package — `target/dalvik` no more than a JVM
does.

---

## No intermediate representation

The alternative was a desugared tree — lambdas hoisted, generics erased,
conversions explicit — which a separate `gen` would then walk. It was rejected
because **the desugared tree is never read.** A foreach does not need to *become*
an iterator loop that something can look at; it needs to emit the opcodes an
iterator loop would have emitted. That is a pure function of `(ast.Node,
attr.Info, flow.Flow)` and a `*classfile.CodeWriter`, and nothing about it
survives the call. The same holds for every desugaring in the table below.

Two costs, named rather than discovered:

- **There is no dump of the desugared form**, because none is built. `javap -c`
  on the output is the dump, and the `javac` disassembly diff `classfile`
  documents is a better test than reading a tree — it compares against a
  reference implementation rather than against our own idea of what the
  desugaring should have been.
- **Emission is not testable without a frontend.** There is no hand-buildable
  method object; tests are small single-method fixtures through the whole
  pipeline.

This is ECJ's design, not javac's. The governing rule — *do what `javac` does,
deviate only where the target forces it* — is about phase order and semantics,
both held exactly. javac's data structures are a consequence of a mutable AST
carrying `type` and `sym` on the node, which [`ast`](../ast) declined on purpose.

Note that the SSA in [`ir`](../ir) is a different thing in the other direction:
bytecode in, SSA out, below the waist. It is what `target/dalvik` builds *from*
this package's output, exactly as d8 does, and it is not an alternative to
anything here.

---

## Invariants

**Two passes, and the split is the architecture.** `classfile`'s encoder may run
a method's body closure more than once — branch widening cannot be expressed in
a single fixup pass, and replay is how it converges. Widening decisions are keyed
on branch ordinal, which is stable *only because the closure is deterministic*.
So: everything with a side effect happens in pass one, outside the closure.
Everything inside the closure is a pure function of the tree.

**No diagnostics from pass two.** Everything reportable was reported by `attr`,
`flow` or `warn`; a diagnostic inside a replayed closure would fire twice and
break the one-diagnostic rule held from the parser down. A condition pass two
cannot emit is a bug in a phase below, and panics through `bug()`.

**A compilation unit does not survive this package.** In goes one `*sym.Unit`;
out come N independent classes. `Outer$1` is a sibling of `Outer`, not a child.
Nothing below has a notion of "file" beyond the `SourceFile` attribute.

**The tree is not mutated**, the same rule `attr` holds. No node is synthesized,
because there is no tree to put one in.

**The frontend's lifetime ends here.** A source symbol is valid only while its
tree is: parse, enter, attribute, check, lower, release. This package is the last
reader of both, so `Release` is safe the moment `Lower` returns.

**Erasure before desugaring.** javac splits `TransTypes` from `Lower` and cannot
reorder them; the same ordering holds inside pass two, where a type is erased at
the point it is consulted for an opcode and never before.

**Output is reproducible.** Anything read from a map is sorted before it reaches
the `Builder`: `attr.Info.Local`, `flow.Captured`, and the accessor table. Same
inputs and flags, byte-identical artifact.

---

## Usage

```go
package main

import (
	"log"

	"github.com/vertex-language/mocha/analyzer/attr"
	"github.com/vertex-language/mocha/analyzer/flow"
	"github.com/vertex-language/mocha/analyzer/warn"
	"github.com/vertex-language/mocha/classpath"
	"github.com/vertex-language/mocha/lower"
	"github.com/vertex-language/mocha/parser"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

func main() {
	cp := classpath.New(classpath.Options{Release: 8})
	defer cp.Close()

	st := sym.NewTable(cp)
	tt := types.NewTable(st)

	unit := token.NewFile("Fetch.java", src)
	file, diags := parser.ParseFile(unit, parser.DefaultMode)
	defer file.Release()

	u, _ := sym.Enter(st, file)
	tt.Register(u)

	in := attr.Attr(tt, u)
	fl := flow.Analyze(in, tt, u)
	wn := warn.Check(in, fl, tt, u)

	if anyErrors(diags, in.Diags, fl.Diags, wn.Diags) {
		return // never lower a broken unit
	}

	classes, diags := lower.Lower(in, fl, tt, u)
	if len(diags) > 0 {
		log.Fatal(diags)
	}

	for _, c := range classes {
		if err := c.WriteFile(path(c)); err != nil {
			log.Fatal(err)
		}
	}
}
```

```go
func Lower(in *attr.Info, fl *flow.Flow, tt *types.Table, u *sym.Unit) (
	[]*classfile.Builder, []token.Diagnostic)
```

A `*classfile.Builder` per class — top-level, member, local, anonymous, and one
per lambda. The caller decides what becomes of them: a file under
`out/com/example/`, an entry in a jar, or input to `ir/builder` on the way to
dex. This package never writes anything.

The returned diagnostics come from pass one only, and in practice are empty: a
unit that survived `warn` is a unit this package can emit. They exist for the one
class of failure no earlier phase can see — a construct the encoder does not
support at 49.0, reported against a source position rather than panicking.

**Lower nothing that did not type-check.** Emitting from a tree with an
`ErrorType` in it produces a class file that fails verification, which is a worse
diagnostic than the one already reported.

---

## Pass one — declare

Runs once per class, mutates the `Builder`, and is where every member the
language adds on your behalf comes from.

| Adds | § | State |
| --- | --- | --- |
| default constructor | 8.8.9 | ✅ |
| implicit `super()` | 8.8.7 | ✅ |
| field initialisers, instance blocks | 8.6 | ✅ folded into every constructor that does not chain to `this(...)`, in source order, after `super()` |
| `<clinit>` | 8.7 | ✅ |
| `ConstantValue` on a folded `static final` | 4.7.2 | ✅ emits no code |
| `this$0` | 8.1.3 | ✅ plus the constructor parameter that sets it |
| capture fields | 4.12.4 | ✅ one per `flow.Captured` entry, plus the constructor parameters |
| bridge methods | 8.4.8.3 | ✅ covariant returns and generic overrides |
| `access$NNN` | — | ✅ private cross-class access; no `NestHost` at 49.0 |
| lambda classes | 15.27 | ✅ body hoisted to `lambda$m$N`, plus a synthetic class implementing the functional interface |
| enum members | 8.9.3 | ❌ `$VALUES`, `values()`, `valueOf(String)` |
| record members | 8.10.3 | ❌ `equals`, `hashCode`, `toString` written longhand |

It also ends the frontend's vocabulary, in three translations, all done:

- **`sym.Flags` → `classfile.Flags`** (`flags.go`). `sealed`, `non-sealed` and
  `default` have no class-file bit and are gone by here: sealedness was `warn`'s
  check and is over, and 49.0 admits no `default` method. `ACC_STRICT` is
  deliberately unmapped — it is only a flag in majors 46–60, and JEP 306 made
  every method FP-strict anyway. Nothing below sees a source modifier.
- **Descriptors become facts.** `types` already filled `MethodSym.Descriptor`
  and `VarSym.Descriptor`. Pass one copies them and adjusts only what it changed
  — constructors that gained capture parameters, accessors, bridges.
- **Local slots** (`slot.go`). Assigned here, stored where pass two and
  `LocalVariableTable` both read them. Slots are reused across disjoint scopes;
  `long` and `double` take two. Assigning inside the closure would survive a
  replay and break the widening fixpoint.

**Synthetic accessors need a scan, not discovery.** You learn that `access$000`
is required while emitting a body that reads a private member of an enclosing
class — by which time pass one is over and the method must already exist. javac
hits this exactly, and says so: some checks during lowering require that all
synthetic members have already been added to the class and its supertypes. So
pass one walks bodies looking for cross-class private access, the way `flow`
already walks them for captures.

**Captures are stored before `super()`.** Illegal for ordinary code, legal here
because §8.8.7's prologue rule exempts assignments to the fields of the class
being constructed. Storing after `super()` would let a superclass constructor
calling an overridden method see a null capture.

---

## Pass two — emit

The closure. Post-order, one `CodeWriter` call per operation, nothing retained.

### Desugarings

| Construct | Becomes | State |
| --- | --- | --- |
| enhanced `for` | an `Iterator` loop, or an indexed loop over an array | ✅ |
| try-with-resources | try/finally with `addSuppressed`, per §14.20.3 | ✅ |
| `synchronized` | `monitorenter` plus a catch-all `monitorexit` | ✅ |
| string `+` | a `StringBuilder` chain, spine flattened | ✅ |
| boxing, unboxing | `valueOf` / `intValue` and friends | ✅ |
| implicit widening, narrowing | an explicit conversion opcode | ✅ |
| varargs call | explicit array creation at the call site | ⚠️ shape-inferred — see [Open questions](#open-questions-for-attr) |
| `assert` | a `$assertionsDisabled` guard and a throw | ⚠️ guard emitted; the `<clinit>` that fills it is not |
| `instanceof` pattern | test, cast, store, in value position | ✅ |
| `++`, `--`, `+=` | explicit read, operate, write | ✅ |
| a folded constant | its literal, from `attr.Info.Consts` | ✅ |
| a statement in `flow.Unreachable` | nothing | ✅ |
| `switch` on an integer | `tableswitch` or `lookupswitch` | ❌ |
| `switch` on `String` | hash switch, then `equals` chains | ❌ |
| `switch` on an enum | ordinal lookup through a synthetic `$SwitchMap` array | ❌ |
| pattern `switch` | — | ❌ refused in pass one |
| `switch` expression | — | ❌ refused in pass one |

### The four things emission owns

**Opcodes are chosen by erased type.** Every expression picks its `i`/`l`/`f`/
`d`/`a` variant from the type `attr` recorded. An array element load is `iaload`
or `aaload` by element type, not by anything syntactic.

**A statement's value is discarded.** The same node emits differently in
expression and statement position — `i++` as a statement leaves nothing on the
stack, as an expression leaves the old value. ECJ restarts code generation when
it gets this wrong; the tree shape tells us up front, so we do not.

**A condition is a branch, not a value.** `if (a && b)` never materialises a
boolean. Comparisons fuse into `if_icmplt` rather than producing 0/1 and testing
it, `!` inverts the branch rather than emitting anything, and `&&`/`||`
short-circuit into jump targets. A boolean becomes a value only where one is
genuinely required — an assignment, an argument, a return — through `condValue`.
The `g`/`l` variant of `fcmp` and `dcmp` is picked so NaN fails whichever test is
being emitted.

**An lvalue is evaluated once.** `a[i()] += f()` evaluates the arrayref and the
index one time, `dup2`s them to reload, and `dup_x2`s the result out if the
enclosing expression wants it. This is javac's `Items`, and it is why compound
assignment is not desugared into a temp: the JVM has the instructions, and
spilling would generate worse code than `javac` for no reason.

### And the two the class file needs

`try` bodies produce exception table entries from the PCs the walk passes
through, which is why control flow stays structured rather than flattened — the
lexical extent *is* the range. `finally` is duplicated onto every path out of the
block, including `return` and `break` crossing it. Duplication rather than `jsr`:
`jsr`/`ret` are deprecated from 51 and were never worth the subroutine merging
they force on a verifier.

`LineNumberTable` and `LocalVariableTable` are both keyed by PC, so both are
recorded during emission, from the spans `ast` stored and the slots pass one
assigned. LVT rows are recorded in label form and converted to PCs after the
widening fixpoint converges — an offset captured mid-pass would be stale the
moment an earlier branch widened.

---

## Files

| File | § | Does | State |
| --- | --- | --- | --- |
| `lower.go` | — | entry point, the class walk, `Builder` construction | ✅ |
| `flags.go` | — | `sym.Flags` → `classfile.Flags`, per location | ✅ |
| `declare.go` | 8.6–8.9 | pass one: implicit and synthetic members | ✅ |
| `capture.go` | 4.12.4 | `this$0`, capture fields, rewritten constructor descriptors | ✅ |
| `lambda.go` | 15.27 | hoisted bodies and the classes that implement the interface | ✅ |
| `bridge.go` | 8.4.8.3 | bridges and `access$NNN` | ✅ |
| `slot.go` | — | local slot assignment and reuse | ✅ |
| `stmt.go` | 14 | statement emission, loops, `try` | ⚠️ no `switch` |
| `expr.go` | 15 | expression emission, the operator switches | ✅ |
| `cond.go` | 15.23 | conditions as branches; short-circuit targets | ✅ |
| `item.go` | — | the lvalue protocol: load, store, duplicate | ✅ |
| `convert.go` | 5 | boxing, widening, narrowing, checkcast | ✅ |
| `string.go` | 15.18.1 | concatenation | ✅ |
| `switch.go` | 14.11 | the three switch shapes | ❌ not written |
| `record.go` | 8.10.3 | `equals`, `hashCode`, `toString` longhand | ❌ not written |
| `enum.go` | 8.9.3 | `$VALUES`, `values`, `valueOf`, constant construction | ❌ not written |

---

## What 49.0 decides for us

`classfile.NewBuilder` targets 49.0 and refuses 50.0 and up, because from 50.0
the verifier expects a `StackMapTable` and generating correct frames means
implementing §4.10.1. That one constraint settles several designs, all the same
way:

- **No `invokedynamic`.** Lambdas and method references cannot go through
  `LambdaMetafactory`, so they are synthetic classes capturing by final field —
  which means lambdas and anonymous classes share one mechanism rather than two.
- **No `StringConcatFactory`**, hence `StringBuilder`.
- **No `ObjectMethods`**, hence record members written longhand.
- **No `NestHost`/`NestMembers`**, hence `access$NNN`.

Free for the Android path: dex has no `invoke-polymorphic` below API 26 either,
so one lowering serves both targets and neither needs a desugaring step bolted on
afterwards — which is the step d8 exists to fold in. It is a consequence of the
encoder's ceiling, not a limitation of the language support.

Records and sealed types stay compile-time constructs here: a record emits as a
plain class, since `java/lang/Record` does not exist on a runtime that loads
49.0. The version cap is about verification format; the runtime floor is a
separate question and not this package's to answer.

One thing 49.0 gives rather than takes: `CONSTANT_Class` became loadable at
exactly this version, which is what makes `Foo.class` and the
`desiredAssertionStatus` call in an assert guard emittable at all.
`CodeWriter.Cconst` gates on it.

---

## Prerequisites on `classfile`

All three of the original blockers have landed, and one more was added:

| Needs | State |
| --- | --- |
| `CodeWriter.TryCatch` | ✅ `try`, `finally`, try-with-resources and `synchronized` all emit |
| `CodeWriter.TableSwitch` / `LookupSwitch` | ✅ padding recomputed per replay; a switch offset is `s4` and never widens |
| `CodeWriter.Cconst` | ✅ `ldc` of a class constant, gated on 49.0 |
| `CodeWriter.MultiANewArray` | ✅ `new int[a][b]` |
| `CodeWriter.Local` | ✅ `LocalVariableTable` rows in label form |

Two fixes went in alongside them, both found while writing this package:
`monitorenter`/`monitorexit` were recorded as popping nothing, which left
`max_stack` one too low for every `synchronized` block; and `athrow` now goes
through the `simpleDelta` table rather than a bespoke `Throw` path.

The remaining `classfile` gap that touches this package is narrower than the old
known-gaps list suggested. Slot arithmetic in `simpleDelta` is correct for the
category-2 forms — `dup2` on a `long` pops 2 and pushes 4, exactly as on two
`int`s — so `max_stack` is right. What depth-only tracking cannot do is tell
*this* package which of `dup`/`dup2` and `dup_x1`/`dup2_x1` is correct; that is
`item.go`'s job, and it decides on `types.Slots`.

---

## What is not done

In rough order of what blocks the most:

1. **`switch.go`.** Three shapes, one dispatch. Integer switches choose between
   `tableswitch` and `lookupswitch` on density, the way javac does. `String`
   switches hash on `hashCode` and then chain `equals`, in two switches. Enum
   switches index a synthetic `$SwitchMap$...` array. **Open decision:** javac
   puts the switch map in a synthetic holder class (`Outer$1`); a static field on
   the enclosing class is simpler and diffs badly against the reference. Pick one
   before writing it. The hooks `declare.go` calls — `enumStatics` and
   `switchMapStatics` — exist and return nothing.

2. **`enum.go`.** `$VALUES`, `values()`, `valueOf(String)`, and the `<clinit>`
   that constructs each constant. Enum switches depend on the ordinal, so this
   and the switch map want writing together.

3. **`record.go`.** `equals`, `hashCode` and `toString` longhand, plus the
   canonical constructor's parameter list, which `sym.sourceCompleter` left as an
   implicit flag for `types` to fill.

4. **The assert `<clinit>` contribution.** `assertStmt` emits the
   `$assertionsDisabled` guard and calls `needAssertions()`, which does not yet
   add the field or the `Class.desiredAssertionStatus` initialiser to `<clinit>`.
   The `Cconst` it needs is in place.

5. **`classCtx` and `pendingBody` fields.** `declare.go`, `bridge.go` and
   `lambda.go` reference `fields`, `bridges`, `accessors` and `lambdas` on
   `classCtx`, and `bridge`, `accessor`, `accDesc` and `lambdaExpr` on
   `pendingBody`. They are used but not yet declared in `lower.go`.

6. **`isSuperOrImplicit`** is written as a method on `*ast.ConstructorCall`,
   which Go will not allow across packages. Move it to a free function.

7. **A method reference** (`X::y`) records a `lambdaRec` but hoists no body:
   `addLambda` only builds the hoisted method for a `LambdaExpr`. A method
   reference should forward directly rather than through a `lambda$` wrapper.

8. **Local and anonymous class emission** is queued from `attr.Info.Local` and
   walks, but `EnclosingMethod` and `InnerClasses` are not written — `classfile`
   models both and `Builder` exposes neither.

---

## Open questions for `attr`

Three things this package guesses at, each of which `attr` already knows:

**Which overload phase a call resolved in.** `args` infers varargs-versus-array
from the argument count and the type of the trailing argument. That is wrong for
`f((Object[]) null)` against `f(null)` in a way no shape inspection can fix. A
`map[ast.Node]bool` on `Info` — this call was resolved in the varargs phase —
would settle it exactly.

**Where an anonymous class's symbol lives.** `Info.Local` is keyed by `ast.Decl`,
and a `NewExpr` carrying a body is not one. `newExpr` currently reads the class
out of `Types[NewExpr]` and the constructor out of `Uses[NewExpr]`. If `attr`
instead puts the `*sym.ClassSym` in `Uses`, that path inverts.

**Whether `Uses` keys a dotted `Name` whole or per part.** `name()` assumes the
whole `*ast.Name` resolves to one symbol. If the entry is per `Parts[i]`, field
chain access has to walk the parts instead.

---

## What this package deliberately does not do

- **Resolve, type or check anything.** Every type comes from `attr.Info`, every
  capture from `flow.Flow`, every legality question was `warn`'s. A name that did
  not resolve was reported three phases ago.
- **Optimise.** No constant propagation beyond what `attr` folded, no dead code
  elimination beyond dropping `flow.Unreachable`, no peephole. `javac` does not
  either, and the diff against it is the test. The optimising IR is
  [`ir`](../ir), below the waist, where d8 puts it too.
- **Manage the constant pool, patch branches, or compute the maxs.**
  `PoolBuilder` interns, `NewLabel`/`Mark` resolve, and `max_stack`/`max_locals`
  are computed as the body is emitted. That is `classfile`'s, and this package
  must not set them.
- **Write bytes.** `Lower` returns builders. Where they go is the caller's.
- **Know about SSA, dex, jars or APKs.** All four are below the waist or beside
  it.

---

## Relationship to the other packages

The only package in the toolchain that touches both sides of the waist, which is
what a code generator is. Above: [`attr`](../analyzer/attr) for types and
resolved symbols, [`flow`](../analyzer/flow) for captures and reachability,
[`types`](../types) for erasure, [`sym`](../sym) for the symbols themselves,
[`ast`](../ast) and [`token`](../token) for the tree and its spans. Below:
[`classfile`](../classfile) for the `Builder`, [`jvm/op`](../jvm/op) and
[`jvm/desc`](../jvm/desc) for opcodes and descriptors.

Nothing imports `lower`. [`ir`](../ir) consumes the class files it produced, and
[`target/dalvik`](../target/dalvik) consumes what `ir` makes of them; neither has
heard of this package.