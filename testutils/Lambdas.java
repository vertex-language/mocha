// Exercises: every lambda and method-reference form, plus the `(` that opens a
// lambda parameter list versus a cast versus a parenthesized expression.
package sample;

import java.util.List;
import java.util.function.BiFunction;
import java.util.function.Function;
import java.util.function.IntBinaryOperator;
import java.util.function.IntFunction;
import java.util.function.Supplier;
import java.util.function.UnaryOperator;

class Lambdas {

    interface TriFunction<A, B, C, R> {
        R apply(A a, B b, C c);
    }

    private int base = 1;

    void forms() {
        Supplier<String> none = () -> "none";
        Function<String, Integer> concise = s -> s.length();
        Function<String, Integer> parens = (s) -> s.length();
        Function<String, Integer> typed = (String s) -> s.length();
        BiFunction<Integer, Integer, Integer> vars = (var a, var b) -> a + b;
        IntBinaryOperator block = (a, b) -> {
            int sum = a + b;
            return sum * 2;
        };
        UnaryOperator<String> ternaryBody = s -> s.isEmpty() ? "" : s.trim();
        Function<Integer, Function<Integer, Integer>> curried = a -> b -> a + b;
        TriFunction<Integer, Integer, Integer, Integer> three = (a, b, c) -> a + b + c;
    }

    void methodReferences(List<String> items) {
        Function<String, Integer> unbound = String::length;      // receiver is a type
        Supplier<String> bound = items.get(0)::trim;             // receiver is an expression
        Function<Integer, Integer> onThis = this::plusBase;
        Supplier<Lambdas> ctor = Lambdas::new;
        Supplier<Inner> nestedCtor = Lambdas.Inner::new;
        IntFunction<String[]> arrayCtor = String[]::new;
        Function<String, Integer> qualified = Integer::parseInt;
        Runnable out = System.out::println;
    }

    // Three different things after a `(`.
    void ambiguity(Object o, int n) {
        int a = (n) - 1;                    // subtraction
        int b = (int) -1.5;                 // primitive cast
        String s = (String) o;              // reference cast
        Runnable r = (Runnable) () -> {};   // cast of a lambda
        Supplier<Integer> t = () -> (n);    // parenthesized body
    }

    int plusBase(int n) {
        return n + base;
    }

    static class Inner {
    }
}