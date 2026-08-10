# mvn

`package mvn` resolves Maven dependency graphs, parses POMs, and fetches artifacts from Maven Central and Google Maven — replacing `mvn`, `gradle`, and `coursier` with pure Go.

```go
import "github.com/vertex-language/mocha/mvn"

```

```bash
go get github.com/vertex-language/mocha/mvn

```

The package is named `mvn` because it implements *their* ecosystem, not a bespoke one. It navigates an existing universe of 100,000+ packages, enforcing Maven's exact conflict resolution rules, POM inheritance mechanics, and property interpolation logic. It turns coordinates into an immutable, fully resolved dependency graph (`*mvn.Graph`) that feeds directly into Mocha's `classpath` package.

---

## Invariants

**Zero external binary execution.** No `mvn`, `gradle`, `cs`, or JDK binaries are spawned. POM XML parsing, version resolution, network fetches over HTTP/2, and SHA-1/MD5 checksum validation are implemented entirely natively in Go.

**Strict ecosystem compliance.** Resolution produces a byte-identical dependency tree and classpath ordering to what Apache Maven would produce. If Maven drops a transitive dependency due to nearest-wins depth resolution, this package does exactly the same.

**Thread-safe cache and resolution.** Network fetching and POM parsing run concurrently. The local disk cache (`$MOCHA_HOME/mvn`) utilizes atomic file locks to prevent partial writes across concurrent Go routines or compiler invocations.

---

## Usage

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/vertex-language/mocha/mvn"
)

func main() {
	ctx := context.Background()

	// 1. Local disk cache & remote repository client setup
	cache := mvn.NewCache(os.ExpandEnv("$HOME/.mocha/mvn"))
	fetcher := mvn.NewFetcher(cache, mvn.DefaultRepositories...)

	// 2. Define root coordinates for the graph
	roots := []mvn.Coordinate{
		mvn.MustParseCoordinate("com.squareup.okhttp3:okhttp:4.12.0"),
		mvn.MustParseCoordinate("com.google.code.gson:gson:2.10.1"),
	}

	// 3. Resolve transitive dependency DAG
	resolver := mvn.NewResolver(fetcher)
	graph, diags, err := resolver.Resolve(ctx, roots, mvn.ResolveOptions{
		Scopes: []mvn.Scope{mvn.ScopeCompile, mvn.ScopeRuntime},
	})
	if err != nil {
		panic(err)
	}

	for _, d := range diags {
		fmt.Printf("[%s] %s\n", d.Severity, d.Msg)
	}

	// 4. Download / materialize JAR and AAR files to the local cache
	artifacts, err := fetcher.FetchArtifacts(ctx, graph.Nodes())
	if err != nil {
		panic(err)
	}

	for _, art := range artifacts {
		fmt.Printf("%s -> %s (%d bytes)\n", art.Coord, art.Path, art.Size)
	}

	// 5. Export ordered classpath slice for Mocha's compiler frontend
	classpath := graph.Classpath()
	fmt.Printf("Resolved %d classpath entries\n", len(classpath))
}

```

---

## Core Types

### `Coordinate` — dependency identifier

Represents a standard Maven coordinate tuple `groupId:artifactId:version[:packaging[:classifier]]`.

```go
type Coordinate struct {
	Group      string
	Artifact   string
	Version    string
	Packaging  string // "jar" (default), "aar", "pom"
	Classifier string // "", "sources", etc.
}

func ParseCoordinate(raw string) (Coordinate, error)
func MustParseCoordinate(raw string) Coordinate
func (c Coordinate) String() string

```

### `POM` — raw and effective Project Object Model

`ParsePOM(r io.Reader) (*POM, error)` decodes `pom.xml` XML structures into Go types. It explicitly handles `<dependencyManagement>`, `<properties>` interpolation (e.g., `${project.version}`), `<exclusions>`, and parent POM inheritance (`<parent>`).

```go
type POM struct {
	Coord        Coordinate
	Parent       *Coordinate
	Properties   map[string]string
	Dependencies []Dependency
	Management   []Dependency
	Repositories []Repository
}

```

### `Resolver` & `Graph` — dependency resolution

`Resolver` walks the directed acyclic graph (DAG) of transitive dependencies, applying Maven conflict resolution rules.

| Type | Purpose |
| --- | --- |
| `Resolver` | Coordinates POM fetching, parent XML traversal, and transitive node discovery |
| `Graph` | Immutable directed acyclic graph of resolved dependencies |
| `Node` | A single node in the graph holding a `Coordinate`, its `*POM`, and resolved child edges |
| `Fetcher` | Handles HTTP/2 downloads, repository failover, and local cache reads |

```go
type ResolveOptions struct {
	Scopes       []Scope      // Compile, Runtime, Provided, Test
	Exclusions   []Exclusion  // groupId:artifactId rules to drop
	Overrides    []Coordinate // Force specific versions regardless of graph depth
}

```

---

## Resolution Semantics

* **Conflict Resolution:** Nearest-wins depth-first evaluation. When the same `groupId:artifactId` appears at equal depths with different versions, the first encountered version wins unless overridden by a top-level dependency or `<dependencyManagement>` entry.
* **AAR Handling:** Coordinates with `packaging="aar"` are preserved as AAR nodes in the graph. The fetcher retrieves the `.aar` archive, leaving the `.aar` unpacking and `classes.jar` extraction to downstream consumers like `classpath`.
* **Property Interpolation:** POM properties (`${java.version}`, custom properties, parent properties) are dynamically evaluated during tree construction *before* dependency version matching occurs.

---

## Relationship to Other Packages

* **[`classpath`](https://www.google.com/search?q=../classpath)**: Receives the resolved file paths from `Graph.Classpath()` to provide binary-to-bytes lookup for `sym` and `analyzer`.
* **[`manifest`](https://www.google.com/search?q=../manifest)**: Consumes extracted `.aar` `AndroidManifest.xml` files resolved by `mvn` for Android manifest merging.
* **[`sdk`](https://www.google.com/search?q=../sdk)**: Shares `mvn.Fetcher` infrastructure when fetching platform stubs (`android.jar`) from Google Maven or Google repository indices.