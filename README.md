# ☕ Mocha

**A Java toolchain for Android, the JVM, and native binaries — one static Go binary.**

No JDK, no Gradle, no Android SDK. Mocha takes class files and produces artifacts: DEX in a
signed APK, JVM class files, or a standalone executable. A Java frontend is attached, so it
takes `.java` too — but source is an input format, not the point. Anything that emits class
files can drive it.

```bash
mocha build app.jar --target android --api 24 --emit apk -o out/
```

> **Status: early development.** `classfile` — the class file reader and writer everything
> else stands on — is done. The Java frontend and the DEX emitter are in progress; the
> native path is behind them. See [Roadmap](#roadmap) before depending on this.

| Target | Emits | Notes |
| --- | --- | --- |
| `android` | `dex`, `apk` | The flagship path. Ships first. |
| `jvm` | `class` | Fastest loop. Capped at class file 49.0 — [see below](#the-490-ceiling). |
| `native` | `exe` | `linux/amd64`, `darwin/arm64`, `windows/amd64`. Furthest out. |

---

## Quick start

```bash
GOPROXY=direct go install github.com/vertex-language/mocha/cmd/mocha@latest
```

Or grab a prebuilt binary from [Releases](https://github.com/vertex-language/mocha/releases).
Single static binary, no runtime dependencies.

### Real Java, a real library, dexed

An HTTP GET with OkHttp — ordinary Java, an ordinary third-party jar, no build system.

```java
// Fetch.java
package com.example;

import okhttp3.OkHttpClient;
import okhttp3.Request;
import okhttp3.Response;

public class Fetch {
    public static void main(String[] args) throws Exception {
        OkHttpClient client = new OkHttpClient();
        Request request = new Request.Builder()
                .url("https://example.com/")
                .build();
        try (Response response = client.newCall(request).execute()) {
            System.out.println(response.code());
            System.out.println(response.body().string().length());
        }
    }
}
```

Fetch the platform stub once, get the jars from whatever resolver you already have, and
build:

```bash
mocha sdk fetch 24

DEPS=$(cs fetch com.squareup.okhttp3:okhttp:4.12.0 | tr '\n' ' ')

mocha build Fetch.java $DEPS --target android --api 24 -o build/
# → build/classes.dex
```

That is the whole toolchain: no JDK on the machine, no `d8`, no SDK Manager. `--api 24`
finds the cached `android.jar` on its own, and desugars try-with-resources and everything
else API 24 can't take.

Two things worth noticing. The OkHttp jars are passed as **inputs**, not on `--classpath` —
that is what puts their code in your DEX. And those jars came out of the Kotlin compiler;
Mocha never learns that. It reads class files.

### Where a jar goes decides what happens to it

| How you pass it | Compiled | In the artifact | For |
| --- | --- | --- | --- |
| `<input>` | yes | **yes** | your code, and library code you want shipped |
| `--classpath` | no | no | signatures only — resolution |
| `--lib` (implied by `--api`) | no | **never** | `android.jar`; ART supplies the implementation |

The one people trip over: a library jar on `--classpath` compiles cleanly and then
`NoClassDefFoundError`s on device. If you want its code, it's an input. Aars are the
exception — they aren't accepted as inputs, so classes reached from an aar's `classes.jar`
are dexed in and its manifest fragment is merged.

### The other loops

```bash
# JVM class files — fastest iteration
mocha build src/ --target jvm -o build/classes

# Build and run
mocha run src/main.java -- input.txt

# Analyse only, emit nothing
mocha check src/ --target android --api 24 --diagnostics json
```

Full reference: [`docs/cli.md`](docs/cli.md).

---

## Why this exists

Mocha is the Android and JVM backend for the [Vertex language](https://github.com/vertex-language).
Vertex is pure Go, and pulling the Java ecosystem into the main compiler would have meant
dragging its toolchain, object model and platform assumptions along with it. So that work
lives here, on its own release cycle, behind a seam.

**The seam is a class file.** Not a published IR. That decision is the useful part for
everyone else:

- **The reader exists regardless.** Real programs depend on jars, and Android compiles
  against `android.jar`. Making the class file the output format too costs one encoder.
- **It comes with an oracle.** `javap -c` diffed against `javac` is the strongest
  correctness check available, and it only works if the seam is a real class file.
- **The backend is useful before the frontend is.** `javac` can produce the input today.

So there is nothing to integrate against. Something Kotlin-shaped, a DSL that has to run on
Android, a language reaching the platform from its own frontend: emit class files and stop.
Mocha handles dexing, manifest merge, packaging, signing — or native codegen — from there.

---

## Architecture

```
  .java ──→ scanner → parser → sym → attr → flow → desugar → gen ──┐
                                ↑                                  │
  classpath ────────────────────┘  (jars, aars, android.jar)       │
  .class / .jar / .aar ────────────────────────────────────────────┤
                                                                   ↓
                            ══════════════ .class ══════════════   ← the waist
                                    ↓                   ↓
                                 dalvik            ir → amd64 / arm64
                                    ↓                   ↓
   AndroidManifest.xml ──→   manifest → bundle      object (elf/pe/macho)
   [resources.arsc]   ─────────────────→ ↓              ↓
                                       .apk         executable
```

Above the waist we're a Java compiler. Below it, nothing knows Java exists.

**Governing rule: do what `javac` does; deviate only where the target forces it.** The
frontend follows `javac`'s phase order — `Enter`/`MemberEnter` → `Attr`/`Resolve` →
`Flow` → `TransTypes`/`LambdaToMethod`/`Lower` → `Gen`. Symbols are pulled, not swept: every
class symbol carries a completer that fires on first touch, which is the only reason
compiling against a 50 MB `android.jar` is tolerable. Every forced deviation is enumerated
in [`docs/arch.md`](docs/arch.md); one that isn't listed is a bug.

**Dependency rule, one direction only.** `classfile` imports `jvm/*` and the standard
library. `target/*` and `ir` import `classfile`. Nothing below the waist imports anything
above it.

---

## The 49.0 ceiling

`--emit class` refuses to write above class file major 49.0, because there is no
`StackMapTable` generator yet. From 50.0 the verifier expects frames; emitting 50 to lean on
HotSpot's failover means shipping a file the spec calls malformed and hoping. Refusing is
better.

The cost lands on `--target jvm`. Everything modern `javac` desugars *into* `invokedynamic`
— lambdas, method references, string concatenation, pattern switch — has nowhere to go, and
those programs are refused with a diagnostic naming the feature. The same programs compile
for `android` and `native`, which desugar indy away for their own reasons: below API 26,
lambdas become anonymous classes, concat becomes `StringBuilder`, type switch becomes an
`instanceof` chain; native links the metafactory call site at build time, closed-world.

Lifting this is the highest-value item on the backlog.

---

## Android

**The invariant the v1 plan rests on:** an APK containing a binary `AndroidManifest.xml` and
one `classes.dex` installs and launches. No resources means no `R` class to generate.

```bash
mocha build src/ --target android --api 24 --emit apk \
  --package com.example.hello -o out/
```

`--emit apk` is always explicit, because it is the one that needs a package name and a
signature. APKs are signed with APK Signature Scheme v2 — v3 is key rotation, v4 needs a v2
underneath it, and v2 installs. Past 65,536 methods the encoder splits into multidex on its
own; there's no flag and it isn't your problem.

### Dependency tiers

State which tier a claim of "Android support" means.

| Tier | Requires | Reaches |
| --- | --- | --- |
| 0 | `android.jar` | the whole framework; no libraries |
| 1 | jar as an **input** | OkHttp, Gson, plain Java |
| 2 | `.aar` on `--classpath` | utility libraries without resources |
| 3 | `--resources`, or aapt2 | AndroidX, Play Billing, Material |

**Tier 2 is what v1 targets.** An aar containing `res/` needs resource compilation, ID
allocation and an `R` class; Mocha names the dependency it choked on rather than silently
producing a broken APK. Most of AndroidX is in that category.

**The escape hatch for Tier 3 without a resource compiler:** hand Mocha a prebuilt
`resources.arsc` and the generated `R` classes from an existing Android build, and it
packages them unchanged. This lets a real Gradle project substitute `mocha` for the
dex-and-package step alone — the most likely first real use.

### Manifest merge

`--manifest` merges with fragments from every aar on the classpath: union by identity,
`<uses-permission>` deduplicated with `maxSdkVersion` preserved verbatim, `android:required`
OR-merged, app `<uses-sdk>` wins. Two deliberate refusals — `tools:` markers are rejected
with a diagnostic naming the library rather than honoured, and permissions are never
injected on version skew, only diagnosed. Every contribution is reported one line each,
naming its source manifest. A permission appearing in an APK that nobody typed is the
failure this exists to prevent.

### `sdk`

```bash
mocha sdk list
mocha sdk fetch 24
mocha sdk path 24
```

Reads Google's SDK repository index, downloads the platform ZIP, streams out `android.jar`,
discards the rest. Cached under `$MOCHA_HOME` (default `~/.mocha`). One XML parse, one ZIP
read, no Maven — it never parses a POM. **Mocha does not download libraries.**

---

## Native

```
classfile → link → ir → amd64 / arm64 → elf / pe / macho
```

The larger half of the project, and the later one. ART was providing everything below the
DEX; here we provide it ourselves.

**Closed world**, forced by emitting a standalone binary: all class files load into one
universe, rapid type analysis prunes from the entry points, and that pruning is what keeps
the runtime small. No dynamic class loading, no proxies, no agents.

**The runtime is Java, compiled by Mocha.** If `Unsafe` lowers to `MOV`/`LDR` and
`java.lang.foreign.Linker` lowers to `SYSCALL`, then a GC written in Java against `Unsafe`
*is* a GC over raw memory. Only the entry stub, the metadata blobs and the unwind tables
aren't Java. `linux/amd64` is the cheapest first target — static, no libc, no container
ceremony; `darwin/arm64` needs an ad-hoc code signature, `windows/amd64` an import address
table.

`ir` is internal: SSA built from stack bytecode by abstract interpretation, for native
codegen. No textual format, no stability promise, no consumers outside this repo. The
Android path ships its first APK without it.

---

## Commands

| Command | Does |
| --- | --- |
| `mocha build` | Compile and package |
| `mocha check` | Analyse only, emit nothing |
| `mocha run` | Build and execute (JVM target only) |
| `mocha sdk` | Fetch and manage platform stubs |
| `mocha version` | Version, and what this binary can emit |

Five. If a sixth is proposed, it has to displace one. `mocha version --verbose` is the
honest answer to "will my input work" without running a build — the class file and DEX
ranges this binary reads and writes, the API levels it targets, the platforms it emits.

Output is reproducible: no timestamps, no absolute paths in metadata, stable ordering in
pools, string tables and id tables. Same inputs and flags, byte-identical artifact,
signature aside.

### Diagnostics

Every diagnostic carries a file, a byte range, a severity and a stable code. Ordering is
deterministic — sorted by position, not by which pass ran first. `--diagnostics json` emits
one object per line on stderr for editors and CI annotators; codes are stable across
releases and never reused for a different meaning.

| Exit | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Compilation failed with diagnostics |
| `2` | Usage error — bad flags, missing input |
| `3` | Internal error — a compiler bug, please report it |

### Environment

| Variable | Meaning |
| --- | --- |
| `MOCHA_HOME` | Cache root for platform stubs; default `~/.mocha` |
| `NO_COLOR` | Honoured; equivalent to `--color never` |

Two. There is no config file — flags and these.

---

## Dependencies

Mocha takes a classpath. It does not resolve coordinates.

```bash
# Maven
mvn dependency:build-classpath -Dmdep.outputFile=cp.txt

# Gradle
./gradlew printClasspath > cp.txt

# Coursier
cs fetch --classpath com.squareup.okhttp3:okhttp:4.12.0 > cp.txt
```

Then `--classpath "$(cat cp.txt)"` for resolution, or the same jars as inputs if you want
their code shipped.

---

## Not building

| Not building | Because |
| --- | --- |
| A public IR with a textual format | the class file is a better public seam, and already supported |
| Maven / Gradle dependency resolution | a large subsystem whose entire output is a list of file paths |
| A bytecode verifier | the JVM verifies for free on every test run |
| Optimisation levels (`-O`) | `dex2oat`, C1/C2 and profiles do it better and later |
| Resource compilation (aapt2, ARSC) | Tier 3, a separate project; the prebuilt-ARSC hatch is the interim answer |
| APK Signature Scheme v3 / v4 | v3 is key rotation, v4 needs a v2 underneath. v2 installs |
| Android App Bundles (`.aab`) | different format, different problem, no user |
| A config file | a tool that says it needs no configuration and then defines a format has failed at the first sentence |
| Build cache, parallel jobs | correctness first, then measure |
| Annotation processing | needs a live JVM and `javax.lang.model` |

---

## Layout

```text
github.com/vertex-language/mocha/
├── cmd/mocha/
│
├── classfile/           # the waist — read and write .class      ← done
├── jvm/{op,desc,mutf8}/ # opcodes, descriptors, modified UTF-8   ← leaves
├── classpath/           # binary name → bytes; jars, aars, dirs, MR jars
├── sdk/                 # platform stub fetch and cache
│
├── target/dalvik/       # classfile → dex
├── manifest/            # AndroidManifest merge, diagnostics, report
├── bundle/              # dex + axml + align + v2 sign → apk
│   └── axml/
│
├── token/ scanner/ ast/ parser/
├── sym/ types/ analyzer/{attr,flow,warn}/ desugar/ gen/
│
├── ir/                  # SSA from bytecode — deferred until needed
├── link/                # closed world, reachability, substitutions
├── target/{amd64,arm64}/
├── object/{elf,pe,macho}/
└── rt/                  # the Java-source runtime
```

---

## Testing

Every layer is checked against an external oracle, not against itself.

| Layer | Oracle |
| --- | --- |
| `scanner`, `parser` | golden token streams, `ast.Fdump` |
| `analyzer` | diagnostic sets compared against `javac -Xlint` |
| `gen` + `classfile` | `javap -c -p` diff against `javac`, then `java -Xverify:all` |
| `classfile` alone | encode ∘ decode round trip |
| `manifest` | merged output diffed against AGP's merged manifest for the same inputs |
| `target/dalvik` | `dexdump` diff against `d8`; run on an emulator |
| native | run the binary |

The `javap` diff is load-bearing. A round trip survives a symmetric bug in the reader and
writer perfectly, and proves nothing on its own.

---

## Roadmap

The Android path lands before the native one.

- [x] `classfile` — read and write class files
- [ ] `classpath` — jars, aars, directories, multi-release jars
- [ ] `sdk` — platform stub fetch and cache
- [ ] Java frontend — `scanner` through `gen`
- [ ] `target/dalvik` — class files → DEX
- [ ] `manifest` — merge, diagnostics, contribution report
- [ ] `bundle` — AXML, alignment, v2 signing → APK
- [ ] `StackMapTable` generation — lifts the 49.0 ceiling and unblocks modern source on `--target jvm`
- [ ] Native — `link`, `ir`, `target/{amd64,arm64}`, `object/*`, `rt`

### Exploring

- **Execution without a JVM.** An interpreter over the internal IR would make `mocha run`
  work with no runtime installed — useful for testing and CI. Whether it's worth the
  maintenance cost, and how far it gets before the missing class library becomes the
  blocker, is open. Not committed to.

## Contributing

Contributions are very welcome, particularly on the emitter tracks above. Open an issue to
discuss direction before starting anything large.

## License

MIT