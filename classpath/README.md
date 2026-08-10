# classpath

`package classpath` maps a binary class name to the bytes of its class file.

```
import "github.com/vertex-language/mocha/classpath"
```

```
go get github.com/vertex-language/mocha/classpath
```

Directories, jars, aars, multi-release jars. A [`Path`](#path) is an ordered search path; lookup is first-wins.

The package resolves nothing beyond a name. It does not decode a class file, does not read `this_class`, and does not import [`classfile`](../classfile) — the dependency runs the other way, so the same bytes serve `sym`'s completers, `target/dalvik`'s enumeration and `link`'s closed world without any of them agreeing on more than a name.

---

## Invariants

**Every `Class` owns its bytes.** A returned `Data` is a fresh allocation, never a window into a shared buffer. This is not a default; it is forced by the layer above. A `classfile.Class` aliases the bytes it was read from, so a shared buffer would make the lifetime of one decoded class depend on every other class read from the same jar — and a 50 MB `android.jar` would stay resident because one stub was touched. One copy per class, read once.

**Lookup is first-wins, and so is the duplicate rule.** The first entry on the path that defines a name provides it; later entries are shadowed. Within a single archive, at equal release, the first central directory record wins. A jar cannot use a duplicated entry name to shadow the copy a reader has already seen.

**A `Path` is safe for concurrent use once built.** Every index is immutable after `Add` returns, and archives are read through `ReaderAt`. This is what lets `sym` fire completers concurrently. `Add` and `Close` are not safe against a concurrent `Load`.

**Every failure names a container.** A miss is a `*NotFoundError`; a decode or I/O failure is an `*Error` carrying an [`Origin`](#origin). Nothing returns an anonymous error about an anonymous byte slice.

---

## Usage

```go
package main

import (
	"fmt"
	"log"

	"github.com/vertex-language/mocha/classpath"
)

func main() {
	p := classpath.New(classpath.Options{Release: 8})
	defer p.Close()

	if err := p.Add(classpath.Input, "build/classes"); err != nil {
		log.Fatal(err)
	}
	if err := p.Add(classpath.Classpath, "okhttp-4.12.0.jar"); err != nil {
		log.Fatal(err)
	}
	if err := p.Add(classpath.Lib, "/home/u/.mocha/platforms/android-24/android.jar"); err != nil {
		log.Fatal(err)
	}

	c, err := p.Load("okhttp3/OkHttpClient")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d bytes from %s\n", len(c.Data), c.Origin)

	// Everything that will be dexed.
	for _, e := range p.Entries(classpath.Input) {
		names, err := e.Names()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(e.Container(), len(names))
	}
}
```

Names are in internal form — `okhttp3/OkHttpClient`, `com/example/Foo$Inner` — never `com.example.Foo` and never with a `.class` suffix. `ValidBinaryName` rejects both, because JVMS §4.2.2 forbids `.` in an unqualified name and that check happens to catch the two mistakes people actually make.

---

## Roles

A jar's position on the command line decides what happens to it, and `Role` is that fact carried through.

| Role | Searched | Enumerated | For |
| --- | --- | --- | --- |
| `Input` | yes | **yes** | your code, and library code you want shipped |
| `Classpath` | yes | no | signatures only — resolution |
| `Lib` | yes | no | `android.jar`; ART supplies the implementation |

Every role is searched, in the order added. The role changes only what a caller iterates: `Entries(Input)` is what `target/dalvik` walks, and nothing else is eligible to reach the artifact. The `NoClassDefFoundError`-on-device failure this exists to prevent comes from a library that was resolved but never enumerated.

---

## `Path`

```go
func New(opts Options) *Path

func (p *Path) Add(role Role, name string) error
func (p *Path) AddEntry(role Role, e Entry)
func (p *Path) Load(binary string) (*Class, error)
func (p *Path) Has(binary string) bool
func (p *Path) Entries(role Role) []Entry
func (p *Path) Close() error
```

`Add` dispatches on what it finds: a directory becomes a directory entry, `.jar` and `.zip` become jars, `.aar` becomes an aar.

**A bare `.class` file is refused.** Its binary name is not derivable from its path — only from `this_class` inside the file — and reading it here would mean importing `classfile` and inverting the one dependency this package is on the clean side of. A driver that accepts loose class files reads them itself and registers them with [`Static`](#static). The error says so.

`Has` answers from the index without decompressing anything, which is what a "does this name exist" check in resolution wants.

---

## `Entry`

```go
type Entry interface {
	Kind() Kind
	Container() string
	Class(binary string) (*Class, error)
	Names() ([]string, error)
	Close() error
}
```

`Names` is **sorted**. Enumeration order decides constant pool and string table order downstream, and reproducible output is not something that can be bolted on after the fact — same inputs and flags, byte-identical artifact.

Jars and aars index at open time and answer `Names` from memory. A directory walks once and caches; a directory that changes underneath an open `Path` is out of scope, because a build reads its inputs once.

### `Origin`

```go
type Origin struct {
	Kind      Kind
	Container string // directory root, jar path, or aar path
	Nested    string // "classes.jar" or "libs/foo.jar", inside an aar
	Entry     string // path within the container
	Release   int    // versioned directory N, or 0 for a base entry
}
```

`String` renders each container the way it is conventionally written:

```
build/classes/com/example/Main.class
okhttp-4.12.0.jar!/okhttp3/OkHttpClient.class
material-1.11.0.aar!/classes.jar!/com/google/android/material/badge/BadgeDrawable.class
```

That string is what a `*SyntaxError` from `classfile` should be able to name. `classfile.Read` takes bytes and has nowhere to put a filename; `Origin` is where the filename went.

---

## Multi-release jars

Resolution follows the JAR File Specification, which is narrower than the folklore. Four rules, and three of them are exclusions:

- The jar must declare `Multi-Release: true` in the **main section** of `META-INF/MANIFEST.MF`. In a jar without it, `META-INF/versions/…` is an ordinary resource with a funny name.
- A versioned directory is `META-INF/versions/N`, where `N` matches `{1-9}{0-9}*`. No leading zero, no sign, no padding. `META-INF/versions/09/` is ignored.
- Any `N` below **9** is ignored, as is any `N` above the configured release.
- **Resources under `META-INF` cannot be versioned.** `META-INF/versions/9/META-INF/services/x` contributes nothing. This is a real path in real jars, not a hypothetical.

Higher `N` presides over lower, and both preside over the base entry.

### The `Multi-Release` comparison rule

The attribute name is case-insensitive and so is its value, but **surrounding whitespace is not tolerated**. `MULTI-RELEASE: TRUE` is true; `Multi-Release:  true` is not. The no-trim rule is deliberate — it is what lets a runtime make this decision without allocating — and it is implemented here exactly, not approximately.

Only the main section is parsed. It terminates at the first blank line, so a signed jar's hundreds of per-entry digest sections are never touched.

### `Options.Release`

```go
p := classpath.New(classpath.Options{Release: 8})
```

`DefaultRelease` is **8**, not the current release, and that is a real decision rather than an oversight.

A versioned entry at `N >= 9` exists precisely because it uses APIs or class file features from release `N`. mocha's encoder is capped at class file 49.0 — [see the 49.0 ceiling](../classfile#the-version-ceiling) — so selecting those entries by default would hand `gen` code the backend cannot reproduce. Eight means base entries only, which is the conservative answer for a toolchain in this shape.

The cost is that `--target jvm` silently takes the Java 8 code path of every MRJAR on the classpath. A caller that knows its target sets `Release` explicitly.

---

## Aars

An aar is a zip carrying `classes.jar`, a manifest fragment, and optionally resources. Class lookup searches `classes.jar` first, then `libs/*.jar` in sorted order.

```go
type Aar interface {
	Manifest() []byte     // AndroidManifest.xml, plain-text XML — not binary AXML
	HasResources() bool
}
```

**`HasResources` is the Tier 3 tripwire.** An aar with a populated `res/` needs resource compilation, ID allocation and an `R` class, none of which mocha does. This flag is what lets a build name the library it choked on instead of silently producing a broken APK. A bare `res/` directory record does not count; only a file beneath it does.

**`classes.jar` is buffered whole.** A nested archive cannot be read lazily through the outer one — deflate has no random access — so this is the single place the package holds a container in memory. Every other archive is read through the file.

---

## `Static`

```go
func NewStatic(name string, classes map[string][]byte) *Static
```

An in-memory entry, for the two callers that need one: loose `.class` inputs, whose binary names only `classfile` can determine, and tests that want a path with no filesystem. It is what keeps `Add`'s refusal of bare class files from being a dead end.

---

## Reading the archive

ZIP itself is not implemented here. `archive/zip` already does the central directory, ZIP64 and deflate, and `zip.NewReader` over an `*os.File` gives exactly the lazy random access this package wants — the central directory is indexed once at open, and no entry is decompressed until someone asks for it. Re-deriving the end-of-central-directory scan would buy nothing.

Two details are worth naming:

**A one-byte probe follows every full read.** `archive/zip` validates the CRC when the decompressor reaches EOF, so reading one byte past the declared length is what turns a corrupt entry into an error *here* rather than a confusing decode failure two packages up. It catches a lying central directory in the same motion.

**Entries are capped.** A single decompressed entry above 512 MiB is refused. `android.jar` is fifty megabytes whole; nothing legitimate is close.

---

## What this package deliberately does not do

- **Decode a class file.** That is [`classfile`](../classfile). This package would have to import it to read `this_class`, and the whole point of the split is that it does not.
- **Resolve coordinates.** Maven, Gradle and Coursier produce a list of paths; this package consumes one. See [Dependencies](../#dependencies).
- **Fetch anything.** `android.jar` arrives from [`sdk`](../sdk) as a path like any other.
- **Merge manifests.** An aar's fragment is handed over as bytes; union by identity, permission dedup and the contribution report are [`manifest`](../manifest)'s.
- **Compile resources.** `HasResources` reports the fact and stops.
- **Watch the filesystem.** A build reads its inputs once.

---

## Relationship to the other packages

[`classfile`](../classfile) decodes what this package produces, and knows nothing about it. [`sdk`](../sdk) produces the `android.jar` that gets added with role `Lib`. `sym` drives it through completers; [`target/dalvik`](../target/dalvik) and `link` enumerate `Input` entries.

`classpath` imports only the standard library.