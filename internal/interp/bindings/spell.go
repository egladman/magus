package bindings

import (
	"context"
	"errors"
	"fmt"
	"github.com/egladman/magus/internal/interactive/tty"
	run "github.com/egladman/magus/internal/proc/run"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/internal/service/identity"
	"github.com/egladman/magus/internal/spellruntime"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

func init() {
	// Lazy-register so fast subcommands (help, version) skip the registration loop entirely.
	project.DefaultSpellRegistry().SetEnsureHook(ensureSpellsRegistered)
	// Validate magus/spell/<handle> imports at magusfile load (did-you-mean for typos).
	interp.RegisterBuzzSpellImportCheck(checkSpellImports)
}

var ensureSpellsRegistered = sync.OnceFunc(func() {
	for _, spec := range spellruntime.Builtins() {
		opts := []spells.Option{
			spells.WithSources(spec.Needs...),
			spells.WithIgnoreDirs(spec.IgnoreDirs...),
			spells.WithManifests(spec.Manifests...),
			spells.WithOutputs(spec.Provides...),
			spells.WithTargets(spec.OpNames()...),
			spells.WithServiceTargets(spec.ServiceOpNames()...),
			spells.WithInvoker(newSpellInvoker(spec.Ops, spec.Tools, spec.IgnoreDirs)),
			spells.WithTools(spec.Tools),
			spells.WithVersionProber(versionProber),
			spells.WithCommandRenderer(newCommandRenderer(spec.Ops)),
			spells.WithCommandExplainer(newCommandExplainer(spec.Ops)),
			spells.WithCommandConflicts(newCommandConflictChecker(spec.Ops)),
			spells.WithServiceView(newServiceViewer(spec.Ops)),
			spells.WithTargetSources(spec.TargetNeeds),
			spells.WithTargetCharms(charmNamesByTarget(spec.Ops)),
			spells.WithTargetDocs(docsByTarget(spec.Ops)),
		}
		if spec.Opaque {
			opts = append(opts, spells.WithOpaque())
		}
		if spec.Language != "" {
			opts = append(opts, spells.WithLanguage(spec.Language))
		}
		project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(spec.Name, opts...))
	}
})

// charmNamesByTarget extracts the sorted charm names each target declares, for
// discovery surfaces like `magus describe`.
func charmNamesByTarget(targets map[string]spells.Op) map[string][]string {
	out := make(map[string][]string, len(targets))
	for name, t := range targets {
		if len(t.Charms) == 0 {
			continue
		}
		names := make([]string, 0, len(t.Charms))
		for c := range t.Charms {
			names = append(names, c)
		}
		slices.Sort(names)
		out[name] = names
	}
	return out
}

// docsByTarget extracts each target handler's doc comment, for discovery surfaces
// like `magus describe`. Targets with no comment are omitted.
func docsByTarget(targets map[string]spells.Op) map[string]string {
	out := make(map[string]string, len(targets))
	for name, t := range targets {
		if t.Doc != "" {
			out[name] = t.Doc
		}
	}
	return out
}

// maxProbeOutput bounds what one probe contributes. A version line is one short
// line; a tool that ignores its version flag and prints its whole help - or a log -
// would otherwise be held whole and mixed into the cache key whole.
const maxProbeOutput = 4 << 10

// versionProber runs one tool's version argv in the project dir and returns trimmed
// stdout, bounded by maxProbeOutput. One function for every tool now that no binary
// is privileged: the argv comes from the Tool entry rather than being baked into a
// per-tool closure.
//
// It forks through run.Exec, like every other spell-declared command, rather than
// exec.Command directly. A spell's `probe` argv is authored input, so it must meet
// the sandbox's exec check; and the fork wants the process group and wait delay that
// let a probe which hangs be reaped with its children.
func versionProber(ctx context.Context, probe spells.Command, dir string) (string, error) {
	if probe.Bin == "" {
		return "", nil
	}
	res, err := run.Exec(ctx, probe.Bin, probe.Args, run.ExecOptions{Dir: dir, Capture: true, Quiet: true})
	if err != nil {
		return "", fmt.Errorf("version probe %s %v in %s: %w", probe.Bin, probe.Args, dir, err)
	}
	out := strings.TrimSpace(res.Stdout)
	if len(out) > maxProbeOutput {
		// The cut can land inside a rune, and the token is displayed as well as hashed.
		out = strings.ToValidUTF8(out[:maxProbeOutput], "")
	}
	return out, nil
}

