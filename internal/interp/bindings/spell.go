package bindings

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc/spawn"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/internal/service/identity"
	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/internal/symbols/buzzindex"
	"github.com/egladman/magus/internal/symbols/goindex"
	"github.com/egladman/magus/internal/symbols/shindex"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

func init() { //nolint:gochecknoinits // project cannot link the Buzz VM; the ensure hook and import check register from the layer that can
	// Lazy-register so fast subcommands (help, version) skip the registration loop entirely.
	project.DefaultSpellRegistry().SetEnsureHook(ensureSpellsRegistered)
	// Validate magus/spell/<handle> imports at magusfile load (did-you-mean for typos).
	interp.RegisterBuzzSpellImportCheck(checkSpellImports)
}

var ensureSpellsRegistered = sync.OnceFunc(func() {
	for _, spec := range spell.Builtins() {
		opts := []spells.Option{
			spells.WithSources(spec.Needs...),
			spells.WithIgnoreDirs(spec.IgnoreDirs...),
			spells.WithManifests(spec.Manifests...),
			spells.WithScriptRunners(spec.ScriptRunners...),
			spells.WithOutputs(spec.Provides...),
			spells.WithTargets(spec.OpNames()...),
			spells.WithServiceTargets(spec.ServiceOpNames()...),
			spells.WithInvoker(newSpellInvoker(spec)),
			spells.WithOps(spec.Ops),
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
		if len(spec.LanguageExtensions) > 0 {
			opts = append(opts, spells.WithLanguageExtensions(spec.LanguageExtensions))
		}
		if spec.Syntax != nil {
			opts = append(opts, spells.WithSyntax(spec.Syntax))
		}
		if spec.SymbolIndexer != nil {
			opts = append(opts, spells.WithSymbolIndexer(spec.SymbolIndexer))
		}
		if spec.Sandbox != nil {
			opts = append(opts, spells.WithSandbox(spec.Sandbox))
		}
		project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(spec.Name, opts...))
	}
})

// charmNamesByTarget extracts the sorted charm names each target declares, for
// discovery commands like `magus describe`.
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

// docsByTarget extracts each target handler's doc comment, for discovery commands
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
// line; a tool that ignores its version flag and prints its whole help (or a log)
// would otherwise be held whole and mixed into the cache key whole.
const maxProbeOutput = 4 << 10

// versionProber runs one tool's version argv in the project dir and returns trimmed
// stdout, bounded by maxProbeOutput. One function for every tool now that no binary
// is privileged: the argv comes from the Tool entry rather than being baked into a
// per-tool closure.
//
// It forks through spawn.Exec, like every other spell-declared command, rather than
// exec.Command directly. A spell's `probe` argv is authored input, so it must meet
// the sandbox's exec check; and the fork wants the process group and wait delay that
// let a probe which hangs be reaped with its children.
func versionProber(ctx context.Context, probe spells.Command, dir string) (string, error) {
	if probe.Bin == "" {
		return "", nil
	}
	res, err := spawn.Exec(ctx, probe.Bin, probe.Args, spawn.ExecOptions{Dir: dir, Capture: true, Quiet: true})
	if err != nil {
		// The exit status alone says nothing; the tool's own words usually name the cause
		// (pnpm: `Command "tsc" not found`). pnpm reports on stdout, so stdout is the
		// fallback.
		tail := causeLine(res.Stderr)
		if tail == "" {
			tail = causeLine(res.Stdout)
		}
		if tail != "" {
			return "", fmt.Errorf("version probe %s %v in %s: %s: %w", probe.Bin, probe.Args, dir, tail, err)
		}
		return "", fmt.Errorf("version probe %s %v in %s: %w", probe.Bin, probe.Args, dir, err)
	}
	out := strings.TrimSpace(res.Stdout)
	if len(out) > maxProbeOutput {
		// The cut can land inside a rune, and the token is displayed as well as hashed.
		out = strings.ToValidUTF8(out[:maxProbeOutput], "")
	}
	return out, nil
}

// maxProbeTail bounds the line a failed probe quotes in its error.
const maxProbeTail = 240

