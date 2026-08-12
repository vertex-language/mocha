# sym

`package sym` builds the symbol table: it turns declarations — yours and the ones inside jars on the class path — into named, scoped symbols that later phases resolve against.

```
import "github.com/vertex-language/mocha/sym"
```

```
go get github.com/vertex-language/mocha/sym
```

This is javac's `Enter`, split the way Go's package graph requires. `sym` answers what is declared, under what name, in what scope, and with what modifiers. It does not answer what anything's type is: a symbol keeps the raw material it arrived with — a descriptor string from a class file, an `ast.Type` from source — and `types` turns that into a type model. Symbol and Type are mutually referential in javac; here the dependency runs one way, so `sym` is the leaf.

---

## Invariants

**Two sources, one shape.** A `ClassSym` read from `okhttp-4.12.0.jar` and a `ClassSym` entered from `Fetch.java` are the same type with the same scope protocol. Nothing above this package should branch on where a symbol came from, which is what lets `attr` resolve `response.code()` without knowing that `Response` is binary and `Fetch` is not.

**Completion is lazy, entry is eager.** Every class on the path gets a stub the moment its name is mentioned; its members arrive only when something asks. Source classes are entered eagerly — all of them, before any member is completed — which is what makes a forward reference between two top-level types in the same unit resolve.

**Lifetime is source-shaped.** A symbol entered from source holds `ast.Node` pointers, and the parser's arena invalidates every node in a tree on `Release`. A source symbol is therefore valid only while its tree is: parse, enter, attribute, lower, then release. A symbol completed from a class file holds no tree and outlives everything.

**A symbol is not a type.** `MethodSym.Descriptor` is empty until something completes it, `VarSym.TypeExpr` is an unresolved `ast.Type`, and neither package tries to reconcile the two representations. That reconciliation needs erasure, and erasure needs types, so it happens above this package.

**A scope conflict is only a conflict within one declaration space.** Java has three (§6.5): types, variables and methods. A field, a method and a member class may all be called `run` in one class, and two methods sharing a name are ordinary — only two sharing a name *and* an erased signature are an error, which needs erasure and is `attr`'s to catch.

**A source declaration always wins over a class file of the same name.** You are compiling it, so whatever the path has under that binary name is stale by definition.

---

## Usage

```go
package main

import (
	"fmt"
	"log"

	"github.com/vertex-language/mocha/classpath"
	"github.com/vertex-language/mocha/parser"
	"github.com/vertex-language/mocha/sym"
)

func main() {
	cpath := classpath.New(classpath.Options{Release: 8})
	defer cpath.Close()
	if err := cpath.Add(classpath.Classpath, "okhttp-4.12.0.jar"); err != nil {
		log.Fatal(err)
	}

	t := sym.NewTable(cpath)

	file, diags := parser.ParseFile("Fetch.java", src)
	if len(diags) > 0 {
		log.Fatal(diags)
	}

	unit, diags := sym.Enter(t, file)
	if len(diags) > 0 {
		log.Fatal(diags)
	}

	for _, c := range unit.Types {
		if err := c.Complete(); err != nil {
			log.Fatal(err)
		}
		fmt.Println(sym.Dotted(c.Binary), c.Flags)
	}

	// A binary class completes the same way, on first use.
	client := t.Class("okhttp3/OkHttpClient")
	for _, m := range client.Methods("newCall") {
		fmt.Println(m.Name, m.Descriptor)
	}
}
```

---

## `Symbol`

```go
type Symbol interface {
	Base() *Sym
	symbolNode()
}

type Sym struct {
	Name  string
	Kind  Kind
	Flags Flags
	Owner Symbol
	Pos   token.Pos // NoPos for a symbol read from a class file
	End   token.Pos
	Unit  *token.File // nil if the symbol is binary
}
```

The unexported method closes the hierarchy: nothing outside this package can introduce a symbol kind. Five concrete types implement it — `PackageSym`, `ClassSym`, `MethodSym`, `VarSym`, `TypeParamSym` — plus `ErrorSym`, which a failed lookup returns instead of `nil` so one unresolvable name costs one diagnostic and not a cascade. `Sym.FromSource` (`Unit != nil`) is how a caller tells a source symbol from a binary one when it matters, which should be rare.

`Kind` is deliberately coarse: whether a `ClassSym` is an interface, an enum or a record is a `Flags` bit, exactly as it is a flag bit in a class file.

