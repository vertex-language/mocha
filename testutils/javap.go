package testutils

import (
	"regexp"
	"sort"
	"strings"
)

// poolRef matches a constant pool reference in javap's disassembly. The index
// itself carries no information across two independent compilers — only the
// resolved comment javap prints beside it does — so it is normalised away.
var poolRef = regexp.MustCompile(`#\d+`)

var spaces = regexp.MustCompile(`[ \t]+`)

// Normalize reduces javap -c output to the facts two compilers must agree on.
//
// Removed: the "Compiled from" header, blank lines, trailing whitespace,
// constant pool indices, and javap's column alignment. Retained: the opcode
// sequence, the bytecode offsets, and the resolved reference each instruction
// names — which is the whole content of the check.
func Normalize(javap string) []string {
	var out []string
	for _, line := range strings.Split(javap, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "Compiled from") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		line = poolRef.ReplaceAllString(line, "#")
		line = spaces.ReplaceAllString(strings.TrimSpace(line), " ")
		out = append(out, strings.Repeat(" ", min(indent, 8))+line)
	}
	return out
}

// Members splits normalised javap output into one entry per class member,
// keyed by the declaration javap printed.
//
// Comparing per member rather than whole-file makes the check independent of
// method table ordering, which the class file format does not constrain and
// which javac and mocha have no reason to agree on.
func Members(javap string) map[string][]string {
	members := map[string][]string{}
	var cur string
	for _, line := range Normalize(javap) {
		trimmed := strings.TrimSpace(line)
		if isMemberDecl(line, trimmed) {
			cur = trimmed
			members[cur] = nil
			continue
		}
		if cur == "" {
			continue // the class declaration line and its closing brace
		}
		members[cur] = append(members[cur], trimmed)
	}
	return members
}

// isMemberDecl recognises the lines javap indents by two spaces and ends with
// a semicolon: fields, methods, constructors, and the static initialiser.
func isMemberDecl(line, trimmed string) bool {
	indent := len(line) - len(strings.TrimLeft(line, " "))
	if indent != 2 || !strings.HasSuffix(trimmed, ";") {
		return false
	}
	return strings.Contains(trimmed, "(") || strings.Contains(trimmed, "{}")
}

// MemberNames lists the members of a disassembly, sorted.
func MemberNames(javap string) []string {
	m := Members(javap)
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// sizesRe pulls max_stack, max_locals and args_size out of javap -v.
var sizesRe = regexp.MustCompile(`stack=(\d+), locals=(\d+), args_size=(\d+)`)

// Sizes are a method's frame sizes, which javap -c does not print.
type Sizes struct{ Stack, Locals, Args int }

// MethodSizes extracts the frame sizes from javap -v output, keyed by member
// declaration.
//
// This closes the one real gap in the -c diff: max_stack and max_locals are
// computed by the encoder and invisible to -c, so a body that disassembles
// identically could still declare a frame that is too small. Reading just
// these three numbers out of -v avoids the pool renumbering that makes a full
// -v diff useless.
func MethodSizes(verbose string) map[string]Sizes {
	out := map[string]Sizes{}
	var cur string
	for _, line := range strings.Split(verbose, "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, " \t\r"))
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 2 && strings.HasSuffix(trimmed, ";") &&
			(strings.Contains(trimmed, "(") || strings.Contains(trimmed, "{}")) {
			cur = spaces.ReplaceAllString(trimmed, " ")
			continue
		}
		if cur == "" {
			continue
		}
		if m := sizesRe.FindStringSubmatch(trimmed); m != nil {
			out[cur] = Sizes{Stack: atoi(m[1]), Locals: atoi(m[2]), Args: atoi(m[3])}
			cur = ""
		}
	}
	return out
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}