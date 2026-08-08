// Exercises: record declarations (compact constructor, generic, nested),
// type patterns, record deconstruction, unnamed patterns, `when` guards and
// a `case null` label.
package sample;

import java.util.List;
import java.util.Objects;

class RecordsPatterns {

    sealed interface Shape permits Circle, Rectangle, Line {}

    record Point(int x, int y) {
        Point {
            if (x < 0 || y < 0) throw new IllegalArgumentException("negative");
        }

        Point(int both) {
            this(both, both);
        }

        static Point origin() {
            return new Point(0, 0);
        }

        int manhattan() {
            return Math.abs(x) + Math.abs(y);
        }
    }

    record Circle(Point centre, double radius) implements Shape {}
    record Rectangle(Point topLeft, Point bottomRight) implements Shape {}
    record Line(Point from, Point to) implements Shape {}

    record Pair<A, B>(A first, B second) {
        <C> Pair<A, C> with(C other) {
            return new Pair<>(first, other);
        }
    }

    static String describe(Object o) {
        if (o instanceof String s && !s.isEmpty()) {
            return "string of " + s.length();
        }
        if (o instanceof Point(int x, int y)) {
            return "point " + x + "," + y;
        }
        if (o instanceof Circle(Point(var cx, var cy), double r) && r > 0) {
            return "circle at " + cx + "," + cy;
        }
        if (!(o instanceof Integer i)) {
            return "other";
        }
        return "int " + i;
    }

    static double area(Shape shape) {
        return switch (shape) {
            case Circle c when c.radius() > 100 -> Double.MAX_VALUE;
            case Circle(Point _, double r) -> Math.PI * r * r;
            case Rectangle(Point(var x1, var y1), Point(var x2, var y2)) ->
                    Math.abs((x2 - x1) * (y2 - y1));
            case Line _ -> 0.0;
        };
    }

    static String nullable(Object o) {
        return switch (o) {
            case null -> "null";
            case String s when s.isBlank() -> "blank";
            case String s -> s;
            case List<?> l -> "list of " + l.size();
            default -> Objects.toString(o);
        };
    }
}