---

## `ClassSym` and completion

```go
func (c *ClassSym) Complete() error
func (c *ClassSym) Lookup(name string) []Symbol
func (c *ClassSym) Methods(name string) []*MethodSym
func (c *ClassSym) Field(name string) *VarSym
func (c *ClassSym) Nested(name string) *ClassSym
```

`Complete` runs a class's `Completer` at most once and is safe for concurrent use; a second caller either gets the first call's cached error or, if it arrives while completion is in flight, `ErrCyclicCompletion`. That error is real, not defensive: a class file naming itself as its own superclass produces one, and so does a source hierarchy with a cycle. `Lookup`, `Methods`, `Field` and `Nested` all complete the class first — inherited members are never searched here, because which supertype member is visible from where is a resolution rule, and resolution belongs to `attr`.

Two completers exist, and nothing above this package can tell which one ran:

- **`sourceCompleter`** (`source.go`) enters a class's members from its `ast.Decl` on first `Complete` — after every type in the unit has already been entered by `Enter`, so a member's type may name any of them, in any order. It also expands what the language declares implicitly: an interface field is `public static final` whether written or not (§9.3), an interface method with no body is `public abstract` (§9.4), and a record's components produce a private final field, a public accessor, and — for a compact constructor — an implicit flag standing in for a parameter list `types` fills in later.
- **`binaryCompleter`** (`binary.go`) reads a class file through `Table.load`, using `SkipCode|SkipDebug` since a symbol table wants signatures and bodies are the expensive part. It refuses a class that declares itself under a different name than the one it was looked up by, and refuses a module descriptor outright — neither is a type this package can stand behind.

---

## `Scope`

```go
func NewScope(owner Symbol, parent *Scope) *Scope
func (s *Scope) Enter(n Symbol) Symbol            // returns the conflicting symbol, or nil
func (s *Scope) Lookup(name string) []Symbol      // this scope only
func (s *Scope) Resolve(name string) (*Scope, []Symbol) // walks outward
func (s *Scope) ResolveKind(name string, k Kind) Symbol
func (s *Scope) Each(f func(Symbol) bool)         // declaration order
func (s *Scope) All() []Symbol
```

| Namespace | Kinds |
| --- | --- |
| Types | `KindClass`, `KindTypeParam`, `KindPackage` |
| Methods | `KindMethod` |
| Variables | everything else (`KindVar`) |

`Enter` checks conflicts only within a symbol's own namespace, and never treats two methods sharing a name as a conflict — that's an overload until erasure says otherwise, and erasure is `attr`'s call. An unnamed variable (`_`) is appended to declaration order but never indexed, so it neither shadows anything nor collides with anything. `Resolve` walks outward through `Parent` scopes; shadowing falls out of the order, since the innermost declaration is found first. `ResolveKind` is `Resolve` restricted to one namespace, which is what stops a local named `x` from hiding a method named `x`.

---

## Flags

```go
type Flags uint32

const AccessFlags = FlagPublic | FlagPrivate | FlagProtected
func (f Flags) Has(mask Flags) bool
func (f Flags) HasAny(mask Flags) bool
func (f Flags) String() string // canonical §JLS order
```

`Flags` is deliberately not `classfile.Flags`: the same bit means different things at different locations there (`0x0020` is `ACC_SUPER` on a class and `ACC_SYNCHRONIZED` on a method), and three source modifiers — `sealed`, `non-sealed`, `default` — have no class-file bit at all. Mapping runs explicitly in both directions: `modifierFlags`/`annotationFlags` read an `ast.Modifiers` list, and `classFileClassFlags`/`classFileFieldFlags`/`classFileMethodFlags` read a `classfile.Flags`, each picking only the bits that mean something to resolution — `ACC_SUPER` is dropped entirely, for instance, since every JVM since 8 treats it as set regardless.

`FlagImplicit` marks a member the language declares on your behalf — a record's accessors and canonical constructor, an enum's `values`/`valueOf`. They are real members resolution must find, but no declaration in the source produced them, which is why `Scope.Enter` returning a conflict for an implicit accessor colliding with an explicit one is not treated as an error.

---

## Names

```go
func Internal(dotted string) string           // "." -> "/"
func Dotted(internal string) string           // "/" and "$" -> "."
func TopLevelBinary(pkg, simple string) string
func NestedBinary(outer, simple string) string       // outer + "$" + simple
func AnonymousBinary(outer string, n int) string     // outer + "$" + n
func LocalBinary(outer string, n int, simple string) string
func SimpleName(binary string) string
func PackageOf(binary string) string
```

