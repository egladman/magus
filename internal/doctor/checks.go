package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/egladman/magus/internal/cache"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/service/identity"
	"github.com/egladman/magus/internal/serviceaudit"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// checkBridgeReachability probes the console endpoint (/api/v1/graph) by
// issuing a real HTTP GET (a 401 proves the guarded route exists).
func (r *runner) checkBridgeReachability() Check {
	return probeBridgeReachability(r.opts.daemonInfo)
}

// checkNearDuplicateServices is the static, whole-workspace half of MGS5001: it
// reports service targets across projects that look like copies of one service
// (same image and container port) but differ subtly, so they run as separate
// processes instead of one shared instance. Unlike the runtime warning (scoped to
// a single run's graph) this audit is repo-wide, so it is "potential overlap":
// some clusters may never co-occur in one run.
func (*runner) checkNearDuplicateServices(projects []*types.Project) Check {
	const name = "service duplication"
	clusters := serviceaudit.NearDuplicates(projects, nil)
	if len(clusters) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no near-duplicate services detected"}
	}
	details := strings.Split(identity.FormatWarning(clusters), "\n")
	details = append(details, fmt.Sprintf("see %s: %s", types.NearDuplicateServices, types.CodeURL(types.NearDuplicateServices)))
	return Check{
		Name:    name,
		Status:  StatusFail,
		Message: fmt.Sprintf("%d near-duplicate service cluster(s); extract a shared target or mark them distinct", len(clusters)),
		Details: details,
	}
}

// checkStaleServiceSuppressions is the allow-unused half of the justified-
// suppression model: it flags services marked distinct (opted out of dedup with a
// reason) whose near-duplicate no longer exists, so the reason is stale and should
// be pruned to keep the opt-out meaningful.
func (*runner) checkStaleServiceSuppressions(projects []*types.Project) Check {
	const name = "service suppressions"
	unused := serviceaudit.UnusedDistinct(projects, nil)
	if len(unused) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no stale distinct-service suppressions"}
	}
	details := make([]string, 0, len(unused)+1)
	for _, n := range unused {
		details = append(details, fmt.Sprintf("%s is marked distinct but has no near-duplicate; remove the opt-out", n))
	}
	details = append(details, fmt.Sprintf("see %s: %s", types.NearDuplicateServices, types.CodeURL(types.NearDuplicateServices)))
	return Check{
		Name:    name,
		Status:  StatusFail,
		Message: fmt.Sprintf("%d stale distinct-service suppression(s)", len(unused)),
		Details: details,
	}
}

// checkLanguageCoverage flags a project that binds no toolchain spell, which usually means
// an import was forgotten and the project's real work is invisible to affected tracking and
// the cache. Usually, not always: a polyglot harness no single pack describes is legitimate,
// and a project says so with magus.project's "no_language" key. The opt-out carries a reason
// rather than a bool so the exemption reads as a decision instead of a silenced check.
func (*runner) checkLanguageCoverage(projects []*types.Project) Check {
	var noLang []string
	exempt := 0
	for _, p := range projects {
		if p.Spell != "" {
			continue
		}
		if p.NoLanguage != "" {
			exempt++
			continue
		}
		noLang = append(noLang, p.Path)
	}
	if len(noLang) == 0 {
		msg := "every project matched a spell"
		if exempt > 0 {
			msg = fmt.Sprintf("every project matched a spell or declared no_language (%d exempt)", exempt)
		}
		return Check{Name: "language coverage", Status: StatusOK, Message: msg}
	}
	slices.Sort(noLang)
	return Check{
		Name:    "language coverage",
		Status:  StatusFail,
		Message: fmt.Sprintf("%d project(s) without a language pack", len(noLang)),
		Details: noLang,
	}
}

// checkCITarget fails when no project in the workspace declares a `ci` target.
// ci is the anchor `magus ci` / `magus affected ci` / `magus affected --plan`
// key off; a workspace defining none would run that gate as a silent no-op (exit
// 0 having gated nothing). The runtime path enforces the same rule (MGS1001);
// this surfaces it as a health check so the gap is visible before CI runs.
// Detection reuses the magusfile source scan (ci lives in the magusfile, never a
// spell). Matching is case-insensitive because magus normalizes CI/Ci to ci.
func (*runner) checkCITarget(projects []*types.Project) Check {
	const name = "ci target"
	if len(projects) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no projects; skipped"}
	}
	norm := types.Normalize
	for _, p := range projects {
		for _, f := range magusfileSourcesInDir(p.Dir) {
			for _, decl := range declaredTargetNames(f) {
				// Normalize the raw identifier as the runtime does (CI/Ci -> ci)
				// so a ci target declared in any casing is recognized.
				if norm(decl) == types.TargetCI {
					return Check{Name: name, Status: StatusOK, Message: "ci target is defined"}
				}
			}
		}
	}
	return Check{
		Name:    name,
		Status:  StatusFail,
		Message: "no ci target defined in any project; `magus ci` / `magus affected ci` would gate nothing (silent no-op)",
		Details: []string{
			`define one in your magusfile, e.g.  export fun ci(ctx: magus\Context, args: [str]) > void { ctx.needs(build, test, lint); }`,
			"run 'magus describe targets' to see the available stages to compose",
			fmt.Sprintf("see %s: %s", types.NoCITarget, types.CodeURL(types.NoCITarget)),
		},
	}
}

// checkSpellDocs requires a doc comment on every function-handler target of each
// workspace-local Buzz spell. Only those targets opt in (DocRequiredTargets);
// built-ins and record-style {cmd,args} ops, whose handler comments aren't
// captured, are skipped - so the check enforces the convention exactly where the
// Buzz interpreter can verify it.
func (*runner) checkSpellDocs(spells []*spells.Spell) Check {
	const name = "spell target docs"
	var undocumented []string
	for _, s := range spells {
		for _, t := range s.DocRequiredTargets() {
			if s.TargetDoc(t) == "" {
				undocumented = append(undocumented, s.Name()+":"+t)
			}
		}
	}
	if len(undocumented) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "every local spell target has a doc comment"}
	}
	slices.Sort(undocumented)
	return Check{
		Name:    name,
		Status:  StatusFail,
		Message: fmt.Sprintf("%d local spell target(s) missing a doc comment", len(undocumented)),
		Details: undocumented,
	}
}

func (r *runner) checkGraphCycles() Check {
	if _, err := r.ws.Graph(); err != nil {
		return Check{Name: "dependency graph", Status: StatusFail, Message: err.Error()}
	}
	return Check{Name: "dependency graph", Status: StatusOK, Message: "no cycles detected"}
}

func (r *runner) checkSymlinks() Check {
	return checkSymlinks(r.ws.Root())
}

func (r *runner) checkGraphBounds() Check {
	return checkGraphBounds(r.ws.Root())
}

