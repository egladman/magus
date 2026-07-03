package bindings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/interp"
	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/project"
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
	for _, spec := range ispell.Builtins() {
		opts := []types.SpellOption{
			types.WithSources(spec.Needs...),
			types.WithClaims(spec.Claims...),
			types.WithSpellOutputs(spec.Provides...),
			types.WithTargets(spec.OpNames()...),
			types.WithServiceTargets(spec.ServiceOpNames()...),
			types.WithInvoker(newSpellInvoker(spec.Ops)),
			types.WithCommandRenderer(newCommandRenderer(spec.Ops)),
			types.WithTargetSources(spec.TargetNeeds),
			types.WithTargetCharms(charmNamesByTarget(spec.Ops)),
			types.WithTargetDocs(docsByTarget(spec.Ops)),
		}
		if spec.Opaque {
			opts = append(opts, types.WithOpaque())
		}
		if len(spec.VersionCmd) > 0 {
			opts = append(opts, types.WithVersionProbe(newVersionProbe(spec.VersionCmd)))
		}
		project.DefaultSpellRegistry().RegisterSpell(types.NewSpell(spec.Name, opts...))
	}
})

// charmNamesByTarget extracts the sorted charm names each target declares, for
// discovery surfaces like `magus describe`.
func charmNamesByTarget(targets map[string]types.SpellOp) map[string][]string {
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
func docsByTarget(targets map[string]types.SpellOp) map[string]string {
	out := make(map[string]string, len(targets))
	for name, t := range targets {
		if t.Doc != "" {
			out[name] = t.Doc
		}
	}
	return out
}

// newVersionProbe runs argv in the project dir and returns trimmed stdout.
func newVersionProbe(argv []string) func(context.Context, string) (string, error) {
	return func(ctx context.Context, dir string) (string, error) {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("version probe %v in %s: %w", argv, dir, err)
		}
		return strings.TrimSpace(string(out)), nil
	}
}

// newCommandRenderer returns the command preview used by `magus describe`: it
// reports cmd plus the argv as reshaped by the active charms, executing nothing. A
// no-op marker op (empty Cmd) returns ok=false — there is no command to show.
func newCommandRenderer(targets map[string]types.SpellOp) func(string, []string) (string, []string, bool) {
	return func(target string, charms []string) (string, []string, bool) {
		op, ok := targets[target]
		if !ok || op.Bin == "" {
			return "", nil, false
		}
		args, err := resolveCharmArgs(types.WithCharms(context.Background(), charms), op.Args, op.Charms)
		if err != nil {
			return "", nil, false
		}
		return op.Bin, args, true
	}
}

// noResult is the invoker's no-op outcome — no Data, no error — for a request that
// fans out to a spell with nothing to contribute: an unknown target, or a handler
// op on a built-in spell (no script body to run). nil Data is an ordinary invoker
// result (a command op returns it on success too; see types.Spell.Invoke), so this
// is a real result rather than a sentinel — the helper names the intent and keeps
// the nilnil suppression in one documented place.
func noResult() (any, error) {
	return nil, nil //nolint:nilnil // documented invoker no-op: nil Data, nil error
}

// dispatchOp is the single op-dispatch bridge between the magus host and the Buzz
// interpreter. It resolves the request's target to a [types.SpellOp] and runs its
// declared command as a subprocess. An unknown target is a no-op, matching the
// fan-out-and-skip dispatch model. Every op is a command; in-VM work (a cache
// backend) is dispatched separately (see newBuzzSpellInvoker), not through here.
func dispatchOp(ctx context.Context, ops map[string]types.SpellOp, req types.InvokeRequest) (any, error) {
	op, ok := ops[req.Target]
	if !ok {
		slog.DebugContext(ctx, "spell: target not provided by this spell (fan-out skip)", "target", req.Target, "dir", req.Dir)
		return noResult()
	}
	slog.DebugContext(ctx, "spell: dispatch command", "target", req.Target, "cmd", op.Bin, "dir", req.Dir)
	_, err := runCommand(ctx, op, commandOpts{cwd: req.Dir, args: project.ExtraArgs(ctx)})
	return nil, err
}

// newSpellInvoker returns an invoker closure for a built-in spell. Built-in ops
// are command-only (cmd/args/charms data, no script body).
func newSpellInvoker(targets map[string]types.SpellOp) func(context.Context, types.InvokeRequest) (any, error) {
	return func(ctx context.Context, req types.InvokeRequest) (any, error) {
		return dispatchOp(ctx, targets, req)
	}
}

