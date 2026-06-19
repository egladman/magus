package spell

import (
	"context"
	"testing"

	"github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolve builds a bare session with the magus/target types registered, execs
// src, and resolves its spec in the given mode — the same setup Extract
// uses, but with the mode passed explicitly so the HandlerOps branch can be
// exercised without a host-importing spell.
func resolve(t *testing.T, src string, mode ResolveMode) (Descriptor, error) {
	t.Helper()
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	sess.SetSourceModule(TargetModulePath, TargetModuleSource)
	require.NoError(t, sess.Exec(ctx, src), "exec")
	return Resolve(ctx, sess, mode)
}

// The cases below drive Resolve with the mode auto-selected from the source
// (CommandOrHandlerOps), exactly as the spell loader does — the bare-session
// extraction path a built-in or self-contained fork spell takes.

func TestResolve_GetName(t *testing.T) {
	const src = `export fun mgs_getName() > str { return "testspell"; }`
	spec, err := resolve(t, src, CommandOrHandlerOps(src))
	require.NoError(t, err)
	assert.Equal(t, "testspell", spec.Name)
}

func TestResolve_MissingGetName(t *testing.T) {
	const src = `var x: int = 1;`
	_, err := resolve(t, src, CommandOrHandlerOps(src))
	assert.Error(t, err, "expected error for missing mgs_getName")
}

func TestResolve_RecordTargets(t *testing.T) {
	src := `
export fun mgs_getName() > str { return "mypkg"; }
export fun mgs_listTargets() > any {
    return {"build": {"cmd": "echo", "args": ["ok"]}};
}
`
	spec, err := resolve(t, src, CommandOrHandlerOps(src))
	require.NoError(t, err)
	assert.Equal(t, "mypkg", spec.Name)
	assert.Contains(t, spec.Ops, "build", "Targets[\"build\"] missing")
}

// TestResolve_FunctionValueTargets verifies the strictly-typed fork form:
// mgs_listTargets returning {str: fun(Target, fun(any)) bool} handlers, referenced
// by value, that hand a {cmd, args, charms} record to the magus-injected cb callback.
// A self-contained (fork) spell's handlers are called once at resolution to record
// their specs, so the result decodes to the same fork targets a plain data form
// would — proving the typed form is behaviorally identical to the old form.
func TestResolve_FunctionValueTargets(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "fnpkg"; }
fun build(t: Target, cb: fun(any)) > bool { cb({"cmd": "go", "args": ["build"]}); return true; }
fun fmt(t: Target, cb: fun(any)) > bool {
    cb({"cmd": "gofmt", "args": ["-l", "."], "charms": {"write": {"ops": [{"op": "replace", "path": "/0", "value": "-w"}]}}}); return true;
}
export fun mgs_listTargets() > {str: fun(Target, fun(any)) bool} {
    return {"build": build, "fmt": fmt};
}
`
	spec, err := resolve(t, src, CommandOrHandlerOps(src))
	require.NoError(t, err)
	b := spec.Ops["build"]
	assert.Equal(t, "go", b.Cmd)
	assert.Equal(t, []string{"build"}, b.Args)

	f := spec.Ops["fmt"]
	assert.Equal(t, "gofmt", f.Cmd)
	ch, ok := f.Charms["write"]
	require.Truef(t, ok, "fmt missing charm \"write\": %+v", f)
	want := types.PatchOp{Op: "replace", Path: "/0", Value: "-w"}
	assert.Equal(t, []types.PatchOp{want}, ch.Ops)
}

// TestResolve_HandlerOpRecordsHandlerName pins that a function-op is
// dispatched by its handler's real exported name, recovered from the function
// value — not by the op-map key. A `"deploy": shipIt` entry must record shipIt,
// so invoke-time lookup (Exports()[fn]) finds the right handler even when the key
// differs from the handler name.
func TestResolve_HandlerOpRecordsHandlerName(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "fnpkg"; }
export fun enabled(tg: Target, cb: fun(any)) > bool { return true; }
export fun shipIt(tg: Target, cb: fun(any)) > bool { return true; }
export fun mgs_listTargets() > {str: fun(Target, fun(any)) bool} {
    return {"enabled": enabled, "deploy": shipIt};
}
`
	spec, err := resolve(t, src, HandlerOps)
	require.NoError(t, err)
	assert.Equal(t, "enabled", spec.Ops["enabled"].Func)
	assert.Equal(t, "shipIt", spec.Ops["deploy"].Func, "Func should be handler name, not op key")
}

