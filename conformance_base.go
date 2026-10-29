package magus

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/internal/symbols/buzzindex"
	"github.com/egladman/magus/internal/symbols/goindex"
	"github.com/egladman/magus/internal/symbols/shindex"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// noBaseIndex is why a touched file of an externally indexed language has no ceiling.
const noBaseIndex = "no in-process index; ceiling not applied"

// baseMeasureVersion keys the kept measures; bump it when measureTree measures differently.
const baseMeasureVersion = "2"

// ConformanceCeilings is head, the lens over the working tree, with c.Base's ceilings: each file
// changed since c.Base whose count of violations of a metric in c.Checks rose past its count
// there. It reads the measure MeasureConformanceBase kept and never measures one itself; a base
// with none is an error naming that step. A touched file only an external indexer reads is
// listed in Unmeasured.
func (m *Magus) ConformanceCeilings(ctx context.Context, c types.Conformance, head types.ConformanceOutput) (types.ConformanceOutput, error) {
	if err := c.Validate(); err != nil {
		return types.ConformanceOutput{}, fmt.Errorf("conformance: %w", err)
	}
	if c.Base == "" {
		return head, nil
	}
	v, commit, err := m.conformanceBaseCommit(ctx, c.Base)
	if err != nil {
		return types.ConformanceOutput{}, err
	}
	touched, err := v.ChangedFiles(ctx, m.ws.Root, c.Base)
	if err != nil {
		return types.ConformanceOutput{}, fmt.Errorf("conformance: files changed since %s: %w", c.Base, err)
	}
	plan, err := m.baseIndexPlan(ctx, m.ws.Root)
	if err != nil {
		return types.ConformanceOutput{}, err
	}
	entry, err := m.conformanceEntry(ctx, commit, plan)
	if err != nil {
		return types.ConformanceOutput{}, err
	}
	base, ok, err := readConformanceEntry(entry)
	if err != nil {
		return types.ConformanceOutput{}, err
	}
	if !ok {
		return types.ConformanceOutput{}, fmt.Errorf("conformance: no measure of base %s under this workspace's configuration, "+
			"which magus\\conformanceBase measures and `magus run conformance-base .` runs here", commit)
	}
	judged := touched[:0:0]
	for _, p := range touched {
		if lang, ok := plan.unmeasured(p); ok {
			head.Unmeasured = append(head.Unmeasured, types.ConformanceUnmeasured{File: p, Language: lang, Reason: noBaseIndex})
			continue
		}
		judged = append(judged, p)
	}
	head.Base = commit
	head.Ceilings = types.ConformanceCeilings(base, head, judged, c.Checks)
	return head, nil
}

// MeasureConformanceBase measures the merge base of base and HEAD with magus's in-process
// indexers under this workspace's configuration, and keeps it in the user cache, which every
// checkout on the machine shares, keyed by commit, policy, indexer versions and magus build. It returns the
// commit, and reuses a kept measure. It opens no workspace and starts no run.
func (m *Magus) MeasureConformanceBase(ctx context.Context, base string) (string, error) {
	v, commit, err := m.conformanceBaseCommit(ctx, base)
	if err != nil {
		return "", err
	}
	tree, err := xdg.ScratchCheckoutDir("conformance")
	if err != nil {
		return "", fmt.Errorf("conformance: %w", err)
	}
	defer os.RemoveAll(tree)
	// Keyed by the indexers as head runs them, so ConformanceCeilings finds the entry: a Go
	// indexer's version is the toolchain its directory resolves.
	head, err := m.baseIndexPlan(ctx, m.ws.Root)
	if err != nil {
		return "", err
	}
	entry, err := m.conformanceEntry(ctx, commit, head)
	if err != nil {
		return "", err
	}
	if _, ok, err := readConformanceEntry(entry); err != nil || ok {
		return commit, err
	}
	if err := v.ExportRevision(ctx, m.ws.Root, commit, tree); err != nil {
		return "", fmt.Errorf("conformance: export %s: %w", commit, err)
	}
	plan, err := m.baseIndexPlan(ctx, tree)
	if err != nil {
		return "", err
	}
	out, err := m.measureTree(ctx, tree, plan)
	if err != nil {
		return "", fmt.Errorf("conformance: measure %s: %w", commit, err)
	}
	line, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("conformance: encode %s: %w", commit, err)
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
		return "", fmt.Errorf("conformance: %w", err)
	}
	if err := file.WriteFileAtomic(entry, append(line, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("conformance: keep %s: %w", commit, err)
	}
	return commit, nil
}

