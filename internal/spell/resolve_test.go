package spell

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolved builds a bare session with the magus/spell types registered, execs src and
// resolves it through the spell contract.
func resolved(t *testing.T, src string) (Resolved, error) {
	t.Helper()
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	sess.SetModuleDecls(SpellModulePath, SpellModuleSource)
	require.NoError(t, sess.Exec(ctx, src), "exec")
	return Resolve(ctx, sess, src)
}

// resolve is resolved plus the semantic checks, which is everything a spell load runs.
func resolve(t *testing.T, src string) (spells.Descriptor, error) {
	t.Helper()
	r, err := resolved(t, src)
	if err != nil {
		return spells.Descriptor{}, err
	}
	return r.Descriptor()
}

func TestResolve_GetName(t *testing.T) {
	const src = `export fun mgs_getName() > str { return "testspell"; }`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Equal(t, "testspell", spec.Name)
}

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
	require.ErrorIs(t, err, types.SpellContractViolated)
	assert.ErrorContains(t, err, "mgs_listRequiredGlobs declares > [str]; the contract wants > [Path]")
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
	assert.ErrorIs(t, err, ErrNotASpell)
}

// A record-shaped op needs `> any`, which the contract check refuses before any
// export is called.
func TestResolve_RecordTargetsAreRefused(t *testing.T) {
	const src = `
export fun mgs_getName() > str { return "mypkg"; }
export fun mgs_listTargets() > any {
    return {"build": {"bin": "echo", "args": ["ok"]}};
}
`
	_, err := resolve(t, src)
	require.ErrorIs(t, err, types.SpellContractViolated)
	assert.ErrorContains(t, err, "mgs_listTargets declares > any")
}

// A [Path] where [Manifest] belongs is refused, with no leniency for the older contract.
func TestResolve_ManifestsRefuseAPathList(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "manifest-paths"; }
export fun mgs_listManifests() > [Path] { return [Path{value = "package.json"}]; }
`
	_, err := resolve(t, src)
	require.ErrorIs(t, err, types.SpellContractViolated)
	assert.ErrorContains(t, err, "mgs_listManifests declares > [Path]; the contract wants > [Manifest]")
}

// A Command's `secrets` map (env var name -> provider reference) reaches the op.
func TestResolve_CommandSecrets(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "secretpkg"; }
fun publish(t: Target) > Command {
    return Command{bin = "npm", args = ["publish"], secrets = {"NPM_TOKEN": "NPM_TOKEN"}};
}
export fun mgs_listTargets() > {str: fun(Target) Command} { return {"publish": publish}; }
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"NPM_TOKEN": "NPM_TOKEN"}, spec.Ops["publish"].Secrets)
}

// An empty secrets map and an absent one are one value (nil), so both spellings share one
// descriptor serialization and one cache entry.
func TestResolve_SecretsEmptyDecodesNil(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "emptysecrets"; }
fun publish(t: Target) > Command {
    return Command{bin = "npm", args = ["publish"], secrets = {<str: str>}};
}
export fun mgs_listTargets() > {str: fun(Target) Command} { return {"publish": publish}; }
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Nil(t, spec.Ops["publish"].Secrets)
}

// The key becomes `name=value` in the child environment, so a key that is not an env var
// name (an "=" inside, an empty string) would silently retarget or corrupt it.
func TestResolve_SecretsRejectsBadEnvName(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "badname"; }
fun publish(t: Target) > Command {
    return Command{bin = "npm", args = ["publish"], secrets = {"A=B": "REF"}};
}
export fun mgs_listTargets() > {str: fun(Target) Command} { return {"publish": publish}; }
`
	_, err := resolve(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an environment variable name")
}

// The supervised path rebuilds a service command without its env, so a secret would reach
// a foregrounded service and vanish from a supervised one.
func TestResolve_SecretsRejectedOnServiceOps(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "svcsecrets"; }
fun serve(t: Target) > Service {
    return Service{command = Command{bin = "node", args = ["server.js"], secrets = {"API_KEY": "API_KEY"}}};
}
export fun mgs_listServices() > {str: fun(Target) Service} { return {"serve": serve}; }
`
	_, err := resolve(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported on a service op")
}

// mgs_listTargets returns {str: fun(Target) Command} handlers by value. Each is called
// once at resolution to record its command, down to the charm patches.
func TestResolve_FunctionValueTargets(t *testing.T) {
	const src = `
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
	assert.Equal(t, spells.Op{Command: spells.Command{Bin: "go", Args: []string{"build"}}}, b)

	f := spec.Ops["fmt"]
	assert.Equal(t, "gofmt", f.Bin)
	ch, ok := f.Charms["write"]
	require.Truef(t, ok, "fmt missing charm \"write\": %+v", f)
	want := spells.PatchOp{Op: "replace", Path: "/0", Value: "-w"}
	assert.Equal(t, []spells.PatchOp{want}, ch.Ops)
}

