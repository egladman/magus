package magus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/secret"
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
	// Pending reports whether any workspace .buzz source may differ from its approved
	// content. False lets the caller skip evaluating the approved sources at all.
	Pending(ctx context.Context) (bool, error)
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
	reader    types.RevisionFileReader
}

func (h headPolicy) Pending(ctx context.Context) (bool, error) {
	dirty, err := h.driver.DirtyFiles(ctx, h.workspace, nil)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(dirty, func(p string) bool { return strings.HasSuffix(p, ".buzz") }), nil
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
	content, err := h.reader.ReadFileAt(ctx, h.repoRoot, "", filepath.ToSlash(rel))
	if err != nil {
		return nil, err
	}
	return []byte(content), nil
}

// approvedPolicy is this workspace's loosening authority, nil when it has none: version
// control disabled, or a backend that cannot read a file at a revision. Without one the
// working tree is the whole policy.
func (m *Magus) approvedPolicy(ctx context.Context) ApprovedPolicy {
	res, err := vcs.Resolve(ctx, m.ws.Root, "", m.ws.VCSOptions)
	if err != nil || res.VCS == nil {
		return nil
	}
	reader, ok := res.VCS.(types.RevisionFileReader)
	if !ok {
		return nil
	}
	repoRoot, err := res.VCS.Root(ctx, m.ws.Root)
	if err != nil {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(repoRoot); err == nil {
		repoRoot = resolved
	}
	return headPolicy{workspace: m.ws.Root, repoRoot: repoRoot, driver: res.VCS, reader: reader}
}

// SpawnRule returns the magus\guard.spawn rule the root magusfile registered, or nil.
func (m *Magus) SpawnRule() workspace.SpawnRule {
	if m.wsReg == nil {
		return nil
	}
	return m.wsReg.SpawnRule()
}

// ApprovedSpawnRule returns the magus\guard.spawn rule as the approved sources register it.
// Nil when nothing is pending (the working tree's rule already is the approved one), when
// the workspace has no approval authority, or when the approved sources register no rule
// or fail to load. The guard runs it beside SpawnRule and keeps the stricter answer.
//
// It loads the root magusfile a second time, reading every source it and its Buzz imports
// read through the approval authority, so call it only for a spawn.
func (m *Magus) ApprovedSpawnRule(ctx context.Context) workspace.SpawnRule {
	approved := m.approvedPolicy(ctx)
	if approved == nil {
		return nil
	}
	if pending, err := approved.Pending(ctx); err != nil || !pending {
		return nil
	}
	return m.spawnRuleFrom(ctx, approved)
}

// spawnRuleFrom evaluates the root magusfile as approved holds it. A magusfiles/ file the
// approved state lacks is left out rather than failing the load: it did not exist there.
func (m *Magus) spawnRuleFrom(ctx context.Context, approved ApprovedPolicy) workspace.SpawnRule {
	if !interp.Available() {
		return nil
	}
	srcs, err := interp.FindAll(m.ws.Root)
	if err != nil {
		return nil
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
	reg := workspace.NewWorkspaceRegistry()
	lctx := installWorkspaceRegistry(ctx, reg)
	lctx = secret.ContextWithResolver(lctx, m.resolver)
	lctx = interp.WithProjectPath(lctx, m.rootProjectPath())
	lctx = interp.WithSourceReader(lctx, read)
	for _, src := range srcs {
		var files []string
		for _, f := range src.Files {
			if _, err := read(f); err == nil {
				files = append(files, f)
			}
		}
		if len(files) == 0 {
			continue
		}
		if _, err := interp.Parse(lctx, &interp.Source{Dir: src.Dir, Files: files, Engine: src.Engine}); err != nil {
			return nil
		}
	}
	return reg.SpawnRule()
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
	approved := m.approvedPolicy(ctx)
	if approved == nil {
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
