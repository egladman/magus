package bindings

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/handler/mcp/origin"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// externalTarget names one target of another project: the {project, target}
// pair a cross-project handle stands for.
type externalTarget struct {
	Project string // project path as written after "project/" in the import
	Target  string // kebab-normalized target name
}

// externalHandles is a session's registry of cross-project target handles: the
// function values a `import "project/<path>"` module binds for each of the
// dependency's targets (see resolveProjectImport), paired with the target each
// dispatches. ctx.needs matches a passed function against it by value
// identity to recover the {project, target} the handle stands for - the handle
// itself stays an ordinary callable, so `gopherbuzz.build()` also just works.
// A linear scan is fine: a magusfile imports a handful of projects at most.
type externalHandles struct {
	vals    []vm.Value
	targets []externalTarget
}

func (e *externalHandles) register(v vm.Value, dep externalTarget) {
	e.vals = append(e.vals, v)
	e.targets = append(e.targets, dep)
}

func (e *externalHandles) lookup(v vm.Value) (externalTarget, bool) {
	for i, hv := range e.vals {
		if hv.Equal(v) {
			return e.targets[i], true
		}
	}
	return externalTarget{}, false
}

// buildCacheNS assembles magus.cache for a magusfile. Today it exposes remote(),
// which wires an imported spell as the cross-shard remote cache backend:
//
//	import "spells/github/actions" as github
//	magus.cache.remote(github)
//
// The import already registered the spell (with handler op support, for a Buzz
// spell); remote() just records its name on the per-Open workspace registry, and
// magus.Open resolves it by name once the magusfile has been evaluated. The spell
// must expose get_artifact/put_artifact handler ops (and optionally enabled()).
func buildCacheNS(ctx context.Context, obs buzz.DirectObserver) vm.Value {
	ns := vm.NewMap()
	ns.MapSet("remote", directVal(obs, "magus.cache.remote", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsMap() {
			return vm.Null, fmt.Errorf(`magus\cache.remote: expected an imported spell handle`)
		}
		nv, ok := args[0].MapGet("name")
		if !ok || !nv.IsStr() || nv.AsString() == "" {
			return vm.Null, fmt.Errorf(`magus\cache.remote: argument is not a spell handle (no name)`)
		}
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
			reg.SetRemoteBackend(nv.AsString())
		}
		return vm.Null, nil
	}))
	return ns
}

// buildCINS assembles magus.ci for a magusfile. It exposes provider(),
// which wires an imported spell as this workspace's CI provider:
//
//	import "spells/github/actions" as github
//	magus.ci.provider(github)
//
// The spell supplies job-log structure for whatever CI system it targets: fold
// markers, pull-request annotations, and a suggested concurrency. Every op is
// optional, because providers differ in what they support at all.
//
// A declared provider wins over magus's built-ins.
func buildCINS(_ context.Context, obs buzz.DirectObserver) vm.Value {
	ns := vm.NewMap()
	ns.MapSet("provider", directVal(obs, "magus.ci.provider", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsMap() {
			return vm.Null, fmt.Errorf(`magus\ci.provider: expected an imported spell handle`)
		}
		nv, ok := args[0].MapGet("name")
		if !ok || !nv.IsStr() || nv.AsString() == "" {
			return vm.Null, fmt.Errorf(`magus\ci.provider: argument is not a spell handle (no name)`)
		}
		SetCIProvider(nv.AsString())
		return vm.Null, nil
	}))
	return ns
}

// buildReviewNS assembles magus\review for a magusfile. provider() wires an imported spell as
// the place this workspace's changes are discussed:
//
//	import "spells/github" as github
//	magus\review.provider(github)
//
// One function, deliberately. Everything else a review needs - opening one, reading its
// threads, publishing drafts, replying - is a reserved name ON the spell (see spells/review.go),
// not a member here. A magusfile says WHERE reviews live; it does not conduct one.
//
// Wiring none is the ordinary state and never an error: the workspace reviews locally, and
// nothing about the diff surface changes.
func buildReviewNS(_ context.Context, obs buzz.DirectObserver) vm.Value {
	ns := vm.NewMap()
	ns.MapSet("provider", directVal(obs, "magus.review.provider", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsMap() {
			return vm.Null, fmt.Errorf(`magus\review.provider: expected an imported spell handle`)
		}
		nv, ok := args[0].MapGet("name")
		if !ok || !nv.IsStr() || nv.AsString() == "" {
			return vm.Null, fmt.Errorf(`magus\review.provider: argument is not a spell handle (no name)`)
		}
		SetReviewProvider(nv.AsString())
		return vm.Null, nil
	}))
	return ns
}

