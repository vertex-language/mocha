# CLI.md

# CLI

`mocha` is one static binary. No JDK, no Gradle, no Android SDK.

It takes class files and produces artifacts. A Java frontend is attached, so it also
takes `.java` — but source is an input format, not the point. Anything that emits class
files can drive it.

```bash
mocha build app.jar --target android --api 24 --emit apk -o out/
```

**One job: compile and package.** No task graph, no lifecycle, no plugins, no config
file, no dependency resolution. Drive it from whatever you already use.

---

## Commands

| Command | Does |
| --- | --- |
| `mocha build` | Compile and package |
| `mocha check` | Analyse only, emit nothing |
| `mocha run` | Build and execute (JVM target only) |
| `mocha sdk` | Fetch and manage platform stubs |
| `mocha version` | Version, and what this binary can emit |

Five. If a sixth is proposed, it has to displace one.

---

## `mocha build`

```
mocha build [flags] <input>...
```

Inputs are `.java` source, `.class` files, `.jar`, or directories. Directories are walked
for all of them. `-` reads source from stdin.

```bash
# Class files in, DEX out
mocha build classes/ --target android --api 24

# Source in, APK out
mocha build src/ --target android --api 24 --emit apk -o build/

# JVM class files, fastest loop
mocha build src/ --target jvm -o build/classes

# Someone else's jar, dexed
mocha build app.jar lib.jar --target android --api 24
```

### Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-t, --target` | `jvm` | `jvm`, `android`, `native` |
| `-o, --out` | `./build` | Output directory; `-` for stdout |
| `--emit` | target default | `class`, `dex`, `apk`, `exe` |
| `--api` | `21` | Android only; selects DEX version and desugaring |
| `--platform` | host | Native only; `linux/amd64`, `darwin/arm64`, `windows/amd64` |
| `--classpath` | — | Jars, aars, class dirs — for resolution |
| `--lib` | — | Platform stub (`android.jar`); implied when `--api` is set and a stub is cached |
| `--manifest` | generated | `AndroidManifest.xml`; merged with library fragments |
| `--package` | — | Application id; required for `--emit apk` without a manifest |
| `--resources` | — | Prebuilt `resources.arsc`, packaged unchanged |
| `--main` | — | Entry point; required for `--emit exe` when more than one class declares `main` |
| `--sign` | debug key | `debug`, or `keystore:path,alias` |
| `-g, --debug-info` | `true` | Line tables and local variable names |
| `--diagnostics` | `text` | `text`, `json` |
| `--max-errors` | `100` | Stop after N; `0` for unlimited |
| `-Werror` | `false` | Promote warnings |
| `--color` | `auto` | `auto`, `always`, `never` |
| `-q` / `-v` | | Errors only / pass timings to stderr |

Emit defaults: `jvm` → `class`, `android` → `dex`, `native` → `exe`. `--emit apk` is
always explicit, because it is the one that needs a package name and a signature.

### Inputs are shipped; `--classpath` is not

Everything in `<input>...` is compiled and lands in the artifact. `--classpath` is for
**resolution** — its contents are read for signatures and otherwise ignored.

The consequence is the one people trip over: a library jar on `--classpath` will compile
and then `NoClassDefFoundError` on device. If you want its code, pass it as an input.
Aars are the exception — they aren't accepted as inputs, so classes reached from an aar's
`classes.jar` are dexed into the output and its manifest fragment is merged.

`--lib` is never shipped under any circumstances. That is the documented mechanism, not a
shortcut: `android.jar` is a stub, ART supplies the implementation.

### The 49.0 ceiling

`--emit class` refuses to write above class file major 49.0, because there is no
`StackMapTable` generator yet. From 50.0 the verifier expects frames, and emitting 50 to
lean on HotSpot's failover means shipping a file the spec calls malformed. Refusing is
better.

What that costs on `--target jvm`: everything modern `javac` desugars *into*
`invokedynamic` — lambdas, method references, string concatenation, pattern switch — has
nowhere to land. Those programs compile for `android` and `native`, which desugar indy
away for their own reasons, and are refused for `jvm` with a diagnostic naming the
feature. Lifting this is the highest-value backlog item.

`--api` gates the Android side of the same problem: `invoke-custom` from 26, default and
static interface methods from 24. Below those, lambdas become anonymous classes, concat
becomes `StringBuilder`, type switch becomes an `instanceof` chain.

### Output

Reproducible: no timestamps, no absolute paths in metadata, stable ordering in pools,
string tables and id tables. Same inputs and flags, byte-identical artifact — signature
aside.

Past 65,536 methods the DEX encoder splits into `classes.dex`, `classes2.dex` and so on.
There is no flag for this and it is not your problem.

APKs are signed with APK Signature Scheme v2 only. v3 is key rotation, v4 needs a v2
underneath it, and v2 installs.

### The `-O` question

There is no `-O`. Optimisation level is not a user decision here:

- **Android** — `dex2oat` re-optimises at install and idle, guided by real profiles.
  Anything we do ahead of it is redundant at best, so DEX quality matters less than DEX
  correctness.
- **JVM** — C1 and C2 do this at runtime, better, with real profiles.
- **Native** — code quality comes from `ir`, not from a level. Until `ir` exists there is
  nothing for a number to select between, and there is no reason to ship a slow mode
  afterwards.

If a knob becomes necessary it will be a named flag for a named problem, not a number.

---

## `mocha check`

```
mocha check [flags] <input>...
```

