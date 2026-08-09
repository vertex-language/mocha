# classfile

`package classfile` reads and writes the JVM `class` file format — chapter 4 of the Java Virtual Machine Specification.

```
import "github.com/vertex-language/mocha/classfile"
```

```
go get github.com/vertex-language/mocha/classfile
```

The format and nothing else. Bytes in, a `*Class` out; a `*Builder` in, bytes out. There is no jar handling, no classpath, no verification, no data flow.

---

## Invariants

**A `Class` aliases the bytes it was read from.** Constant pool strings, code arrays and raw attribute data all point into the input slice; nothing is copied. Reading a class keeps the whole file alive, which is the right trade for a compiler that reads a class once and lowers it immediately. Do not modify the input afterwards.

**Pool entries inflate lazily.** A `Utf8` entry is an offset and a length until someone asks for it, then the decoded string is memoised. `android.jar` is fifty megabytes of stubs whose UTF-8 entries are almost entirely untouched; decoding them all up front is the single largest waste available.

**Consumers get resolved references, never indices.** `Pool.Ref(i)` returns a `Ref{Class, Name, Descriptor}`. Nothing above this package should ever write `pool[pool[x].NameAndType].Name`, and nothing has to. The mirror holds on the writing side: `PoolBuilder` takes symbolic values and returns indices, so callers never manage the table.

**Unrecognised attributes survive as `*Raw`.** §4.7 obliges an implementation to silently ignore attributes it does not recognise, and forbids non-predefined attributes from affecting semantics. Retaining them verbatim is what makes byte-exact round-tripping possible; dropping them would be a correctness bug, not a simplification.

**Every error carries a byte offset.** The reader is bounded with a sticky error, so a decoder reads a whole structure and checks once. A malformed file yields a `*SyntaxError` naming the offset, not a panic.

```go
type SyntaxError struct {
	File string
	Off  int
	Msg  string
}
```

---

## Reading

```go
package main

import (
	"fmt"
	"log"

	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/jvm/op"
)

func main() {
	c, err := classfile.ReadFile("build/classes/com/example/Main.class", classfile.DefaultMode)
	if err != nil {
		log.Fatal(err)
	}

	m, ok := c.Method("main", "([Ljava/lang/String;)V")
	if !ok || m.Code == nil {
		log.Fatal("no main")
	}

	it := m.Code.Iter()
	for it.Next() {
		in := it.Instr()
		switch in.Op {
		case op.Invokestatic, op.Invokevirtual, op.Invokespecial, op.Invokeinterface:
			ref, err := c.Pool.MethodRef(in.Index())
			if err != nil {
				log.Fatal(err)
			}
			fmt.Printf("%4d  %-16s %s\n", in.PC, in.Op, ref)
		}
	}
	if err := it.Err(); err != nil {
		log.Fatal(err)
	}
}
```

Always check `it.Err()` after the loop. `Next` returning false means *stopped*, not *finished*.

### Modes

```go
c, err := classfile.ReadFile(entry, classfile.SkipCode|classfile.SkipDebug)
```

| Mode | Effect |
| --- | --- |
| `DefaultMode` | decode everything modelled |
| `SkipCode` | drop method bodies — most of the decode time on a stub jar |
| `SkipDebug` | drop `LineNumberTable`, `LocalVariableTable`, `LocalVariableTypeTable` |
| `KeepRaw` | retain each attribute's original bytes alongside the decoded form |
| `AllowPreview` | accept a class file depending on the newest release's preview features |

Reading `android.jar` to validate signatures wants `SkipCode|SkipDebug`; the stubs have no bodies worth having.

---

## Writing