Names inside this package are internal form — `com/example/Foo$Inner` — because that's what `classpath`, `classfile` and `target/dalvik` all agree on. Dotted form exists only for what a user reads and writes, and the conversion is lossy in one direction: `Dotted` turns both `/` and `$` into `.`, so `com/example/A$B` reads as `com.example.A.B`, which is how a user wrote it and never how the JVM did.

---

## `Table`

```go
func NewTable(p *classpath.Path) *Table
func (t *Table) Package(dotted string) *PackageSym
func (t *Table) Class(binary string) *ClassSym   // nil if the path has no such name
func (t *Table) Existing(binary string) (*ClassSym, bool)
func (t *Table) Declare(c *ClassSym) *ClassSym   // registers a source class
func (t *Table) Members(pkgInternal string) []string
```

`Table` owns every `ClassSym` and `PackageSym` in play, whether entered from source or completed from the path, and is safe for concurrent use — `classpath.Path` is immutable once built, which is what lets completers run in parallel. `Class` returns a stub immediately and attaches a `binaryCompleter`; the stub's members arrive on first `Complete`. `Declare` is source's entry point: it returns the class already declared under a binary name only if that class came from source, since a class-file entry of the same name is expected to lose. `Package` makes a package's existence unconditional (§7.4.3 makes packages observable, not declared), so a lookup of any dotted name always succeeds.

A handful of binary names attribution needs without going through resolution — `java/lang/Object`, `java/lang/String`, `java/lang/Record`, `java/io/Serializable`, and others — are exposed as constants plus a couple of convenience methods (`Table.Object`, `Table.JavaLang`). Nothing fails if the path lacks them; a unit that never touches records or try-with-resources compiles fine against a path that has neither.

---

## `Unit`

```go
func Enter(t *Table, file *ast.File) (*Unit, []token.Diagnostic)
func (u *Unit) FindType(simple string) *ClassSym
func (u *Unit) FindStatic(member string) []*ClassSym
```

`Enter` is the first semantic phase: it walks one compilation unit, creates a symbol for every type it declares — recursively, for member types — and registers each with the `Table`, all before completing any of them. Bodies are not walked; a local or anonymous class is entered later, during attribution, once the enclosing method's scope exists to hold it (`ClassSym.NextAnonymous` numbers them per §13.1 when that happens). A module declaration short-circuits `Enter` entirely: it declares no types, and module resolution is not modelled.

`FindType` implements §6.5.5's order and nothing else: a single-type import, then a type of this unit's own package, then an on-demand import (ambiguous if two of them supply the name, which resolves to `nil` for the caller to report), then `java.lang`. Types declared in an enclosing class or method are deliberately not searched here — those shadow everything below and belong to `attr`'s scope chain, not the unit's.

---

## What this package deliberately does not do

- **Resolve a name to a type.** A field's `TypeExpr` and a binary method's `Descriptor` are raw material; turning either into a type model is [`types`](../types)'s job.
- **Detect overload conflicts.** Two methods sharing a name and an erased signature are an error, but detecting that needs erasure, which needs types. `Scope.Enter` lets same-named methods coexist and leaves the check to `attr`.
- **Model module resolution.** A module declaration is recorded on `Unit.Module` and its directives are left untouched.
- **Enter local or anonymous classes.** They can't be named from outside their enclosing method body, so they're entered during attribution, not here.
- **Decode or verify class files.** That's [`classfile`](../classfile), already done by the time `binaryCompleter` sees a `*classfile.Class`.
- **Map a name to bytes.** That's [`classpath`](../classpath); `Table.load` only calls into it.

---

## Relationship to the other packages

[`classfile`](../classfile) decodes the class files `binaryCompleter` reads, and [`classpath`](../classpath) maps a binary name to those bytes; `sym` imports both but neither imports back. [`ast`](../ast) and [`token`](../token) carry the source side — `Sym.Pos`/`End`/`Unit` resolve through `token.File`, and `sourceCompleter` reads `ast.Decl` nodes directly. `types` consumes `sym`'s output and knows nothing about where a symbol came from; `attr` drives both `Scope` resolution and the overload and cycle checks this package deliberately leaves undone.