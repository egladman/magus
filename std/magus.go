//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module magus -lang buzz -out ../internal/interp/bindings/gen/magus.go

func init() { Register(Magus) }

// Magus declares the host-declarable subset of the magus module. The remaining
// methods (target, dispatch, deps, pry, register) and the PROVIDER NAMESPACES
// (magus\cache, magus\ci, magus\secret) are VM-infrastructure:
// they manipulate the per-VM target registry and store/invoke VM-side
// function values, so they cannot share a Go Impl across backends and remain
// as hand-written trampolines in bindings/magus.go.
var Magus = Module{
	Name: "magus",
	// The first paragraph stays one short line with no ". " in it: cmd/magus-docs
	// derives the page's frontmatter description from the doc's first sentence, and a
	// second sentence up here would drag a paragraph break into the YAML.
	Doc: "Magus core primitives.\n\n" +
		"Three provider namespaces are wired by the runtime rather than declared here, so " +
		"they do not appear in the method list below: `magus\\cache.remote(<spell>)` selects " +
		"a remote cache backend, `magus\\ci.provider(<spell>)` a CI provider, and " +
		"`magus\\secret.provider(<spell>)` / `magus\\secret.read(<ref>)` a secret backend and " +
		"the credentials read through it. Each takes an imported spell handle. See " +
		"[Secrets](../../concepts/secrets.md), [Remote cache](../../concepts/cache/remote.md) " +
		"and [CI integration](../../guides/integrations/ci.md).\n\n" +
		"`import \"magus\"` resolves in a `magus buzz` script as well as in a magusfile. The " +
		"members that declare into the workspace magus is loading (`magus\\project`, the provider " +
		"selections above) and the ones served in-process from a loaded workspace (`ls`, `targets`, " +
		"`affected`, `graph`, `where`) raise [MGS1022](../codes/magusfile/MGS1022.md) in a script; " +
		"the nested-command methods (`cmd`, `run`, `describe`, `insight`, `doctor`) work there and " +
		"discover the workspace themselves.",
	Methods: []Method{
		{
			Name: "cmd",
			Doc:  "Escape hatch: run `magus <sub> <args>` for a subcommand with no dedicated method (status, affected, agent, graph, ...). Its signature is the typed methods' signature with the subcommand pushed in front: magus.cmd(sub, args, [opts]) beside magus.run(args, [opts]), same argv, same opts, same ExecResult. The SUBCOMMAND is a typed argument rather than args[0] because it is the part of the invocation magus can reason about - it stays readable in the signature and greppable in the source, while the remaining argv stays free-form. Prefer the dedicated methods (run, describe, insight, doctor) when one exists - magus.cmd warns when sub names one that has. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec); opts.quiet captures the output without echoing it to the console.",
			Args: []Arg{
				{Name: "sub", Type: TypeString},
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ExecResult"}},
			Raises:  true,
			Impl:    MagusCmd,
		},
		{
			Name:    "ls",
			Doc:     "List the workspace's projects: {workspace, count, projects}, each project {path, dir, spell, spells, sources, outputs, dependsOn, exclusive}. Annotate the result `> Projects` (magus's own type, no import needed) for compile-checked field access. Unlike magus.cmd(\"ls\"), this reads the workspace already open on the context - no subprocess, no second workspace load, no JSON round-trip.",
			Args:    nil,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Projects"}},
			Raises:  true,
			Impl:    MagusLs,
		},
		{
			Name:    "targets",
			Doc:     "The TARGET dependency graph of every project: {projects}, each project {path, name, engine, nodes, cycle, dependsOn} and each node {name, declared, doc, dependencies, charms, spells, crossDependencies, inputs, outputs}. Annotate the result `> TargetGraph` (magus's own type, no import needed) for compile-checked field access. This is the per-project view magus.graph() does not carry: graph() is the project-level DAG, this is the targets inside each one. Read statically from the magusfile source, so it never runs a target body, and served in-process from the workspace on the context - no subprocess, no markdown to re-parse.",
			Args:    nil,
			Returns: []Ret{{Type: TypeAnyMap, Object: "TargetGraph"}},
			Raises:  true,
			Impl:    MagusTargets,
		},
		{
			Name: "affected",
			Doc:  "Compute the VCS-affected project set against base (empty uses the configured base ref): {base, changed, seed, filesBySeed, affected}. Served in-process from the workspace on the context - no subprocess. Raises when the diff cannot be computed, rather than reporting an empty set, since an empty set and an uncomputable one mean opposite things to a caller deciding what to build.",
			Args: []Arg{
				{Name: "base", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Affected"}},
			Raises:  true,
			Impl:    MagusAffected,
		},
		{
			Name:    "project_graph",
			Doc:     "The project dependency DAG as {nodes, dependsOn, blastRadius}. nodes is in TOPOLOGICAL order, so iterating it is already a valid build order; dependsOn gives each node's direct predecessors and blastRadius how many projects it can transitively affect. Served in-process from the workspace on the context - no subprocess.",
			Args:    nil,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Graph"}},
			Raises:  true,
			Impl:    MagusGraph,
		},
		{
			Name: "where",
			Doc:  "Return the project path containing dir, or null when dir is inside no project. Served in-process from the workspace on the context - no subprocess.",
			Args: []Arg{
				{Name: "dir", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    MagusWhere,
		},
		{
			Name: "raise",
			Doc:  "Fail with a CODED diagnostic instead of a bare string, so a caller can branch on the code: `catch (e) { if (e.code == \"ACME1001\") ... }`. code is yours to define and namespace - anything but the MGS prefix, which is magus's own. opts.cause is the error being wrapped, usually the value from an inner catch; it is appended to the message the way Go's %w renders one, and the failure it came from stays reachable underneath. opts.url is the page documenting the code, rendered as the `see:` line the CLI prints under its own diagnostics.",
			Args: []Arg{
				{Name: "code", Type: TypeString},
				{Name: "message", Type: TypeString},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: nil,
			Raises:  true,
			Impl:    MagusRaise,
		},
		{
			Name: "run",
			Doc:  "Run `magus run <args>` recursively in the target's project directory and capture its output. Child invocations share the parent's concurrency budget over the local socket. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec); opts.quiet captures the output without echoing it to the console.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ExecResult"}},
			Raises:  true,
			Impl:    MagusRun,
		},
		{
			Name: "describe",
			Doc:  "Run `magus describe <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec); opts.quiet captures the output without echoing it to the console. Unlike a raw binary call, the working directory is always the contextual project dir, so a nested project describes itself, not the root workspace.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ExecResult"}},
			Raises:  true,
			Impl:    MagusDescribe,
		},
		{
			Name: "insight",
			Doc:  "Run `magus insight <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec); opts.quiet captures the output without echoing it to the console.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ExecResult"}},
			Raises:  true,
			Impl:    MagusInsight,
		},
		{
			Name: "insight_report",
			Doc:  "Every VCS-history lens plus the knowledge-graph axis, as one typed report: {hotspots, affinity, ownership, trend, volatility, graphStats}. Annotate the result `> InsightReport` for compile-checked field access - `r.ownership.projects` gives each project's primary author and bus-factor flag, `r.hotspots.files` the churn-by-complexity ranking, `r.volatility` the targets that flapped. This is the same data `magus insight report` renders as Markdown, handed over as values instead of a document to scrape. Runs a nested magus, so it works from a `magus buzz` script with no workspace on the context.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "InsightReport"}},
			Raises:  true,
			Impl:    MagusInsightReport,
		},
		{
			Name: "affected_impact",
			Doc:  "The VCS-affected set and WHY each project is in it: {base, changedFileCount, changedFiles, seedProjects, affectedProjects, notes}, each affected project carrying whether it was a seed and the files that pulled it in. Annotate the result `> Impact`. This is `magus affected --impact`, a forensic mode that reports the set without running a target - unlike `magus affected list`, which dispatches a target across every affected project to answer the same question. Runs a nested magus, so it works from a `magus buzz` script with no workspace on the context.",
			Args: []Arg{
				{Name: "base", Type: TypeString, Optional: true},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Impact"}},
			Raises:  true,
			Impl:    MagusAffectedImpact,
		},
		{
			Name: "target_graph",
			Doc:  "The TARGET dependency graph of every project as a typed value: {projects}, each with its nodes and each node's declared footprint (readsFiles / writesFiles / modifiesExistingFiles). Annotate the result `> TargetGraph`. This is what magus.targets() serves in-process, reached by a nested magus instead, so it works from a `magus buzz` script with no workspace on the context - the case that matters for CI advisories reasoning about a pull request.",
			Args: []Arg{
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "TargetGraph"}},
			Raises:  true,
			Impl:    MagusTargetGraph,
		},
		{
			Name: "describe_file",
			Doc:  "Classify paths against the workspace's declared globs: for each, the owning project and whether it is a declared `output` (generated - regenerate it, never hand-edit), a declared `source` (it feeds cache keys and the affected set), or `unclaimed`. Returns a typed DoctorReport-style envelope {definition, count, files}, not text to re-parse: this is the question \"can I disregard this changed file\", and a caller branches on `role` rather than grepping. Runs a nested magus, so it needs no workspace on the context and works from a `magus buzz` script.",
			Args: []Arg{
				{Name: "paths", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "FileReport"}},
			Raises:  true,
			Impl:    MagusDescribeFile,
		},
		{
			Name: "doctor",
			Doc:  "Validate the workspace and return what every check found: {workspace, checks, summary}, each check {name, status, message, details} with status `ok`, `fail`, or `advice` (advice is worth knowing and never a gate). Annotate the result `> DoctorReport` for compile-checked field access. A caller branches on a check's status rather than grepping console text for the word fail. It does NOT raise when a check fails: doctor exits non-zero precisely when it has something to report, and raising would discard the report. Gate on `summary.fail` instead, which says more than an exit code does. It DOES raise when the underlying `magus doctor` subprocess itself cannot be launched or its output cannot be decoded - an infrastructure failure, not a check result. opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec).",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "DoctorReport"}},
			Raises:  true,
			Impl:    MagusDoctor,
		},
		{
			Name: "diagnose_drift",
			Doc:  "Diagnose why a generate gate's declared outputs drifted and RETURN the verdict {drifted, code, message, url, files} so the caller decides whether to fail or warn. Pass the target's output globs and (optional) input globs, project-relative. code is MGS4006 when a declared input changed (real drift, commit it), MGS4005 when the inputs are unchanged but a dev build produced differing output (version/tool skew, not your change), or MGS4003 when a release build's identical inputs still differ (a reproducibility bug). files are the drifted outputs as Paths based at the repository root. drifted is false with every field zero when the outputs are clean. It lives here rather than on vcs because choosing between those codes is magus policy; vcs only supplies the probe. Composes vcs.status; does not replace it.",
			Args: []Arg{
				{Name: "outputs", Type: TypeStringSlice},
				{Name: "inputs", Type: TypeStringSlice, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "DriftResult"}},
			Raises:  true,
			Impl:    MagusDiagnoseDrift,
		},
		{
			Name: "bust_cache",
			Doc:  "Invalidate the build cache. Escape hatch - prefer modeling missing inputs as Sources. No arg clears all; a project path clears one project.",
			Args: []Arg{
				{Name: "project_path", Type: TypeString, Optional: true},
			},
			Returns: nil,
			Raises:  true,
			Impl:    MagusBustCache,
		},
		{
			Name: "has_charm",
			Doc:  "True when execution charm `name` is active, letting a target body branch on a charm carried in context (e.g. has_charm(\"rw\")).",
			Args: []Arg{
				{Name: "name", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeBool}},
			Impl:    MagusHasCharm,
		},

		// Everything below is Extern: DECLARED here, BOUND by
		// internal/interp/bindings/buzz.go via MapSet onto the magus namespace at run
		// time. They are here so the checker knows they exist - without a declaration a
		// namespace member is unknown, and an unknown member used to type-check as
		// `any` rather than being reported. Keep this set in step with buildMagusNS;
		// TestMagusExternsMatchBindings holds the two together.
		{
			Name: "project",
			Doc:  "Declare this directory's project: its spell, sources, outputs, and options. A magusfile calls it once at top level. Raises MGS1022 in a `magus buzz` script, which has no workspace to declare into.",
			// `any` rather than a shape: the binding accepts BOTH project(config) and
			// project(path, config), which one Buzz signature cannot express, and the
			// config map's keys are validated by the loader rather than the checker.
			Args: []Arg{{Name: "config", Type: TypeAny}, {Name: "opts", Type: TypeAny, Optional: true}},
			// NOT Raises, though the script-surface binding does fail: a magusfile calls
			// this at TOP LEVEL, where there is no enclosing function to declare !> and
			// nothing to catch with. Declaring it raising makes the one mandatory call in
			// every magusfile unwritable.
			Extern: true,
		},
		{
			Name:    "canonical_name",
			Doc:     "The canonical form of a magus entity name - a target, charm, or spell op. `build2` gains a '-' you did not type; `HTTPServer` breaks before its last letter. Returns the NAME, never a spell handle: a handle can only come from a literal import, because the target graph is built by reading imports statically.",
			Args:    []Arg{{Name: "name", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			// NOT Raises. The binding's only failure is a non-string argument, and the
			// declared `str` parameter now rejects that statically - so the raise is
			// unreachable for any call the checker admits. Declaring it would force a
			// try/catch around a pure string transform, including in the `magus buzz -e`
			// one-liners where there is no enclosing function to propagate from.
			Extern: true,
		},
		{
			Name: "fatal",
			Doc:  "Log at error level, then abort the run with exit status 1.",
			Args: []Arg{{Name: "msg", Type: TypeString, Optional: true}},
			// NOT Raises: it aborts by design. Requiring every call to be caught would
			// ask callers to handle the thing they invoked to be unhandleable.
			Extern: true,
		},
		{
			Name:   "pry",
			Doc:    "Drop into an interactive REPL at this point, with the calling scope in hand. A no-op while the magusfile is only being parsed.",
			Extern: true,
		},
	},
	// The provider namespaces the runtime assembles. Each is reached THROUGH rather
	// than called - `magus\cache.remote(<spell>)` - so it is declared as an object
	// with static extern methods; see std.Namespace for why that, and not a nested
	// module.
	//
	// None of the selection calls is Raises. Every one is made at the TOP LEVEL of a
	// magusfile, where there is no enclosing function to declare !> and nothing to
	// catch with, so declaring them raising would make them unwritable - the same
	// reason magus\project is not Raises.
	Namespaces: []Namespace{
		{
			Name: "log",
			Doc:  "Emitting a message without changing control flow: the four levels, plus hint. magus\\fatal and magus\\raise are deliberately NOT here - they END the run rather than report on it, and grouping them by how they look rather than what they do is what made this surface hard to read.",
			Methods: []Method{
				{
					Name:   "debug",
					Doc:    "Log at debug level. See magus.info.",
					Args:   []Arg{{Name: "msg", Type: TypeString, Optional: true}, {Name: "fields", Type: TypeStringMap, Optional: true}},
					Extern: true,
				},
				{
					Name:   "error",
					Doc:    "Log at error level. See magus.info. Logging an error does not abort; magus.fatal does.",
					Args:   []Arg{{Name: "msg", Type: TypeString, Optional: true}, {Name: "fields", Type: TypeStringMap, Optional: true}},
					Extern: true,
				},
				{
					Name:   "hint",
					Doc:    "Emit an advisory nudge: non-fatal, deduped, and suppressed when hints are toggled off.",
					Args:   []Arg{{Name: "msg", Type: TypeString, Optional: true}},
					Extern: true,
				},
				{
					Name:   "info",
					Doc:    "Log at info level. The only way to log from a magusfile; there is no separate log module on this surface.",
					Args:   []Arg{{Name: "msg", Type: TypeString, Optional: true}, {Name: "fields", Type: TypeStringMap, Optional: true}},
					Extern: true,
				},
				{
					Name:   "warn",
					Doc:    "Log at warn level. See magus.info.",
					Args:   []Arg{{Name: "msg", Type: TypeString, Optional: true}, {Name: "fields", Type: TypeStringMap, Optional: true}},
					Extern: true,
				},
			},
		},
		{
			Name: "cache",
			Doc:  "Remote cache backend selection.",
			Methods: []Method{{
				Name:   "remote",
				Doc:    "Select the remote cache backend, given an imported spell handle. Declared at the top level of the root magusfile.",
				Args:   []Arg{{Name: "spell", Type: TypeAnyMap}},
				Extern: true,
			}},
		},
		{
			Name: "ci",
			Doc:  "CI provider selection.",
			Methods: []Method{{
				Name:   "provider",
				Doc:    "Select the CI provider, given an imported spell handle.",
				Args:   []Arg{{Name: "spell", Type: TypeAnyMap}},
				Extern: true,
			}},
		},
		{
			Name: "secret",
			Doc:  "Secret backend selection, and the credentials read through it.",
			Methods: []Method{
				{
					Name:   "provider",
					Doc:    "Select the secret backend, given an imported spell handle.",
					Args:   []Arg{{Name: "spell", Type: TypeAnyMap}},
					Extern: true,
				},
				{
					Name: "read",
					Doc:  "Read a credential by reference through the selected backend. Unlike the selections, this is called from inside a target, so its failure IS something a caller can handle.",
					Args: []Arg{{Name: "ref", Type: TypeString}},
					// A magus-resolved value rather than a bare str, which is what lets
					// magus recognise it and keep it out of logs and cache keys.
					Returns: []Ret{{Type: TypeString}},
					Raises:  true,
					Extern:  true,
				},
			},
		},
		{
			Name: "workspace",
			Doc:  "Workspace-level declarations made from the root magusfile.",
			Methods: []Method{{
				Name:   "provider",
				Doc:    "Select the workspace provider, given an imported spell handle.",
				Args:   []Arg{{Name: "spell", Type: TypeAnyMap}},
				Extern: true,
			}},
		},
	},
}