// causeLine picks the line of a failed probe's output that names why: the first one
// that mentions an error and is not a warning, else the first that mentions an error,
// else the last. mise ends on a "run with --verbose" footer and pnpm opens with a stray
// "undefined", so neither end alone is the cause; and a mise warning can quote an HTTP
// "client error" ahead of the ERROR line that failed the probe.
func causeLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	mentions := func(l, word string) bool { return strings.Contains(strings.ToLower(l), word) }
	i := slices.IndexFunc(lines, func(l string) bool { return mentions(l, "err") && !mentions(l, "warn") })
	if i < 0 {
		i = slices.IndexFunc(lines, func(l string) bool { return mentions(l, "err") })
	}
	if i < 0 {
		i = len(lines) - 1
	}
	line := strings.Join(strings.Fields(lines[i]), " ")
	if len(line) > maxProbeTail {
		line = strings.ToValidUTF8(line[:maxProbeTail], "")
	}
	return line
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
		// TrailingArgs land last of all in a real run; on the no-args preview
		// nothing forwarded comes between them and DefaultArgs.
		args = append(args, op.TrailingArgs...)
		// A declared Sources is appended as its placeholder token: this renderer
		// takes no project dir (magus describe has none per-target either), so it
		// cannot run the runner's real per-project expansion; see
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
		charmSteps, err := spell.ExplainCharms(op.Args, op.Charms, active)
		if err != nil {
			return nil, false, err
		}
		// Prepend the base step, then prefix every step's argv with the bin so each
		// line is the full command a reader can compare top to bottom. Defaults
		// trail every step: charms patch Args only, and execution appends the
		// defaults after the patched base, so this is each step's real argv.
		full := func(args []string) []string {
			return append(append(append([]string{op.Bin}, args...), op.DefaultArgs...), op.TrailingArgs...)
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
// active charm on this op's argv (see spell.Conflicts). It mirrors the renderer's
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
		conflicts, err := spell.Conflicts(op.Args, op.Charms, active)
		if err != nil {
			return nil, false, err
		}
		return conflicts, true, nil
	}
}

