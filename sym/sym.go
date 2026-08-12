// Package sym builds the symbol table: it turns declarations — yours and the
// ones inside jars on the class path — into named, scoped symbols that later
// phases resolve against.
//
// This is javac's Enter, split the way Go's package graph requires. sym answers
// what is declared, under what name, in what scope, and with what modifiers. It
// does not answer what anything's type is: a symbol keeps the raw material it
// arrived with — a descriptor string from a class file, an ast.Type from source
// — and [types] turns that into a type model. Symbol and Type are mutually
// referential in javac; here the dependency runs one way, so sym is the leaf.
//
// # Two sources, one shape
//
// A ClassSym read from okhttp-4.12.0.jar and a ClassSym entered from Fetch.java
// are the same type with the same scope protocol. Nothing above this package
// should branch on where a symbol came from, which is what lets attr resolve
// `response.code()` without knowing that Response is binary and Fetch is not.
//
// # Completion is lazy, entry is eager
//
// Every class on the path gets a stub the moment its name is mentioned; its
// members arrive only when something asks. Source classes are entered eagerly —
// all of them, before any member is completed — which is what makes a forward
// reference between two top-level types in the same unit resolve.
//
// # Lifetime
//
// A symbol entered from source holds ast.Node pointers, and the parser's arena
// invalidates every node in a tree on Release. A source symbol is therefore
// valid only while its tree is: parse, enter, attribute, lower, then release.
// A symbol completed from a class file holds no tree and outlives everything.
package sym

import (
	"errors"
	"sync"

	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/token"
)

// Kind classifies a symbol. It is deliberately coarse: whether a ClassSym is an
// interface, an enum or a record is a Flag, exactly as it is a flag bit in a
// class file.
type Kind uint8

const (
	// KindError is the symbol a failed lookup yields. It exists for the same
	// reason ast.BadExpr does: a consumer gets a symbol, not a nil, so one
	// unresolvable name costs one diagnostic and no cascade.
	KindError Kind = iota
	KindPackage
	KindClass
	KindMethod
	KindVar
	KindTypeParam
)

var kindStrings = [...]string{
	KindError:     "error",
	KindPackage:   "package",
	KindClass:     "class",
	KindMethod:    "method",
	KindVar:       "variable",
	KindTypeParam: "type parameter",
}

func (k Kind) String() string {
	if int(k) < len(kindStrings) {
		return kindStrings[k]
	}
	return "Kind(?)"
}

// namespace groups kinds by §6.5's three declaration spaces. Java lets a field,
// a method and a member type share a name, so a scope conflict is only a
// conflict within one space.
func (k Kind) namespace() int {
	switch k {
	case KindClass, KindTypeParam, KindPackage:
		return nsType
	case KindMethod:
		return nsMethod
	default:
		return nsVar
	}
}

const (
	nsType = iota
	nsVar
	nsMethod
)

// Symbol is the interface every symbol satisfies. The unexported method closes
// the hierarchy: nothing outside this package can introduce a symbol kind.
type Symbol interface {
	Base() *Sym
	symbolNode()
}

// Sym is the part every symbol has. It is embedded, not inherited.
type Sym struct {
	Name  string // simple name; "<init>" for a constructor
	Kind  Kind
	Flags Flags
	Owner Symbol    // enclosing package, class or method; nil for the root
	Pos   token.Pos // NoPos for a symbol read from a class file
	End   token.Pos
	Unit  *token.File // the position space Pos resolves through; nil if binary
}

func (s *Sym) Base() *Sym { return s }

// FromSource reports whether the symbol was entered from a compilation unit
// rather than completed from a class file.
func (s *Sym) FromSource() bool { return s.Unit != nil }

// --- packages ---------------------------------------------------------------

// PackageSym is a package. Its members are the types it declares; subpackages
// are not members, because `a.b` being a package tells you nothing about
// whether `a.b.C` is one.
type PackageSym struct {
	Sym
	Dotted   string // com.example
	Internal string // com/example
	Members  *Scope
	table    *Table
}

func (*PackageSym) symbolNode() {}

// IsUnnamed reports whether this is the unnamed package (§7.4.2).
func (p *PackageSym) IsUnnamed() bool { return p.Dotted == "" }

// --- classes ----------------------------------------------------------------

// ClassSym is a class, interface, enum, record or annotation interface. Which
// one is in Flags.
type ClassSym struct {
	Sym

	// Binary is the internal-form name: com/example/Fetch, com/example/A$B.
	// It is the key everything below this package agrees on.
	Binary  string
	Package *PackageSym
	Outer   *ClassSym // nil for a top-level class

	// Super and Interfaces are internal-form names. Super is "" for
	// java/lang/Object and for an interface.
	Super      string
	Interfaces []string
	Permits    []string // a sealed class's permitted subclasses

	TypeParams []*TypeParamSym
	Members    *Scope

	// Decl is the declaration this class was entered from, or nil for a class
	// completed from a class file. See the note on lifetime in the package doc.
	Decl ast.Decl

	SourceFile string

	mu        sync.Mutex
	completer Completer
	state     completionState
	err       error

	// nextAnon numbers the anonymous classes of this class's body. It lives
	// here because §13.1's naming is per innermost enclosing class.
	nextAnon int
}

func (*ClassSym) symbolNode() {}

func (c *ClassSym) IsInterface() bool  { return c.Flags.Has(FlagInterface) }
func (c *ClassSym) IsEnum() bool       { return c.Flags.Has(FlagEnum) }
func (c *ClassSym) IsRecord() bool     { return c.Flags.Has(FlagRecord) }
func (c *ClassSym) IsAnnotation() bool { return c.Flags.Has(FlagAnnotation) }

