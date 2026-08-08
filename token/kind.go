// Package token defines the lexical vocabulary of Java SE 25 as mocha scans it,
// together with the per-compilation-unit position space that every span in the
// front end resolves through.
//
// Two departures from the JLS are visible in this file:
//
//   - The `>` character is never merged with a following `>`. SHR, USHR,
//     SHR_ASSIGN and USHR_ASSIGN exist as Kinds but the scanner never emits
//     them; the parser synthesizes them from adjacent tokens via Join.
//   - Contextual keywords are scanned as IDENT and tagged with a Ctx. The
//     scanner does not resolve them; see ctx.go.
package token

import "strconv"

// Kind classifies a token.
type Kind uint8

const (
	ILLEGAL Kind = iota
	EOF
	COMMENT // traditional or end-of-line; the scanner does not distinguish

	IDENT // §3.8; carries a Ctx when it spells a contextual keyword

	literalBeg
	INT        // §3.10.1 — decimal, hex, octal, binary; undecoded
	FLOAT      // §3.10.2 — decimal or hexadecimal; undecoded
	CHAR       // §3.10.4
	STRING     // §3.10.5
	TEXTBLOCK  // §3.10.6 — raw span, delimiters included
	TRUE       // §3.10.3
	FALSE      // §3.10.3
	NULL       // §3.10.8
	literalEnd

	operatorBeg
	ASSIGN     // =
	GTR        // >
	LSS        // 
	NOT        // !
	TILDE      // ~
	QUESTION   // ?
	COLON      // :
	ARROW      // ->
	EQL        // ==
	GEQ        // >=
	LEQ        // <=
	NEQ        // !=
	LAND       // &&
	LOR        // ||
	INC        // ++
	DEC        // --
	ADD        // +
	SUB        // -
	MUL        // *
	QUO        // /
	AND        // &
	OR         // |
	XOR        // ^
	REM        // %
	SHL        // 
	ADD_ASSIGN // +=
	SUB_ASSIGN // -=
	MUL_ASSIGN // *=
	QUO_ASSIGN // /=
	AND_ASSIGN // &=
	OR_ASSIGN  // |=
	XOR_ASSIGN // ^=
	REM_ASSIGN // %=
	SHL_ASSIGN // <<=
	operatorEnd

	separatorBeg
	LPAREN     // (
	RPAREN     // )
	LBRACE     // {
	RBRACE     // }
	LBRACK     // [
	RBRACK     // ]
	SEMICOLON  // ;
	COMMA      // ,
	PERIOD     // .
	ELLIPSIS   // ...
	AT         // @
	COLONCOLON // ::
	separatorEnd

	// §3.9 ReservedKeyword. Fifty words plus UNDERSCORE, matching the JLS's
	// count of 51 reserved character sequences.
	keywordBeg
	ABSTRACT
	ASSERT
	BOOLEAN
	BREAK
	BYTE
	CASE
	CATCH
	CHARK // `char`; CHAR is the literal kind
	CLASS
	CONST
	CONTINUE
	DEFAULT
	DO
	DOUBLE
	ELSE
	ENUM
	EXTENDS
	FINAL
	FINALLY
	FLOATK // `float`; FLOAT is the literal kind
	FOR
	GOTO
	IF
	IMPLEMENTS
	IMPORT
	INSTANCEOF
	INT_KW // `int`; INT is the literal kind
	INTERFACE
	LONG
	NATIVE
	NEW
	PACKAGE
	PRIVATE
	PROTECTED
	PUBLIC
	RETURN
	SHORT
	STATIC
	STRICTFP
	SUPER
	SWITCH
	SYNCHRONIZED
	THIS
	THROW
	THROWS
	TRANSIENT
	TRY
	VOID
	VOLATILE
	WHILE
	UNDERSCORE // `_` — reserved, never an IDENT
	keywordEnd

	// NON_SEALED is the one contextual keyword that cannot be an IDENT: the
	// hyphen forces the scanner to splice `non` `-` `sealed` into a single
	// token, under the same adjacency condition as the `>` rule.
	NON_SEALED

	// Parser-synthesized. The scanner NEVER produces these; they exist so the
	// tree can name the operator that Join built. See Join in token.go.
	syntheticBeg
	SHR         // > >
	USHR        // > > >
	SHR_ASSIGN  // > >=
	USHR_ASSIGN // > > >=
	syntheticEnd
)