// newCommandRenderer returns the command preview used by `magus describe`: it
// reports cmd plus the argv as reshaped by the active charms, executing nothing. A
// no-op marker op (empty Cmd) returns ok=false, since there is no command to show.
// A charm whose patch is well-formed but does not apply to this op's argv (an
// out-of-range index, a failing `test` op) returns the apply error, so `describe`
// reports it as MGS6001 instead of dropping the command line without a word.
func newCommandRenderer(targets map[string]spells.Op) func(string, []string) (string, []string, bool, error) {
	return func(target string, charms []string) (string, []string, bool, error) {
		op, ok := targets[target]
		if !ok || op.Bin == "" {
			return "", nil, false, nil
		}
		args, err := resolveCharmArgs(types.WithCharms(context.Background(), charms), op.Args, op.Charms)
		if err != nil {
			return "", nil, false, err
		}
		// The preview is the no-args invocation, so the declared defaults show
		// exactly where execution appends them: after the charm-patched base.
		args = append(args, op.DefaultArgs...)
		// A declared Sources is appended as its placeholder token: this renderer
		// takes no project dir (magus describe has none per-target either), so it
		// cannot run the runner's real per-project expansion - see
		// spells.Command.Sources and SourcesPlaceholder.
		args = append(args, op.SourcesPlaceholder()...)
		return op.Bin, args, true, nil
	}
}

// newCommandExplainer returns the charm-trace renderer used by `magus describe
// target --explain`: step 0 is the base command (no charms) and each later step
// is the command after one more active charm's patch, in magus's sorted-name
// application order. It mirrors newCommandRenderer's ok/err contract and executes
// nothing.
func newCommandExplainer(targets map[string]spells.Op) func(string, []string) ([]spells.CharmTraceStep, bool, error) {
	return func(target string, charms []string) ([]spells.CharmTraceStep, bool, error) {
		op, ok := targets[target]
		if !ok || op.Bin == "" {
			return nil, false, nil
		}
		var active []string
		ctx := types.WithCharms(context.Background(), charms)
		for name := range op.Charms {
			if types.HasCharm(ctx, name) {
				active = append(active, name)
			}
		}
		charmSteps, err := spellruntime.ExplainCharms(op.Args, op.Charms, active)
		if err != nil {
			return nil, false, err
		}
		// Prepend the base step, then prefix every step's argv with the bin so each
		// line is the full command a reader can compare top to bottom. Defaults
		// trail every step: charms patch Args only, and execution appends the
		// defaults after the patched base, so this is each step's real argv.
		full := func(args []string) []string {
			return append(append([]string{op.Bin}, args...), op.DefaultArgs...)
		}
		steps := make([]spells.CharmTraceStep, 0, len(charmSteps)+1)
		steps = append(steps, spells.CharmTraceStep{Command: full(op.Args)})
		for _, s := range charmSteps {
			steps = append(steps, spells.CharmTraceStep{Charm: s.Charm, Command: full(s.Command)})
		}
		return steps, true, nil
	}
}

// newCommandConflictChecker returns the charm-conflict detector used by `magus
// describe target`: it reports the active charms whose edit is overridden by another
// active charm on this op's argv (see spellruntime.Conflicts). It mirrors the renderer's
// ok/err contract and executes nothing.
func newCommandConflictChecker(targets map[string]spells.Op) func(string, []string) ([]spells.CharmConflict, bool, error) {
	return func(target string, charms []string) ([]spells.CharmConflict, bool, error) {
		op, ok := targets[target]
		if !ok || op.Bin == "" {
			return nil, false, nil
		}
		var active []string
		ctx := types.WithCharms(context.Background(), charms)
		for name := range op.Charms {
			if types.HasCharm(ctx, name) {
				active = append(active, name)
			}
		}
		conflicts, err := spellruntime.Conflicts(op.Args, op.Charms, active)
		if err != nil {
			return nil, false, err
		}
		return conflicts, true, nil
	}
}