// IsTopLevel reports whether the class is declared directly in a package.
func (c *ClassSym) IsTopLevel() bool { return c.Outer == nil }

type completionState uint8

const (
	notCompleted completionState = iota
	completing
	completed
)

// ErrCyclicCompletion reports a class whose completion depends on itself. A
// class file naming itself as its own superclass produces one; so does a source
// hierarchy with a cycle.
var ErrCyclicCompletion = errors.New("sym: cyclic class completion")

// Completer fills in a class's members and supertypes on demand.
type Completer interface {
	Complete(*ClassSym) error
}

// Complete populates the class if it has not been populated already. It is safe
// for concurrent use and runs the completer at most once; a second call returns
// the first call's error.
func (c *ClassSym) Complete() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case completed:
		return c.err
	case completing:
		return ErrCyclicCompletion
	}
	if c.completer == nil {
		c.state = completed
		return nil
	}
	c.state = completing
	err := c.completer.Complete(c)
	c.state = completed
	c.completer = nil
	c.err = err
	return err
}

// Lookup returns the members of this class with the given name, completing it
// first. Inherited members are not searched: which supertype member is visible
// from where is a resolution rule, and resolution is attr's.
func (c *ClassSym) Lookup(name string) []Symbol {
	if c.Complete() != nil {
		return nil
	}
	return c.Members.Lookup(name)
}

// Methods returns the methods named name declared directly in this class.
func (c *ClassSym) Methods(name string) []*MethodSym {
	var out []*MethodSym
	for _, s := range c.Lookup(name) {
		if m, ok := s.(*MethodSym); ok {
			out = append(out, m)
		}
	}
	return out
}

// Field returns the field named name declared directly in this class, or nil.
func (c *ClassSym) Field(name string) *VarSym {
	for _, s := range c.Lookup(name) {
		if v, ok := s.(*VarSym); ok {
			return v
		}
	}
	return nil
}

// Nested returns the member type named name, or nil.
func (c *ClassSym) Nested(name string) *ClassSym {
	for _, s := range c.Lookup(name) {
		if n, ok := s.(*ClassSym); ok {
			return n
		}
	}
	return nil
}

// --- methods ----------------------------------------------------------------

// MethodSym is a method, a constructor or an annotation interface element.
//
// Descriptor is set for a symbol completed from a class file and empty for one
// entered from source: building it needs erasure, which needs resolved types.
// types fills it in.
type MethodSym struct {
	Sym
	Class      *ClassSym
	Descriptor string
	TypeParams []*TypeParamSym
	Params     []*VarSym
	Result     ast.Type // nil for void and for a binary symbol
	Throws     []string // internal-form names, from a class file only
	ThrowsExpr []ast.Type
	Decl       ast.Decl // *ast.MethodDecl, *ast.ConstructorDecl or *ast.AnnotationElemDecl
	Default    ast.Node // an annotation element's default value
}

func (*MethodSym) symbolNode() {}

// IsConstructor reports whether this is an instance initialization method.
func (m *MethodSym) IsConstructor() bool { return m.Name == InitName }

// IsClassInit reports whether this is the class initialization method.
func (m *MethodSym) IsClassInit() bool { return m.Name == ClinitName }

// The two names the JVM reserves for initialization methods. They are not
// identifiers, so no source declaration can collide with them.
const (
	InitName   = "<init>"
	ClinitName = "<clinit>"
)

// --- variables --------------------------------------------------------------

// VarKind distinguishes the positions a VarSym can occupy. ast.VarDecl covers
// fields, constants and locals with one node because they have one shape; the
// difference is where they were declared, and this is where that is recorded.
type VarKind uint8

const (
	VarField VarKind = iota
	VarLocal
	VarParam
	VarRecordComponent
	VarEnumConstant
	VarExceptionParam
	VarResource
	VarBinding // a pattern variable
)

// VarSym is a field, local, parameter, record component or enum constant.
type VarSym struct {
	Sym
	Var        VarKind
	Class      *ClassSym  // the declaring class, for a field
	Method     *MethodSym // the declaring method, for a local or parameter
	Descriptor string     // class files only
	TypeExpr   ast.Type   // source only; may be *ast.VarType
	Decl       ast.Node

	// Const is a compile-time constant read from a ConstantValue attribute.
	// A source constant is folded by attr, not here.
	Const *classfile.Const
}

func (*VarSym) symbolNode() {}

// Unnamed reports whether the declaration used `_` (§6.1). An unnamed variable
// is entered so its span survives, but it is never findable by name.
func (v *VarSym) Unnamed() bool { return v.Name == "_" }

// --- type parameters --------------------------------------------------------

// TypeParamSym is one type parameter of a class or method.
type TypeParamSym struct {
	Sym
	Index      int
	Bounds     []ast.Type // source only
	Decl       *ast.TypeParam
	Signature  string // class files only; unparsed, see classfile.Signature
}

func (*TypeParamSym) symbolNode() {}

// --- errors -----------------------------------------------------------------

// ErrorSym stands in for a name that could not be resolved. Handing one back
// instead of nil is what keeps one bad import from producing an error at every
// use of the type it failed to name.
type ErrorSym struct {
	Sym
	// Sought is the name that was looked up, for diagnostics.
	Sought string
}

func (*ErrorSym) symbolNode() {}

// NewError returns an error symbol for a name.
func NewError(name string, pos token.Pos, unit *token.File) *ErrorSym {
	return &ErrorSym{
		Sym:    Sym{Name: name, Kind: KindError, Pos: pos, Unit: unit},
		Sought: name,
	}
}

// IsError reports whether s is an error symbol. Every consumer should check
// this before trusting a lookup result.
func IsError(s Symbol) bool {
	if s == nil {
		return true
	}
	return s.Base().Kind == KindError
}