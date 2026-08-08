# java_grammar.md

Java SE 25 grammar for `mocha`. Notation per JLS §2.4. Section anchors refer to the JLS (2025-07-29); productions are restated here for `mocha` and are not a reproduction of the specification text.

**Target.** Java SE 25 (LTS), final language features only. Java SE 26 (March 2026) added no new final syntax — its only JLS-level change is *Primitive Types in Patterns, `instanceof`, and `switch`*, which remains a preview feature — so this grammar is current for the stable language. Enabling that preview would alter `Pattern` and `SwitchLabel`; neither is included here.

## Notation

```
Nonterminal:
    alternative one
    alternative two
```

- **CamelCase** words are nonterminals. Everything else — lowercase keywords, punctuation, operators — is a terminal, to appear exactly as written.
- `{x}` — zero or more occurrences of `x`.
- `[x]` — zero or one occurrence of `x`.
- `(one of)` — each symbol on the following lines is a separate alternative.
- `but not` — excludes the named expansions.
- A phrase in *(italics-by-parenthesis)* defines a nonterminal narratively where enumeration is impractical.
- A long right-hand side continues on an indented following line.

## Deviations from the JLS

`mocha` follows the JLS's nonterminal names and productions throughout, with one deliberate exception.

**The `>` character is never merged with a following `>`.** The JLS resolves the tension between nested type arguments (`List<List<String>>`) and the shift operators with a context-sensitive rule in §3.5: inside a type context, a run of `>` characters is split into individual comparison-operator tokens. `mocha` instead makes the split unconditional in the scanner and gives the join to the parser. Consequences:

- `>>`, `>>>`, `>>=`, and `>>>=` are **not** tokens, and do not appear in `Operator` or as single terminals anywhere.
- `>=` *is* still a token; maximal munch applies to every operator except the `>`-`>` join.
- Productions that need a compound shift operator write its tokens separately, with the spacing significant: `> >`, `> > >`, `> >=`, `> > >=`. The tokens must be adjacent in the input (no white space or comment between them) for the parser to join them.

Scanning examples: `x >>= 2` yields `>` `>=`; `x >>> 3` yields `>` `>` `>`; `Map<String, List<Integer>>` yields two separate `>`.

Semantic-only constraints (those the JLS states in prose rather than in the grammar) are noted narratively below and are not encoded in the productions.

---

## §3 — Lexical Structure

### §3.1–3.3 Unicode and escapes

```
UnicodeInputCharacter:
    UnicodeEscape
    RawInputCharacter

UnicodeEscape:
    \ UnicodeMarker HexDigit HexDigit HexDigit HexDigit

UnicodeMarker:
    u {u}

RawInputCharacter:
    (any Unicode character representable in UTF-16)

HexDigit: (one of)
    0 1 2 3 4 5 6 7 8 9 a b c d e f A B C D E F
```

### §3.4–3.5 Line terminators, input, tokens

```
LineTerminator:
    (the ASCII LF character)
    (the ASCII CR character)
    (the ASCII CR character followed by the ASCII LF character)

InputCharacter:
    UnicodeInputCharacter but not CR or LF

Input:
    {InputElement} [Sub]

InputElement:
    WhiteSpace
    Comment
    Token

Token:
    Identifier
    Keyword
    Literal
    Separator
    Operator

Sub:
    (the ASCII SUB character, also known as control-Z)
```

Lexical translation uses the longest possible match at each step, even where a shorter match would yield a grammatically correct program: `a--b` is tokenized `a`, `--`, `b`, and is not part of any correct program, even though `a`, `-`, `-`, `b` would be.

The single exception is the `>` rule. A `>` character is never combined with a `>` character that follows it. This replaces the JLS §3.5 type-context rule and applies uniformly, in type contexts and expression contexts alike: `mocha` has no lexical notion of type context. Longest-match still governs `>=`, so `>` is combined with a following `=`.

### §3.6–3.7 White space and comments

```
WhiteSpace:
    (the ASCII SP character)
    (the ASCII HT character)
    (the ASCII FF character)
    LineTerminator

Comment:
    TraditionalComment
    EndOfLineComment

TraditionalComment:
    / * CommentTail

CommentTail:
    * CommentTailStar
    NotStar CommentTail

CommentTailStar:
    /
    * CommentTailStar
    NotStarNotSlash CommentTail

NotStar:
    InputCharacter but not *
    LineTerminator

NotStarNotSlash:
    InputCharacter but not * or /
    LineTerminator

EndOfLineComment:
    / / {InputCharacter}
```

### §3.8 Identifiers

