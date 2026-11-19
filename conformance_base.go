package magus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/internal/symbols/buzzindex"
	"github.com/egladman/magus/internal/symbols/goindex"
	"github.com/egladman/magus/internal/symbols/shindex"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// noBaseIndex is why a touched file of an externally indexed language has no base measure.
const noBaseIndex = "no in-process index, so the base holds no measure"

// projectNewAtHead is why a file of a project the base lacks has no base measure.
const projectNewAtHead = "its project is new at head, so the base holds no measure"

// baseMeasureVersion keys the kept measures; replace it when measureTree measures differently.
// Replace it too when the kept entry's JSON shape changes: an old entry decodes
// empty and reads as a base of zero.
// It replaces the magus build in the key: a gate measures under one
// binary and lint reads under the checkout's own.
const baseMeasureVersion = "magus.base-measure/v1alpha1"

// ConformanceBase is the kept measure of the merge base of base and HEAD, with the files changed
// since it: a touched file only an external indexer reads, or inside a project the base lacks,
// is listed in Unmeasured and not in Touched. It reads the measure MeasureConformanceBase kept
// and never measures one itself; a base with none is an error naming that step.
func (m *Magus) ConformanceBase(ctx context.Context, base string) (types.ConformanceBase, error) {
	v, commit, err := m.conformanceBaseCommit(ctx, base)
	if err != nil {
		return types.ConformanceBase{}, err
	}
	touched, err := v.ChangedFiles(ctx, m.ws.Root, base)
	if err != nil {
		return types.ConformanceBase{}, fmt.Errorf("conformance: files changed since %s: %w", base, err)
	}
	plan, err := m.baseIndexPlan(ctx, m.ws.Root)
	if err != nil {
		return types.ConformanceBase{}, err
	}
	entry, err := m.conformanceEntry(commit, plan)
	if err != nil {
		return types.ConformanceBase{}, err
	}
	kept, ok, err := readConformanceEntry(entry)
	if err != nil {
		return types.ConformanceBase{}, err
	}
	if !ok {
		return types.ConformanceBase{}, fmt.Errorf("conformance: no measure of base %s under this workspace's configuration (%s), "+
			"which magus\\change().mergeBase measures and `magus run conformance-base .` runs here", commit, whyRemeasure(entry))
	}
	out := types.ConformanceBase{Commit: commit, Distributions: kept.Distributions, Packages: kept.Packages,
		Touched: []string{}, Unmeasured: []types.ConformanceUnmeasured{}}
	for _, p := range touched {
		if lang, ok := plan.unmeasured(p); ok {
			out.Unmeasured = append(out.Unmeasured, types.ConformanceUnmeasured{File: p, Language: lang, Reason: noBaseIndex})
			continue
		}
		if project, ok := insideAbsentProject(kept.Unmeasured, p); ok {
			out.Unmeasured = append(out.Unmeasured, types.ConformanceUnmeasured{File: p, Language: project.Language, Reason: project.Reason})
			continue
		}
		out.Touched = append(out.Touched, p)
	}
	return out, nil
}

