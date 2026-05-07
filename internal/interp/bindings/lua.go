package bindings

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/engine"
	"github.com/egladman/magus/internal/interp/engine/lua"
	"github.com/egladman/magus/internal/interp/pool"
	"github.com/egladman/magus/internal/proc"
	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/internal/std/gen/lua"
	"github.com/egladman/magus/internal/wire"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// magusPry implements magus.pry(): suspend execution and open a REPL on the
// active VM with introspection of the surrounding frame, locals, and source.
//
// When the engine implements engine.Stepper, .step / .next / .finish install a
// one-shot hook and let execution resume; the hook re-enters Pry on the next
// matching event so debugging continues line-by-line.
func magusPry(ctx context.Context, r lua.Session) int {
	cwd, _ := os.Getwd()

	pctx := pryContextFromSession(r)
	opts := interp.ReplOptions{
		WorkDir: cwd,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}

	resume, err := interp.Pry(ctx, r, pctx, opts)
	if err != nil {
		r.RaiseError("magus.pry: %s", err)
		return 0
	}
	if resume == interp.ResumeContinue {
		return 0
	}

	stepper, ok := r.(engine.Stepper)
	if !ok {
		fmt.Fprintln(os.Stdout, "(stepping not supported on this engine — resuming)")
		return 0
	}
	// Install a step hook. We record call depth now so .next (step-over) can
	// compare against it later to ignore lines in deeper frames.
	startDepth := 0
	if dbg, ok := r.(engine.DebugReader); ok {
		startDepth = dbg.CallDepth()
	}
	stepMode := resume
	var hook func(engine.StepEvent, engine.Frame)
	hook = func(ev engine.StepEvent, frame engine.Frame) {
		curDepth := 0
		if dbg, ok := r.(engine.DebugReader); ok {
			curDepth = dbg.CallDepth()
		}
		switch stepMode {
		case interp.ResumeStep:
			if ev != engine.StepLine {
				return
			}
		case interp.ResumeNext:
			if ev != engine.StepLine || curDepth > startDepth {
				return
			}
		case interp.ResumeFinish:
			if ev != engine.StepReturn || curDepth != startDepth {
				return
			}
		default:
			return
		}
		// Re-enter pry. Clear the hook first so the inner REPL doesn't
		// re-trigger on its own evals.
		stepper.ClearStepHook()
		pctx2 := pryContextFromSession(r)
		pctx2.File = strings.TrimPrefix(frame.Source, "@")
		pctx2.Line = frame.CurrentLine
		pctx2.Func = frame.Name
		newResume, err := interp.Pry(ctx, r, pctx2, opts)
		if err != nil {
			r.RaiseError("magus.pry: %s", err)
			return
		}
		if newResume == interp.ResumeContinue {
			return
		}
		// Re-arm for the next step.
		stepMode = newResume
		if dbg, ok := r.(engine.DebugReader); ok {
			startDepth = dbg.CallDepth()
		}
		stepper.SetStepHook(stepMask(newResume), hook)
	}
	stepper.SetStepHook(stepMask(stepMode), hook)
	return 0
}

// stepMask selects the hook mask that matches a resume mode.
func stepMask(resume interp.PryResume) engine.StepMask {
	switch resume {
	case interp.ResumeStep, interp.ResumeNext:
		return engine.MaskLine
	case interp.ResumeFinish:
		return engine.MaskReturn
	default:
		return 0
	}
}

// pryContextFromSession builds the PryContext that magus.pry() passes to the
// REPL: location of the call site (from frame 0) plus the full Lua stack.
func pryContextFromSession(r lua.Session) interp.PryContext {
	pctx := interp.PryContext{}
	dbg, ok := r.(engine.DebugReader)
	if !ok {
		return pctx
	}
	frames := dbg.Frames()
	pctx.Frames = frames
	if len(frames) > 0 {
		f := frames[0]
		pctx.File = strings.TrimPrefix(f.Source, "@")
		pctx.Line = f.CurrentLine
		pctx.Func = f.Name
	}
	return pctx
}

// magusPryNoop is the parse-mode stub: magus.pry() does nothing during
// `magus list` / `magus describe` so those subcommands don't enter the REPL.
func magusPryNoop(_ context.Context, _ lua.Session) int { return 0 }