// MagusHasCharm reports whether the execution charm name is active in ctx. It
// backs magus.has_charm, the read side of the charm system: a function target
// can publish conditionally on has_charm("rw") or branch on a custom charm.
func MagusHasCharm(ctx context.Context, name string) (bool, error) {
	return types.HasCharm(ctx, name), nil
}

// MagusBustCache invalidates cached build entries. When projectPath is empty
// all manifests are cleared; otherwise only entries for that project are removed.
// A structured warning is always emitted: this is an escape hatch, not routine.
func MagusBustCache(ctx context.Context, projectPath string) error {
	slog.WarnContext(ctx, "magus.bust_cache called - consider modeling the missing input as a Source instead",
		"project_path", projectPath)
	c := cache.FromContext(ctx)
	if c == nil {
		return nil // no cache in context (parse mode, tests)
	}
	if projectPath == "" {
		return c.Delete(ctx)
	}
	return c.Delete(ctx, projectPath)
}

// typedMagusSubcommands are the magus subcommands that have a dedicated,
// typed magus.<name>(...) method. magus.cmd warns when its first arg names one,
// nudging authors toward the clearer, signature-stable wrapper.
var typedMagusSubcommands = map[string]bool{
	"run": true, "describe": true, "insight": true, "doctor": true,
}