// checkGraphBounds fails when the committed knowledge graph holds a node naming a
// location outside the workspace. It is the sibling of checkSymlinks: both answer "does
// anything here reach out of the tree", one for the filesystem and one for the artifact
// the workspace publishes.
//
// It matters because the graph is committed, rendered into the docs site, and shared
// through the remote cache, so a node naming a path on one machine reaches all three. A
// real graph carried 93 of them, bottoming out in a developer's ~/Library/Caches/go-build
// shards, put there by a language indexer reporting document paths for the dependencies
// it resolved.
//
// The ingest guard in internal/symbols is what stops those getting in. This is what
// notices if a future extractor finds a way around it, which is why it reads the merged,
// committed artifact and keys on the node ID rather than any one field: several node
// kinds carry their path only in an ID or a Label.
//
// Import nodes are exempt. Their ID is the specifier a source file literally wrote, so
// "../../badge" records what the code says rather than a path magus resolved.
func checkGraphBounds(root string) Check {
	const name = "graph bounds"
	path := filepath.Join(root, "gen", "knowledge-graph.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Check{Name: name, Status: StatusOK, Message: "no committed knowledge graph to check"}
	}
	var g struct {
		Nodes []struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Label  string `json:"label"`
			Source string `json:"source"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &g); err != nil {
		return Check{Name: name, Status: StatusFail, Message: fmt.Sprintf("could not read the committed knowledge graph: %v", err)}
	}
	var escaping []string
	for _, n := range g.Nodes {
		if n.Kind == types.KindImport {
			continue
		}
		if escapesRoot(strings.TrimPrefix(n.ID, n.Kind+":")) || escapesRoot(n.Source) || escapesRoot(n.Label) {
			escaping = append(escaping, n.ID)
		}
	}
	if len(escaping) == 0 {
		return Check{Name: name, Status: StatusOK, Message: fmt.Sprintf("%d graph node(s); none name a path outside the workspace", len(g.Nodes))}
	}
	slices.Sort(escaping)
	return Check{
		Name:    name,
		Status:  StatusFail,
		Message: fmt.Sprintf("%d graph node(s) name a location outside the workspace; the graph is committed and shared, so they leak a local machine's layout", len(escaping)),
		Details: escaping,
	}
}

// escapesRoot reports whether s carries a ".." segment. Empty and non-path values are not
// escapes: many node kinds put a name rather than a path in these fields.
func escapesRoot(s string) bool {
	return s != "" && slices.Contains(strings.Split(s, "/"), "..")
}

// checkSymlinks fails on symlinks whose resolved target escapes root. They are
// a sandbox-escape vector where landlock is unavailable. In-tree symlinks are
// reported as context, since project discovery skips them.
func checkSymlinks(root string) Check {
	var escaping, inTree []string
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries, continue the walk
		}
		if d.IsDir() {
			if p != root && project.IsIgnoreDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		rel := toSlashRel(root, p)
		if target, escapes := symlinkEscapes(root, p); escapes {
			escaping = append(escaping, fmt.Sprintf("%s -> %s", rel, target))
		} else {
			inTree = append(inTree, rel)
		}
		return nil
	})
	if walkErr != nil {
		return Check{Name: "symlinks", Status: StatusFail, Message: fmt.Sprintf("could not scan for symlinks: %v", walkErr)}
	}
	slices.Sort(escaping)
	slices.Sort(inTree)
	if len(escaping) > 0 {
		details := escaping
		if len(inTree) > 0 {
			details = append(details, fmt.Sprintf("%d in-tree symlink(s) ignored by project discovery", len(inTree)))
		}
		return Check{
			Name:    "symlinks",
			Status:  StatusFail,
			Message: fmt.Sprintf("%d symlink(s) resolve outside the workspace root; they can escape the sandbox where landlock is unavailable", len(escaping)),
			Details: details,
		}
	}
	if len(inTree) > 0 {
		return Check{
			Name:    "symlinks",
			Status:  StatusOK,
			Message: fmt.Sprintf("%d in-tree symlink(s); none escape the workspace root (symlinked directories are skipped by project discovery)", len(inTree)),
			Details: inTree,
		}
	}
	return Check{Name: "symlinks", Status: StatusOK, Message: "no symlinks found under the workspace root"}
}

// symlinkEscapes reports whether the symlink at link resolves outside root,
// returning the resolved target (or the lexical target when dangling).
func symlinkEscapes(root, link string) (target string, escapes bool) {
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		// Dangling link: fall back to the lexical target to judge direction.
		raw, rerr := os.Readlink(link)
		if rerr != nil {
			return link, true // unreadable link, treat as suspect
		}
		if !filepath.IsAbs(raw) {
			raw = filepath.Join(filepath.Dir(link), raw)
		}
		resolved = filepath.Clean(raw)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return resolved, true
	}
	return resolved, false
}

func toSlashRel(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}

func (*runner) checkJSONCodec() Check {
	v := json.Version()
	msg := "encoding/json " + v
	if v == "v2" {
		msg += " (GOEXPERIMENT=jsonv2; faster marshaling)"
	}
	return Check{Name: "json codec", Status: StatusOK, Message: msg}
}

func (r *runner) checkConfigFile() Check {
	paths := configFilePaths(r.root)
	if len(paths) == 0 {
		return Check{Name: "config file", Status: StatusOK, Message: "no magus.yaml found; using defaults"}
	}
	var all []string
	for _, p := range paths {
		_, err := config.LoadFile(p, true)
		if err == nil {
			continue
		}
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			for _, f := range ve.Failures {
				all = append(all, fmt.Sprintf("%s: %s", filepath.Base(p), f.String()))
			}
		} else {
			all = append(all, fmt.Sprintf("%s: %s", filepath.Base(p), err.Error()))
		}
	}
	if len(all) == 0 {
		msg := paths[0]
		if len(paths) > 1 {
			msg = fmt.Sprintf("%d files checked", len(paths))
		}
		return Check{Name: "config file", Status: StatusOK, Message: msg + " (valid)"}
	}
	slices.Sort(all)
	return Check{
		Name:    "config file",
		Status:  StatusFail,
		Message: fmt.Sprintf("%d problem(s) in config file(s)", len(all)),
		Details: all,
	}
}

func configFilePaths(root string) []string {
	var paths []string
	add := func(dir string) {
		if p := firstExistingConfig(dir); p != "" {
			paths = append(paths, p)
		}
	}
	if udc, err := config.UserConfigDir(); err == nil {
		add(filepath.Join(udc, "magus"))
	}
	if root != "" {
		add(root)
	}
	if cwd, err := os.Getwd(); err == nil && cwd != root {
		add(cwd)
	}
	return paths
}

func firstExistingConfig(dir string) string {
	for _, name := range []string{"magus.yaml", ".magus.yaml"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// cacheDir resolves the workspace's cache directory, honoring an absolute or
// root-relative cache.dir override.
func (r *runner) cacheDir() string {
	if d := r.opts.cfg.Cache.Dir; d != "" {
		if filepath.IsAbs(d) {
			return filepath.Clean(d)
		}
		return filepath.Join(r.root, d)
	}
	return filepath.Join(r.root, ".magus")
}

// checkCacheYield surfaces the same finding the run path emits as a hint, so `magus
// doctor` gives the whole picture rather than only the target you happened to run.
func (r *runner) checkCacheYield() Check {
	const name = "cache yield"
	stalled := cache.StalledTargets(r.cacheDir(), nil)
	if len(stalled) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no target is running uncached"}
	}
	details := make([]string, 0, len(stalled)+1)
	for _, s := range stalled {
		details = append(details, fmt.Sprintf("%s %s: %d runs, 0 cached, %.0fs spent (%.0fs avg)",
			s.Project, s.Target, s.Runs, float64(s.TotalMs)/1000, float64(s.AvgMs())/1000))
	}
	details = append(details,
		"a target that never replays usually declares a footprint wider than it reads, so unrelated edits keep changing its key")
	return Check{
		Name:   name,
		Status: StatusFail,
		Message: fmt.Sprintf("[%s] %d target(s) executed repeatedly and never replayed from cache",
			types.TargetNeverReplays, len(stalled)),
		Details: details,
	}
}

func (r *runner) checkCacheWritable() Check {
	cacheDir := r.cacheDir()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return Check{
			Name:    "cache writable",
			Status:  StatusFail,
			Message: fmt.Sprintf("cannot create cache dir: %v", err),
			Details: []string{cacheDir},
		}
	}
	tmp, err := os.CreateTemp(cacheDir, ".magus-doctor-*")
	if err != nil {
		return Check{
			Name:    "cache writable",
			Status:  StatusFail,
			Message: fmt.Sprintf("cache dir not writable: %v", err),
			Details: []string{cacheDir},
		}
	}
	_ = tmp.Close()
	_ = os.Remove(tmp.Name())
	return Check{Name: "cache writable", Status: StatusOK, Message: cacheDir}
}