func registerMagus(r lua.Session, parseMode bool) {
	m := luagen.RegisterMagus(r) // magus table with host-generated methods (cmd, bust_cache)
	r.SetGlobal("magus", m)

	// magus.project is a namespace (a plain table, like magus.spell): register
	// declares a project — its spell, regenerable outputs, and cross-project
	// dependency edges. Namespaced so the call site reads magus.project.register
	// and says WHAT is being registered.
	projectNS := r.NewTable()
	projectNS.RawSetString("register", r.NewFunction(magusRegister))
	m.RawSetString("project", projectNS)

	// magus.target is a namespace (a plain table, like magus.spell): expand_globs
	// resolves target-name globs to names. Targets are declared as exported global
	// functions; magus.target.new is no longer part of the magusfile surface.
	targetNS := r.NewTable()
	targetNS.RawSetString("expand_globs", r.NewFunction(magusTargetExpandGlobs))
	m.RawSetString("target", targetNS)

	// magus.cache is a namespace exposing remote(), which wires a function-op spell
	// handle as the cross-shard remote cache backend (parity with the Buzz
	// magus.cache.remote). It records the spell's name on the per-Open workspace
	// registry; magus.Open resolves it by name after the magusfile is evaluated. The
	// spell must expose get_artifact/put_artifact function-ops (and optionally enabled()).
	cacheNS := r.NewTable()
	cacheNS.RawSetString("remote", r.NewFunction(magusCacheRemote))
	m.RawSetString("cache", cacheNS)
	if parseMode {
		m.RawSetString("pry", r.NewFunction(magusPryNoop))
	} else {
		m.RawSetString("pry", r.NewFunction(magusPry))
	}

	m.RawSetString("dispatch", r.NewFunction(magusDispatch))
	m.RawSetString("depends_on", r.NewFunction(magusDependsOn))
	// magus.has_charm(name): the Lua twin of the Buzz binding — true when execution
	// charm `name` is active. Lets a function target branch on any charm carried in
	// context, e.g. build:container or the built-in rw (has_charm("rw")).
	m.RawSetString("has_charm", r.NewFunction(magusHasCharm))

	// Logging on the magus namespace itself (magus.info/debug/warn/error). This is
	// the one way to log from a magusfile — there is no separate std log module.
	// Each level writes into the process slog logger via emitMagusLog.
	m.RawSetString("info", r.NewFunction(magusLogFn(slog.LevelInfo)))
	m.RawSetString("debug", r.NewFunction(magusLogFn(slog.LevelDebug)))
	m.RawSetString("warn", r.NewFunction(magusLogFn(slog.LevelWarn)))
	m.RawSetString("error", r.NewFunction(magusLogFn(slog.LevelError)))

	// magus.hint(msg): an advisory nudge — print an actionable note (e.g. how to
	// read a confusing upstream error) without affecting the exit code. Routes
	// through the shared hint channel, so MAGUS_HINTS_ENABLED=false silences it.
	m.RawSetString("hint", r.NewFunction(magusHint))
	// magus.fatal(msg): log msg at error level, then abort the run (exit 1). The
	// one blessed "stop with a message" — terser than magus.error + os.exit.
	m.RawSetString("fatal", r.NewFunction(magusFatal))
}

// magusCacheRemote implements magus.cache.remote(spellHandle): record the spell's
// name as the remote cache backend on the per-Open workspace registry. The handle
// (from require/magus.spell.load/define) must carry a name. The Lua twin of the
// Buzz buildCacheNS remote() binding.
func magusCacheRemote(ctx context.Context, r lua.Session) int {
	h, ok := r.Get(1).AsTable()
	if !ok {
		r.RaiseError("magus.cache.remote: expected a spell handle")
		return 0
	}
	name, ok := h.RawGetString("name").AsString()
	if !ok || name == "" {
		r.RaiseError("magus.cache.remote: argument is not a spell handle (no name)")
		return 0
	}
	if reg := wire.WorkspaceRegistryFromContext(ctx); reg != nil {
		reg.SetRemoteBackend(name)
	}
	return 0
}