// buildSecretNS assembles magus\secret for a magusfile. provider() wires an imported
// spell as this workspace's secret backend; read() reads one credential through it:
//
//	import "spells/onepassword" as secrets
//	magus\secret.provider(secrets)
//	final token = magus\secret.read("DOCKERHUB_TOKEN")
//
// read(), NOT resolve(): `resolve` is a hard keyword in the Buzz lexer, so member access
// on it cannot parse. Do not "fix" this back.
//
// read() is the ONLY way a value becomes known-secret - redaction keys off having been
// read here, not off the reference looking credential-shaped. Reading the same variable
// with os\env gets a plain string magus has no reason to protect: the documented seam,
// not a gap.
func buildSecretNS(runCtx context.Context, obs buzz.DirectObserver) vm.Value {
	ns := vm.NewMap()
	ns.MapSet("provider", directVal(obs, "magus.secret.provider", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		// Records onto the resolver captured at construction, the same way
		// buildCacheNS records onto the registry it captured.
		if len(args) == 0 || !args[0].IsMap() {
			return vm.Null, fmt.Errorf(`magus\secret.provider: expected an imported spell handle`)
		}
		nv, ok := args[0].MapGet("name")
		if !ok || !nv.IsStr() || nv.AsString() == "" {
			return vm.Null, fmt.Errorf(`magus\secret.provider: argument is not a spell handle (no name)`)
		}
		// Records onto the resolver captured at construction, the same way buildCacheNS
		// records onto the registry it captured.
		//
		// ERRORS when there is no resolver rather than no-opping. A silently dropped
		// selection means the run falls back to the built-in environment provider - the
		// magusfile asked for 1Password and got whatever $VAR happened to hold. That is
		// the wrong-credential failure memoKey is provider-keyed to prevent, arriving
		// through a different door, and it would be invisible.
		r := secret.ResolverFromContext(runCtx)
		if r == nil {
			return vm.Null, fmt.Errorf(`magus\secret.provider: no secret resolver on this run`)
		}
		r.SetProviderName(nv.AsString())
		return vm.Null, nil
	}))
	ns.MapSet("read", directVal(obs, "magus.secret.read", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsStr() || args[0].AsString() == "" {
			return vm.Null, fmt.Errorf(`magus\secret.read: expected a non-empty reference string`)
		}
		// Resolver from the RUN context captured at construction (runCtx); cancellation
		// from the per-call one (ctx). The VM supplies its own ctx per call, carrying
		// neither the run's resolver nor its provider selection.
		//
		// The per-call context holds the conventional name deliberately: a future edit
		// that reaches for `ctx` gets the live, cancellable one, which is the safe
		// default. Reaching for it to find the resolver fails loudly rather than
		// resolving against nothing.
		ref := args[0].AsString()
		r := secret.ResolverFromContext(runCtx)
		if r == nil {
			return vm.Null, fmt.Errorf(`magus\secret.read: no secret resolver on this run`)
		}
		v, err := r.Read(ctx, ref)
		if err != nil {
			return vm.Null, err
		}
		// Audited HERE rather than inside secret.Read, for two reasons. The engine
		// reason: internal/journal imports internal/secret for redaction, so emitting
		// from there would be an import cycle. The better reason: this binding IS the
		// boundary worth auditing - it is the one place a magusfile reaches for a
		// credential, which is the act an audit trail exists to record.
		//
		// The reference and the provider, NEVER the value - and that is a discipline here,
		// not a mechanism. journal.Emit redacts against the resolver on the context it is
		// given, and this is the per-call context, which carries none. Do not add a value
		// to this Text expecting something downstream to catch it.
		if project, target, ok := journal.StepFromContext(ctx); ok {
			via := r.ProviderName()
			if via == "" {
				via = "built-in environment provider"
			}
			journal.Emit(ctx, journal.Event{
				Kind: journal.KindSecret, Project: project, Target: target,
				Text: fmt.Sprintf("read secret %q via %s", ref, via),
			})
		}
		// Reveal where the credential crosses into a magusfile string. From here it is an
		// ordinary Buzz str with only redact-at-write behind it, which is the documented
		// seam - magus\secret.grant is the surface that avoids this crossing entirely.
		return vm.StrValue(v.Reveal()), nil
	}))
	// endpoint() is what a grant is FOR. It returns a loopback base URL a CHILD can
	// be pointed at, so a tool magus shells out to gets the credential attached without
	// ever holding it. The magusfile decides which env var carries it
	// (ctx.with_env({"OPENAI_BASE_URL": magus\secret.endpoint(g)})) rather than magus
	// setting one, because a tool with no base-URL knob should fail where you can see
	// it, not silently talk to the real endpoint unauthenticated.
	//
	// Binding a socket resolves nothing either: the provider is invoked on the first
	// request the child actually sends through it.
	ns.MapSet("endpoint", directVal(obs, "magus.secret.endpoint", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		g, err := secretGrantArg("endpoint", args)
		if err != nil {
			return vm.Null, err
		}
		r := secret.ResolverFromContext(ctx)
		if r == nil {
			return vm.Null, fmt.Errorf(`magus\secret.endpoint: no secret resolver on this run`)
		}
		// The per-call ctx - see grant() above for why the captured one is stale. It is
		// also the correct LIFETIME: a target's dispatch context is cancelled when the
		// run ends, not when this call returns, so the forwarder outlives the magusfile
		// asking for its URL and dies with the run that asked.
		url, err := r.OpenEndpoint(ctx, g)
		if err != nil {
			return vm.Null, fmt.Errorf(`magus\secret.endpoint: %w`, err)
		}
		if project, target, ok := journal.StepFromContext(ctx); ok {
			journal.Emit(ctx, journal.Event{
				Kind: journal.KindSecret, Project: project, Target: target,
				Text: fmt.Sprintf("opened a local endpoint for %s carrying secret %q", g.Host, g.Ref),
			})
		}
		recordCredentialGrant(ctx, g, "secret.endpoint")
		return vm.StrValue(url), nil
	}))
	return ns
}