// errNoWorkspace is the MGS1022 error a magus.* member raises when it is called
// without the loaded workspace it reads from. Every such member serves its answer
// IN-PROCESS off types.WorkspaceFromContext, which magus.Open puts there for a
// magusfile target; a `magus buzz` script has no workspace open, and shelling out
// (magus.describe/cmd, which fork and rediscover the root) is what it reaches for
// instead. Coded so the constraint is greppable and linkable rather than one more
// bare sentence.
func errNoWorkspace(member string) error {
	return types.DiagnosticErrorf(types.MagusfileOnlyMember,
		"magus\\%s: no workspace on the context - it is callable from a magusfile target, not from a spell or a `magus buzz` script; reach for magus\\describe/magus\\cmd instead, which fork a nested magus that discovers the workspace itself", member)
}

// MagusLs lists the workspace's projects from the workspace already open on ctx.
//
// It is the first of the read-only verbs served IN-PROCESS. The typed methods below it
// (run, describe, insight, doctor) and magus.cmd all fork a full magus subprocess via
// runMagus: a process spawn, a second workspace discovery and load, a JSON encode, and a
// Buzz-side parse - to answer a question this process already has the answer to. A read
// that mutates nothing has no reason to pay that, and the domain method it needs is
// already typed (types.WorkspaceRepository.ListProjects).
//
// The workspace reaches ctx via types.WithWorkspace in magus.Open's load, so it is
// present for every magusfile target. A caller outside that path (a bare Buzz script
// run through `magus buzz`) has none, hence the guard.
func MagusLs(ctx context.Context) (types.ProjectsOutput, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.ProjectsOutput{}, errNoWorkspace("ls")
	}
	return ws.ListProjects(ctx)
}

