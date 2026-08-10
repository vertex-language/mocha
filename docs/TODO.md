# TODO

Working order toward a self-contained toolchain. The Android path lands before
the native one; within it, each item is chosen so that the thing it depends on
already has an oracle.

Ordering rule: **nothing goes above a layer whose external oracle does not yet
run.** A round trip survives a symmetric bug in the reader and writer
perfectly. That is why `classfile` had to earn its checkbox before `dalvik`
starts, and why `dalvik` has to earn its own before `bundle` does.

---

## Status

| Package | State | Oracle |
| --- | --- | --- |
| `token` | usable | golden dumps — **not written** |
| `scanner` | usable | golden token streams — **not written** |
| `ast` | usable | `Fdump` goldens — **not written** |
| `parser` | usable | goldens + `javac` diagnostic sets — **not written** |
| `classfile` | done | `javap -c` diff vs `javac`, `java -Xverify:all` — running |
| `classpath` | usable | none yet |
| `sdk` | **README only, no source** | none |
| `jvm/{op,desc,mutf8}` | usable | none yet |
| `cmd/mocha` | `check`, `version` only | none |
| everything else | not started | — |

Two roadmap checkboxes in the top-level README are ahead of the code:
`classpath` and `sdk` are listed as in progress, but `sdk` has no source at
all. Fix the README or write the package; the quick-start's `mocha sdk fetch
24` does not work today.

---

## 0. Foundations — before any new package

These are cross-cutting. Retrofitting them across five packages later is
strictly worse than doing them while there are two producers.

### 0.1 Diagnostics — one model, stable codes

**Blocking `target/dalvik`.** Three unrelated error types exist and none
matches what the CLI promises:

| Type | Carries | Code |
| --- | --- | --- |
| `token.Diagnostic` | `Pos`, `End`, `Severity`, `Msg` | no |
| `classfile.SyntaxError` | `File`, `Off`, `Msg` | no |
| `classpath.Error` / `NotFoundError` | `Origin`, wrapped error | no |

The README promises a file, a byte range, a severity and a **stable code**,
deterministically ordered, with `--diagnostics json`. And `token.Pos` is
per-`File` by invariant 2 — so a dalvik diagnostic about a class file has
nowhere to live today.

- [ ] New `diag` package: a `Code` type, the registry, and a `Source` union —
      either a `*token.File` position or a `classpath.Origin` plus a byte
      offset. One place declares every code, so "never reused for a different
      meaning" is enforceable rather than aspirational.
- [ ] Add `Code` to `token.Diagnostic`; assign codes to the existing scanner
      and parser messages.
- [ ] `--diagnostics json`: one object per line on stderr.
- [ ] Deterministic ordering across sources, not just within a `File`.
- [ ] Fix `cmd/mocha`'s `isError`, which is stale — `token.SevError` and
      `SevWarning` both exist, so it is `d.Severity == token.SevError` and
      warnings stop costing exit 1.

### 0.2 Front-end goldens

`scanner`, `ast` and `parser` have no tests. Their oracles are cheap and the
frontend work in §4 will churn them constantly.

- [ ] `testutils`: golden-file helper with `-update`.
- [ ] `scanner`: token streams over the awkward cases the README already
      names — the `>` splits, the `non-sealed` splice and its two negatives,
      `1...2`, `0b1012`, `0_777`, `0x1.8`, `'\u000a'`, text block delimiters.
- [ ] `parser`: `ast.Fdump` goldens for each speculation site, plus the
      contextual-keyword pairs (`record`/`yield`/`sealed` as declaration vs
      name) and the two casts.
- [ ] `parser`: recovery cases assert **one** diagnostic, never a cascade.
      Invariant 4 is currently only enforced by reading the code.

### 0.3 Bugs found while reviewing

- [ ] `parser.skipBalanced` never checks that a closer matches its opener.
      `{ a )` pops the `RBRACE` expectation on a `)`. `HeaderOnly` depends on
      this for body skipping.
- [ ] `codewriter.go`: `Athrow` is absent from `simpleDelta`, so
      `c.Op(op.Athrow)` fails with "not permitted here" while `c.Throw()`
      bypasses the table and never pops the throwable. Add
      `set(1, 0, op.Athrow)` and route `Throw` through `Op`.
- [ ] `token/kind.go`: the `LSS` and `SHL` comments are empty — text eaten by
      an unescaped `<` somewhere in the pipeline. Should read `// <` and
      `// <<`.
- [ ] `token.Join`'s callers disagree on the success predicate: `expr.go` uses
      `n > 0`, the doc comment and both READMEs use `k != ILLEGAL`. Pick
      `n > 0` and make all three match.
- [ ] `classfile` README's `TestRoundTrip` calls a `rebuild(c)` that cannot
      exist — the package deliberately has no model-to-model path. Either add
      `rebuild` to the test package or amend the section.
- [ ] `ACC_STRICT` doc conflict: the code says major 60 is the last version
      honouring it, the `classfile` README's version table says 61. Code is
      right.

---

## 1. `cmd/mocha build` — join the two halves