func (r *runner) checkVCSBaseRef() Check {
	return checkVCSBaseRef(r.root, r.ws.VCSOptions())
}

func checkVCSBaseRef(root string, opts types.VCSOptions) Check {
	res, err := vcs.Resolve(context.Background(), root, "", opts)
	if err != nil {
		return Check{Name: "vcs base ref", Status: StatusFail, Message: err.Error()}
	}
	switch res.Source {
	case types.VCSSourceDisabled:
		return Check{Name: "vcs base ref", Status: StatusOK, Message: "vcs disabled; skipped"}
	default:
		// explicit/auto/default sources: proceed to the live probe below
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var probeArgs []string
	switch res.Name {
	case "git":
		probeArgs = []string{"-C", root, "rev-parse", "--verify", "--quiet", res.Base}
	case "hg":
		probeArgs = []string{"-R", root, "log", "-r", res.Base, "-l", "1", "-T", "{node}\\n"}
	case "jj":
		probeArgs = []string{"-R", root, "log", "-r", res.Base, "-n", "1", "--no-graph", "-T", "commit_id"}
	default:
		return Check{Name: "vcs base ref", Status: StatusOK, Message: fmt.Sprintf("%s: no probe available; skipped", res.Name)}
	}

	cmd := exec.CommandContext(ctx, res.Name, probeArgs...)
	if err := cmd.Run(); err != nil {
		return Check{
			Name:    "vcs base ref",
			Status:  StatusFail,
			Message: fmt.Sprintf("base_ref %q not reachable (set MAGUS_VCS_BASE_REF to a reachable ref)", res.Base),
			Details: []string{fmt.Sprintf("%s exited: %v", res.Name, err)},
		}
	}

	return Check{Name: "vcs base ref", Status: StatusOK, Message: fmt.Sprintf("%s %q resolves", res.Name, res.Base)}
}

func (*runner) checkEnvVars() Check {
	var unknown []string
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key := kv[:eq]
		if !strings.HasPrefix(key, "MAGUS_") {
			continue
		}
		if _, ok := KnownEnvVars[key]; ok {
			continue
		}
		// MAGUS_LEVEL is injected into every subprocess (the recursion
		// depth, like GNU Make's MAKELEVEL; see internal/proc/run SelfVars). It's a
		// runtime signal, not a config field, so it won't be in KnownEnvVars - a
		// nested magus legitimately sees it.
		if key == "MAGUS_LEVEL" {
			continue
		}
		// MAGUS_VCS_<NAME>_BASE_REF is a dynamic per-VCS pattern, not a
		// static config field. Allow any key of this shape.
		if strings.HasPrefix(key, "MAGUS_VCS_") && strings.HasSuffix(key, "_BASE_REF") {
			continue
		}
		unknown = append(unknown, key)
	}
	if len(unknown) == 0 {
		return Check{Name: "environment variables", Status: StatusOK, Message: "no unknown MAGUS_* variables"}
	}
	slices.Sort(unknown)
	return Check{
		Name:    "environment variables",
		Status:  StatusFail,
		Message: fmt.Sprintf("%d unknown MAGUS_* variable(s); typos?", len(unknown)),
		Details: unknown,
	}
}

// checkTargetNameConventions fails when a workspace declares target functions
// using more than one naming convention (snake_case, camelCase, PascalCase).
// magus normalizes every target name, so this doesn't restrict which casing you
// use; it only requires the workspace to pick ONE convention and stay consistent,
// which keeps invocations greppable. Single-word, all-lowercase names (build,
// test) are convention-neutral and ignored.
func (r *runner) checkTargetNameConventions(projects []*types.Project) Check {
	conventions := map[string]string{} // convention -> first "name (file)" example
	for _, p := range projects {
		for _, f := range magusfileSourcesInDir(p.Dir) {
			for _, name := range declaredTargetNames(f) {
				conv := nameConvention(name)
				if conv == "" {
					continue
				}
				if _, seen := conventions[conv]; !seen {
					rel, _ := filepath.Rel(r.root, f)
					conventions[conv] = fmt.Sprintf("%s: %q in %s", conv, name, filepath.ToSlash(rel))
				}
			}
		}
	}
	if len(conventions) <= 1 {
		return Check{
			Name:    "target name conventions",
			Status:  StatusOK,
			Message: "target names use a consistent convention",
		}
	}
	details := make([]string, 0, len(conventions))
	for _, ex := range conventions {
		details = append(details, ex)
	}
	slices.Sort(details)
	return Check{
		Name:   "target name conventions",
		Status: StatusFail,
		Message: fmt.Sprintf("target names mix %d naming conventions; magus normalizes any casing so they "+
			"all resolve, but the workspace must pick one convention for consistent, greppable invocations", len(conventions)),
		Details: details,
	}
}