// recordCredentialGrant writes the governance half of a credential declaration to the
// activity trail: a run made this credential reachable at this destination.
//
// The journal already records the same act, and the two are not redundant. The journal
// answers "what did this build do" and is read per invocation; the trail answers "who
// did what against this daemon" and is what the console's activity view shows. When an
// AGENT triggers a run that makes a credential spendable, this is the event that
// connects the tool call to its consequence - without it the activity log shows the
// call and not what it unlocked.
//
// The REFERENCE, host and header only. The value is not resolved at declaration time
// and must not be resolved in order to log it - that would undo the laziness the whole
// interaction policy rests on, and put a credential one formatting mistake from a
// durable append-only file.
func recordCredentialGrant(ctx context.Context, g types.SecretGrant, action string) {
	base := trail.BaseFromContext(ctx)
	if base == "" {
		return
	}
	// "magusfile" when no agent triggered this run: the declaration is still worth
	// recording, and attributing it to a person or an agent magus cannot identify would
	// be worse than naming the file that actually declared it.
	actor, userAgent := "magusfile", ""
	if o, ok := origin.FromContext(ctx); ok && o.Agent != "" {
		actor, userAgent = o.Agent, o.UserAgent
	}
	trail.Append(ctx, base, trail.Event{
		Ts:        time.Now().UnixMilli(),
		Kind:      trail.KindCredentialGrant,
		Actor:     actor,
		UserAgent: userAgent,
		Action:    action,
		Outcome:   trail.OutcomeOK,
		Preview:   fmt.Sprintf("%s -> %s (%s)", g.Ref, g.Host, g.Header),
	})
}

