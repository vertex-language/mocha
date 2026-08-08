// Exercises: precedence climbing, the joined shift operators, and the
// distinction between a primitive cast, a reference cast and a
// parenthesized expression.
package sample;

import java.io.Serializable;

class Expressions {

    static int shifts(int x) {
        int a = x >> 2;
        int b = x >>> 3;
        int c = x << 1;
        a >>= 1;
        b >>>= 2;
        c <<= 1;
        return a | b & c ^ ~x;
    }

    static boolean relations(int p, int q) {
        return p > q && p >= q || !(p < q) != (p <= q);
    }

    static int casts(double d, Object o) {
        int i = (int) -d;                                       // primitive cast: UnaryExpression
        int j = (i) - 1;                                        // parenthesized expr, then subtraction
        String s = (String) o;                                  // reference cast
        Serializable z = (Comparable<String> & Serializable) s; // intersection cast
        return i + j + s.length() + z.hashCode();
    }

    static int conditional(int n) {
        return n < 0 ? -n : n == 0 ? 0 : n * 2;
    }

    static int unary(int n) {
        int m = n++;
        m = ++n;
        m = n--;
        m = --n;
        m = +m;
        return -m;
    }

    static int arraysAndCalls(int[][] grid, String s) {
        int total = grid[0][1] + grid.length + s.trim().length();
        int[] row = { 1, 2, 3, };
        int[][] nested = { { 1 }, { 2, 3 }, };
        Object o = new int[4];
        return total + row[0] + nested[1][0] + ((int[]) o).length;
    }

    static boolean instanceOf(Object o) {
        return o instanceof String && !(o instanceof Integer);
    }
}