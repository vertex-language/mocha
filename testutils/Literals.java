// Exercises: every literal production, all of which stay undecoded through
// the front end. Underscores are validated, not removed; the text block keeps
// its delimiters and its incidental whitespace.
package sample;

class Literals {

    static final int  DECIMAL   = 1_024;
    static final int  HEX       = 0xCAFE_BABE;
    static final int  OCTAL     = 0_777;          // leading underscores are legal here
    static final int  BINARY    = 0b1010_1010;
    static final long LONG_MAX  = 9_223_372_036_854_775_807L;
    static final long HEX_LONG  = 0xFFFF_FFFF_FFFF_FFFFL;

    static final float  F       = 1.5f;
    static final double SMALL   = 1e-9;
    static final double LEADING = .5d;
    static final double TRAIL   = 2.;
    static final double HEXFP   = 0x1.8p3;        // hex float needs the binary exponent
    static final double HEXFP2  = 0x1p-2;         // exponent is a SignedInteger, not hex

    static final char TAB   = '\t';
    static final char QUOTE = '\'';
    static final char SLASH = '\\';
    static final char ACUTE = '\u00e9';
    static final char OCT   = '\101';

    static final String ESCAPED = "quote:\" backslash:\\ newline:\n unit:\u2122";

    static final String BLOCK = """
            {
              "name": "mocha",
              "quoted": "\"yes\""
            }
            """;

    static final String JOINED = """
            one \
            line
            """;

    // An identifier spelled with a Unicode escape: `count`, translated before
    // tokenization, but `Raw` still underlines what was typed.
    static int \u0063ount = 0;

    static final boolean YES = true;
    static final Object  NIL = null;
}