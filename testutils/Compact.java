// Exercises: File.Compact — members at top level with no enclosing class
// (JEP 512), a module import (JEP 511), an instance main. No package
// declaration: a compact source file is in the unnamed package.
import module java.base;

int calls = 0;

String greeting(String who) {
    calls++;
    return "Sample, " + who + ".";
}

void main() {
    IO.println(greeting("Mocha"));
    IO.println("calls: " + calls);
}