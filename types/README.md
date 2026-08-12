# types

`package types` turns what [`sym`](../sym) carries — a descriptor string, an
`ast.Type`, an unparsed `Signature` — into a resolved type model: generics,
erasure, and the supertype graph.

```
import "github.com/vertex-language/mocha/types"
```

```
go get github.com/vertex-language/mocha/types
```

`sym` answers what is declared and under what name. This package answers what it
*is*. The split is javac's `Enter` / `TypeEnter`, made a package boundary because
Go's graph requires one: `sym` is the leaf, `types` sits directly on it, and
`analyzer/attr` consumes both.

---

## Invariants

**Erasure lives in the descriptor; generics live in the signature.** `classfile`
retains `Signature` as an opaque string on purpose. This package holds the only
JVMS §4.7.9.1 parser in the toolchain, and nothing else ever needs one.

**A `Type` is closed.** An unexported marker method keeps anything outside this
package from introducing a kind, exactly as `ast`'s five hierarchies do.
Consumers switch on `Kind()`.

**Primitives, `void` and `null` are singletons**, comparable with `==`. A
`ClassType` is not: two uses of `List<String>` are distinct values describing one
type, and the comparison is `Identical`.

**A raw type is a `ClassType` with no arguments**, not a separate kind. §4.8
makes raw types a degenerate parameterization, and a distinct `Kind` would force
every switch to handle two cases that behave identically everywhere but display.

**Every unresolved name yields an `ErrorType`, never `nil`** — `sym.ErrorSym`'s
rule one layer up. One bad import costs one diagnostic, not a nil dereference
three phases later.

**Type parameters are published before their bounds are resolved.**
`enum Enum<E extends Enum<E>>` is the standard idiom, not a pathology. Shells
first, bounds second, so F-bounded declarations need no cycle detection at all.

---

## Usage

```go
st := sym.NewTable(cp)
tt := types.NewTable(st)

unit, diags := sym.Enter(st, file)
tt.Register(unit)          // so source types resolve against its imports

mt := tt.MethodType(m)     // *MethodType, and fills m.Descriptor
fmt.Println(types.MethodDescriptor(mt))   // (Lokhttp3/Request;)Lokhttp3/Call;
```

`MethodType` and `FieldType` fill `sym.MethodSym.Descriptor` and
`sym.VarSym.Descriptor` for source symbols as a side effect. That is not
incidental plumbing — `sym`'s own doc defers it here — and it is what lets `gen`
treat a source method and a binary method identically.

---

## The model

| Kind | Type | Notes |
| --- | --- | --- |
| `KindBoolean` … `KindDouble` | `*Basic` | eight singletons, §4.2 |
| `KindVoid`, `KindNull` | `*Basic` | `Void` only as a result; `Null` only from the literal |
| `KindClass` | `*ClassType` | `Args` nil when raw; `Outer` for `Map<K,V>.Entry` |
| `KindArray` | `*ArrayType` | nested one dimension at a time |
| `KindTypeVar` | `*TypeVar` | `Bound` is never nil after completion |
| `KindWildcard` | `*Wildcard` | only ever inside `ClassType.Args` |
| `KindIntersection` | `*Intersection` | a multi-bound parameter or cast target |
| `KindError` | `*ErrorType` | carries the name that was sought |

`ArrayType` re-nests what `ast.ArrayType` flattened into `Dims`, because
subtyping and erasure both recurse one layer per step (§10.8 covariance).

---

## `Table`

```go
func NewTable(st *sym.Table) *Table

func (t *Table) Register(u *sym.Unit)

func (t *Table) TypeParams(c *sym.ClassSym) []*TypeVar
func (t *Table) Supertype(c *sym.ClassSym) Type
func (t *Table) Interfaces(c *sym.ClassSym) []Type

func (t *Table) MethodType(m *sym.MethodSym) *MethodType
func (t *Table) FieldType(v *sym.VarSym) Type

func (t *Table) IsSubtype(sub, sup Type) bool
```