// emitMagusHint prints msg through the shared hint channel, honoring the global
// hints toggle. Shared by both engines; advisory only, never fatal. No dedup —
// it would mean process-global state that leaks across runs in the daemon.
func emitMagusHint(msg string) {
	if !interactive.Enabled() {
		return
	}
	interactive.Emit(os.Stderr, msg)
}

// magusHint is the Lua trampoline for magus.hint(msg).
func magusHint(_ context.Context, r lua.Session) int {
	emitMagusHint(r.CheckString(1))
	return 0
}

// magusHasCharm implements magus.has_charm(name): push whether execution charm
// `name` is active on the run context. The Lua twin of the Buzz binding.
func magusHasCharm(ctx context.Context, r lua.Session) int {
	r.Push(engine.BoolValue(types.HasCharm(ctx, r.CheckString(1))))
	return 1
}

// magusFatal is the Lua trampoline for magus.fatal(msg): log at error level,
// then abort with exit 1. RecordExit carries the code out-of-band (the raised
// Lua error stringifies the type away) so the interpreter reports a typed
// ExitError regardless of engine.
func magusFatal(ctx context.Context, r lua.Session) int {
	msg := r.CheckString(1)
	emitMagusLog(ctx, slog.LevelError, msg, nil)
	types.RecordExit(ctx, 1)
	r.RaiseError("%s", types.ExitError{Code: 1}.Error())
	return 0
}

// emitMagusLog writes msg at level into the process logger with optional fields.
// Shared by the Lua and Buzz magus.<level> trampolines.
func emitMagusLog(ctx context.Context, level slog.Level, msg string, fields map[string]string) {
	if len(fields) == 0 {
		slog.Default().Log(ctx, level, msg)
		return
	}
	attrs := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		attrs = append(attrs, k, v)
	}
	slog.Default().Log(ctx, level, msg, attrs...)
}

// magusLogFn builds the Lua trampoline for magus.<level>(msg, fields?).
func magusLogFn(level slog.Level) func(context.Context, lua.Session) int {
	return func(ctx context.Context, r lua.Session) int {
		msg := r.CheckString(1)
		fields := map[string]string{}
		if r.GetTop() >= 2 && !r.Get(2).IsNil() {
			if tbl, ok := r.Get(2).AsTable(); ok {
				tbl.ForEach(func(k, v engine.Value) {
					fields[k.String()] = v.String()
				})
			}
		}
		emitMagusLog(ctx, level, msg, fields)
		return 0
	}
}