func TestResolve_CommandCapture(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "capture"; }
fun inspect(t: Target) > Command { return Command{bin = "go", args = ["mod", "edit", "-print"], capture = true}; }
export fun mgs_listTargets() > {str: fun(Target) Command} { return {"inspect": inspect}; }
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.True(t, spec.Ops["inspect"].Capture)
}

// One spell mixes a command op (run to completion) and a service op (a long-running
// process `magus run` blocks on). The service op's embedded Command mirrors
// Service.Command so every fork, render and cache path reads it uniformly.
func TestResolve_ServiceAndCommandCoexist(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "node"; }
fun nodeBuild(t: Target) > Command { return Command{bin = "npm", args = ["run", "build"]}; }
fun nodeServe(t: Target) > Service {
    return Service{ command   = Command{bin = "npm", args = ["run", "dev"]},
                   readiness = Command{bin = "curl", args = ["-sf", "http://localhost:5173"]} };
}
export fun mgs_listTargets() > {str: fun(Target) Command} { return {"build": nodeBuild}; }
export fun mgs_listServices() > {str: fun(Target) Service} { return {"serve": nodeServe}; }
`
	spec, err := resolve(t, src)
	require.NoError(t, err)

	build := spec.Ops["build"]
	assert.Equal(t, spells.OpKindCommand, build.OpKind())
	assert.Equal(t, spells.Op{Command: spells.Command{Bin: "npm", Args: []string{"run", "build"}}}, build)

	assert.Equal(t, []string{"serve"}, spec.ServiceOpNames())

	serve := spec.Ops["serve"]
	assert.Equal(t, spells.OpKindService, serve.OpKind())
	assert.True(t, serve.IsService())
	cmd := spells.Command{Bin: "npm", Args: []string{"run", "dev"}}
	assert.Equal(t, spells.Op{
		Kind:    spells.OpKindService,
		Command: cmd,
		Service: &spells.Service{
			Command:   cmd,
			Readiness: spells.Command{Bin: "curl", Args: []string{"-sf", "http://localhost:5173"}},
		},
	}, serve)
}

// An op declared as both a target and a service is one name claimed twice.
func TestResolve_TargetAndServiceSharingANameCollide(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "twice"; }
fun runDev(t: Target) > Command { return Command{bin = "true"}; }
fun serve(t: Target) > Service { return Service{command = Command{bin = "true"}}; }
export fun mgs_listTargets() > {str: fun(Target) Command} { return {"dev": runDev}; }
export fun mgs_listServices() > {str: fun(Target) Service} { return {"dev": serve}; }
`
	_, err := resolve(t, src)
	require.Error(t, err)
	assert.ErrorContains(t, err, `declares ops "dev" and "dev", which are the same op "dev"`)
}

// The optional distinct (justified dedup opt-out) and idle (per-service idle timeout)
// fields reach spells.Service.
func TestResolve_ServiceDistinctAndIdle(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "db"; }
fun pg(t: Target) > Service {
    return Service{ command  = Command{bin = "docker", args = ["run", "postgres:16"]},
                   distinct = "pins PG 16 for the 15 to 16 migration test",
                   idle     = "45m" };
}
export fun mgs_listServices() > {str: fun(Target) Service} { return {"pg": pg}; }
`
	spec, err := resolve(t, src)
	require.NoError(t, err)

	pg := spec.Ops["pg"]
	assert.Equal(t, &spells.Service{
		Command:  spells.Command{Bin: "docker", Args: []string{"run", "postgres:16"}},
		Distinct: "pins PG 16 for the 15 to 16 migration test",
		Idle:     "45m",
	}, pg.Service)
}

// A service op that detaches (docker run -d) is rejected by the kind-coherence ward
// (MGS5002) at resolution, before anything forks.
func TestResolve_DetachedServiceRejected(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "db"; }
fun pg(t: Target) > Service {
    return Service{ command = Command{bin = "docker", args = ["run", "-d", "postgres:16"]} };
}
export fun mgs_listServices() > {str: fun(Target) Service} { return {"pg": pg}; }
`
	_, err := resolve(t, src)
	require.Error(t, err)
	assert.ErrorIs(t, err, types.ServiceOpDetached)
}

// Every op is a command or a service: a handler with another signature, such as a cache
// backend's `fun(Target, fun(any))`, is refused by the contract check.
func TestResolve_NonCommandOpRejected(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "fnpkg"; }
export fun enabled(tg: Target, cb: fun(any)) > bool { return true; }
export fun mgs_listTargets() > {str: fun(Target, fun(any)) bool} {
    return {"enabled": enabled};
}
`
	_, err := resolve(t, src)
	require.ErrorIs(t, err, types.SpellContractViolated)
}

// An op handler's doc comment (the block directly above its `fun`) lands on the target's
// Doc, while an undocumented handler and one separated by a blank line carry none.
func TestResolve_CommandCapturesHandlerDoc(t *testing.T) {
	const src = `
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