```
Identifier:
    IdentifierChars but not a ReservedKeyword or BooleanLiteral or NullLiteral

IdentifierChars:
    JavaLetter {JavaLetterOrDigit}

JavaLetter:
    (any Unicode character that is a "Java letter")

JavaLetterOrDigit:
    (any Unicode character that is a "Java letter-or-digit")

TypeIdentifier:
    Identifier but not permits, record, sealed, var, or yield

UnqualifiedMethodIdentifier:
    Identifier but not yield
```

### §3.9 Keywords

```
Keyword:
    ReservedKeyword
    ContextualKeyword

ReservedKeyword: (one of)
    abstract   continue   for          new         switch
    assert     default    if           package     synchronized
    boolean    do         goto         private     this
    break      double     implements   protected   throw
    byte       else       import       public      throws
    case       enum       instanceof   return      transient
    catch      extends    int          short       try
    char       final      interface    static      void
    class      finally    long         strictfp    volatile
    const      float      native       super       while
    _

ContextualKeyword: (one of)
    exports    opens      permits      provides    record
    module     open       requires     sealed      to
    transitive uses       var          when        with
    yield      non-sealed
```

Fifty reserved keywords plus `_`, matching the JLS's count of 51 reserved character sequences.

A contextual keyword is recognized only when it appears as a terminal in the production that admits it, and only when the character immediately before and immediately after the sequence is not a `JavaLetterOrDigit`. `non-sealed` is recognized as a single token under the same adjacency condition.

### §3.10 Literals

```
Literal:
    IntegerLiteral
    FloatingPointLiteral
    BooleanLiteral
    CharacterLiteral
    StringLiteral
    TextBlock
    NullLiteral
```

#### §3.10.1 Integer literals

```
IntegerLiteral:
    DecimalIntegerLiteral
    HexIntegerLiteral
    OctalIntegerLiteral
    BinaryIntegerLiteral

DecimalIntegerLiteral:
    DecimalNumeral [IntegerTypeSuffix]

HexIntegerLiteral:
    HexNumeral [IntegerTypeSuffix]

OctalIntegerLiteral:
    OctalNumeral [IntegerTypeSuffix]

BinaryIntegerLiteral:
    BinaryNumeral [IntegerTypeSuffix]

IntegerTypeSuffix: (one of)
    l L

DecimalNumeral:
    0
    NonZeroDigit [Digits]
    NonZeroDigit Underscores Digits

Digits:
    Digit
    Digit [DigitsAndUnderscores] Digit

DigitsAndUnderscores:
    DigitOrUnderscore {DigitOrUnderscore}

DigitOrUnderscore:
    Digit
    _

Underscores:
    _ {_}

Digit:
    0
    NonZeroDigit

NonZeroDigit: (one of)
    1 2 3 4 5 6 7 8 9

HexNumeral:
    0 x HexDigits
    0 X HexDigits

HexDigits:
    HexDigit
    HexDigit [HexDigitsAndUnderscores] HexDigit

HexDigitsAndUnderscores:
    HexDigitOrUnderscore {HexDigitOrUnderscore}

HexDigitOrUnderscore:
    HexDigit
    _

OctalNumeral:
    0 OctalDigits
    0 Underscores OctalDigits

OctalDigits:
    OctalDigit
    OctalDigit [OctalDigitsAndUnderscores] OctalDigit

OctalDigitsAndUnderscores:
    OctalDigitOrUnderscore {OctalDigitOrUnderscore}

OctalDigitOrUnderscore:
    OctalDigit
    _

OctalDigit: (one of)
    0 1 2 3 4 5 6 7

BinaryNumeral:
    0 b BinaryDigits
    0 B BinaryDigits

BinaryDigits:
    BinaryDigit
    BinaryDigit [BinaryDigitsAndUnderscores] BinaryDigit

BinaryDigitsAndUnderscores:
    BinaryDigitOrUnderscore {BinaryDigitOrUnderscore}

BinaryDigitOrUnderscore:
    BinaryDigit
    _

BinaryDigit: (one of)
    0 1
```

#### §3.10.2 Floating-point literals

```
FloatingPointLiteral:
    DecimalFloatingPointLiteral
    HexadecimalFloatingPointLiteral

DecimalFloatingPointLiteral:
    Digits . [Digits] [ExponentPart] [FloatTypeSuffix]
    . Digits [ExponentPart] [FloatTypeSuffix]
    Digits ExponentPart [FloatTypeSuffix]
    Digits [ExponentPart] FloatTypeSuffix

ExponentPart:
    ExponentIndicator SignedInteger

ExponentIndicator: (one of)
    e E

SignedInteger:
    [Sign] Digits

Sign: (one of)
    + -

FloatTypeSuffix: (one of)
    f F d D

HexadecimalFloatingPointLiteral:
    HexSignificand BinaryExponent [FloatTypeSuffix]

HexSignificand:
    HexNumeral [.]
    0 x [HexDigits] . HexDigits
    0 X [HexDigits] . HexDigits

BinaryExponent:
    BinaryExponentIndicator SignedInteger

BinaryExponentIndicator: (one of)
    p P
```

