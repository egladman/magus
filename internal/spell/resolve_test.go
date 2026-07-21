package spell

import (
	"context"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolve builds a bare session with the magus/target types registered, execs
// src, and resolves its spec — the same setup Extract uses. Every op resolves to
// its declared command.
func resolve(t *testing.T, src string) (Descriptor, error) {
	t.Helper()
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	sess.SetSourceModule(TargetModulePath, builtinModuleSources[TargetModulePath])
	require.NoError(t, sess.Exec(ctx, src), "exec")
	return Resolve(ctx, sess)
}

func TestResolve_GetName(t *testing.T) {
	const src = `export fun mgs_getName() > str { return "testspell"; }`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Equal(t, "testspell", spec.Name)
}

func TestResolve_MissingGetName(t *testing.T) {
	const src = `var x: int = 1;`
	_, err := resolve(t, src)
	assert.Error(t, err, "expected error for missing mgs_getName")
}

func TestResolve_RecordTargets(t *testing.T) {
	src := `
export fun mgs_getName() > str { return "mypkg"; }
export fun mgs_listTargets() > any {
    return {"build": {"bin": "echo", "args": ["ok"]}};
}
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Equal(t, "mypkg", spec.Name)
	assert.Contains(t, spec.Ops, "build", "Targets[\"build\"] missing")
}

// TestResolve_FunctionValueTargets verifies the op form: mgs_listTargets returning
// {str: fun(Target) Command} handlers, referenced by value, each returning the
// {bin, args, charms} Command it declares. Handlers are called once at resolution to
// record their commands, so the result decodes to the same targets a plain data form
// would — proving the function form is behaviorally identical to a record.
func TestResolve_FunctionValueTargets(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "fnpkg"; }
fun build(t: Target) > Command { return Command{bin = "go", args = ["build"]}; }
fun fmt(t: Target) > Command {
    return Command{bin = "gofmt", args = ["-l", "."], charms = {"write": {"ops": [{"op": "replace", "path": "/0", "value": "-w"}]}}};
}
export fun mgs_listTargets() > {str: fun(Target) Command} {
    return {"build": build, "fmt": fmt};
}
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	b := spec.Ops["build"]
	assert.Equal(t, "go", b.Bin)
	assert.Equal(t, []string{"build"}, b.Args)

	f := spec.Ops["fmt"]
	assert.Equal(t, "gofmt", f.Bin)
	ch, ok := f.Charms["write"]
	require.Truef(t, ok, "fmt missing charm \"write\": %+v", f)
	want := types.PatchOp{Op: "replace", Path: "/0", Value: "-w"}
	assert.Equal(t, []types.PatchOp{want}, ch.Ops)
}

// TestResolve_ServiceAndCommandCoexist proves op-level kind: one spell mixes a
// command op (returns Command, run to completion) and a service op (returns Service,
// a long-running process `magus run` blocks on) under one name. The service op's
// embedded Command mirrors Service.Command so every fork/render/cache path reads it
// uniformly.
func TestResolve_ServiceAndCommandCoexist(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "node"; }
fun nodeBuild(t: Target) > Command { return Command{bin = "npm", args = ["run", "build"]}; }
fun nodeServe(t: Target) > Service {
    return Service{ command   = Command{bin = "npm", args = ["run", "dev"]},
                   readiness = Command{bin = "curl", args = ["-sf", "http://localhost:5173"]} };
}
export fun mgs_listTargets() > any { return {"build": nodeBuild, "serve": nodeServe}; }
`
	spec, err := resolve(t, src)
	require.NoError(t, err)

	build := spec.Ops["build"]
	assert.Equal(t, types.OpKindCommand, build.OpKind())
	assert.Equal(t, "npm", build.Bin)
	assert.Equal(t, []string{"run", "build"}, build.Args)
	assert.Nil(t, build.Service, "a command op has no Service")

	// Only the service op is reported as a service target (drives uncached-at-run).
	assert.Equal(t, []string{"serve"}, spec.ServiceOpNames())

	serve := spec.Ops["serve"]
	assert.Equal(t, types.OpKindService, serve.OpKind())
	assert.True(t, serve.IsService())
	require.NotNil(t, serve.Service)
	assert.Equal(t, "npm", serve.Service.Command.Bin)
	assert.Equal(t, []string{"run", "dev"}, serve.Service.Command.Args)
	// Optional readiness probe decodes when provided.
	assert.Equal(t, "curl", serve.Service.Readiness.Bin)
	// stop is optional and omitted here, so it stays the empty Command.
	assert.Equal(t, "", serve.Service.Stop.Bin)
	// The embedded Command mirrors Service.Command so existing paths read it uniformly.
	assert.Equal(t, serve.Service.Command.Bin, serve.Bin)
	assert.Equal(t, serve.Service.Command.Args, serve.Args)
}

// TestResolve_ServiceDistinctAndIdle pins that the optional distinct (justified
// dedup opt-out) and idle (per-service idle-timeout override) fields decode from
// the Buzz object Service into types.Service.
func TestResolve_ServiceDistinctAndIdle(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "db"; }
fun pg(t: Target) > Service {
    return Service{ command  = Command{bin = "docker", args = ["run", "postgres:16"]},
                   distinct = "pins PG 16 for the 15 to 16 migration test",
                   idle     = "45m" };
}
export fun mgs_listTargets() > any { return {"pg": pg}; }
`
	spec, err := resolve(t, src)
	require.NoError(t, err)

	pg := spec.Ops["pg"]
	require.NotNil(t, pg.Service)
	assert.Equal(t, "pins PG 16 for the 15 to 16 migration test", pg.Service.Distinct)
	assert.Equal(t, "45m", pg.Service.Idle)
}

// TestResolve_DetachedServiceRejected pins the kind-coherence ward (MGS5002): a
// service op that detaches (docker run -d) is rejected at resolution, before
// anything forks, because detaching breaks foreground supervision.
func TestResolve_DetachedServiceRejected(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "db"; }
fun pg(t: Target) > Service {
    return Service{ command = Command{bin = "docker", args = ["run", "-d", "postgres:16"]} };
}
export fun mgs_listTargets() > any { return {"pg": pg}; }
`
	_, err := resolve(t, src)
	require.Error(t, err)
	assert.ErrorIs(t, err, types.ServiceOpDetached)
}