// nameConvention classifies a raw target identifier. Returns "snake_case",
// "camelCase", "PascalCase", or "" for a convention-neutral name (a single
// all-lowercase word such as "build", which fits every convention).
func nameConvention(name string) string {
	if strings.ContainsRune(name, '_') {
		return "snake_case"
	}
	if strings.IndexFunc(name, unicode.IsUpper) < 0 {
		return "" // all lowercase, no delimiter, neutral
	}
	if unicode.IsUpper(rune(name[0])) {
		return "PascalCase"
	}
	return "camelCase"
}

// bespokePhaseFragmentNames are normalized target names that name a subset of an
// existing canonical phase (see targets.md#the-target-name) rather than a phase of
// their own: static analysis or formatting a lesser model might carve out as its
// own target. typecheck and type-check are both listed because they normalize to
// different kebab forms (no word boundary vs. an explicit hyphen).
var bespokePhaseFragmentNames = map[string]bool{
	"typecheck": true, "type-check": true, "vet": true,
	"audit": true, "security": true, "style": true, "prettify": true,
}

// checkBespokePhaseFragmentTargets is MGS1003: a target whose normalized name
// names a static-analysis or formatting subset rather than a phase of its own
// should be composed into lint (or format) instead, so `magus affected ci`
// covers it without a bespoke target the pipeline can silently forget to run.
// A warning, not a load error: the escape hatch of keeping the name stays.
func (r *runner) checkBespokePhaseFragmentTargets(projects []*types.Project) Check {
	const name = "bespoke phase-fragment target names"
	seen := map[string]string{} // normalized name -> "name (file)" example
	for _, p := range projects {
		for _, f := range magusfileSourcesInDir(p.Dir) {
			for _, raw := range declaredTargetNames(f) {
				norm := types.Normalize(raw)
				if !bespokePhaseFragmentNames[norm] {
					continue
				}
				if _, ok := seen[norm]; ok {
					continue
				}
				rel, _ := filepath.Rel(r.root, f)
				seen[norm] = fmt.Sprintf("%q in %s", raw, filepath.ToSlash(rel))
			}
		}
	}
	if len(seen) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no bespoke phase-fragment target names"}
	}
	details := make([]string, 0, len(seen))
	for norm, ex := range seen {
		details = append(details, fmt.Sprintf("%s: %s", norm, ex))
	}
	slices.Sort(details)
	return Check{
		Name:   name,
		Status: StatusFail,
		Message: fmt.Sprintf(
			"%d target name(s) name static analysis or formatting rather than a phase of their own; "+
				"compose the op into lint (or format) so `magus affected ci` covers it (docs/targets.md#the-target-name, see %s)",
			len(seen), types.CodeURL(types.BespokePhaseFragmentName)),
		Details: details,
	}
}

// checkUnreachedFootprintDecls is MGS1004: a ctx.readsFiles/writesFiles call the static
// extractor can't reach from a target body - one in an unreferenced or
// indirectly-dispatched helper, or the identifier used as a value. Such a declaration
// never enters a cache key, so the target silently under-declares its footprint (a
// stale-hit risk). A warning, not a load error: an orphan may just be dead code.
func (r *runner) checkUnreachedFootprintDecls(projects []*types.Project) Check {
	const name = "unreached footprint declarations"
	var details []string
	for _, p := range projects {
		for _, f := range magusfileSourcesInDir(p.Dir) {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for _, ref := range describe.UnreachedIO(string(data)) {
				details = append(details, fmt.Sprintf("ctx.%s in %s (%s:%d)",
					ref.Kind, ref.Fn, r.relPath(f), ref.Line))
			}
		}
	}
	if len(details) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no unreached ctx.readsFiles/writesFiles declarations"}
	}
	slices.Sort(details)
	return Check{
		Name:   name,
		Status: StatusFail,
		Message: fmt.Sprintf(
			"%d ctx.readsFiles/writesFiles call(s) are not statically reachable from a target body, so they never enter a cache key; "+
				"call them directly in the target body (see %s)",
			len(details), types.CodeURL(types.UnreachedFootprintDecl)),
		Details: details,
	}
}

// checkRedundantFootprintGlobs is MGS1005: a per-target output glob already
// present project-wide. Explicit inputs intentionally do not participate because
// they narrow a target's source footprint even when a glob is also project-wide.
func (r *runner) checkRedundantFootprintGlobs(projects []*types.Project) Check {
	const name = "redundant footprint globs"
	var details []string
	for _, p := range projects {
		for target, refs := range p.TargetOutputs {
			for _, ref := range refs {
				// A cross-project output is never redundant with THIS project's globs:
				// its glob is relative to the tree it writes into, not to this one.
				if ref.Project != "" && ref.Project != p.Path {
					continue
				}
				if slices.Contains(p.Outputs, ref.Glob) {
					details = append(details, fmt.Sprintf("%s: ctx.writesFiles(%q) already in project outputs", target, ref.Glob))
				}
			}
		}
	}
	if len(details) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no redundant per-target footprint globs"}
	}
	slices.Sort(details)
	return Check{
		Name:   name,
		Status: StatusFail,
		Message: fmt.Sprintf(
			"%d per-target ctx.writesFiles glob(s) duplicate a project-wide declaration; drop the duplicate (see %s)",
			len(details), types.CodeURL(types.RedundantFootprintGlob)),
		Details: details,
	}
}

