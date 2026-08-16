package spellruntime

import (
	"context"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolve builds a bare session with the magus/spell types registered, execs
// src, and resolves its spec  -  the same setup Extract uses. Every op resolves to
// its declared command.
func resolve(t *testing.T, src string) (spells.Descriptor, error) {
	t.Helper()
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	sess.SetModuleDecls(SpellModulePath, builtinModuleSources[SpellModulePath])
	require.NoError(t, sess.Exec(ctx, src), "exec")
	return Resolve(ctx, sess)
}

func TestResolve_GetName(t *testing.T) {
	const src = `export fun mgs_getName() > str { return "testspell"; }`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Equal(t, "testspell", spec.Name)
}

// Required globs are static spell metadata. They are generated Path objects, not
// primitive strings, and Resolve projects their lexical values once for the cache.
func TestResolve_RequiredGlobsUseGeneratedPaths(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "static-needs"; }
export fun mgs_listRequiredGlobs() > [Path] { return [Path{value = "**/*.go"}, Path{value = "go.mod"}]; }
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Equal(t, []string{"**/*.go", "go.mod"}, spec.Needs)
}

func TestResolve_RequiredGlobsRejectStrings(t *testing.T) {
	const src = `
export fun mgs_getName() > str { return "untyped-needs"; }
export fun mgs_listRequiredGlobs() > [str] { return ["**/*.go"]; }
`
	_, err := resolve(t, src)
	require.Error(t, err)
	assert.ErrorContains(t, err, "mgs_listRequiredGlobs[0] must be Path")
}

func TestResolve_IgnoreDirsRequireDirectoryPaths(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "bad-ignore"; }
export fun mgs_listIgnoreDirs() > [Path] { return [Path{value = "vendor"}]; }
`
	_, err := resolve(t, src)
	require.Error(t, err)
	assert.ErrorContains(t, err, "mgs_listIgnoreDirs[0] must set isDir = true")
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

// TestResolve_RecordTargetsSecrets verifies a record op's `secrets` map (env var
// name -> provider reference) round-trips through a real Buzz session into
// Op.Secrets  -  the by-value form a spell can declare without the typed Command
// object (gen/types/command.buzz), proving the StrMap decode path works end to end,
// not only against the Go-level test double in decode_test.go.
func TestResolve_RecordTargetsSecrets(t *testing.T) {
	src := `
export fun mgs_getName() > str { return "secretpkg"; }
export fun mgs_listTargets() > any {
    return {"publish": {"bin": "npm", "args": ["publish"], "secrets": {"NPM_TOKEN": "NPM_TOKEN"}}};
}
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"NPM_TOKEN": "NPM_TOKEN"}, spec.Ops["publish"].Secrets)
}

// TestResolve_SecretsEmptyDecodesNil pins the production normalization: an empty
// secrets map and an absent one are ONE value (nil), so both spellings share one
// descriptor serialization and one cache entry. This runs through a real Buzz
// session, exercising buzzSpellObj.StrMap plus decodeCommand's normalization - not
// the Go test double.
func TestResolve_SecretsEmptyDecodesNil(t *testing.T) {
	src := `
export fun mgs_getName() > str { return "emptysecrets"; }
export fun mgs_listTargets() > any {
    return {"publish": {"bin": "npm", "args": ["publish"], "secrets": {<str: str>}}};
}
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Nil(t, spec.Ops["publish"].Secrets)
}

// TestResolve_SecretsRejectsWrongTypedValue proves the load-time type error
// reaches a real session: a non-string value must fail resolution loudly rather
// than decode to a zero that resolves nothing at spawn.
func TestResolve_SecretsRejectsWrongTypedValue(t *testing.T) {
	src := `
export fun mgs_getName() > str { return "badsecrets"; }
export fun mgs_listTargets() > any {
    return {"publish": {"bin": "npm", "args": ["publish"], "secrets": {"NPM_TOKEN": 7}}};
}
`
	_, err := resolve(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NPM_TOKEN")
}

// TestResolve_SecretsRejectsBadEnvName: the key becomes `name=value` in the child
// environment, so a key that is not an env var name (an "=" inside, an empty
// string) would silently retarget or corrupt the environment; decode refuses it.
func TestResolve_SecretsRejectsBadEnvName(t *testing.T) {
	src := `
export fun mgs_getName() > str { return "badname"; }
export fun mgs_listTargets() > any {
    return {"publish": {"bin": "npm", "args": ["publish"], "secrets": {"A=B": "REF"}}};
}
`
	_, err := resolve(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an environment variable name")
}

// TestResolve_SecretsRejectedOnServiceOps: the supervised path rebuilds a service
// command without its env, so a secret would reach a foregrounded service and
// vanish from a supervised one. Decode refuses all three service commands until
// the supervisor threads env through.
func TestResolve_SecretsRejectedOnServiceOps(t *testing.T) {
	src := `
export fun mgs_getName() > str { return "svcsecrets"; }
export fun mgs_listTargets() > any {
    return {"serve": {"command": {"bin": "node", "args": ["server.js"], "secrets": {"API_KEY": "API_KEY"}}}};
}
`
	_, err := resolve(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported on a service op")
}

// TestResolve_FunctionValueTargets verifies the op form: mgs_listTargets returning
// {str: fun(Target) Command} handlers, referenced by value, each returning the
// {bin, args, charms} Command it declares. Handlers are called once at resolution to
// record their commands, so the result decodes to the same targets a plain data form
// would  -  proving the function form is behaviorally identical to a record.
func TestResolve_FunctionValueTargets(t *testing.T) {
	src := `
import "magus/spell";
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
	want := spells.PatchOp{Op: "replace", Path: "/0", Value: "-w"}
	assert.Equal(t, []spells.PatchOp{want}, ch.Ops)
}

