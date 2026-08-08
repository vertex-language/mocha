// Exercises: annotations interleaved with keyword modifiers in written order,
// an annotation type declaration with defaults and a trailing comma, sealed /
// non-sealed / permits, initializers, enum constant bodies, interface default
// and private methods, and local and anonymous classes.
package sample;

import java.lang.annotation.Documented;
import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

@Documented
@Retention(RetentionPolicy.RUNTIME)
@Target({ ElementType.TYPE, ElementType.METHOD, ElementType.FIELD, ElementType.TYPE_USE, })
@interface Marked {
    String value() default "";
    int order() default 0;
    Class<?>[] on() default { Object.class, };
}

@Marked("type")
public sealed abstract class Modifiers permits Modifiers.Open, Modifiers.Shut {

    public static non-sealed class Open extends Modifiers {
        @Override
        public String label() {
            return "open";
        }
    }

    static final class Shut extends Modifiers {
        @Override
        public String label() {
            return "shut";
        }
    }

    public abstract String label();

    @Marked(value = "field", order = 1)
    private static final int COUNTER = 0;

    @Deprecated
    protected transient volatile int mutable;

    static { System.out.print(""); }   // static initializer
    { this.mutable = 1; }              // instance initializer

    @Marked
    public strictfp final synchronized int compute(@Marked int a, int... rest) {
        return a + rest.length + COUNTER;
    }

    @SafeVarargs
    static <E> int count(E first, E... more) {
        return 1 + more.length;
    }

    interface Named {
        String PREFIX = "n:";

        String name();

        default String shout() {
            return helper().toUpperCase();
        }

        private String helper() {
            return PREFIX + name();
        }

        static Named of(String s) {
            return () -> s;
        }
    }

    enum Level {
        LOW("low") {
            @Override public int weight() { return 1; }
        },
        HIGH("high") {
            @Override public int weight() { return 10; }
        },
        ;

        private final String text;

        Level(String text) {
            this.text = text;
        }

        public abstract int weight();

        public String text() {
            return text;
        }
    }

    void locals() {
        class Local implements Named {
            @Override public String name() { return "local"; }
        }

        Named anonymous = new Named() {
            @Override public String name() { return "anon"; }
        };

        Runnable r = new Runnable() {
            @Override public void run() { }
        };
    }
}