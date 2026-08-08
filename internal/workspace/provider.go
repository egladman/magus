package workspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// A WORKSPACE PROVIDER is a spell that supplies the workspace's project set,
// because another tool already owns it: nx, gradle, pnpm, cargo. A magusfile wires
// one with magus\workspace.provider(<spell handle>), the spell exports the
// list_projects contract function (spells.ListProjectsContract), and magus folds
// what it returns into the workspace as ordinary projects.
//
// This is the third instance of one extension point, not a third mechanism:
// magus\cache.remote wires a cache backend and magus\ci.provider wires a CI
// provider the same way, and in all three magus knows nothing about the foreign
// system - it invokes contract functions by name and reads what comes back.
//
// NAMING: "provider" in this file always means a workspace provider. The unrelated
// WithProvider in workspace.go takes an observability.Provider; nothing here has
// anything to do with telemetry.
//
// The runner lives behind a hook for the reason the remote-cache opener does: only
// the bindings layer can run a Buzz spell, and the magus library must not link the
// VM to open a workspace.

// ProviderRunner invokes spellName's list_projects contract against the workspace at
// root and returns the records it reported, undecoded. It returns the wire record
// (spells.ProvidedProject) rather than options so the result stays serializable -
// which is what lets [AddProvidedProjects] cache it instead of shelling out to the
// foreign tool on every magus command.
type ProviderRunner func(ctx context.Context, spellName, root string) ([]spells.ProvidedProject, error)

var providerRunner ProviderRunner

// RegisterProviderRunner installs the runner [AddProvidedProjects] delegates to.
// Meant to be called once, from the bindings package's init; a second call panics
// rather than silently shadowing the first.
func RegisterProviderRunner(fn ProviderRunner) {
	if providerRunner != nil {
		panic("workspace: provider runner already registered")
	}
	providerRunner = fn
}

// AddProvidedProjects runs each wired provider in order and adds the projects it
// reports to ws. It is a no-op when no provider was wired, which is every workspace
// that does not have one - so the cost of the mechanism to everyone else is one nil
// check per open.
//
// cache says where a provider's answer is remembered between commands; a zero value
// re-runs every provider. See provider_cache.go for what invalidates an entry.
//
// Precedence, in the order the rules apply:
//
//   - A path that already carries a magusfile is left alone (MGS1019). The
//     magusfile is the workspace's own definition, the same rule that makes a
//     magusfile target shadow a same-named spell op.
//   - The FIRST provider to report a path owns it. Two providers claiming one
//     directory is a real configuration (an nx repo with a cargo workspace inside
//     it), and wiring order is the only deterministic tiebreak.
//   - magus\project("libs/foo", {...}) still layers on top of everything here,
//     because WorkspaceRegistry.Apply runs after this.
//
// A rejected path fails the whole load rather than being skipped: a silently
// dropped project is a target that no longer exists, with nothing on screen to say
// so. ws is mutated in place, so a caller that continues past an error holds a
// partially folded workspace; every caller today discards it.
func AddProvidedProjects(ctx context.Context, ws *types.Workspace, spellNames []string, cache ProviderCache) error {
	if len(spellNames) == 0 {
		return nil
	}
	if providerRunner == nil {
		return errors.New("magus: workspace provider wired but no runner registered in this binary")
	}
	// Which provider claimed each path, so a collision reports the right cause: an
	// earlier provider, or this same provider reporting one directory twice.
	claimedBy := map[string]string{}
	for _, spellName := range spellNames {
		provided, err := providedProjects(ctx, cache, ws.Root, spellName)
		if err != nil {
			return fmt.Errorf("magus: workspace provider %q: %w", spellName, err)
		}
		if len(provided) == 0 {
			slog.WarnContext(ctx, "magus: workspace provider reported no projects",
				slog.String("provider", spellName), slog.String("root", ws.Root))
			continue
		}
		for _, pp := range provided {
			rel, err := resolveProvidedPath(ws.Root, spellName, pp.Path)
			if err != nil {
				return err
			}
			if _, exists := ws.Projects[rel]; exists {
				reportProvidedCollision(ctx, spellName, rel, claimedBy[rel])
				continue
			}
			p := &types.Project{
				Path: rel,
				Dir:  filepath.Join(ws.Root, filepath.FromSlash(rel)),
				// Provenance, not identity: it is what `magus describe project` answers
				// with when there is no magusfile to point at.
				Origin: types.ProvidedBy(spellName),
			}
			for _, opt := range projectOptions(pp) {
				if err := opt(p); err != nil {
					return fmt.Errorf("magus: workspace provider %q: project %q: %w", spellName, rel, err)
				}
			}
			ws.Projects[rel] = p
			claimedBy[rel] = spellName
		}
	}
	return nil
}