#### §3.10.3–3.10.8 Boolean, character, string, text block, null

```
BooleanLiteral: (one of)
    true false

CharacterLiteral:
    ' SingleCharacter '
    ' EscapeSequence '

SingleCharacter:
    InputCharacter but not ' or \

StringLiteral:
    " {StringCharacter} "

StringCharacter:
    InputCharacter but not " or \
    EscapeSequence

TextBlock:
    """ {TextBlockWhiteSpace} LineTerminator {TextBlockCharacter} """

TextBlockWhiteSpace:
    WhiteSpace but not LineTerminator

TextBlockCharacter:
    InputCharacter but not \
    EscapeSequence
    LineTerminator

EscapeSequence:
    \ b
    \ s
    \ t
    \ n
    \ f
    \ r
    \ LineTerminator
    \ "
    \ '
    \ \
    OctalEscape

OctalEscape:
    \ OctalDigit
    \ OctalDigit OctalDigit
    \ ZeroToThree OctalDigit OctalDigit

ZeroToThree: (one of)
    0 1 2 3
```

The `\ LineTerminator` alternative (line continuation) is admitted only within a `TextBlock`; the restriction is semantic, not syntactic.

```
NullLiteral:
    null
```

### §3.11–3.12 Separators and operators

```
Separator: (one of)
    ( ) { } [ ] ; , . ... @ ::

Operator: (one of)
    =    >    <    !    ~    ?    :    ->
    ==   >=   <=   !=   &&   ||   ++   --
    +    -    *    /    &    |    ^    %    <<
    +=   -=   *=   /=   &=   |=   ^=   %=   <<=
```

`>>`, `>>>`, `>>=`, and `>>>=` are absent by design; see *Deviations from the JLS*. Each is assembled by the parser from adjacent `>`, `>=`, and `<`-free tokens in `ShiftExpression` and `AssignmentOperator`.

---

## §4 — Types, Values, and Variables

```
Type:
    PrimitiveType
    ReferenceType

PrimitiveType:
    {Annotation} NumericType
    {Annotation} boolean

NumericType:
    IntegralType
    FloatingPointType

IntegralType: (one of)
    byte short int long char

FloatingPointType: (one of)
    float double

ReferenceType:
    ClassOrInterfaceType
    TypeVariable
    ArrayType

ClassOrInterfaceType:
    ClassType
    InterfaceType

ClassType:
    {Annotation} TypeIdentifier [TypeArguments]
    PackageName . {Annotation} TypeIdentifier [TypeArguments]
    ClassOrInterfaceType . {Annotation} TypeIdentifier [TypeArguments]

InterfaceType:
    ClassType

TypeVariable:
    {Annotation} TypeIdentifier

ArrayType:
    PrimitiveType Dims
    ClassOrInterfaceType Dims
    TypeVariable Dims

Dims:
    {Annotation} [ ] {{Annotation} [ ]}

TypeParameter:
    {TypeParameterModifier} TypeIdentifier [TypeBound]

TypeParameterModifier:
    Annotation

TypeBound:
    extends TypeVariable
    extends ClassOrInterfaceType {AdditionalBound}

AdditionalBound:
    & InterfaceType

TypeArguments:
    < TypeArgumentList >

TypeArgumentList:
    TypeArgument {, TypeArgument}

TypeArgument:
    ReferenceType
    Wildcard

Wildcard:
    {Annotation} ? [WildcardBounds]

WildcardBounds:
    extends ReferenceType
    super ReferenceType
```

Nested type arguments need no special handling: because the scanner never merges `>` with a following `>`, the closing delimiters of `List<List<String>>` arrive as two separate `>` tokens, each closing one `TypeArguments`.

---

## §6 — Names

```
ModuleName:
    Identifier
    ModuleName . Identifier

PackageName:
    Identifier
    PackageName . Identifier

TypeName:
    TypeIdentifier
    PackageOrTypeName . TypeIdentifier

ExpressionName:
    Identifier
    AmbiguousName . Identifier

MethodName:
    UnqualifiedMethodIdentifier

PackageOrTypeName:
    Identifier
    PackageOrTypeName . Identifier

AmbiguousName:
    Identifier
    AmbiguousName . Identifier
```

---

## §7 — Packages and Modules

