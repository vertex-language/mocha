# `mocha` — Multi-Architecture Java Compiler (Go)

`mocha` is a Go-based compiler that takes Java (via `.class`/bytecode) and emits native binaries for multiple OS/architecture targets — no C/C++ toolchain required. The design splits cleanly into three concerns, mirroring how VVM-style toolchains work:

- **Targets** — turn IR into raw CPU/VM instructions
- **Objects** — wrap those instructions in an OS-native executable container
- **Bundles** — package the result for deployment (e.g., signed APKs)

## Project Layout

```text
github.com/vertex-language/mocha/
├── main.go                # CLI driver
├── token/                 # (Frontend) Tokens and source mapping
├── scanner/                # (Frontend) Lexer
├── ast/                    # (Frontend) Abstract Syntax Tree
├── parser/                 # (Frontend) Source to AST
│
├── classfile/              # JVM .class file format (binary standard)
│   ├── classfile.go         # Structs: ConstantPool, Attributes, MethodInfo
│   ├── decoder.go           # .class bytes -> Go structs
│   └── encoder.go           # Go structs -> .class bytes
│
├── ir/                      # Universal IR (SSA form)
│   ├── ir.go                 # Flat, register-based graph
│   └── builder.go            # Lowers AST or classfile into IR
│
├── target/                  # Code generators (IR -> machine instructions)
│   ├── arch.go                # Common Emitter interface (EmitAdd, EmitBranch, ...)
│   ├── amd64/                 # x86_64 instruction encoder
│   ├── arm64/                 # AArch64 instruction encoder
│   └── dalvik/                # Android VM register-based bytecode encoder
│       ├── dex.go              # String pool, method IDs, type IDs
│       ├── decoder.go          # Parses existing .dex files
│       └── encoder.go          # IR -> raw classes.dex
│
├── object/                  # OS executable wrappers
│   ├── object.go              # Writer interface (WriteText, WriteData, AddSymbol)
│   ├── elf/                    # Linux (statically linked)
│   ├── pe/                     # Windows .exe (shadow space, DLL imports)
│   └── macho/                  # macOS
│       └── adhoc.go             # Apple Silicon ad-hoc code signer (required for W^X)
│
└── bundle/                  # Deployment packagers
    └── apk/                    # Android App Bundle builder
        ├── axml/                # Android Binary XML compiler
        │   ├── decoder.go        # Binary XML -> text XML
        │   └── encoder.go        # Text XML -> binary XML (AndroidManifest.xml)
        ├── zipalign.go           # 4-byte boundary alignment for ZIP offsets
        ├── v2signer.go           # APK Signature Scheme v2/v3 block injection
        └── builder.go            # archive/zip orchestrator
```

> **Note:** for the machine-code paths, only emit what the Java program actually needs — no runtime bloat.

## Pipeline Flows

**1. macOS / Apple Silicon (`darwin/arm64`)**
`classfile → ir → target/arm64 → object/macho`
Apple Silicon enforces W^X (Write XOR Execute); an unsigned `arm64` binary gets `SIGKILL`ed on launch. `object/macho/adhoc.go` must SHA-256 the binary's pages and append a `CodeDirectory` block with the `adhoc` flag before writing to disk.

**2. Android (`android/apk`)**
`classfile → ir → target/dalvik → bundle/apk`
`bundle/apk` takes the Dalvik encoder's `.dex` output, merges it with the `axml` binary manifest, 4-byte-aligns all uncompressed entries, and injects an APK Signature Scheme v2/v3(.2) block directly ahead of the ZIP Central Directory.

**3. Windows (`windows/amd64`)**
`classfile → ir → target/amd64 → object/pe`
`object/pe` builds an Import Address Table (IAT) so the Windows loader can resolve symbols like `kernel32.dll` at runtime.

## Standard-Library Interception Strategy

You don't need to write C/C++ glue — modern OpenJDK already exposes the hardware/OS boundary through two standard `java.base` APIs. Every major JVM (HotSpot, ART, GraalVM) intercepts these same APIs, so `mocha` can too.

**1. `Unsafe` — raw memory & atomics**
`jdk.internal.misc.Unsafe` (formerly `sun.misc.Unsafe`) backs `ByteBuffer`, `String`, `java.util.concurrent`, etc. `mocha`'s `target/amd64` / `target/arm64` recognize calls to it and emit instructions directly instead of a method call:

- `Unsafe.getByte(address)` → x86 `MOV AL, [RBX]` / ARM64 `LDRB W0, [X1]`
- `Unsafe.compareAndSetInt(...)` → x86 `LOCK CMPXCHG`

Because `java.base` already routes through `Unsafe` everywhere, intercepting it makes classes like `ConcurrentHashMap`, `AtomicInteger`, and `ArrayList` work immediately.

**2. `java.lang.foreign` (Project Panama) — OS/native calls**
JNI is legacy; the modern, pure-Java way to call `libc`/`kernel32.dll` is the Foreign Function & Memory API (finalized Java 22, standard in 25/26). When `mocha` sees a `Linker.downcallHandle` target, it resolves the symbol (`write`, `mmap`, `GetStdHandle`, ...) and emits a direct `CALL`/`SYSCALL` rather than a dynamic invoke.

**3. `@IntrinsicCandidate` — bit/math primitives**
OpenJDK marks methods like `Integer.numberOfLeadingZeros` with `@jdk.internal.vm.annotation.IntrinsicCandidate` — a Java fallback exists, but a smart compiler should replace it with a single instruction (`LZCNT`, `CLZ`, `POPCNT`, `BSWAP`, ...).

| Problem | Standard Java hook | What `mocha` emits |
|---|---|---|
| Raw pointers / off-heap memory | `Unsafe` | `MOV` / `LDR` |
| Hardware atomics / locks | `Unsafe.compareAndSet*` | `LOCK CMPXCHG` |
| OS syscalls / DLL calls | `java.lang.foreign.Linker` | Direct `SYSCALL` / DLL `CALL` |
| Bitwise / math primitives | `@IntrinsicCandidate` | `POPCNT`, `BSWAP`, etc. |

## Fetching the Android SDK Stub Without Google's SDK Manager

Google's SDK Manager is just a script that parses XML and pulls ZIPs from a public CDN — `mocha` can replicate this directly. Google doesn't publish `android.jar` standalone; it's bundled in ~50–60MB "Platform ZIPs."

1. **Fetch the index** — GET `https://dl.google.com/android/repository/repository2-1.xml` (or `-2`/`-3` for newer schemas) and parse with Go's `encoding/xml` for the `<remotePackage path="platforms;android-33">` block to get the archive filename (e.g. `platform-33_r02.zip`).
2. **Download the ZIP** — `https://dl.google.com/android/repository/<filename>`, cached at e.g. `~/.mocha/cache/android-33/`.
3. **Stream-extract just the jar** — use `archive/zip` to read the ZIP in memory, pull out `android.jar` only, then delete the ZIP.
4. **Compile** — point `mocha/classfile` at the cached `android.jar` to validate signatures (`WebView`, `Socket`, ...) and generate the `.dex`.

**Alternative:** third-party mirrors (e.g. Robolectric-adjacent tooling) publish stub jars to Maven Central at paths like `com/virjar/androidstub/<version>/androidstub-<version>.jar`, skipping the XML step entirely. Recommended default is still the official `dl.google.com` route — it's cryptographically authoritative and auto-tracks new API levels as Google publishes them.