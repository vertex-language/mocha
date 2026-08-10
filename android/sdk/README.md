# sdk

`package sdk` fetches and caches Android platform stubs.

```
import "github.com/vertex-language/mocha/android/sdk"
```

```
go get github.com/vertex-language/mocha/android/sdk
```

One job: given an API level, produce a path to an `android.jar`.

The platform ZIP is downloaded, the stub is streamed out, and the rest — samples, sources, build tools, system images — is discarded. Nothing is installed, no SDK layout is created, and no environment variable is set. The result is a file path, which [`classpath`](../classpath) then treats like any other jar.

One XML parse, one ZIP read, no Maven. It never parses a POM. **Mocha does not download libraries.**

---

## Usage

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/vertex-language/mocha/android/sdk"
)

func main() {
	c, err := sdk.Open("") // MOCHA_HOME, else ~/.mocha
	if err != nil {
		log.Fatal(err)
	}

	jar, err := c.Path(24)
	if err != nil {
		log.Fatal(err) // "api 24: platform not cached (run: mocha sdk fetch 24)"
	}
	fmt.Println(jar)

	c.Progress = func(done, total int64) { /* … */ }
	jar, err = c.Fetch(context.Background(), 29, false)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(jar)
}
```

---

## `Path` never fetches

```go
func (c *Cache) Path(api int) (string, error)              // cache only
func (c *Cache) Fetch(ctx context.Context, api int, force bool) (string, error)
```

`--api 24` finds the cached `android.jar` on its own, and if it is not there the build fails with a diagnostic naming the command that fixes it. It does not quietly reach the network.

A compiler that opens a socket because a flag named a level nobody had fetched is a compiler that behaves differently on a plane, in CI, and behind a proxy. The network is `mocha sdk fetch`, and it is a command a user typed.

`Fetch` is idempotent: an already-cached platform is returned untouched unless `force`.

---

## Cache layout

```text
$MOCHA_HOME/                        # default ~/.mocha
├── platforms/android-24/android.jar
├── platforms/android-24/meta.json
├── index/repository.xml
└── tmp/
```

`Open("")` resolves `MOCHA_HOME`, then falls back to `~/.mocha`. There is no config file — [there is no config file anywhere](../#environment).

**`meta.json` is written last, and that is what makes it meaningful.** The jar is renamed into place, then the record is written; so the record's *presence* is the completion marker. `Path` checks for it before the jar. An interrupted fetch leaves a directory that reads as absent rather than as a half-downloaded stub that opens and then fails somewhere inside `classfile`.

```json
{
  "api": 24,
  "revision": "2.0.0",
  "url": "https://dl.google.com/android/repository/platform-24_r02.zip",
  "sha1": "…",
  "zip_size": 76028402,
  "entry": "android-7.0/android.jar",
  "jar_size": 27594311,
  "fetched": "2026-08-10T14:21:07Z"
}
```

Every write goes through a temp file and a rename. An interrupted write cannot leave a truncated file that a later run treats as valid.

---

## The repository index

```go
func (c *Cache) List(ctx context.Context) ([]Platform, error)
func (c *Cache) Lookup(ctx context.Context, api int) (Platform, error)
```

`DefaultIndexURL` is Google's `repository2-3.xml`. A `remotePackage` looks like this:

```xml
<remotePackage path="platforms;android-23">
  <type-details xsi:type="sdk:platformDetailsType">
    <api-level>23</api-level>
    <codename></codename>
    <layoutlib api="16"/>
  </type-details>
  <revision><major>3</major></revision>
  <display-name>Android SDK Platform 23</display-name>
  <channelRef ref="channel-0"/>
  <archives>
    <archive>
      <complete>
        <size>70433421</size>
        <checksum>027fede3de6aa1649115bbd0bffff30ccd51c9a0</checksum>
        <url>platform-23_r03.zip</url>
      </complete>
    </archive>
  </archives>