```go
b := classfile.NewBuilder("com/example/Main")
b.SetSourceFile("Main.java")

b.Method(classfile.AccPublic, "<init>", "()V").Code(func(c *classfile.CodeWriter) {
	c.Aload(0)
	c.InvokeSpecial("java/lang/Object", "<init>", "()V")
	c.Return()
})

b.Method(classfile.AccPublic|classfile.AccStatic, "main", "([Ljava/lang/String;)V").
	Code(func(c *classfile.CodeWriter) {
		loop, end := c.NewLabel(), c.NewLabel()

		c.Iconst(0)
		c.Istore(1)

		c.Mark(loop)
		c.Iload(1)
		c.Iconst(3)
		c.IfICmpGe(end)

		c.GetStatic("java/lang/System", "out", "Ljava/io/PrintStream;")
		c.Iload(1)
		c.InvokeVirtual("java/io/PrintStream", "println", "(I)V")

		c.Iinc(1, 1)
		c.Goto(loop)

		c.Mark(end)
		c.Return()
	})

if err := b.WriteFile("out/com/example/Main.class"); err != nil {
	log.Fatal(err)
}
```

`max_stack` and `max_locals` are computed as the body is emitted. Do not set them.

**The body is a closure because the encoder may run it more than once.** No conditional branch has a long form: making `ifeq` reach further than 32767 bytes means inverting the test and jumping over a `goto_w`, which changes that instruction's length from three bytes to eight and moves everything after it. A single fixup pass cannot express that. Replaying the closure with the widening decisions from the previous pass can. Widening decisions are keyed on branch *ordinal*, which is stable across replays because the closure is deterministic.

### The version ceiling

`NewBuilder` targets **49.0**, and `SetVersion` rejects anything from 50.0 up.

From 50.0 the verifier expects a `StackMapTable`, and generating correct frames means implementing the type checker of §4.10.1 — the verification type lattice, frame merging, `uninitializedThis` tracking, and the compressed frame encoding on top. This package does not do that. Emitting 49.0 gets the class verified by type inference instead, which every current JVM still supports; version 50 would work today only via fail-over machinery deprecated since JDK 13. Refusing is better than emitting a class that loads with a `VerifyError`.

---

## Verifying the output

Three checks, in increasing strength. Only the last one is the verifier.

### 1. Does the JDK agree with the bytes?

`javap` parses the file with the JDK's own reader. If it prints, the structure is sound.

```
$ go run ./gen                                  # writes out/com/example/Main.class
$ javap -v -p out/com/example/Main.class
```

`-v` prints the constant pool, flags, attributes and disassembly; `-p` includes private members. `javap -c` alone gives just the code, which is what you want when eyeballing a body.

### 2. Does it match what `javac` would have produced?

Write the equivalent Java, compile it, and diff the disassembly.

```
$ javac -d ref Main.java
$ javap -c -p ref/com/example/Main.class > ref.txt
$ javap -c -p out/com/example/Main.class > got.txt
$ diff ref.txt got.txt
```

Use `-c`, not `-v`, for the diff. Constant pool ordering is not specified and mocha's interning order will not match `javac`'s, so a `-v` diff is drowned in renumbered `#N` references. The disassembly is what should agree.

### 3. Does the verifier accept it?

Loading the class runs the verifier. A successful run *is* the check — there is no separate verify command.

```
$ java -cp out com.example.Main
```

The class must sit at `out/com/example/Main.class` for that classpath to find `com.example.Main`; a file written to `Main.class` in the working directory will not be found under its package name.

To verify every class rather than only non-bootstrap ones:

```
$ java -Xverify:all -cp out com.example.Main
```

Do not reach for `-Xverify:none` or `-noverify` to make a stubborn class load. They are deprecated as of JDK 13 and removed outright in JDK 27 — and a class that only loads with verification off is a class mocha emitted wrongly.

A `VerifyError` naming a bytecode offset points at the `CodeWriter` call that emitted it; a `ClassFormatError` points at the encoder's structure. The two failures are worth telling apart before debugging.

### 4. Round trip

The check that needs no JDK at all, and the reason the encoder exists:

```go
func TestRoundTrip(t *testing.T) {
	out, err := buildSample().Bytes()
	if err != nil { t.Fatal(err) }

	c, err := classfile.Read(out, classfile.KeepRaw)
	if err != nil { t.Fatal(err) }        // the decoder must accept the encoder

	again, err := rebuild(c).Bytes()
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(out, again) {         // decode ∘ encode is the identity
		t.Errorf("round trip differs")
	}
}
```