// newServiceViewer returns the static service-facts accessor used by `magus describe
// target`: for a service op it reports the readiness probe, stop command, idle
// override, distinct reason, and fingerprint, all known without starting the
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
// in a way magus could act on: a daemon that dies after the check fails the op
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
	key := fmt.Sprintf("%s\x00%s", op.Bin, dir)
	if cached, hit := readinessMemo.Load(key); hit {
		if cached == nil {
			return nil
		}
		err, _ := cached.(error)
		return err
	}
	out := probeUntilReady(ctx, probe, op.Bin, dir)
	// A context error means THIS run was interrupted (Ctrl-C, a deadline), not that
	// the tool is unready: the memo's "cannot change mid-run" justification above
	// assumes one process per run, but the server is one process spanning many runs,
	// so storing it here would fail every later run's probe on this (bin, dir) with a
	// stale "context canceled" for as long as the server lives.
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
// reach the same failure, and it would make a deterministic failure depend on how fast
// something else happened to start. At a terminal the trade inverts: the fix is one
// command away, and waiting saves re-running everything.
//
// Failing fast stays cheap either way: the check runs before the op forks, so nothing
// was consumed and a re-run replays from cache.
func probeUntilReady(ctx context.Context, probe spells.Command, tool, dir string) error {
	cmdline := strings.Join(append([]string{probe.Bin}, probe.Args...), " ")
	// Captured so the tool's OWN diagnosis reaches the user. `docker info` says
	// "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the
	// docker daemon running?": it names the socket and asks the right question,
	// which is strictly better than anything magus can say about a tool it does not
	// own. Paraphrasing it was throwing away the most actionable line available.
	probe.Capture = true
	probeOp := spells.Op{Command: probe}

	notReady := func(res spawn.ExecResult, cause error) error {
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

// runInstall resolves the project's install against its live lockfile, seeds a missing
// dependency tree, and runs the package manager. Under a run it goes through the run's
// install runner, so a replayable install forks nothing whichever way it was reached.
func runInstall(ctx context.Context, op spells.Op, opts commandOpts) error {
	dir := opts.cwd
	if !filepath.IsAbs(dir) {
		base, err := std.EffectiveCurrentWorkingDirectory(ctx)
		if err != nil {
			return err
		}
		dir = filepath.Join(base, dir)
	}
	opts.cwd = dir
	opts.spell = op.Install.Spell
	stop := dir
	if ws := types.WorkspaceFromContext(ctx); ws != nil {
		stop = ws.Root()
	}
	choice, found, err := spell.ResolveInstall(op.Install, dir, stop)
	if err != nil {
		return err
	}
	if !found {
		slog.With(attr.Component("spell")).DebugContext(ctx, "nothing to install (no manifest with a lockfile)", "dir", dir)
		return nil
	}
	body := func(ctx context.Context) error {
		switch from, found, err := spell.SeedInstall(ctx, choice, dir); {
		case err != nil:
			slog.With(attr.Component("magus")).WarnContext(ctx, "could not seed the dependency tree from another checkout; installing without it",
				slog.String("dir", filepath.Join(dir, choice.Install.Dir)), attr.Error(err))
		case found:
			slog.With(attr.Component("magus")).InfoContext(ctx, "seeded the dependency tree from another checkout",
				slog.String("dir", filepath.Join(dir, choice.Install.Dir)), slog.String("from", from))
		}
		_, err := runCommand(ctx, spells.Op{Command: choice.Install.Command}, opts)
		return err
	}
	if r := types.InstallRunnerFromContext(ctx); r != nil {
		return r(ctx, dir, op.Install.Spell, opts.op, choice, body)
	}
	return body(ctx)
}

func dispatchOp(ctx context.Context, spec spells.Descriptor, req spells.InvokeRequest) (any, error) {
	op, ok := spec.Ops[req.Target]
	if !ok {
		slog.With(attr.Component("spell")).DebugContext(ctx, "target not provided by this spell (fan-out skip)", "target", req.Target, "dir", req.Dir)
		return noResult()
	}
	slog.With(attr.Component("spell")).DebugContext(ctx, "dispatch command", "target", req.Target, "cmd", op.Bin, "dir", req.Dir)
	// Ahead of every other setup step: a tool that cannot run makes the rest moot,
	// and the point is to fail with what is actually wrong rather than let the op
	// fork and report a build failure for a project with nothing wrong with it.
	if err := checkReady(ctx, spec.Tools, op, req.Dir); err != nil {
		return nil, err
	}
	opts := commandOpts{spell: spec.Name, op: req.Target, cwd: req.Dir, args: project.ExtraArgs(ctx), ignoreDirs: spec.IgnoreDirs}
	if err := runOp(ctx, spec, op, opts); err != nil {
		return nil, err
	}
	return noResult()
}

// runOp runs op by its kind, which magus stamps on the ops it synthesizes.
// Matching the op's name instead would make a capability a reserved string any
// spell could claim by spelling it.
func runOp(ctx context.Context, spec spells.Descriptor, op spells.Op, opts commandOpts) error {
	switch op.Kind {
	case spells.OpKindInstall:
		return runInstall(ctx, op, opts)
	case spells.OpKindCoverage:
		return runCoverage(ctx, spec.Name, op, opts)
	case spells.OpKindSymbolIndex:
		return runSymbolIndex(ctx, spec, op, opts)
	}
	_, err := runCommand(ctx, op, opts)
	return err
}

// runSymbolIndex writes the project's index into the cache, not the tree. A
// command indexer gets the destination as MAGUS_SYMBOL_INDEX, in both its refs
// and its environment, so one that shells out still finds it.
func runSymbolIndex(ctx context.Context, spec spells.Descriptor, op spells.Op, opts commandOpts) error {
	projectDir, indexPath, err := symbolIndexEnv(ctx, opts.cwd, opts.op)
	if err != nil {
		return err
	}
	var envs []map[string]string
	var format spells.SymbolFormat
	if spec.SymbolIndexer != nil {
		envs, format = spec.SymbolIndexer.Envs, spec.SymbolIndexer.Format
	}
	if format == spells.SymbolFormatGoPackages {
		return runGoPackagesIndex(ctx, spec.Name, op, projectDir, indexPath, envs)
	}
	if len(envs) > 0 {
		return fmt.Errorf("spell %q symbol indexer: envs need magus's in-process Go indexer (format %s), since %q runs once, in the host's environment",
			spec.Name, spells.SymbolFormatGoPackages, op.Bin)
	}
	if format == spells.SymbolFormatBuzzModule || format == spells.SymbolFormatShellScript {
		return runFileIndex(ctx, spec.Name, format, projectDir, indexPath)
	}
	return runSCIPIndex(ctx, spec.Name, op, opts, indexPath)
}

func runGoPackagesIndex(ctx context.Context, spellName string, op spells.Op, projectDir, indexPath string, envs []map[string]string) error {
	patterns := slices.Clone(op.Args)
	if len(patterns) == 0 {
		patterns = []string{"."}
	}
	return runUnitIndexer(ctx, spellName, "Go", projectDir, indexPath, &goindex.Indexer{Dir: projectDir, Patterns: patterns, Builds: envs})
}

// runFileIndex runs the in-process Buzz or shell indexer, which names symbols
// by workspace path.
func runFileIndex(ctx context.Context, spellName string, format spells.SymbolFormat, projectDir, indexPath string) error {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return fmt.Errorf("spell %q symbol indexer: %s names symbols by workspace path, and this run has no workspace", spellName, format)
	}
	if format == spells.SymbolFormatBuzzModule {
		return runUnitIndexer(ctx, spellName, "Buzz", projectDir, indexPath, &buzzindex.Indexer{Dir: projectDir, WorkspaceRoot: ws.Root()})
	}
	return runUnitIndexer(ctx, spellName, "shell", projectDir, indexPath, &shindex.Indexer{Dir: projectDir, WorkspaceRoot: ws.Root()})
}

// runSCIPIndex runs a command indexer, which writes SCIP beside the pack; the
// pack is what readers open.
func runSCIPIndex(ctx context.Context, spellName string, op spells.Op, opts commandOpts, indexPath string) error {
	base := map[string]string{}
	if ws := types.WorkspaceFromContext(ctx); ws != nil {
		base[symbols.WorkspaceRootEnvVar] = ws.Root()
	}
	scipPath := indexPath + ".scip"
	defer os.Remove(scipPath)
	if err := runSymbolIndexer(ctx, spellName, op, opts, base, scipPath); err != nil {
		return err
	}
	if err := symbols.PackFromSCIP(scipPath, indexPath, "scip "+op.Bin); err != nil {
		return fmt.Errorf("spell %q symbol indexer: storing the index %q wrote: %w", spellName, op.Bin, err)
	}
	return nil
}

// runCoverage runs a coverage op with MAGUS_COVERAGE_DIR set to its report
// dir, in its refs and its environment. The dir is emptied first, so a report
// an earlier run left never reads as this run's.
func runCoverage(ctx context.Context, spellName string, op spells.Op, opts commandOpts) error {
	dir, err := coverageDir(ctx, opts.cwd)
	if err != nil {
		return err
	}
	refs := map[string]string{spells.CoverageDirEnvVar: dir}
	opts.env, opts.refs = maps.Clone(refs), refs
	if _, err := runCommand(ctx, op, opts); err != nil {
		return err
	}
	return checkCoverageReport(spellName, op, dir)
}

// coverageDir is the project's report dir, emptied. It needs the active cache,
// since the report is read from there.
func coverageDir(ctx context.Context, projectDir string) (string, error) {
	c := cache.FromContext(ctx)
	if c == nil {
		return "", errors.New("a coverage op must run as a magus target so its report lands in the cache")
	}
	_, project, err := projectUnderWorkspace(ctx, projectDir)
	if err != nil {
		return "", err
	}
	dir := coverage.Dir(c.Dir(), project)
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("empty the coverage dir: %w", err)
	}
	return dir, os.MkdirAll(dir, 0o755)
}

