# manifest

`package manifest` merges `AndroidManifest.xml` files from application source and library AAR fragments into a single unified manifest — replacing AGP's manifest merger with pure Go.

```go
import "github.com/vertex-language/mocha/android/manifest"

```

```bash
go get github.com/vertex-language/mocha/android/manifest

```

`manifest` parses, validates, and merges XML structures. It enforces predictable, safe merging semantics, rejecting dangerous mutations (such as silent permission injection or `tools:` overrides) with explicit, attributed diagnostics.

---

## Invariants

**Zero external tool dependencies.** No Java, Android Gradle Plugin (AGP), or Gradle binaries are executed. XML parsing, attribute merging, deduplication, and reporting are implemented natively in Go.

**Zero silent permission injection.** Unlike AGP—which silently injects system permissions when a library declares a lower `targetSdkVersion`—Mocha emits a version skew diagnostic instead. Permissions appear in the final APK only if explicitly declared in source or library manifests.

**Strict refusal of `tools:` markers.** Directives like `tools:replace`, `tools:ignore`, or `tools:node="remove"` are rejected with a diagnostic naming the offending library rather than honored. Libraries cannot quietly override application attributes.

**Line-by-line attribution.** Every element and attribute in the merged result records its source manifest. A permission appearing in an APK that nobody typed is the failure mode this package exists to prevent.

---

## Usage

```go
package main

import (
	"fmt"
	"os"

	"github.com/vertex-language/mocha/android/manifest"
)

func main() {
	// 1. Parse the application's primary AndroidManifest.xml
	appFile, err := os.Open("src/main/AndroidManifest.xml")
	if err != nil {
		panic(err)
	}
	defer appFile.Close()

	appManifest, err := manifest.Parse(appFile, "src/main/AndroidManifest.xml")
	if err != nil {
		panic(err)
	}

	// 2. Initialize the merger with the primary manifest
	merger := manifest.NewMerger(appManifest, manifest.Options{
		PackageName: "com.example.myapp",
		MinSDK:      24,
		TargetSDK:   34,
	})

	// 3. Add library manifest fragments (e.g., extracted from .aar dependencies)
	libFile, err := os.Open("build/aar/okhttp/AndroidManifest.xml")
	if err == nil {
		defer libFile.Close()
		if libManifest, err := manifest.Parse(libFile, "okhttp-4.12.0.aar"); err == nil {
			merger.AddLibrary(libManifest)
		}
	}

	// 4. Perform the merge
	result := merger.Merge()

	// 5. Inspect diagnostics
	for _, diag := range result.Diagnostics {
		fmt.Printf("[%s] %s:%d: %s\n", diag.Severity, diag.Source, diag.Line, diag.Message)
	}

	if result.HasErrors() {
		os.Exit(1)
	}

	// 6. Print attribution report
	for _, entry := range result.Report.Entries {
		fmt.Printf("%s -> contributed by %s\n", entry.TargetNode, entry.SourceManifest)
	}

	// 7. Write merged XML out
	os.WriteFile("build/AndroidManifest.xml", result.Bytes(), 0o644)
}

```

---

## Core Types

### `Manifest` — parsed XML tree

Represents an in-memory DOM tree of an `AndroidManifest.xml` file, preserving node spans, line numbers, and file origins for diagnostic reporting.

```go
type Manifest struct {
	Origin   string // File path or AAR coordinate
	Package  string // Package attribute (e.g., "com.example.lib")
	Root     *Node  // Root <manifest> element
}

func Parse(r io.Reader, origin string) (*Manifest, error)

```

### `Merger` & `Options` — merge engine

`Merger` applies application overrides, validates library fragments against target SDK levels, and merges child elements.

```go
type Options struct {
	PackageName string // Application package ID (overrides manifest package if set)
	MinSDK      int    // Application minSdkVersion override
	TargetSDK   int    // Application targetSdkVersion override
}

type Merger struct {
	App  *Manifest
	Libs []*Manifest
	Opts Options
}

func NewMerger(app *Manifest, opts Options) *Merger
func (m *Merger) AddLibrary(lib *Manifest)
func (m *Merger) Merge() *Result

```

### `Result` — output, diagnostics, and attribution

Contains the final merged XML, a list of diagnostics (warnings/errors), and an attribution log detailing where every node originated.

```go
type Result struct {
	Root        *Node
	Diagnostics []Diagnostic
	Report      AttributionReport
}

func (r *Result) Bytes() []byte
func (r *Result) HasErrors() bool

```

---

## Merge Semantics

| Element / Attribute | Behavior | Rationale |
| --- | --- | --- |
| **Element Union** | Identity by `android:name` (or tag name if unique).

 | Combines `<activity>`, `<service>`, `<uses-permission>` nodes safely.

 |
| **`<uses-permission>`** | Deduplicated; `android:maxSdkVersion` is preserved verbatim.

 | Prevents library permissions from silently escalating scope on modern devices.

 |
| **`<uses-sdk>`** | App values win; library `minSdkVersion` above app's triggers an error.

 | Ensures application floor compatibility is never broken by a library.

 |
| **`android:required`** | OR-merged (`true` if any manifest requires it).

 | Hardware requirement flags retain maximum strictness.

 |
| **`tools:` Markers** | **Refused with an error** naming the library.

 | Prevents libraries from injecting AGP-specific override directives.

 |
| **Version Skew** | **Diagnosed, never injected**.

 | Never injects permissions (e.g., `READ_EXTERNAL_STORAGE`) due to SDK gaps.

 |

---

## Relationship to Other Packages

* **[`bundle/axml`](https://www.google.com/search?q=../bundle)**: Consumes the merged XML tree produced by `manifest` and encodes it into binary AXML format for inclusion in the final APK[cite: 2].
* **[`mvn`](https://www.google.com/search?q=../mvn)** / **[`gradle`](https://www.google.com/search?q=../gradle)**: Extracts `.aar` dependencies containing `AndroidManifest.xml` fragments and hands them to `manifest` for processing.