// secretGrantArg reads a secret-grant object out of a namespace call's arguments.
// Shared by grant() and endpoint() so the two cannot disagree about what a grant looks
// like or word the same mistake differently.
//
// The magusfile DECLARES the object; magus does not export one. A type in a host
// module's generated declarations can be annotated but not constructed, so exporting a
// `magus\SecretGrant` would name something an author could not build - which an
// earlier version of this error message told them to write.
//
// A present-but-wrong-typed field is distinguished from an absent one. Reporting
// `host = 42` as "host is required" pointed at the wrong mistake, and `prefix = 42`
// was worse than misleading: Normalize never inspects prefix, so it silently became ""
// and the credential went out with no "Bearer " in front of it.
func secretGrantArg(method string, args []vm.Value) (types.SecretGrant, error) {
	// MapView, not IsMap. A magusfile and a spell both declare `object SecretGrant {...}`
	// and pass an INSTANCE, which is tagObject and NOT tagMap - so an IsMap check rejected
	// the documented spelling outright. Nothing caught it because every test built the Go
	// struct directly instead of going through Buzz; magus's own GitHub Actions cache
	// spell was the first caller to use the surface as written. MapView accepts a map and
	// an object instance both, which is what this always meant to take.
	// Length first: indexing args[0] to build the view before checking it exists
	// panics on a no-argument call instead of reporting the error below.
	if len(args) == 0 {
		return types.SecretGrant{}, fmt.Errorf(`magus\secret.%s: expected an object with ref/host/header/prefix fields, e.g. SecretGrant{ ref = "...", host = "api.example.com", header = "Authorization", prefix = "Bearer " } declared in your magusfile`, method)
	}
	fields, viewOK := args[0].MapView()
	if !viewOK {
		return types.SecretGrant{}, fmt.Errorf(`magus\secret.%s: expected an object with ref/host/header/prefix fields, e.g. SecretGrant{ ref = "...", host = "api.example.com", header = "Authorization", prefix = "Bearer " } declared in your magusfile`, method)
	}
	var bad error
	field := func(name string) string {
		v, ok := fields.MapGet(name)
		if !ok {
			return ""
		}
		if !v.IsStr() {
			if bad == nil {
				bad = fmt.Errorf(`magus\secret.%s: field %q must be a str`, method, name)
			}
			return ""
		}
		return v.AsString()
	}
	g := types.SecretGrant{
		Ref:    field("ref"),
		Host:   field("host"),
		Header: field("header"),
		Prefix: field("prefix"),
	}
	if bad != nil {
		return types.SecretGrant{}, bad
	}
	g, err := g.Normalize()
	if err != nil {
		return types.SecretGrant{}, fmt.Errorf(`magus\secret.%s: %w`, method, err)
	}
	return g, nil
}

// dispatchBuzzExternal runs the cross-project target an external handle names,
// through the run's CrossDispatch coordinator (run-once + cross-project cycle
// detection). The project path is resolved with file.ResolveImport against the caller's
// workspace-relative path, the same rule describe.go applies to the extracted ref, so
// the graph edge and the runtime dispatch agree, and a ..-escape or absolute path is rejected
// rather than running a magusfile outside the workspace. The dep's canonical dir
// comes from the workspace, keeping the coordinator's run-once/cycle key canonical.
// It yields the caller's concurrency slot for the duration (the remote run needs
// slots of its own), mirroring buzzDispatchViaPool. No-op when no coordinator/
// workspace is in ctx (describe/parse), so the handle stays graph-only.
func dispatchBuzzExternal(ctx context.Context, ref externalTarget) error {
	cd := interp.CrossDispatchFromContext(ctx)
	src := interp.SourceFromContext(ctx)
	ws := types.WorkspaceFromContext(ctx)
	if cd == nil || src == nil || ws == nil {
		return nil
	}
	callerRel, err := filepath.Rel(ws.Root(), src.Dir)
	if err != nil {
		return fmt.Errorf("magus: cross-project dependency: %w", err)
	}
	depPath, err := file.ResolveImport(ref.Project, filepath.ToSlash(callerRel))
	if err != nil {
		return err
	}
	dep := ws.Get(depPath)
	if dep == nil {
		return fmt.Errorf("magus: cross-project dependency: unknown project %q", depPath)
	}
	// The real normalizer, not ToLower. Today's only producer of a cross ref already
	// normalized it, so the two agree by luck; a producer that hands over a raw name
	// would get goBuild -> gobuild here and dispatch a target that does not exist.
	target := types.Normalize(ref.Target)
	lim := cache.LimiterFromContext(ctx)
	return proc.RunChildSync(ctx, lim, func() error {
		return cd.Dispatch(cache.WithoutSlotHeld(ctx), dep.Dir, target)
	})
}