```
CompilationUnit:
    OrdinaryCompilationUnit
    CompactCompilationUnit
    ModularCompilationUnit

OrdinaryCompilationUnit:
    [PackageDeclaration] {ImportDeclaration} {TopLevelClassOrInterfaceDeclaration}

CompactCompilationUnit:
    {ImportDeclaration} {ClassMemberDeclarationNoMethod} MethodDeclaration
        {ClassMemberDeclaration}

ClassMemberDeclarationNoMethod:
    FieldDeclaration
    ClassDeclaration
    InterfaceDeclaration
    ;

ModularCompilationUnit:
    {ImportDeclaration} ModuleDeclaration

PackageDeclaration:
    {PackageModifier} package Identifier {. Identifier} ;

PackageModifier:
    Annotation

ImportDeclaration:
    SingleTypeImportDeclaration
    TypeImportOnDemandDeclaration
    SingleStaticImportDeclaration
    StaticImportOnDemandDeclaration
    SingleModuleImportDeclaration

SingleTypeImportDeclaration:
    import TypeName ;

TypeImportOnDemandDeclaration:
    import PackageOrTypeName . * ;

SingleStaticImportDeclaration:
    import static TypeName . Identifier ;

StaticImportOnDemandDeclaration:
    import static TypeName . * ;

SingleModuleImportDeclaration:
    import module ModuleName ;

TopLevelClassOrInterfaceDeclaration:
    ClassDeclaration
    InterfaceDeclaration
    ;

ModuleDeclaration:
    {Annotation} [open] module Identifier {. Identifier} { {ModuleDirective} }

ModuleDirective:
    requires {RequiresModifier} ModuleName ;
    exports PackageName [to ModuleName {, ModuleName}] ;
    opens PackageName [to ModuleName {, ModuleName}] ;
    uses TypeName ;
    provides TypeName with TypeName {, TypeName} ;

RequiresModifier: (one of)
    transitive static
```

A compact compilation unit must contain at least one method declaration; the grammar encodes this by requiring a `MethodDeclaration` after any leading non-method members. This also disambiguates against `OrdinaryCompilationUnit`: a unit with only class/interface declarations is ordinary, never compact.

---

## §8 — Classes

### §8.1 Class declarations

```
ClassDeclaration:
    NormalClassDeclaration
    EnumDeclaration
    RecordDeclaration

NormalClassDeclaration:
    {ClassModifier} class TypeIdentifier [TypeParameters]
        [ClassExtends] [ClassImplements] [ClassPermits] ClassBody

ClassModifier: (one of)
    Annotation public protected private
    abstract static final sealed non-sealed strictfp

TypeParameters:
    < TypeParameterList >

TypeParameterList:
    TypeParameter {, TypeParameter}

ClassExtends:
    extends ClassType

ClassImplements:
    implements InterfaceTypeList

InterfaceTypeList:
    InterfaceType {, InterfaceType}

ClassPermits:
    permits TypeName {, TypeName}

ClassBody:
    { {ClassBodyDeclaration} }

ClassBodyDeclaration:
    ClassMemberDeclaration
    InstanceInitializer
    StaticInitializer
    ConstructorDeclaration

ClassMemberDeclaration:
    FieldDeclaration
    MethodDeclaration
    ClassDeclaration
    InterfaceDeclaration
    ;
```

A class is also declared implicitly by a compact compilation unit (§7.3), by a class instance creation expression ending in a class body, and by an enum constant ending in a class body. None of these is a `ClassDeclaration`.

### §8.3 Field declarations

```
FieldDeclaration:
    {FieldModifier} UnannType VariableDeclaratorList ;

FieldModifier: (one of)
    Annotation public protected private
    static final transient volatile

VariableDeclaratorList:
    VariableDeclarator {, VariableDeclarator}

VariableDeclarator:
    VariableDeclaratorId [= VariableInitializer]

VariableDeclaratorId:
    Identifier [Dims]
    _

VariableInitializer:
    Expression
    ArrayInitializer

UnannType:
    UnannPrimitiveType
    UnannReferenceType

UnannPrimitiveType:
    NumericType
    boolean

UnannReferenceType:
    UnannClassOrInterfaceType
    UnannTypeVariable
    UnannArrayType

UnannClassOrInterfaceType:
    UnannClassType
    UnannInterfaceType

UnannClassType:
    TypeIdentifier [TypeArguments]
    PackageName . {Annotation} TypeIdentifier [TypeArguments]
    UnannClassOrInterfaceType . {Annotation} TypeIdentifier [TypeArguments]

UnannInterfaceType:
    UnannClassType

UnannTypeVariable:
    TypeIdentifier

UnannArrayType:
    UnannPrimitiveType Dims
    UnannClassOrInterfaceType Dims
    UnannTypeVariable Dims
```

The `_` alternative of `VariableDeclaratorId` admits unnamed variables (local variables, catch parameters, lambda parameters, pattern variables, and enhanced-`for` / `try`-with-resources variables). Contexts where `_` is not permitted (e.g. fields, method formal parameters of a method declaration) are excluded semantically, not syntactically.

### §8.4 Method declarations