// TestResolve_HandlerOpUnexportedHandler pins that referencing a
// non-exported handler fails at resolution, not silently at invoke time — the
// invoke path can only look up exported names.
func TestResolve_HandlerOpUnexportedHandler(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "fnpkg"; }
fun helper(tg: Target, cb: fun(any)) > bool { return true; }
export fun mgs_listTargets() > {str: fun(Target, fun(any)) bool} {
    return {"build": helper};
}
`
	_, err := resolve(t, src, HandlerOps)
	require.Error(t, err)
	assert.ErrorContains(t, err, "not exported")
}

// TestResolve_CommandRejectsMultipleRun pins that a fork handler calling cb
// more than once is an error, not a silently dropped earlier command.
func TestResolve_CommandRejectsMultipleRun(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "forkpkg"; }
export fun build(tg: Target, cb: fun(any)) > bool {
    cb({"cmd": "echo", "args": ["a"]});
    cb({"cmd": "echo", "args": ["b"]});
    return true;
}
export fun mgs_listTargets() > {str: fun(Target, fun(any)) bool} {
    return {"build": build};
}
`
	_, err := resolve(t, src, CommandOps)
	require.Error(t, err)
	assert.ErrorContains(t, err, "exactly once")
}

// TestResolve_CommandCapturesHandlerDoc pins that a fork handler's doc comment —
// the comment block directly above its `fun` declaration — is captured onto the
// target's Doc, while an undocumented handler and one separated by a blank line
// carry none. This is the data `magus describe` prints and `magus doctor` enforces.
func TestResolve_CommandCapturesHandlerDoc(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "forkpkg"; }

// build compiles the project.
fun build(tg: Target, run: any) > bool { return run({"cmd": "echo", "args": ["a"]}); }

fun test(tg: Target, run: any) > bool { return run({"cmd": "echo", "args": ["b"]}); }

// stray comment with a blank line below — not a doc comment.

fun lint(tg: Target, run: any) > bool { return run({"cmd": "echo", "args": ["c"]}); }

export fun mgs_listTargets() > {str: fun(Target, any) bool} {
    return {"build": build, "test": test, "lint": lint};
}
`
	spec, err := resolve(t, src, CommandOps)
	require.NoError(t, err)
	assert.Equal(t, "build compiles the project.", spec.Ops["build"].Doc)
	assert.Empty(t, spec.Ops["test"].Doc, "undocumented handler should carry no doc")
	assert.Empty(t, spec.Ops["lint"].Doc, "blank line breaks the doc block")
}

// TestResolve_DocTargetsExcludesRecordOps pins that only function-authored
// targets land in DocTargets (the doctor's doc-comment scope); a plain
// {cmd,args} record op does not, so it is never required to carry a comment.
func TestResolve_DocTargetsExcludesRecordOps(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "mixed"; }

// build is a function handler.
fun build(tg: Target, run: any) > bool { return run({"cmd": "echo", "args": ["a"]}); }

export fun mgs_listTargets() > any {
    return {
        "build": build,
        "lint": {"cmd": "echo", "args": ["b"]},
    };
}
`
	spec, err := resolve(t, src, CommandOps)
	require.NoError(t, err)
	assert.Equal(t, []string{"build"}, spec.DocOps, "record op 'lint' should be excluded")
}

// TestResolve_HandlerOpCapturesHandlerDoc pins doc capture for the function-op
// form too: an `export fun` handler's preceding comment lands on the target Doc.
func TestResolve_HandlerOpCapturesHandlerDoc(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "fnpkg"; }

// deploy ships the build to production.
export fun deploy(tg: Target, p: any) > bool { return true; }

export fun mgs_listTargets() > {str: fun(Target, any) bool} {
    return {"deploy": deploy};
}
`
	spec, err := resolve(t, src, HandlerOps)
	require.NoError(t, err)
	assert.Equal(t, "deploy ships the build to production.", spec.Ops["deploy"].Doc)
}

// TestResolve_CommandRejectsTargetRead pins that a fork handler that reads
// the Target fails at resolution (the Target is null there) rather than silently
// recording a command built from empty fields.
func TestResolve_CommandRejectsTargetRead(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "forkpkg"; }
export fun build(tg: Target, cb: fun(any)) > bool {
    cb({"cmd": "echo", "args": [tg.name]});
    return true;
}
export fun mgs_listTargets() > {str: fun(Target, fun(any)) bool} {
    return {"build": build};
}
`
	_, err := resolve(t, src, CommandOps)
	assert.Error(t, err, "expected error for a handler reading the Target")
}
