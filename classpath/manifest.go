package classpath

import (
	"bytes"
	"strings"
)

// mainAttrs holds the main section of a META-INF/MANIFEST.MF, keyed by
// lowercased attribute name. Only the main section is parsed: it terminates at
// the first blank line, so a signed jar's hundreds of per-entry digest sections
// are never touched.
type mainAttrs map[string]string

// parseMainSection reads the main section per the JAR File Specification.
//
// Errors are not reported. The specification directs a reader to ignore
// attributes it does not understand, and the only attribute this package needs
// is Multi-Release; a manifest too malformed to parse simply yields no
// attributes, which makes the jar an ordinary jar. That is the correct fallback
// — it is what every pre-9 runtime does.
func parseMainSection(data []byte) mainAttrs {
	// A trailing EOF character (code 26) is whitespace.
	data = bytes.TrimSuffix(data, []byte{0x1a})

	attrs := make(mainAttrs)
	var name string
	var val []byte

	flush := func() {
		if name != "" {
			// "Attribute names cannot be repeated within a section." A jar
			// that repeats one is malformed; first wins.
			if _, dup := attrs[name]; !dup {
				attrs[name] = string(val)
			}
		}
		name, val = "", nil
	}

	for len(data) > 0 {
		var line []byte
		line, data = nextLine(data)
		if len(line) == 0 {
			break // end of the main section
		}
		if line[0] == ' ' {
			// A continuation contributes everything after the single SPACE.
			val = append(val, line[1:]...)
			continue
		}
		flush()

		i := bytes.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		n := string(line[:i])
		if !validHeaderName(n) {
			continue
		}
		v := line[i+1:]
		if len(v) > 0 && v[0] == ' ' {
			v = v[1:] // the separator, not part of the value
		}
		name = strings.ToLower(n)
		val = append([]byte(nil), v...)
	}
	flush()
	return attrs
}

// multiRelease implements the Multi-Release check exactly as specified: the
// name is case-insensitive, the value is case-insensitive, and surrounding
// white space is *not* tolerated. The no-trim rule is deliberate — it is what
// lets a runtime make this decision cheaply — so " true" is not true.
func (a mainAttrs) multiRelease() bool {
	v, ok := a["multi-release"]
	return ok && strings.EqualFold(v, "true")
}

// nextLine splits on CR LF, LF, or CR not followed by LF.
func nextLine(b []byte) (line, rest []byte) {
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '\n':
			return b[:i], b[i+1:]
		case '\r':
			if i+1 < len(b) && b[i+1] == '\n' {
				return b[:i], b[i+2:]
			}
			return b[:i], b[i+1:]
		}
	}
	return b, nil
}

// validHeaderName: alphanum, then alphanum, '-' or '_'.
func validHeaderName(s string) bool {
	if s == "" || len(s) > 70 { // a name cannot be continued, so 72 - ": "
		return false
	}
	if !isAlnum(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isAlnum(s[i]) && s[i] != '-' && s[i] != '_' {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}