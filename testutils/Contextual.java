// Exercises: all seventeen contextual keywords of §3.9, each used as an
// ordinary name and — where it has one — as a keyword, in the same unit.
// Nothing here needs more than the production to disambiguate.
package sample;

import module java.base;
// import module.foo.Bar;   // a *type* import, not a module import: `module`
//                          // is only a keyword here when an identifier follows.
//                          // Left commented out because the package does not exist.

class Contextual {

    // All seventeen spellings as ordinary field names.
    int exports = 1;
    int module = 2;
    int open = 3;
    int opens = 4;
    int permits = 5;
    int provides = 6;
    int record = 7;
    int requires = 8;
    int sealed = 9;
    int to = 10;
    int transitive = 11;
    int uses = 12;
    int when = 13;
    int with = 14;
    int yield = 15;
    int var = 16;

    // ...and as method names — all but `yield`, which §3.8 forbids unqualified.
    int record() { return record; }
    int sealed() { return sealed; }
    int permits() { return permits; }
    int when() { return when; }
    int var() { return var; }

    void assignments() {
        record = 3;
        record += 1;
        record++;
        sealed = record;
        yield = sealed;
        var = yield;
        this.yield = var;
        when = record() + sealed();
    }

    void declarations() {
        // `record` followed by an identifier and then `(` or `<` — a declaration.
        record Point(int x, int y) {}
        record Boxed<T>(T value) {}

        // `record` followed by anything else — an ordinary name.
        int record = 0;
        record = new Point(1, 2).x();
        record = new Boxed<>("s").value().length();

        var inferred = new Point(3, 4);
        var lambda = (Runnable) () -> {};
        for (var i = 0; i < 3; i++) {
            inferred = new Point(i, i);
        }
    }

    int yields(int n) {
        return switch (n) {
            case 0 -> 0;
            default -> {
                int yield = n * 2;   // a variable named `yield`...
                yield yield + 1;     // ...then the statement, then the name again
            }
        };
    }

    String guards(Object o) {
        int when = 0;
        return switch (o) {
            case Integer i when i > when -> "big";
            case Integer i -> "small";
            default -> "other";
        };
    }

    sealed interface Node permits Leaf, Branch {}
    record Leaf(int value) implements Node {}
    non-sealed interface Branch extends Node {}
}