// checkDeadOutputGlobs is MGS1018: a declared output glob that matches nothing while its
// SIBLING globs in the same project match files.
//
// This is the shape that bit us: the typescript spell contributes `dist/**` to every project
// that binds it, and a project emitting to gen/ never writes a dist/ - so a target with no
// ctx.writesFiles of its own inherits a glob it can never satisfy. Snapshot then rejects it,
// but only on a cache MISS, so the failure hides for as long as the target keeps replaying.
// A cold cache - a new clone, a CI runner, `magus clean --cache` - is the first thing to see
// it, which is the worst possible time to find out.
//
// The sibling test is what keeps this quiet on an unbuilt tree. If NOTHING a project declares
// has been produced yet, the project simply has not been built and every glob matching zero is
// expected; reporting then would fire on every fresh clone and train people to ignore it. Only
// when some outputs exist and one glob still matches nothing is that glob suspect.
//
// "Some outputs exist" has to mean UNTRACKED outputs exist. A generated tree that is committed
// (console/src/gen from buf-generate, proto/gen) is declared as an output for staleness
// tracking and is present on a fresh clone, so counting it as evidence says "this was built"
// about a tree nobody has built - and then reports every sibling glob writing to an untracked
// gen/ as dead. That is the exact false positive this check was written to avoid, arriving
// through the back door: it fired on console's `gen/**` on every CI runner while passing on
// every developer machine, where gen/ had been built. A backend that cannot answer "is this
// tracked?" cannot tell those apart, so it degrades to plain presence rather than guess.
func (r *runner) checkDeadOutputGlobs(projects []*types.Project) Check {
	const name = "dead output globs"

	var tracked types.TrackedFileReporter
	if res, err := vcs.Resolve(context.Background(), r.root, "", r.ws.VCSOptions()); err == nil && res.VCS != nil {
		tracked, _ = res.VCS.(types.TrackedFileReporter)
	}

	var details []string
	for _, p := range projects {
		var dead []string
		builtAny := false
		for _, glob := range p.Outputs {
			hits, err := filepath.Glob(filepath.Join(p.Dir, filepath.FromSlash(strings.ReplaceAll(glob, "**", "*"))))
			if err != nil {
				continue
			}
			if len(hits) > 0 {
				if provesBuilt(tracked, p.Dir, hits) {
					builtAny = true
				}
				continue
			}
			dead = append(dead, glob)
		}
		if !builtAny {
			continue
		}
		for _, glob := range dead {
			details = append(details, fmt.Sprintf("%s: output glob %q matched no files while the project's other outputs did", types.ProjectDisplayName(p.Path, p.Name, p.Dir), glob))
		}
	}
	if len(details) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no dead output globs"}
	}
	slices.Sort(details)
	return Check{
		Name:   name,
		Status: StatusFail,
		Message: fmt.Sprintf(
			"%d declared output glob(s) never match; a target inheriting one fails its snapshot on a cold cache (see %s)",
			len(details), types.CodeURL(types.DeadOutputGlob)),
		Details: details,
	}
}

// checkOutputOwnedByTwoTargets is MGS1020: one output glob declared by two targets in the
// SAME project. MGS4002 already covers the cross-project shape (two projects, one target);
// this is its sibling, and the gap it fills is the common one - a generator and a formatter
// that both rewrite a gen/ tree.
//
// Two owners of one file is not an ordering problem, which is why it earns a diagnostic
// rather than a scheduling fix. Run the generator first and the formatter's edit is what
// lands, so the generator's next drift gate fails; run the formatter first and its work is
// immediately overwritten. Whichever goes last wins and the other's gate fails on the next
// run, forever, at every ordering. The fix is always ownership: one target owns the bytes,
// and if generated output needs formatting the GENERATOR formats it as its final step.
//
// Only DECLARED writes are visible here. A formatter that reformats a tree without
// declaring ctx.writesFiles is undetectable statically, which is why every formatter in
// this workspace excludes generated trees by configuration as well.
func (*runner) checkOutputOwnedByTwoTargets(projects []*types.Project) Check {
	const name = "output ownership"
	var details []string
	for _, p := range projects {
		owners := map[string][]string{}
		for target, refs := range p.TargetOutputs {
			for _, ref := range refs {
				if !slices.Contains(owners[ref.Glob], target) {
					owners[ref.Glob] = append(owners[ref.Glob], target)
				}
			}
		}
		for glob, targets := range owners {
			if len(targets) < 2 {
				continue
			}
			slices.Sort(targets)
			details = append(details, fmt.Sprintf("%s: output glob %q is declared by %s",
				types.ProjectDisplayName(p.Path, p.Name, p.Dir), glob, strings.Join(targets, " and ")))
		}
	}
	if len(details) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "every declared output has one owning target"}
	}
	slices.Sort(details)
	return Check{
		Name:   name,
		Status: StatusFail,
		Message: fmt.Sprintf(
			"%d output glob(s) declared by more than one target; whichever runs last wins and the other's drift gate fails (see %s)",
			len(details), types.CodeURL(types.OutputOwnedByTwoTargets)),
		Details: details,
	}
}

// provesBuilt reports whether a glob's matches are evidence that the project was actually
// built, which is true only when nothing the glob matched is committed. Matching a committed
// file proves the clone happened, not the build.
//
// Whether ls-files ECHOES the paths is the whole answer, so its output is only ever tested for
// emptiness: a directory argument makes it print the tracked files underneath instead of the
// directory itself, and comparing those names against the globbed ones would never line up.
// A hit set mixing tracked and untracked files reads as "not evidence", which under-reports
// rather than over-reports - the right way to be wrong for a check whose failure mode is
// training people to ignore it.
func provesBuilt(reporter types.TrackedFileReporter, dir string, hits []string) bool {
	if reporter == nil {
		return true
	}
	rels := make([]string, 0, len(hits))
	for _, h := range hits {
		rel, err := filepath.Rel(dir, h)
		if err != nil {
			return true
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	found, err := reporter.TrackedFiles(context.Background(), dir, rels)
	if err != nil {
		return true
	}
	return len(found) == 0
}

// magusfileSourcesInDir returns every Buzz magusfile source for a project
// directory: the top-level magusfile.buzz plus magusfiles/*.buzz.
func magusfileSourcesInDir(dir string) []string {
	var out []string
	if _, err := os.Stat(filepath.Join(dir, "magusfile.buzz")); err == nil {
		out = append(out, filepath.Join(dir, "magusfile.buzz"))
	}
	entries, _ := filepath.Glob(filepath.Join(dir, "magusfiles", "*.buzz"))
	out = append(out, entries...)
	slices.Sort(out)
	return out
}

// declaredTargetNames extracts the raw identifiers of target functions declared
// in a Buzz magusfile source: `export fun NAME`. Names are returned verbatim (not
// normalized) so the caller can classify the source's naming convention. A source
// that fails to parse yields no names (best-effort).
func declaredTargetNames(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	prog, err := buzz.ParseEmbedded(string(data))
	if err != nil || prog == nil {
		return nil
	}
	var names []string
	for _, stmt := range prog.Stmts {
		if fn, ok := stmt.(*ast.FunDecl); ok && fn.IsExported {
			names = append(names, fn.Name)
		}
	}
	return names
}

// checkMagusfileSyntax parses every magusfile in the workspace with the
// gopherbuzz checker and reports all syntax / strict-parity errors at once.
// Magusfiles are parsed in embedded mode (ParseEmbedded) because they
// legitimately use embedding-only constructs (top-level statements, unlabeled
// host calls) that upstream-strict parsing rejects; the check still catches the
// unconditional strict-parity errors (untyped params, reserved-word bindings,
// omitted return arrows, non-optional fiber yields) and plain syntax errors.
//
// Every magusfile is parsed before returning, so one run reports everything wrong
// rather than stopping at the first failure - what makes it useful in the CI
// preflight target: one `magus doctor` surfaces all magusfile problems at once.
func (r *runner) checkMagusfileSyntax(projects []*types.Project) Check {
	const name = "magusfile syntax"
	var problems []string
	var checked int
	for _, p := range projects {
		for _, f := range magusfileSourcesInDir(p.Dir) {
			data, err := os.ReadFile(f)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", r.relPath(f), err))
				continue
			}
			checked++
			if _, err := buzz.ParseEmbedded(string(data)); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", r.relPath(f), err))
			}
		}
	}
	if len(problems) == 0 {
		return Check{
			Name:    name,
			Status:  StatusOK,
			Message: fmt.Sprintf("%d magusfile(s) parse cleanly", checked),
		}
	}
	slices.Sort(problems)
	return Check{
		Name:    name,
		Status:  StatusFail,
		Message: fmt.Sprintf("%d magusfile(s) have syntax errors", len(problems)),
		Details: problems,
	}
}

