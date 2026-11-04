//go:build !wasm

package std

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// MagusWorkspaceOpen backs magus\workspace(dir): the workspace on ctx, and
// the checkout whose branch its job facts read, the root when dir is "".
func MagusWorkspaceOpen(ctx context.Context, dir string) (types.MagusWorkspace, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		if job.HasSnapshot(ctx) {
			return types.MagusWorkspace{Dir: dir}, nil
		}
		return types.MagusWorkspace{}, errorNoWorkspace("workspace")
	}
	if dir == "" {
		dir = ws.Root()
	}
	return types.MagusWorkspace{Root: ws.Root(), Dir: dir}, nil
}

// MagusGraphOpen backs magus\graph(): the symbol index the graph's answers
// come from, which every Graph method holds the index to.
func MagusGraphOpen(ctx context.Context, languages []string) (types.SymbolIndexDigest, error) {
	return MagusSymbolIndexDigest(ctx, languages)
}

// MagusHistoryOpen backs magus\history(opts): the window its metrics read.
func MagusHistoryOpen(ctx context.Context, opts types.HistoryMetricsOptions) (types.History, error) {
	if types.WorkspaceFromContext(ctx) == nil {
		return types.History{}, errorNoWorkspace("history")
	}
	return types.History(opts), nil
}

// MagusSessionOpen backs magus\session(opts): which session's trail a read
// covers. The window is checked when it is read.
func MagusSessionOpen(ctx context.Context, opts types.TrailReadOptions) (types.Session, error) {
	if types.WorkspaceFromContext(ctx) == nil {
		return types.Session{}, errorNoWorkspace("session")
	}
	return types.Session(opts), nil
}

// MagusJobOpen backs magus\job(id): the stored row. An id no row carries
// raises, since a handle on a row that is not there answers nothing.
func MagusJobOpen(ctx context.Context, id string) (types.Job, error) {
	list, err := MagusListJob(ctx)
	if err != nil {
		return types.Job{}, err
	}
	i := slices.IndexFunc(list.Jobs, func(j types.Job) bool { return j.ID == id })
	if i < 0 {
		return types.Job{}, fmt.Errorf("magus\\job: no row has id %q, and `magus job ls` lists the ids", id)
	}
	return list.Jobs[i], nil
}

// MagusChangeOpen backs magus\change(base, opts): base "" is the configured
// base, as `magus affected` reads it. It records the tree it was built on.
func MagusChangeOpen(ctx context.Context, base string, opts types.ChangeOptions) (types.Change, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.Change{}, errorNoWorkspace("change")
	}
	at, files, err := changeTree(ctx, ws)
	if err != nil {
		return types.Change{}, fmt.Errorf("magus\\change: %w", err)
	}
	return types.Change{Base: base, Rev: opts.Rev, Patch: opts.Patch, At: at, Files: files}, nil
}

// changeTree is the checkpoint of ws's tree and a digest of each dirty path.
func changeTree(ctx context.Context, ws types.WorkspaceRepository) (types.VCSCheckpoint, map[string]string, error) {
	res, err := vcs.Resolve(ctx, ws.Root(), "", ws.VCSOptions())
	if err != nil {
		return types.VCSCheckpoint{}, nil, err
	}
	at, err := vcs.Checkpoint(ctx, ws.Root(), res, false)
	if err != nil {
		return types.VCSCheckpoint{}, nil, err
	}
	paths, err := res.VCS.DirtyFiles(ctx, ws.Root(), nil)
	if err != nil {
		return types.VCSCheckpoint{}, nil, err
	}
	return at, digestFiles(ws.Root(), paths), nil
}

// digestFiles maps each path to a digest of its content, "" for one that
// no longer exists.
func digestFiles(root string, paths []string) map[string]string {
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			out[p] = ""
			continue
		}
		sum := sha256.Sum256(b)
		out[p] = hex.EncodeToString(sum[:])
	}
	return out
}