// conformanceBaseCommit is the commit base and HEAD fork from, the one ChangedFiles diffs
// against, so work merged upstream since is neither counted nor touched.
func (m *Magus) conformanceBaseCommit(ctx context.Context, base string) (types.VCSDriver, string, error) {
	res, err := vcs.Resolve(ctx, m.ws.Root, "", m.ws.VCSOptions)
	if err != nil || res.VCS == nil {
		return nil, "", types.DiagnosticErrorf(types.VCSCapabilityMissing,
			"version control is disabled, so base %q names no revision", base)
	}
	merger, ok := res.VCS.(types.TreeMerger)
	if !ok {
		return nil, "", types.DiagnosticErrorf(types.VCSCapabilityMissing,
			"this version control finds no merge base, so base %q names no fork point", base)
	}
	commit, ok, err := merger.MergeBase(ctx, m.ws.Root, base, "HEAD")
	if err != nil {
		return nil, "", fmt.Errorf("conformance: merge base of %q and HEAD: %w", base, err)
	}
	if !ok {
		return nil, "", fmt.Errorf("conformance: %q and HEAD share no single merge base to count from", base)
	}
	return res.VCS, commit, nil
}

// readConformanceEntry is the measure kept at entry, and false when none is.
func readConformanceEntry(entry string) (types.ConformanceOutput, bool, error) {
	raw, err := os.ReadFile(entry)
	if errors.Is(err, os.ErrNotExist) {
		return types.ConformanceOutput{}, false, nil
	}
	if err != nil {
		return types.ConformanceOutput{}, false, fmt.Errorf("conformance: read %s: %w", entry, err)
	}
	var out types.ConformanceOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return types.ConformanceOutput{}, false, fmt.Errorf("conformance: %s does not parse, so delete it and measure the base again: %w", entry, err)
	}
	return out, true, nil
}

// conformanceEntry is the cache file of commit's measure under this workspace's policy and
// plan's indexers, as measured by this magus build.
func (m *Magus) conformanceEntry(ctx context.Context, commit string, plan baseIndexPlan) (string, error) {
	policy, err := json.Marshal(m.cfg.Knowledge.Conformance)
	if err != nil {
		return "", fmt.Errorf("conformance: digest the policy: %w", err)
	}
	h := sha256.New()
	build := types.MagusBuildFromContext(ctx)
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00", baseMeasureVersion, build.Version, build.Commit)
	fmt.Fprintf(h, "%s", policy)
	for _, ix := range plan.indexes {
		fmt.Fprintf(h, "\x00%s\x00%s\x00%s", ix.project, ix.language, ix.ix.Version())
	}
	root, err := xdg.CacheDir()
	if err != nil {
		return "", fmt.Errorf("conformance: %w", err)
	}
	return filepath.Join(root, "magus", "conformance", fmt.Sprintf("%s-%x.jsonl", commit, h.Sum(nil))), nil
}

// baseIndex is one in-process symbol index of a project, over the base tree.
type baseIndex struct {
	project, language string
	ix                symbols.UnitIndexer
}

// baseExternal is a project's index that only an external indexer writes: its language and
// the file extensions the language owns.
type baseExternal struct {
	dir, language string
	extensions    []string
}

// baseIndexPlan is the indexes this workspace declares, split into those magus runs in
// process, over the base tree, and those it cannot.
type baseIndexPlan struct {
	indexes  []baseIndex
	external []baseExternal
}

// unmeasured reports whether file is one only an external index reads, and its language.
func (p baseIndexPlan) unmeasured(file string) (string, bool) {
	for _, e := range p.external {
		inside := e.dir == "." || file == e.dir || strings.HasPrefix(file, e.dir+"/")
		if inside && slices.Contains(e.extensions, path.Ext(file)) {
			return e.language, true
		}
	}
	return "", false
}