// MeasureConformanceBase measures the merge base of base and HEAD with magus's in-process
// indexers under this workspace's configuration. It keeps the measure in the
// workspace store every checkout shares, keyed by commit, policy, indexer
// versions and baseMeasureVersion. No part of the key names the checkout.
// The base is exported there too, since a sandboxed gate writes only to the
// store and the temp dir. It returns the commit, and reuses a kept measure.
// It logs which it did, and why a kept measure of the commit did not match.
//
// With none kept it uses the remote tier's measure of the key only when
// that verifies against the trust set, and seeds head's packs from the
// signed packs beside it. A cold measure is signed and stored there when
// the run may write the tier. It logs why either did not happen.
func (m *Magus) MeasureConformanceBase(ctx context.Context, base string) (string, error) {
	v, commit, err := m.conformanceBaseCommit(ctx, base)
	if err != nil {
		return "", err
	}
	tree, err := conformanceTree(ctx)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tree)
	// Keyed by the indexers as head runs them, so ConformanceBase finds the entry: a Go
	// indexer's version is the toolchain its directory resolves.
	head, err := m.baseIndexPlan(ctx, m.ws.Root)
	if err != nil {
		return "", err
	}
	entry, err := m.conformanceEntry(commit, head)
	if err != nil {
		return "", err
	}
	if _, ok, err := readConformanceEntry(entry); err != nil || ok {
		if ok {
			slog.InfoContext(ctx, "reusing the kept conformance measure of base "+commit, slog.String("entry", entry))
		}
		return commit, err
	}
	remote, reused := m.reuseRemoteMeasure(ctx, entry, commit, head)
	if reused {
		return commit, nil
	}
	if err := v.ExportRevision(ctx, m.ws.Root, commit, tree); err != nil {
		return "", fmt.Errorf("conformance: export %s: %w", commit, err)
	}
	plan, err := m.baseIndexPlan(ctx, tree)
	if err != nil {
		return "", err
	}
	present, absent := plan.splitAbsent(tree)
	out, err := m.measureTree(ctx, tree, present)
	if err != nil {
		return "", fmt.Errorf("conformance: measure %s: %w", commit, err)
	}
	m.seedHeadIndexes(ctx, tree, present, head)
	out.Unmeasured = absent
	if err := writeConformanceEntry(entry, out); err != nil {
		return "", err
	}
	if remote != nil {
		m.publishConformanceBase(ctx, remote, entry, commit, tree, present)
	}
	return commit, nil
}

// reuseRemoteMeasure keeps the remote tier's measure at entry when one
// verifies, and logs why it measures cold when none does. It returns the
// tier to store a cold measure in, nil when there is none.
func (m *Magus) reuseRemoteMeasure(ctx context.Context, entry, commit string, head baseIndexPlan) (*cache.RemoteNamespace, bool) {
	why := whyRemeasure(entry)
	remote, none := m.conformanceRemote(ctx)
	if remote == nil {
		slog.InfoContext(ctx, fmt.Sprintf("measuring conformance base %s cold, since %s and %s", commit, why, none))
		return nil, false
	}
	err := m.restoreConformanceBase(ctx, remote, entry, commit, head)
	switch {
	case err == nil:
		slog.InfoContext(ctx, fmt.Sprintf("reusing the signed conformance measure of base %s from the remote tier", commit), slog.String("entry", entry))
		return remote, true
	case errors.Is(err, types.ErrCacheMiss):
		slog.InfoContext(ctx, fmt.Sprintf("measuring conformance base %s cold, since %s and the remote tier holds no signed measure of it", commit, why))
	default:
		slog.WarnContext(ctx, fmt.Sprintf("the remote tier's conformance measure of base %s is refused, so measuring cold", commit),
			slog.String("entry", entry), attr.Error(err))
	}
	return remote, false
}

// conformanceRemoteNamespace holds signed base measures and their packs.
// The leading @ keeps it clear of every project path.
const conformanceRemoteNamespace = "@conformance"

// conformanceRemote is the remote tier's view for base measures, or nil
// and why there is none to ask.
func (m *Magus) conformanceRemote(ctx context.Context) (*cache.RemoteNamespace, string) {
	if m.cache == nil {
		return nil, "no remote tier is wired: this workspace opened no build cache"
	}
	ns := m.cache.RemoteNamespace(conformanceRemoteNamespace)
	if ns == nil {
		return nil, "no remote tier is wired"
	}
	if !ns.Active(ctx) {
		return nil, fmt.Sprintf("no remote tier is wired here: its %s backend is not active in this environment", ns.Backend())
	}
	return ns, ""
}

// conformancePackKey is the remote tier's key of b's pack of commit. The
// project and indexer version are digested: a key is one path segment.
func conformancePackKey(commit string, b baseIndex) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%s", b.project, b.op, b.ix.Version()))
	return fmt.Sprintf("%s-pack-%s", commit, shortDigest(sum))
}