`Register` is required for source classes and harmless for anything else: a
`sym.ClassSym` does not carry the `*sym.Unit` that entered it, and resolving a
simple name in a declaration needs that unit's imports. Nested classes inherit
their top-level ancestor's registration.

Completion is per-class, cached, and safe for concurrent use. It never recurses
into the same class: bounds resolve against an explicit environment rather than
by re-entering the table, and building `ClassType{Sym: B}` never completes `B`.
The cycle guard lives in the supertype *walk* (`IsSubtype`, `Super`), where an
`A extends B extends A` hierarchy would otherwise loop.

Two sources converge on one model. Source classes read `Extends`/`Implements`
off the `ast.Decl` (or take the implicit `java/lang/Record`, `java/lang/Enum<E>`).
Binary classes parse the class `Signature`, and — absent one, which is the
common case — fall back to the plain internal names `sym.ClassSym.Super` and
`.Interfaces` already hold, wrapped raw.

### One prerequisite on `sym`

`sym.binaryCompleter` reads every hoisted attribute except `Signature`, and
`sym.Table.load` is unexported, so this package cannot fetch it independently.
Three fields are needed, populated where `binary.go` already reads the others:

```go
ClassSym.Signature  string   // generic class signature, "" if absent
MethodSym.Signature string
VarSym.Signature    string   // fields only
```

Until they land, every non-generic class — most of `android.jar` — is exact, and
generic binary classes silently erase, as though no `Signature` had been read.

---

## Signatures

The grammar is parsed in two phases: a `sigType` tree with names still as
strings, then resolution against `sym.Table`. Parsing never touches a symbol
table; resolution never re-walks bytes.

Four points worth having written down once:

- `TypeArgument` admits `*` as well as `+`/`-`. `List<?>` and
  `List<? extends Foo>` differ by one byte and no lookahead.
- `ClassTypeSignatureSuffix` is how a member type is spelled —
  `Louter/Outer<...>.Inner;` — and each suffix carries its own arguments. This
  is where `ClassType.Outer` comes from on the binary side.
- A `ThrowsSignature` may name a type variable. `<E extends Exception> void m()
  throws E` is legal.
- An empty `ClassBound` (`<T::Comparable<T>>`) means the interface bound is the
  only bound — not `Object` intersected with it.

A signature that fails to parse is treated as absent: generics erase, nothing
becomes unusable. `attr` never sees a signature syntax error. This is the same
trade `classfile` already makes for an attribute at the wrong location.

---

## Erasure

```go
func Erase(t Type) Type
func Descriptor(t Type) desc.Type
func MethodDescriptor(mt *MethodType) string
```

§4.6: a `ClassType` drops `Args`, a `TypeVar` erases to its bound, an
`ArrayType` erases its element. `Descriptor` bridges into `jvm/desc` — the same
`desc.Type` shape `classfile.Builder` validates against, so an erased type goes
into a `MethodBuilder` unchanged.

---

## Subtyping

`IsSubtype` is nominal, plus array covariance, plus primitive widening
(`Widens`, §5.1.2). Two `ClassType`s over the same `Sym` compare as if raw.

Generic containment and inference are deliberately absent. They need a
constraint solver over `Args`, and that solver belongs next to overload
resolution and target typing — in `attr`, which this package cannot see.

---

## What this package deliberately does not do

- **Resolve names inside a method body.** Locals, casts and expression types are
  `attr`'s scope chain.
- **Fold constant expressions.** `Constant` is the shared shape; producing one
  from source is `attr`'s, as `sym` already says.
- **Decode a plain descriptor.** `jvm/desc` does that.
- **Model verification types.** Erasure is the endpoint.

---

## Relationship to the other packages

[`sym`](../sym) is the leaf this sits on; every `Type` is anchored to a symbol
from it. [`ast`](../ast) supplies the nodes a source declaration is walked from,
[`jvm/desc`](../jvm/desc) the plain descriptor grammar, [`classfile`](../classfile)
the `Signature` and `Const` bytes this package is the sole consumer of.
`analyzer/attr` consumes the output and owns everything the last section
declines.