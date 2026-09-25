package bindings

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/sandbox"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// readOnlyRefusal is why a read-only session refuses a host member, and so which code
// the refusal carries.
type readOnlyRefusal int

const (
	readOnlyAllowed readOnlyRefusal = iota
	// readOnlyWrites refuses a member that changes a file, a store magus keeps, or
	// something across the network, as MGS2002.
	readOnlyWrites
	// readOnlyExecs refuses a member that starts a process, as MGS2007. That includes
	// every read through a VCS binary: git refreshes its index during a status and jj
	// snapshots the working copy on any command.
	readOnlyExecs
)

// readOnlyRefused are the members a read-only session replaces with a refusal, keyed by
// native module name and the member's path inside it. TestReadOnlyClassifiesEveryMember
// fails on a member of a non-pure module that is neither here nor listed as a read.
var readOnlyRefused = map[string]readOnlyRefusal{
	"archive.compress":   readOnlyWrites,
	"archive.uncompress": readOnlyWrites,

	"crypto.signFile": readOnlyWrites,

	"fs.appendFile":      readOnlyWrites,
	"fs.chmod":           readOnlyWrites,
	"fs.copyDir":         readOnlyWrites,
	"fs.copyFile":        readOnlyWrites,
	"fs.delete":          readOnlyWrites,
	"fs.deleteDirectory": readOnlyWrites,
	"fs.deleteFile":      readOnlyWrites,
	"fs.makeDirectory":   readOnlyWrites,
	"fs.mkdirAll":        readOnlyWrites,
	"fs.move":            readOnlyWrites,
	"fs.remove":          readOnlyWrites,
	"fs.removeAll":       readOnlyWrites,
	"fs.rename":          readOnlyWrites,
	"fs.symlink":         readOnlyWrites,
	"fs.tempDir":         readOnlyWrites,
	"fs.tempFile":        readOnlyWrites,
	"fs.writeFile":       readOnlyWrites,
	"fs.writeFileAtomic": readOnlyWrites,
	"fs.writeLines":      readOnlyWrites,

	"http.download": readOnlyWrites,
	"http.post":     readOnlyWrites,
	// Refused whatever the method: http\get is the read.
	"http.request":        readOnlyWrites,
	"http.server":         readOnlyWrites,
	"http.upload_chunked": readOnlyWrites,

	"magus.affected":       readOnlyExecs,
	"magus.affectedImpact": readOnlyExecs,
	"magus.attention":      readOnlyExecs,
	"magus.bustCache":      readOnlyWrites,
	"magus.cmd":            readOnlyExecs,
	"magus.describe":       readOnlyExecs,
	"magus.describeFile":   readOnlyExecs,
	"magus.diagnoseDrift":  readOnlyExecs,
	"magus.diff":           readOnlyExecs,
	"magus.doctor":         readOnlyExecs,
	"magus.insight":        readOnlyExecs,
	"magus.job.clear":      readOnlyWrites,
	"magus.job.exit":       readOnlyWrites,
	"magus.job.put":        readOnlyWrites,
	"magus.job.register":   readOnlyWrites,
	"magus.run":            readOnlyExecs,

	"os.Socket.connect": readOnlyWrites,
	"os.TcpServer.init": readOnlyWrites,
	"os.execute":        readOnlyExecs,
	"os.tmpFilename":    readOnlyWrites,

	"pipe.diff":     readOnlyExecs,
	"pipe.exportTo": readOnlyWrites,

	"proc.exec":  readOnlyExecs,
	"proc.shell": readOnlyExecs,

	"vcs.base":         readOnlyExecs,
	"vcs.changedFiles": readOnlyExecs,
	"vcs.cmd":          readOnlyExecs,
	"vcs.commit":       readOnlyExecs,
	"vcs.describe":     readOnlyExecs,
	"vcs.dirtyDiff":    readOnlyExecs,
	"vcs.history":      readOnlyExecs,
	"vcs.isDirty":      readOnlyExecs,
	"vcs.name":         readOnlyExecs,
	"vcs.ref":          readOnlyExecs,
	"vcs.root":         readOnlyExecs,
	"vcs.status":       readOnlyExecs,
	"vcs.tags":         readOnlyExecs,
}

// readOnlyGated are the members that read or write depending on their arguments, each
// with the check that decides. Both are io's File.open(path, mode): a file opened for
// reading is a read.
var readOnlyGated = map[string]func(args []vm.Value) readOnlyRefusal{
	"io.File.open":     fileOpenRefusal,
	"iocore.File.open": fileOpenRefusal,
}

func fileOpenRefusal(args []vm.Value) readOnlyRefusal {
	if len(args) == 2 && strings.HasSuffix(args[1].String(), ".read") {
		return readOnlyAllowed
	}
	return readOnlyWrites
}

// gateCallSource defines the function a gated member's original is called through.
// Session.CallValue returns null for a native callee, whose result the VM leaves on the
// stack, but a Buzz function's return survives it.
const gateCallSource = "fun call(f: any, a: any, b: any) > any { return f(a, b); }"

// readOnlyPureModules reach nothing past the process, so a read-only session leaves them
// whole. ffi is among them because its memory API touches only buffers it allocated; the
// zdef() calls that reach native code are refused through the FFI provider instead (see
// RefuseFFI). Every magus module the playground may install is pure the same way: its only
// filesystem access is through the sandbox checks, which the read-only policy refuses.
var readOnlyPureModules = []string{"assertcore", "buffer", "cryptocore", "debug", "ffi", "gc", "math", "serialize", "std"}