// newServiceViewer returns the static service-facts accessor used by `magus describe
// target`: for a service op it reports the readiness probe, stop command, idle
// override, distinct reason, and fingerprint - all known without starting the
// service. ok is false for a non-service op. It executes nothing.
func newServiceViewer(targets map[string]spells.Op) func(string) (*spells.ServiceView, bool) {
	return func(target string) (*spells.ServiceView, bool) {
		op, ok := targets[target]
		if !ok || !op.IsService() || op.Service == nil {
			return nil, false
		}
		svc := spells.Service{
			Command:   spells.Command{Bin: op.Bin, Args: op.Args},
			Readiness: op.Service.Readiness,
			Stop:      op.Service.Stop,
			Idle:      op.Service.Idle,
			Distinct:  op.Service.Distinct,
		}
		view := &spells.ServiceView{
			Idle:        op.Service.Idle,
			Distinct:    op.Service.Distinct,
			Fingerprint: identity.Fingerprint(svc),
		}
		if op.Service.Readiness.Bin != "" {
			view.Readiness = append([]string{op.Service.Readiness.Bin}, op.Service.Readiness.Args...)
		}
		if op.Service.Stop.Bin != "" {
			view.Stop = append([]string{op.Service.Stop.Bin}, op.Service.Stop.Args...)
		}
		return view, true
	}
}

// noResult is the invoker's no-op outcome (no Data, no error) for a request that
// fans out to a spell with nothing to contribute: an unknown target, or a handler
// op on a built-in spell (no script body to run). nil Data is an ordinary invoker
// result (a command op returns it on success too; see spells.Spell.Invoke), so this
// is a real result rather than a sentinel; the helper names the intent and keeps
// the nilnil suppression in one documented place.
func noResult() (any, error) {
	return nil, nil //nolint:nilnil // documented invoker no-op: nil Data, nil error
}

// dispatchOp is the single op-dispatch bridge between the magus host and the Buzz
// interpreter. It resolves the request's target to a [spells.Op] and runs its
// declared command as a subprocess. An unknown target is a no-op, matching the
// fan-out-and-skip dispatch model. Every op is a command; in-VM work (a cache
// backend) is dispatched separately (see newBuzzSpellInvoker), not through here.
// readinessMemo records the outcome of each readiness probe per (bin, dir) for the
// life of the process. A probe forks, so re-running it for every op of every project
// would cost more than the failure it prevents; and its answer cannot change mid-run
// in a way magus could act on - a daemon that dies after the check fails the op
// anyway, with the tool's own message.
var readinessMemo sync.Map // key "bin\x00dir" -> error (nil when ready)

// checkReady gates an op on its tool being usable, when the spell declared a probe
// for that tool. Resolved through the op's own Bin, so no op restates which tool it
// runs, and a spell driving both docker and hadolint gates only the former.
//
// The probe's OUTPUT never enters a cache key: it is a precondition, not an input.
// `docker info` reports running containers and disk usage, so keying on it would
// invalidate every entry on every run.
func checkReady(ctx context.Context, tools map[string]spells.Tool, op spells.Op, dir string) error {
	t, ok := tools[op.Bin]
	if !ok || t.Ready.Bin == "" {
		return nil
	}
	probe := t.Ready
	key := op.Bin + "\x00" + dir
	if cached, hit := readinessMemo.Load(key); hit {
		if cached == nil {
			return nil
		}
		err, _ := cached.(error)
		return err
	}
	out := probeUntilReady(ctx, probe, op.Bin, dir)
	// A context error means THIS run was interrupted (Ctrl-C, a deadline), not that
	// the tool is unready - the memo's "cannot change mid-run" justification above
	// assumes one process per run, but the daemon is one process spanning many runs,
	// so storing it here would fail every later run's probe on this (bin, dir) with a
	// stale "context canceled" for as long as the daemon lives.
	if !errors.Is(out, context.Canceled) && !errors.Is(out, context.DeadlineExceeded) {
		readinessMemo.Store(key, out)
	}
	return out
}

