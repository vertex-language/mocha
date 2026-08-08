// Command mocha is the driver for the Mocha toolchain.
//
// The back end does not exist yet, so the only command that does real work is
//
//	mocha check [flags] <path>...
//
// which runs the front end over Java sources and reports diagnostics. Nothing
// is emitted unless --emit asks for a debug dump.
package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/parser"
	"github.com/vertex-language/mocha/scanner"
	"github.com/vertex-language/mocha/token"
)

// Exit codes, per the CLI contract in the top-level README.
const (
	exitOK       = 0 // success
	exitDiags    = 1 // compilation or verification failed with diagnostics
	exitUsage    = 2 // bad flags, missing input
	exitInternal = 3 // a compiler bug
)

// Set with -ldflags at release time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usageText = `mocha — a VM and toolchain for Android and the JVM

usage:
	mocha <command> [flags] <path>...

commands:
	check     parse and check sources, emit nothing
	version   print version information
	help      print this message

Run "mocha <command> -h" for a command's flags.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return exitUsage
	}

	switch cmd := args[0]; cmd {
	case "check":
		return check(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "mocha %s\ncommit:  %s\nbuilt:   %s\ngo:      %s\n",
			version, commit, date, runtime.Version())
		return exitOK
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return exitOK
	default:
		fmt.Fprintf(stderr, "mocha: unknown command %q\n\n", cmd)
		fmt.Fprint(stderr, usageText)
		return exitUsage
	}
}

// ---------------------------------------------------------------- check

func check(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		emit       = fs.String("emit", "", "debug dump: `ast` or `tokens` (default none)")
		comments   = fs.Bool("comments", false, "retain comments (parser.ParseComments)")
		headerOnly = fs.Bool("header-only", false, "stop after the package, imports and module directives")
		tolerant   = fs.Bool("tolerant", false, "keep going past the resync budget")
		maxErrors  = fs.Int("max-errors", 100, "stop reporting after `n` diagnostics; 0 for unlimited")
		quiet      = fs.Bool("q", false, "suppress the summary line")
	)

	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: mocha check [flags] <path>...\n\n"+
			"Paths may be files or directories; directories are walked for .java.\n"+
			"\"-\" reads one compilation unit from stdin.\n\nflags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return exitUsage // ContinueOnError already printed the reason
	}

	switch *emit {
	case "", "ast", "tokens":
	default:
		fmt.Fprintf(stderr, "mocha: --emit: unknown value %q (want ast or tokens)\n", *emit)
		return exitUsage
	}

	if fs.NArg() == 0 {
		fmt.Fprintf(stderr, "mocha: no input paths\n\n")
		fs.Usage()
		return exitUsage
	}

	paths, err := collect(fs.Args())
	if err != nil {
		fmt.Fprintf(stderr, "mocha: %v\n", err)
		return exitUsage
	}
	if len(paths) == 0 {
		fmt.Fprintf(stderr, "mocha: no .java files under the given paths\n")
		return exitUsage
	}

	mode := parser.DefaultMode
	if *comments {
		mode |= parser.ParseComments
	}
	if *headerOnly {
		mode |= parser.HeaderOnly
	}
	if *tolerant {
		mode |= parser.Tolerant
	}

	rep := &reporter{w: stderr, max: *maxErrors}
	code := exitOK

	for _, path := range paths {
		switch c := checkOne(path, mode, *emit, rep, stdout, stderr); c {
		case exitInternal:
			return exitInternal // a panic in the front end: stop, don't grind on
		case exitUsage:
			code = worse(code, exitUsage)
		}
	}

	if rep.suppressed > 0 {
		fmt.Fprintf(stderr, "mocha: %d more diagnostic(s) not shown (--max-errors %d)\n",
			rep.suppressed, *maxErrors)
	}
	if rep.errors > 0 {
		code = worse(code, exitDiags)
	}
	if !*quiet && (rep.reported > 0 || len(paths) > 1) {
		fmt.Fprintf(stderr, "mocha: %d file(s), %d diagnostic(s)\n", len(paths), rep.reported+rep.suppressed)
	}
	return code
}

// checkOne runs the front end over a single compilation unit. The tree is
// released before it returns, so nothing here may outlive the call.
func checkOne(path string, mode parser.Mode, emit string, rep *reporter, stdout, stderr io.Writer) (code int) {
	name := path
	var src []byte
	var err error
	if path == "-" {
		name = "<stdin>"
		src, err = io.ReadAll(os.Stdin)
	} else {
		src, err = os.ReadFile(path)
	}
	if err != nil {
		fmt.Fprintf(stderr, "mocha: %v\n", err)
		return exitUsage
	}

	// The front end is young; turn a panic into exit 3 with a location rather
	// than an unhelpful goroutine dump.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "mocha: internal error while parsing %s: %v\n", name, r)
			fmt.Fprintf(stderr, "mocha: this is a compiler bug, please report it\n")
			buf := make([]byte, 1<<16)
			fmt.Fprintf(stderr, "%s\n", buf[:runtime.Stack(buf, false)])
			code = exitInternal
		}
	}()

	unit := token.NewFile(name, src)

	// Tokens are dumped from a separate scan; the scanner's diagnostics are
	// dropped here because ParseFile merges the same ones in below.
	if emit == "tokens" {
		var smode scanner.Mode
		if mode&parser.ParseComments != 0 {
			smode |= scanner.ScanComments
		}
		toks, _ := scanner.Scan(unit, smode)
		dumpTokens(stdout, unit, toks)
	}

	file, diags := parser.ParseFile(unit, mode)
	defer file.Release()

	for _, d := range diags {
		rep.report(unit, d)
	}

	if emit == "ast" {
		ast.Fdump(stdout, unit, file)
	}
	return exitOK
}

func dumpTokens(w io.Writer, f *token.File, toks []token.Token) {
	for _, t := range toks {
		if t.Kind == token.EOF {
			break
		}
		p := f.Position(t.Pos)
		fmt.Fprintf(w, "%s:%d:%d\t%-12s\t%q\n", p.Filename, p.Line, p.Column, t.Kind, f.Slice(t.Pos, t.End))
	}
}

// ---------------------------------------------------------------- reporting

type reporter struct {
	w          io.Writer
	max        int // 0 for unlimited
	reported   int
	suppressed int
	errors     int
}

func (r *reporter) report(f *token.File, d token.Diagnostic) {
	if isError(d) {
		r.errors++
	}
	if r.max > 0 && r.reported >= r.max {
		r.suppressed++
		return
	}
	p := f.Position(d.Pos)
	fmt.Fprintf(r.w, "%s:%d:%d: %s: %s\n", p.Filename, p.Line, p.Column, d.Severity, d.Msg)
	r.reported++
}

// isError reports whether a diagnostic should fail the run.
//
// TODO: the severity constants aren't nailed down yet, so this is deliberately
// conservative — every diagnostic is fatal. Once token exports them this
// becomes `return d.Severity >= token.SeverityError`, and warnings stop
// costing exit 1.
func isError(d token.Diagnostic) bool { return true }

// ---------------------------------------------------------------- inputs

// collect expands the command line into a list of units to parse. Named files
// are taken as given; directories are walked for .java. Dotted directories are
// skipped unless named explicitly.
func collect(paths []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}

	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	for _, p := range paths {
		if p == "-" {
			add("-")
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			add(p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != p && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(d.Name(), ".java") {
				add(path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func worse(a, b int) int {
	if b > a {
		return b
	}
	return a
}