// checkCoverageReport refuses an op that exited 0 without writing the report
// it declares. Unchecked, the project would read as never covered.
func checkCoverageReport(spellName string, op spells.Op, dir string) error {
	if op.Coverage == nil {
		return fmt.Errorf("spell %q: the coverage op carries no mgs_getCoverage declaration", spellName)
	}
	switch _, err := os.Stat(filepath.Join(dir, op.Coverage.File)); {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("spell %q declares coverage, but %q exited 0 and wrote no %s to %s, it must write it under the directory magus passes in %s",
			spellName, op.Bin, op.Coverage.File, dir, spells.CoverageDirEnvVar)
	case err != nil:
		return fmt.Errorf("spell %q coverage: reading the report %q wrote: %w", spellName, op.Bin, err)
	}
	return nil
}

// runUnitIndexer brings the pack at indexPath up to date with one of magus's in-process
// indexers, re-indexing only the units whose files moved and the dependents a removed
// symbol reaches. Each file the pack holds no document for is warned about by name on
// every run, not only the run that met it.
func runUnitIndexer(ctx context.Context, spellName, language, dir, indexPath string, ix symbols.UnitIndexer) error {
	res, err := symbols.UpdatePack(ctx, indexPath, ix)
	if err != nil {
		return fmt.Errorf("spell %q symbol indexer: %w", spellName, err)
	}
	log := slog.With(attr.Component("symbols"))
	for _, gap := range res.Gaps {
		log.WarnContext(ctx, fmt.Sprintf("the %s symbol index leaves this out", language), slog.String("gap", gap))
	}
	log.InfoContext(ctx, fmt.Sprintf("updated the %s symbol index", language),
		slog.String("dir", dir),
		slog.Int("indexed", len(res.Indexed)),
		slog.Int("dependents", len(res.Dependents)),
		slog.Int("dropped", len(res.Dropped)),
		slog.Int("kept", res.Kept),
		slog.Int("gaps", len(res.Gaps)),
		slog.Bool("rebuilt", res.Rebuilt))
	return nil
}