// relPath renders path relative to the workspace root for display, falling back
// to the original path when it can't be made relative.
func (r *runner) relPath(path string) string {
	if rel, err := filepath.Rel(r.root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

// checkCharmTargetCollision warns when a charm name also names a target. Charms
// attach to a target with a ":" suffix (magus run lint:rw), so a charm sharing a
// target name makes invocations ambiguous to read and debug: `magus run cd` (the
// target) versus `magus run build:cd` (the charm). The charm set is magus's
// reserved built-ins (write, cd) plus every charm a target body branches on via
// has_charm; collisions are compared on the canonical name both sides normalize to.
func (r *runner) checkCharmTargetCollision(projects []*types.Project) Check {
	targets := map[string]string{} // normalized name -> first raw name seen
	charms := map[string]string{}  // normalized name -> first raw name seen
	for _, c := range types.ReservedCharms() {
		charms[types.Normalize(c)] = c
	}
	for _, p := range projects {
		for _, f := range magusfileSourcesInDir(p.Dir) {
			for _, name := range declaredTargetNames(f) {
				n := types.Normalize(name)
				if _, seen := targets[n]; !seen {
					targets[n] = name
				}
			}
			for _, name := range declaredCharmNames(f) {
				n := types.Normalize(name)
				if _, seen := charms[n]; !seen {
					charms[n] = name
				}
			}
		}
	}

	var details []string
	for n, charm := range charms {
		if target, ok := targets[n]; ok {
			if charm == target {
				details = append(details, fmt.Sprintf("%q is both a charm and a target", charm))
			} else {
				details = append(details, fmt.Sprintf("charm %q collides with target %q", charm, target))
			}
		}
	}
	if len(details) == 0 {
		return Check{
			Name:    "charm/target name collisions",
			Status:  StatusOK,
			Message: "no charm shares a target name",
		}
	}
	slices.Sort(details)
	return Check{
		Name:   "charm/target name collisions",
		Status: StatusFail,
		Message: fmt.Sprintf("%d charm name(s) also name a target; the `target:charm` suffix "+
			"makes these ambiguous to read and debug; rename one side", len(details)),
		Details: details,
	}
}

// declaredCharmNames extracts the charm names a magusfile's target bodies branch
// on: every has_charm("NAME") literal (including the built-in has_charm("rw")). It
// reuses the static target-graph extractor, so a has_charm mention inside a comment
// or string literal is correctly ignored.
func declaredCharmNames(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var names []string
	for _, n := range describe.Extract(string(data)) {
		names = append(names, n.Charms...)
	}
	return names
}

// checkHasCharmTypos flags a has_charm("NAME") read whose NAME matches no charm
// but is a near-miss of a real one: almost always a typo that silently becomes
// dead code, since the branch it guards can never be taken. It deliberately does
// NOT flag a novel undeclared name with no close match: a function target may
// legitimately read a charm no spell declares (a runtime-only toggle a user opts
// into with a suffix), so only a near-collision with a known charm is reported.
//
// Names are compared on their canonical (kebab) form, so separator- and
// case-variants that collapse onto a real charm (has_charm("no_cache") for
// "no-cache", has_charm("rw_") for "rw") are correctly treated as live reads, not
// typos. What remains are genuine misspellings (has_charm("rww") for "rw").
func (r *runner) checkHasCharmTypos(projects []*types.Project) Check {
	const name = "has_charm typos"

	// The known-charm vocabulary: magus's reserved built-ins plus every charm any
	// bound spell declares, canonicalized. A read matching one of these is live; a
	// near-miss of one is the typo we flag.
	known := map[string]struct{}{}
	var knownNames []string
	add := func(c string) {
		n := types.Normalize(c)
		if _, ok := known[n]; ok {
			return
		}
		known[n] = struct{}{}
		knownNames = append(knownNames, n)
	}
	for _, c := range types.ReservedCharms() {
		add(c)
	}
	for _, p := range projects {
		for _, s := range p.ResolvedSpells {
			for _, t := range s.Targets() {
				for _, c := range s.Charms(t) {
					add(c)
				}
			}
		}
	}

	seen := map[string]struct{}{}
	var details []string
	for _, p := range projects {
		for _, f := range magusfileSourcesInDir(p.Dir) {
			for _, raw := range declaredCharmNames(f) {
				n := types.Normalize(raw)
				if _, ok := known[n]; ok {
					continue // a live read of a real charm
				}
				hint := interactive.SuggestNearest(n, knownNames)
				if hint == "" {
					continue // a novel undeclared name: a legitimate runtime toggle
				}
				if _, dup := seen[raw]; dup {
					continue
				}
				seen[raw] = struct{}{}
				details = append(details, fmt.Sprintf("has_charm(%q) matches no charm; did you mean %q?", raw, hint))
			}
		}
	}
	if len(details) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no has_charm reads look like typos"}
	}
	slices.Sort(details)
	return Check{
		Name:    name,
		Status:  StatusFail,
		Message: fmt.Sprintf("%d has_charm read(s) look like a misspelled charm; the guarded branch is dead as written", len(details)),
		Details: details,
	}
}

// checkStaleShadowAcks flags a spells.allow_shadow entry whose shadow no longer
// exists: the deeper spell was moved or renamed, so the acknowledgment and its
// reason are dead config. Mirrors the unused-distinct check for services, keeping
// the acknowledged-suppression list honest.
func (r *runner) checkStaleShadowAcks() Check {
	const name = "stale spell-shadow acknowledgments"
	acks := r.opts.cfg.Spells.AllowShadow
	if len(acks) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no allow_shadow entries"}
	}
	if r.ws == nil {
		return Check{Name: name, Status: StatusOK, Message: "workspace not loaded"}
	}
	// r.ws.Root() is the resolved workspace root; r.root can be empty on this path.
	conflicts, err := project.SpellShadows(r.ws.Root())
	if err != nil {
		return Check{Name: name, Status: StatusOK, Message: "spell layout not scanned: " + err.Error()}
	}
	shadowed := make(map[string]struct{}, len(conflicts))
	for _, c := range conflicts {
		shadowed[c.Import] = struct{}{}
	}
	var details []string
	for _, a := range acks {
		if _, ok := shadowed[a.Name]; !ok {
			details = append(details, fmt.Sprintf("%q no longer shadows anything (reason: %q)", a.Name, a.Reason))
		}
	}
	if len(details) == 0 {
		return Check{Name: name, Status: StatusOK, Message: fmt.Sprintf("%d allow_shadow entr(ies), all live", len(acks))}
	}
	slices.Sort(details)
	return Check{
		Name:    name,
		Status:  StatusFail,
		Message: fmt.Sprintf("%d allow_shadow entr(ies) no longer match a real shadow; remove them", len(details)),
		Details: details,
	}
}