// heldChange refuses a Change whose tree moved since it was built, naming
// the moved paths, and returns the workspace it was checked against.
func heldChange(ctx context.Context, c types.Change, member string) (types.WorkspaceRepository, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil, errorNoWorkspace("Change." + member)
	}
	if c.At.Revision == "" {
		return nil, fmt.Errorf("magus\\Change.%s: this Change records no tree, so build it with magus\\change(...)", member)
	}
	at, files, err := changeTree(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("magus\\Change.%s: %w", member, err)
	}
	if moved := movedPaths(c, at, files); len(moved) > 0 {
		return nil, fmt.Errorf("magus\\Change.%s: the tree moved since magus\\change(...) built this Change: %s, so build a new one",
			member, strings.Join(moved, ", "))
	}
	return ws, nil
}

// movedPaths names what differs between the tree c was built on and now:
// each path whose content digest moved, and a moved revision.
func movedPaths(c types.Change, at types.VCSCheckpoint, files map[string]string) []string {
	var moved []string
	if c.At.Revision != at.Revision {
		moved = append(moved, fmt.Sprintf("HEAD %s -> %s", shortRev(c.At.Revision), shortRev(at.Revision)))
	}
	for _, p := range slices.Sorted(maps.Keys(unionKeys(c.Files, files))) {
		then, wasDirty := c.Files[p]
		now, isDirty := files[p]
		if then != now || wasDirty != isDirty {
			moved = append(moved, p)
		}
	}
	if len(moved) == 0 && (c.At.PatchDigest != at.PatchDigest || c.At.UntrackedDigest != at.UntrackedDigest) {
		moved = append(moved, "the uncommitted patch")
	}
	return moved
}