// runSymbolIndexer runs op once, handing it dest as MAGUS_SYMBOL_INDEX. base is the rest
// of what magus sets for the run.
func runSymbolIndexer(ctx context.Context, spellName string, op spells.Op, opts commandOpts, base map[string]string, dest string) error {
	refs := maps.Clone(base)
	refs[symbols.IndexEnvVar] = dest
	opts.env = maps.Clone(refs)
	opts.refs = refs
	if _, err := runCommand(ctx, op, opts); err != nil {
		return err
	}
	// Checked HERE because this is the site that made the promise: it handed the
	// indexer a destination, so it is the one place that knows what was supposed to
	// appear and can still name the op that failed to write it.
	//
	// Without it an indexer could exit 0 having written nothing and the project still
	// read as symbol-capable with no index, while `graph build` reported it reindexed
	// and meant it. Ingestion cannot raise this: a missing index there is
	// indistinguishable from one never built.
	// Only a MISSING file is the indexer's fault. A permission error or a broken path
	// reads as "wrote nothing" otherwise, which sends the spell author to debug a command
	// that worked. The format is deliberately not named here: op.Bin and the path are the
	// diagnosis, and reading it off spec would mean trusting that the caller kept
	// spec.SymbolIndexer in step with op.Kind, which only Decode guarantees.
	switch _, err := os.Stat(dest); {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("spell %q declares a symbol indexer, but %q exited 0 and wrote no index to %s, it must write to the path magus passes in %s",
			spellName, op.Bin, dest, symbols.IndexEnvVar)
	case err != nil:
		return fmt.Errorf("spell %q symbol indexer: reading the index %q wrote: %w", spellName, op.Bin, err)
	}
	return nil
}

// resolveSecretEnv resolves a command's declared secrets (env var name -> provider
// reference) through the run's secret resolver and returns base plus the resolved
// values, as a fresh map. The caller's base is never mutated, so a mid-loop provider failure
// cannot leave already-resolved values behind in a map the caller retains.
// secret.Resolver.Read is what registers each value for redaction (internal/secret),
// so a secret reaching a child through this path is masked out of captured output
// the same as one a magusfile body read directly.
//
// Names are resolved in sorted order so resolution (and any provider logging along
// the way) is deterministic regardless of map iteration order.
//
// A name already present in base is an error rather than a silently dropped or
// overridden value. Base is env magus already set for this op (MAGUS_SYMBOL_INDEX on
// the scip op, a magusfile's withEnv overlay). A declared secret landing on the
// same name is an authoring bug in a security-sensitive path, not something to
// resolve quietly by picking a winner.
func resolveSecretEnv(ctx context.Context, opName string, refs, base map[string]string) (map[string]string, error) {
	resolver := secret.ResolverFromContext(ctx)
	if resolver == nil {
		return nil, fmt.Errorf("op %q declares secrets but no secret resolver is on this run", opName)
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
			return nil, fmt.Errorf("op %q secret env %q collides with env already set for this op", opName, name)
		}
		v, err := resolver.Read(ctx, refs[name])
		if err != nil {
			return nil, fmt.Errorf("op %q secret %q: %w", opName, name, err)
		}
		// Reveal where the credential crosses into a child process's environment, which
		// is the boundary Command.secrets declared it for.
		env[name] = v.Reveal()
	}
	return env, nil
}