// MagusTargets returns every project's target graph in-process. It is the typed
// counterpart to `magus describe graph`: a caller that wants the target inventory no
// longer has to shell out and parse the markdown that command renders. TargetGraph
// reads the magusfile statically, so this is side-effect free.
func MagusTargets(ctx context.Context) (types.TargetGraphOutput, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.TargetGraphOutput{}, errNoWorkspace("targets")
	}
	return ws.TargetGraph(ctx)
}

// MagusAffected computes the affected project set in-process. See MagusLs for why
// the read-only verbs do not fork.
//
// It deliberately does NOT swallow ErrAffectedFallback. When the VCS cannot produce a
// diff, magus selects every project as a safety net (MGS1010); a magusfile branching on
// this result needs to know that happened, because "nothing changed" and "we could not
// tell what changed" call for opposite decisions.
func MagusAffected(ctx context.Context, base string) (types.AffectedResult, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.AffectedResult{}, errNoWorkspace("affected")
	}
	res, err := ws.Affected(ctx, base)
	if err != nil {
		return types.AffectedResult{}, err
	}
	return *res, nil
}

// MagusWhere returns the project path containing dir, or "" when dir is inside none.
// "" rather than an error: asking whether a path is inside a project is a question, and
// "no" is a valid answer a magusfile branches on.
func MagusWhere(ctx context.Context, dir string) (string, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return "", errNoWorkspace("where")
	}
	p, ok := ws.Where(dir)
	if !ok {
		return "", nil
	}
	return p.Path, nil
}