`classpath` and `classfile` have never been in the same process. Nothing
imports both. `classpath.Load` returns bytes plus an `Origin`; `classfile.Read`
takes bytes and has nowhere to put a filename. The seam is untested.

- [ ] `mocha build --target jvm --emit none <inputs>`: build a `Path`, walk
      `Entries(Input)`, `Load` each name, `Read` it, report failures with the
      `Origin` attached.
- [ ] Flag surface: `<input>` vs `--classpath` vs `--lib`, mapped to
      `classpath.Role`. This is the distinction that prevents the
      `NoClassDefFoundError`-on-device failure, so it needs a test.
- [ ] Loose `.class` inputs: the driver reads them, gets the binary name from
      `this_class`, and registers a `classpath.Static`. This is the caller
      `Static` exists for and it has none.
- [ ] Exit codes wired: 0 / 1 / 2 / 3, per the README contract.

Small and unblocked, but it gives `target/dalvik` a caller on day one.

---

## 2. `sdk` — write the package

The README describes it fully; there is no source. Everything it needs from
the rest of the tree is a file path, so it can be written any time.

- [ ] `Cache`, `Open`, `Path`, `Fetch`. `Path` never touches the network.
- [ ] Repository index parse: local names only, no namespace binding, no
      `xsi:type` matching.
- [ ] The two load-bearing filters: exclude non-empty `<codename>` (preview
      platforms collide on `api-level`), accept only `channel-0`.
- [ ] `meta.json` written **last** — its presence is the completion marker.
- [ ] Extraction: stream to `tmp/`, find the entry by base name at depth two
      (the top directory is named for the platform version, not the API
      level), error on an ambiguous match.
- [ ] Size **and** SHA-1 both checked; the trust anchor is TLS, and the
      package should not pretend otherwise.
- [ ] `mocha sdk list|fetch|path`.
- [ ] Tests: index parsing against a captured `repository2-3.xml`; extraction
      against a synthetic ZIP. No network in tests.

---

## 3. `target/dalvik` — the flagship path

### 3.0 Decide where abstract interpretation lives — **do this first**

The docs contradict each other. `classfile`'s README: *"mocha never rewrites a
`.class`; dex comes from IR."* The top-level README hangs `dalvik` directly off
the waist and says the Android path ships its first APK without `ir`.

This is a real design question, not a typo. JVM bytecode is stack-based, dex is
register-based, and the translation needs the operand stack's shape at every
program point. `CodeWriter` already shows the cost of not having it — the
documented `dup2`/`pop2` gap is exactly the category-1 vs category-2
distinction that depth-only tracking cannot see, and dex needs it to allocate a
register **pair** for a `long`.

Three consumers want the same machinery: `dalvik`, `StackMapTable` generation
(§8), and `ir/builder` (§9). Decide now whether it lives in a small shared
package below all three, or whether `dalvik` rolls its own and the other two
inherit it later. Writing it twice guarantees the two copies disagree.

- [ ] Decide, write it down in `docs/arch.md`, fix whichever README is wrong.

### 3.1 The encoder

- [ ] Little-endian writer with LEB128 / SLEB128 / ULEB128p1. Not shared with
      `classfile`'s big-endian reader.
- [ ] ID tables. **`string_ids` sorts by UTF-16 code point order, not UTF-8
      byte order** — they differ above U+FFFF, and reproducibility makes this
      load-bearing rather than cosmetic. `type_ids`, `proto_ids`, `field_ids`,
      `method_ids` each have their own sort key.
- [ ] `jvm/desc` and `jvm/mutf8` carry over verbatim; `Shorty()` already
      exists for `proto_ids`.
- [ ] Instruction selection, register allocation, `try`/`catch` tables.
- [ ] Header, map list, checksum, SHA-1 signature.
- [ ] Defer multidex. One correct `classes.dex` first, then the 65,536-method
      split.

### 3.2 Oracle

- [ ] `testutils`: `d8` and `dexdump` alongside the JDK tools, same skip
      behaviour.
- [ ] `dexdump` diff against `d8`, over class files `javac` produced. This is
      available from day one and is why `dalvik` comes before the frontend.
- [ ] Emulator run, once there is an APK to install.

---

## 4. Frontend — `sym` through `gen`

Five packages before anything is observable end to end, which is why they come
after a working dex path rather than before it. `javac`'s phase order:
`Enter`/`MemberEnter` → `Attr`/`Resolve` → `Flow` →
`TransTypes`/`LambdaToMethod`/`Lower` → `Gen`.

- [ ] `sym`, `types` — symbols pulled, not swept: every class symbol carries a
      completer that fires on first touch. This is the only reason compiling
      against a 50 MB `android.jar` is tolerable, and it is what drives
      `classpath` concurrently.
- [ ] `analyzer/attr` — resolution and typing.
- [ ] `analyzer/flow` — definite assignment, reachability, effectively final.
- [ ] `analyzer/warn` — oracle is `javac -Xlint` diagnostic sets.
- [ ] `desugar` — inner classes, generics erasure, enhanced for, varargs,
      string concat, enums, records, try-with-resources, lambdas → anonymous
      classes below API 26.
