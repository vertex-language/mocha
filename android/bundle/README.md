# bundle

`package bundle` assembles, aligns, and signs `.apk` packages from DEX bytecode, binary XML, and resources — replacing `zipalign`, `apksigner`, and `aapt` packaging with pure Go.

```go
import "github.com/vertex-language/mocha/android/bundle"
import "github.com/vertex-language/mocha/android/bundle/axml"

```

```bash
go get github.com/vertex-language/mocha/android/bundle

```

`bundle` takes the outputs of upstream compiler passes (`classes.dex`, merged `AndroidManifest.xml`, optional `resources.arsc` and raw assets), formats them into an aligned ZIP container, and embeds an APK Signature Scheme v2 block.

---

## Package Hierarchy

```text
bundle/
├── axml/                # Text/DOM XML → Android Binary XML (AXML)
└── (root)               # ZIP container construction, 4-byte alignment, APK v2 signing

```

---

## Invariants

**Zero external tool dependencies.** No `zipalign`, `apksigner`, `keytool`, or Android SDK binaries are executed. ZIP structure generation, byte alignment, and cryptographic signing are implemented natively in Go.

**4-byte ZIP alignment.** Uncompressed entries (such as `resources.arsc` or uncompressed raw assets) are aligned to 4-byte boundaries within the ZIP archive to enable zero-copy memory mapping (`mmap`) by the Android runtime (ART).

**APK Signature Scheme v2.** Packages are signed using APK Signature Scheme v2. The signature block is injected into the ZIP file directly ahead of the Central Directory, signing all three ZIP sections (Contents, Signing Block, Central Directory).

**Deterministic output.** Given identical inputs and signing keys, `bundle` generates byte-identical `.apk` files by normalizing ZIP timestamps, entry attributes, and string pools.

---

## Usage

```go
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"os"

	"github.com/vertex-language/mocha/android/bundle"
	"github.com/vertex-language/mocha/android/bundle/axml"
	"github.com/vertex-language/mocha/android/manifest"
)

func main() {
	// 1. Encode merged AndroidManifest.xml DOM to binary AXML
	appManifest := getMergedManifest() // *manifest.Manifest
	axmlBytes, err := axml.Encode(appManifest.Root)
	if err != nil {
		panic(err)
	}

	// 2. Prepare the APK Builder
	b := bundle.NewBuilder()

	// Add binary manifest (mandatory)
	b.SetManifest(axmlBytes)

	// Add primary classes.dex and multidex entries
	dexBytes, _ := os.ReadFile("build/classes.dex")
	b.AddDex("classes.dex", dexBytes)

	// Add optional uncompressed resources.arsc
	if resBytes, err := os.ReadFile("build/resources.arsc"); err == nil {
		b.SetResources(resBytes)
	}

	// Add raw assets / AAR resources
	b.AddAsset("assets/config.json", []byte(`{"env":"prod"}`))

	// 3. Configure Signing (APK Signature Scheme v2)
	privateKey, cert := loadOrGenerateDebugKey()
	signer, err := bundle.NewSigner(privateKey, cert)
	if err != nil {
		panic(err)
	}

	// 4. Assemble, align, and sign the final APK
	apkFile, err := os.Create("out/app.apk")
	if err != nil {
		panic(err)
	}
	defer apkFile.Close()

	if err := b.Build(apkFile, signer); err != nil {
		panic(err)
	}
}

```

---

## Core Submodules & Packages

### `bundle/axml` — Binary XML Encoder

Turns text XML trees (such as those produced by `package manifest`) into Android Binary XML (AXML) format.

```go
package axml

// Encode converts an XML node tree into binary AXML bytes.
func Encode(root *manifest.Node) ([]byte, error)

```

**Encoding Mechanics:**

* Builds the global string pool, deduplicating element names, attribute names, and string constants.
* Maps framework attribute names (`android:name`, `android:icon`, etc.) to their known Android framework resource IDs (`0x01010003`, etc.).
* Serializes `XML_START_NAMESPACE`, `XML_START_ELEMENT`, `XML_END_ELEMENT`, and `XML_END_NAMESPACE` chunk structures.

---

### `bundle` (Root) — Packaging & Signing

#### `Builder` — Package Construction

Manages ZIP entries, applies entry compression rules, and computes byte alignments.

```go
type Builder struct {
	// ... unexported fields
}

func NewBuilder() *Builder
func (b *Builder) SetManifest(axmlData []byte)
func (b *Builder) AddDex(name string, dexData []byte)
func (b *Builder) SetResources(arscData []byte)
func (b *Builder) AddAsset(path string, data []byte)
func (b *Builder) Build(w io.Writer, signer *Signer) error

```

#### `Signer` — APK Signature Scheme v2

Implements APK Signature Scheme v2 hashing, signer block formatting, and cryptographic signing.

```go
type Signer struct {
	PrivateKey crypto.PrivateKey
	Certificates []*x509.Certificate
}

func NewSigner(key crypto.PrivateKey, certs []*x509.Certificate) (*Signer, error)
func NewDebugSigner() (*Signer, error) // Generates standard Android debug key/cert pair

```

---

## Layout of an APK Produced by `bundle`

```text
+--------------------------------------------------+
| 1. ZIP Local File Entries                        |
|    - AndroidManifest.xml (compressed or AXML)    |
|    - classes.dex (compressed)                    |
|    - resources.arsc (stored / 4-byte aligned)    |
|    - assets/...                                  |
+--------------------------------------------------+
| 2. APK Signing Block (v2 Signature)              |
|    - ID 0x7109871a (APK Signature Scheme v2)     |
|    - Signed Digests, Certificates, Attributes    |
+--------------------------------------------------+
| 3. ZIP Central Directory                         |
|    - References offsets in Section 1             |
+--------------------------------------------------+
| 4. ZIP End of Central Directory (EOCD)           |
|    - Contains offset & size of Central Directory |
+--------------------------------------------------+

```

---

## Relationship to Other Packages

* **[`manifest`](https://www.google.com/search?q=../manifest)**: Provides the merged `*manifest.Manifest` XML tree that `bundle/axml` converts into binary XML.


* **[`target/dalvik`](https://www.google.com/search?q=../target/dalvik)**: Generates the `classes.dex` (and multidex `classes2.dex`) byte slices passed to `Builder.AddDex()`.


* **[`mvn`](https://www.google.com/search?q=../mvn)** / **[`gradle`](https://www.google.com/search?q=../gradle)**: Supplies extracted assets, raw resources, and prebuilt `resources.arsc` chunks from dependencies for inclusion in the final bundle[cite: 2, 3].