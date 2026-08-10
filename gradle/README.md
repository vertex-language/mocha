# gradle

`package gradle` parses Gradle project structures, version catalogs, and module metadata — replacing the Gradle daemon and JVM build evaluation with static Go analysis.

```go
import "github.com/vertex-language/mocha/gradle"

```

```bash
go get github.com/vertex-language/mocha/gradle

```

Because Gradle relies on Turing-complete Kotlin (`.gradle.kts`) and Groovy (`.gradle`) DSLs, Mocha does not execute build scripts. Instead, `package gradle` statically analyzes the declarative subset of Gradle's ecosystem—extracting multi-module hierarchies, source sets, Android variants, and `libs.versions.toml` catalogs natively, translating them directly into Mocha build inputs.

---

## Invariants

**Zero JVM or Gradle Daemon execution.** No `java`, `gradlew`, or Kotlin compiler processes are spawned. Build files are parsed as syntax trees to extract declarative blocks (`dependencies {}`, `android {}`), intentionally ignoring dynamic, imperative build logic.

**Source Set Variant Resolution.** The package maps Android product flavors and build types directly to disk paths (e.g., resolving the `freeDebug` variant to `src/main`, `src/free`, and `src/debug`), matching the exact directory fallback order of the Android Gradle Plugin (AGP).

**Module Metadata Native Understanding.** Parses Gradle Module Metadata (`.module` JSON, spec v1.1) to support rich version constraints, feature variants, and platform alignments without falling back to Maven POMs.

---

## Usage

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/vertex-language/mocha/gradle"
	"github.com/vertex-language/mocha/mvn"
)

func main() {
	// 1. Load the multi-module workspace root (settings.gradle.kts)
	workspace, err := gradle.LoadWorkspace(os.ExpandEnv("$HOME/projects/my-app"))
	if err != nil {
		panic(err)
	}

	// 2. Parse the Version Catalog (libs.versions.toml) if present
	catalog := workspace.Catalog()
	fmt.Printf("Loaded %d libraries from version catalog\n", len(catalog.Libraries))

	// 3. Resolve a specific project (e.g., ":app") and variant ("debug")
	project, err := workspace.Project(":app")
	if err != nil {
		panic(err)
	}

	variant, err := project.Variant("debug")
	if err != nil {
		panic(err)
	}

	// 4. Extract source roots and resolved dependency coordinates
	fmt.Println("Source roots to compile:")
	for _, srcDir := range variant.SourceSets {
		fmt.Printf(" - %s\n", srcDir)
	}

	fmt.Println("\nRequired Dependencies:")
	var roots []mvn.Coordinate
	for _, dep := range variant.Dependencies {
		coord := mvn.MustParseCoordinate(dep.GAV())
		roots = append(roots, coord)
		fmt.Printf(" - %s\n", coord)
	}

	// 5. These roots can now be passed directly to package mvn
	// resolver := mvn.NewResolver(fetcher)
	// graph, _, _ := resolver.Resolve(context.Background(), roots, ...)
}

```

---

## Core Types

### `Workspace` & `Project` — the build tree

`Workspace` parses `settings.gradle.kts` to discover `include(":app", ":core")` directives. `Project` analyzes a specific module's `build.gradle.kts` to extract dependencies and plugin configurations.

```go
type Workspace struct {
	RootPath string
	Projects map[string]*Project
	Catalog  *VersionCatalog
}

type Project struct {
	Name         string
	Path         string
	Plugins      []string
	Variants     map[string]*Variant
}

```

### `VersionCatalog` — dependency management

Parses `gradle/libs.versions.toml`. It maps the standard `[versions]`, `[libraries]`, `[plugins]`, and `[bundles]` TOML blocks into strict Go maps, resolving alias chains safely.

```go
type VersionCatalog struct {
	Versions  map[string]string
	Libraries map[string]LibraryRef
	Bundles   map[string][]LibraryRef
	Plugins   map[string]PluginRef
}

type LibraryRef struct {
	Alias   string
	Group   string
	Name    string
	Version string // Resolved from [versions] block if referenced
}

```

### `Variant` & `SourceSet` — Android configuration

Represents the intersection of build types and product flavors. A `Variant` holds the exact list of dependency coordinates required for that specific build, along with the ordered list of local directories holding `.java` and `res/` files.

```go
type Variant struct {
	Name         string       // e.g., "demoDebug"
	Dependencies []Dependency // Merges `implementation` and `demoDebugImplementation`
	SourceSets   []string     // [ "src/main/java", "src/demo/java", "src/debug/java" ]
	ManifestPath string       // e.g., "src/main/AndroidManifest.xml"
}

```

### `ModuleMetadata` — `.module` JSON parser

Decodes Gradle Module Metadata (`.module`) files published alongside or instead of POMs. This is critical for supporting Kotlin Multiplatform (KMP) artifacts, where Gradle uses `.module` attributes to map a generic dependency coordinate to the platform-specific JAR.

```go
type ModuleMetadata struct {
	FormatVersion string
	Component     ComponentInfo
	Variants      []MetadataVariant
}

```

---

## Resolution Semantics

* **Declarative Extraction:** Mocha's Gradle parser ignores closures, variables, and loops inside build scripts. It uses an AST visitor to specifically target top-level block calls (`dependencies { ... }`, `android { ... }`) and extracts literal strings or version catalog accessors (e.g., `libs.androidx.core`).
* **Dependency Merging:** When evaluating a `Variant` named `release`, dependencies declared in `implementation`, `api`, and `releaseImplementation` are unioned. `testImplementation` dependencies are strictly excluded unless the variant represents a test run.
* **Catalog Injection:** When `build.gradle.kts` uses a type-safe accessor like `implementation(libs.okhttp)`, `package gradle` performs a lookup against the parsed `VersionCatalog` to yield the raw `GAV` (Group-Artifact-Version) coordinate.

---

## Relationship to Other Packages

* **[`mvn`](https://www.google.com/search?q=../mvn)**: Consumes the raw `mvn.Coordinate` arrays extracted from Gradle configurations by `Project.Variant()`, fetching the actual binaries and walking the transitive dependency tree.
* **[`manifest`](https://www.google.com/search?q=../manifest)**: Reads the local `AndroidManifest.xml` files located by the resolved `Variant.SourceSets`.
* **[`ast`](https://www.google.com/search?q=../ast)**: `gradle` borrows heavily from the architecture of Mocha's Java parser, executing structural static analysis over `.kts` files rather than interpreting them.