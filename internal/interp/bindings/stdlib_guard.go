package bindings

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/sandbox"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// upstreamCheck names which string arguments of one of Buzz's own stdlib functions are
// paths, and whether each is read or written.
type upstreamCheck struct {
	reads, writes []int
}

// upstreamPathChecks lists the functions in Buzz's stdlib that reach the filesystem by a
// path argument. gopherbuzz implements them to match upstream Buzz, so they know nothing
// of magus's sandbox; under a policy each is checked first, then run unchanged. fs.exists
// is here although magus's own fs.exists replaces it, so dropping that overlay would not
// open a hole.
var upstreamPathChecks = map[string]map[string]upstreamCheck{
	"fs": {
		"makeDirectory":   {writes: []int{0}},
		"delete":          {writes: []int{0}},
		"deleteFile":      {writes: []int{0}},
		"deleteDirectory": {writes: []int{0}},
		"move":            {writes: []int{0, 1}},
		"list":            {reads: []int{0}},
		"exists":          {reads: []int{0}},
		"modified":        {reads: []int{0}},
	},
	"io": {
		"runFile": {reads: []int{0}},
	},
	// gopherbuzz binds the same io module a second time under this name.
	"iocore": {
		"runFile": {reads: []int{0}},
	},
}

// upstreamGuard runs in place of an upstream function while a sandbox policy is on ctx;
// orig calls the upstream function itself.
type upstreamGuard func(ctx context.Context, args []vm.Value, orig func() (vm.Value, error)) (vm.Value, error)

// guardUpstreamStdlib puts magus's sandbox in front of the parts of Buzz's stdlib that
// reach the filesystem, the environment or a process: os.env, os.execute, io.File.open
// and the functions in upstreamPathChecks. It runs after Buzz's stdlib binds and before
// magus's modules overlay it.
//
// With no policy on the call's ctx each wrapper calls straight through, so a script
// outside the sandbox sees upstream Buzz exactly. Under one, os.env reads through the env
// allowlist as env.lookup does, os.execute forks through run.Exec (exec check, scrubbed
// env, no proc socket), and a path is checked the way magus's fs module checks it.
func guardUpstreamStdlib(sess *buzz.Session) {
	caller := newUpstreamCaller(sess)
	for module, fns := range upstreamPathChecks {
		mod, ok := sess.NativeModule(module)
		if !ok {
			continue
		}
		for name, check := range fns {
			caller.wrap(mod, module+"."+name, name, func(ctx context.Context, args []vm.Value, orig func() (vm.Value, error)) (vm.Value, error) {
				if err := checkUpstreamPaths(ctx, module+"."+name, check, args); err != nil {
					return vm.Null, err
				}
				return orig()
			})
		}
	}
	if osMod, ok := sess.NativeModule("os"); ok {
		caller.wrap(osMod, "os.env", "env", upstreamEnv)
		caller.wrap(osMod, "os.execute", "execute", upstreamExecute)
	}
	for _, module := range []string{"io", "iocore"} {
		ioMod, ok := sess.NativeModule(module)
		if !ok {
			continue
		}
		if file, ok := ioMod.MapGet("File"); ok && file.IsMap() {
			caller.wrap(file, "File.open", "open", upstreamFileOpen)
		}
	}
}

// upstreamCaller calls the upstream functions it wrapped. Through a Buzz function rather
// than Session.CallValue directly: CallValue on a native function drops its result, since
// the VM it runs has no frame to return one from.
type upstreamCaller struct {
	sess *buzz.Session
	// trampolines[n] calls its first argument with the n after it.
	trampolines [3]vm.Value
	// err is why the trampolines did not compile. Every wrapped call then fails with it:
	// the alternative is leaving the upstream function unguarded.
	err error
}

// upstreamTrampolineNames are declared into the session's shared scope, so a script that
// declares one of these names itself collides with it.
var upstreamTrampolineNames = [3]string{"magusStdlibGuardCall0", "magusStdlibGuardCall1", "magusStdlibGuardCall2"}

// newUpstreamCaller compiles the trampolines. Up front, before any script runs, because
// compiling into the shared scope from inside a running call is not something a session
// promises to survive. Declared and read back rather than evaluated: strict mode refuses
// the top-level return an expression would need to hand its value back.
func newUpstreamCaller(sess *buzz.Session) *upstreamCaller {
	c := &upstreamCaller{sess: sess}
	for n, src := range []string{
		"fun %s(f: any) > any { return f(); }",
		"fun %s(f: any, a: any) > any { return f(a); }",
		"fun %s(f: any, a: any, b: any) > any { return f(a, b); }",
	} {
		name := upstreamTrampolineNames[n]
		if _, err := sess.Eval(context.Background(), fmt.Sprintf(src, name)); err != nil {
			c.err = fmt.Errorf("magus: stdlib guard: %w", err)
			return c
		}
		if c.trampolines[n] = sess.GetGlobal(name); !c.trampolines[n].IsFun() {
			c.err = fmt.Errorf("magus: stdlib guard: %s did not bind", name)
			return c
		}
	}
	return c
}