// MagusRaise fails with a caller-defined coded diagnostic.
//
// magus already gives a Buzz `catch` the code, message and url of its OWN failures
// (diagnostics.Error.BuzzError), so a magusfile can branch on MGS2001 without matching
// prose. Authoring one was the missing half: a magusfile could only `throw` a string,
// which leaves its own callers doing the substring matching that mechanism exists to
// avoid. Workspaces build things magus cannot anticipate, and their failures deserve the
// same stable identifier magus gives its own.
//
// The MGS prefix is refused rather than merely discouraged. MGS codes are a closed
// catalog that `magus explain`, the knowledge graph and the docs URL map all resolve
// against, so a workspace minting MGS9999 would produce a diagnostic that renders like
// magus's own and documents nothing.
//
// `raise` rather than `throw`, which would match the Buzz keyword: throw is reserved, so
// a member cannot be named it. error and fatal are taken by the logging members above.
//
// cause and url live in an opts map, not as trailing positionals. The generated trampoline
// binds by index, so a fourth positional url was unreachable without also passing a cause -
// and every other optional in this module is already an opts map.
func MagusRaise(_ context.Context, code, message string, opts map[string]any) error {
	if code == "" {
		return errors.New("magus.raise: needs a code, e.g. \"ACME1001\" - it is the stable identifier a caller branches on")
	}
	if message == "" {
		return fmt.Errorf("magus.raise: %s needs a message; a code is an identifier, not a sentence", code)
	}
	if strings.HasPrefix(strings.ToUpper(code), "MGS") {
		return fmt.Errorf("magus.raise: %q is in magus's own MGS namespace, which is a closed catalog; pick a prefix for this workspace instead", code)
	}
	// A per-call domain is how a caller-supplied url reaches the rendered error: Error's
	// url field is captured at construction from the domain's function, never set later.
	url, _ := opts["url"].(string)
	d := diagnostics.New(func(diagnostics.Code) string { return url })
	summary, c := buzzCause(opts["cause"])
	if c == nil {
		return d.Errorf(diagnostics.Code(code), "%s", message)
	}
	// Wrapf keeps the cause reachable through Unwrap but deliberately does not render it,
	// so the summary is spliced in here to match what Go's %w prints. A cause nobody can
	// see is the failure the author was trying to report.
	return d.Wrapf(diagnostics.Code(code), c, "%s: %s", message, summary)
}

// buzzCause converts a caught Buzz value into the one-line summary to splice into the
// wrapper's message, plus the Go error to keep reachable underneath it.
//
// A structured catch arrives as the map BuzzError produced, so a coded cause is rebuilt as
// a coded error and errors.Is keeps matching it underneath the new code. The summary is
// the cause's message rather than its rendered form, which would drag a second "see:" line
// into the middle of the wrapper's.
func buzzCause(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		if t == "" {
			return "", nil
		}
		return t, errors.New(t)
	case map[string]any:
		msg, _ := t["message"].(string)
		code, _ := t["code"].(string)
		switch {
		case code != "":
			u, _ := t["url"].(string)
			e := diagnostics.New(func(diagnostics.Code) string { return u }).
				Errorf(diagnostics.Code(code), "%s", msg)
			return "[" + code + "] " + msg, e
		case msg != "":
			return msg, errors.New(msg)
		}
		return "", nil
	default:
		s := fmt.Sprintf("%v", t)
		return s, errors.New(s)
	}
}