// projectOptions turns one reported record into the ProjectOption values a
// magusfile's magus\project({...}) would have produced. Routing through the SAME
// constructors is what keeps one vocabulary for project configuration - and one
// validation path: WithDependsOn resolves and rejects paths here exactly as it does
// for a hand-authored dependency, and WithRegisteredSpell rejects an unknown spell.
func projectOptions(pp spells.ProvidedProject) []ProjectOption {
	var opts []ProjectOption
	if pp.Name != "" {
		opts = append(opts, WithName(pp.Name))
	}
	if len(pp.DependsOn) > 0 {
		opts = append(opts, WithDependsOn(pp.DependsOn...))
	}
	if len(pp.Sources) > 0 {
		opts = append(opts, WithSources(pp.Sources...))
	}
	if len(pp.Outputs) > 0 {
		opts = append(opts, WithOutputs(pp.Outputs...))
	}
	for _, name := range pp.Spells {
		opts = append(opts, WithRegisteredSpell(name))
	}
	if pp.Exclusive {
		opts = append(opts, WithExclusive())
	}
	return opts
}

// reportProvidedCollision explains why a reported project did not land. owner is the
// provider that already claimed the path, or "" when a magusfile did.
//
// The two cases differ in who needs to act. A magusfile shadow is a precedence rule
// the author may not have intended, so it warns (MGS1019). A provider-vs-provider
// collision follows a documented rule with one possible outcome, so it stays at
// debug: a workspace whose two providers overlap should not be nagged every
// command.
func reportProvidedCollision(ctx context.Context, spellName, rel, owner string) {
	switch owner {
	case "":
		// slog, not stderr: the fold runs on every open (cache hit included), so a raw
		// stderr line would nag on every command and shell completion, and would land in
		// the daemon's log as unstructured text among structured records.
		slog.WarnContext(ctx, types.FormatDiagnostic(types.ProviderProjectShadowed,
			fmt.Sprintf("workspace provider %q reported %q, which already has a magusfile; the magusfile wins and the provider's configuration for it is ignored", spellName, rel)))
	case spellName:
		slog.DebugContext(ctx, "magus: workspace provider reported the same path twice; the first record wins",
			slog.String("provider", spellName), slog.String("path", rel))
	default:
		slog.DebugContext(ctx, "magus: workspace provider path already claimed by an earlier provider",
			slog.String("provider", spellName), slog.String("owner", owner), slog.String("path", rel))
	}
}

// resolveProvidedPath validates one reported path and returns it in the workspace's
// own coordinate system (repo-relative, forward slashes).
//
// A provider is code that shelled out to another tool, so its answer is the least
// trustworthy input in the load path: this rejects rather than repairs. The symlink
// resolution is the half a lexical check misses - a link inside the tree pointing
// outside is textually clean, and would otherwise become a project whose Dir is the
// exec cwd and source-hash root for a directory outside the workspace.
func resolveProvidedPath(root, spellName, reported string) (string, error) {
	reject := func(why string) error {
		return types.DiagnosticErrorf(types.ProviderPathRejected,
			"workspace provider %q reported project path %q: %s", spellName, reported, why)
	}
	trimmed := strings.TrimSpace(reported)
	if trimmed == "" {
		return "", reject("a project path is required")
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") {
		return "", reject("project paths are relative to the workspace root, never absolute")
	}
	rel := path.Clean(filepath.ToSlash(trimmed))
	if rel == "." {
		return "", reject("the root project belongs to the magusfile that wired the provider, not to the provider")
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", reject("the path escapes the workspace root")
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", reject("no such directory in the workspace")
		}
		return "", reject(fmt.Sprintf("cannot read the directory: %v", err))
	}
	// Lstat, and a symlink is refused outright rather than followed-and-contained.
	// Discovery does not follow symlinked directories either (see workspace.md), so
	// accepting one here would make a provided project reachable by a route a declared
	// one is not - and two reported paths pointing at one real directory would become
	// two projects sharing a cwd, an output set, and a source hash.
	if info.Mode()&os.ModeSymlink != 0 {
		return "", reject("the path is a symlink; magus does not follow symlinked directories into projects")
	}
	if !info.IsDir() {
		return "", reject("a project is a directory")
	}
	// Resolve BOTH sides rather than trusting the caller's root. project.Discover
	// hands over a symlink-free root, but a library caller building a types.Workspace
	// by hand (a macOS /tmp path, say) would otherwise have every provided path
	// rejected as escaping.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", reject(fmt.Sprintf("cannot resolve the directory: %v", err))
	}
	if resolved != realRoot && !strings.HasPrefix(resolved, realRoot+string(filepath.Separator)) {
		return "", reject("the path resolves outside the workspace root through a symlink")
	}
	return rel, nil
}