// wrap replaces mod[name] with a function that runs guard under a sandbox policy and the
// original otherwise. A name mod does not hold is left alone.
func (c *upstreamCaller) wrap(mod vm.Value, label, name string, guard upstreamGuard) {
	fn, ok := mod.MapGet(name)
	if !ok || !fn.IsFun() {
		return
	}
	mod.MapSet(name, vm.DirectValue(label, func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		orig := func() (vm.Value, error) { return c.call(ctx, fn, args) }
		if sandbox.PolicyFromContext(ctx) == nil {
			return orig()
		}
		return guard(ctx, args, orig)
	}))
}

func (c *upstreamCaller) call(ctx context.Context, fn vm.Value, args []vm.Value) (vm.Value, error) {
	if c.err != nil {
		return vm.Null, c.err
	}
	// None of the wrapped functions reads past its second argument.
	args = args[:min(len(args), len(c.trampolines)-1)]
	return c.sess.CallValue(ctx, c.trampolines[len(args)], append([]vm.Value{fn}, args...))
}

// upstreamEnv is os.env read through the sandbox's env allowlist: a stripped name reads
// as unset, the answer env.lookup gives.
func upstreamEnv(ctx context.Context, args []vm.Value, orig func() (vm.Value, error)) (vm.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return orig() // upstream words the usage error
	}
	v, found, err := std.EnvLookup(ctx, args[0].AsString())
	if err != nil || !found {
		return vm.Null, err
	}
	return vm.StrValue(v), nil
}

// upstreamExecute is os.execute forked through run.Exec, so the child gets the exec check,
// the scrubbed environment and no proc socket. Upstream handed it this process's whole
// environment, MAGUS_PROC_SOCKET included.
func upstreamExecute(ctx context.Context, args []vm.Value, orig func() (vm.Value, error)) (vm.Value, error) {
	argv, ok := executeArgv(args)
	if !ok {
		return orig()
	}
	cwd, _ := std.CwdFromContext(ctx)
	res, err := run.Exec(ctx, argv[0], argv[1:], run.ExecOptions{Dir: cwd})
	if res.Started {
		return vm.IntValue(int64(res.Code)), nil
	}
	if errors.Is(err, types.ExecDenied) {
		return vm.Null, err
	}
	return vm.Null, fmt.Errorf("os.execute: %w", err)
}

// upstreamFileOpen checks File.open's path for the access its mode takes: read reads,
// write creates or truncates, update does both.
func upstreamFileOpen(ctx context.Context, args []vm.Value, orig func() (vm.Value, error)) (vm.Value, error) {
	if len(args) >= 2 && args[0].IsStr() {
		check := upstreamCheck{writes: []int{0}}
		switch mode := args[1].String(); {
		case strings.HasSuffix(mode, "read"):
			check = upstreamCheck{reads: []int{0}}
		case strings.HasSuffix(mode, "update"):
			check.reads = []int{0}
		}
		if err := checkUpstreamPaths(ctx, "File.open", check, args); err != nil {
			return vm.Null, err
		}
	}
	return orig()
}

// checkUpstreamPaths runs the read and write checks c names against args. An argument
// that is not a string is left for the function itself to reject.
func checkUpstreamPaths(ctx context.Context, what string, c upstreamCheck, args []vm.Value) error {
	for _, i := range c.reads {
		if i < len(args) && args[i].IsStr() {
			if err := std.CheckRead(ctx, args[i].AsString()); err != nil {
				return fmt.Errorf("%s: %w", what, err)
			}
		}
	}
	for _, i := range c.writes {
		if i < len(args) && args[i].IsStr() {
			if err := std.CheckWrite(ctx, args[i].AsString()); err != nil {
				return fmt.Errorf("%s: %w", what, err)
			}
		}
	}
	return nil
}

// executeArgv is os.execute's [str] argument, or false when it is not one, which the
// upstream function reports in its own words.
func executeArgv(args []vm.Value) ([]string, bool) {
	if len(args) < 1 || !args[0].IsList() {
		return nil, false
	}
	items := args[0].ListItems()
	if len(items) == 0 {
		return nil, false
	}
	argv := make([]string, len(items))
	for i, it := range items {
		if !it.IsStr() {
			return nil, false
		}
		argv[i] = it.AsString()
	}
	return argv, true
}