// MagusGraph returns the project dependency graph as a flat object. See MagusLs for
// why the read-only verbs are served in-process.
func MagusGraph(ctx context.Context) (types.GraphView, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.GraphView{}, errNoWorkspace("graph")
	}
	g, err := ws.Graph()
	if err != nil {
		return types.GraphView{}, err
	}
	return g.View(), nil
}

// MagusCmd is the escape hatch: it runs `magus <sub> <args>` for a subcommand with no
// dedicated wrapper (status, affected, agent, ...). The subcommand is a parameter of its
// own rather than args[0], which is what lets the warning below be exact instead of a
// guess at a list's first element, and what keeps the invocation readable to anything
// reading the source. Like the typed methods it runs in the contextual project dir
// unless opts.dir says otherwise; see runMagus.
func MagusCmd(ctx context.Context, sub string, args []string, opts map[string]any) (types.ExecResult, error) {
	warnIfTypedSubcommand(ctx, sub)
	return runMagusSub(ctx, sub, args, opts)
}

// warnIfTypedSubcommand warns when sub names a subcommand with a dedicated
// magus.<name>(...) method, nudging authors off the escape hatch. It is the pure
// decision half of MagusCmd, split out so it can be tested without the nested exec.
func warnIfTypedSubcommand(ctx context.Context, sub string) {
	if typedMagusSubcommands[sub] {
		slog.WarnContext(ctx, "magus.cmd called for a subcommand with a dedicated method; prefer it for clarity and a stable signature",
			"subcommand", sub,
			"hint", fmt.Sprintf("use magus.%s([...]) instead of magus.cmd(%q, [...])", sub, sub))
	}
}

// MagusRun runs `magus run <args>` recursively; see runMagus.
func MagusRun(ctx context.Context, args []string, opts map[string]any) (types.ExecResult, error) {
	return runMagusSub(ctx, "run", args, opts)
}

// MagusDescribe runs `magus describe <args>`; see runMagus.
func MagusDescribe(ctx context.Context, args []string, opts map[string]any) (types.ExecResult, error) {
	return runMagusSub(ctx, "describe", args, opts)
}

// MagusInsight runs `magus insight <args>`; see runMagus.
func MagusInsight(ctx context.Context, args []string, opts map[string]any) (types.ExecResult, error) {
	return runMagusSub(ctx, "insight", args, opts)
}

// MagusDoctor validates the workspace and returns the typed report.
//
// It is the shape every method here should have and most do not yet: the child already
// knows how to say this in JSON, so the answer crosses the boundary as the domain type
// rather than as console text a caller has to parse back out of a string.
func MagusDoctor(ctx context.Context, args []string, opts map[string]any) (types.DoctorReport, error) {
	return runMagusJSON[types.DoctorReport](ctx, "doctor", args, opts)
}

// MagusAffectedImpact reports the affected set through a nested magus. It is the forking
// counterpart to MagusAffected, and answers the richer question: not just which projects,
// but which files seeded them.
func MagusAffectedImpact(ctx context.Context, base string, opts map[string]any) (types.ImpactResult, error) {
	args := []string{"--impact"}
	if base != "" {
		args = append(args, "--base", base)
	}
	return runMagusJSON[types.ImpactResult](ctx, "affected", args, opts)
}

// MagusTargetGraph returns every project's target graph through a nested magus, the
// forking counterpart to MagusTargets. See runMagusJSON.
func MagusTargetGraph(ctx context.Context, opts map[string]any) (types.TargetGraphOutput, error) {
	return runMagusJSON[types.TargetGraphOutput](ctx, "describe", []string{"graph"}, opts)
}

// MagusInsightReport returns every insight lens as one typed report. `magus insight`
// itself stays the escape hatch for a single lens, the same split describe keeps: the
// noun that has a domain type gets a typed method, the rest stays free-form.
func MagusInsightReport(ctx context.Context, args []string, opts map[string]any) (types.InsightReport, error) {
	return runMagusJSON[types.InsightReport](ctx, "insight", append([]string{"report"}, args...), opts)
}

// MagusDescribeFile classifies paths as generated output, declared source, or
// unclaimed. See runMagusJSON for why it forks rather than reading the workspace on
// the context: this answer is wanted precisely where there is no workspace loaded -
// a `magus buzz` script deciding whether a changed file is worth a human's attention.
func MagusDescribeFile(ctx context.Context, paths []string, opts map[string]any) (types.FileReport, error) {
	return runMagusJSON[types.FileReport](ctx, "describe", append([]string{"file"}, paths...), opts)
}

