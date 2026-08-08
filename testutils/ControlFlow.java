// Exercises: every statement form, both switch shapes (Rules vs Groups),
// labels, try-with-resources, multi-catch, and a stray semicolon.
package sample;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.StringReader;
import java.util.List;

class ControlFlow {

    static int loops(List<String> items) {
        int n = 0;

        for (int i = 0, j = items.size(); i < j; i++, j--) {
            n += i;
        }

        for (String s : items) {
            if (s.isEmpty()) continue;
            n += s.length();
        }

        while (n > 100) n /= 2;

        do {
            n++;
        } while (n < 10);

        for (;;) {
            break;
        }

        outer:
        for (int i = 0; i < 3; i++) {
            for (int j = 0; j < 3; j++) {
                if (j == 2) continue outer;
                if (i == 2) break outer;
                n++;
            }
        }
        return n;
    }

    // Colon form: groups, fallthrough, a block-bodied group.
    static String colonSwitch(int day) {
        String name;
        switch (day) {
            case 1:
            case 7:
                name = "weekend";
                break;
            case 2:
            case 3:
            case 4: {
                name = "weekday";
                break;
            }
            default:
                name = "unknown";
        }
        return name;
    }

    // Arrow form: rules, a multi-label rule, a block rule with `yield`.
    static int arrowSwitch(String s) {
        return switch (s) {
            case "one" -> 1;
            case "two", "three" -> 2;
            case "many" -> {
                int k = s.length();
                yield k * 10;
            }
            default -> 0;
        };
    }

    static String exceptions(String text) {
        try (BufferedReader a = new BufferedReader(new StringReader(text));
             BufferedReader b = new BufferedReader(new StringReader(text))) {
            return a.readLine() + b.readLine();
        } catch (IOException | RuntimeException e) {
            return e.getMessage();
        } finally {
            System.out.print("");
        }
    }

    static void raise(boolean b) {
        if (b) throw new IllegalStateException("nope");
        else if (!b) return;
    }

    static synchronized void guarded(Object lock) {
        synchronized (lock) {
            assert lock != null : "lock";
            ;   // EmptyStmt — kept, so a formatter does not have to invent one
        }
    }
}