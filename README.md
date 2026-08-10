# ☕ Mocha

**A Java toolchain for Android, the JVM, and native binaries — one static Go binary.**

No JDK. No Gradle or Maven. No Android Studio or SDK binaries. Mocha handles dependency resolution, compilation, dexing, manifest merging, APK bundling, signing, and device deployment natively in Go. 

```bash
mocha build app.jar --target android --api 24 --emit apk -o out/

```

> **Status: early development.** `classfile` — the class file reader and writer everything
> else stands on — is done. The Java frontend and the DEX emitter are in progress; the
> native path is behind them. See [Roadmap](https://www.google.com/search?q=%23roadmap) before depending on this.

| Target | Emits | Notes |
| --- | --- | --- |
| `android` | `dex`, `apk` | The flagship path. Ships first. |
| `jvm` | `class` | Fastest loop. Capped at class file 49.0. |
| `native` | `exe` | `linux/amd64`, `darwin/arm64`, `windows/amd64`. Furthest out. |

---

## Quick start

```bash
GOPROXY=direct go install [github.com/vertex-language/mocha/cmd/mocha@latest](https://github.com/vertex-language/mocha/cmd/mocha@latest)

```

Or grab a prebuilt binary from [Releases](https://www.google.com/search?q=https://github.com/vertex-language/mocha/releases).
Single static binary, no runtime dependencies.

### Real Java, a real library, zero setup

An HTTP GET with OkHttp — ordinary Java, an ordinary third-party jar, absolutely no build system required.

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
                .url("[https://example.com/](https://example.com/)")
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

# 2. Build the signed APK. Mocha resolves the POM, downloads the JARs, 
# compiles the Java, and bundles the APK automatically.
mocha build Fetch.java \
  --dep com.squareup.okhttp3:okhttp:4.12.0 \
  --target android --api 24 --emit apk -o build/
# → build/app.apk

```

That is the whole toolchain. No `javac`, no `d8`, no `aapt2`, no `apksigner`. `--api 24` finds the cached `android.jar` on its own, and Mocha's native resolver handles the transitive dependencies.

### Where a dependency goes decides what happens to it

| How you pass it | Compiled | In the artifact | For |
| --- | --- | --- | --- |
| `<input>` | yes | **yes** | Local source files and local library jars you want shipped |
| `--dep` | yes | **yes** | Remote Maven coordinates; downloaded, resolved, and shipped |
| `--classpath` | no | no | Local jars used for signatures only (compilation resolution) |
| `--lib` (implied by `--api`) | no | **never** | `android.jar`; ART supplies the implementation on device |

---

## Architecture

```
  .java ──→ scanner → parser → sym → attr → flow → desugar → gen ──┐
                                ↑                                  │
  mvn / gradle ─────────────────┘  (jars, aars)                    │
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

Above the waist we're a Java compiler and dependency resolver. Below it, nothing knows Java exists.

**Governing rule: do what `javac` does; deviate only where the target forces it.** The frontend follows `javac`'s phase order.

**Dependency rule, one direction only.** `classfile` imports `jvm/*` and the standard library. `target/*` and `ir` import `classfile`. Nothing below the waist imports anything above it.

---

## Commands

| Command | Does |
| --- | --- |
| `mocha build` | Resolve dependencies, compile, and package |
| `mocha deploy` | Build, package, and stream directly to a connected device via ADB |
| `mocha check` | Analyse only, emit nothing (fast type-checking) |
| `mocha run` | Build and execute (JVM and Native targets only) |
| `mocha sdk` | Fetch and manage Android platform stubs from Google |
| `mocha version` | Version, and what this binary can emit |

---

## Layout

```text
[github.com/vertex-language/mocha/](https://github.com/vertex-language/mocha/)
├── cmd/mocha/
│
├── android/             # Android-specific ecosystem
│   ├── adb/             # device discovery, socket transport, streaming install
│   ├── bundle/          # dex + axml + align + v2 sign → apk
│   │   └── axml/        # binary XML encoder
│   ├── manifest/        # AndroidManifest merge, diagnostics, report
│   └── sdk/             # platform stub fetch and cache
│
├── classfile/           # the waist — read and write .class      ← done
├── jvm/{op,desc,mutf8}/ # opcodes, descriptors, modified UTF-8   ← leaves
├── classpath/           # binary name → bytes; jars, aars, dirs, MR jars
│
├── gradle/              # Universal Gradle module and catalog parsing
├── mvn/                 # Universal Maven POM resolution and graph solving
│
├── token/ scanner/ ast/ parser/
├── sym/ types/ analyzer/{attr,flow,warn}/ desugar/ gen/
│
├── ir/                  # SSA from bytecode — deferred until needed
├── link/                # closed world, reachability, substitutions
│
├── target/              # Target instruction sets
│   ├── amd64/
│   ├── arm64/
│   └── dalvik/          # classfile → dex
│
├── object/              # Native executable containers
│   ├── elf/             # Linux
│   ├── macho/           # macOS
│   └── pe/              # Windows
│
└── rt/                  # the Java-source runtime

```

## License

MIT