// runMagusJSON runs a nested magus subcommand and decodes its report into T.
//
// It forces `-o json` and quiet: the caller is consuming the value, not watching the
// output, and letting a caller pass its own -o would hand back a shape T cannot decode.
// The alternative - returning ExecResult and making every caller run the bytes through
// jsonDecode - loses the type at the boundary, and annotating a decoded value back to a
// mirror does NOT restore it: that compiles and silently reads null for every field.
func runMagusJSON[T any](ctx context.Context, sub string, args []string, opts map[string]any) (T, error) {
	var out T
	quiet := map[string]any{"quiet": true}
	for k, v := range opts {
		if k == "quiet" {
			continue
		}
		quiet[k] = v
	}
	res, runErr := runMagusSub(ctx, sub, append(append([]string(nil), args...), "-o", "json"), quiet)
	// DECODE FIRST, exit status second. A report command exits non-zero precisely when
	// it has something to report - doctor exits 1 because a check failed - and raising
	// there would throw away the very payload the caller asked for. A report that parsed
	// IS the answer; the caller branches on it (summary.fail), which is strictly more
	// than an exit code carries. Only an unparsable answer is a failure to answer.
	if derr := json.Unmarshal([]byte(res.Stdout), &out); derr == nil {
		return out, nil
	}
	if runErr != nil {
		return out, runErr
	}
	return out, fmt.Errorf("magus.%s: decode report: %s", sub, res.Stderr)
}

// runMagusSub runs a nested magus invocation for subcommand sub: it prepends sub
// to args (so the subcommand name is fixed by the caller, not user-supplied) and
// hands off to runMagus.
func runMagusSub(ctx context.Context, sub string, args []string, opts map[string]any) (types.ExecResult, error) {
	return runMagus(ctx, sub, append([]string{sub}, args...), opts)
}

// resolveRunDir picks the directory a nested magus runs in: the contextual project dir,
// or opts.dir resolved RELATIVE to it - exactly as proc.exec's dir is, so the two spell the
// same idea the same way. An absolute opts.dir wins outright, and with no contextual dir
// there is nothing to resolve against, so it is used as given.
func resolveRunDir(ctx context.Context, opts map[string]any) string {
	dir, _ := CwdFromContext(ctx)
	sub, ok := opts["dir"].(string)
	if !ok || sub == "" {
		return dir
	}
	if filepath.IsAbs(sub) || dir == "" {
		return sub
	}
	return filepath.Join(dir, sub)
}

// runMagus runs a nested magus invocation with the full arg vector, yielding the
// caller's concurrency slot for the duration so the child can run. Output streams
// live and is captured: on success it returns the same {stdout, stderr, code, ok}
// object as proc.exec, so a magusfile can read a subcommand's output (e.g. `magus
// describe graph -o markdown` to generate MAGUS.md). It raises (non-nil error, nil
// object) when the child can't launch or exits non-zero, mirroring proc.exec. label
// names the calling method for error messages.
//
// The child runs in the working directory carried by ctx (WithCwd), so a nested
// project describes/insights its own project rather than the root workspace (the
// contextual-cwd contract every magus stdlib primitive honors). opts may carry
// "root" (string), emitted as the global --root flag (which precedes the subcommand).
func runMagus(ctx context.Context, label string, args []string, opts map[string]any) (types.ExecResult, error) {
	self, err := os.Executable()
	if err != nil {
		return types.ExecResult{}, fmt.Errorf("magus.%s: executable: %w", label, err)
	}

	// Global flags (e.g. --root) precede the subcommand and its args.
	var full []string
	if root, ok := opts["root"].(string); ok && root != "" {
		full = append(full, "--root", root)
	}
	full = append(full, args...)

	// Re-inject the daemon socket vars: childEnv withholds them from subprocesses
	// (the socket is unauthenticated - MGS2008), but a nested magus is a legitimate
	// recursive invocation that needs daemon access. Passed as Env overrides, which
	// childEnv layers last so they win; MAGUS/MAGUS_LEVEL are added by childEnv. The
	// withheld set is run.DaemonForwardVars, so this re-injection stays in lockstep.
	var env []string
	for _, k := range run.DaemonForwardVars {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
			// Debug, not Info: this fires on every recursive invocation (a fan-out can
			// spawn many), so at default verbosity it is noise. An internal correctness
			// note, not user-actionable; surface it only at -v.
			slog.DebugContext(ctx, types.FormatDiagnostic(types.DaemonSocketWithheld,
				"daemon socket injected into recursive magus invocation"), "var", k)
		}
	}

	// Run in the contextual project dir; "" inherits the process cwd (the
	// behavior for magusfile targets that run under a process chdir). opts.dir
	// redirects it, resolved RELATIVE to that contextual dir exactly as proc.exec's
	// dir is - a nested magus that must run somewhere else (a sibling project, a
	// directory of scripts) has no other way to say so, and reaching for
	// proc.exec("magus", ...) to get it is the thing magus warns about.
	dir := resolveRunDir(ctx, opts)

	// opts.quiet captures the output without echoing it to the console, for a
	// command whose stdout is consumed (e.g. written to a file), not displayed.
	quiet, _ := opts["quiet"].(bool)

	lim := cache.LimiterFromContext(ctx)
	var rec types.ExecResult
	var cmdErr error
	runFn := func() error {
		res, err := run.Exec(ctx, self, full, run.ExecOptions{Dir: dir, Env: env, Capture: true, Quiet: quiet})
		switch {
		case err != nil && errors.Is(err, types.ExecDenied):
			cmdErr = err
		case res.Code != 0 && !res.Started:
			// The child never launched (binary not found, permission, ctx cancelled
			// before exec); surface the real cause, not a fabricated "code -1".
			// Mirrors proc.exec's runResult.
			cmdErr = fmt.Errorf("magus.%s: %s: %w", label, strings.Join(full, " "), err)
		case res.Code != 0:
			// The child's own diagnostic is not repeated here. It reaches the console
			// itself - printed by the child when it runs as its own process, and by
			// proc.Forward when it was adopted - so folding it into this message too
			// produced the same paragraph twice, once truncated into a `cause:` line.
			// Under opts.quiet nothing streams by construction, and there the caller's
			// own ExecResult.Stderr is the account of the failure.
			cmdErr = fmt.Errorf("magus.%s: %s exited with code %d", label, strings.Join(full, " "), res.Code)
		}
		if res.Started {
			// Recorded even on a non-zero exit. A child that RAN said something, and
			// that output is the answer for a command whose failure IS its report -
			// doctor exits 1 because a check failed, and dropping stdout there left
			// nothing to decode. The error is still returned alongside, so a caller
			// that only wants the happy path is unaffected.
			rec = types.ExecResult{
				Stdout: strings.TrimSpace(res.Stdout),
				Stderr: strings.TrimSpace(res.Stderr),
				Code:   res.Code,
				OK:     res.Code == 0,
			}
		}
		return nil
	}
	if err := proc.RunChildSync(ctx, lim, runFn); err != nil {
		return types.ExecResult{}, fmt.Errorf("magus.%s: %w", label, err)
	}
	return rec, cmdErr
}