// commandTargetNames returns the spell's command target names, sorted. Every op is
// a command, so this is all of them.
func commandTargetNames(targets map[string]types.SpellOp) []string {
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
func loadLocalSpell(ctx context.Context, path string) (ispell.Descriptor, bool) {
	if !filepath.IsAbs(path) {
		cwd, err := std.EffectiveCwd(ctx)
		if err != nil {
			slog.Error("load local spell: getwd", "err", err)
			return ispell.Descriptor{}, false
		}
		path = filepath.Join(cwd, path)
	}
	return loadLocalBuzzSpell(ctx, path)
}

// loadSpellFile loads a spell file as a function-op-capable SpellDriver and
// registers it — the in-package entry point the remote cache backend uses to
// resolve a backend selected by a file path. A .buzz spell loads through the Buzz
// path (registering a function-op spell eagerly, capturing its source for in-VM
// dispatch).
func loadSpellFile(ctx context.Context, path string) (types.SpellDriver, error) {
	if !strings.HasSuffix(path, ".buzz") {
		return nil, fmt.Errorf("spell file %q: must be a .buzz spell", path)
	}
	_, sp, err := loadBuzzSpell(ctx, path)
	if err != nil {
		return nil, err // explicit nil: don't return a typed-nil *types.Spell as a non-nil interface
	}
	return sp, nil
}

// loadLocalBuzzSpell compiles a workspace-local Buzz spell at path, returning its
// spec and ok=false on any failure. Extract routes through the same
// ispell.Decode a built-in uses, so a .buzz workspace spell and a built-in are
// read and validated identically. Errors are logged, not raised, since discovery
// paths cannot route an error back to the caller. Registration is deferred to
// magus.project; the handle the caller builds carries the resolved spec
// so it registers by value there.
func loadLocalBuzzSpell(ctx context.Context, path string) (ispell.Descriptor, bool) {
	// loadBuzzSpell registers the spell with the function-op invoker (capturing its
	// source), so an imported Buzz spell carries function-ops whether it is later
	// bound to a project or wired as the remote cache backend. project bind finds
	// it already registered and binds it by name.
	m, _, err := loadBuzzSpell(ctx, path)
	if err != nil {
		slog.Error("load local spell: buzz", "path", path, "err", err)
		return ispell.Descriptor{}, false
	}
	return m, true
}

// localSpellBaseOptions builds the SpellOptions common to every workspace-local
// spell registration (cache metadata, command renderer, charm/doc discovery),
// minus the invoker — each registration path supplies its own.
func localSpellBaseOptions(m ispell.Descriptor) []types.SpellOption {
	opts := []types.SpellOption{
		types.WithSources(m.Needs...),
		types.WithClaims(m.Claims...),
		types.WithSpellOutputs(m.Provides...),
		types.WithTargets(m.OpNames()...),
		types.WithServiceTargets(m.ServiceOpNames()...),
		types.WithCommandRenderer(newCommandRenderer(m.Ops)),
		types.WithTargetCharms(charmNamesByTarget(m.Ops)),
		types.WithTargetDocs(docsByTarget(m.Ops)),
		types.WithDocRequiredTargets(m.DocOps...),
	}
	if m.Opaque {
		opts = append(opts, types.WithOpaque())
	}
	if len(m.VersionCmd) > 0 {
		opts = append(opts, types.WithVersionProbe(newVersionProbe(m.VersionCmd)))
	}
	return opts
}

// registerLocalSpell registers a decoded fork-only workspace-local spell into the
// default registry. The shared ispell.Decode produces m for the imported Buzz
// spell by-value path, so this is the single deferred registration point (called at
// magus.project bind time). A function-op spell instead registers eagerly
// at load via loadBuzzSpell.
func registerLocalSpell(m ispell.Descriptor) {
	opts := append(localSpellBaseOptions(m), types.WithInvoker(newSpellInvoker(m.Ops)))
	project.DefaultSpellRegistry().RegisterIfAbsent(types.NewSpell(m.Name, opts...))
}

// commonSpellAliases maps the language or tool name a user is likely to type to
// the abbreviated handle the spell actually registers under. Built-in handles are
// deliberately short (TypeScript is ts, Python py, Rust rs, Markdown md), so
// `import "magus/spell/typescript"` is a natural first guess; this turns that slip
// into a precise suggestion.
var commonSpellAliases = map[string]string{
	"typescript": "ts",
	"javascript": "ts",
	"js":         "ts",
	"node":       "ts",
	"nodejs":     "ts",
	"python":     "py",
	"python3":    "py",
	"rust":       "rs",
	"cargo":      "rs",
	"markdown":   "md",
	"golang":     "go",
}

// checkSpellImports validates the handles a magusfile imports via
// `import "magus/spell/<handle>"`. An unknown handle resolves to nothing and would
// otherwise surface later as a disconnected "undefined" error, so we fail fast
// here with a did-you-mean suggestion. Registered from init() via
// interp.RegisterBuzzSpellImportCheck.
func checkSpellImports(handles []string) error {
	for _, h := range handles {
		if isRegisteredSpell(h) {
			continue
		}
		return errors.New(unknownSpellMessage(h))
	}
	return nil
}

// isRegisteredSpell reports whether name is a handle reachable as
// `import "magus/spell/<name>"` — a compiled built-in or a host-registered spell.
// This mirrors the synthetic modules registerAllBuzz installs, so the check can
// never reject a handle the import would actually resolve.
func isRegisteredSpell(name string) bool {
	if _, ok := ispell.Builtins()[name]; ok {
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
	fmt.Fprintf(&b, "\nbuilt-in handles are abbreviated: %s", strings.Join(builtinSpellHandles(), ", "))
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
		if d := levenshtein(lower, h); d < bestDist || (d == bestDist && h < best) {
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
	b := ispell.Builtins()
	out := make([]string, 0, len(b))
	for name := range b {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// levenshtein is the edit distance between a and b, for the did-you-mean search.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	row := make([]int, len(b)+1)
	for j := range row {
		row[j] = j
	}
	for i, ca := range a {
		prev := i + 1
		for j, cb := range b {
			cost := 1
			if ca == cb {
				cost = 0
			}
			next := row[j+1] + 1
			if d := prev + 1; d < next {
				next = d
			}
			if d := row[j] + cost; d < next {
				next = d
			}
			row[j] = prev
			prev = next
		}
		row[len(b)] = prev
	}
	return row[len(b)]
}