// buildBuzzNeeds returns ctx.needs(...), the one dependency primitive. Every
// argument is a target function - a same-project exported target passed by
// reference (ctx.needs(format)), a cross-project handle a project import binds
// (ctx.needs(gopherbuzz.build)), or a LIST of target functions produced by
// ctx.glob (ctx.needs(ctx.glob("*-generate"))). A string is never accepted:
// a name pattern becomes handles through ctx.glob, so needs only ever sees target
// functions and stays monomorphic. Same-project targets are awaited through the VM
// pool / TargetMemo path (runBuzzDependencies); a cross-project handle dispatches via
// CrossDispatch.
func buildBuzzNeeds(targets map[string]vm.Callable, exports map[string]vm.Value, ext *externalHandles) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		var names []string
		// collect resolves one argument to its target name(s): a target function to
		// its name, or a ctx.glob(...) list to each element's name. A cross-project
		// handle dispatches immediately (awaited via CrossDispatch, not the same-project
		// pool). Errors are returned unprefixed; the caller adds the verb.
		var collect func(arg vm.Value) error
		collect = func(arg vm.Value) error {
			if arg.IsList() {
				for _, el := range arg.ListItems() {
					if err := collect(el); err != nil {
						return err
					}
				}
				return nil
			}
			if !arg.IsFun() {
				return fmt.Errorf("each argument must be a target function (an exported target, a project import member, or a ctx.glob(...) result)")
			}
			if ref, ok := ext.lookup(arg); ok {
				return dispatchBuzzExternal(callCtx, ref)
			}
			name, err := resolveTargetFun(targets, exports, arg)
			if err != nil {
				return err
			}
			names = append(names, name)
			return nil
		}
		for _, arg := range args {
			if err := collect(arg); err != nil {
				return vm.Null, fmt.Errorf("ctx.needs: %w", err)
			}
		}
		if err := runBuzzDependencies(callCtx, targets, names); err != nil {
			return vm.Null, fmt.Errorf("ctx.needs: %w", err)
		}
		return vm.Null, nil
	}
}

// resolveTargetFun maps a function value passed to ctx.needs to its canonical
// target key. The declared name (vm.Value.FunName) is run through the same
// normalizer targetMap registration uses, so a handle gets the same
// many-spellings forgiveness as the CLI. When the session's export registry is
// available, the passed value must BE the exported function (value identity),
// so a local helper that merely shares a target's normalized name cannot
// silently stand in for it.
func resolveTargetFun(targets map[string]vm.Callable, exports map[string]vm.Value, arg vm.Value) (string, error) {
	name := arg.FunName()
	// The chunk compiler names an anonymous closure "<fun>"; a Go DirectValue can
	// legitimately carry an empty name too.
	if name == "" || name == "<fun>" {
		return "", fmt.Errorf("anonymous function is not a target; pass an exported target function")
	}
	key := types.Normalize(name)
	if _, ok := targets[key]; !ok {
		return "", fmt.Errorf("function %q does not name an exported target", name)
	}
	if exports != nil {
		exp, ok := exports[key]
		if !ok || !exp.Equal(arg) {
			return "", fmt.Errorf("function %q matches target name %q but is not the exported target function", name, key)
		}
	}
	return key, nil
}