// restoreConformanceBase keeps the remote tier's measure at entry and
// seeds head's packs from the packs beside it. It returns
// [types.ErrCacheMiss] when the tier holds none, and the verification's
// error when no trusted key signed it for this key.
func (m *Magus) restoreConformanceBase(ctx context.Context, remote *cache.RemoteNamespace, entry, commit string, head baseIndexPlan) error {
	data, err := getRemote(ctx, remote, filepath.Base(entry))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
		return fmt.Errorf("conformance: %w", err)
	}
	if err := file.WriteFileAtomic(entry, data, 0o600); err != nil {
		return fmt.Errorf("conformance: keep %s: %w", entry, err)
	}
	if _, _, err := readConformanceEntry(entry); err != nil {
		return errors.Join(err, os.Remove(entry))
	}
	m.seedRemotePacks(ctx, remote, commit, head)
	return nil
}

// getRemote reads the verified object stored under key.
func getRemote(ctx context.Context, remote *cache.RemoteNamespace, key string) ([]byte, error) {
	rc, err := remote.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(rc)
	return data, errors.Join(err, rc.Close())
}

// seedRemotePacks seeds each pack head keeps none of from the remote
// tier's pack of commit. The key holds head's indexer version, so another
// indexer's pack is a miss. A pack not seeded is logged.
func (m *Magus) seedRemotePacks(ctx context.Context, remote *cache.RemoteNamespace, commit string, head baseIndexPlan) {
	cacheDir := resolveCacheDir(m.ws.Root, m.cfg)
	for _, h := range head.indexes {
		dest := symbols.IndexPath(cacheDir, h.project, h.op)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		attrs := []any{slog.String("project", h.project), slog.String("language", h.language), slog.String("index", dest)}
		seeded, err := seedRemotePack(ctx, remote, conformancePackKey(commit, h), dest, cacheImmutable(m.cfg))
		switch {
		case err != nil:
			slog.InfoContext(ctx, "head's symbol index is not seeded from the remote tier's signed pack of the base, so its first freshen builds it whole",
				append(attrs, attr.Error(err))...)
		case seeded:
			slog.InfoContext(ctx, "seeded head's symbol index from the remote tier's signed pack of the base, so its first freshen re-indexes only what moved", attrs...)
		}
	}
}

// seedRemotePack links the verified pack stored under key in at dest, as
// seedPack does.
func seedRemotePack(ctx context.Context, remote *cache.RemoteNamespace, key, dest string, readOnly bool) (bool, error) {
	if readOnly {
		return false, errors.New("the cache is read-only")
	}
	data, err := getRemote(ctx, remote, key)
	if errors.Is(err, types.ErrCacheMiss) {
		return false, errors.New("the remote tier holds no signed pack of it")
	}
	if err != nil {
		return false, err
	}
	return linkPack(data, dest)
}

// publishConformanceBase signs and stores the measure at entry and each
// pack it wrote on the remote tier. A failed store is only logged, since
// the measure is kept here already.
func (m *Magus) publishConformanceBase(ctx context.Context, remote *cache.RemoteNamespace, entry, commit, tree string, present baseIndexPlan) {
	data, err := os.ReadFile(entry)
	if err == nil {
		err = remote.Put(ctx, filepath.Base(entry), bytes.NewReader(data))
	}
	switch {
	case errors.Is(err, cache.ErrRemoteReadOnly):
		slog.InfoContext(ctx, "stored nothing on the remote tier for conformance base "+commit, attr.Error(err))
		return
	case err != nil:
		slog.WarnContext(ctx, fmt.Sprintf("conformance base %s is not stored on the remote tier", commit), slog.String("entry", entry), attr.Error(err))
		return
	}
	stored := 0
	for _, b := range present.indexes {
		data, err := readBasePack(tree, basePack(tree, b))
		if err == nil {
			err = remote.Put(ctx, conformancePackKey(commit, b), bytes.NewReader(data))
		}
		if err != nil {
			slog.WarnContext(ctx, "the base's symbol pack is not stored on the remote tier",
				slog.String("project", b.project), slog.String("language", b.language), attr.Error(err))
			continue
		}
		stored++
	}
	slog.InfoContext(ctx, fmt.Sprintf("stored the conformance measure of base %s and %d of its %d symbol packs on the remote tier, signed",
		commit, stored, len(present.indexes)), slog.String("entry", entry))
}