- [ ] `gen` — oracle is the `javap -c` diff, already running.
- [ ] Populate `docs/arch.md` with every forced deviation from `javac`. The
      README says one that isn't listed is a bug; the file needs to exist for
      that to mean anything.

---

## 5. `manifest`

Fully unblocked — aar fragments already arrive as bytes from
`classpath.Aar.Manifest()`, and AGP's merged output is a first-class oracle.
Good parallel work whenever the dexer is being stubborn.

- [ ] Union by identity; `<uses-permission>` deduplicated with
      `maxSdkVersion` preserved verbatim; `android:required` OR-merged; app
      `<uses-sdk>` wins.
- [ ] Two deliberate refusals: `tools:` markers rejected with a diagnostic
      naming the library, never honoured; permissions never injected on
      version skew, only diagnosed.
- [ ] Contribution report, one line each, naming the source manifest. A
      permission appearing in an APK that nobody typed is the failure this
      exists to prevent.
- [ ] Oracle: diff against AGP's merged manifest for the same inputs.

---

## 6. `bundle` — AXML, alignment, signing

- [ ] `bundle/axml`: binary XML encoder, string pool, resource map.
- [ ] Zip assembly with correct alignment.
- [ ] APK Signature Scheme v2. Not v3 (key rotation), not v4 (needs a v2
      underneath). v2 installs.
- [ ] Reproducible output: no timestamps, no absolute paths in metadata,
      stable ordering in pools and id tables. Byte-identical artifact for the
      same inputs and flags, signature aside — assert it in a test.
- [ ] `--emit apk` always explicit, since it is the one needing a package name
      and a signature.

**v1 target is Tier 2.** An aar with a populated `res/` is named as the
dependency that was refused, not silently mis-packaged —
`classpath.Aar.HasResources()` is already the tripwire.

- [ ] Tier 3 escape hatch: accept a prebuilt `resources.arsc` and generated
      `R` classes, package them unchanged. This is what lets a real Gradle
      project substitute `mocha` for the dex-and-package step alone, and is
      the most likely first real use.

---

## 7. First APK

The v1 invariant: an APK containing a binary `AndroidManifest.xml` and one
`classes.dex` installs and launches. No resources means no `R` class.

- [ ] End-to-end test: `.java` → APK → install on an emulator → launch.
- [ ] `mocha version --verbose`: the class file and DEX ranges this binary
      reads and writes, the API levels it targets, the platforms it emits.
      The honest answer to "will my input work" without running a build.

---

## 8. `StackMapTable` — lift the 49.0 ceiling

Highest-value item on the backlog once Android ships. It unblocks modern source
on `--target jvm`: everything `javac` desugars *into* `invokedynamic` —
lambdas, method references, string concatenation, pattern switch — currently
has nowhere to go and is refused by name.

- [ ] The §4.10.1 verification type lattice, frame merging,
      `uninitializedThis` tracking, compressed frame encoding.
- [ ] Then: `invokedynamic` and `BootstrapMethods` in the encoder.
- [ ] Then: `tableswitch` / `lookupswitch` emission. Deferred because their
      four-byte padding depends on their own offset, so widening an earlier
      branch changes a switch's length and feeds back into the replay
      fixpoint.
- [ ] Fix `dup2`, `dup2_x1`, `dup2_x2`, `pop2` — currently assume one-slot
      operands, a latent `VerifyError` for code that mixes the forms. Falls
      out of §3.0's machinery.
- [ ] Raise `classpath.DefaultRelease` from 8 once the ceiling is gone. It is
      8 today *because* of the ceiling, and the cost is that `--target jvm`
      silently takes the Java 8 path of every MRJAR.

---

## 9. Native

The larger half, and the later one. ART was providing everything below the DEX;
here we provide it ourselves.

- [ ] `link` — closed world, rapid type analysis from the entry points,
      substitutions. Forced by emitting a standalone binary: no dynamic class
      loading, no proxies, no agents.
- [ ] `ir` — SSA from stack bytecode by abstract interpretation. Internal: no
      textual format, no stability promise, no consumers outside this repo.
- [ ] `target/amd64`, `target/arm64`.
- [ ] `object/elf` first — `linux/amd64` is the cheapest target: static, no
      libc, no container ceremony. Then `macho` (ad-hoc code signature) and
      `pe` (import address table).
- [ ] `rt` — the runtime, in Java, compiled by mocha. If `Unsafe` lowers to
      `MOV`/`LDR` and `java.lang.foreign.Linker` lowers to `SYSCALL`, a GC
      written in Java against `Unsafe` *is* a GC over raw memory. Only the
      entry stub, the metadata blobs and the unwind tables aren't Java.
- [ ] Oracle: run the binary.

---

## Not doing

Unchanged from the README, restated so the list is in one place: a public IR
with a textual format; Maven/Gradle dependency resolution; a bytecode verifier;
optimisation levels; resource compilation (aapt2, ARSC); APK Signature Scheme
v3/v4; App Bundles; a config file; build cache and parallel jobs; annotation
processing.

**Exploring, not committed:** an interpreter over the internal IR, so
`mocha run` works with no runtime installed. Open questions are the maintenance
cost and how far it gets before the missing class library becomes the blocker.