// readinessGrace is how long an INTERACTIVE run waits for a tool to become usable
// before giving up. It is roughly what Docker Desktop takes to finish starting.
const readinessGrace = 30 * time.Second

// probeUntilReady runs the probe, and when a human is watching, keeps trying until the
// tool comes up or the grace period expires.
//
// Waiting is deliberately interactive-only. In CI or under an agent nobody is going to
// start a daemon mid-run, so a grace period there is minutes burned across projects to
// reach the same failure - and it would make a deterministic failure depend on how fast
// something else happened to start. At a terminal the trade inverts: the fix is one
// command away, and waiting saves re-running everything.
//
// Failing fast stays cheap either way: the check runs before the op forks, so nothing
// was consumed and a re-run replays from cache.
func probeUntilReady(ctx context.Context, probe spells.Command, tool, dir string) error {
	cmdline := strings.Join(append([]string{probe.Bin}, probe.Args...), " ")
	// Captured so the tool's OWN diagnosis reaches the user. `docker info` says
	// "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the
	// docker daemon running?" - it names the socket and asks the right question,
	// which is strictly better than anything magus can say about a tool it does not
	// own. Paraphrasing it was throwing away the most actionable line available.
	probeOp := spells.Op{Command: probe, Capture: true}
	probeOp.Command.Capture = true

	notReady := func(res run.ExecResult, cause error) error {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		// Wrapped, not formatted in: errors.Is still reaches the underlying exec
		// failure, matching how MGS3003 wraps its cause in std/os.go.
		if msg == "" {
			return types.WrapDiagnostic(types.ToolNotReady, cause,
				"%s is installed but not ready: `%s` failed. The op %q needs it running",
				tool, cmdline, tool)
		}
		return types.WrapDiagnostic(types.ToolNotReady, cause,
			"%s is installed but not ready. The op %q needs it running, and `%s` said:\n  %s",
			tool, tool, cmdline, strings.ReplaceAll(msg, "\n", "\n  "))
	}

	res, err := runCommand(ctx, probeOp, commandOpts{cwd: dir})
	if err == nil {
		return nil
	}
	if !tty.StdinIsTerminal() {
		return notReady(res, err)
	}

	slog.WarnContext(ctx, "waiting for tool to become ready",
		slog.String("tool", tool), slog.String("probe", cmdline),
		slog.Duration("grace", readinessGrace))

	deadline := time.Now().Add(readinessGrace)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		res, err = runCommand(ctx, probeOp, commandOpts{cwd: dir})
		if err == nil {
			slog.InfoContext(ctx, "tool became ready", slog.String("tool", tool))
			return nil
		}
		if time.Now().After(deadline) {
			return notReady(res, err)
		}
	}
}

func dispatchOp(ctx context.Context, ops map[string]spells.Op, tools map[string]spells.Tool, ignoreDirs []string, req spells.InvokeRequest) (any, error) {
	op, ok := ops[req.Target]
	if !ok {
		slog.DebugContext(ctx, "spell: target not provided by this spell (fan-out skip)", "target", req.Target, "dir", req.Dir)
		return noResult()
	}
	slog.DebugContext(ctx, "spell: dispatch command", "target", req.Target, "cmd", op.Bin, "dir", req.Dir)
	// Ahead of every other setup step: a tool that cannot run makes the rest moot,
	// and the point is to fail with what is actually wrong rather than let the op
	// fork and report a build failure for a project with nothing wrong with it.
	if err := checkReady(ctx, tools, op, req.Dir); err != nil {
		return nil, err
	}
	opts := commandOpts{op: req.Target, cwd: req.Dir, args: project.ExtraArgs(ctx), ignoreDirs: ignoreDirs}
	// The reserved `scip` op writes its index into the cache, not the tree: magus
	// hands it the destination via MAGUS_SYMBOL_INDEX so the spell command (a bare
	// "$MAGUS_SYMBOL_INDEX" arg token, resolved by resolveRunnerRefs) needs no
	// knowledge of where the cache is. Set on both opts.refs (what a spell's Args
	// token resolves against) and opts.env (the process environment), so a
	// workspace-local scip spell that still shells out - the doc comment on
	// symbols.IndexEnvVar promises the env var is set - keeps working.
	if req.Target == symbols.IndexOp {
		env, err := symbolIndexEnv(ctx, req.Dir)
		if err != nil {
			return nil, err
		}
		opts.env = env
		opts.refs = env
	}
	_, err := runCommand(ctx, op, opts)
	return nil, err
}

