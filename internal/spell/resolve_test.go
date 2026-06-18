package spell

import (
	"context"
	"testing"

	"github.com/egladman/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolve builds a bare session with the magus/target types registered, execs
// src, and resolves its spec in the given mode — the same setup Extract
// uses, but with the mode passed explicitly so the FunctionOps branch can be
// exercised without a host-importing spell.
func resolve(t *testing.T, src string, mode ResolveMode) (Spec, error) {
	t.Helper()
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	sess.SetSourceModule(TargetModulePath, TargetModuleSource)
	require.NoError(t, sess.Exec(ctx, src), "exec")
	return Resolve(ctx, sess, mode)
}

// TestResolve_FunctionOpRecordsHandlerName pins that a function-op is
// dispatched by its handler's real exported name, recovered from the function
// value — not by the op-map key. A `"deploy": shipIt` entry must record shipIt,
// so invoke-time lookup (Exports()[fn]) finds the right handler even when the key
// differs from the handler name.
func TestResolve_FunctionOpRecordsHandlerName(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "fnpkg"; }
export fun enabled(tg: Target, cb: fun(any)) > bool { return true; }
export fun shipIt(tg: Target, cb: fun(any)) > bool { return true; }
export fun mgs_listTargets() > {str: fun(Target, fun(any)) bool} {
    return {"enabled": enabled, "deploy": shipIt};
}
`
	spec, err := resolve(t, src, FunctionOps)
	require.NoError(t, err)
	assert.Equal(t, "enabled", spec.Ops["enabled"].Func)
	assert.Equal(t, "shipIt", spec.Ops["deploy"].Func, "Func should be handler name, not op key")
}

// TestResolve_FunctionOpUnexportedHandler pins that referencing a
// non-exported handler fails at resolution, not silently at invoke time — the
// invoke path can only look up exported names.
func TestResolve_FunctionOpUnexportedHandler(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "fnpkg"; }
fun helper(tg: Target, cb: fun(any)) > bool { return true; }
export fun mgs_listTargets() > {str: fun(Target, fun(any)) bool} {
    return {"build": helper};
}
`
	_, err := resolve(t, src, FunctionOps)
	require.Error(t, err)
	assert.ErrorContains(t, err, "not exported")
}

// TestResolve_ForkRejectsMultipleRun pins that a fork handler calling cb
// more than once is an error, not a silently dropped earlier command.
func TestResolve_ForkRejectsMultipleRun(t *testing.T) {
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
	_, err := resolve(t, src, ForkExtract)
	require.Error(t, err)
	assert.ErrorContains(t, err, "exactly once")
}

// TestResolve_ForkCapturesHandlerDoc pins that a fork handler's doc comment —
// the comment block directly above its `fun` declaration — is captured onto the
// target's Doc, while an undocumented handler and one separated by a blank line
// carry none. This is the data `magus describe` prints and `magus doctor` enforces.
func TestResolve_ForkCapturesHandlerDoc(t *testing.T) {
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
	spec, err := resolve(t, src, ForkExtract)
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
	spec, err := resolve(t, src, ForkExtract)
	require.NoError(t, err)
	assert.Equal(t, []string{"build"}, spec.DocOps, "record op 'lint' should be excluded")
}

// TestResolve_FunctionOpCapturesHandlerDoc pins doc capture for the function-op
// form too: an `export fun` handler's preceding comment lands on the target Doc.
func TestResolve_FunctionOpCapturesHandlerDoc(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "fnpkg"; }

// deploy ships the build to production.
export fun deploy(tg: Target, p: any) > bool { return true; }

export fun mgs_listTargets() > {str: fun(Target, any) bool} {
    return {"deploy": deploy};
}
`
	spec, err := resolve(t, src, FunctionOps)
	require.NoError(t, err)
	assert.Equal(t, "deploy ships the build to production.", spec.Ops["deploy"].Doc)
}

// TestResolve_ForkRejectsTargetRead pins that a fork handler that reads
// the Target fails at resolution (the Target is null there) rather than silently
// recording a command built from empty fields.
func TestResolve_ForkRejectsTargetRead(t *testing.T) {
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
	_, err := resolve(t, src, ForkExtract)
	assert.Error(t, err, "expected error for a handler reading the Target")
}
