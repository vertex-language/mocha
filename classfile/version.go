package classfile

import "fmt"

// A Version is a class file format version. See JVMS §4.1.
type Version struct {
	Major uint16
	Minor uint16
}

// Major version numbers. The major version is the Java SE release plus 44.
const (
	Java1_0 = 45
	Java5   = 49 // CONSTANT_Class becomes loadable; Signature, annotations
	Java6   = 50 // StackMapTable
	Java7   = 51 // MethodHandle, MethodType, InvokeDynamic, BootstrapMethods
	Java8   = 52 // type annotations, MethodParameters, default methods;
	//              REF_invokeStatic/Special may name an InterfaceMethodref
	Java9  = 53 // Module, ModulePackages, ModuleMainClass
	Java11 = 55 // CONSTANT_Dynamic, NestHost, NestMembers
	Java16 = 60 // Record; last version interpreting 0x0800 as ACC_STRICT
	Java17 = 61 // PermittedSubclasses
	Java21 = 65
	Java24 = 68
	Java25 = 69
	Java26 = 70
)

// PreviewMinor marks a class file that depends on the preview features of the
// release named by its major version.
const PreviewMinor = 65535

// MaxSupported is the newest format this package will read. It is one major
// beyond the newest release a shipping VM accepts, deliberately: reading a
// format is cheaper than being unable to.
var MaxSupported = Version{Major: Java26, Minor: 0}

// PreviewRelease is the one major version whose preview files this build will
// accept. A preview class file may only be read by an implementation of that
// exact release (§4.1), so this cannot be derived from MaxSupported — that
// deliberately runs ahead of any real VM, and tying preview acceptance to it
// would reject every preview file in existence.
var PreviewRelease uint16 = Java25

func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// Java returns the Java SE release corresponding to the major version.
func (v Version) Java() int { return int(v.Major) - 44 }

// Preview reports whether the file depends on preview features.
func (v Version) Preview() bool { return v.Minor == PreviewMinor }

// AtLeast reports whether the format is major or newer. Use it to gate
// version-dependent constructs, e.g. v.AtLeast(Java7) before honouring a
// CONSTANT_MethodHandle entry.
func (v Version) AtLeast(major uint16) bool { return v.Major >= major }

// check applies the three separate version rules of JVMS §4.1.
func (v Version) check(allowPreview bool) error {
	if v.Major < Java1_0 || v.Major > MaxSupported.Major {
		return fmt.Errorf("unsupported class file version %s (this build reads %d through %d)",
			v, Java1_0, MaxSupported.Major)
	}
	// Below 56 any minor is legal; from 56 on only 0 and 65535.
	if v.Major >= 56 && v.Minor != 0 && v.Minor != PreviewMinor {
		return fmt.Errorf("illegal minor version in %s (must be 0 or %d for major %d and above)",
			v, PreviewMinor, 56)
	}
	if v.Preview() {
		// Only a VM of that exact release may read a preview file, in either
		// direction: an older release's preview format is unreadable by
		// anything newer, and a newer one is unknown to us.
		if v.Major != PreviewRelease {
			return fmt.Errorf("class file %s depends on preview features of Java SE %d, "+
				"but this build only accepts previews of Java SE %d",
				v, v.Java(), int(PreviewRelease)-44)
		}
		if !allowPreview {
			return fmt.Errorf("class file %s depends on preview features (pass AllowPreview to accept it)", v)
		}
	}
	return nil
}