// conformanceBaseCommit is the commit base and HEAD fork from, the one ChangedFiles diffs
// against, so work merged upstream since is neither counted nor touched.
func (m *Magus) conformanceBaseCommit(ctx context.Context, base string) (types.VCSDriver, string, error) { //nolint:ireturn // the VCS driver is picked by detection at runtime
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

// keptMeasure is a kept measure's one line. Name is the file name its key
// gives, so a line read under any other name is not this key's measure.
type keptMeasure struct {
	Name    string            `json:"name"`
	Measure types.Conformance `json:"measure"`
}

// writeConformanceEntry keeps out at entry, a path conformanceEntry gave.
func writeConformanceEntry(entry string, out types.Conformance) error {
	line, err := codec.Marshal(keptMeasure{Name: filepath.Base(entry), Measure: out})
	if err != nil {
		return fmt.Errorf("conformance: encode %s: %w", entry, err)
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
		return fmt.Errorf("conformance: %w", err)
	}
	if err := file.WriteFileAtomic(entry, append(line, '\n'), 0o600); err != nil {
		return fmt.Errorf("conformance: keep %s: %w", entry, err)
	}
	return nil
}

// readConformanceEntry is the measure kept at entry, and false when none is.
// A line naming another key is refused: a file copied or renamed to this
// name holds another key's measure.
func readConformanceEntry(entry string) (types.Conformance, bool, error) {
	data, err := os.ReadFile(entry)
	if errors.Is(err, os.ErrNotExist) {
		return types.Conformance{}, false, nil
	}
	if err != nil {
		return types.Conformance{}, false, fmt.Errorf("conformance: read %s: %w", entry, err)
	}
	var line struct {
		Name    string           `json:"name"`
		Measure codec.RawMessage `json:"measure"`
	}
	if err := codec.Unmarshal(data, &line); err != nil {
		return types.Conformance{}, false, fmt.Errorf("conformance: %s does not parse, so delete it and measure the base again: %w", entry, err)
	}
	if line.Name != filepath.Base(entry) {
		return types.Conformance{}, false, fmt.Errorf("conformance: %s holds the measure kept as %q, "+
			"so delete it and measure the base again with `magus run conformance-base .`", entry, line.Name)
	}
	raw := line.Measure
	var out types.Conformance
	if err := codec.Unmarshal(raw, &out); err != nil {
		return types.Conformance{}, false, fmt.Errorf("conformance: %s does not parse, so delete it and measure the base again: %w", entry, err)
	}
	var keys struct {
		Rows          codec.RawMessage `json:"rows"`
		Distributions codec.RawMessage `json:"distributions"`
	}
	if err := codec.Unmarshal(raw, &keys); err != nil {
		return types.Conformance{}, false, fmt.Errorf("conformance: %s does not parse, so delete it and measure the base again: %w", entry, err)
	}
	drifted := len(out.Distributions) == 0 && out.Answer.Verdict == types.VerdictFound
	if drifted || (len(keys.Rows) == 0 && len(keys.Distributions) == 0) {
		return types.Conformance{}, false, fmt.Errorf("conformance: %s holds no distributions, since it was kept in another shape, "+
			"so measure the base again with `magus run conformance-base .`", entry)
	}
	return out, true, nil
}

// conformanceEntry is the cache file of commit's measure under this workspace's policy and
// plan's indexers, under baseMeasureVersion. Its name keeps the key's parts
// apart, so whyRemeasure can name the one that moved.
func (m *Magus) conformanceEntry(commit string, plan baseIndexPlan) (string, error) {
	policy, err := codec.Marshal(m.cfg.Knowledge.Conformance)
	if err != nil {
		return "", fmt.Errorf("conformance: digest the policy: %w", err)
	}
	indexers := sha256.New()
	for _, ix := range plan.indexes {
		fmt.Fprintf(indexers, "%s\x00%s\x00%s\x00", ix.project, ix.language, ix.ix.Version())
	}
	store, err := config.StoreDir(m.ws.Root, m.cfg)
	if err != nil {
		return "", fmt.Errorf("conformance: %w", err)
	}
	key := conformanceKey{
		commit: commit,
		// The name is one path segment, its parts split on "-", and the remote tier's key is its base.
		version:  strings.NewReplacer("/", ".", "-", ".").Replace(baseMeasureVersion),
		policy:   shortDigest(sha256.Sum256(policy)),
		indexers: shortDigest([sha256.Size]byte(indexers.Sum(nil))),
	}
	return filepath.Join(store, "conformance", key.file()), nil
}

// shortDigest is sum's first 8 bytes in hex: a key part, not a checksum.
func shortDigest(sum [sha256.Size]byte) string { return hex.EncodeToString(sum[:8]) }

// conformanceKey is what a kept measure's file name holds.
type conformanceKey struct {
	commit, version, policy, indexers string
}

func (k conformanceKey) file() string {
	return fmt.Sprintf("%s-v%s-%s-%s.jsonl", k.commit, k.version, k.policy, k.indexers)
}

// parseConformanceKey reads a kept measure's file name.
// It is false for a name in another layout.
func parseConformanceKey(name string) (conformanceKey, bool) {
	stem, ok := strings.CutSuffix(name, ".jsonl")
	parts := strings.Split(stem, "-")
	if !ok || len(parts) != 4 || !strings.HasPrefix(parts[1], "v") {
		return conformanceKey{}, false
	}
	return conformanceKey{commit: parts[0], version: parts[1][1:], policy: parts[2], indexers: parts[3]}, true
}

// whyRemeasure says why no kept measure answers entry.
// It names the key part each kept measure of the commit differs in.
func whyRemeasure(entry string) string {
	const none = "no measure of it is kept"
	want, ok := parseConformanceKey(filepath.Base(entry))
	if !ok {
		return none
	}
	kept, err := filepath.Glob(filepath.Join(filepath.Dir(entry), want.commit+"-*.jsonl"))
	if err != nil || len(kept) == 0 {
		return none
	}
	var reasons []string
	for _, path := range kept {
		reasons = append(reasons, keyMismatch(want, filepath.Base(path)))
	}
	slices.Sort(reasons)
	return "the kept measure of it " + strings.Join(slices.Compact(reasons), "; another ")
}

// keyMismatch names the first part of name's key that differs from want's.
func keyMismatch(want conformanceKey, name string) string {
	got, ok := parseConformanceKey(name)
	switch {
	case !ok:
		return "is filed under an older key layout"
	case got.version != want.version:
		return fmt.Sprintf("is measure version %s, and this magus measures version %s", got.version, want.version)
	case got.policy != want.policy:
		return "was measured under another knowledge.conformance policy"
	case got.indexers != want.indexers:
		return "was indexed by other indexer versions (the Go toolchain, scipbuzz or the sh parser)"
	default:
		return "has this key but did not read as a measure"
	}
}

// baseIndex is one in-process symbol index of a project, over the base tree.
// op names the pack head's index of the project is kept in.
type baseIndex struct {
	project, language, op string
	ix                    symbols.UnitIndexer
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

// index is the plan's index of project that op writes.
func (p baseIndexPlan) index(project, op string) (baseIndex, bool) {
	for _, ix := range p.indexes {
		if ix.project == project && ix.op == op {
			return ix, true
		}
	}
	return baseIndex{}, false
}

// splitAbsent parts the indexes by whether tree holds their project. The plan
// is head's, so a project new at head has no directory in a base tree.
func (p baseIndexPlan) splitAbsent(tree string) (baseIndexPlan, []types.ConformanceUnmeasured) {
	present := baseIndexPlan{external: p.external}
	var absent []types.ConformanceUnmeasured
	for _, ix := range p.indexes {
		if _, err := os.Stat(filepath.Join(tree, filepath.FromSlash(ix.project))); errors.Is(err, os.ErrNotExist) {
			absent = append(absent, types.ConformanceUnmeasured{File: ix.project, Language: ix.language, Reason: projectNewAtHead})
			continue
		}
		present.indexes = append(present.indexes, ix)
	}
	return present, absent
}

// insideAbsentProject is the base measure's record of the project holding file.
func insideAbsentProject(absent []types.ConformanceUnmeasured, file string) (types.ConformanceUnmeasured, bool) {
	for _, project := range absent {
		if strings.HasPrefix(file, project.File+"/") {
			return project, true
		}
	}
	return types.ConformanceUnmeasured{}, false
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
			plan.indexes = append(plan.indexes, baseIndex{project: p.Path, language: byName[name].Language, op: si.OpName(), ix: ix})
		}
	}
	return plan, nil
}

