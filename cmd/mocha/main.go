// Command mocha is the driver for the Mocha toolchain.
//
// Two things work end to end today:
//
//	mocha check [flags] <path>...   parse, resolve symbols, report diagnostics
//	mocha build [flags] <path>...   compile to class file 49.0 (jvm target only)
//
// build refuses switch, enum and record bodies with a diagnostic rather than
// emitting something wrong — lower hasn't got to them yet. There is no dex,
// no APK, no native backend, and no device deployment, so --target only
// accepts jvm.
package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vertex-language/mocha/analyzer/attr"
	"github.com/vertex-language/mocha/analyzer/flow"
	"github.com/vertex-language/mocha/analyzer/warn"
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/classpath"
	"github.com/vertex-language/mocha/lower"
	"github.com/vertex-language/mocha/parser"
	"github.com/vertex-language/mocha/scanner"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
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

const usageText = `mocha — a Java toolchain for Android and the JVM

usage:
	mocha <command> [flags] <path>...

commands:
	check     parse sources, resolve symbols, and report diagnostics
	build     compile to class file 49.0 (jvm target only)
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
	case "build":
		return build(args[1:], stdout, stderr)
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

// ---------------------------------------------------------------- shared flags/helpers

// stringList collects a repeatable flag, either passed multiple times
// (-classpath a.jar -classpath b.jar) or comma-separated (-classpath a.jar,b.jar).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

type parsedUnit struct {
	name string
	tf   *token.File
	file *ast.File
}

// parseAll reads and parses every path, merging scan/parse diagnostics into
// rep. Trees are NOT released; the caller owns their lifetime, since symbol
// entry and (for build) attribution need them to stay alive across files.
func parseAll(paths []string, mode parser.Mode, rep *reporter, stderr io.Writer) ([]parsedUnit, int) {
	var units []parsedUnit
	code := exitOK
	for _, path := range paths {
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
			code = worse(code, exitUsage)
			continue
		}
		tf := token.NewFile(name, src)
		file, diags := parser.ParseFile(tf, mode)
		for _, d := range diags {
			rep.report(tf, d)
		}
		units = append(units, parsedUnit{name: name, tf: tf, file: file})
	}
	return units, code
}

func releaseAll(units []parsedUnit) {
	for _, u := range units {
		u.file.Release()
	}
}

func newClasspath(cpPaths, libPaths stringList, release int, stderr io.Writer) (*classpath.Path, int) {
	cp := classpath.New(classpath.Options{Release: release})
	for _, p := range cpPaths {
		if err := cp.Add(classpath.Classpath, p); err != nil {
			fmt.Fprintf(stderr, "mocha: --classpath %s: %v\n", p, err)
			cp.Close()
			return nil, exitUsage
		}
	}
	for _, p := range libPaths {
		if err := cp.Add(classpath.Lib, p); err != nil {
			fmt.Fprintf(stderr, "mocha: --lib %s: %v\n", p, err)
			cp.Close()
			return nil, exitUsage
		}
	}
	return cp, exitOK
}

// ---------------------------------------------------------------- check

func check(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		emit       = fs.String("emit", "", "debug dump: `ast`, `tokens` or `symbols` (default none)")
		comments   = fs.Bool("comments", false, "retain comments (parser.ParseComments)")
		headerOnly = fs.Bool("header-only", false, "stop after the package, imports and module directives")
		tolerant   = fs.Bool("tolerant", false, "keep going past the resync budget")
		maxErrors  = fs.Int("max-errors", 100, "stop reporting after `n` diagnostics; 0 for unlimited")
		quiet      = fs.Bool("q", false, "suppress the summary line")
		release    = fs.Int("release", 8, "multi-release jar version to select on the classpath")
	)
	var classpathPaths, libPaths stringList
	fs.Var(&classpathPaths, "classpath", "jar, aar or directory added for signature resolution (repeatable)")
	fs.Var(&libPaths, "lib", "jar or directory provided at runtime, e.g. android.jar (repeatable)")

	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: mocha check [flags] <path>...\n\n"+
			"Paths may be files or directories; directories are walked for .java.\n"+
			"\"-\" reads one compilation unit from stdin.\n\nflags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	switch *emit {
	case "", "ast", "tokens", "symbols":
	default:
		fmt.Fprintf(stderr, "mocha: --emit: unknown value %q (want ast, tokens or symbols)\n", *emit)
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

	if *emit == "symbols" {
		code = checkSymbols(paths, mode, classpathPaths, libPaths, *release, rep, stdout, stderr)
	} else {
		for _, path := range paths {
			switch c := checkOne(path, mode, *emit, rep, stdout, stderr); c {
			case exitInternal:
				return exitInternal
			case exitUsage:
				code = worse(code, exitUsage)
			}
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

// checkSymbols parses every unit, enters them all into one classpath-backed
// table, completes every declared type, and dumps the result. Every tree
// stays alive until every unit has been entered and completed, since a
// source symbol's completer reads its ast.Decl on demand.
func checkSymbols(paths []string, mode parser.Mode, cpPaths, libPaths stringList, release int, rep *reporter, stdout, stderr io.Writer) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "mocha: internal error resolving symbols: %v\n", r)
			fmt.Fprintf(stderr, "mocha: this is a compiler bug, please report it\n")
			buf := make([]byte, 1<<16)
			fmt.Fprintf(stderr, "%s\n", buf[:runtime.Stack(buf, false)])
			code = exitInternal
		}
	}()

	cp, ccode := newClasspath(cpPaths, libPaths, release, stderr)
	if cp == nil {
		return ccode
	}
	defer cp.Close()

	table := sym.NewTable(cp)

	units, ucode := parseAll(paths, mode, rep, stderr)
	code = worse(code, ucode)
	defer releaseAll(units)

	type entered struct {
		name string
		tf   *token.File
		su   *sym.Unit
	}
	var all []entered
	for _, u := range units {
		su, diags := sym.Enter(table, u.file)
		for _, d := range diags {
			rep.report(u.tf, d)
		}
		all = append(all, entered{name: u.name, tf: u.tf, su: su})
	}

	for _, e := range all {
		if e.su == nil {
			continue
		}
		for _, c := range e.su.Types {
			if err := c.Complete(); err != nil {
				fmt.Fprintf(stderr, "mocha: %s: %s: %v\n", e.name, sym.Dotted(c.Binary), err)
				rep.errors++
				continue
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", e.name, sym.Dotted(c.Binary), c.Flags)
		}
	}

	return code
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

// ---------------------------------------------------------------- build

func build(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		target    = fs.String("target", "jvm", "output target: only `jvm` is supported so far")
		emit      = fs.String("emit", "class", "output form: `class` (a directory tree of .class files) or `jar`")
		out       = fs.String("o", "out", "output directory (--emit class) or jar path (--emit jar)")
		mainClass = fs.String("main-class", "", "Main-Class manifest entry, internal form (--emit jar only)")
		release   = fs.Int("release", 8, "multi-release jar version to select on the classpath")
		maxErrors = fs.Int("max-errors", 100, "stop reporting after `n` diagnostics; 0 for unlimited")
		quiet     = fs.Bool("q", false, "suppress the summary line")
	)
	var classpathPaths, libPaths stringList
	fs.Var(&classpathPaths, "classpath", "jar, aar or directory added for signature resolution (repeatable)")
	fs.Var(&libPaths, "lib", "jar or directory provided at runtime, e.g. android.jar (repeatable)")

	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: mocha build [flags] <path>...\n\n"+
			"Compiles to class file 49.0. A unit with any diagnostic — from parsing\n"+
			"through attr, flow or warn — is not lowered. switch, enum and record\n"+
			"bodies are refused by lower with a diagnostic rather than emitted wrong.\n\nflags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	if *target != "jvm" {
		fmt.Fprintf(stderr, "mocha: --target: unknown value %q (only jvm is supported so far)\n", *target)
		return exitUsage
	}
	switch *emit {
	case "class", "jar":
	default:
		fmt.Fprintf(stderr, "mocha: --emit: unknown value %q (want class or jar)\n", *emit)
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

	rep := &reporter{w: stderr, max: *maxErrors}
	code := buildAll(paths, classpathPaths, libPaths, *release, *emit, *out, *mainClass, rep, stdout, stderr)

	if rep.suppressed > 0 {
		fmt.Fprintf(stderr, "mocha: %d more diagnostic(s) not shown (--max-errors %d)\n",
			rep.suppressed, *maxErrors)
	}
	if rep.errors > 0 {
		code = worse(code, exitDiags)
	}
	if !*quiet {
		fmt.Fprintf(stderr, "mocha: %d file(s), %d diagnostic(s)\n", len(paths), rep.reported+rep.suppressed)
	}
	return code
}

// buildAll runs parse → enter → attr → flow → warn → lower over every unit,
// sharing one symbol table and type table so cross-file references resolve.
// A unit with any diagnostic at any stage is skipped, per lower's own rule:
// never lower a broken unit. Other units still build.
func buildAll(paths []string, cpPaths, libPaths stringList, release int, emit, out, mainClass string, rep *reporter, stdout, stderr io.Writer) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "mocha: internal error while building: %v\n", r)
			fmt.Fprintf(stderr, "mocha: this is a compiler bug, please report it\n")
			buf := make([]byte, 1<<16)
			fmt.Fprintf(stderr, "%s\n", buf[:runtime.Stack(buf, false)])
			code = exitInternal
		}
	}()

	cp, ccode := newClasspath(cpPaths, libPaths, release, stderr)
	if cp == nil {
		return ccode
	}
	defer cp.Close()

	st := sym.NewTable(cp)
	tt := types.NewTable(st)

	units, ucode := parseAll(paths, parser.DefaultMode, rep, stderr)
	code = worse(code, ucode)
	defer releaseAll(units)

	// Enter and register every unit before attributing any of them, so a
	// forward reference between two files in the batch resolves.
	type entered struct {
		name string
		tf   *token.File
		su   *sym.Unit
	}
	var all []entered
	for _, u := range units {
		su, diags := sym.Enter(st, u.file)
		for _, d := range diags {
			rep.report(u.tf, d)
		}
		if su != nil {
			tt.Register(su)
		}
		all = append(all, entered{name: u.name, tf: u.tf, su: su})
	}

	var zw *zip.Writer
	var zf *os.File
	if emit == "jar" {
		f, err := os.Create(out)
		if err != nil {
			fmt.Fprintf(stderr, "mocha: %v\n", err)
			return exitUsage
		}
		zf = f
		zw = zip.NewWriter(f)
		if mainClass != "" {
			if w, err := zw.Create("META-INF/MANIFEST.MF"); err == nil {
				fmt.Fprintf(w, "Manifest-Version: 1.0\nMain-Class: %s\n", strings.ReplaceAll(mainClass, "/", "."))
			}
		}
	}

	for _, e := range all {
		if e.su == nil {
			continue
		}

		in := attr.Attr(tt, e.su)
		for _, d := range in.Diags {
			rep.report(e.tf, d)
		}
		fl := flow.Analyze(in, tt, e.su)
		for _, d := range fl.Diags {
			rep.report(e.tf, d)
		}
		wn := warn.Check(in, fl, tt, e.su)
		for _, d := range wn.Diags {
			rep.report(e.tf, d)
		}
		if len(in.Diags)+len(fl.Diags)+len(wn.Diags) > 0 {
			continue // never lower a broken unit
		}

		classes, diags := lower.Lower(in, fl, tt, e.su)
		for _, d := range diags {
			rep.report(e.tf, d)
		}
		if len(diags) > 0 {
			continue
		}

		for _, c := range classes {
			// NOTE: assumes classfile.Builder exposes the binary name it was
			// constructed with (classfile.NewBuilder("com/example/Main")).
			// Not in the classfile README's documented surface — add it if
			// it isn't there yet; nothing else here can compute this path.
			name := c.Name()

			if zw != nil {
				b, err := c.Bytes()
				if err != nil {
					fmt.Fprintf(stderr, "mocha: %s: %v\n", name, err)
					rep.errors++
					continue
				}
				w, err := zw.Create(name + ".class")
				if err != nil || func() error { _, err := w.Write(b); return err }() != nil {
					fmt.Fprintf(stderr, "mocha: %s: write failed\n", name)
					rep.errors++
					continue
				}
				continue
			}

			dest := filepath.Join(out, filepath.FromSlash(name)+".class")
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				fmt.Fprintf(stderr, "mocha: %v\n", err)
				rep.errors++
				continue
			}
			if err := c.WriteFile(dest); err != nil {
				fmt.Fprintf(stderr, "mocha: %s: %v\n", name, err)
				rep.errors++
				continue
			}
		}
	}

	if zw != nil {
		if err := zw.Close(); err != nil {
			fmt.Fprintf(stderr, "mocha: %v\n", err)
			rep.errors++
		}
		zf.Close()
	}

	return code
}

// ---------------------------------------------------------------- reporting

type reporter struct {
	w          io.Writer
	max        int
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