Run all four. A round trip alone proves nothing about correctness: a symmetric bug in the reader and writer survives it perfectly. That is what the `javap` diff is for.

---

## Versions

Three separate rejection rules live in `Version.check`, and they are not one rule with cases (§4.1):

- `major` must fall in 45 through 70. A Java SE 25 VM accepts precisely 45–69; this package reads one further.
- For `major` ≥ 56, `minor` must be `0` or `65535`. Below 56 any minor is legal — JDK 1.1 really did ship 45.0 through 45.65535.
- `minor == 65535` means the file depends on that release's preview features. A preview file from an *older* release can never be read, by anyone, regardless of flags; only a VM of that exact release may load it.

```go
const (
	Java1_0 = 45
	Java5   = 49 // CONSTANT_Class becomes loadable; Signature, annotations
	Java6   = 50 // StackMapTable
	Java7   = 51 // MethodHandle, MethodType, InvokeDynamic, BootstrapMethods
	Java8   = 52 // type annotations, MethodParameters
	Java9   = 53 // Module, ModulePackages, ModuleMainClass
	Java11  = 55 // CONSTANT_Dynamic, NestHost, NestMembers
	Java16  = 60 // Record
	Java17  = 61 // PermittedSubclasses; last version honouring ACC_STRICT
	Java25  = 69
	Java26  = 70
)
```

Version gating is enforced, not assumed. A 45.3 file claiming a `CONSTANT_Dynamic` is rejected at the pool rather than read and misinterpreted, and `CONSTANT_Class` is refused as an `ldc` operand below 49.0 — it is the one tag that became loadable later than it was defined.

---

## The constant pool

`Pool` carries the bootstrap method table as well. They are one type because they refer to each other: `BootstrapMethods` is nominally an attribute, but `CONSTANT_Dynamic` and `CONSTANT_InvokeDynamic` index *into* it, so separating them makes attributes and the pool mutually dependent and Go will not compile it. The JDK's own Class-File API resolved this the same way.

Three shapes of the table bite, and all three are handled at `Pool.at`:

- **Index 0 is not a valid index.** It is meaningful as an absent value in several structures (`super_class`, `catch_type`, `outer_class_info_index`), and those callers go through `optClass`.
- **`Long` and `Double` occupy two slots.** The following index is valid but permanently unusable; it is stored with a zero tag so any attempt to resolve it is an error rather than a silent misread. The spec calls this a poor choice in retrospect. It is.
- **Tags 2, 13 and 14 are unassigned.** Seventeen kinds exist; the numbering is not contiguous.

Utf8 bytes are validated on the way in: no byte may be `0`, and none may fall in `0xF0`–`0xFF`, because the four-byte UTF-8 form does not exist here.

On the writing side, `PoolBuilder` interns floats and doubles **by bit pattern, not by value**. Two NaNs with different payloads are distinct constants that compare unequal, and `+0.0` and `-0.0` compare equal but are distinct constants; keying the intern map on the float would merge or duplicate the wrong ones.

---

## Attributes

Modelled as typed structs, hoisted onto `Class`, `Field`, `Method` and `Code` where a consumer will want them without a lookup:

`ConstantValue` · `Code` · `Exceptions` · `InnerClasses` · `EnclosingMethod` · `Synthetic` · `Signature` · `SourceFile` · `LineNumberTable` · `LocalVariableTable` · `LocalVariableTypeTable` · `Deprecated` · `BootstrapMethods` · `MethodParameters` · `NestHost` · `NestMembers` · `Record` · `PermittedSubclasses`

`StackMapTable` is retained as bytes. Nothing needs it to lower to IR, and generating it is the wall described above.

The annotation and module families decode to `*Raw`. The `element_value` and `target_info` unions are several hundred lines that no part of mocha reads. They are already listed in the location table, so adding a real decoder is a `case` arm rather than a redesign.

Each attribute body is decoded through a sub-reader bounded by its own `attribute_length`, so a length that lies cannot walk into the next attribute. An attribute defined at the wrong location, or introduced after this file's version, degrades to `*Raw` — it does not fail the parse.