Parses, resolves, and type-checks. Emits nothing. Same input and diagnostic flags as
`build`; fastest path to a complete diagnostic set. Use it in editors and pre-commit
hooks.

```bash
mocha check src/ --target android --api 24 --diagnostics json
```

`--target` and `--api` still matter — platform APIs, API-level availability and the class
file ceiling are part of the check.

On `.class` input this validates structure and references, which is useful for auditing
artifacts from another toolchain. It is not a bytecode verifier and will not become one:
the JVM verifies for free on every test run.

---

## `mocha run`

```
mocha run [flags] <input>... [-- args...]
```

Builds to a temporary directory and executes. Requires `--target jvm` and a JVM on
`PATH`. Android has no execution story — use `adb install`. Native needs no help; the
artifact is the executable.

```bash
mocha run src/main.java -- input.txt
mocha run app.jar --main com.example.Hello
```

Arguments after `--` go to the program. `--main` picks an entry point when more than one
class declares `main`. The 49.0 ceiling applies here too, since this is the JVM target.

---

## `mocha sdk`

```
mocha sdk list
mocha sdk fetch <api-level>
mocha sdk path <api-level>
```

Fetches `android.jar` from Google's SDK repository without the SDK Manager: reads the
repository index, downloads the platform ZIP, streams out the jar, discards the rest.

```bash
mocha sdk fetch 24
mocha build src/ --target android --api 24 --emit apk
```

Once fetched, `--api` finds the stub automatically and `--lib` becomes unnecessary.
Cached under `$MOCHA_HOME` (default `~/.mocha`).

`mocha sdk path 24` prints the jar location, for feeding to other tools.

This is `dl.google.com/android/repository/`, the SDK repository — not
`dl.google.com/dl/android/maven2/`, Google's Maven repository. It fetches platform stubs
and nothing else. It never parses a POM. `mocha` does not download libraries.

---

## `mocha version`

```
mocha version [--verbose]
```

Version, commit, Go version. With `--verbose`, the class file and DEX format ranges this
binary can read and write, the API levels it can target, and the native platforms it can
emit — the honest answer to "will my input work" without running a build.

Check it before assuming a target exists. The Android path lands before the native one.

---

## Dependencies

`mocha` takes a classpath. It does not resolve coordinates.

```bash
# Maven
mvn dependency:build-classpath -Dmdep.outputFile=cp.txt
mocha build src/ --classpath "$(cat cp.txt)" --target android --api 24

# Gradle
./gradlew printClasspath > cp.txt

# Coursier
cs fetch --classpath com.squareup.okhttp3:okhttp:4.12.0 > cp.txt
```

State which tier a claim of "Android support" means:

| Tier | Requires | Reaches |
| --- | --- | --- |
| 0 | `--lib android.jar` | the whole framework; no libraries |
| 1 | jar as an **input** | OkHttp, Gson, plain Java |
| 2 | `.aar` on `--classpath` | utility libraries without resources |
| 3 | `--resources`, or aapt2 | AndroidX, Play Billing, Material |

Tier 2 is what v1 targets. An aar containing `res/` needs resource compilation, ID
allocation and an `R` class; `mocha` will name the dependency it choked on rather than
silently producing a broken APK. Most of AndroidX is in this category.

### Resources

`--resources` is the escape hatch for Tier 3 without a resource compiler: take
`resources.arsc` and the generated `R` classes from an existing Android build, and
`mocha` packages them unchanged.

```bash
mocha build classes/ R-classes/ \
  --target android --api 24 --emit apk \
  --manifest AndroidManifest.xml \
  --resources build/intermediates/resources.arsc \
  -o out/
```

The `R` classes are ordinary inputs; nothing is regenerated and no ID is allocated. This
lets a real Gradle project substitute `mocha` for the dex-and-package step alone.

### Manifest merge

`--manifest` is merged with fragments from every aar on the classpath. Union is by
identity, `<uses-permission>` is deduplicated with `maxSdkVersion` preserved verbatim,
`android:required` is OR-merged, and the app's `<uses-sdk>` wins — a library
`minSdkVersion` above the app's is an error.

Two deliberate refusals: `tools:` markers are rejected with a diagnostic naming the
library rather than ignored, and permissions are never injected on version skew, only
diagnosed. Every contribution is reported one line each, naming its source manifest. A
permission appearing in an APK that nobody typed is the failure this section exists to
prevent.

---

## Diagnostics

Every diagnostic carries a file, a byte range, a severity, and a stable code. Ordering is
deterministic — sorted by position, not by which pass ran first.

`--diagnostics json` emits one object per line on stderr, for editors and CI annotators.
Codes are stable across releases and never reused for a different meaning.

### Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Compilation failed with diagnostics |
| `2` | Usage error — bad flags, missing input |
| `3` | Internal error — a compiler bug, please report it |

---

## Environment

| Variable | Meaning |
| --- | --- |
| `MOCHA_HOME` | Cache root for platform stubs; default `~/.mocha` |
| `NO_COLOR` | Honoured; equivalent to `--color never` |

Two. There is no config file — flags and these.

---

## Flags that don't exist

| Not accepted | Instead |
| --- | --- |
| `-O`, optimisation levels | see above |
| `-processor`, `-proc:` | annotation processing needs a live JVM and `javax.lang.model` |
| Dependency coordinates | resolve them elsewhere, pass paths |
| `--config`, `mocha.toml` | a tool that says it needs no configuration and then defines a format has failed at the first sentence |
| `--jobs`, `--cache` | correctness first, then measure |
| `--aab` | different format, different problem |