func unionKeys(a, b map[string]string) map[string]bool {
	out := make(map[string]bool, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

func shortRev(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// changeBase is c's base, the configured one when c names none.
func changeBase(ctx context.Context, ws types.WorkspaceRepository, c types.Change) (string, error) {
	if c.Base != "" {
		return c.Base, nil
	}
	res, err := vcs.Resolve(ctx, ws.Root(), "", ws.VCSOptions())
	if err != nil {
		return "", err
	}
	return res.Base, nil
}

// MagusChangeTier is Change.tier(target, opts): the tier magus proves for
// the change against target's gate, measured from c's base.
func MagusChangeTier(ctx context.Context, c types.Change, target string, opts types.TierOptions) (types.TierReport, error) {
	if _, err := heldChange(ctx, c, "tier"); err != nil {
		return types.TierReport{}, err
	}
	return MagusRiskAssess(ctx, target, tierOptions(c, opts))
}

// tierOptions is opts measured from c's base. A TierOptions record always
// carries paths, so an empty list named none: the VCS diff, not no change.
func tierOptions(c types.Change, opts types.TierOptions) types.TierOptions {
	opts.Base = c.Base
	if len(opts.Paths) == 0 {
		opts.Paths = nil
	}
	return opts
}

// MagusChangeDiff is Change.diff(opts): the review of c, its rev or patch
// standing in for the working tree when opts names neither.
func MagusChangeDiff(ctx context.Context, c types.Change, opts types.MagusDiffOptions) (types.Diff, error) {
	if _, err := heldChange(ctx, c, "diff"); err != nil {
		return types.Diff{}, err
	}
	if opts.Rev == "" && opts.Patch == "" {
		opts.Rev, opts.Patch = c.Rev, c.Patch
	}
	return MagusDiff(ctx, opts)
}

// MagusChangeImpact is Change.impact(opts): `magus affected --impact`
// against c's base.
func MagusChangeImpact(ctx context.Context, c types.Change, opts types.MagusCommandOptions) (types.ImpactResult, error) {
	if _, err := heldChange(ctx, c, "impact"); err != nil {
		return types.ImpactResult{}, err
	}
	return MagusImpact(ctx, c.Base, opts)
}

// MagusChangeAffected is Change.affected(): the affected set against c's base.
func MagusChangeAffected(ctx context.Context, c types.Change) (types.AffectedResult, error) {
	if _, err := heldChange(ctx, c, "affected"); err != nil {
		return types.AffectedResult{}, err
	}
	return MagusAffected(ctx, c.Base)
}

// MagusChangeConformance is Change.conformance(checks): the conformance
// lens, with the ceilings checks crossed since c's base when checks is set.
func MagusChangeConformance(ctx context.Context, c types.Change, checks []string) (types.ConformanceReport, error) {
	ws, err := heldChange(ctx, c, "conformance")
	if err != nil {
		return types.ConformanceReport{}, err
	}
	if len(checks) == 0 {
		return MagusConformance(ctx, nil, "")
	}
	base, err := changeBase(ctx, ws, c)
	if err != nil {
		return types.ConformanceReport{}, fmt.Errorf("magus\\Change.conformance: %w", err)
	}
	return MagusConformance(ctx, checks, base)
}

// MagusChangeMergeBase is Change.mergeBase(): measures the merge base of
// c's base and HEAD for conformance's ceilings and returns the commit.
func MagusChangeMergeBase(ctx context.Context, c types.Change) (string, error) {
	ws, err := heldChange(ctx, c, "mergeBase")
	if err != nil {
		return "", err
	}
	base, err := changeBase(ctx, ws, c)
	if err != nil {
		return "", fmt.Errorf("magus\\Change.mergeBase: %w", err)
	}
	return MagusConformanceBase(ctx, base)
}

// heldGraph refuses a Graph whose symbol index moved since magus\graph()
// built it, naming the projects whose index came or went.
func heldGraph(ctx context.Context, g types.SymbolIndexDigest, member string) error {
	now, err := MagusSymbolIndexDigest(ctx, g.Languages)
	if err != nil {
		return err
	}
	if now.Digest == g.Digest {
		return nil
	}
	moved := symmetricDifference(g.Projects, now.Projects)
	if len(moved) == 0 {
		moved = now.Projects
	}
	return fmt.Errorf("magus\\Graph.%s: the symbol index moved since magus\\graph() built this Graph, in %s, so build a new one",
		member, strings.Join(moved, ", "))
}

func symmetricDifference(a, b []string) []string {
	var out []string
	for _, p := range a {
		if !slices.Contains(b, p) {
			out = append(out, p)
		}
	}
	for _, p := range b {
		if !slices.Contains(a, p) {
			out = append(out, p)
		}
	}
	return out
}

// graphMethod holds g to its index, then answers read.
func graphMethod[T any](ctx context.Context, g types.SymbolIndexDigest, member string, read func() (T, error)) (T, error) {
	if err := heldGraph(ctx, g, member); err != nil {
		var zero T
		return zero, err
	}
	return read()
}

// MagusGraphQuery answers Graph.query.
// It raises once the index g holds moved.
func MagusGraphQuery(ctx context.Context, g types.SymbolIndexDigest, query string, opts types.KnowledgeQueryOptions) (types.KnowledgeQueryOutput, error) {
	return graphMethod(ctx, g, "query", func() (types.KnowledgeQueryOutput, error) { return MagusQuery(ctx, query, opts) })
}

// MagusGraphExplain answers Graph.explain.
// It raises once the index g holds moved.
func MagusGraphExplain(ctx context.Context, g types.SymbolIndexDigest, node string) (types.KnowledgeExplainOutput, error) {
	return graphMethod(ctx, g, "explain", func() (types.KnowledgeExplainOutput, error) { return MagusExplain(ctx, node) })
}

// MagusGraphPath answers Graph.path.
// It raises once the index g holds moved.
func MagusGraphPath(ctx context.Context, g types.SymbolIndexDigest, node, to string, opts types.KnowledgePathOptions) (types.KnowledgePathOutput, error) {
	return graphMethod(ctx, g, "path", func() (types.KnowledgePathOutput, error) { return MagusPath(ctx, node, to, opts) })
}

// MagusGraphRefs answers Graph.refs.
// It raises once the index g holds moved.
func MagusGraphRefs(ctx context.Context, g types.SymbolIndexDigest, symbol string, opts types.KnowledgeRefsOptions) (types.KnowledgeRefsOutput, error) {
	return graphMethod(ctx, g, "refs", func() (types.KnowledgeRefsOutput, error) { return MagusRefs(ctx, symbol, opts) })
}

// MagusGraphStats answers Graph.stats.
// It raises once the index g holds moved.
func MagusGraphStats(ctx context.Context, g types.SymbolIndexDigest, kind string) (types.KnowledgeStats, error) {
	return graphMethod(ctx, g, "stats", func() (types.KnowledgeStats, error) { return MagusStats(ctx, kind) })
}

// MagusGraphNeighborhood answers Graph.neighborhood.
// It raises once the index g holds moved.
func MagusGraphNeighborhood(ctx context.Context, g types.SymbolIndexDigest, focus string, opts types.KnowledgeNeighborhoodOptions) (types.KnowledgeNeighborhoodOutput, error) {
	return graphMethod(ctx, g, "neighborhood", func() (types.KnowledgeNeighborhoodOutput, error) { return MagusNeighborhood(ctx, focus, opts) })
}

// MagusGraphDir answers Graph.dir.
// It raises once the index g holds moved.
func MagusGraphDir(ctx context.Context, g types.SymbolIndexDigest, path string) (types.Dir, error) {
	return graphMethod(ctx, g, "dir", func() (types.Dir, error) { return MagusDir(ctx, path) })
}

// MagusGraphDirs answers Graph.dirs.
// It raises once the index g holds moved.
func MagusGraphDirs(ctx context.Context, g types.SymbolIndexDigest, glob string, opts types.DirsOptions) ([]types.Dir, error) {
	return graphMethod(ctx, g, "dirs", func() ([]types.Dir, error) { return MagusDirs(ctx, glob, opts) })
}

// MagusGraphLayer answers Graph.layer.
// It raises once the index g holds moved.
func MagusGraphLayer(ctx context.Context, g types.SymbolIndexDigest, name string) (types.Layer, error) {
	return graphMethod(ctx, g, "layer", func() (types.Layer, error) { return MagusLayer(ctx, name) })
}

// MagusGraphProjectGraph answers Graph.projectGraph.
// It raises once the index g holds moved.
func MagusGraphProjectGraph(ctx context.Context, g types.SymbolIndexDigest) (types.ProjectGraph, error) {
	return graphMethod(ctx, g, "projectGraph", func() (types.ProjectGraph, error) { return MagusProjectGraph(ctx) })
}

// MagusGraphImports answers Graph.imports.
// It raises once the index g holds moved.
func MagusGraphImports(ctx context.Context, g types.SymbolIndexDigest) (types.ImportGraph, error) {
	return graphMethod(ctx, g, "imports", func() (types.ImportGraph, error) { return MagusImportGraph(ctx) })
}

// MagusGraphSymbols answers Graph.symbols.
// It raises once the index g holds moved.
func MagusGraphSymbols(ctx context.Context, g types.SymbolIndexDigest) (types.Symbols, error) {
	return graphMethod(ctx, g, "symbols", func() (types.Symbols, error) { return MagusSymbols(ctx, g.Languages) })
}

// MagusGraphPrecedents answers Graph.precedents.
// It raises once the index g holds moved.
func MagusGraphPrecedents(ctx context.Context, g types.SymbolIndexDigest) (types.Holds, error) {
	return graphMethod(ctx, g, "precedents", func() (types.Holds, error) { return MagusPrecedents(ctx, g.Languages) })
}

// MagusGraphUnreferenced answers Graph.unreferenced.
// It raises once the index g holds moved.
func MagusGraphUnreferenced(ctx context.Context, g types.SymbolIndexDigest) (types.Unreferenced, error) {
	return graphMethod(ctx, g, "unreferenced", func() (types.Unreferenced, error) { return MagusUnreferenced(ctx) })
}

// MagusGraphDuplication answers Graph.duplication.
// It raises once the index g holds moved.
func MagusGraphDuplication(ctx context.Context, g types.SymbolIndexDigest) (types.Duplication, error) {
	return graphMethod(ctx, g, "duplication", func() (types.Duplication, error) { return MagusDuplication(ctx) })
}

// MagusHistoryHotspots answers History.hotspots.
// It reads the git window h holds.
func MagusHistoryHotspots(ctx context.Context, h types.History) (types.Hotspots, error) {
	return MagusHotspots(ctx, h.Options())
}

// MagusHistoryAffinity answers History.affinity.
// It reads the git window h holds.
func MagusHistoryAffinity(ctx context.Context, h types.History) (types.Affinity, error) {
	return MagusAffinity(ctx, h.Options())
}

// MagusHistoryOwnership answers History.ownership.
// It reads the git window h holds.
func MagusHistoryOwnership(ctx context.Context, h types.History) (types.Ownership, error) {
	return MagusOwnership(ctx, h.Options())
}

// MagusHistoryTrend answers History.trend.
// It reads the git window h holds.
func MagusHistoryTrend(ctx context.Context, h types.History) (types.Trend, error) {
	return MagusTrend(ctx, h.Options())
}

// MagusSessionTrail answers Session.trail.
// It reads this repository's trail store.
func MagusSessionTrail(ctx context.Context, s types.Session) (types.Trail, error) {
	return MagusTrailRead(ctx, s.TrailOptions())
}

// MagusSessionMarks is Session.marks: every mark in the repository.
func MagusSessionMarks(ctx context.Context, _ types.Session) ([]types.TrailMark, error) {
	return MagusTrailMarks(ctx)
}

// MagusSessionMark answers Session.mark.
// It reads this repository's trail store.
func MagusSessionMark(ctx context.Context, _ types.Session, mark map[string]any) (types.TrailMark, error) {
	return MagusTrailMark(ctx, mark)
}

// MagusWorkspaceJobs answers Workspace.jobs.
// It reads the workspace on ctx and raises MGS1022 outside one.
func MagusWorkspaceJobs(ctx context.Context, _ types.MagusWorkspace) (types.JobList, error) {
	return MagusListJob(ctx)
}

// MagusWorkspacePutJob answers Workspace.putJob.
// It reads the workspace on ctx and raises MGS1022 outside one.
func MagusWorkspacePutJob(ctx context.Context, _ types.MagusWorkspace, id string, opts map[string]any) (types.Job, error) {
	put, err := decodeJobPut(opts)
	if err != nil {
		return types.Job{}, err
	}
	return MagusPutJob(ctx, id, put)
}

// MagusWorkspaceClearJobs answers Workspace.clearJobs.
// It reads the workspace on ctx and raises MGS1022 outside one.
func MagusWorkspaceClearJobs(ctx context.Context, _ types.MagusWorkspace) (int, error) {
	return MagusClearJob(ctx)
}

// MagusWorkspaceForkFacts is Workspace.forkFacts, about w's checkout.
func MagusWorkspaceForkFacts(ctx context.Context, w types.MagusWorkspace) (types.JobForkFacts, error) {
	return MagusForkFactsJob(ctx, w.Dir)
}

// MagusWorkspaceUnmerged is Workspace.unmerged, about w's checkout.
func MagusWorkspaceUnmerged(ctx context.Context, w types.MagusWorkspace) (types.JobUnmergedReport, error) {
	return MagusUnmergedJob(ctx, w.Dir)
}

// MagusWorkspaceTierOutcomes answers Workspace.tierOutcomes.
// It reads the workspace on ctx and raises MGS1022 outside one.
func MagusWorkspaceTierOutcomes(ctx context.Context, _ types.MagusWorkspace) (types.ModelTiers, error) {
	return MagusModelTiers(ctx)
}

// MagusWorkspaceGrouping answers Workspace.grouping.
// It reads the workspace on ctx and raises MGS1022 outside one.
func MagusWorkspaceGrouping(ctx context.Context, _ types.MagusWorkspace) (types.Grouping, error) {
	return MagusGrouping(ctx)
}

// MagusWorkspaceVolatility answers Workspace.volatility.
// It reads the workspace on ctx and raises MGS1022 outside one.
func MagusWorkspaceVolatility(ctx context.Context, _ types.MagusWorkspace) (types.Volatility, error) {
	return MagusVolatility(ctx)
}

// MagusWorkspaceAttention answers Workspace.attention.
// It reads the workspace on ctx and raises MGS1022 outside one.
func MagusWorkspaceAttention(ctx context.Context, _ types.MagusWorkspace, args []string, opts types.MagusCommandOptions) (types.AttentionList, error) {
	return MagusAttention(ctx, args, opts)
}

// MagusWorkspaceFileDiskAttention answers Workspace.fileDiskAttention.
// It reads the workspace on ctx and raises MGS1022 outside one.
func MagusWorkspaceFileDiskAttention(ctx context.Context, _ types.MagusWorkspace, message string) error {
	return MagusFileDiskAttention(ctx, message)
}

// MagusWorkspaceDiskReclaim answers Workspace.diskReclaim.
// It reads the workspace on ctx and raises MGS1022 outside one.
func MagusWorkspaceDiskReclaim(ctx context.Context, _ types.MagusWorkspace) (types.DiskReclaim, error) {
	return MagusDiskReclaim(ctx)
}

// MagusWorkspaceBustCache answers Workspace.bustCache.
// It reads the workspace on ctx and raises MGS1022 outside one.
func MagusWorkspaceBustCache(ctx context.Context, _ types.MagusWorkspace, projectPath string) error {
	return MagusBustCache(ctx, projectPath)
}

// MagusWorkspaceDiagnoseDrift answers Workspace.diagnoseDrift.
// It reads the workspace on ctx and raises MGS1022 outside one.
func MagusWorkspaceDiagnoseDrift(ctx context.Context, _ types.MagusWorkspace, outputs, inputs []string) (types.DriftResult, error) {
	return MagusDiagnoseDrift(ctx, outputs, inputs)
}

// SkillCatalog answers Workspace.skills. internal/agent holds the catalog
// and imports std, so the bindings set it.
var SkillCatalog func(ctx context.Context, opts types.SkillsOptions) ([]types.Skill, error)

// MagusWorkspaceSkills answers Workspace.skills.
// It reads the workspace on ctx and raises MGS1022 outside one.
func MagusWorkspaceSkills(ctx context.Context, _ types.MagusWorkspace, opts types.SkillsOptions) ([]types.Skill, error) {
	if SkillCatalog == nil {
		return nil, errors.New("magus\\Workspace.skills: this build wires no skill catalog")
	}
	return SkillCatalog(ctx, opts)
}

// MagusJobExit is Job.exit(result): files result, or no_return when it is
// empty.
func MagusJobExit(ctx context.Context, j types.Job, result map[string]any) (types.Job, error) {
	if len(result) == 0 {
		return MagusAbandonJob(ctx, j.ID)
	}
	decoded, err := decodeJobResult(result)
	if err != nil {
		return types.Job{}, err
	}
	return MagusExitJob(ctx, j.ID, decoded)
}

// MagusJobWait is Job.wait(result): verifies result, or the one exit filed
// when it is empty.
func MagusJobWait(ctx context.Context, j types.Job, result map[string]any) (types.JobStatus, error) {
	if len(result) == 0 {
		return MagusWaitJob(ctx, j.ID, nil)
	}
	decoded, err := decodeJobResult(result)
	if err != nil {
		return types.JobStatus{}, err
	}
	return MagusWaitJob(ctx, j.ID, &decoded)
}

// decodeJobPut reads a putJob opts map through the store's own decoder.
func decodeJobPut(opts map[string]any) (types.JobPut, error) {
	return job.DecodePut(opts)
}

// decodeJobResult reads a result map through the strict, versioned decoder
// a JSON result uses, so an unknown field is refused here too.
func decodeJobResult(result map[string]any) (types.JobResult, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return types.JobResult{}, fmt.Errorf("magus\\Job: encode result: %w", err)
	}
	return job.DecodeResult(bytes.NewReader(raw))
}

// MagusJobRegister is Job.register(): takes the job in this checkout.
func MagusJobRegister(ctx context.Context, j types.Job) (types.JobRegistration, error) {
	row, advice, err := MagusRegisterJob(ctx, j.ID)
	if err != nil {
		return types.JobRegistration{}, err
	}
	return types.JobRegistration{Job: row, Advice: advice}, nil
}
