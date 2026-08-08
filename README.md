# ☕ Mocha

**A VM and toolchain for Android and the JVM, written in pure Go.**

Mocha builds, verifies, and runs code for the Android and JVM platforms. The whole
toolchain is one static Go binary — no JDK, no Gradle, no Android SDK.

It comes in three pieces, and you can take any one of them on its own:

- **`mocha`, the CLI** — Java source and Mocha IR in, DEX or JVM bytecode out.
  Build, check, verify, run.
- **The compiler framework** — the same phases as Go packages. Bring a language
  frontend, emit Mocha IR, and Mocha handles type checking, register allocation,
  verification, and lowering to either target.
- **The IR** — platform-agnostic, statically typed, register-based. The seam
  everything else is built on, and the interface most consumers should target.

Think LLVM, scoped to the Android ecosystem.

> **Status: early development.** The IR and package layout are taking shape; the
> Java frontend and the DEX/JVM emitters are in progress. See [Roadmap](#roadmap)
> before depending on this.

---

## Why this exists

Mocha is the Android and JVM backend for the [Vertex language](https://github.com/vertex-language).
Vertex is pure Go, and pulling the Java ecosystem into the main compiler would have
meant dragging its toolchain, object model, and platform assumptions along with it.
So that work lives here instead, behind an IR boundary, with its own release cycle.

That boundary is the useful part for everyone else. Mocha does not know or care that
Vertex exists — it consumes IR. Something Kotlin-shaped, a DSL that has to run on
Android, a framework reaching the platform from a custom language: you write a
frontend and stop there. The backend is already done.

---

## Quick start

```bash
GOPROXY=direct go install github.com/vertex-language/mocha/cmd/mocha@latest
```

Or grab a prebuilt binary from [Releases](https://github.com/vertex-language/mocha/releases).
Single static binary, no runtime dependencies.

```bash
# JVM class files — fastest loop while iterating
mocha build src/ --target jvm -o build/classes

# Build and run it
mocha run src/main.java -- --port 8080

# Android DEX
mocha build src/ --target android --android-api 24 -o build/

# Type-check only, no artifacts
mocha check src/
```

`.java` and `.mir` inputs mix freely in one invocation. Output is reproducible by
default. Full command reference under [CLI](#cli).

---

## Using Mocha from Go

The CLI is a thin shell over the framework. If you're building a language, skip it —
import `ir` and `lower`, construct modules, ship your own binary.

```go
package main

import (
	"log"
	"os"

	"github.com/vertex-language/mocha/ir"
	"github.com/vertex-language/mocha/lower"
)

func main() {
	// A module corresponds to one class file.
	m := ir.NewModule("com/example/Hello")

	// public static void main(String[] args)
	main := m.NewMethod("main", ir.Void, ir.Array(ir.String))
	main.SetFlags(ir.Public | ir.Static)

	b := main.Body
	r := b.Regs

	// Virtual registers — ir.Int32 maps to the JVM 'int'.
	a := r.New(ir.Int32)
	c := r.New(ir.Int32)
	sum := r.New(ir.Int32)

	b.Const(ir.Int32, a, 10)
	b.Const(ir.Int32, c, 32)
	b.Add(ir.Int32, sum, a, c)
	b.Ret()

	bytecode, err := lower.ToJVM(m)
	if err != nil {
		log.Fatalf("lowering failed: %v", err)
	}

	if err := os.WriteFile("Hello.class", bytecode, 0644); err != nil {
		log.Fatalf("write failed: %v", err)
	}
}
```

Swap `lower.ToJVM` for `lower.ToDEX` to target Android instead. The IR is identical;
virtual registers, types, and control flow do not change between targets.

**What Mocha handles:** register allocation, constant pool construction, verification,
target-specific lowering.

**What your frontend owns:** lexing, parsing, name resolution, your own semantics.
Mocha type-checks the IR you hand it, but it will not infer your language's rules.

Frontends depend on `ir` and `lower` and nothing above them — the dependency graph
runs one direction only, so importing the IR does not drag in the Java parser.

Modules also serialize to a textual form (`.mir`), which the CLI accepts as input.
That's a second option: emit IR to a file and let `mocha` handle lowering,
verification, caching, and diagnostics without linking against anything.

See [`docs/ir.md`](docs/ir.md) for the instruction set and the textual grammar.

### From IR to an installable APK

`lower.ToDEX` gets you a DEX payload. `bundle` takes it the rest of the way — a
frontend that can emit IR can produce an installable artifact without touching
`aapt2`, `zipalign`, `apksigner`, or the Android SDK at all.

```go
package main

import (
	"log"
	"os"

	"github.com/vertex-language/mocha/bundle"
	"github.com/vertex-language/mocha/ir"
	"github.com/vertex-language/mocha/lower"
)

func main() {
	m := ir.NewModule("com/example/hello/MainActivity")
	// ... build methods, as above ...

	dex, err := lower.ToDEX(m)
	if err != nil {
		log.Fatalf("lowering failed: %v", err)
	}

	apk, err := bundle.APK(bundle.Options{
		Package: "com.example.hello",
		Label:   "Hello",
		MinAPI:  24,
		Dex:     [][]byte{dex},
		Res:     "res/",             // optional; compiled and indexed in-process
		Sign:    bundle.DebugKey(),  // or bundle.Keystore(path, alias, pass)
	})
	if err != nil {
		log.Fatalf("bundling failed: %v", err)
	}

	if err := os.WriteFile("hello.apk", apk, 0644); err != nil {
		log.Fatalf("write failed: %v", err)
	}
}
```

`bundle` generates the manifest from `Options` when you don't supply one, compiles
and indexes resources, assembles and aligns the archive, and signs it. Pass
`Manifest:` a path and it uses yours instead, merging only what it must.

Multiple modules lower independently and go into `Dex` together; `bundle` handles
multidex once the method count crosses the limit rather than making that your
problem.

The same path is one flag from the CLI:

```bash
mocha build src/ --target android --android-api 24 --emit apk -o build/
```

Reproducible like everything else — same inputs, byte-identical APK, signature
aside.

> **Status.** Not implemented yet. Resource handling, manifest generation, and
> packaging are on the [Roadmap](#roadmap); `lower.ToDEX` is the furthest the
> pipeline currently goes. The API above is the shape being built toward, not
> something you can call today.

## Architecture

Each phase is a separate package with no upward dependencies, so you can enter the
pipeline wherever makes sense. A frontend typically ignores everything above `ir`.

| Package | Role | Used by |
| --- | --- | --- |
| `token` | Lexical token definitions | Java frontend |
| `scanner` | Source text → tokens | Java frontend |
| `ast` | Abstract syntax tree | Java frontend |
| `parser` | Tokens → AST (grammar and syntax) | Java frontend |
| `analyzer` | Semantic analysis and type checking over the AST | Java frontend |
| **`ir`** | **Platform-agnostic, statically typed program representation** | **All frontends** |
| **`lower`** | **IR → target bytecode (DEX, JVM)** | **All frontends** |
| `bundle` | DEX + resources → signed APK | CLI, Android frontends |
| `verify` | Bytecode verification, pre- and post-lowering | `lower`, CLI |

All under `github.com/vertex-language/mocha/`.

---

# CLI

`mocha` is a thin shell over the framework with the Java frontend attached. It is a
closed loop: Java source and Mocha IR in, DEX or JVM bytecode out. Nothing is
CLI-only — every command composes the same passes a Go consumer would call directly.

## Commands

| Command | Does |
| --- | --- |
| `mocha build` | Compile sources to bytecode |
| `mocha check` | Run the full analysis pass, emit nothing |
| `mocha verify` | Verify existing bytecode against a target's rules |
| `mocha run` | Build and execute (JVM target only) |
| `mocha targets` | List available targets and their constraints |
| `mocha version` | Version, commit, supported DEX and class-file versions |

---

### `mocha build`

```
mocha build [flags] <path>...
```

Accepts `.java` source and `.mir` textual IR, mixed freely in one invocation. Paths
may be files or directories; directories are walked for both extensions. `-` reads a
single unit from stdin, with `--lang` to disambiguate.

```bash
# Android DEX
mocha build src/ --target android --android-api 24 -o build/

# JVM class files, for local iteration
mocha build src/ --target jvm -o build/classes

# IR emitted by something else, lowered here
mocha build module.mir --target android

# Inspect IR instead of emitting bytecode
mocha build src/main.java --emit ir
```

**Flags**

| Flag | Default | Meaning |
| --- | --- | --- |
| `-t, --target` | `jvm` | `android` or `jvm` |
| `-o, --out` | `./build` | Output directory; `-` for stdout |
| `--emit` | target default | Comma-separated: `dex`, `class`, `apk`, `ir`, `ast`, `tokens` |
| `--android-api` | `21` | Minimum API level; selects DEX format version |
| `--classpath` | — | Jars or class dirs for type resolution |
| `--source-path` | input roots | Where to resolve unlisted sources from |
| `--lang` | by extension | `java` or `ir`; only needed for stdin |
| `-O` | `1` | Optimization level: `0`, `1`, `2` |
| `--debug-info` | `true` | Emit line tables and local variable names |
| `--verify` | `on` | `off`, `on`, `strict` — see [Verification](#verification) |
| `-j, --jobs` | `NumCPU` | Parallel compilation units |
| `--cache-dir` | `$MOCHA_CACHE` | Content-addressed build cache |
| `--no-cache` | `false` | Bypass the cache for this invocation |

Output is reproducible by default: no timestamps, no absolute paths in metadata,
stable ordering in constant pools and string tables. The same inputs and flags
produce byte-identical artifacts across machines.

The `.mir` input path is the seam between the CLI and everything else. A frontend
that writes IR to a file gets lowering, verification, caching, and diagnostics for
free without Mocha knowing anything about the language it came from. `--emit ir` is
the same seam in reverse, and the intended way to debug the Java frontend — check
what it produced before blaming the emitter.

> **Note.** The textual IR format is not yet stable. Treat `.mir` files as valid only
> for the binary version that produced them until this lands in a tagged release.

---

### `mocha check`

```
mocha check [flags] <path>...
```

Parses, resolves, and type-checks without emitting artifacts. Accepts the same input
and diagnostic flags as `build`. Use it in editors and pre-commit hooks; it is the
fastest path to a complete diagnostic set.

```bash
mocha check src/ --classpath libs/android.jar --diagnostics json
```

On `.mir` input this validates the IR itself — type consistency, register liveness,
well-formed control flow — which is what you want in a frontend's own test suite.

`--target` still matters here. Platform APIs and API-level availability are part of
the check.

---

### `mocha verify`

```
mocha verify [flags] <artifact>...
```

Verifies `.dex`, `.class`, and `.jar` files structurally and by dataflow, independent
of how they were produced. Mocha's own output goes through this on every build; the
command exposes it for artifacts from other toolchains, or as a CI gate on a release
directory.

```bash
mocha verify build/classes.dex --android-api 24
mocha verify build/classes/ --strict
```

| Flag | Meaning |
| --- | --- |
| `--android-api` | Verify against a specific API level's rules |
| `--strict` | Fail on constructs that are legal but that ART or HotSpot reject in practice |
| `--summary` | Counts and sizes per class, no per-instruction detail |

---

### `mocha run`

```
mocha run [flags] <path>... [-- args...]
```

Builds to a temporary directory and executes. Requires `--target jvm` and a JVM on
`PATH`; the Android target has no execution story yet.

```bash
mocha run src/main.java -- --port 8080
mocha run module.mir --main com.example.Hello
```

Arguments after `--` go to the program, not to Mocha. `--main` selects an entry point
when more than one class declares `main`.

---

### `mocha targets`

```
mocha targets [--target NAME] [--json]
```

Lists targets with their constraints — DEX format versions per API level, class file
versions, register limits, supported instruction coverage. Without arguments it
prints a summary; with `--target` it prints everything known about one. `--json` for
tooling.

This is the honest answer to "will my IR lower cleanly," available without running a
build.

---

### `mocha version`

```
mocha version [--json]
```

Version, commit, build date, Go version, and the ranges of DEX format and class file
versions this binary can emit and verify.

---

## Verification

`check` and `verify` are separate passes on purpose, and both run inside `build`:

- **`check`** catches what the *source language or IR* forbids — types, name
  resolution, definite assignment, flow, register consistency.
- **`verify`** catches what the *target* forbids — register width, type confusion
  across branches, stack depth, constant pool integrity, method and field reference
  resolution.

Well-typed IR can still lower to bytecode the platform rejects, so the second pass
catches things the first cannot see by construction. `--verify strict` additionally
rejects bytecode that verifies formally but that ART's verifier or HotSpot's have
historically been unhappy with.

`--verify off` exists for bisecting emitter bugs. It is not a build-speed knob;
verification is not where the time goes.

---

## Diagnostics

Every diagnostic carries a file, a byte range, a severity, and a stable code.
Ordering is deterministic — sorted by position, not by the order passes happened to
run.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--diagnostics` | `text` | `text`, `short`, `json` |
| `--max-errors` | `100` | Stop after N errors; `0` for unlimited |
| `--warnings-as-errors` | `false` | Promote all warnings |
| `--allow` / `--deny` | — | Adjust severity of a specific code |
| `--color` | `auto` | `auto`, `always`, `never` |
| `-q, --quiet` | `false` | Errors only |
| `-v, --verbose` | `false` | Pass timings and cache decisions to stderr |

`--diagnostics json` emits one object per line on stderr, suitable for piping into an
editor or a CI annotator. Codes are stable across releases; a code is never reused
for a different meaning.

Diagnostics on `.mir` input point at the IR, with source positions preserved if the
producer recorded them.

**Exit codes**

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Compilation or verification failed with diagnostics |
| `2` | Usage error — bad flags, missing input |
| `3` | Internal error — a compiler bug, please report it |

---

## Configuration

Mocha needs no configuration file. `mocha.toml` in the working directory or any
parent is optional, and exists only so repeated invocations don't need repeated
flags. Flags always win over the file.

```toml
[build]
target = "android"
android-api = 24
out = "build"
classpath = ["libs/android.jar"]

[diagnostics]
warnings-as-errors = true
```

There is no plugin system, no task graph, and no lifecycle. If you need those, drive
`mocha` from whatever you already use.

---

## Environment

| Variable | Meaning |
| --- | --- |
| `MOCHA_CACHE` | Build cache location; defaults to the OS cache dir |
| `MOCHA_TARGET` | Default target when unset by flag or config |
| `NO_COLOR` | Honored; equivalent to `--color never` |

---

## Testing

Every emitter runs against a differential suite: the same IR lowered to both targets,
executed, and compared. Bytecode that verifies but misbehaves is the failure mode
that matters, so the suite runs real programs rather than checking byte sequences.

`mocha check` on `.mir` input is the intended entry point for a frontend's own tests
— assert your IR is well-formed without depending on a working emitter.

## Roadmap

- [ ] Java → Mocha IR frontend
- [ ] Mocha IR → JVM emitter
- [ ] Mocha IR → DEX emitter
- [ ] Textual IR parser and printer
- [ ] Resource compilation and manifest generation
- [ ] APK assembly and signing
- [ ] Differential test harness across targets

### Exploring

- **Execution without a JVM.** An interpreter over Mocha IR would make `mocha run`
  work with no runtime installed — useful for testing and CI. Whether it's worth the
  maintenance cost, and how far it gets before the missing class library becomes the
  blocker, is an open question. Not committed to.

## Contributing

Contributions are very welcome, particularly on the emitter tracks above. Open an
issue to discuss direction before starting anything large.

## License

MIT