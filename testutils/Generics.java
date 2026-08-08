// Exercises: nested type arguments closing on separate `>` tokens, wildcards,
// explicit type arguments, and `<` / `>` that are *not* type arguments sitting
// next to ones that are.
package sample;

import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

class Generics<T extends Comparable<? super T> & Cloneable> {

    private final List<List<String>> nested = new ArrayList<>();
    private final Map<String, List<Map<Integer, String>>> deep = new HashMap<>();
    private final List<? extends Number> covariant = List.of(1, 2, 3);
    private final List<? super Integer> contravariant = new ArrayList<Number>();
    private final List<?> unbounded = List.of();

    static <E> List<E> singleton(E e) {
        return List.of(e);
    }

    static <K, V extends Comparable<V>> Map<K, V> emptyMap() {
        return new HashMap<K, V>();
    }

    <E> List<E> identity(List<E> in) {
        return in;
    }

    void explicitTypeArguments() {
        List<String> a = Generics.<String>singleton("x");
        List<String> b = this.<String>identity(a);
        Comparator<String> c = Comparator.<String, Integer>comparing(String::length);
    }

    // Speculation fodder: `<` opening type arguments versus less-than.
    static boolean ambiguous(int x, int y, int z, int w) {
        boolean lt = x < y;
        boolean both = x < y && z > w;
        int shifted = (x < y ? x : y) >> 1;
        List<List<String>> ls = new ArrayList<List<String>>();
        return lt && both && shifted >= 0 && ls.isEmpty();
    }

    T[] copy(T[] proto) {
        return proto.clone();
    }
}