// magusRegister implements magus.project.register(path, opts).
func magusRegister(ctx context.Context, r lua.Session) int {
	path := r.CheckString(1)

	var opts []wire.ProjectOption
	var spellErr error
	if r.GetTop() >= 2 {
		tbl, ok := r.Get(2).AsTable()
		if !ok {
			r.ArgError(2, "opts must be a table")
			return 0
		}
		tbl.ForEach(func(k, v engine.Value) {
			if spellErr != nil {
				return
			}
			switch k.String() {
			case "depends_on":
				paths := tableToStringSlice(v)
				if len(paths) > 0 {
					opts = append(opts, wire.WithDependsOn(paths...))
				}
			case "outputs":
				paths := tableToStringSlice(v)
				if len(paths) > 0 {
					opts = append(opts, wire.WithOutputs(paths...))
				}
			case "exclusive":
				if v.AsBool() {
					opts = append(opts, wire.WithExclusive())
				}
			case "spells":
				// spells is an array of MagusSpell handles (from
				// require("magus.spell.<name>"), .get, .load, or .define). A local
				// spell (.load/.define) is registered by value here, at bind time,
				// from the resolved spec its handle carries; built-ins and host
				// spells are already registered, so they only need binding by name.
				if arr, ok := v.AsTable(); ok {
					for i := 1; i <= arr.Len(); i++ {
						st, ok := arr.RawGetInt(i).AsTable()
						if !ok {
							continue
						}
						name := st.RawGetString("name").String()
						if name == "" {
							continue
						}
						if _, exists := project.DefaultSpellRegistry().Lookup(name); !exists {
							m, err := ispell.Decode(luaSpellObj{t: st, rt: r})
							if err != nil {
								spellErr = fmt.Errorf("spell %q: %w", name, err)
								return
							}
							registerLocalSpell(m)
						}
						opts = append(opts, wire.WithRegisteredSpell(name))
					}
				}
			case "watch_ignore":
				if wt, ok := v.AsTable(); ok {
					var patterns []types.IgnorePattern
					if gv := wt.RawGetString("glob"); !gv.IsNil() {
						if gt, ok := gv.AsTable(); ok {
							gt.ForEach(func(_, ev engine.Value) {
								if s, ok := ev.AsString(); ok {
									patterns = append(patterns, wire.IgnoreGlob(s))
								}
							})
						} else if s, ok := gv.AsString(); ok {
							patterns = append(patterns, wire.IgnoreGlob(s))
						}
					}
					if rv := wt.RawGetString("regex"); !rv.IsNil() {
						if rt, ok := rv.AsTable(); ok {
							rt.ForEach(func(_, ev engine.Value) {
								if s, ok := ev.AsString(); ok {
									patterns = append(patterns, wire.IgnoreRegex(s))
								}
							})
						} else if s, ok := rv.AsString(); ok {
							patterns = append(patterns, wire.IgnoreRegex(s))
						}
					}
					if lv := wt.RawGetString("literal"); !lv.IsNil() {
						if lt, ok := lv.AsTable(); ok {
							lt.ForEach(func(_, ev engine.Value) {
								if s, ok := ev.AsString(); ok {
									patterns = append(patterns, wire.IgnoreLiteral(s))
								}
							})
						} else if s, ok := lv.AsString(); ok {
							patterns = append(patterns, wire.IgnoreLiteral(s))
						}
					}
					if len(patterns) > 0 {
						opts = append(opts, wire.WithWatchIgnore(patterns...))
					}
				}
			case "targets":
				// per-target policy table: cachable=false opts the target out of
				// the cache; isolated=true serializes it against the whole batch.
				if tt, ok := v.AsTable(); ok {
					tt.ForEach(func(tname, policy engine.Value) {
						pt, ok := policy.AsTable()
						if !ok {
							return
						}
						if cv := pt.RawGetString("cachable"); !cv.IsNil() && !cv.AsBool() {
							opts = append(opts, wire.WithTarget(tname.String(), wire.NoCache()))
						}
						if sv := pt.RawGetString("isolated"); !sv.IsNil() && sv.AsBool() {
							opts = append(opts, wire.WithTarget(tname.String(), wire.Isolated()))
						}
					})
				}
			}
		})
	}

	if spellErr != nil {
		r.RaiseError("magus.project.register: %v", spellErr)
		return 0
	}

	if reg := wire.WorkspaceRegistryFromContext(ctx); reg != nil {
		reg.RegisterProject(path, opts...)
	}
	return 0
}

// depGraphCycle reports whether any target reachable from names sits on a
// depends_on cycle, returning the name of a target on that cycle. It walks the
// _magus_target_deps graph with a three-colour DFS (white/grey/black), so the
// cycle is found in Go before any target wrapper runs — letting the caller
// return a plain error instead of raising from deep in a nested call.
func depGraphCycle(r lua.Session, names []string) (string, bool) {
	depsTbl, ok := r.GetGlobal("_magus_target_deps").AsTable()
	if !ok {
		return "", false
	}
	const (
		grey  = 1
		black = 2
	)
	state := map[string]int{}
	var found string
	var visit func(n string) bool
	visit = func(n string) bool {
		switch state[n] {
		case grey:
			found = n
			return true
		case black:
			return false
		}
		state[n] = grey
		if dv := depsTbl.RawGetString(n); !dv.IsNil() {
			if dt, ok := dv.AsTable(); ok {
				for i := 1; i <= dt.Len(); i++ {
					if d, ok := dt.RawGetInt(i).AsString(); ok && d != "" && visit(d) {
						return true
					}
				}
			}
		}
		state[n] = black
		return false
	}
	for _, n := range names {
		if visit(n) {
			return found, true
		}
	}
	return "", false
}

