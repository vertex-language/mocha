package classpath

import "strings"

// ValidBinaryName reports whether s is a binary name in internal form —
// com/example/Foo, or com/example/Foo$Inner.
//
// JVMS §4.2.2 forbids '.', ';', '[' and '/' in an unqualified name; '/' is the
// package separator here, so it is checked per component. Backslash and NUL are
// rejected on top of the specification, because a directory entry turns this
// string into a filesystem path and neither belongs there. Rejecting '.' also
// catches the common mistake of passing com.example.Foo or Foo.class.
func ValidBinaryName(s string) bool {
	if s == "" || len(s) > maxNameLen {
		return false
	}
	if s[0] == '/' || s[len(s)-1] == '/' {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		switch part {
		case "", ".", "..":
			return false
		}
		if strings.ContainsAny(part, ".;[\\\x00") {
			return false
		}
	}
	return true
}

const maxNameLen = 4096

// entryName is the archive path a binary name occupies.
func entryName(binary string) string { return binary + ".class" }

// binaryName inverts entryName, reporting false for anything that is not a
// class file or whose name would not round-trip.
func binaryName(entry string) (string, bool) {
	s, ok := strings.CutSuffix(entry, ".class")
	if !ok || !ValidBinaryName(s) {
		return "", false
	}
	return s, true
}