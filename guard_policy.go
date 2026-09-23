package magus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/secret"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// ApprovedPolicy is the authority an unapproved loosening of a workspace guard rule waits
// on. The working tree applies wherever it is stricter; where it is looser, the approved
// sources decide, so an agent cannot relax the rules that grade it by editing them.
//
// The checked-out commit is the first implementation. A commit is not proof a person
// approved anything, which is why this is an interface: an approved pin replaces HEAD here
// without the evaluator, the stricter-of merge or the trail provenance changing.
type ApprovedPolicy interface {
	// Pending returns the absolute paths of the workspace sources that may differ from
	// their approved content: every .buzz source, and the config that shapes a load. Empty
	// lets the caller skip evaluating the approved sources at all.
	Pending(ctx context.Context) ([]string, error)
	// ReadFile returns the approved content of an absolute path inside the workspace, and
	// an error when the approved state has no such file.
	ReadFile(ctx context.Context, path string) ([]byte, error)
}

// headPolicy approves what the checked-out commit holds, read one file at a time through
// the VCS layer rather than by materializing the revision.
type headPolicy struct {
	workspace string
	repoRoot  string
	driver    types.VCSDriver
}

// loadShapingFiles are the files besides Buzz sources whose edit can change what a load
// registers, or whether it loads at all.
var loadShapingFiles = []string{workspaceMarker, remotespell.LockFile}

func (h headPolicy) Pending(ctx context.Context) ([]string, error) {
	dirty, err := h.driver.DirtyFiles(ctx, h.workspace, nil)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range dirty {
		if strings.HasSuffix(p, ".buzz") || slices.Contains(loadShapingFiles, path.Base(p)) {
			out = append(out, filepath.Join(h.repoRoot, filepath.FromSlash(p)))
		}
	}
	return out, nil
}

func (h headPolicy) ReadFile(ctx context.Context, path string) ([]byte, error) {
	// The directory is resolved, not the file, so a file the working tree deleted still
	// maps to its place in the repository. git reports the root resolved, and a temp or
	// home directory behind a symlink would otherwise read as outside it.
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		dir = filepath.Dir(path)
	}
	rel, err := filepath.Rel(h.repoRoot, filepath.Join(dir, filepath.Base(path)))
	if err != nil || !filepath.IsLocal(rel) {
		// Outside the repository nothing is versioned, so both sides read the same bytes.
		return os.ReadFile(path)
	}
	content, err := h.driver.ReadFileAt(ctx, h.repoRoot, "", filepath.ToSlash(rel))
	if err != nil {
		return nil, err
	}
	return []byte(content), nil
}

// approvedPolicyAt is the loosening authority of the workspace at root, nil when it has
// none: version control disabled or no repository. Without one the working tree is the whole
// policy.
func approvedPolicyAt(ctx context.Context, root string, opts types.VCSOptions) (ApprovedPolicy, error) {
	res, err := vcs.Resolve(ctx, root, "", opts)
	if err != nil {
		return nil, err
	}
	if res.VCS == nil {
		return nil, nil //nolint:nilnil // no authority is a documented answer, not a failure
	}
	repoRoot, err := res.VCS.Root(ctx, root)
	if err != nil {
		//nolint:nilerr,nilnil // a workspace outside any repository has no approved state to defer to
		return nil, nil
	}
	if resolved, err := filepath.EvalSymlinks(repoRoot); err == nil {
		repoRoot = resolved
	}
	return headPolicy{workspace: root, repoRoot: repoRoot, driver: res.VCS}, nil
}

// SpawnRule returns the magus\guard.spawn rule the root magusfile registered, or nil.
func (m *Magus) SpawnRule() workspace.SpawnRule {
	if m.wsReg == nil {
		return nil
	}
	return m.wsReg.SpawnRule()
}

// ApprovedSpawnRule returns the magus\guard.spawn rule as the approved sources register it.
// Nil with no error when nothing is pending (the working tree's rule already is the
// approved one), when the workspace has no approval authority, or when the approved sources
// register no rule. An error means the approved rule could not be resolved, which is not
// the fact that there is none. The guard runs it beside SpawnRule and keeps the stricter
// answer.
//
// It loads the root magusfile a second time, reading every source it and its Buzz imports
// read through the approval authority, so call it only for a spawn.
func (m *Magus) ApprovedSpawnRule(ctx context.Context) (workspace.SpawnRule, error) {
	approved, err := approvedPolicyAt(ctx, m.ws.Root, m.ws.VCSOptions)
	if err != nil || approved == nil {
		return nil, err
	}
	pending, err := approved.Pending(ctx)
	if err != nil || len(pending) == 0 {
		return nil, err
	}
	return approvedSpawnRule(ctx, m.ws.Root, m.rootProjectPath(), m.resolver, approved, pending)
}

// ApprovedSpawnRuleAt is ApprovedSpawnRule for the workspace at root when its working tree
// does not load. Nothing in the working tree is trusted: its magus.yaml is not read, since a
// broken or edited config (a version floor this binary is below, say) may be the failure,
// and the approved sources are evaluated whether or not any of them looks pending.
func ApprovedSpawnRuleAt(ctx context.Context, root string) (workspace.SpawnRule, error) {
	approved, err := approvedPolicyAt(ctx, root, types.VCSOptions{})
	if err != nil || approved == nil {
		return nil, err
	}
	pending, err := approved.Pending(ctx)
	if err != nil {
		return nil, err
	}
	return approvedSpawnRule(ctx, root, ".", secret.New(), approved, pending)
}