// magusDispatch implements magus.dispatch(patterns [, opts]).
//
// patterns is a string or list of strings. Each pattern:
//   - no "*" → expands to "*-<pattern>" (suffix shorthand)
//   - contains "*" → translated to a regexp ("*" → ".*", anchored)
//
// opts is an optional table: {ignore_missing: boolean, fail_fast: boolean}.
//
// Matched targets are dispatched in parallel via the in-process VM pool when
// available; results are collected and joined. The parent's global limiter
// slot is yielded for the duration so children can acquire slots without
// deadlocking at MAGUS_CONCURRENCY=1.
func magusDispatch(ctx context.Context, r lua.Session) int {
	// --- parse args ---
	patternsVal := r.CheckAny(1)
	var patterns []string
	if s, ok := patternsVal.AsString(); ok {
		patterns = []string{s}
	} else if _, ok := patternsVal.AsTable(); ok {
		patterns = tableToStringSlice(patternsVal)
	} else {
		r.ArgError(1, "patterns must be a string or table of strings")
		return 0
	}

	var ignoreMissing bool
	if r.GetTop() >= 2 {
		if tbl, ok := r.Get(2).AsTable(); ok {
			ignoreMissing = tbl.RawGetString("ignore_missing").AsBool()
		}
	}

	matched, err := matchTargetNames(r, patterns)
	if err != nil {
		r.RaiseError("magus.dispatch: %v", err)
		return 0
	}
	if len(matched) == 0 {
		if ignoreMissing {
			return 0
		}
		r.RaiseError("magus.dispatch: no targets match %v", patterns)
		return 0
	}
	if err := runTargetNames(ctx, r, matched); err != nil {
		r.RaiseError("%v", err)
	}
	return 0
}

// compileTargetPatterns turns target match patterns into anchored regexps,
// shared by the Lua and Buzz dispatch matchers. A pattern with no "*" is suffix
// shorthand ("build" → names ending in "-build"); a pattern with "*" is a glob
// ("*" → ".*"). Both forms are QuoteMeta'd first, so the result is always a valid
// regexp and MustCompile never panics.
func compileTargetPatterns(patterns []string) []*regexp.Regexp {
	res := make([]*regexp.Regexp, 0, len(patterns))
	for _, pat := range patterns {
		if !strings.Contains(pat, "*") {
			res = append(res, regexp.MustCompile(`^.*-`+regexp.QuoteMeta(pat)+`$`))
			continue
		}
		res = append(res, regexp.MustCompile("^"+strings.ReplaceAll(regexp.QuoteMeta(pat), `\*`, `.*`)+"$"))
	}
	return res
}

// matchTargetNames resolves patterns (compiled by compileTargetPatterns) against
// the registered target names in the current VM, returning the sorted, deduped
// matches.
func matchTargetNames(r lua.Session, patterns []string) ([]string, error) {
	res := compileTargetPatterns(patterns)

	registry, ok := r.GetGlobal("_magus_targets").AsTable()
	if !ok {
		return nil, fmt.Errorf("_magus_targets is not a table")
	}
	seen := make(map[string]struct{})
	var matched []string
	registry.ForEach(func(k, _ engine.Value) {
		name, ok := k.AsString()
		if !ok {
			return
		}
		for _, re := range res {
			if re.MatchString(name) {
				if _, dup := seen[name]; !dup {
					seen[name] = struct{}{}
					matched = append(matched, name)
				}
				break
			}
		}
	})
	slices.Sort(matched)
	return matched, nil
}