```
MethodDeclaration:
    {MethodModifier} MethodHeader MethodBody

MethodModifier: (one of)
    Annotation public protected private
    abstract static final synchronized native strictfp

MethodHeader:
    Result MethodDeclarator [Throws]
    TypeParameters {Annotation} Result MethodDeclarator [Throws]

Result:
    UnannType
    void

MethodDeclarator:
    Identifier ( [ReceiverParameter ,] [FormalParameterList] ) [Dims]

ReceiverParameter:
    {Annotation} UnannType [Identifier .] this

FormalParameterList:
    FormalParameter {, FormalParameter}

FormalParameter:
    {VariableModifier} UnannType VariableDeclaratorId
    VariableArityParameter

VariableArityParameter:
    {VariableModifier} UnannType {Annotation} ... Identifier

VariableModifier:
    Annotation
    final

Throws:
    throws ExceptionTypeList

ExceptionTypeList:
    ExceptionType {, ExceptionType}

ExceptionType:
    ClassType
    TypeVariable

MethodBody:
    Block
    ;
```

### §8.6–8.8 Initializers and constructors

```
InstanceInitializer:
    Block

StaticInitializer:
    static Block

ConstructorDeclaration:
    {ConstructorModifier} ConstructorDeclarator [Throws] ConstructorBody

ConstructorModifier: (one of)
    Annotation public protected private

ConstructorDeclarator:
    [TypeParameters] SimpleTypeName ( [ReceiverParameter ,] [FormalParameterList] )

SimpleTypeName:
    TypeIdentifier

ConstructorBody:
    { [BlockStatements] ExplicitConstructorInvocation [BlockStatements] }
    { [BlockStatements] }

ExplicitConstructorInvocation:
    [TypeArguments] this ( [ArgumentList] ) ;
    [TypeArguments] super ( [ArgumentList] ) ;
    ExpressionName . [TypeArguments] super ( [ArgumentList] ) ;
    Primary . [TypeArguments] super ( [ArgumentList] ) ;
```

The `[BlockStatements]` preceding an `ExplicitConstructorInvocation` form the constructor *prologue*; those following it form the *epilogue* (flexible constructor bodies, finalized in SE 25). Where no explicit invocation appears, the prologue is empty and all statements are the epilogue. The restrictions on prologue code — the early construction context, and the ban on `return e;` — are semantic, not syntactic.

### §8.9 Enum classes

```
EnumDeclaration:
    {ClassModifier} enum TypeIdentifier [ClassImplements] EnumBody

EnumBody:
    { [EnumConstantList] [,] [EnumBodyDeclarations] }

EnumConstantList:
    EnumConstant {, EnumConstant}

EnumConstant:
    {EnumConstantModifier} Identifier [( [ArgumentList] )] [ClassBody]

EnumConstantModifier:
    Annotation

EnumBodyDeclarations:
    ; {ClassBodyDeclaration}
```

### §8.10 Record classes

```
RecordDeclaration:
    {ClassModifier} record TypeIdentifier [TypeParameters] RecordHeader
        [ClassImplements] RecordBody

RecordHeader:
    ( [RecordComponentList] )

RecordComponentList:
    RecordComponent {, RecordComponent}

RecordComponent:
    {RecordComponentModifier} UnannType Identifier
    VariableArityRecordComponent

VariableArityRecordComponent:
    {RecordComponentModifier} UnannType {Annotation} ... Identifier

RecordComponentModifier:
    Annotation

RecordBody:
    { {RecordBodyDeclaration} }

RecordBodyDeclaration:
    ClassBodyDeclaration
    CompactConstructorDeclaration

CompactConstructorDeclaration:
    {ConstructorModifier} SimpleTypeName ConstructorBody
```

---

## §9 — Interfaces

```
InterfaceDeclaration:
    NormalInterfaceDeclaration
    AnnotationInterfaceDeclaration

NormalInterfaceDeclaration:
    {InterfaceModifier} interface TypeIdentifier [TypeParameters]
        [InterfaceExtends] [InterfacePermits] InterfaceBody

InterfaceModifier: (one of)
    Annotation public protected private
    abstract static sealed non-sealed strictfp

InterfaceExtends:
    extends InterfaceTypeList

InterfacePermits:
    permits TypeName {, TypeName}

InterfaceBody:
    { {InterfaceMemberDeclaration} }

InterfaceMemberDeclaration:
    ConstantDeclaration
    InterfaceMethodDeclaration
    ClassDeclaration
    InterfaceDeclaration
    ;

ConstantDeclaration:
    {ConstantModifier} UnannType VariableDeclaratorList ;

ConstantModifier: (one of)
    Annotation public static final

InterfaceMethodDeclaration:
    {InterfaceMethodModifier} MethodHeader MethodBody

InterfaceMethodModifier: (one of)
    Annotation public private abstract default static strictfp

AnnotationInterfaceDeclaration:
    {InterfaceModifier} @ interface TypeIdentifier AnnotationInterfaceBody

AnnotationInterfaceBody:
    { {AnnotationInterfaceMemberDeclaration} }

AnnotationInterfaceMemberDeclaration:
    AnnotationInterfaceElementDeclaration
    ConstantDeclaration
    ClassDeclaration
    InterfaceDeclaration
    ;

AnnotationInterfaceElementDeclaration:
    {AnnotationInterfaceElementModifier} UnannType Identifier ( ) [Dims]
        [DefaultValue] ;

AnnotationInterfaceElementModifier: (one of)
    Annotation public abstract

DefaultValue:
    default ElementValue

Annotation:
    NormalAnnotation
    MarkerAnnotation
    SingleElementAnnotation

NormalAnnotation:
    @ TypeName ( [ElementValuePairList] )

ElementValuePairList:
    ElementValuePair {, ElementValuePair}

ElementValuePair:
    Identifier = ElementValue

ElementValue:
    ConditionalExpression
    ElementValueArrayInitializer
    Annotation

ElementValueArrayInitializer:
    { [ElementValueList] [,] }

ElementValueList:
    ElementValue {, ElementValue}

MarkerAnnotation:
    @ TypeName

SingleElementAnnotation:
    @ TypeName ( ElementValue )
```