// Every op is a handler now, so every op is in DocOps, the doctor's doc-comment scope,
// service handlers included.
func TestResolve_DocOpsListEveryHandler(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "mixed"; }

// build is a command handler.
fun build(tg: Target) > Command { return Command{bin = "echo", args = ["a"]}; }

// serve is a service handler.
fun serve(tg: Target) > Service { return Service{command = Command{bin = "echo", args = ["b"]}}; }

export fun mgs_listTargets() > {str: fun(Target) Command} { return {"build": build}; }
export fun mgs_listServices() > {str: fun(Target) Service} { return {"serve": serve}; }
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	assert.Equal(t, []string{"build", "serve"}, spec.DocOps)
	assert.Equal(t, "serve is a service handler.", spec.Ops["serve"].Doc)
}

// An op handler that reads the Target fails at resolution (the Target is null there)
// rather than recording a command built from empty fields.
func TestResolve_CommandRejectsTargetRead(t *testing.T) {
	const src = `
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

// The candidates survive the boundary: the Manifest decodes whole, lockCandidates
// included, and one declaring none stays a bare manifest.
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
	}, spec.Manifests)
}

// A manifest with an empty value names no file, which the decoder accepts and the
// semantic check refuses.
func TestResolve_ManifestsRequireAValue(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "manifest-empty"; }
export fun mgs_listManifests() > [Manifest] { return [Manifest{value = ""}]; }
`
	_, err := resolve(t, src)
	require.Error(t, err)
	assert.ErrorContains(t, err, "mgs_listManifests[0].value is required")
}

// mgs_getSandbox decodes into the declaration magus.yaml's sandbox makes.
func TestResolve_SandboxDecodes(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "sandboxed"; }
export fun mgs_getSandbox() > Sandbox {
    return Sandbox{
        allow = [
            SandboxAllow{env = "TOOL_CACHE", base = "userCache", path = "tool", mode = SandboxAccess.rw},
            SandboxAllow{base = "binRoot", bin = "tool", requires = "lib", mode = SandboxAccess.rx},
        ],
        env = SandboxEnv{passthrough = ["TOOL_CACHE", "TOOL_*"]},
    };
}
`
	spec, err := resolve(t, src)
	require.NoError(t, err)
	want := &spells.Sandbox{
		Allow: []spells.SandboxAllow{
			{Env: "TOOL_CACHE", Base: "userCache", Path: "tool", Mode: spells.SandboxAccessRW},
			{Base: "binRoot", Bin: "tool", Requires: "lib", Mode: spells.SandboxAccessRX},
		},
		Env: spells.SandboxEnv{Passthrough: []string{"TOOL_CACHE", "TOOL_*"}},
	}
	assert.Equal(t, want, spec.Sandbox)
}

// A declaration no host could honor is refused when the spell loads, naming the spell,
// never skipped with a warning.
func TestResolve_SandboxRefusesAMalformedDeclaration(t *testing.T) {
	for name, entry := range map[string]string{
		"escaping path":     `SandboxAllow{base = "userCache", path = "../elsewhere", mode = SandboxAccess.rw}`,
		"absolute sub-path": `SandboxAllow{base = "xdgData", path = "/etc", mode = SandboxAccess.ro}`,
		"empty env name":    `SandboxAllow{env = "", mode = SandboxAccess.rw}`,
		"unknown base":      `SandboxAllow{base = "cache", path = "tool", mode = SandboxAccess.rw}`,
		"a variable path":   `SandboxAllow{path = "$HOME/.tool", mode = SandboxAccess.rw}`,
	} {
		t.Run(name, func(t *testing.T) {
			src := fmt.Sprintf(`
import "magus/spell";
export fun mgs_getName() > str { return "bad-sandbox"; }
export fun mgs_getSandbox() > Sandbox { return Sandbox{allow = [%s]}; }
`, entry)
			_, err := resolve(t, src)
			require.ErrorIs(t, err, types.AllowlistUnresolved)
			assert.Contains(t, err.Error(), `spell "bad-sandbox"`)
		})
	}
}

// A cache names a writable grant of its own declaration by variable; one naming no such
// grant is refused at load, like any grant that cannot be honored.
func TestResolve_SandboxRefusesACacheOfNoWritableGrant(t *testing.T) {
	for name, decl := range map[string]string{
		"no grant":        `Sandbox{caches = [SandboxCache{env = "TOOL_CACHE"}]}`,
		"read-only grant": `Sandbox{allow = [SandboxAllow{env = "TOOL_CACHE", mode = SandboxAccess.rx}], caches = [SandboxCache{env = "TOOL_CACHE"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			src := fmt.Sprintf(`
import "magus/spell";
export fun mgs_getName() > str { return "bad-cache"; }
export fun mgs_getSandbox() > Sandbox { return %s; }
`, decl)
			_, err := resolve(t, src)
			require.ErrorIs(t, err, types.AllowlistUnresolved)
			assert.Contains(t, err.Error(), "no writable allow entry has env TOOL_CACHE")
		})
	}
}