var kindStrings = [...]string{
	ILLEGAL: "ILLEGAL",
	EOF:     "EOF",
	COMMENT: "COMMENT",
	IDENT:   "IDENT",

	INT:       "INT",
	FLOAT:     "FLOAT",
	CHAR:      "CHAR",
	STRING:    "STRING",
	TEXTBLOCK: "TEXTBLOCK",
	TRUE:      "true",
	FALSE:     "false",
	NULL:      "null",

	ASSIGN:     "=",
	GTR:        ">",
	LSS:        "<",
	NOT:        "!",
	TILDE:      "~",
	QUESTION:   "?",
	COLON:      ":",
	ARROW:      "->",
	EQL:        "==",
	GEQ:        ">=",
	LEQ:        "<=",
	NEQ:        "!=",
	LAND:       "&&",
	LOR:        "||",
	INC:        "++",
	DEC:        "--",
	ADD:        "+",
	SUB:        "-",
	MUL:        "*",
	QUO:        "/",
	AND:        "&",
	OR:         "|",
	XOR:        "^",
	REM:        "%",
	SHL:        "<<",
	ADD_ASSIGN: "+=",
	SUB_ASSIGN: "-=",
	MUL_ASSIGN: "*=",
	QUO_ASSIGN: "/=",
	AND_ASSIGN: "&=",
	OR_ASSIGN:  "|=",
	XOR_ASSIGN: "^=",
	REM_ASSIGN: "%=",
	SHL_ASSIGN: "<<=",

	LPAREN:     "(",
	RPAREN:     ")",
	LBRACE:     "{",
	RBRACE:     "}",
	LBRACK:     "[",
	RBRACK:     "]",
	SEMICOLON:  ";",
	COMMA:      ",",
	PERIOD:     ".",
	ELLIPSIS:   "...",
	AT:         "@",
	COLONCOLON: "::",

	ABSTRACT:     "abstract",
	ASSERT:       "assert",
	BOOLEAN:      "boolean",
	BREAK:        "break",
	BYTE:         "byte",
	CASE:         "case",
	CATCH:        "catch",
	CHARK:        "char",
	CLASS:        "class",
	CONST:        "const",
	CONTINUE:     "continue",
	DEFAULT:      "default",
	DO:           "do",
	DOUBLE:       "double",
	ELSE:         "else",
	ENUM:         "enum",
	EXTENDS:      "extends",
	FINAL:        "final",
	FINALLY:      "finally",
	FLOATK:       "float",
	FOR:          "for",
	GOTO:         "goto",
	IF:           "if",
	IMPLEMENTS:   "implements",
	IMPORT:       "import",
	INSTANCEOF:   "instanceof",
	INT_KW:       "int",
	INTERFACE:    "interface",
	LONG:         "long",
	NATIVE:       "native",
	NEW:          "new",
	PACKAGE:      "package",
	PRIVATE:      "private",
	PROTECTED:    "protected",
	PUBLIC:       "public",
	RETURN:       "return",
	SHORT:        "short",
	STATIC:       "static",
	STRICTFP:     "strictfp",
	SUPER:        "super",
	SWITCH:       "switch",
	SYNCHRONIZED: "synchronized",
	THIS:         "this",
	THROW:        "throw",
	THROWS:       "throws",
	TRANSIENT:    "transient",
	TRY:          "try",
	VOID:         "void",
	VOLATILE:     "volatile",
	WHILE:        "while",
	UNDERSCORE:   "_",

	NON_SEALED: "non-sealed",

	SHR:         "> >",
	USHR:        "> > >",
	SHR_ASSIGN:  "> >=",
	USHR_ASSIGN: "> > >=",
}

func (k Kind) String() string {
	if int(k) < len(kindStrings) && kindStrings[k] != "" {
		return kindStrings[k]
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// identTable maps a spelling to its reserved Kind. Everything not in here that
// scans as IdentifierChars is an IDENT — including all sixteen non-hyphenated
// contextual keywords, which are tagged rather than resolved (see ctx.go).
var identTable = map[string]Kind{}

func init() {
	for k := keywordBeg + 1; k < keywordEnd; k++ {
		identTable[kindStrings[k]] = k
	}
	// BooleanLiteral and NullLiteral are not keywords in §3.9, but they are
	// excluded from Identifier in §3.8, so they resolve through the same table.
	identTable["true"] = TRUE
	identTable["false"] = FALSE
	identTable["null"] = NULL

	if n := len(identTable) - 3; n != 51 {
		panic("token: reserved keyword count is " + strconv.Itoa(n) + ", want 51")
	}
}

// Lookup returns the reserved Kind for a spelling, or IDENT.
func Lookup(s string) Kind {
	if k, ok := identTable[s]; ok {
		return k
	}
	return IDENT
}

func (k Kind) IsLiteral() bool   { return literalBeg < k && k < literalEnd }
func (k Kind) IsOperator() bool  { return operatorBeg < k && k < operatorEnd }
func (k Kind) IsSeparator() bool { return separatorBeg < k && k < separatorEnd }
func (k Kind) IsKeyword() bool   { return keywordBeg < k && k < keywordEnd }

// IsSynthetic reports whether k is a compound shift operator that the parser
// assembles. No token produced by the scanner ever has a synthetic Kind.
func (k Kind) IsSynthetic() bool { return syntheticBeg < k && k < syntheticEnd }