`InterfaceModifier` admits `sealed` and `non-sealed` for the nonterminal as a whole; their use on an `AnnotationInterfaceDeclaration` is a compile-time error, enforced semantically.

---

## §10 — Arrays

```
ArrayInitializer:
    { [VariableInitializerList] [,] }

VariableInitializerList:
    VariableInitializer {, VariableInitializer}
```

---

## §14 — Blocks, Statements, and Patterns

### §14.2–14.4 Blocks and local declarations

```
Block:
    { [BlockStatements] }

BlockStatements:
    BlockStatement {BlockStatement}

BlockStatement:
    LocalClassOrInterfaceDeclaration
    LocalVariableDeclarationStatement
    Statement

LocalClassOrInterfaceDeclaration:
    ClassDeclaration
    NormalInterfaceDeclaration

LocalVariableDeclarationStatement:
    LocalVariableDeclaration ;

LocalVariableDeclaration:
    {VariableModifier} LocalVariableType VariableDeclaratorList

LocalVariableType:
    UnannType
    var
```

### §14.5 Statements

```
Statement:
    StatementWithoutTrailingSubstatement
    LabeledStatement
    IfThenStatement
    IfThenElseStatement
    WhileStatement
    ForStatement

StatementNoShortIf:
    StatementWithoutTrailingSubstatement
    LabeledStatementNoShortIf
    IfThenElseStatementNoShortIf
    WhileStatementNoShortIf
    ForStatementNoShortIf

StatementWithoutTrailingSubstatement:
    Block
    EmptyStatement
    ExpressionStatement
    AssertStatement
    SwitchStatement
    DoStatement
    BreakStatement
    ContinueStatement
    ReturnStatement
    SynchronizedStatement
    ThrowStatement
    TryStatement
    YieldStatement
```

### §14.6–14.10 Simple statements

```
EmptyStatement:
    ;

LabeledStatement:
    Identifier : Statement

LabeledStatementNoShortIf:
    Identifier : StatementNoShortIf

ExpressionStatement:
    StatementExpression ;

StatementExpression:
    Assignment
    PreIncrementExpression
    PreDecrementExpression
    PostIncrementExpression
    PostDecrementExpression
    MethodInvocation
    ClassInstanceCreationExpression

IfThenStatement:
    if ( Expression ) Statement

IfThenElseStatement:
    if ( Expression ) StatementNoShortIf else Statement

IfThenElseStatementNoShortIf:
    if ( Expression ) StatementNoShortIf else StatementNoShortIf

AssertStatement:
    assert Expression ;
    assert Expression : Expression ;
```

### §14.11 The switch statement

```
SwitchStatement:
    switch ( Expression ) SwitchBlock

SwitchBlock:
    { SwitchRule {SwitchRule} }
    { {SwitchBlockStatementGroup} {SwitchLabel :} }

SwitchRule:
    SwitchLabel -> Expression ;
    SwitchLabel -> Block
    SwitchLabel -> ThrowStatement

SwitchBlockStatementGroup:
    SwitchLabel : {SwitchLabel :} BlockStatements

SwitchLabel:
    case CaseConstant {, CaseConstant}
    case null [, default]
    case CasePattern {, CasePattern} [Guard]
    default

CaseConstant:
    ConditionalExpression

CasePattern:
    Pattern

Guard:
    when Expression
```

The multi-pattern form of a case label dates from SE 22. A label with more than one pattern may not declare pattern variables, and its guard governs the label as a whole; both constraints are semantic.

### §14.12–14.14 Loops