// The go spell declares the two caches a warm build reads: the build cache, whose
// entries go re-dates as it uses them, and the module cache less its zips and clones.
func TestGoSpellDeclaresItsCaches(t *testing.T) {
	sb := Builtins()["go"].Sandbox
	require.NotNil(t, sb)
	assert.Equal(t, []spells.SandboxCache{
		{Env: "GOCACHE", StampsUse: true, Skip: []string{"README", "trim.txt", "fuzz/**"}},
		{Env: "GOMODCACHE", Skip: []string{"cache/vcs/**", "cache/download/**/*.zip", "**/*.lock", "**/*.partial", "**/*.tmp"}},
	}, sb.Caches)
}

// A passthrough pattern env.Parse refuses is refused at load too.
func TestResolve_SandboxRefusesAPassthroughThatDoesNotParse(t *testing.T) {
	const src = `
import "magus/spell";
export fun mgs_getName() > str { return "bad-passthrough"; }
export fun mgs_getSandbox() > Sandbox { return Sandbox{env = SandboxEnv{passthrough = ["GO*"]}}; }
`
	_, err := resolve(t, src)
	require.ErrorIs(t, err, types.AllowlistUnresolved)
	assert.Contains(t, err.Error(), `"GO*"`)
}

// The go spell carries the Go toolchain's grants: GOCACHE executable, since go run and
// go tool exec the binaries they cache there, and every Go variable passed through.
// A scope without it gets neither.
func TestGoSpellDeclaresTheToolchainsGrants(t *testing.T) {
	gocache := filepath.Join(t.TempDir(), "gocache")
	decls := map[string]spells.Sandbox{}
	for _, name := range []string{"go", "markdown"} {
		sb := Builtins()[name].Sandbox
		require.NotNil(t, sb, name)
		decls[name] = *sb
	}
	p := sandbox.BuildPolicy(sandbox.PolicyOptions{
		Home: t.TempDir(), GOOS: "linux",
		Environ: []string{"GOCACHE=" + gocache, "GOFLAGS=-mod=mod", "GOEXPERIMENT=jsonv2", "CGO_ENABLED=0"},
		Spells:  decls,
	})
	cached := filepath.Join(gocache, "ab", "tool")

	withGo := p.Scoped([]string{"go"}, nil)
	require.NoError(t, withGo.CheckExec(t.Context(), cached))
	require.NoError(t, withGo.CheckWrite(t.Context(), cached))
	assert.Subset(t, withGo.BaseEnv, []string{"GOCACHE=" + gocache, "GOFLAGS=-mod=mod", "GOEXPERIMENT=jsonv2", "CGO_ENABLED=0"})

	without := p.Scoped([]string{"markdown"}, nil)
	assert.Error(t, without.CheckRead(t.Context(), cached))
	for _, kv := range without.BaseEnv {
		assert.False(t, strings.HasPrefix(kv, "GO"), "%s reaches a project without the go spell", kv)
	}
}

// go keeps the checksum database's tree head under the first GOPATH entry, not under
// GOMODCACHE, and writes it whenever it verifies a module go.sum does not list. So a
// module cache moved elsewhere still leaves go writing GOPATH/pkg/sumdb, and with
// GOPATH unset, the default under home.
func TestGoSpellGrantsTheChecksumDatabaseWhereGoKeepsIt(t *testing.T) {
	sb := Builtins()["go"].Sandbox
	require.NotNil(t, sb)
	gopath, home := t.TempDir(), t.TempDir()
	for name, c := range map[string]struct {
		opts   sandbox.PolicyOptions
		gopath string
	}{
		"GOPATH set, GOMODCACHE apart": {sandbox.PolicyOptions{Home: t.TempDir(), Environ: []string{"GOPATH=" + gopath, "GOMODCACHE=" + t.TempDir()}}, gopath},
		"GOPATH unset":                 {sandbox.PolicyOptions{Home: home}, filepath.Join(home, "go")},
	} {
		t.Run(name, func(t *testing.T) {
			c.opts.GOOS, c.opts.Spells = "linux", map[string]spells.Sandbox{"go": *sb}
			p := sandbox.BuildPolicy(c.opts).Scoped([]string{"go"}, nil)
			require.NoError(t, p.CheckWrite(t.Context(), filepath.Join(c.gopath, "pkg", "sumdb", "sum.golang.org", "latest")))
		})
	}
}