// trustRoot adds root to MISE_TRUSTED_CONFIG_PATHS in environ. CI trusts
// only the workspace, and the base tree holds the same config.
func trustRoot(environ []string, root string) []string {
	const name = "MISE_TRUSTED_CONFIG_PATHS"
	paths := []string{root}
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if value, ok := strings.CutPrefix(kv, name+"="); ok {
			if value != "" {
				paths = []string{value, root}
			}
			continue
		}
		out = append(out, kv)
	}
	return append(out, fmt.Sprintf("%s=%s", name, strings.Join(paths, string(os.PathListSeparator))))
}

// inProcessIndexer is the in-process indexer si's format names over dir, nil for an external one.
func inProcessIndexer(si *spells.SymbolIndexer, dir, root string) symbols.UnitIndexer { //nolint:ireturn // symbols.UnitIndexer: goindex, buzzindex or shindex
	switch si.Format {
	case spells.SymbolFormatGoPackages:
		patterns := slices.Clone(si.Command.Args)
		if len(patterns) == 0 {
			patterns = []string{"."}
		}
		return &goindex.Indexer{Dir: dir, Patterns: patterns, Env: trustRoot(os.Environ(), root), Builds: si.Envs}
	case spells.SymbolFormatBuzzModule:
		return &buzzindex.Indexer{Dir: dir, WorkspaceRoot: root}
	case spells.SymbolFormatShellScript:
		return &shindex.Indexer{Dir: dir, WorkspaceRoot: root}
	case spells.SymbolFormatNone, spells.SymbolFormatSCIP:
	}
	return nil
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

// basePack is where measureTree writes b's pack of tree: the path head keeps
// that index at, under the tree's own cache dir.
func basePack(tree string, b baseIndex) string {
	return symbols.IndexPath(filepath.Join(tree, ".magus"), b.project, b.op)
}

// measureTree indexes tree with plan's in-process indexers,
// several at once, and measures it under this workspace's policy.
// A source an indexer cannot read fails, naming it.
func (m *Magus) measureTree(ctx context.Context, tree string, plan baseIndexPlan) (types.Conformance, error) {
	parsed := make([][]types.KnowledgeSymbol, len(plan.indexes))
	indexing, ictx := errgroup.WithContext(ctx)
	indexing.SetLimit(runtime.GOMAXPROCS(0))
	for i, b := range plan.indexes {
		indexing.Go(func() error {
			got, err := indexBase(ictx, tree, basePack(tree, b), b)
			if err != nil {
				return fmt.Errorf("%s %s index: %w", b.project, b.language, err)
			}
			parsed[i] = got
			return nil
		})
	}
	if err := indexing.Wait(); err != nil {
		return types.Conformance{}, err
	}
	// In plan order: the measure is the same whichever indexer ends first.
	syms := map[string][]types.KnowledgeSymbol{}
	for i, b := range plan.indexes {
		syms[b.project] = append(syms[b.project], parsed[i]...)
	}
	graph, err := m.TargetGraph(ctx)
	if err != nil {
		return types.Conformance{}, err
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
		return types.Conformance{}, err
	}
	if opts.Generated, err = m.declaredOutputs(ctx, g); err != nil {
		return types.Conformance{}, err
	}
	out := g.Conformance(opts)
	out.Definition = types.ConformanceDefinition
	out.Packages = g.ConformancePackages(opts)
	if out.Answer.Verdict == types.VerdictUnknown {
		return types.Conformance{}, errors.New("the in-process indexers read no symbols, so no ceiling can be judged against it")
	}
	return out, nil
}

// seedHeadIndexes copies each pack measureTree wrote to where head keeps
// that index, when head keeps none. Digests are paths and bytes, so head's
// first freshen re-indexes only what moved. A refused seed is logged.
func (m *Magus) seedHeadIndexes(ctx context.Context, tree string, base, head baseIndexPlan) {
	cacheDir := resolveCacheDir(m.ws.Root, m.cfg)
	for _, b := range base.indexes {
		dest := symbols.IndexPath(cacheDir, b.project, b.op)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		attrs := []any{slog.String("project", b.project), slog.String("language", b.language), slog.String("index", dest)}
		seeded, err := seedPack(tree, basePack(tree, b), dest, b, head, cacheImmutable(m.cfg))
		switch {
		case err != nil:
			slog.InfoContext(ctx, "head's symbol index is not seeded from the base measure's, so its first freshen builds it whole",
				append(attrs, attr.Error(err))...)
		case seeded:
			slog.InfoContext(ctx, "seeded head's symbol index from the base measure's, so its first freshen re-indexes only what moved", attrs...)
		}
	}
}

// seedPack links a copy of pack, b's index of tree, in at dest. It is false
// when dest appeared meanwhile, which it never replaces. It refuses a pack
// head's indexer would not take, or one naming tree's path.
func seedPack(tree, pack, dest string, b baseIndex, head baseIndexPlan, readOnly bool) (bool, error) {
	if readOnly {
		return false, errors.New("the cache is read-only")
	}
	h, ok := head.index(b.project, b.op)
	if !ok {
		return false, fmt.Errorf("head declares no %s index of %s", b.op, b.project)
	}
	if got, want := b.ix.Version(), h.ix.Version(); got != want {
		return false, fmt.Errorf("the base tree's indexer is %q and head's is %q", got, want)
	}
	data, err := readBasePack(tree, pack)
	if err != nil {
		return false, err
	}
	return linkPack(data, dest)
}

// readBasePack is pack's bytes. It refuses a pack naming tree's path,
// which points every reader at a removed directory.
func readBasePack(tree, pack string) ([]byte, error) {
	data, err := os.ReadFile(pack)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(tree)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{tree, resolved} {
		if bytes.Contains(data, []byte(dir)) {
			return nil, fmt.Errorf("its shards name the base tree's path %s", dir)
		}
	}
	return data, nil
}

// linkPack links a file holding data in at dest. It is false when dest
// appeared meanwhile, which it never replaces.
func linkPack(data []byte, dest string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), fmt.Sprintf(".%s.seed-*", filepath.Base(dest)))
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	_, written := tmp.Write(data)
	if err := errors.Join(written, tmp.Chmod(0o644), tmp.Close()); err != nil {
		return false, err
	}
	// A link, not a rename: rename would replace an index head built meanwhile.
	err = os.Link(tmp.Name(), dest)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	return err == nil, err
}

// conformanceTree is outside the workspace on purpose. Mise walks up from
// it for config, and the Linux sandbox denies the workspace's.
func conformanceTree(ctx context.Context) (string, error) {
	tree, err := os.MkdirTemp(sandbox.PolicyFromContext(ctx).TempBase(), "magus-conformance-tree-")
	if err != nil {
		return "", fmt.Errorf("conformance: %w", err)
	}
	return tree, nil
}