// resolveSecretEnv resolves a command's declared secrets (env var name -> provider
// reference) through the run's secret resolver and returns base plus the resolved
// values, as a fresh map - base is never mutated, so a mid-loop provider failure
// cannot leave already-resolved values behind in a map the caller retains.
// secret.Resolver.Read is what registers each value for redaction (internal/secret),
// so a secret reaching a child through this path is masked out of captured output
// the same as one a magusfile body read directly.
//
// Names are resolved in sorted order so resolution - and any provider logging along
// the way - is deterministic regardless of map iteration order.
//
// A name already present in base is an error rather than a silently dropped or
// overridden value: base is env magus already set for this op (MAGUS_SYMBOL_INDEX on
// the scip op, a magusfile's withEnv overlay), and a declared secret landing on the
// same name is an authoring bug in a security-sensitive path, not something to
// resolve quietly by picking a winner.
func resolveSecretEnv(ctx context.Context, opName string, refs, base map[string]string) (map[string]string, error) {
	resolver := secret.ResolverFromContext(ctx)
	if resolver == nil {
		return nil, fmt.Errorf("spell: op %q declares secrets but no secret resolver is on this run", opName)
	}
	env := make(map[string]string, len(base)+len(refs))
	maps.Copy(env, base)
	names := make([]string, 0, len(refs))
	for name := range refs {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if _, exists := env[name]; exists {
			return nil, fmt.Errorf("spell: op %q secret env %q collides with env already set for this op", opName, name)
		}
		v, err := resolver.Read(ctx, refs[name])
		if err != nil {
			return nil, fmt.Errorf("spell: op %q secret %q: %w", opName, name, err)
		}
		// Reveal where the credential crosses into a child process's environment, which
		// is the boundary Command.secrets declared it for.
		env[name] = v.Reveal()
	}
	return env, nil
}

// symbolIndexEnv resolves the cache destination for a `scip` op run and returns it as
// the MAGUS_SYMBOL_INDEX environment binding, having created the containing dir. It
// requires the active cache (the op runs inside a cached target), so the index lands
// where ingestion later looks; run outside that path it errors rather than emit an
// index the graph will never find.
func symbolIndexEnv(ctx context.Context, projectDir string) (map[string]string, error) {
	c := cache.FromContext(ctx)
	if c == nil {
		return nil, fmt.Errorf("spell: the %q op must run as a magus target so its index lands in the cache", symbols.IndexOp)
	}
	abs := projectDir
	if !filepath.IsAbs(abs) {
		cwd, err := std.EffectiveCwd(ctx)
		if err != nil {
			return nil, fmt.Errorf("spell: resolve %q op dir: %w", symbols.IndexOp, err)
		}
		abs = filepath.Join(cwd, abs)
	}
	path := symbols.IndexPath(c.Dir(), abs)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("spell: prepare symbol index dir: %w", err)
	}
	return map[string]string{symbols.IndexEnvVar: path}, nil
}

// newSpellInvoker returns an invoker closure for a built-in spell. Built-in ops
// are command-only (cmd/args/charms data, no script body). ignoreDirs is the
// spell's own declared mgs_listIgnoreDirs, carried through to dispatchOp so a
// Command.Sources op inherits it (see commandOpts.ignoreDirs).
func newSpellInvoker(targets map[string]spells.Op, tools map[string]spells.Tool, ignoreDirs []string) func(context.Context, spells.InvokeRequest) (any, error) {
	return func(ctx context.Context, req spells.InvokeRequest) (any, error) {
		return dispatchOp(ctx, targets, tools, ignoreDirs, req)
	}
}

