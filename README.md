# ☕ Mocha

**A Java toolchain for Android, the JVM, and native binaries — one static Go binary.**

No JDK. No Gradle or Maven. No Android Studio or SDK binaries. Mocha handles
dependency resolution, compilation, dexing, manifest merging, APK bundling,
signing, and device deployment natively in Go.

```bash
mocha build Fetch.java --target android --api 24 --emit apk -o out/
```

> **Status: early development.** `classfile` — the class file reader and writer
> everything else stands on — is done, as is the frontend through analysis. Code
> generation and the DEX path are in progress; the native path is behind them.
> See [Roadmap](#roadmap) before depending on this.

| Target | Emits | Notes |
| --- | --- | --- |
| `android` | `dex`, `apk` | The flagship path. Ships first. |
| `jvm` | `class` | Fastest loop. Capped at class file 49.0. |
| `native` | `exe` | `linux/amd64`, `darwin/arm64`, `windows/amd64`. Furthest out. |

---

## Quick start

```bash
GOPROXY=direct go install github.com/vertex-language/mocha/cmd/mocha@latest
```

Or grab a prebuilt binary from
[Releases](https://github.com/vertex-language/mocha/releases).
Single static binary, no runtime dependencies.

### Real Java, a real library, zero setup

An HTTP GET with OkHttp — ordinary Java, an ordinary third-party jar, no build
system.

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

Fetch the platform stub once, pass the Maven coordinate directly, and build:

```bash
# 1. Cache the Android platform stub natively
mocha sdk fetch 24

# 2. Build the signed APK. Mocha resolves the POM, downloads the jars,
#    compiles the Java, and bundles the APK.
mocha build Fetch.java \
  --dep com.squareup.okhttp3:okhttp:4.12.0 \
  --target android --api 24 --emit apk -o build/
# → build/app.apk
```

That is the whole toolchain. No `javac`, no `d8`, no `aapt2`, no `apksigner`.
`--api 24` finds the cached `android.jar` on its own, and Mocha's native resolver
handles the transitive dependencies.

### Where a dependency goes decides what happens to it

| How you pass it | Compiled | In the artifact | For |
| --- | --- | --- | --- |
| `<input>` | yes | **yes** | Local source files and local library jars you want shipped |
| `--dep` | yes | **yes** | Remote Maven coordinates; downloaded, resolved, and shipped |
| `--classpath` | no | no | Local jars used for signatures only |
| `--lib` (implied by `--api`) | no | **never** | `android.jar`; ART supplies the implementation on device |

---

## Architecture

```
  .java ──→ scanner → parser → sym → types → attr → flow → warn → lower ──┐
                                ↑                                          │
  mvn / gradle ─────────────────┘  (jars, aars)                            │
  .class / .jar / .aar ────────────────────────────────────────────────────┤
                                                                           ↓
                          ══════════════ .class ══════════════      ← the waist
                                              ↓
                                          ir/builder                  SSA construction
                                              ↓
                                             ir                       one SSA form
                            ┌─────────────────┼─────────────────┐
                            ↓                 ↓                 ↓
                     target/dalvik     target/amd64      target/arm64
                     regalloc → dex    regalloc → asm    regalloc → asm
                            ↓                 ↓                 ↓
                        dexfile            object (elf / macho / pe)
                            ↓                       ↓
   AndroidManifest.xml ──→ manifest → bundle     executable
   [resources.arsc]  ───────────────→   ↓
                                      .apk
```

Above the waist we're a Java compiler and dependency resolver. Below it, nothing
knows Java exists — `target/dalvik` no more than a JVM does.

**Governing rule: do what `javac` does; deviate only where the target forces
it.** The frontend follows `javac`'s phase order. Where it doesn't follow
`javac`'s data structures, the package README says why.

**One SSA form, one per-target register allocator.** Dex instruction operands
are mostly four bits wide, so allocation is not an optimisation on that path —
it is most of what makes the output compact. This is d8's arrangement, and the
reason `ir` is on the critical path to the flagship target rather than behind it.

**Dependency rule, one direction only.** `classfile` and `dexfile` import
`jvm/*`, `dalvik/*` and the standard library. `ir` imports `classfile`;
`target/*` imports `ir`. Nothing below the waist imports anything above it, and
nothing imports `lower`.

---

## Commands

| Command | Does |
| --- | --- |
| `mocha build` | Resolve dependencies, compile, and package |
| `mocha deploy` | Build, package, and stream directly to a connected device via ADB |
| `mocha check` | Analyse only, emit nothing (fast type-checking) |
| `mocha run` | Build and execute (JVM and native targets only) |
| `mocha sdk` | Fetch and manage Android platform stubs from Google |
| `mocha version` | Version, and what this binary can emit |

---

## Layout

```text
github.com/vertex-language/mocha/
├── cmd/mocha/
│
├── android/              # Android-specific ecosystem
│   ├── adb/              # device discovery, socket transport, streaming install
│   ├── bundle/           # dex + axml + align + v2 sign → apk
│   │   └── axml/         # binary XML encoder
│   ├── manifest/         # AndroidManifest merge, diagnostics, report
│   └── sdk/              # platform stub fetch and cache
│
├── classfile/            # the waist — read and write .class          ← done
├── dexfile/              # read and write .dex — the format, nothing else
├── jvm/
│   ├── op/               # JVM opcode constants and operand shapes
│   ├── desc/             # field and method descriptors   ┐ shared with
│   └── mutf8/            # modified UTF-8 codec            ┘ dexfile
├── dalvik/
│   └── op/               # Dalvik opcode constants and instruction formats
├── classpath/            # binary name → bytes; jars, aars, dirs, MR jars
│
├── gradle/               # Gradle module and version catalog parsing
├── mvn/                  # Maven POM resolution and graph solving
│
├── token/                # lexical vocabulary, position space
├── scanner/              # source → tokens
├── ast/                  # syntax tree
├── parser/               # tokens → tree
├── sym/                  # symbol table
├── types/                # type model, erasure
├── analyzer/
│   ├── attr/             # type-check, resolve
│   ├── flow/             # assignment, reachability, capture
│   └── warn/             # diagnostics beyond errors
├── lower/                # attributed tree → classfile.Builder
│
├── ir/                   # the SSA form: values, blocks, phis
│   └── builder/          # .class → SSA (Braun et al. construction)
├── link/                 # closed world, reachability, substitutions
│
├── target/               # instruction selection and register allocation
│   ├── dalvik/           # ir → dex instructions
│   ├── amd64/
│   └── arm64/
│
├── object/               # native executable containers
│   ├── elf/              # Linux
│   ├── macho/            # macOS
│   └── pe/               # Windows
│
└── rt/                   # the Java-source runtime
```

## License

MIT