// symbolIndexEnv resolves the cache destination for a symbol-index run, having created
// the containing dir. It requires the active cache (the op runs inside a cached
// target), so the index lands where ingestion later looks; run outside that path it
// errors rather than emit an index the graph will never find.
//
// The cache check STAYS after the move to a declared capability. The indexer is still
// registered as an ordinary target, which is what gives it cache keying and the
// freshness probe SymbolIndexStatus reads, so `magus run scip <project>` remains
// reachable and a magusfile can still compose the op into a body that runs outside a
// cached target. Only the authoring hack went away, not the path this defends.
//
// op is the indexer's op name, which keys the file: two indexing spells on one project
// write two indexes. The project's absolute directory comes back with the path.
func symbolIndexEnv(ctx context.Context, projectDir, op string) (string, string, error) {
	c := cache.FromContext(ctx)
	if c == nil {
		return "", "", fmt.Errorf("a symbol indexer must run as a magus target so its index lands in the cache")
	}
	abs, project, err := projectUnderWorkspace(ctx, projectDir)
	if err != nil {
		return "", "", err
	}
	path := symbols.IndexPath(c.Dir(), project, op)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", fmt.Errorf("prepare symbol index dir: %w", err)
	}
	return abs, path, nil
}

// projectUnderWorkspace resolves projectDir to its absolute path and its path
// under the workspace root, which keys what an op writes into the cache.
func projectUnderWorkspace(ctx context.Context, projectDir string) (abs, project string, err error) {
	abs, err = absoluteDir(ctx, projectDir)
	if err != nil {
		return "", "", err
	}
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return "", "", errors.New("the op must run in a workspace so what it writes is keyed by project path")
	}
	project, err = filepath.Rel(canonicalDir(ws.Root()), canonicalDir(abs))
	if err != nil || !filepath.IsLocal(project) {
		return "", "", fmt.Errorf("project dir %s is outside the workspace %s", abs, ws.Root())
	}
	return abs, project, nil
}

func absoluteDir(ctx context.Context, dir string) (string, error) {
	if filepath.IsAbs(dir) {
		return dir, nil
	}
	cwd, err := std.EffectiveCurrentWorkingDirectory(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve the project dir: %w", err)
	}
	return filepath.Join(cwd, dir), nil
}

// canonicalDir resolves symlinks, so a root and a project dir reached through
// different spellings (macOS /var and /private/var) still relate.
func canonicalDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return filepath.Clean(dir)
}