```
WhileStatement:
    while ( Expression ) Statement

WhileStatementNoShortIf:
    while ( Expression ) StatementNoShortIf

DoStatement:
    do Statement while ( Expression ) ;

ForStatement:
    BasicForStatement
    EnhancedForStatement

ForStatementNoShortIf:
    BasicForStatementNoShortIf
    EnhancedForStatementNoShortIf

BasicForStatement:
    for ( [ForInit] ; [Expression] ; [ForUpdate] ) Statement

BasicForStatementNoShortIf:
    for ( [ForInit] ; [Expression] ; [ForUpdate] ) StatementNoShortIf

ForInit:
    StatementExpressionList
    LocalVariableDeclaration

ForUpdate:
    StatementExpressionList

StatementExpressionList:
    StatementExpression {, StatementExpression}

EnhancedForStatement:
    for ( LocalVariableDeclaration : Expression ) Statement

EnhancedForStatementNoShortIf:
    for ( LocalVariableDeclaration : Expression ) StatementNoShortIf
```

### §14.15–14.20 Transfer of control

```
BreakStatement:
    break [Identifier] ;

YieldStatement:
    yield Expression ;

ContinueStatement:
    continue [Identifier] ;

ReturnStatement:
    return [Expression] ;

ThrowStatement:
    throw Expression ;

SynchronizedStatement:
    synchronized ( Expression ) Block

TryStatement:
    try Block Catches
    try Block [Catches] Finally
    TryWithResourcesStatement

Catches:
    CatchClause {CatchClause}

CatchClause:
    catch ( CatchFormalParameter ) Block

CatchFormalParameter:
    {VariableModifier} CatchType VariableDeclaratorId

CatchType:
    UnannClassType {| ClassType}

Finally:
    finally Block

TryWithResourcesStatement:
    try ResourceSpecification Block [Catches] [Finally]

ResourceSpecification:
    ( ResourceList [;] )

ResourceList:
    Resource {; Resource}

Resource:
    LocalVariableDeclaration
    VariableAccess

VariableAccess:
    ExpressionName
    FieldAccess
```

A `Resource` that is a `LocalVariableDeclaration` must declare exactly one variable with an initializer; this constraint is semantic, not syntactic.

### §14.30 Patterns

```
Pattern:
    TypePattern
    RecordPattern

TypePattern:
    LocalVariableDeclaration

RecordPattern:
    ReferenceType ( [ComponentPatternList] )

ComponentPatternList:
    ComponentPattern {, ComponentPattern}

ComponentPattern:
    Pattern
    MatchAllPattern

MatchAllPattern:
    _
```

---

## §15 — Expressions

### §15.8–15.13 Primary expressions

```
Primary:
    PrimaryNoNewArray
    ArrayCreationExpression

PrimaryNoNewArray:
    Literal
    ClassLiteral
    this
    TypeName . this
    ( Expression )
    ClassInstanceCreationExpression
    FieldAccess
    ArrayAccess
    MethodInvocation
    MethodReference

ClassLiteral:
    TypeName {[ ]} . class
    NumericType {[ ]} . class
    boolean {[ ]} . class
    void . class

ClassInstanceCreationExpression:
    UnqualifiedClassInstanceCreationExpression
    ExpressionName . UnqualifiedClassInstanceCreationExpression
    Primary . UnqualifiedClassInstanceCreationExpression

UnqualifiedClassInstanceCreationExpression:
    new [TypeArguments] ClassOrInterfaceTypeToInstantiate
        ( [ArgumentList] ) [ClassBody]

ClassOrInterfaceTypeToInstantiate:
    {Annotation} Identifier {. {Annotation} Identifier} [TypeArgumentsOrDiamond]

TypeArgumentsOrDiamond:
    TypeArguments
    <>

FieldAccess:
    Primary . Identifier
    super . Identifier
    TypeName . super . Identifier

ArrayAccess:
    ExpressionName [ Expression ]
    PrimaryNoNewArray [ Expression ]
    ArrayCreationExpressionWithInitializer [ Expression ]

MethodInvocation:
    MethodName ( [ArgumentList] )
    TypeName . [TypeArguments] Identifier ( [ArgumentList] )
    ExpressionName . [TypeArguments] Identifier ( [ArgumentList] )
    Primary . [TypeArguments] Identifier ( [ArgumentList] )
    super . [TypeArguments] Identifier ( [ArgumentList] )
    TypeName . super . [TypeArguments] Identifier ( [ArgumentList] )

ArgumentList:
    Expression {, Expression}

MethodReference:
    ExpressionName :: [TypeArguments] Identifier
    Primary :: [TypeArguments] Identifier
    ReferenceType :: [TypeArguments] Identifier
    super :: [TypeArguments] Identifier
    TypeName . super :: [TypeArguments] Identifier
    ClassType :: [TypeArguments] new
    ArrayType :: new
```