</remotePackage>
```

`<url>` is relative to the index's own directory and is resolved against it.

The index is cached under `$MOCHA_HOME/index/` for `IndexMaxAge` (default 24 hours). It changes when Google ships a platform revision, which is not often.

### Two filters that are load-bearing

**A non-empty `<codename>` is a preview platform, and its `api-level` is the level *under development*.** `android-VanillaIceCream` reports `<api-level>35</api-level>` and will collide with the real API 35 package if you key on the level alone. Preview platforms are excluded.

**`channelRef` distinguishes stable from beta, dev and canary.** Only `channel-0` is accepted. Without this, `sdk fetch 36` could hand you a canary stub whose signatures do not match anything that has shipped.

Where the index carries more than one package for a level, the highest revision wins.

### Namespaces are deliberately not bound

The repository namespace is versioned — `…/repository2/01` through `/03` — so binding it in the struct tags would make the parser fail on the next index revision for no benefit. Element names are matched by local name only.

The same reasoning applies to `xsi:type`. Its value is a QName whose prefix the document is free to choose, so a platform package is recognised by its `path` and its `api-level`, not by a string that says `sdk:platformDetailsType` today.

---

## Trust

Google's repository index is served over HTTPS and is **unsigned**, and the checksums it carries are **SHA-1**.

Verification here therefore detects a truncated or corrupted download. It does not detect a hostile one. The trust anchor is TLS to `dl.google.com`; treating the SHA-1 as a security control would be a misreading of what it is, and this package does not pretend otherwise. Both the declared size and the checksum are checked, and a mismatch on either aborts the fetch.

---

## Extraction

```
platform-24_r02.zip  →  android-7.0/android.jar  →  platforms/android-24/android.jar
```

The archive streams to a temp file under `$MOCHA_HOME/tmp`, hashed on the way past, and is then opened with `archive/zip` and the one entry copied out. The ZIP is removed on both the success and the failure path.

Streaming to disk rather than to memory is the whole reason for the temp file: `archive/zip` needs a `ReaderAt`, and a platform ZIP is 70–150 MB. Ranged requests would let us pull only the stub — roughly half the transfer — but that means a hand-rolled end-of-central-directory scan and local header parse, which belongs behind a working v1 rather than in front of it.

**The entry is found by base name at depth two, not by a constructed path.** The archive's top-level directory is named for the platform *version*, not its API level — `android-6.0` for API 23, `android-10` for API 29 — and that naming has changed more than once. An ambiguous match (two `android.jar` entries) is an error rather than a guess.

Peak disk during a fetch is the ZIP plus the extracted jar, so budget around 150 MB. A killed process leaves the ZIP behind in `tmp/`.

---

## Commands

```bash
mocha sdk list
mocha sdk fetch 24
mocha sdk path 24
```

Three subcommands under one of the [five commands](../#commands). `list` shows what is cached and what the index offers; `fetch` downloads; `path` prints the cached location or fails.

---

## What this package deliberately does not do

| Not doing | Because |
| --- | --- |
| Resolve library coordinates | `classpath` takes paths; Maven, Gradle and Coursier already produce them |
| Parse a POM | there is nothing here that needs one |
| Install build-tools, platform-tools, emulator images | mocha does not shell out to `d8`, `aapt2` or `adb` |
| Accept the SDK licence on your behalf | fetching a stub is not agreeing to terms; that is between the user and Google |
| Read an existing `ANDROID_HOME` | one cache, one layout, and no inherited state to diagnose |
| Verify signatures on the index | Google does not sign it |
| Auto-fetch during a build | see [`Path` never fetches](#path-never-fetches) |

---

## Relationship to the other packages

`sdk` produces a file path. [`classpath`](../classpath) adds it with role `Lib`, which is what keeps `android.jar`'s code out of the artifact while its signatures remain visible to resolution — ART supplies the implementation on device.

`sdk` imports only the standard library, and nothing in mocha imports `sdk` except `cmd/mocha`.