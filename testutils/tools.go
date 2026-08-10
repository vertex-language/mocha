package testutils

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Compile writes the given sources into a temporary tree and compiles them,
// returning the output directory.
//
// Keys are paths relative to the source root, e.g. "com/example/Main.java".
// javac is left on its default target: this package's callers diff the
// disassembly, which -c prints without a version, so pinning --release would
// only make the harness fail on JDKs that no longer support the value.
func (j *JDK) Compile(t testing.TB, sources map[string]string) string {
	t.Helper()

	root := t.TempDir()
	src := filepath.Join(root, "src")
	out := filepath.Join(root, "ref")
	mustMkdir(t, out)

	var paths []string
	for rel, body := range sources {
		p := filepath.Join(src, filepath.FromSlash(rel))
		mustMkdir(t, filepath.Dir(p))
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", rel, err)
		}
		paths = append(paths, p)
	}

	args := append([]string{"-d", out, "-encoding", "UTF-8", "-g:none"}, paths...)
	if _, err := run(context.Background(), "", j.Javac, args...); err != nil {
		t.Fatalf("javac: %v", err)
	}
	return out
}

// Disassemble runs javap -c -p over one class file.
//
// -c, never -v. Constant pool ordering is unspecified and mocha's interning
// order will not match javac's, so a -v diff drowns in renumbered #N
// references. The disassembly is the thing that should agree.
func (j *JDK) Disassemble(t testing.TB, classFile string) string {
	t.Helper()
	out, err := run(context.Background(), "", j.Javap, "-c", "-p", classFile)
	if err != nil {
		t.Fatalf("javap: %v", err)
	}
	return out
}

// Verbose runs javap -v -p, for the few facts -c does not print. Sizes is the
// intended consumer; do not diff this output directly.
func (j *JDK) Verbose(t testing.TB, classFile string) string {
	t.Helper()
	out, err := run(context.Background(), "", j.Javap, "-v", "-p", classFile)
	if err != nil {
		t.Fatalf("javap -v: %v", err)
	}
	return out
}

// Run loads and executes a class, returning its combined output. Loading is
// what runs the verifier — there is no separate verify command — so a
// successful call is the strongest check available.
//
// -Xverify:all is passed when the JDK still accepts it, to verify bootstrap
// classes too. It was deprecated in JDK 13 and is gone in 27, so its absence
// is not an error: ordinary loading already verifies everything mocha emits.
func (j *JDK) Run(t testing.TB, classpath, mainClass string, args ...string) (string, error) {
	t.Helper()

	base := []string{"-cp", classpath, mainClass}
	if j.Release > 0 && j.Release < 27 {
		out, err := run(context.Background(), "",
			j.Java, append([]string{"-Xverify:all"}, append(base, args...)...)...)
		if !strings.Contains(out, "Unrecognized option") {
			return out, err
		}
	}
	return run(context.Background(), "", j.Java, append(base, args...)...)
}

// WriteClass places bytes at the path a classpath root implies for a binary
// name, creating directories as needed, and returns the root.
//
// The layout matters: a class written to Main.class in the working directory
// will not be found under its package name, and the resulting
// NoClassDefFoundError says nothing useful about the bytes.
func WriteClass(t testing.TB, root, binary string, data []byte) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(binary)+".class")
	mustMkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("writing %s: %v", p, err)
	}
	return p
}

func mustMkdir(t testing.TB, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}