### §15.10 Array creation

```
ArrayCreationExpression:
    ArrayCreationExpressionWithoutInitializer
    ArrayCreationExpressionWithInitializer

ArrayCreationExpressionWithoutInitializer:
    new PrimitiveType DimExprs [Dims]
    new ClassOrInterfaceType DimExprs [Dims]

ArrayCreationExpressionWithInitializer:
    new PrimitiveType Dims ArrayInitializer
    new ClassOrInterfaceType Dims ArrayInitializer

DimExprs:
    DimExpr {DimExpr}

DimExpr:
    {Annotation} [ Expression ]
```

### §15.14–15.29 Operators, lambdas, switch expressions, constant expressions

```
Expression:
    LambdaExpression
    AssignmentExpression

LambdaExpression:
    LambdaParameters -> LambdaBody

LambdaParameters:
    ( [LambdaParameterList] )
    ConciseLambdaParameter

LambdaParameterList:
    NormalLambdaParameter {, NormalLambdaParameter}
    ConciseLambdaParameter {, ConciseLambdaParameter}

NormalLambdaParameter:
    {VariableModifier} LambdaParameterType VariableDeclaratorId
    VariableArityParameter

LambdaParameterType:
    UnannType
    var

ConciseLambdaParameter:
    Identifier
    _

LambdaBody:
    Expression
    Block

AssignmentExpression:
    ConditionalExpression
    Assignment

Assignment:
    LeftHandSide AssignmentOperator Expression

LeftHandSide:
    ExpressionName
    FieldAccess
    ArrayAccess

AssignmentOperator:
    SingleTokenAssignmentOperator
    > >=
    > > >=

SingleTokenAssignmentOperator: (one of)
    =  *=  /=  %=  +=  -=  <<=  &=  ^=  |=

ConditionalExpression:
    ConditionalOrExpression
    ConditionalOrExpression ? Expression : ConditionalExpression
    ConditionalOrExpression ? Expression : LambdaExpression

ConditionalOrExpression:
    ConditionalAndExpression
    ConditionalOrExpression || ConditionalAndExpression

ConditionalAndExpression:
    InclusiveOrExpression
    ConditionalAndExpression && InclusiveOrExpression

InclusiveOrExpression:
    ExclusiveOrExpression
    InclusiveOrExpression | ExclusiveOrExpression

ExclusiveOrExpression:
    AndExpression
    ExclusiveOrExpression ^ AndExpression

AndExpression:
    EqualityExpression
    AndExpression & EqualityExpression

EqualityExpression:
    RelationalExpression
    EqualityExpression == RelationalExpression
    EqualityExpression != RelationalExpression

RelationalExpression:
    ShiftExpression
    RelationalExpression < ShiftExpression
    RelationalExpression > ShiftExpression
    RelationalExpression <= ShiftExpression
    RelationalExpression >= ShiftExpression
    InstanceofExpression

InstanceofExpression:
    RelationalExpression instanceof ReferenceType
    RelationalExpression instanceof Pattern

ShiftExpression:
    AdditiveExpression
    ShiftExpression << AdditiveExpression
    ShiftExpression > > AdditiveExpression
    ShiftExpression > > > AdditiveExpression

AdditiveExpression:
    MultiplicativeExpression
    AdditiveExpression + MultiplicativeExpression
    AdditiveExpression - MultiplicativeExpression

MultiplicativeExpression:
    UnaryExpression
    MultiplicativeExpression * UnaryExpression
    MultiplicativeExpression / UnaryExpression
    MultiplicativeExpression % UnaryExpression

UnaryExpression:
    PreIncrementExpression
    PreDecrementExpression
    + UnaryExpression
    - UnaryExpression
    UnaryExpressionNotPlusMinus

PreIncrementExpression:
    ++ UnaryExpression

PreDecrementExpression:
    -- UnaryExpression

UnaryExpressionNotPlusMinus:
    PostfixExpression
    ~ UnaryExpression
    ! UnaryExpression
    CastExpression
    SwitchExpression

PostfixExpression:
    Primary
    ExpressionName
    PostIncrementExpression
    PostDecrementExpression

PostIncrementExpression:
    PostfixExpression ++

PostDecrementExpression:
    PostfixExpression --

CastExpression:
    ( PrimitiveType ) UnaryExpression
    ( ReferenceType {AdditionalBound} ) UnaryExpressionNotPlusMinus
    ( ReferenceType {AdditionalBound} ) LambdaExpression

SwitchExpression:
    switch ( Expression ) SwitchBlock

ConstantExpression:
    Expression
```

In `ShiftExpression` and `AssignmentOperator`, the spaced forms `> >`, `> > >`, `> >=`, and `> > >=` denote separate, adjacent tokens joined by the parser. Adjacency is required: `a > > b` in the source is a syntax error, not a shift.