// approvedSpawnRule evaluates the root magusfile at root as approved holds it. pending
// names the sources the working tree changed, so a file it deleted is still found.
func approvedSpawnRule(ctx context.Context, root, projectPath string, resolver *secret.Resolver, approved ApprovedPolicy, pending []string) (workspace.SpawnRule, error) {
	if !interp.Available() {
		return nil, nil //nolint:nilnil // without an interpreter no magusfile can register a rule
	}
	memo := map[string][]byte{}
	read := func(path string) ([]byte, error) {
		if data, ok := memo[path]; ok {
			return data, nil
		}
		data, err := approved.ReadFile(ctx, path)
		if err != nil {
			return nil, err
		}
		memo[path] = data
		return data, nil
	}
	files, err := approvedMagusfiles(root, pending, read)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	reg := workspace.NewWorkspaceRegistry()
	lctx := installWorkspaceRegistry(ctx, reg)
	lctx = secret.ContextWithResolver(lctx, resolver)
	lctx = interp.WithProjectPath(lctx, projectPath)
	lctx = interp.WithSourceReader(lctx, read)
	for _, src := range interp.Sources(root, files) {
		if _, err := interp.Parse(lctx, src); err != nil {
			return nil, fmt.Errorf("approved magusfile: %w", err)
		}
	}
	return reg.SpawnRule(), nil
}

// approvedMagusfiles is the root magusfile's files as approved holds them, in whichever
// form it holds them. The working tree only proposes candidates, so neither deleting a file
// nor adding the other form can hide one; a candidate approved lacks did not exist there.
func approvedMagusfiles(root string, pending []string, read func(string) ([]byte, error)) ([]string, error) {
	candidates, err := interp.MagusfileCandidates(root)
	if err != nil {
		return nil, err
	}
	single := candidates[0]
	if _, err := read(single); err == nil {
		return []string{single}, nil
	}
	// Pending paths hang off the resolved repository root, which root may reach through a
	// symlink; each is renamed onto root so a file named both ways is read once.
	dir := filepath.Join(root, "magusfiles")
	pendingDir := dir
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		pendingDir = filepath.Join(resolved, "magusfiles")
	}
	for _, p := range pending {
		if filepath.Dir(p) == pendingDir && filepath.Ext(p) == ".buzz" {
			candidates = append(candidates, filepath.Join(dir, filepath.Base(p)))
		}
	}
	slices.Sort(candidates)
	var files []string
	for _, f := range slices.Compact(candidates) {
		if f == single {
			continue
		}
		if _, err := read(f); err == nil {
			files = append(files, f)
		}
	}
	return files, nil
}

// rootProjectPath is the path the root project is registered under, "." when none is.
func (m *Magus) rootProjectPath() string {
	for _, p := range m.All() {
		if filepath.Clean(p.Dir) == filepath.Clean(m.ws.Root) {
			return p.Path
		}
	}
	return "."
}

// GuardPolicy is the effective set of workspace guard rules as this load sees it. Nothing
// in it runs a process, so a hook reads it on every call.
type GuardPolicy struct {
	// Digest names the rule set; "" when the working tree declares no guard rule.
	Digest string
	// Sources are the files the root magusfile's load read, which is where a spawn rule can
	// come from, each named by the git blob id of the bytes read.
	Sources    []interp.SourceFile
	ShellRules int
	SpawnRule  bool
}

// GuardPolicy describes the workspace guard rules this load registered.
//
// A spawn rule is code, so its digest covers every file its load read: an edit to any of
// them counts as a policy change while a spawn rule is registered. Shell rules are data
// and count only for themselves.
func (m *Magus) GuardPolicy() GuardPolicy {
	rules := m.ShellRules()
	spawn := m.SpawnRule() != nil
	var sources []interp.SourceFile
	if m.policyLog != nil {
		sources = m.policyLog.Files()
	}
	policy := GuardPolicy{Sources: sources, ShellRules: len(rules), SpawnRule: spawn}
	if len(rules) == 0 && !spawn {
		return policy
	}
	h := sha256.New()
	for _, r := range rules {
		fmt.Fprintf(h, "shell %q %q %q %q %q %q\n", r.Name, r.Decision, r.Program, r.Args, r.Reason, r.Dialect)
	}
	if spawn {
		for _, s := range sources {
			fmt.Fprintf(h, "spawn %s %s\n", s.Path, s.BlobID)
		}
	}
	policy.Digest = hex.EncodeToString(h.Sum(nil))
	return policy
}

// ApprovedBlobIDs maps each path to the git blob id of its approved content, "" when the
// approved state has no such file. Nil when the workspace has no approval authority. It
// reads each file through the authority, one process per file on git, so call it only
// when the answer can change what is recorded.
func (m *Magus) ApprovedBlobIDs(ctx context.Context, paths []string) map[string]string {
	approved, err := approvedPolicyAt(ctx, m.ws.Root, m.ws.VCSOptions)
	if err != nil || approved == nil {
		return nil
	}
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		data, err := approved.ReadFile(ctx, p)
		if err != nil {
			out[p] = ""
			continue
		}
		out[p] = interp.GitBlobID(data)
	}
	return out
}
