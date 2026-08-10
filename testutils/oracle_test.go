package classfile_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/jvm/op"
	"github.com/vertex-language/mocha/testutils"
)

// The reference source and the builder below must stay equivalent. Between
// them they exercise the pool builder, the local slot forms, the compact
// constant forms, a backward branch, iinc, a field access, an invocation, and
// the replay loop — which is most of what the encoder does.
const mainJava = `package com.example;

public class Main {
    public static void main(String[] args) {
        for (int i = 0; i < 3; i++) {
            System.out.println(i);
        }
    }
}
`

func buildMain() *classfile.Builder {
	b := classfile.NewBuilder("com/example/Main")
	b.SetSourceFile("Main.java")

	b.Method(classfile.AccPublic, "<init>", "()V").Code(func(c *classfile.CodeWriter) {
		c.Aload(0)
		c.InvokeSpecial("java/lang/Object", "<init>", "()V")
		c.Return()
	})

	b.Method(classfile.AccPublic|classfile.AccStatic, "main", "([Ljava/lang/String;)V").
		Code(func(c *classfile.CodeWriter) {
			loop, end := c.NewLabel(), c.NewLabel()

			c.Iconst(0)
			c.Istore(1)

			c.Mark(loop)
			c.Iload(1)
			c.Iconst(3)
			c.IfICmpGe(end)

			c.GetStatic("java/lang/System", "out", "Ljava/io/PrintStream;")
			c.Iload(1)
			c.InvokeVirtual("java/io/PrintStream", "println", "(I)V")

			c.Iinc(1, 1)
			c.Goto(loop)

			c.Mark(end)
			c.Return()
		})
	return b
}

func mainBytes(t *testing.T) []byte {
	t.Helper()
	out, err := buildMain().Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return out
}

// TestMatchesJavac is the load-bearing check. A round trip survives a
// symmetric bug in the reader and writer perfectly; this does not.
func TestMatchesJavac(t *testing.T) {
	jdk := testutils.RequireJDK(t)

	refDir := jdk.Compile(t, map[string]string{"com/example/Main.java": mainJava})
	want := jdk.Disassemble(t, filepath.Join(refDir, "com", "example", "Main.class"))

	outDir := t.TempDir()
	classFile := testutils.WriteClass(t, outDir, "com/example/Main", mainBytes(t))
	got := jdk.Disassemble(t, classFile)

	testutils.DiffDisassembly(t, want, got)
}

// TestFrameSizesMatchJavac covers what -c does not print. A body can
// disassemble identically and still declare a frame too small to run.
func TestFrameSizesMatchJavac(t *testing.T) {
	jdk := testutils.RequireJDK(t)

	refDir := jdk.Compile(t, map[string]string{"com/example/Main.java": mainJava})
	want := testutils.MethodSizes(jdk.Verbose(t, filepath.Join(refDir, "com", "example", "Main.class")))

	outDir := t.TempDir()
	classFile := testutils.WriteClass(t, outDir, "com/example/Main", mainBytes(t))
	got := testutils.MethodSizes(jdk.Verbose(t, classFile))

	for name, w := range want {
		g, ok := got[name]
		if !ok {
			t.Errorf("no frame sizes for %s", name)
			continue
		}
		// max_stack and max_locals are lower bounds the VM enforces, not
		// values the format pins, so a larger frame is legal. It is still a
		// bug: it means the depth model lost track.
		if g != w {
			t.Errorf("%s: frame sizes differ\njavac: %+v\nmocha: %+v", name, w, g)
		}
	}
}

// TestVerifiesAndRuns is the check with no substitute: loading the class runs
// the verifier, and running it proves the body does what it was meant to.
func TestVerifiesAndRuns(t *testing.T) {
	jdk := testutils.RequireJDK(t)

	outDir := t.TempDir()
	testutils.WriteClass(t, outDir, "com/example/Main", mainBytes(t))

	out, err := jdk.Run(t, outDir, "com.example.Main")
	if err != nil {
		// A VerifyError names a bytecode offset and points at the CodeWriter
		// call that emitted it; a ClassFormatError points at the encoder's
		// structure. Telling them apart is worth doing before debugging.
		t.Fatalf("running com.example.Main: %v", err)
	}
	if got := strings.Fields(out); len(got) != 3 || got[0] != "0" || got[1] != "1" || got[2] != "2" {
		t.Errorf("output = %q, want 0 1 2", out)
	}
}