---

## Instructions

`Instr` holds a slice of the code array rather than unpacked operands, so walking a method allocates nothing. Accessors decode on demand and account for a `wide` prefix:

```go
func (in Instr) Index() uint16      // constant pool index
func (in Instr) Local() uint16      // local slot, incl. the implicit iload_1 forms
func (in Instr) Increment() int32   // iinc delta
func (in Instr) Immediate() int32   // bipush, sipush
func (in Instr) Target() uint32     // absolute branch destination
func (in Instr) Switch() (uint32, []SwitchCase)
```

Two encodings mean an instruction's length is not a function of its opcode, which is why `op.Op.Len()` returns `-1` for three of them:

- **`wide`** widens the next instruction's local index to `u2`. Applied to `iinc` it also widens the delta, giving six bytes rather than four. Applied to anything that takes no local index it is a decode error.
- **`tableswitch` and `lookupswitch`** pad the byte after the opcode up to the next four-byte boundary *measured from the start of the code array* — not from the instruction. Their lengths come from their own operands, which are read before the length is known.

---

## Traps worth naming

**`Code_attribute` is `u2 max_stack`, `u2 max_locals`, `u4 code_length`.** The 1995 first edition used `u1/u1/u2`, and that version is still the top hit on several mirrors. Using it misparses every class file compiled since.

**`MethodParameters` counts with a `u1`**, alone among the table-shaped attributes.

**`ACC_STRICT` (0x0800) is only a flag in majors 46 through 60.** Outside that window the bit is unassigned and must be ignored, not reported. `Flags.Strict` takes a `Version` for exactly this reason.

**The same bit means different things by location.** `0x0020` is `ACC_SUPER` on a class and `ACC_SYNCHRONIZED` on a method; `0x0040` is `ACC_VOLATILE` on a field and `ACC_BRIDGE` on a method. A mask is only meaningful alongside the structure it came from.

---

## What this package deliberately does not do

- **Open jars or resolve a classpath.** `Read` takes bytes. Mapping `com/example/Foo` to bytes, handling multi-release jars and caching belong to `classpath`.
- **Verify.** No type checking, no stack map validation. The static constraints checked here are the ones needed to decode safely, not the ones needed to run safely. That is what `java -Xverify:all` is for.
- **Analyse.** The abstract interpretation that turns an operand stack into SSA is `ir/builder`'s job. The JDK's own Class-File API draws the same line, and it is the right one: analysis wants a graph, not a byte stream.
- **Transform.** There is no model-to-model rewrite path. mocha never rewrites a `.class`; dex comes from IR.

### Known gaps in the encoder

- **No `StackMapTable`**, so no target above 49.0.
- **No `invokedynamic` or `BootstrapMethods`.** No lambdas, no `StringConcatFactory`, no generated record members.
- **No `tableswitch` or `lookupswitch` emission.** The reader handles them; the writer does not. They interact badly with replay, because their four-byte padding depends on their own offset, so widening an earlier branch can change a switch's length and feed back into the fixpoint.
- **`dup2`, `dup2_x1`, `dup2_x2` and `pop2` assume one-slot operands.** Their real behaviour depends on whether the values beneath are category 1 or category 2, which depth-only tracking cannot see. Safe for generated code that does not mix the forms; a latent `VerifyError` otherwise.

---

## Relationship to the other packages

[`jvm/desc`](../jvm/desc) parses field and method descriptors, and [`jvm/mutf8`](../jvm/mutf8) implements the modified UTF-8 codec. Both are leaves, and both are shared with [`target/dalvik`](../target/dalvik) — dex reuses the JVM descriptor grammar verbatim and encodes its strings the same way. The byte reader is *not* shared: class files are big-endian, dex is little-endian with LEB128.

[`jvm/op`](../jvm/op) holds the opcode constants and operand shapes, kept separate so `ir/builder` can switch on opcodes without importing the format.

`classfile` imports `jvm/desc`, `jvm/mutf8`, `jvm/op`, and the standard library. [`ir`](../ir) consumes it; `classfile` knows nothing about IR.