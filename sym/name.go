package sym

import (
	"strconv"
	"strings"

	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
)

// Names in this package are internal form — com/example/Foo$Inner — because
// that is what classpath, classfile and dalvik all agree on. Dotted form exists
// only for what a user reads and writes.

// Internal converts a dotted name to internal form.
func Internal(dotted string) string { return strings.ReplaceAll(dotted, ".", "/") }

// Dotted converts an internal name to the source form of its enclosing chain.
// Nested-class separators become dots too, so com/example/A$B reads as
// com.example.A.B — which is how a user wrote it, and never how the JVM did.
func Dotted(internal string) string {
	s := strings.ReplaceAll(internal, "/", ".")
	return strings.ReplaceAll(s, "$", ".")
}

// PackageOf returns the internal-form package of a binary name, or "" for the
// unnamed package.
func PackageOf(binary string) string {
	if i := strings.LastIndexByte(binary, '/'); i >= 0 {
		return binary[:i]
	}
	return ""
}

// SimpleName returns the last component of a binary name, after both the last
// '/' and the last '$'.
func SimpleName(binary string) string {
	if i := strings.LastIndexByte(binary, '/'); i >= 0 {
		binary = binary[i+1:]
	}
	if i := strings.LastIndexByte(binary, '$'); i >= 0 {
		binary = binary[i+1:]
	}
	return binary
}

// TopLevelBinary joins an internal package and a simple name.
func TopLevelBinary(pkg, simple string) string {
	if pkg == "" {
		return simple
	}
	return pkg + "/" + simple
}

// NestedBinary is §13.1's name for a member type: the enclosing binary name, a
// '$', and the simple name.
func NestedBinary(outer, simple string) string { return outer + "$" + simple }

// AnonymousBinary is the name of the n-th anonymous class of a body, numbered
// from one within its innermost enclosing class.
func AnonymousBinary(outer string, n int) string {
	return outer + "$" + strconv.Itoa(n)
}

// LocalBinary is the name of a class declared in a method body: a number, then
// the simple name, which is what keeps two local classes of the same name in
// two different methods apart.
func LocalBinary(outer string, n int, simple string) string {
	return outer + "$" + strconv.Itoa(n) + simple
}

// NameString renders a dotted ast.Name as source text.
func NameString(n *ast.Name, unit *token.File) string {
	if n == nil || len(n.Parts) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, p := range n.Parts {
		if i > 0 {
			sb.WriteByte('.')
		}
		sb.WriteString(p.Name(unit))
	}
	return sb.String()
}

// identName returns an identifier's spelling, or "_" for the unnamed form.
func identName(id *ast.Ident, unit *token.File) string {
	if id == nil {
		return ""
	}
	if id.Underscore {
		return "_"
	}
	return id.Name(unit)
}