// TestResolve_NonCommandOpRejected pins the new invariant: every op is a command,
// so a function op that declares no command (returns a value without handing/
// returning a Run) is rejected at resolution rather than silently becoming a
// no-op. In-VM work (a cache backend's enabled/get/put) is no longer an op kind;
// it lives on a spell's plain exported functions, not in mgs_listTargets.
func TestResolve_NonCommandOpRejected(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "fnpkg"; }
export fun enabled(tg: Target, cb: fun(any)) > bool { return true; }
export fun mgs_listTargets() > {str: fun(Target, fun(any)) bool} {
    return {"enabled": enabled};
}
`
	_, err := resolve(t, src)
	require.Error(t, err)
	assert.ErrorContains(t, err, "return `Command{...}`")
}

// TestResolve_CommandCapturesHandlerDoc pins that an op handler's doc comment —
// the comment block directly above its `fun` declaration — is captured onto the
// target's Doc, while an undocumented handler and one separated by a blank line
// carry none. This is the data `magus describe` prints and `magus doctor` enforces.
func TestResolve_CommandCapturesHandlerDoc(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "forkpkg"; }

// build compiles the project.
fun build(tg: Target) > Command { return Command{bin = "echo", args = ["a"]}; }

fun test(tg: Target) > Command { return Command{bin = "echo", args = ["b"]}; }

// stray comment with a blank line below — not a doc comment.

fun lint(tg: Target) > Command { return Command{bin = "echo", args = ["c"]}; }

export fun mgs_listTargets() > {str: fun(Target) Command} {
    return {"build": build, "test": test, "lint": lint};
}
`
	spec, err := resolve(t, src)
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
fun build(tg: Target) > Command { return Command{bin = "echo", args = ["a"]}; }

export fun mgs_listTargets() > any {
    return {
        "build": build,
        "lint": {"bin": "echo", "args": ["b"]},
    };
}
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Equal(t, []string{"build"}, spec.DocOps, "record op 'lint' should be excluded")
}

// TestResolve_CommandRejectsTargetRead pins that an op handler that reads the
// Target fails at resolution (the Target is null there) rather than silently
// recording a command built from empty fields.
func TestResolve_CommandRejectsTargetRead(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "forkpkg"; }
export fun build(tg: Target) > Command { return Command{bin = "echo", args = [tg.name]}; }
export fun mgs_listTargets() > {str: fun(Target) Command} {
    return {"build": build};
}
`
	_, err := resolve(t, src)
	assert.Error(t, err, "expected error for a handler reading the Target")
}