// commandTargetNames returns the spell's command target names, sorted. Every op is
// a command, so this is all of them.
func commandTargetNames(targets map[string]spells.Op) []string {
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// loadLocalSpell compiles a workspace-local Buzz spell at path and registers
// it, returning its spec and ok=false on any failure. Errors are logged, not
// raised: discovery paths cannot route an error back to a caller.
func loadLocalSpell(ctx context.Context, path string) (spells.Descriptor, bool) {
	if !filepath.IsAbs(path) {
		cwd, err := std.EffectiveCwd(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "load local spell: getwd", "err", err)
			return spells.Descriptor{}, false
		}
		path = filepath.Join(cwd, path)
	}
	return loadLocalBuzzSpell(ctx, path)
}

// loadSpellFile loads a spell file as a function-op-capable SpellDriver and
// registers it: the in-package entry point the remote cache backend uses to
// resolve a backend selected by a file path. A .buzz spell loads through the Buzz
// path (registering a function-op spell eagerly, capturing its source for in-VM
// dispatch).
func loadSpellFile(ctx context.Context, path string) (spells.Driver, error) {
	if !strings.HasSuffix(path, ".buzz") {
		return nil, fmt.Errorf("spell file %q: must be a .buzz spell", path)
	}
	_, sp, err := loadBuzzSpell(ctx, path)
	if err != nil {
		return nil, err // explicit nil: don't return a typed-nil *spells.Spell as a non-nil interface
	}
	return sp, nil
}

// loadLocalBuzzSpell compiles a workspace-local Buzz spell at path, returning its
// spec and ok=false on any failure. Extract routes through the same spellruntime.Decode
// a built-in uses, so a .buzz workspace spell and a built-in are read and validated
// identically. Errors are logged, not raised, since discovery paths cannot route an
// error back to the caller. Registration is deferred to magus.project; the handle
// the caller builds carries the resolved spec so it registers by value there.
func loadLocalBuzzSpell(ctx context.Context, path string) (spells.Descriptor, bool) {
	// loadBuzzSpell registers the spell with the function-op invoker (capturing its
	// source), so an imported Buzz spell carries function-ops whether it is later
	// bound to a project or wired as the remote cache backend. project bind finds
	// it already registered and binds it by name.
	m, _, err := loadBuzzSpell(ctx, path)
	if err != nil {
		// A plain Buzz library imported by name (not a spell) is expected here:
		// resolution falls through to a normal module import. Only a genuinely
		// malformed spell is worth logging.
		if !errors.Is(err, spellruntime.ErrNotASpell) {
			slog.ErrorContext(ctx, "load local spell: buzz", "path", path, "err", err)
		}
		return spells.Descriptor{}, false
	}
	return m, true
}

// localSpellBaseOptions builds the SpellOptions common to every workspace-local
// spell registration (cache metadata, command renderer, charm/doc discovery),
// minus the invoker, which each registration path supplies itself.
func localSpellBaseOptions(m spells.Descriptor) []spells.Option {
	opts := []spells.Option{
		spells.WithSources(m.Needs...),
		spells.WithIgnoreDirs(m.IgnoreDirs...),
		spells.WithManifests(m.Manifests...),
		spells.WithOutputs(m.Provides...),
		spells.WithTargets(m.OpNames()...),
		spells.WithServiceTargets(m.ServiceOpNames()...),
		spells.WithCommandRenderer(newCommandRenderer(m.Ops)),
		spells.WithCommandExplainer(newCommandExplainer(m.Ops)),
		spells.WithCommandConflicts(newCommandConflictChecker(m.Ops)),
		spells.WithServiceView(newServiceViewer(m.Ops)),
		spells.WithTargetCharms(charmNamesByTarget(m.Ops)),
		spells.WithTargetDocs(docsByTarget(m.Ops)),
		spells.WithDocRequiredTargets(m.DocOps...),
	}
	if m.Opaque {
		opts = append(opts, spells.WithOpaque())
	}
	if m.Language != "" {
		opts = append(opts, spells.WithLanguage(m.Language))
	}
	opts = append(opts, spells.WithTools(m.Tools), spells.WithVersionProber(versionProber))
	return opts
}