// buildBuzzGlob returns ctx.glob(...), the pattern resolver that FEEDS
// ctx.needs. Each argument is a glob pattern string matched against the project's
// target names (matchBuzzTargets semantics: "*" wildcards, and a pattern without "*"
// matches as "-<pattern>" suffix shorthand); it RETURNS the list of matching target
// function handles, so ctx.needs(ctx.glob("*-generate")) depends on every
// matching target. glob is the ONE place a pattern (a string) enters the dependency
// surface: it turns a name query into handles, keeping ctx.needs monomorphic - it
// only ever receives target functions. A pattern matching nothing yields an empty
// list (needs of it is a no-op). Only exported-function targets carry a handle, so a
// pattern that would match a spell-provided op yields no handle for it - depend on
// such a target directly.
func buildBuzzGlob(targets map[string]vm.Callable, exports map[string]vm.Value) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(_ context.Context, args []vm.Value) (vm.Value, error) {
		var patterns []string
		for _, arg := range args {
			if !arg.IsStr() {
				return vm.Null, fmt.Errorf("ctx.glob: each argument must be a glob pattern string")
			}
			patterns = append(patterns, arg.AsString())
		}
		if len(patterns) == 0 {
			return vm.Null, fmt.Errorf("ctx.glob: requires at least one glob pattern")
		}
		var handles []vm.Value
		for _, name := range matchBuzzTargets(targets, patterns) {
			if h, ok := exports[name]; ok {
				handles = append(handles, h)
			}
		}
		return vm.ListValue(handles), nil
	}
}