// MagusDiagnoseDrift diagnoses a generate gate's drift into a coded diagnostic. Given the
// target's declared output globs and input globs (project-relative) and the fact that the
// tree drifted, it distinguishes the three causes the plan defines:
//
//   - outputs dirty AND a declared input is also dirty -> MGS4006 StaleGeneratedOutput:
//     a source input changed, so regeneration is expected; commit it.
//   - outputs dirty, inputs byte-identical, running a DEV build -> MGS4005 EnvironmentalDrift:
//     the committed form is produced by the pinned release (compat contract), so a dev
//     build's differing output is version/tool skew - not the developer's change.
//   - outputs dirty, inputs byte-identical, running a RELEASE build -> MGS4003
//     NondeterministicOutput: same inputs and generator version, yet output differs - a
//     reproducibility bug.
//
// It RETURNS the classification as a verdict record rather than throwing, so the gate
// owns the response - fail on a clean-tree drift, warn on a mid-edit dirty tree (the
// plan's local-warn / CI-fail split). The record is a plain map:
//
//	{ drifted: bool, code: str, message: str, url: str, files: []str }
//
// drifted is false (and code/message/url empty, files empty) when the outputs are not
// actually dirty. files carries the backend's status lines for the drifted outputs, so a
// gate can say WHICH files moved without shelling out to the VCS itself.
// It composes vcs.isDirty (called on outputs and inputs) rather than replacing it:
// isDirty stays the general "is this path dirty" primitive; diagnoseDrift is the
// drift-specific reading on top of it plus the version signal.
func MagusDiagnoseDrift(ctx context.Context, outputs, inputs []string) (types.DriftResult, error) {
	// Same keys as the drifted verdict, so a caller can read .files unconditionally
	// rather than discovering the key is absent only on the clean path.
	var clean types.DriftResult
	v, _ := resolveVCS(ctx)
	if v == nil {
		return clean, nil
	}
	dir, err := EffectiveCwd(ctx)
	if err != nil {
		dir = ""
	}
	// DirtyFiles, not Dirty: the verdict carries WHICH outputs drifted, and Dirty is
	// defined in terms of this anyway, so naming them costs nothing extra. A gate that
	// reports only "something drifted" sends its reader to reproduce the run just to
	// learn what a status call already knew - and a gate fires precisely when the
	// reader is looking at a CI log rather than the tree.
	dirtyFiles, err := v.DirtyFiles(ctx, dir, outputs)
	if err != nil {
		// Split from the !outDirty case below on purpose: they were one branch, so a
		// failed probe returned the same "clean" verdict as a genuinely clean tree. A
		// drift diagnosis that cannot read the tree has no verdict to give.
		return types.DriftResult{}, types.WrapDiagnostic(types.VCSUnavailable, err, "read %s status", v.Name())
	}
	if len(dirtyFiles) == 0 {
		return clean, nil
	}
	inDirty := false
	if len(inputs) > 0 {
		inDirty, _ = v.Dirty(ctx, dir, inputs)
	}

	// Shared with `magus vcs add`, which asks the same question at staging time: see
	// types.ClassifyDrift for why the fork lives there rather than here.
	code, msg := types.ClassifyDrift(inDirty, types.MagusVersionFromContext(ctx))
	root, err := v.Root(ctx, dir)
	if err != nil {
		root = dir
	}
	files := make([]types.Path, 0, len(dirtyFiles))
	for _, p := range statusPaths(v.Name(), dirtyFiles) {
		files = append(files, types.Path{Value: p, Base: root})
	}
	return types.DriftResult{
		Drifted: true,
		Code:    string(code),
		Message: msg,
		URL:     types.CodeURL(code),
		Files:   files,
	}, nil
}