// readOnlyModuleNames lists the native modules RestrictToReads walks, each flagged pure
// when a read-only session leaves it whole.
func readOnlyModuleNames() map[string]bool {
	names := map[string]bool{"magus": false}
	for _, m := range buzzstd.Modules {
		names[m.Name] = slices.Contains(readOnlyPureModules, m.Name)
	}
	for name, reg := range bindinggen.Modules {
		path := name
		if reg.Path != "" {
			path = reg.Path
		}
		// A magus module overlays Buzz's module of the same name, so the pair is pure
		// only when both halves are.
		pure := reg.Capabilities.Has(bindinggen.WASM)
		if prior, ok := names[path]; ok {
			pure = pure && prior
		}
		names[path] = pure
	}
	return names
}

// RestrictToReads replaces, on sess, every host member that writes or starts a process
// with one that raises: MGS2002 for a write to a file, a store or the network, MGS2007
// for a process. Call it after the module surface and the magus namespace are
// registered and before any code runs. Reads, stdin, stdout, stderr and computation are
// untouched.
//
// It is the interpreter-level half of `magus buzz --read-only`, and it holds on every OS.
// The sandbox.ReadOnly policy on the context is the other half: it refuses whatever a
// permitted member reaches through the fs, archive, crypto and exec checks.
//
// Only sess's own module values change; a child session shares them, so io\runFile and
// imported files are held to the same rule. An error means the session is unusable and
// nothing should run on it.
func RestrictToReads(ctx context.Context, sess *buzz.Session) error {
	// A child keeps call out of the script's globals and shares sess's modules.
	helper := sess.NewChild()
	if err := helper.Exec(ctx, gateCallSource); err != nil {
		return err
	}
	g := readOnlyGate{sess: helper, call: helper.GetGlobal("call")}
	for name := range readOnlyModuleNames() {
		if mod, ok := sess.NativeModule(name); ok {
			g.restrict(name, name, mod)
		}
	}
	return nil
}

type readOnlyGate struct {
	sess *buzz.Session
	call vm.Value
}

// restrict walks one level of mod, descending into nested maps (io\File, magus\job,
// os\Socket) under the same dotted key.
func (g readOnlyGate) restrict(module, key string, mod vm.Value) {
	for _, member := range mod.MapKeys() {
		v, _ := mod.MapGet(member)
		path := key + "." + member
		if v.IsMap() {
			g.restrict(module, path, v)
			continue
		}
		display := module + `\` + strings.TrimPrefix(path, module+".")
		if why := readOnlyRefused[path]; why != readOnlyAllowed {
			mod.MapSet(member, vm.DirectValue(display, func(context.Context, []vm.Value) (vm.Value, error) {
				return vm.Null, readOnlyError(display, why)
			}))
			continue
		}
		if gate, ok := readOnlyGated[path]; ok {
			inner := v
			mod.MapSet(member, vm.DirectValue(display, func(ctx context.Context, args []vm.Value) (vm.Value, error) {
				if why := gate(args); why != readOnlyAllowed {
					return vm.Null, readOnlyError(display, why)
				}
				return g.sess.CallValue(ctx, g.call, append([]vm.Value{inner}, args...))
			}))
		}
	}
}

func readOnlyError(member string, why readOnlyRefusal) error {
	if why == readOnlyExecs {
		return types.DiagnosticErrorf(types.ExecDenied,
			"%s refused: it starts a process, and this run is read-only (magus buzz --read-only)", member)
	}
	return types.DiagnosticErrorf(types.PathWriteDenied,
		"%s refused: it writes, and this run is read-only (magus buzz --read-only)", member)
}

// WithReadOnlyPolicy returns ctx carrying sandbox.ReadOnly of the policy already on it,
// or of none: the policy a RestrictToReads session runs under.
func WithReadOnlyPolicy(ctx context.Context) context.Context {
	return sandbox.WithPolicy(ctx, sandbox.ReadOnly(sandbox.PolicyFromContext(ctx)))
}

// ApplyReadOnlyKernel is sandbox.ApplyReadOnly for a one-shot command: where landlock is
// unavailable it logs that at debug and returns nil, since RestrictToReads and the policy
// still hold there. Any other failure is returned, and the caller must not run the script.
func ApplyReadOnlyKernel(ctx context.Context) error {
	err := sandbox.ApplyReadOnly()
	if errors.Is(err, sandbox.ErrUnsupported) {
		slog.DebugContext(ctx, "kernel landlock unavailable; read-only holds at the interpreter level only",
			slog.String("reason", err.Error()))
		return nil
	}
	return err
}

// refusingFFI is an FFI backend that binds nothing: a native library call can write
// anything, and no policy can see inside one.
type refusingFFI struct{}

func (refusingFFI) OpenLibrary(libname string, _ []vm.CFuncSig) (vm.Value, error) {
	return vm.Null, types.DiagnosticErrorf(types.ExecDenied,
		"zdef(%q) refused: it calls native code, and this run is read-only (magus buzz --read-only)", libname)
}

// RefuseFFI makes every zdef() in the process raise MGS2007 until restore runs. The FFI
// backend is process-wide, so this belongs to a one-shot command such as `magus buzz
// --read-only`, never to a server running other sessions.
func RefuseFFI() (restore func()) {
	prev := vm.GetFFIProvider()
	vm.SetFFIProvider(refusingFFI{})
	return func() { vm.SetFFIProvider(prev) }
}
