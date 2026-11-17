package guard

import (
	"cmp"
	"context"
	"errors"
	"log/slog"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	typesagent "github.com/egladman/magus/types/agent"
)

// Dependencies hands the guard the workspace facts its rules cannot resolve for
// themselves: what NewDependencies builds from the CLI's workspace open, cache dir
// and loaded config, plus the graph and symbol index lookups and the model facts, which
// only the CLI holds.
//
// rootOverride is the --root the command loaded its workspace with, "" for the cwd. A rule
// asking for "the workspace" gets that one: resolving the cwd instead names a second
// workspace whenever the command ran with --root from inside another checkout.
func (e HookEnv) Dependencies(ctx context.Context, rootOverride string) Dependencies {
	deps, _ := e.LoadDependencies(ctx, rootOverride)
	return deps
}

// checkoutJudgeDeps drops deps' rules when binary is not root's ./magus.
// A delegate keeps them. Its caller picked it as the call's checkout
// binary, and root is only the hook's working directory.
func checkoutJudgeDeps(ctx context.Context, deps Dependencies, root, binary string) Dependencies {
	if delegatedBy(ctx) != "" {
		return deps
	}
	return ForeignJudgeDeps(deps, root, binary)
}

// LoadDependencies is Dependencies that also returns the rules it loaded, nil
// when none loaded, for a caller that must know which files they came from.
func (e HookEnv) LoadDependencies(ctx context.Context, rootOverride string) (Dependencies, *WorkspaceRules) {
	return e.loadDependenciesAt(ctx, rootOverride, CallAuthority{})
}

// loadDependenciesAt is LoadDependencies for a call whose authority
// resolved: its policy loads from the row's checkout at the row's revision.
func (e HookEnv) loadDependenciesAt(ctx context.Context, rootOverride string, authority CallAuthority) (Dependencies, *WorkspaceRules) {
	root, err := e.FindRoot(rootOverride)
	if err != nil {
		root = ""
	}
	policyRoot := root
	if authority.Checkout != "" {
		leased, err := e.FindRoot(authority.Checkout)
		if err != nil {
			slog.WarnContext(ctx, "the lease's checkout holds no workspace, so its call is judged by this checkout's policy",
				attr.Error(err), attr.Component("magus shell"))
		}
		policyRoot = cmp.Or(leased, root)
	}
	inspect := func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
		return e.Inspect(ctx, rootOverride, root)
	}
	// An unresolvable path leaves the trail row without one; the version still records.
	binary, _ := e.BinaryPath()
	deps, rules := NewDependencies(ctx, Wiring{
		Root:                 policyRoot,
		Authority:            authority.Revision,
		Config:               e.Config,
		BinaryVersion:        e.Version,
		Binary:               binary,
		Inspect:              inspect,
		TargetPolicies:       e.targetPolicies(rootOverride, inspect),
		RecordTargetPolicies: e.recordTargetPolicies(rootOverride),
		CacheDir:             e.CacheDir(rootOverride),
		Trace:                e.Trace,
	})
	deps = checkoutJudgeDeps(ctx, deps, root, binary)
	// The rule cliOptions opens every workspace with.
	deps.TierRule = e.TierRule
	index := e.newLookups(rootOverride)
	deps.GraphStaleAdvice = index.staleGraphAdvice
	deps.SymbolDefined = index.symbolDefined
	deps.SymbolIDs = index.symbolIDs
	deps.SymbolSites = index.symbolSites
	deps.GraphIDs = index.graphIDs
	deps.IndexedIDs = index.indexedIDs
	cacheDir := deps.CacheDir
	deps.GraphMoved = func(ctx context.Context, root string) (string, bool) {
		if op := knowledge.ReadGuardOperation(ctx, root); op != "" {
			return knowledge.GuardCheckout{}.StaleAt(knowledge.GuardCheckout{Operation: op}), true
		}
		dir, err := cacheDir(root)
		if err != nil {
			return "", false
		}
		built, err := knowledge.ReadGuardIndexCheckout(dir, root)
		if err != nil {
			return "", false
		}
		return built.StaleAt(knowledge.GuardCheckout{}), false
	}
	deps.SymbolIndexBuilt = func(root string) bool {
		dir, err := cacheDir(root)
		if err != nil {
			return false
		}
		has, err := knowledge.GuardIndexHasSymbols(dir, root)
		return err == nil && has
	}
	// A hook reads the model facts install wrote beside its tool rows: loading the workspace
	// for them cost most of a judgment, and under load more than the hook's deadline. A
	// caller that is no hook names no tool rows and loads the workspace as before.
	if tools, hook := agent.ToolRowsFromContext(ctx); hook {
		facts, err := tools.Models()
		if err == nil {
			deps.ModelRows = func() []types.ModelPricing { return facts.Rows }
			deps.ModelTiers = func() *typesagent.ModelTierSet { return facts.Tiers }
			return deps, rules
		}
		slog.WarnContext(ctx, "the guard loads the whole workspace for its model rows and tiers, which can miss the hook's deadline",
			attr.Error(err), attr.Notice(""), attr.Component("magus shell"))
	}
	deps.ModelRows = e.modelRows(ctx, rootOverride)
	deps.ModelTiers = e.modelTiers(ctx, rootOverride)
	return deps, rules
}

