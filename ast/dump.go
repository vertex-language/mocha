package ast

import (
	"fmt"
	"io"
	"reflect"

	"github.com/vertex-language/mocha/token"
)

// Fdump writes a readable form of n to w. Identifiers and literals are printed
// with the text their spans resolve to, which is why a *token.File is required:
// the tree does not hold that text.
//
// Positions are printed as line:column in raw source coordinates, so a
// diagnostic in a file that used Unicode escapes lines up with what the user
// typed.
func Fdump(w io.Writer, f *token.File, n Node) error {
	d := &dumper{w: w, f: f}
	d.node(reflect.ValueOf(n), 0)
	d.nl()
	return d.err
}

type dumper struct {
	w   io.Writer
	f   *token.File
	err error
}

func (d *dumper) printf(format string, args ...any) {
	if d.err != nil {
		return
	}
	_, d.err = fmt.Fprintf(d.w, format, args...)
}

func (d *dumper) nl() { d.printf("\n") }

func (d *dumper) indent(n int) {
	for i := 0; i < n; i++ {
		d.printf("  ")
	}
}

func (d *dumper) node(val reflect.Value, depth int) {
	if depth > 200 {
		d.printf("...")
		return
	}
	switch val.Kind() {
	case reflect.Interface:
		if val.IsNil() {
			d.printf("nil")
			return
		}
		d.node(val.Elem(), depth)
		return
	case reflect.Ptr:
		if val.IsNil() {
			d.printf("nil")
			return
		}
		name := val.Type().Elem().Name()
		d.printf("%s", name)
		if n, ok := val.Interface().(Node); ok {
			d.printf(" %s", d.span(n))
		}
		if lit := d.text(val); lit != "" {
			d.printf(" %s", lit)
		}
		d.fields(val.Elem(), depth)
		return
	case reflect.Slice:
		if val.Len() == 0 {
			d.printf("[]")
			return
		}
		d.printf("[")
		for i := 0; i < val.Len(); i++ {
			d.nl()
			d.indent(depth + 1)
			d.node(val.Index(i), depth+1)
		}
		d.nl()
		d.indent(depth)
		d.printf("]")
		return
	}
	d.printf("%v", val.Interface())
}

func (d *dumper) fields(val reflect.Value, depth int) {
	if val.Kind() != reflect.Struct {
		return
	}
	t := val.Type()
	for i := 0; i < val.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" || f.Name == "Span" || f.Name == "Releaser" || f.Name == "Unit" {
			continue
		}
		fv := val.Field(i)
		if isZero(fv) {
			continue
		}
		d.nl()
		d.indent(depth + 1)
		d.printf("%s: ", f.Name)
		switch fv.Kind() {
		case reflect.Ptr, reflect.Interface, reflect.Slice:
			d.node(fv, depth+1)
		default:
			d.scalar(f.Name, fv)
		}
	}
}

func (d *dumper) scalar(name string, val reflect.Value) {
	switch v := val.Interface().(type) {
	case token.Pos:
		d.printf("%s", d.pos(v))
	case token.Kind:
		d.printf("%s", v)
	case token.Ctx:
		d.printf("%s", v)
	default:
		d.printf("%v", v)
	}
}

// text returns the source text of a node whose spelling carries meaning.
func (d *dumper) text(val reflect.Value) string {
	switch n := val.Interface().(type) {
	case *Ident:
		if n.Underscore {
			return `"_"`
		}
		return fmt.Sprintf("%q", n.Name(d.f))
	case *BasicLit:
		s := d.f.Slice(n.Lo, n.Hi)
		if len(s) > 40 {
			s = s[:37] + "..."
		}
		return fmt.Sprintf("%q", s)
	}
	return ""
}

func (d *dumper) span(n Node) string {
	a, b := d.f.Position(n.Pos()), d.f.Position(n.End())
	if a.Line == b.Line {
		return fmt.Sprintf("%d:%d-%d", a.Line, a.Column, b.Column)
	}
	return fmt.Sprintf("%d:%d-%d:%d", a.Line, a.Column, b.Line, b.Column)
}

func (d *dumper) pos(p token.Pos) string {
	if !p.IsValid() {
		return "-"
	}
	q := d.f.Position(p)
	return fmt.Sprintf("%d:%d", q.Line, q.Column)
}

func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	case reflect.Slice:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	}
	return v.IsZero()
}