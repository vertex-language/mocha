# Architecture

`mocha` is a Go toolchain that turns JVM class files into things that run: DEX in an APK
for Android, native binaries for desktop. A Java frontend feeds it and is a separate
concern on a separate schedule.

The class file is the waist. Above it we're a Java compiler; below it, nothing knows Java
exists.

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

**Governing rule: do what `javac` does; deviate only where the target forces it.** Every
deviation is listed under [Forced deviations](#forced-deviations). One that isn't listed
is a bug.

---

## Why the class file is the waist

- **The reader exists regardless.** Real programs depend on jars; Android compiles against
  `android.jar`. Making it the output format too costs one encoder.
- **It comes with an oracle.** `javap -c` diffed against `javac` is the strongest
  correctness check available, and it only works if the seam is a real class file.
- **No IR to publish.** Anything targeting mocha emits class files; `javac` can produce
  the input, so the backend is useful before the frontend exists.

Cost: class files erase generics, and local names survive only under `-g`. The frontend
always emits `Signature`, `LineNumberTable` and `LocalVariableTable` internally regardless
of what the user keeps on disk.

---

## Frontend

`javac`'s phase order, verified against `openjdk/jdk` master.

| `javac` | Does | mocha |
| --- | --- | --- |
| `Scanner` / `JavacParser` | source → tree | `scanner`, `parser`, `ast` |
| `Enter` / `TypeEnter` / `MemberEnter` | symbols, supertypes, members, imports | `sym` |
| `ClassFinder` + `ClassReader` | classpath `.class` → symbols | `classpath` + `classfile` |
| `Attr` / `Resolve` / `Infer` | types, resolution, inference, constant folding | `analyzer/attr` |
| `Flow` | definite assignment, reachability, exceptions | `analyzer/flow` |
| `WarningAnalyzer` | lint — a real phase since JDK 24 | `analyzer/warn`, post-v1 |
| `TransTypes` | erasure, bridges | `desugar` |
| `TransPatterns` | record patterns, pattern switch | `desugar` |
| `LambdaToMethod` | lambdas, method refs | `desugar` |
| `Lower` | inner classes, enums, foreach, assert, try-with-resources | `desugar` |
| `Gen` / `Code` | tree → bytecode | `gen` |
| `ClassWriter` | → bytes | `classfile` |

`Check` is a helper `Attr` calls into, not a phase. Annotation processing is not
supported.

### Symbols are pulled, not swept

No importer phase. `Enter` installs a **completer** on each class symbol; everything after
is on demand. When `Attr` first touches a member of `java.util.Map`, the completer fires,
decodes that class, and returns. `Symtab` takes its default completer from `ClassFinder`,
so source-backed and classpath-backed symbols differ only in which completer was
installed.

This is why `classfile` inflates pool entries lazily and why `classpath` reads stub jars
with `SkipCode|SkipDebug`. It is the only reason compiling against a 50 MB `android.jar`
is tolerable.

Cyclic completion is detected in `sym`, reported once, and the symbol left poisoned but
usable — invariant 4, same as the parser.

### One class at a time

`javac`'s `BY_TODO` policy: pull one class off the queue, run it to bytecode, take the
next. Two ordering constraints fall out, neither optional:

- Erasure destroys information flow analysis needs → a class is fully flowed before it is
  desugared.
- Lowering reads synthetic members on superclasses → **supertypes desugar before
  subtypes**. `desugar` forces supertype dependencies through first.

---

## The `invokedynamic` wall

Modern `javac` desugars *into* indy: `LambdaMetafactory`, `StringConcatFactory`,
`SwitchBootstraps`. All of it lands on class file features the encoder lacks — indy,
`BootstrapMethods`, `StackMapTable`.

Three exits, chosen per target:

- **Dalvik.** ART has `invoke-custom` from API 26. Below that, what D8 does: lambdas →
  anonymous classes, concat → `StringBuilder`, type switch → `instanceof` chain. Gated on
  `--api`.
- **Native.** Closed world, so a `LambdaMetafactory` site is *linked* at build time:
  synthesise the class, emit `new` + virtual call.
- **JVM output.** Doesn't work today. Needs §4.10.1 frame generation — the highest-value
  backlog item.

Desugaring therefore has a **portable** layer matching `javac` exactly, and a **target**
layer that runs only when the API level or the class file ceiling forces it.
`--emit class` gives the portable layer only.

### The 49.0 ceiling

From 50.0 the verifier expects a `StackMapTable`. HotSpot still fails over to the
type-inference verifier for major 50 when frames are missing or wrong; it does **not** for
51 and up. Emitting 50 and relying on that failover means shipping a file the spec calls
malformed and hoping. 49.0 verifies by type inference by design, which every current JVM
supports. Refusing above 49.0 is better than emitting a class that loads by accident.

---

## Platform surface

Two distinct services live at `dl.google.com`:

- **`/android/repository/`** — the SDK repository. `repository2-1.xml` indexes platform
  ZIPs, each containing `android.jar`. This is the **compile-time API surface**: never
  emitted, linked, or bundled. ART supplies the implementation.
- **`/dl/android/maven2/`** — Google's Maven repository. **Real libraries whose code goes
  into your dex.**

`android.webkit.WebView` is in the first. `com.android.billingclient:billing` is in the
second. Almost everything people call "the Android API" is in the first and costs nothing.

### `sdk`

GET the repository index, find the `<remotePackage>` for the API level, download the
platform ZIP, stream out `android.jar`, cache it, discard the rest. One XML parse, one ZIP
read, no Maven. Never parses a POM — if it starts to, it is the wrong package.

### Dependency tiers

State which tier a claim of "Android support" means.

| Tier | Requires | Reaches |
| --- | --- | --- |
| 0 — platform only | `android.jar` | the whole framework; no libraries |
| 1 — code-only JARs | jar on classpath | OkHttp, Gson, plain Java |
| 2 — AARs without resources | AAR unpack, manifest merge | some utility libraries |
| 3 — AARs with resources | ARSC, R classes, resource merge | AndroidX, Play Billing, Material |

**Tier 2 is the v1 target.** Tier 3 is a separate project, and the sole reason to revisit
the ARSC rejection.

**Escape hatch for Tier 3 without aapt2:** accept a prebuilt `resources.arsc` and `R`
class from an existing Android build and package them unchanged. This lets a real Gradle
project substitute mocha for the dex-and-package step alone — the most likely first real
use.

---

## Android path

```
classfile → dalvik ──┐
AndroidManifest.xml → manifest ──→ bundle → .apk
[resources.arsc] ────┘
```

**Invariant the whole v1 plan rests on:** an APK containing a binary
`AndroidManifest.xml` and one `classes.dex` installs and launches. `AndroidManifest.xml`
is the only mandatory entry; with no resources there is no `R` class to generate, and an
`<application>` with no `android:icon` gets the system default. If this is ever false,
Tiers 0–2 collapse and the build order is wrong.

**We compile against stubs and ship nothing.** `android.jar` gives `classpath` signatures
and never leaves the build — the documented mechanism, not a shortcut; `d8` takes the
platform jar the same way. ART supplies runtime, GC, threads, class loading and `<clinit>`
ordering, then `dex2oat` AOT-compiles at install and idle, guided by profiles.

Which means **DEX quality matters less than DEX correctness.** Emit straightforward code
and move on.

### `dalvik`

- Register machine, but **arguments live in the highest registers** and most instruction
  forms address only `v0`–`v15`. Hot values go low; shuffles otherwise.
- 64-bit values take **register pairs**; a wide value in `vN` makes `vN+1` unusable.
- Id tables are sorted and 16-bit indexed. Past 65,536 methods the encoder splits into
  multidex rather than making that the caller's problem.
- Little-endian with LEB128 against the class file's big-endian. Byte readers are not
  shared; `jvm/desc` and `jvm/mutf8` are.
- Features gate on API level, not class file version: default and static interface methods
  from 24, `invoke-custom` from 26.

### `manifest`

Its own package, beside `bundle`. Merge is XML tree semantics, priority ordering and
conflict diagnostics; `bundle` is a ZIP writer with an AXML encoder. Different jobs.

Tier 2 AARs ship manifests with `<uses-permission>` and `<application>` children, so this
is v1 work, not follow-up. It is also more than a union — the Gradle merger special-cases
`<uses-sdk>`, ORs `android:required`, honours `tools:` markers and selectors, and will
inject system permissions when a library declares a lower `targetSdkVersion`. A library
can also attach `android:maxSdkVersion` to a `<uses-permission>`, which makes a permission
the app declared silently disappear on newer devices.

v1 rules, deliberately small:

| Concern | v1 behaviour |
| --- | --- |
| Element union | by identity (`android:name` where it exists) |
| `<uses-permission>` | deduplicated; `maxSdkVersion` preserved verbatim, never inferred |
| `<uses-sdk>` | app value wins; a library `minSdkVersion` above the app's is an error |
| `android:required` | OR merge, matching the platform tool |
| `tools:` markers | **refused with a diagnostic naming the library** — not ignored |
| Implicit permission injection | **not done.** Diagnose the version skew; never add a permission nobody wrote |

Every contribution is logged, one line each, naming the source manifest. A permission
appearing in an APK that nobody typed is the failure mode this whole section exists to
prevent.

**Open:** binary AXML carries a resource-map chunk associating `android:*` attribute names
with framework `attr` resource IDs. Whether `axml` hardcodes that table or extracts it
from `android.jar` decides whether the encoder needs a platform stub at all. Verify
against AOSP `ResourceTypes.h` before committing.

### `bundle`

Writes the manifest as binary XML, aligns entries to 4 bytes, stores `resources.arsc`
**uncompressed** when one is present, and injects a v2 signature block immediately ahead
of the ZIP central directory.

---

## Native path

```
classfile → link → ir → amd64 / arm64 → elf / pe / macho
```

Step 6 of 7, and much larger than the Android path. ART was providing everything below the
dex; here we provide it ourselves. Detail belongs in `native.md`; this is the shape.

**Closed world.** All class files — application, libraries, the reachable subset of
`java.base` — load into one universe. No dynamic class loading, no `Class.forName` on a
computed name, no proxies, no agents. Rapid type analysis prunes from the entry points;
that pruning is what keeps the runtime small. Closed world is forced by emitting a
standalone binary, not chosen.

**The runtime is Java, compiled by mocha.** This falls out of the interception strategy:
if `Unsafe` lowers to `MOV`/`LDR` and `java.lang.foreign.Linker` lowers to `SYSCALL`, then
a GC written in Java against `Unsafe` *is* a GC over raw memory. Only the entry stub, the
metadata blobs and the unwind tables aren't Java.

| Concern | Where |
| --- | --- |
| Object layout, vtables, itables, class metadata | data emitted by `target/*` |
| Allocation, GC | Java, over `Unsafe` |
| Monitors, park/unpark, threads | Java, over `Unsafe` CAS + `Linker` |
| Exception unwind tables | emitted by `target/*`, consumed by Java handlers |
| `<clinit>` ordering | scheduled at link time where provably safe, guarded otherwise |
| `native` methods in `java.base` | substitution table keyed on name + descriptor |
| Entry stub, relocations, containers | `object/{elf,pe,macho}` |

Per-target obligations:

- **darwin/arm64** — W^X; unsigned binaries are `SIGKILL`ed, so `macho/adhoc.go` appends an
  ad-hoc `CodeDirectory` before the file hits disk.
- **windows/amd64** — an Import Address Table so the loader resolves `kernel32.dll`;
  Panama downcalls become IAT entries.
- **linux/amd64** — static, no libc, direct `SYSCALL`. No container ceremony, so the
  cheapest first target.

---

## IR

`ir/builder` turns stack bytecode into a register-based SSA graph by abstract
interpretation: split blocks at branch targets and handler bounds, interpret each with a
symbolic stack, φ at merges. Exception edges are real edges — any instruction in a
protected range can reach its handler, so the handler's entry state is the merge of every
partial state in the range. For classes ≥ 50.0 the `StackMapTable` already names block
boundaries and entry types: a shortcut and a free cross-check.

Internal detail — no textual format, no stability promise, no consumers outside this repo.
**Deferred.** The Android path ships a first APK with a naive stack-slot-to-register
mapping and no `ir` package. Build it when native codegen needs it, or when DEX quality
shows up in a measurement.

`jsr`/`ret` — inline or refuse; don't try to represent them.

---

## Rejected

| Not building | Because |
| --- | --- |
| A public IR with a textual format | the class file is a better public seam, and already supported |
| Maven / Gradle dependency resolution | a large subsystem whose entire output is a list of file paths; `javac` and `d8` both take a classpath |
| A bytecode verifier | the JVM verifies for free on every test run; `-Xverify:all` plus the `javap` diff is the real gate |
| Resource compilation (aapt2, ARSC) | Tier 3, a separate project; the prebuilt-ARSC hatch is the interim answer |
| APK Signature Scheme v3 / v4 | v3 is key rotation plus per-signer SDK ranges; v4 is streaming install and needs a v2/v3 anyway. v2 installs |
| Android App Bundles (`.aab`) | different format, different problem, no user |
| A config file | a tool that says it needs no configuration and then defines a format has failed at the first sentence |
| Build cache, parallel jobs | correctness first, then measure |
| Annotation processing | needs a live JVM and `javax.lang.model` |

---

## Forced deviations

| Deviation | Forced by |
| --- | --- |
| Scanner never merges `>` with a following `>` | no lexical type context; parser rejoins via `token.Join` |
| Class file ceiling of 49.0 | no `StackMapTable` generator yet |
| indy desugared away | the 49.0 ceiling (JVM), API < 26 (Dalvik), closed world (native) |
| Closed world, no dynamic loading | standalone native binaries have no other option |
| `native` methods replaced by substitutions | no JNI, no OpenJDK `libjava.so` |
| `Unsafe` / `Linker` / `@IntrinsicCandidate` lowered to instructions | the runtime must reach hardware, and these are the standard-API doors |
| SDK fetched from `dl.google.com` directly | Google's SDK Manager is a JVM tool and we don't require a JDK |
| Manifest merge implemented, not delegated | AGP owns the only other implementation and it needs Gradle |

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

**Dependency rule, one direction only.** `classfile` imports `jvm/*` and stdlib.
`target/*` and `ir` import `classfile`. Nothing below the waist imports anything above it.

---

## Testing

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