// runBuzzDependencies awaits the named same-project targets: via the Buzz VM pool
// when one is in ctx (parallel, TargetMemo-deduped), else inline sequential. It
// returns unprefixed errors so each caller attaches its own verb name.
func runBuzzDependencies(callCtx context.Context, targets map[string]vm.Callable, names []string) error {
	if len(names) == 0 {
		return nil
	}
	// These are dependencies (ctx.needs), so a service op among them is supervised
	// in the background rather than blocked on (see runCommand). The directly-run
	// target is dispatched without this marker, so it still foregrounds.
	callCtx = service.WithSupervision(callCtx)
	// `--` args belong to the target the USER named, not to whatever that target
	// pulls in. They ride the context, and runBuzzCommand hands them to any op that
	// declared no explicit args, so without this every dependency got them too:
	// `magus run test <p> -- -run TestX` reached the format dependency, and gofmt
	// tried to lstat "-run" and "TestX" as paths. That made the documented way to
	// narrow a run unusable on any target with a ctx.needs, which is most of them.
	callCtx = project.WithExtraArgs(callCtx, nil)
	names = dedupStrings(names)
	if src := interp.SourceFromContext(callCtx); src != nil {
		if reg := buzz.PoolRegistryFromContext(callCtx); reg != nil {
			key := src.Dir + "\x00buzz"
			p := reg.Get(key, interp.NewBuzzWorkerFunc(src))
			return buzzDispatchViaPool(callCtx, p, names)
		}
	}
	for _, name := range names {
		fn, ok := targets[name]
		if !ok {
			return types.DiagnosticErrorf(types.UnknownTarget, "unknown target %q", name)
		}
		if fn == nil {
			continue
		}
		if _, err := fn(callCtx, nil); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// buzzDispatchViaPool fans names out via the Buzz pool, yielding the RunAll
// limiter slot (if held) for the duration so pool workers can acquire it.
func buzzDispatchViaPool(ctx context.Context, p *buzz.Pool, names []string) error {
	lim := cache.LimiterFromContext(ctx)
	ancestors := buzz.AncestorsFromContext(ctx)
	return proc.RunChildSync(ctx, lim, func() error {
		childCtx := cache.WithoutSlotHeld(ctx)
		return p.Dispatch(childCtx, names, ancestors)
	})
}

// matchBuzzTargets matches registered Buzz target names against ctx.glob's patterns
// (suffix shorthand, "*" globs, and "!" negation). types.MatchTargetPatterns owns the
// semantics so this dispatch set, the dry-run tracer's, and describe's static edge set
// cannot drift apart.
func matchBuzzTargets(targets map[string]vm.Callable, patterns []string) []string {
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	return types.MatchTargetPatterns(names, patterns)
}

// ctxMarker identifies a value as a magus.Context, base or derived. An op call needs
// to tell a leading context from a leading opts table, and with a map-based value model
// and no protocol conformance in gopherbuzz on this base there is no type to ask. Not
// part of the authored surface; it disappears when the context becomes a real type.
const ctxMarker = "__magus_context"

// execRefusedDecls are the ctx members a magus\Exec answers with a refusal rather
// than a no-op, so ctx.withEnv({...}).readsFiles("x") fails loudly instead of
// declaring nothing. Named rather than written inline at its one use because it is
// one of three places that enumerate this context's members independently (the other
// two being buildTargetContext itself and the dry host's buildCtx), and a name is
// what lets a test hold them against each other.
var execRefusedDecls = []string{"needs", "glob", "readsFiles", "writesFiles", "modifiesExistingFiles", "envInputs", "observes", "hasCharm"}

// TargetContextKeys returns the member names bound on the magus\Context a target
// receives, and ExecRefusedKeys those a magus\Exec refuses. Same role as
// MagusModuleKeys one surface over: nothing but a test connects the enumerations
// above to the dry-run host's copy, so a declaration added to one and forgotten in
// another is silent until someone's body stops tracing.
func TargetContextKeys() []string { return buildTargetContext(nil, nil, nil, nil).MapKeys() }

// ExecRefusedKeys returns the ctx members a magus\Exec derivation refuses. It carries the
// same caveat as [TargetContextKeys]: execRefusedDecls is one of three independent
// enumerations of this context's members, and nothing but a test holds them against each
// other, so a declaration added to buildTargetContext or the dry host's buildCtx and
// forgotten here is silent until an Exec quietly accepts what it should refuse.
func ExecRefusedKeys() []string { return slices.Clone(execRefusedDecls) }

// buildTargetContext assembles the shared magus.Context value every target receives
// as its first argument. Its methods are the injected, per-target form of what used to
// be global magus.* declarations: `ctx.needs(format)` binds on the context the function
// received rather than a floating `magus.needs` attributed by lexical position.
//
//   - needs(...) dispatches the named dependencies - a target function, or a
//     ctx.glob(...) list of them - deduped through the pool.
//   - glob(pattern) resolves a pattern to matching target handles, feeding needs.
//   - readsFiles / writesFiles / modifiesExistingFiles declare the cache footprint, and
//     are no-ops at run time: it is read STATICALLY by describe.Extract (both arms of
//     any branch), so the body is never run to learn it. A non-literal argument is
//     caught there as DynamicIO at load, not here.
//   - envInputs / observes extend that footprint past the tree: an env var whose
//     process value keys the step, and a fact outside the tree entirely.
//   - has_charm(name) returns the live charm state.
//
// The value is stateless, so the session stashes one instance and reuses it for every
// target.
func buildTargetContext(obs buzz.DirectObserver, targets map[string]vm.Callable, exports map[string]vm.Value, ext *externalHandles) vm.Value {
	c := vm.NewMap()
	c.MapSet("needs", directVal(obs, "ctx.needs", buildBuzzNeeds(targets, exports, ext)))
	// ctx.glob(...): resolve glob patterns to matching target function handles, the
	// pattern resolver that feeds ctx.needs (ctx.needs(ctx.glob("*-generate"))). It
	// returns handles; ctx.needs dispatches them, so needs stays monomorphic.
	c.MapSet("glob", directVal(obs, "ctx.glob", buildBuzzGlob(targets, exports)))
	// File declarations are read statically by describe.Extract; at run time
	// they do nothing.
	c.MapSet(ctxMarker, vm.BoolValue(true))
	// ctx.withEnv({...}) / ctx.withCwd(".."): a magus\Exec, the EXECUTION-only context,
	// carrying overrides for the op calls made with it -
	// go["go-test"](ctx.withEnv({"CGO_ENABLED": "0"})).
	//
	// Named for WHAT DIFFERS, not the act of making it, following context.WithValue /
	// WithCancel / WithTimeout: at a call site you want to read the change.
	//
	// magus\Exec deliberately carries no declaration methods, so
	// ctx.withEnv({...}).inputs("x") fails loudly instead of silently no-op'ing - the
	// guarantee a checked type would give once gopherbuzz has protocol conformance.
	var execCtx func(env, cwd vm.Value) vm.Value
	execCtx = func(env, cwd vm.Value) vm.Value {
		e := vm.NewMap()
		e.MapSet(ctxMarker, vm.BoolValue(true))
		if !env.IsNull() {
			e.MapSet("env", env)
		}
		if !cwd.IsNull() {
			e.MapSet("cwd", cwd)
		}
		// Chainable: ctx.withEnv({...}).withCwd(".."). Each returns a fresh Exec, so a
		// derivation hoisted into a variable is never mutated by a later one.
		e.MapSet("withEnv", directVal(obs, "ctx.withEnv", func(_ context.Context, args []vm.Value) (vm.Value, error) {
			if len(args) == 0 || !args[0].IsMap() {
				return vm.Null, fmt.Errorf("ctx.withEnv: requires a {NAME: value} map")
			}
			return execCtx(args[0], cwd), nil
		}))
		e.MapSet("withCwd", directVal(obs, "ctx.withCwd", func(_ context.Context, args []vm.Value) (vm.Value, error) {
			if len(args) == 0 || !args[0].IsStr() {
				return vm.Null, fmt.Errorf("ctx.withCwd: requires a directory string")
			}
			return execCtx(env, args[0]), nil
		}))
		for _, decl := range execRefusedDecls {
			e.MapSet(decl, directVal(obs, "ctx."+decl, func(_ context.Context, _ []vm.Value) (vm.Value, error) {
				return vm.Null, fmt.Errorf(
					"ctx.%s: magus\\Exec carries execution overrides only; declare on the magus\\Context the target received", decl)
			}))
		}
		return e
	}
	// The base context's derivation pair IS the empty derivation's, so take it from
	// execCtx rather than writing the two closures (and their two error strings) a
	// second time. Only those keys are copied: the rest of an Exec is the refusal to
	// declare, which the base context must not inherit.
	for _, k := range []string{"withEnv", "withCwd"} {
		if v, ok := execCtx(vm.Null, vm.Null).MapGet(k); ok {
			c.MapSet(k, v)
		}
	}
	footprintDecl := func(_ context.Context, _ []vm.Value) (vm.Value, error) { return vm.Null, nil }
	c.MapSet("readsFiles", directVal(obs, "ctx.readsFiles", footprintDecl))
	c.MapSet("writesFiles", directVal(obs, "ctx.writesFiles", footprintDecl))
	// modifiesExistingFiles is outputs' explicit counterpart: the target changes an
	// existing file but does not own its whole contents, so magus neither deletes it
	// nor restores it from a snapshot. See types.UpdateRef.
	c.MapSet("modifiesExistingFiles", directVal(obs, "ctx.modifiesExistingFiles", footprintDecl))
	for old, replacement := range map[string]string{
		"inputs": "readsFiles", "outputs": "writesFiles", "updates": "modifiesExistingFiles",
	} {
		old, replacement := old, replacement
		c.MapSet(old, directVal(obs, "ctx."+old, func(_ context.Context, _ []vm.Value) (vm.Value, error) {
			return vm.Null, fmt.Errorf("ctx.%s was removed in v0.4; use ctx.%s instead", old, replacement)
		}))
	}
	// env names variables whose PROCESS value folds into the key - the counterpart to
	// withEnv, which carries a value written in the magusfile. Declaration only: the
	// static read collects the names, and hashing reads the values.
	// envInputs, not env: "env" is already the key carrying the Exec's actual
	// environment map, which spell dispatch reads back - a declaration under that name
	// silently replaced the environment with a no-op and dropped every withEnv override.
	c.MapSet("envInputs", directVal(obs, "ctx.envInputs", footprintDecl))
	// observes names an EXTERNAL fact the answer depends on but the tree does not
	// contain - a vulnerability feed's id, a remote schema's revision - so a target
	// whose answer moves with the world stays cacheable instead of opting out via
	// skip_cache. Both halves are written in the magusfile and hashed directly, making
	// it withEnv's mechanical twin and its semantic opposite: an override changes what
	// the tool RUNS WITH, an observation changes nothing about execution and only
	// states what the answer depends on.
	//
	// The VALUE is a cheap stamp the magusfile states - a version, a digest, a date.
	// magus stores it, compares it, and never interprets it: an observation that moves
	// is a miss, one that holds still replays. This is observation, not verification,
	// so an expensive probe belongs in the target BODY, where its cost is paid only on
	// a miss. Both arguments must be literals; the key is computed before the body
	// runs, so a computed value could not reach it and is rejected at load.
	c.MapSet("observes", directVal(obs, "ctx.observes", footprintDecl))
	c.MapSet("hasCharm", directVal(obs, "ctx.hasCharm", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		return vm.BoolValue(types.HasCharm(ctx, argStr(args, 0))), nil
	}))
	return c
}