// registerLocalSpell registers a decoded fork-only workspace-local spell into the
// default registry. The shared spellruntime.Decode produces m for the imported Buzz
// spell by-value path, so this is the single deferred registration point (called at
// magus.project bind time). A function-op spell instead registers eagerly at load
// via loadBuzzSpell.
func registerLocalSpell(m spells.Descriptor) {
	opts := append(localSpellBaseOptions(m), spells.WithInvoker(newSpellInvoker(m.Ops, m.Tools, m.IgnoreDirs)))
	project.DefaultSpellRegistry().RegisterIfAbsent(spells.NewSpell(m.Name, opts...))
}

// commonSpellAliases maps a name a user might reach for to the spell that actually
// serves it. Every entry is now a genuine SYNONYM, not an abbreviation: spells are
// named for the thing they adapt (typescript, rust, python, markdown, go), so the
// language name is the real name and needs no translation.
//
// The abbreviations this used to carry - typescript->ts, rust->rs, python->py,
// markdown->md, golang->go - are gone with the rename that made them unnecessary.
// A table existing to convert what users naturally type into what something was
// actually called is a report about the name, not a feature.
var commonSpellAliases = map[string]string{
	"javascript": "typescript",
	"js":         "typescript",
	"node":       "typescript",
	"nodejs":     "typescript",
	"python3":    "python",
	"cargo":      "rust",
}

// checkSpellImports validates the handles a magusfile imports via
// `import "magus/spell/<handle>"`. An unknown handle resolves to nothing and would
// otherwise surface later as a disconnected "undefined" error, so we fail fast
// here with a did-you-mean suggestion. Registered from init() via
// interp.RegisterBuzzSpellImportCheck.
func checkSpellImports(handles []string) error {
	for _, h := range handles {
		// The magusfile driver IS registered - dispatch needs it - so the generic
		// check below would happily accept this import even though the handle it
		// binds is now unusable. Rejected explicitly, with the migration.
		if h == magusfileSpellName {
			return magusfileNotASpellErr("importing it")
		}
		if isRegisteredSpell(h) {
			continue
		}
		return errors.New(unknownSpellMessage(h))
	}
	return nil
}

// isRegisteredSpell reports whether name is a handle reachable as
// `import "magus/spell/<name>"`: a compiled built-in or a host-registered spell.
// This mirrors the native modules registerAllBuzz installs, so the check can
// never reject a handle the import would actually resolve.
func isRegisteredSpell(name string) bool {
	if _, ok := spellruntime.Builtins()[name]; ok {
		return true
	}
	_, ok := project.DefaultSpellRegistry().Lookup(name)
	return ok
}

func unknownSpellMessage(name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "no spell %q to import as \"magus/spell/%s\"", name, name)
	if s := suggestSpellName(name); s != "" {
		fmt.Fprintf(&b, "; did you mean %q (import \"magus/spell/%s\")", s, s)
	}
	fmt.Fprintf(&b, "\nbuilt-in spells: %s", strings.Join(builtinSpellHandles(), ", "))
	return b.String()
}

// suggestSpellName returns the handle a user most likely meant, or "" if nothing
// is close. A known language/tool alias wins outright; otherwise the nearest
// registered handle by edit distance, within a small threshold.
func suggestSpellName(name string) string {
	lower := strings.ToLower(name)
	if h, ok := commonSpellAliases[lower]; ok {
		return h
	}
	const threshold = 3
	best, bestDist := "", threshold+1
	for _, h := range builtinSpellHandles() {
		if d := hint.Distance(lower, h); d < bestDist || (d == bestDist && h < best) {
			best, bestDist = h, d
		}
	}
	if bestDist > threshold {
		return ""
	}
	return best
}

// builtinSpellHandles returns the compiled-in spell handles, sorted. Used both for
// the suggestion search and the handles listed in the error.
func builtinSpellHandles() []string {
	b := spellruntime.Builtins()
	out := make([]string, 0, len(b))
	for name := range b {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