// baseIndexPlan reads which index each project's symbol-capable spells declare, with head's
// configuration, over tree.
func (m *Magus) baseIndexPlan(ctx context.Context, tree string) (baseIndexPlan, error) {
	projects, err := m.ListProjects(ctx)
	if err != nil {
		return baseIndexPlan{}, fmt.Errorf("conformance: %w", err)
	}
	all, err := ListSpells(ctx)
	if err != nil {
		return baseIndexPlan{}, fmt.Errorf("conformance: %w", err)
	}
	byName := map[string]types.Spell{}
	for _, sp := range all {
		byName[sp.Name] = sp
	}
	var plan baseIndexPlan
	for _, p := range projects.Projects {
		bound := p.Spells
		if len(bound) == 0 && p.Spell != "" {
			bound = []string{p.Spell}
		}
		for _, name := range bound {
			si := spellIndexer(name)
			if si == nil || byName[name].SymbolFormat == "" || !m.cfg.Spells.RunsSymbolIndexer(name, si) {
				continue
			}
			ix := inProcessIndexer(si, filepath.Join(tree, filepath.FromSlash(p.Path)), tree)
			if ix == nil {
				plan.external = append(plan.external, baseExternal{dir: p.Path, language: byName[name].Language, extensions: byName[name].Extensions})
				continue
			}
			plan.indexes = append(plan.indexes, baseIndex{project: p.Path, language: byName[name].Language, ix: ix})
		}
	}
	return plan, nil
}

// inProcessIndexer is the in-process indexer si's format names over dir, nil for an external one.
func inProcessIndexer(si *spells.SymbolIndexer, dir, root string) symbols.UnitIndexer {
	switch si.Format {
	case spells.SymbolFormatGoPackages:
		patterns := slices.Clone(si.Command.Args)
		if len(patterns) == 0 {
			patterns = []string{"."}
		}
		return &goindex.Indexer{Dir: dir, Patterns: patterns, Builds: si.Envs}
	case spells.SymbolFormatBuzzModule:
		return &buzzindex.Indexer{Dir: dir, WorkspaceRoot: root}
	case spells.SymbolFormatShellScript:
		return &shindex.Indexer{Dir: dir, WorkspaceRoot: root}
	default:
		return nil
	}
}

// indexBase writes b's index of tree to pack and reads its symbols back. A file the index
// leaves out fails it, naming the file.
func indexBase(ctx context.Context, tree, pack string, b baseIndex) ([]types.KnowledgeSymbol, error) {
	if err := os.MkdirAll(filepath.Dir(pack), 0o700); err != nil {
		return nil, err
	}
	var res symbols.UpdateResult
	res, err := symbols.UpdatePack(ctx, pack, b.ix)
	if err != nil {
		return nil, err
	}
	if len(res.Gaps) > 0 {
		return nil, fmt.Errorf("cannot read %s", strings.Join(res.Gaps, ", "))
	}
	idx, err := symbols.ReadIndex(pack, true)
	if err != nil {
		return nil, err
	}
	parsed := symbols.ParseDecoded(ctx, idx, b.project, b.language)
	symbols.FingerprintBodies(tree, parsed)
	return parsed, nil
}

// measureTree indexes tree with plan's in-process indexers and measures it under this
// workspace's policy. A source an indexer cannot read fails, naming it.
func (m *Magus) measureTree(ctx context.Context, tree string, plan baseIndexPlan) (types.ConformanceOutput, error) {
	syms := map[string][]types.KnowledgeSymbol{}
	for i, b := range plan.indexes {
		parsed, err := indexBase(ctx, tree, filepath.Join(tree, ".magus", "conformance", fmt.Sprintf("%d.pack", i)), b)
		if err != nil {
			return types.ConformanceOutput{}, fmt.Errorf("%s %s index: %w", b.project, b.language, err)
		}
		syms[b.project] = append(syms[b.project], parsed...)
	}
	graph, err := m.TargetGraph(ctx)
	if err != nil {
		return types.ConformanceOutput{}, err
	}
	g := knowledge.NewGraph()
	// The tree walk gives every file its node and line count, as the head graph's does, so a
	// file no index defines a symbol in is measured too.
	in := knowledge.Inputs{Graph: graph, Symbols: syms, Root: tree, Tree: knowledge.WalkTree(tree)}
	for _, sh := range knowledge.AssembleShards(in) {
		g.Merge(sh.Nodes, sh.Edges)
	}
	opts, err := m.ConformanceOptions()
	if err != nil {
		return types.ConformanceOutput{}, err
	}
	if opts.Generated, err = m.declaredOutputs(ctx, g); err != nil {
		return types.ConformanceOutput{}, err
	}
	out := g.Conformance(opts)
	out.Definition = types.ConformanceDefinition
	if out.Answer.Verdict == types.VerdictUnknown {
		return types.ConformanceOutput{}, errors.New("the in-process indexers read no symbols, so no ceiling can be judged against it")
	}
	return out, nil
}