// targetPolicies answers what the workspace at root, "" for the one found from
// rootOverride, declares as target policy from the knowledge store's record of it (see
// workspacegraph.RecordedTargetPolicies). Each call holds a lazy workspace of its own, whose one walk of
// the tree is taken now: a daemon grading checks keeps these dependencies for hours, and
// a walk kept from an earlier call would vouch for a record the tree has since moved past.
// The record is read without opening the workspace; open is there only because a lazy
// workspace must say how it would be opened.
func (e HookEnv) targetPolicies(rootOverride string, inspect func(context.Context, string) (types.WorkspaceRepository, error)) func(context.Context, string) (types.TargetPolicyIndex, bool) {
	return func(ctx context.Context, root string) (types.TargetPolicyIndex, bool) {
		if e.LazyWorkspace == nil {
			return types.TargetPolicyIndex{}, false
		}
		ask := root
		if root == "" {
			found, err := workspace.FindRoot(rootOverride)
			if err != nil {
				return types.TargetPolicyIndex{}, false
			}
			root = found
		}
		lw := e.LazyWorkspace(root, func(ctx context.Context) (types.WorkspaceRepository, error) {
			return inspect(ctx, ask)
		})
		return workspacegraph.RecordedTargetPolicies(ctx, lw, e.Config)
	}
}

// recordTargetPolicies records what ws, opened for root, declares as
// target policy, so the next judgment answers from targetPolicies.
func (e HookEnv) recordTargetPolicies(rootOverride string) func(context.Context, string, types.WorkspaceRepository) error {
	return func(ctx context.Context, root string, ws types.WorkspaceRepository) error {
		if e.LazyWorkspace == nil {
			return errors.New("the hook has no way to stand in a lazy workspace, so the target policies were not recorded")
		}
		if root == "" {
			found, err := workspace.FindRoot(rootOverride)
			if err != nil {
				return err
			}
			root = found
		}
		lw := e.LazyWorkspace(root, func(context.Context) (types.WorkspaceRepository, error) {
			return ws, nil
		})
		return workspacegraph.RecordTargetPolicies(ctx, lw, e.Config)
	}
}

// LoadRules loads the guard rules of the workspace found from rootOverride (the cwd's
// when it is "") from its root magusfile alone, nil with the reason when that file does not load, and nil with no
// error outside any workspace. Unloadable is not fatal for the reason
// LoadWorkspaceShellRules gives: a magusfile typo must not take down every hook.
//
// Not the full workspace load: that opens every project and costs an order of magnitude
// more, on every agent tool call, for facts no guard rule reads. The rules that do need
// the workspace open it through Dependencies.Inspect, and only when they apply.
func (e HookEnv) LoadRules(ctx context.Context, rootOverride string) (*WorkspaceRules, error) {
	root, err := e.FindRoot(rootOverride)
	if err != nil {
		return nil, nil //nolint:nilnil,nilerr // outside a workspace there are no rules and nothing failed
	}
	return LoadWorkspaceRules(ctx, root, types.VCSOptions{
		Enabled: e.Config.VCS.Enabled, Name: e.Config.VCS.Name, BaseRef: e.Config.VCS.BaseRef,
	})
}

// LoadWorkspaceShellRules returns additive rules the root magusfile of the workspace found
// from rootOverride declared via magus\guard.shell, and the last non-empty dialect among
// them for outer parse. Missing or unloadable is empty so a magusfile typo cannot take
// down every shell hook (built-ins still apply).
func (e HookEnv) LoadWorkspaceShellRules(ctx context.Context, rootOverride string) ([]WorkspaceShellRule, Dialect) {
	rules, _ := e.LoadRules(ctx, rootOverride)
	if rules == nil {
		return nil, ""
	}
	return rules.ShellRules()
}
