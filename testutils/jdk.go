// Package testutils provides the external oracles mocha's tests check against.
//
// Every layer of the compiler is checked against something outside itself: the
// scanner and parser against golden dumps, gen and classfile against javac's
// own disassembly, target/dalvik against d8. This package holds the JDK half.
//
// Nothing here imports any other mocha package. It shells out to javac, javap
// and java, and it is the only place in the tree that knows those exist.
//
// If no JDK is present the helpers skip rather than fail, so `go test ./...`
// works on a machine without one. Set MOCHA_REQUIRE_JDK=1 to turn those skips
// into failures — CI should, because a green run that silently skipped the
// load-bearing check is worse than a red one.
package testutils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// toolTimeout bounds every JDK invocation. javac on one file is fast; a hang
// means something is wrong and a test should say so rather than block CI.
const toolTimeout = 90 * time.Second

// A JDK is a located Java toolchain.
type JDK struct {
	Home    string // JAVA_HOME, or "" when the tools came from PATH
	Javac   string
	Javap   string
	Java    string
	Release int // 21, 25, ... — the feature release, or 0 if unparsable
}

var (
	jdkOnce  sync.Once
	jdkFound *JDK
	jdkErr   error
)

// FindJDK locates javac, javap and java, preferring $JAVA_HOME/bin over PATH.
// The result is cached: tests fork enough processes already.
func FindJDK() (*JDK, error) {
	jdkOnce.Do(findJDK)
	return jdkFound, jdkErr
}

// RequireJDK returns the toolchain or skips the test.
//
// With MOCHA_REQUIRE_JDK set it fails instead. The javap diff is the only
// check that can catch a symmetric bug in the reader and writer, so a CI run
// that skipped it has not verified the encoder at all.
func RequireJDK(t testing.TB) *JDK {
	t.Helper()
	j, err := FindJDK()
	if err == nil {
		return j
	}
	if os.Getenv("MOCHA_REQUIRE_JDK") != "" {
		t.Fatalf("MOCHA_REQUIRE_JDK is set but no JDK was found: %v", err)
	}
	t.Skipf("no JDK: %v", err)
	return nil
}

func findJDK() {
	j := &JDK{}
	var missing []string

	lookup := func(name string) string {
		bin := exeName(name)
		if home := os.Getenv("JAVA_HOME"); home != "" {
			p := filepath.Join(home, "bin", bin)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				j.Home = home
				return p
			}
		}
		p, err := exec.LookPath(name)
		if err != nil {
			missing = append(missing, name)
			return ""
		}
		return p
	}

	j.Javac = lookup("javac")
	j.Javap = lookup("javap")
	j.Java = lookup("java")

	if len(missing) > 0 {
		jdkErr = fmt.Errorf("%s not found in $JAVA_HOME/bin or $PATH", strings.Join(missing, ", "))
		return
	}

	// javac reports "javac 21.0.4" or "javac 1.8.0_402". Only the feature
	// release matters, and only for gating tests on newer language features.
	out, err := run(context.Background(), "", j.Javac, "-version")
	if err != nil {
		jdkErr = fmt.Errorf("javac -version failed: %w", err)
		return
	}
	j.Release = parseRelease(out)
	jdkFound = j
}

var releaseRe = regexp.MustCompile(`(\d+)(?:\.(\d+))?`)

func parseRelease(version string) int {
	m := releaseRe.FindStringSubmatch(version)
	if m == nil {
		return 0
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	// 1.8 means 8; everything since 9 names its feature release directly.
	if major == 1 && m[2] != "" {
		minor, err := strconv.Atoi(m[2])
		if err != nil {
			return 0
		}
		return minor
	}
	return major
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// run executes a tool and returns its combined output. dir may be empty.
func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("%s timed out after %s", filepath.Base(name), toolTimeout)
	}
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w\n%s",
			filepath.Base(name), strings.Join(args, " "), err, out)
	}
	return string(out), nil
}