// checkWorkspaceRegistration reports whether this workspace is currently
// loaded in the multi-workspace daemon and how many other workspaces are
// present. Informational only - a workspace not yet loaded is normal (it
// loads on first use).
func (r *runner) checkWorkspaceRegistration() Check {
	d := r.opts.daemonInfo
	if d == nil || !d.Reachable || len(d.Workspaces) == 0 {
		return Check{Name: "workspace registration", Status: StatusOK, Message: "no loaded workspaces in daemon"}
	}
	thisRoot := r.root
	if r.ws != nil {
		thisRoot = r.ws.Root()
	}
	var registered bool
	for _, w := range d.Workspaces {
		if w.Root == thisRoot {
			registered = true
			break
		}
	}
	details := make([]string, 0, len(d.Workspaces))
	for _, w := range d.Workspaces {
		age := time.Since(w.LastAccess).Round(time.Second)
		details = append(details, fmt.Sprintf("%s  (idle %s)", w.Root, age))
	}
	if registered {
		return Check{
			Name:    "workspace registration",
			Status:  StatusOK,
			Message: fmt.Sprintf("loaded in daemon  (%d workspace(s) total)", len(d.Workspaces)),
			Details: details,
		}
	}
	return Check{
		Name:    "workspace registration",
		Status:  StatusOK,
		Message: fmt.Sprintf("not yet loaded in daemon  (%d other workspace(s) loaded)", len(d.Workspaces)),
		Details: details,
	}
}

// checkStaleSockets scans the magus socket directory. Multiple live daemons
// fail the check; leftover dead sockets are harmless and reported only as
// context.
func (r *runner) checkStaleSockets() Check {
	sockDir := r.opts.daemonInfo.sockDirOrDefault()
	if sockDir == "" {
		return Check{Name: "sockets", Status: StatusOK, Message: "no socket directory"}
	}

	entries, err := os.ReadDir(sockDir)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{Name: "sockets", Status: StatusOK, Message: "no socket directory"}
		}
		return Check{Name: "sockets", Status: StatusFail, Message: fmt.Sprintf("scan %s: %v", sockDir, err)}
	}

	var stale, live []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "magus-") || !strings.HasSuffix(e.Name(), ".sock") {
			continue
		}
		p := filepath.Join(sockDir, e.Name())
		if isSocketAlive(p) {
			live = append(live, p)
		} else {
			stale = append(stale, p)
		}
	}

	if len(stale) == 0 && len(live) <= 1 {
		return Check{Name: "sockets", Status: StatusOK, Message: fmt.Sprintf("%d live socket(s)", len(live))}
	}

	var details []string
	for _, p := range stale {
		details = append(details, "stale: "+p)
	}
	if len(live) > 1 {
		for _, p := range live {
			details = append(details, "live: "+p)
		}
	}

	// Multiple live daemons is a real conflict; leftover dead sockets are
	// harmless cruft, so stale-only no longer fails the check.
	if len(live) > 1 {
		return Check{
			Name:    "sockets",
			Status:  StatusFail,
			Message: fmt.Sprintf("%d live daemon sockets - multiple daemons running", len(live)),
			Details: details,
		}
	}
	return Check{
		Name:    "sockets",
		Status:  StatusOK,
		Message: fmt.Sprintf("%d stale socket(s)", len(stale)),
		Details: details,
	}
}

// sockDirOrDefault returns the daemon's socket directory, or "" when unset.
func (d *DaemonInfo) sockDirOrDefault() string {
	if d == nil {
		return ""
	}
	return d.SockDir
}

// isSocketAlive performs a lightweight dial to test whether a Unix-domain
// socket is connected to a live process.
func isSocketAlive(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// checkStaleWorktrees reports orphaned checkout directories under .claude/worktrees.
func (r *runner) checkStaleWorktrees() Check {
	return checkStaleWorktrees(r.ws.Root())
}

// checkSpellContract reports each registered spell's mgs_ contract coverage.
func (r *runner) checkSpellContract() Check {
	return checkSpellContract(project.DefaultSpellRegistry().All())
}

// checkGuardBinary reports which magus binary an agent-host guard hook would
// actually execute, and whether it predates the working tree's Go sources.
//
// This exists because of a real and expensive failure. The hook resolved its
// binary as `command -v magus || /tmp/magus`, nothing was on PATH, and so every
// verdict for a whole session came from a months-old binary left in /tmp by an
// earlier session. The guard looked like it was working - it denied things, it
// printed reasons - while every bypass being fixed that session was still wide
// open in the binary doing the enforcing. Nothing anywhere said so.
//
// A stale guard is worse than an absent one: an absent guard is noticed within a
// command or two, and a stale one is trusted indefinitely. So this check reports
// the resolved path always, not only on failure, because the question it answers
// ("which binary is judging me?") has no other way to be asked.
//
// The staleness test is deliberately coarse - binary mtime against the newest
// tracked .go file - because it only has to catch "you edited the guard and did
// not rebuild", which is the case that actually bites.
func (r *runner) checkGuardBinary() Check {
	const name = "guard binary"

	bin := filepath.Join(r.ws.Root(), "magus")
	if info, err := os.Stat(bin); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		if found, lookErr := exec.LookPath("magus"); lookErr == nil {
			return Check{Name: name, Status: StatusOK, Message: "hook would run " + found + " (from PATH; no ./magus built)"}
		}
		return Check{
			Name:    name,
			Status:  StatusFail,
			Message: "no ./magus and no magus on PATH, so a guard hook is unenforced",
			Details: []string{"build one: magus run build ."},
		}
	}

	info, err := os.Stat(bin)
	if err != nil {
		return Check{Name: name, Status: StatusFail, Message: err.Error()}
	}
	newest, newestPath := newestGoSource(r.ws.Root())
	if !newest.IsZero() && info.ModTime().Before(newest) {
		return Check{
			Name:    name,
			Status:  StatusFail,
			Message: "./magus is older than the working tree, so guard verdicts come from stale rules",
			Details: []string{
				"binary:  " + info.ModTime().Format(time.RFC3339),
				"newest:  " + newest.Format(time.RFC3339) + "  (" + newestPath + ")",
				"rebuild: magus run build .",
			},
		}
	}
	return Check{Name: name, Status: StatusOK, Message: "hook would run ./magus, newer than every tracked Go source"}
}