// TestDecoderAcceptsEncoder needs no JDK. It is the weakest of the four checks
// and is here for the case where it fails while the others cannot run.
func TestDecoderAcceptsEncoder(t *testing.T) {
	out := mainBytes(t)

	c, err := classfile.Read(out, classfile.KeepRaw)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if c.Name != "com/example/Main" {
		t.Errorf("Name = %q", c.Name)
	}
	if c.Super != "java/lang/Object" {
		t.Errorf("Super = %q", c.Super)
	}
	if c.SourceFile != "Main.java" {
		t.Errorf("SourceFile = %q", c.SourceFile)
	}
	if c.Version != (classfile.Version{Major: classfile.Java5, Minor: 0}) {
		t.Errorf("Version = %s, want 49.0", c.Version)
	}

	m, ok := c.Method("main", "([Ljava/lang/String;)V")
	if !ok {
		t.Fatal("no main method")
	}
	if m.Code == nil {
		t.Fatal("main has no Code attribute")
	}

	// Walk the body. Next returning false means stopped, not finished.
	var ops []op.Op
	it := m.Code.Iter()
	for it.Next() {
		ops = append(ops, it.Instr().Op)
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iterating main: %v", err)
	}

	want := []op.Op{
		op.Iconst0, op.Istore1, op.Iload1, op.Iconst3, op.IfIcmpge,
		op.Getstatic, op.Iload1, op.Invokevirtual, op.Iinc, op.Goto, op.Return,
	}
	if len(ops) != len(want) {
		t.Fatalf("decoded %d instructions, want %d: %v", len(ops), len(want), ops)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Errorf("instruction %d = %s, want %s", i, ops[i], want[i])
		}
	}
}

// TestBackwardBranchWidening drives the replay loop. A conditional branch has
// no long form, so widening one means inverting the test and jumping over a
// goto_w — which changes that instruction's length and moves everything after
// it. Nothing else in the suite reaches this path.
func TestBackwardBranchWidening(t *testing.T) {
	b := classfile.NewBuilder("com/example/Wide")
	b.SetSourceFile("Wide.java")

	b.Method(classfile.AccPublic|classfile.AccStatic, "f", "(I)V").
		Code(func(c *classfile.CodeWriter) {
			loop, end := c.NewLabel(), c.NewLabel()
			c.Mark(loop)
			c.Iload(0)
			c.IfEq(end)
			// Pad the body past 32767 bytes so the forward branch to end
			// cannot use a signed 16-bit offset.
			for i := 0; i < 12000; i++ {
				c.Op(op.Nop)
				c.Op(op.Nop)
				c.Op(op.Nop)
			}
			c.Iinc(0, -1)
			c.Goto(loop)
			c.Mark(end)
			c.Return()
		})

	out, err := b.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	c, err := classfile.Read(out, classfile.DefaultMode)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	m, _ := c.Method("f", "(I)V")

	var sawGotoW, sawIfne bool
	it := m.Code.Iter()
	for it.Next() {
		switch it.Instr().Op {
		case op.GotoW:
			sawGotoW = true
		case op.Ifne: // ifeq inverted
			sawIfne = true
		}
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iterating f: %v", err)
	}
	if !sawIfne || !sawGotoW {
		t.Errorf("widening did not happen: inverted test %v, goto_w %v", sawIfne, sawGotoW)
	}

	if jdk, err := testutils.FindJDK(); err == nil {
		dir := t.TempDir()
		testutils.WriteClass(t, dir, "com/example/Wide", out)
		if _, err := jdk.Run(t, dir, "com.example.Wide"); err == nil {
			t.Error("expected NoSuchMethodError: the class has no main, only f")
		} else if !strings.Contains(err.Error(), "NoSuchMethodError") {
			// Anything else — VerifyError above all — means the widened
			// branch offsets are wrong.
			t.Errorf("loading the widened class failed for the wrong reason: %v", err)
		}
	}
}