// runTargetNames runs the given already-resolved target names: via the
// in-process VM pool when one is present in ctx (parallel, recursion-checked),
// otherwise sequentially in the current VM. No glob expansion happens here.
func runTargetNames(ctx context.Context, r lua.Session, names []string) error {
	if len(names) == 0 {
		return nil
	}
	if reg := pool.RegistryFromContext(ctx); reg != nil {
		if src := interp.SourceFromContext(ctx); src != nil {
			return dispatchViaPool(ctx, reg, src, names, pool.AncestorsFromContext(ctx))
		}
	}
	registry, ok := r.GetGlobal("_magus_targets").AsTable()
	if !ok {
		return fmt.Errorf("_magus_targets is not a table")
	}
	// Reject a depends_on cycle over the whole transitive graph here, before
	// invoking any target, so it surfaces as a returned error at this frame
	// rather than as a RaiseError from a deeply nested target wrapper (which
	// gopher-lua does not unwind cleanly through nested protected calls).
	if cyc, ok := depGraphCycle(r, names); ok {
		return fmt.Errorf("target %q is part of a depends_on cycle", cyc)
	}
	emptyArgs := r.NewTable()
	for _, name := range names {
		fn := registry.RawGetString(name)
		if fn.IsNil() {
			// Fail fast on a typo'd or removed dependency rather than silently
			// skipping it — parity with the pool dispatch path (pool.go) and the
			// Buzz depends_on, both of which already error on an unknown target.
			// (magus.dispatch only ever passes registry-matched names here, so this
			// fires only for a literal name in magus.depends_on.)
			return fmt.Errorf("unknown target %q", name)
		}
		if err := r.Call(engine.CallParams{Fn: fn, NRet: 0, Protect: true}, emptyArgs); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// magusTargetExpandGlobs implements magus.target.expand_globs(patterns): it
// returns the names of registered targets matching the glob/suffix patterns,
// sorted and deduped. Feed the result into another target's depends_on.
func magusTargetExpandGlobs(_ context.Context, r lua.Session) int {
	patternsVal := r.CheckAny(1)
	var patterns []string
	if s, ok := patternsVal.AsString(); ok {
		patterns = []string{s}
	} else if _, ok := patternsVal.AsTable(); ok {
		patterns = tableToStringSlice(patternsVal)
	} else {
		r.ArgError(1, "patterns must be a string or table of strings")
		return 0
	}
	matched, err := matchTargetNames(r, patterns)
	if err != nil {
		r.RaiseError("magus.target.expand_globs: %v", err)
		return 0
	}
	out := r.NewTable()
	for i, name := range matched {
		out.RawSetInt(i+1, engine.StringValue(name))
	}
	r.Push(out)
	return 1
}

// magusDependsOn implements magus.depends_on(name, ...) and
// magus.depends_on({name, ...}): runs each named target as a dependency of the
// calling target, with cycle detection and deduplication. Accepts any mix of
// string and table-of-string arguments.
func magusDependsOn(ctx context.Context, r lua.Session) int {
	var names []string
	n := r.GetTop()
	for i := 1; i <= n; i++ {
		v := r.CheckAny(i)
		if s, ok := v.AsString(); ok {
			names = append(names, strings.ToLower(s))
		} else if _, ok := v.AsTable(); ok {
			for _, s := range tableToStringSlice(v) {
				names = append(names, strings.ToLower(s))
			}
		} else {
			r.ArgError(i, "expected string or table of strings")
			return 0
		}
	}
	if err := runTargetNames(ctx, r, dedupStrings(names)); err != nil {
		r.RaiseError("magus.depends_on: %v", err)
	}
	return 0
}

// dedupStrings returns names with duplicates removed, preserving first-occurrence
// order. magus.depends_on uses it so a target listed manually *and* matched by a
// magus.target.expand_globs glob in the same list runs once, not twice. Names are
// already lowercased by the callers, so the dedup is case-insensitive.
func dedupStrings(names []string) []string {
	if len(names) < 2 {
		return names
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// dispatchViaPool fans out matched target names to the Lua VM pool, yielding the
// RunAll limiter slot for the duration so pool workers can acquire it. The pool's
// Dispatch applies the TargetMemo in ctx, so each target runs at most once per
// invocation (diamond deduplication).
func dispatchViaPool(ctx context.Context, reg *pool.Registry, src *interp.Source, names []string, ancestors []string) error {
	p := reg.Get(src)
	lim := cache.LimiterFromContext(ctx)
	return proc.RunChildSync(ctx, lim, func() error {
		return p.Dispatch(cache.WithoutSlotHeld(ctx), names, ancestors)
	})
}

func tableToStringSlice(v engine.Value) []string {
	tbl, ok := v.AsTable()
	if !ok {
		return nil
	}
	var out []string
	tbl.ForEach(func(_, elem engine.Value) {
		if str, ok := elem.AsString(); ok {
			out = append(out, str)
		}
	})
	return out
}