// newestGoSource returns the modification time of the most recently changed .go
// file in the tree, skipping the directories that never hold guard sources.
func newestGoSource(root string) (time.Time, string) {
	var newest time.Time
	var at string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is not a doctor failure
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "gen", ".claude":
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // ditto
		}
		if info.ModTime().After(newest) {
			newest, at = info.ModTime(), path
		}
		return nil
	})
	if at != "" {
		if rel, err := filepath.Rel(root, at); err == nil {
			at = rel
		}
	}
	return newest, at
}

// selfStalingScanLimits bound what checkSelfStalingOutputs is willing to read. A workspace
// can declare thousands of output files, and doctor runs interactively, so an unbounded scan
// would turn a health check into a build step. The caps are generous enough that a real
// finding is not missed in practice: the pattern this looks for is a provenance line in a
// rendered text file, and those are small.
const (
	selfStalingMaxFiles    = 5000
	selfStalingMaxFileSize = 1 << 20 // 1 MiB: past this it is a bundle or a binary, not a page
	selfStalingMaxReported = 10
)

// checkSelfStalingOutputs is MGS1019: a COMMITTED generated file whose bytes contain this
// repository's own HEAD commit, which is a build that can never be clean.
//
// The loop closes on itself. Committing a source change moves HEAD; HEAD is an input to the
// generated file; so the file committed alongside the source is stale the instant it lands.
// Regenerating and committing that moves HEAD again. Amending does not escape it either - a
// new hash restales the footer that recorded the previous one. The only fixed point is a
// second commit containing nothing but regenerated output, which is why a repository in this
// state grows a trail of "refresh generated metadata" commits. This one has several, made
// before anyone traced them to a cause.
//
// The tracked test carries the whole check. After the fix the same generator still writes the
// same commit hash into the same files; all that changed is that those files stopped being
// committed. A check that skipped the tracked test would fire on the repaired state as loudly
// as on the broken one, so a backend that cannot answer "is this path tracked?"
// (types.TrackedFileReporter) makes this check skip rather than guess.
//
// Matching HEAD's OWN hash is what keeps it precise. A lockfile pinning some other
// repository's commit, a vendored dependency, a test fixture full of hashes: none of them
// contain this repo's current HEAD, so none of them match.
func (r *runner) checkSelfStalingOutputs(projects []*types.Project) Check {
	const name = "self-staling outputs"

	res, err := vcs.Resolve(context.Background(), r.root, "", r.ws.VCSOptions())
	if err != nil || res.VCS == nil {
		return Check{Name: name, Status: StatusOK, Message: "no VCS resolved; nothing to check"}
	}
	reporter, ok := res.VCS.(types.TrackedFileReporter)
	if !ok {
		return Check{Name: name, Status: StatusOK, Message: fmt.Sprintf("%s cannot report tracked paths; skipped", res.VCS.Name())}
	}
	meta, err := res.VCS.Metadata(context.Background(), r.root)
	if err != nil || meta.Hash == "" {
		return Check{Name: name, Status: StatusOK, Message: "no commit yet; nothing to check"}
	}

	var details []string
	scanned := 0
	for _, p := range projects {
		rels := declaredOutputFiles(p)
		if len(rels) == 0 {
			continue
		}
		tracked, err := reporter.TrackedFiles(context.Background(), p.Dir, rels)
		if err != nil {
			continue
		}
		for _, rel := range tracked {
			if scanned >= selfStalingMaxFiles {
				break
			}
			scanned++
			if !fileRecordsCommit(filepath.Join(p.Dir, filepath.FromSlash(rel)), meta) {
				continue
			}
			details = append(details, fmt.Sprintf("%s: %s is committed and records this repository's own HEAD commit",
				types.ProjectDisplayName(p.Path, p.Name, p.Dir), rel))
		}
	}
	if len(details) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no committed output records the current commit"}
	}
	slices.Sort(details)
	total := len(details)
	if len(details) > selfStalingMaxReported {
		details = details[:selfStalingMaxReported]
	}
	return Check{
		Name:   name,
		Status: StatusFail,
		Message: fmt.Sprintf(
			"%d committed output file(s) record the commit that produced them, so regenerating after a commit always drifts; untrack them or drop the VCS stamp (see %s)",
			total, types.CodeURL(types.SelfStalingOutput)),
		Details: details,
	}
}

// declaredOutputFiles expands a project's declared output globs to the project-relative files
// that exist now. AllOutputs (not p.Outputs) because an output declared per-target with
// ctx.writesFiles is just as committable as a project-wide one, and the per-target form is
// the one this workspace actually uses.
func declaredOutputFiles(p *types.Project) []string {
	var rels []string
	for _, glob := range p.AllOutputs() {
		hits, err := filepath.Glob(filepath.Join(p.Dir, filepath.FromSlash(strings.ReplaceAll(glob, "**", "*"))))
		if err != nil {
			continue
		}
		for _, hit := range hits {
			info, err := os.Stat(hit)
			if err != nil || info.IsDir() {
				continue
			}
			rel, err := filepath.Rel(p.Dir, hit)
			if err != nil {
				continue
			}
			rels = append(rels, filepath.ToSlash(rel))
		}
	}
	slices.Sort(rels)
	return slices.Compact(rels)
}

// fileRecordsCommit reports whether path's bytes contain HEAD's short or full hash.
//
// Oversized and binary files are skipped rather than searched: a provenance stamp lands in
// rendered text, and reading a multi-megabyte bundle to look for a 40-character string is
// cost with no corresponding finding. The NUL probe is the usual cheap binary test.
func fileRecordsCommit(path string, meta types.VCSMeta) bool {
	info, err := os.Stat(path)
	if err != nil || info.Size() > selfStalingMaxFileSize {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	head := data
	if len(head) > 1024 {
		head = head[:1024]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	if meta.Hash != "" && bytes.Contains(data, []byte(meta.Hash)) {
		return true
	}
	// The short hash only counts when it is not part of a longer hex run, or every file
	// holding any 7-hex-digit substring of a longer id would match.
	return meta.ShortHash != "" && containsShortHash(data, meta.ShortHash)
}

// containsShortHash finds the short hash as a standalone hex token: the byte on each side
// must not itself be a hex digit, so an abbreviation is not matched inside a full hash or
// inside some other longer identifier.
func containsShortHash(data []byte, short string) bool {
	needle := []byte(short)
	for i := 0; ; {
		j := bytes.Index(data[i:], needle)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(needle)
		if !isHexByte(data, start-1) && !isHexByte(data, end) {
			return true
		}
		i = start + 1
	}
}

func isHexByte(data []byte, i int) bool {
	if i < 0 || i >= len(data) {
		return false
	}
	c := data[i]
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