// newSpellInvoker returns an invoker closure for a built-in spell. Built-in ops
// are command-only (cmd/args/charms data, no script body). The whole descriptor is
// carried rather than a projection of it: dispatchOp reads the ops, the tools, the
// declared ignore dirs (so a Command.Sources op inherits them, see
// commandOpts.ignoreDirs) and the declared symbol indexer.
func newSpellInvoker(spec spells.Descriptor) func(context.Context, spells.InvokeRequest) (any, error) {
	return func(ctx context.Context, req spells.InvokeRequest) (any, error) {
		return dispatchOp(ctx, spec, req)
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
		cwd, err := std.EffectiveCurrentWorkingDirectory(ctx)
		if err != nil {
			slog.With(attr.Component("spell")).ErrorContext(ctx, "could not resolve a relative local spell path", slog.String("path", path), attr.Error(err))
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

// loadLocalBuzzSpell compiles a workspace-local Buzz spell at path and registers it,
// returning its spec and ok=false on any failure. It resolves through the same
// contract a built-in does, so the two are read and validated identically. Errors are
// logged, not raised, since discovery paths cannot route an error back to the caller.
func loadLocalBuzzSpell(ctx context.Context, path string) (spells.Descriptor, bool) {
	m, _, err := loadBuzzSpell(ctx, path)
	if err != nil {
		// A plain Buzz library imported by name (not a spell) is expected here:
		// resolution falls through to a normal module import. Only a genuinely
		// malformed spell is worth logging.
		if !errors.Is(err, spell.ErrNotASpell) {
			slog.With(attr.Component("spell")).ErrorContext(ctx, "could not load a local Buzz spell", slog.String("path", path), attr.Error(err))
		}
		return spells.Descriptor{}, false
	}
	return m, true
}

// localSpellBaseOptions builds the SpellOptions common to every workspace-local
// spell registration (cache metadata, command renderer, charm/doc discovery, language,
// syntax, symbol indexer, sandbox), minus the invoker, which each registration path supplies itself.
func localSpellBaseOptions(m spells.Descriptor) []spells.Option {
	opts := []spells.Option{
		spells.WithSources(m.Needs...),
		spells.WithIgnoreDirs(m.IgnoreDirs...),
		spells.WithManifests(m.Manifests...),
		spells.WithScriptRunners(m.ScriptRunners...),
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
	if len(m.LanguageExtensions) > 0 {
		opts = append(opts, spells.WithLanguageExtensions(m.LanguageExtensions))
	}
	if m.Syntax != nil {
		opts = append(opts, spells.WithSyntax(m.Syntax))
	}
	if m.SymbolIndexer != nil {
		opts = append(opts, spells.WithSymbolIndexer(m.SymbolIndexer))
	}
	if m.Sandbox != nil {
		opts = append(opts, spells.WithSandbox(m.Sandbox))
	}
	opts = append(opts, spells.WithOps(m.Ops), spells.WithTools(m.Tools), spells.WithVersionProber(versionProber))
	return opts
}

// commonSpellAliases maps a name a user might reach for to the spell that actually
// serves it. Every entry is now a genuine SYNONYM, not an abbreviation: spells are
// named for the thing they adapt (typescript, rust, python, markdown, go), so the
// language name is the real name and needs no translation.
//
// The abbreviations this used to carry (typescript->ts, rust->rs, python->py,
// markdown->md, golang->go) are gone with the rename that made them unnecessary.
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
		// The magusfile driver IS registered (dispatch needs it), so the generic
		// check below would happily accept this import even though the handle it
		// binds is now unusable. Rejected explicitly, with the migration.
		if h == magusfileSpellName {
			return magusfileNotASpellError("importing it")
		}
		if isRegisteredSpell(h) {
			continue
		}
		return unknownSpellError{name: h}
	}
	return nil
}

// unknownSpellError refuses an import naming a spell this build does not ship. The guard
// reads the name through UnknownSpell, with errors.As, to tell a ./magus built on the other
// side of a spell's addition or removal from a misspelled import.
type unknownSpellError struct{ name string }

func (e unknownSpellError) Error() string { return unknownSpellMessage(e.name) }

// UnknownSpell is the handle the import named.
func (e unknownSpellError) UnknownSpell() string { return e.name }

// isRegisteredSpell reports whether name is a handle reachable as
// `import "magus/spell/<name>"`: a compiled built-in or a host-registered spell.
// This mirrors the native modules registerAllBuzz installs, so the check can
// never reject a handle the import would actually resolve.
func isRegisteredSpell(name string) bool {
	if _, ok := spell.Builtins()[name]; ok {
		return true
	}
	_, ok := project.DefaultSpellRegistry().Lookup(name)
	return ok
}

func unknownSpellMessage(name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "no spell %q to import as \"magus/spell/%s\"", name, name)
	if s := suggestSpellName(name); s != "" {
		fmt.Fprintf(&b, ", did you mean %q (import \"magus/spell/%s\")", s, s)
	}
	fmt.Fprintf(&b, " (built-in spells: %s)", strings.Join(builtinSpellHandles(), ", "))
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
	b := spell.Builtins()
	out := make([]string, 0, len(b))
	for name := range b {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