func TestResolve_CommandCapture(t *testing.T) {
	src := `
import "magus/spell";
export fun mgs_getName() > str { return "capture"; }
fun inspect(t: Target) > Command { return Command{bin = "go", args = ["mod", "edit", "-print"], capture = true}; }
export fun mgs_listTargets() > {str: fun(Target) Command} { return {"inspect": inspect}; }
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.True(t, spec.Ops["inspect"].Capture)
}

// TestResolve_ServiceAndCommandCoexist proves op-level kind: one spell mixes a
// command op (returns Command, run to completion) and a service op (returns Service,
// a long-running process `magus run` blocks on) under one name. The service op's
// embedded Command mirrors Service.Command so every fork/render/cache path reads it
// uniformly.
func TestResolve_ServiceAndCommandCoexist(t *testing.T) {
	src := `
import "magus/spell";
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
	assert.Equal(t, spells.OpKindCommand, build.OpKind())
	assert.Equal(t, "npm", build.Bin)
	assert.Equal(t, []string{"run", "build"}, build.Args)
	assert.Nil(t, build.Service, "a command op has no Service")

	// Only the service op is reported as a service target (drives uncached-at-run).
	assert.Equal(t, []string{"serve"}, spec.ServiceOpNames())

	serve := spec.Ops["serve"]
	assert.Equal(t, spells.OpKindService, serve.OpKind())
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
// the Buzz object Service into spells.Service.
func TestResolve_ServiceDistinctAndIdle(t *testing.T) {
	src := `
import "magus/spell";
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
import "magus/spell";
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
import "magus/spell";
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

// TestResolve_CommandCapturesHandlerDoc pins that an op handler's doc comment  -
// the comment block directly above its `fun` declaration  -  is captured onto the
// target's Doc, while an undocumented handler and one separated by a blank line
// carry none. This is the data `magus describe` prints and `magus doctor` enforces.
func TestResolve_CommandCapturesHandlerDoc(t *testing.T) {
	src := `
import "magus/spell";
export fun mgs_getName() > str { return "forkpkg"; }

// build compiles the project.
fun build(tg: Target) > Command { return Command{bin = "echo", args = ["a"]}; }

fun test(tg: Target) > Command { return Command{bin = "echo", args = ["b"]}; }

// stray comment with a blank line below  -  not a doc comment.

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
import "magus/spell";
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
import "magus/spell";
export fun mgs_getName() > str { return "forkpkg"; }
export fun build(tg: Target) > Command { return Command{bin = "echo", args = [tg.name]}; }
export fun mgs_listTargets() > {str: fun(Target) Command} {
    return {"build": build};
}
`
	_, err := resolve(t, src)
	assert.Error(t, err, "expected error for a handler reading the Target")
}

// TestOptionalContract_PathEntriesAreSelfDescribing pins the invariant that made a rename fail
// silently: which reduction a contract field's value needs is recorded ON the entry, so it travels
// with the entry when its Field is renamed.
//
// The regression it guards is specific: deciding the reduction with a switch over field-name
// strings in resolve.go means a rename can update the contract, the decoder, and every spell
// source while the switch still names the old field - pathValues stops running, the Path
// objects are never reduced to strings, and the decoded value comes back EMPTY with no error
// anywhere. A second list of these field names is the thing to keep out of this package.
//
// The table below states each entry's shape by NAME, which is what holds the invariant through a
// change of shape: mgs_listManifests sits at ShapeManifests rather than ShapePaths because it
// carries lock candidates, and naming the expectation makes such a move a one-line diff a
// reviewer reads as a decision rather than a silent behaviour change.
func TestOptionalContract_PathEntriesAreSelfDescribing(t *testing.T) {
	// Every entry's Buzz element type, stated by NAME - the stable half of the contract, since a
	// Field rename must not be able to change this map. Absent means ShapeStrs.
	shapes := map[string]ContractShape{
		"mgs_listRequiredGlobs": ShapePaths,
		"mgs_listProvidedGlobs": ShapePaths,
		"mgs_listClaimedGlobs":  ShapePaths,
		"mgs_listIgnoreDirs":    ShapePaths,
		"mgs_listManifests":     ShapeManifests,
	}
	seen := map[string]bool{}
	for _, e := range OptionalContract {
		seen[e.Name] = true
		assert.Equal(t, shapes[e.Name], e.Shape,
			"%s: Shape must match the element type its Buzz signature returns", e.Name)
	}
	for name := range shapes {
		assert.True(t, seen[name], "%s left the contract; drop it here too", name)
	}
}

// TestResolve_ManifestsUsePathValues is the end-to-end half: the manifests entry decodes to
// populated values. Read together with the test above, a rename that broke the Shape wiring
// would surface here as an empty slice rather than as a mysterious downstream absence.
//
// It uses the [Path] form deliberately, so it doubles as the compat proof: mgs_listManifests
// returned [Path] before Manifest existed, and a spell that still does must keep loading rather
// than fail. Path and Manifest agree on .value, and the reduction reads keys structurally, so
// the older form decodes as manifests declaring no lockfile.
func TestResolve_ManifestsUsePathValues(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "manifest-paths"; }
export fun mgs_listManifests() > [Path] { return [Path{value = "package.json"}, Path{value = "deno.json"}]; }
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Equal(t, []spells.Manifest{{Value: "package.json"}, {Value: "deno.json"}}, spec.Manifests,
		"a pre-Manifest [Path] entry must still decode, as manifests carrying no lock candidates")
}

// TestResolve_ManifestsCarryLockCandidates pins the field this type was added for. pathValues
// next door reduces each object to its .value and would drop lockCandidates in silence, since it
// reads only the keys it knows - so the assertion that matters is that the candidates SURVIVE the
// boundary, not merely that the manifest does.
func TestResolve_ManifestsCarryLockCandidates(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "manifest-locks"; }
export fun mgs_listManifests() > [Manifest] {
    return [Manifest{value = "package.json", lockCandidates = ["pnpm-lock.yaml", "yarn.lock"]},
            Manifest{value = "setup.py"}];
}
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Equal(t, []spells.Manifest{
		{Value: "package.json", LockCandidates: []string{"pnpm-lock.yaml", "yarn.lock"}},
		{Value: "setup.py"},
	}, spec.Manifests, "lockCandidates must survive the MGS boundary, not be flattened away")
}

// TestResolve_ManifestsRejectAWrongReturnType covers the other half of why manifestValues
// validates rather than passing through: this package's recurring failure is a malformed
// declaration decoding to empty with nothing naming the cause.
//
// The case it uses is the one the CHECKER cannot catch. A mistyped FIELD is rejected at
// compile time (`Manifest.lockCandidates is [str], got str` - BZZ1005), because the annotated
// return type makes the object's shape known. Nothing checks the annotation against the mgs_
// contract itself, though, so a spell returning the wrong LIST type compiles cleanly and only
// this validation stands between it and a silently empty Manifests.
func TestResolve_ManifestsRejectAWrongReturnType(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "manifest-bad-shape"; }
export fun mgs_listManifests() > [str] { return ["package.json"]; }
`
	_, err := resolve(t, src)
	require.Error(t, err, "a [str] where [Manifest] belongs must fail at load, not decode to nothing")
	assert.Contains(t, err.Error(), "must be Manifest")
}
