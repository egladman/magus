package doctor

import (
	"cmp"
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

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/ast"
	"github.com/egladman/magus/internal/cache/reflink"
	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

func (*runner) checkLanguageCoverage(projects []*types.Project) Check {
	var noLang []string
	for _, p := range projects {
		if p.Spell == "" {
			noLang = append(noLang, p.Path)
		}
	}
	if len(noLang) == 0 {
		return Check{Name: "language coverage", Status: StatusOK, Message: "every project matched a spell"}
	}
	slices.Sort(noLang)
	return Check{
		Name:    "language coverage",
		Status:  StatusWarn,
		Message: fmt.Sprintf("%d project(s) without a language pack", len(noLang)),
		Details: noLang,
	}
}

// checkCITarget fails when no project in the workspace declares a `ci` target.
// ci is the anchor `magus ci` / `magus affected ci` / `magus affected --plan`
// key off; a workspace that defines none would run that gate as a silent no-op
// (exit 0 having gated nothing). The run-time path enforces the same rule (it
// returns MGS1001); this surfaces it as a workspace health check so the gap is
// visible before CI runs. Detection reuses the magusfile source scan that
// checkTargetNameConventions relies on — ci lives in the magusfile, never in a
// spell. Name matching is case-insensitive because magus normalizes CI/Ci to ci.
func (*runner) checkCITarget(projects []*types.Project) Check {
	const name = "ci target"
	if len(projects) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no projects; skipped"}
	}
	norm := types.DefaultTargetNameNormalizer.NormalizeTargetName
	for _, p := range projects {
		for _, f := range magusfileSourcesInDir(p.Dir) {
			for _, decl := range declaredTargetNames(f) {
				// Normalize the raw identifier the same way the runtime does
				// (CI/Ci → ci) before comparing, so a magusfile that declares
				// the ci target in any casing is recognized.
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
			`define one in your magusfile, e.g.  export fun ci(_a: [str]) > void { magus.needs(magus.target.literal("build"), magus.target.literal("test"), magus.target.literal("lint")) }`,
			"run 'magus describe targets' to see the available stages to compose",
			fmt.Sprintf("see %s: %s", types.NoCITarget, types.NoCITarget.URL()),
		},
	}
}

// checkSpellDocs requires a doc comment on every function-handler target of each
// workspace-local Buzz spell. Only those targets opt in (DocRequiredTargets) —
// built-ins and record-style {cmd,args} ops, whose handler comments
// aren't captured, are skipped — so the check enforces the convention exactly
// where the Buzz interpreter can verify it.
func (*runner) checkSpellDocs(spells []*types.Spell) Check {
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

// checkShellCompletion warns when no magus tab-completion setup is detected for
// the user's shell. It is best-effort — a normal magus run has no reliable signal
// for whether completion is active in the current shell, so this scans the common
// rc files / completion dirs for a marker. A non-standard install can therefore
// produce a false warning, which is why it warns (never fails) and skips unknown
// shells entirely.
func (*runner) checkShellCompletion() Check {
	const name = "shell completion"
	shell := filepath.Base(os.Getenv("SHELL"))
	home, _ := os.UserHomeDir()
	if home == "" || (shell != "bash" && shell != "zsh" && shell != "fish") {
		return Check{Name: name, Status: StatusOK, Message: "skipped (no supported shell detected)"}
	}
	if shellCompletionInstalled(shell, home) {
		return Check{Name: name, Status: StatusOK, Message: fmt.Sprintf("%s tab-completion detected", shell)}
	}
	install := fmt.Sprintf("magus completion %s >> ~/.%src", shell, shell)
	if shell == "fish" {
		install = "magus completion fish > ~/.config/fish/completions/magus.fish"
	}
	return Check{
		Name:    name,
		Status:  StatusWarn,
		Message: fmt.Sprintf("no magus tab-completion detected for %s", shell),
		Details: []string{"enable it: " + install},
	}
}

// shellCompletionInstalled reports whether a magus completion marker is present
// in the standard locations for shell. Markers cover both the appended-script
// (`_magus_complete`, `complete -c magus`) and source-eval (`magus completion`)
// install styles.
func shellCompletionInstalled(shell, home string) bool {
	markers := []string{"magus completion", "_magus_complete", "complete -c magus"}
	var files []string
	switch shell {
	case "bash":
		files = []string{
			filepath.Join(home, ".bashrc"),
			filepath.Join(home, ".bash_profile"),
			filepath.Join(home, ".bash_completion"),
			filepath.Join(home, ".profile"),
			filepath.Join(home, ".local/share/bash-completion/completions/magus"),
			"/etc/bash_completion.d/magus",
			"/usr/share/bash-completion/completions/magus",
		}
	case "zsh":
		zdot := os.Getenv("ZDOTDIR")
		if zdot == "" {
			zdot = home
		}
		files = []string{
			filepath.Join(zdot, ".zshrc"),
			filepath.Join(zdot, ".zprofile"),
			filepath.Join(home, ".zshrc"),
		}
	case "fish":
		// fish auto-loads this path, so its presence alone is a strong signal.
		if _, err := os.Stat(filepath.Join(home, ".config", "fish", "completions", "magus.fish")); err == nil {
			return true
		}
		files = []string{filepath.Join(home, ".config", "fish", "config.fish")}
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range markers {
			if strings.Contains(string(data), m) {
				return true
			}
		}
	}
	return false
}

func (r *runner) checkExplicitVCS() Check {
	return checkExplicitVCS(r.ws.Root(), r.ws.VCSOptions())
}

func checkExplicitVCS(root string, opts types.VCSOptions) Check {
	res, err := vcs.Resolve(context.Background(), root, "", opts)
	if err != nil {
		return Check{Name: "vcs", Status: StatusFail, Message: err.Error()}
	}
	switch res.Source {
	case types.VCSSourceDisabled:
		return Check{Name: "vcs", Status: StatusOK, Message: "disabled (vcs.enabled: false); affected falls back to all"}
	case types.VCSSourceExplicit:
		return Check{Name: "vcs", Status: StatusOK, Message: fmt.Sprintf("pinned: %s (base_ref %s)", res.Name, res.Base)}
	case types.VCSSourceAuto:
		return Check{Name: "vcs", Status: StatusWarn, Message: fmt.Sprintf("auto-detected: %s (set MAGUS_VCS_NAME to pin)", res.Name)}
	case types.VCSSourceDefault:
		return Check{Name: "vcs", Status: StatusWarn, Message: fmt.Sprintf("no VCS marker found at %s; falling back to %s", root, res.Name)}
	default:
		return Check{Name: "vcs", Status: StatusWarn, Message: fmt.Sprintf("unexpected vcs source %q (%s)", res.Source, res.Name)}
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

// checkSymlinks fails on symlinks whose resolved target escapes root. They are
// a sandbox-escape vector where landlock is unavailable. In-tree symlinks are
// reported as context, since project discovery skips them.
func checkSymlinks(root string) Check {
	var escaping, inTree []string
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
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
		return Check{Name: "symlinks", Status: StatusWarn, Message: fmt.Sprintf("could not scan for symlinks: %v", walkErr)}
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

func (*runner) checkMagefileCoexistence(_ []*types.Project) Check {
	return Check{Name: "magefile coexistence", Status: StatusOK, Message: "single magusfile.buzz layout enforced"}
}

func (*runner) checkLegacyMageForms(_ []*types.Project) Check {
	return Check{Name: "legacy mage forms", Status: StatusOK, Message: "workspace uses Buzz magusfiles"}
}

func (*runner) checkMixedMageforms(_ []*types.Project) Check {
	return Check{Name: "consistent mage forms", Status: StatusOK, Message: "workspace uses Buzz magusfiles"}
}

func (*runner) checkVariadicMageTargets(_ []*types.Project) Check {
	return Check{Name: "variadic mage targets", Status: StatusOK, Message: "Buzz targets receive args as a list — no variadic conflicts"}
}

func (r *runner) checkWatchBackend() Check {
	if watch.ProbeBackend(r.ws.Root()) == watch.FsnotifyBackend {
		return Check{Name: "watch backend", Status: StatusOK, Message: "OS-level events available (fsnotify)"}
	}
	return Check{
		Name:    "watch backend",
		Status:  StatusWarn,
		Message: "OS-level filesystem events unavailable; magus watch will fall back to polling (1 s interval)",
		Details: []string{"common causes: NFS, FUSE, WSL2 — pass --backend=poll to magus watch to silence this warning"},
	}
}

func (r *runner) checkBinarySigned() Check {
	if r.opts.version == "unknown" || r.opts.commit == "unknown" {
		return Check{Name: "binary signature", Status: StatusWarn, Message: "binary is unsigned; it cannot be trusted"}
	}
	return Check{
		Name:    "binary signature",
		Status:  StatusOK,
		Message: fmt.Sprintf("%s (%s)", r.opts.version, r.opts.commit),
	}
}

func (r *runner) checkBinaryTree() Check {
	if strings.HasSuffix(r.opts.commit, "-dirty") {
		return Check{
			Name:    "binary git tree",
			Status:  StatusWarn,
			Message: fmt.Sprintf("binary was built from a dirty git tree (%s); it cannot be trusted", r.opts.commit),
		}
	}
	return Check{Name: "binary git tree", Status: StatusOK, Message: "clean"}
}

func (*runner) checkJSONCodec() Check {
	v := codec.CodecVersion()
	msg := "encoding/json " + v
	if v == "v2" {
		msg += " (GOEXPERIMENT=jsonv2; faster marshaling)"
	}
	return Check{Name: "json codec", Status: StatusOK, Message: msg}
}

// checkReflinkSupport verifies whether the cache directory's filesystem
// supports CoW reflinks. Reflinks make cache replays O(1); without them
// replayBlob falls back to hard-link then io.Copy.
func (r *runner) checkReflinkSupport() Check {
	cacheDir := filepath.Join(r.root, ".magus")
	if d := r.opts.cfg.Cache.Dir; d != "" {
		if filepath.IsAbs(d) {
			cacheDir = filepath.Clean(d)
		} else {
			cacheDir = filepath.Join(r.root, d)
		}
	}
	if _, err := os.Stat(cacheDir); errors.Is(err, os.ErrNotExist) {
		return Check{
			Name:    "reflink support",
			Status:  StatusOK,
			Message: "cache dir not yet created; check skipped",
		}
	}
	if reflink.Probe(cacheDir) {
		return Check{
			Name:    "reflink support",
			Status:  StatusOK,
			Message: "filesystem supports CoW; cache replays are O(1)",
		}
	}
	return Check{
		Name:    "reflink support",
		Status:  StatusWarn,
		Message: "cache replay falls back to hardlink/copy; btrfs, xfs (reflink=1), or APFS give O(1) replays",
	}
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

func (r *runner) checkCacheWritable() Check {
	cacheDir := filepath.Join(r.root, ".magus")
	if d := r.opts.cfg.Cache.Dir; d != "" {
		if filepath.IsAbs(d) {
			cacheDir = filepath.Clean(d)
		} else {
			cacheDir = filepath.Join(r.root, d)
		}
	}
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
			Status:  StatusWarn,
			Message: fmt.Sprintf("base_ref %q not reachable (set MAGUS_VCS_BASE_REF to a reachable ref)", res.Base),
			Details: []string{fmt.Sprintf("%s exited: %v", res.Name, err)},
		}
	}

	if res.Name == "git" {
		dctx, dcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer dcancel()
		if err := exec.CommandContext(dctx, "git", "-C", root, "symbolic-ref", "--quiet", "HEAD").Run(); err != nil {
			return Check{
				Name:    "vcs base ref",
				Status:  StatusWarn,
				Message: fmt.Sprintf("base_ref %q resolves but HEAD is detached; affected diff base may be unexpected", res.Base),
			}
		}
	}

	return Check{Name: "vcs base ref", Status: StatusOK, Message: fmt.Sprintf("%s %q resolves", res.Name, res.Base)}
}

// Hardcoded thresholds for the graph health checks. These are tuned to be
// sensible across most monorepos; use health.exempt to suppress individual
// projects rather than raising the thresholds.
const (
	healthNearCycleDepth  = 3    // max cycle length to probe for near-cycles
	healthFanOutWarn      = 20   // direct-dependency count that triggers a warning
	healthBlastRadiusWarn = 0.20 // fraction of workspace above which a project warns
	healthNCCDWarn        = 2.0  // Normalized CCD above which workspace-wide tangle warns
)

func (r *runner) checkNearCycles(g *types.Graph) Check {
	const depth = healthNearCycleDepth
	ncs := g.NearCycles(context.Background(), depth)
	if len(ncs) == 0 {
		return Check{
			Name:    "near-cyclical edges",
			Status:  StatusOK,
			Message: fmt.Sprintf("no pairs within %d hops of a cycle", depth),
		}
	}
	details := make([]string, 0, min(5, len(ncs)))
	for i, nc := range ncs {
		if i >= 5 {
			break
		}
		hops := len(nc.BackPath)
		details = append(details, fmt.Sprintf(
			"%s -> %s would close a %d-cycle (path: %s)",
			nc.From, nc.To, hops, strings.Join(nc.BackPath, " -> "),
		))
	}
	if len(ncs) > 5 {
		details = append(details, fmt.Sprintf("(+%d more)", len(ncs)-5))
	}
	return Check{
		Name:    "near-cyclical edges",
		Status:  StatusWarn,
		Message: fmt.Sprintf("%d pair(s) within %d hops of forming a cycle", len(ncs), depth),
		Details: details,
	}
}

func (r *runner) checkFanOut(projects []*types.Project) Check {
	const threshold = healthFanOutWarn

	type offender struct {
		path   string
		fanOut int
	}
	var offenders []offender
	maxFO, maxPath := 0, ""
	for _, p := range projects {
		fo := len(p.DependsOn)
		if fo > maxFO {
			maxFO, maxPath = fo, p.Path
		}
		if fo > threshold {
			offenders = append(offenders, offender{p.Path, fo})
		}
	}
	if len(offenders) == 0 {
		msg := "all projects within threshold"
		if maxPath != "" {
			msg = fmt.Sprintf("max %d direct dep(s) in %s", maxFO, maxPath)
		}
		return Check{Name: "dependency fan-out", Status: StatusOK, Message: msg}
	}

	slices.SortFunc(offenders, func(a, b offender) int {
		if a.fanOut != b.fanOut {
			return cmp.Compare(b.fanOut, a.fanOut)
		}
		return cmp.Compare(a.path, b.path)
	})
	details := make([]string, 0, min(5, len(offenders)))
	for i, o := range offenders {
		if i >= 5 {
			break
		}
		details = append(details, fmt.Sprintf("%s: %d direct dep(s) (threshold: %d)", o.path, o.fanOut, threshold))
	}
	if len(offenders) > 5 {
		details = append(details, fmt.Sprintf("(+%d more)", len(offenders)-5))
	}
	return Check{
		Name:    "dependency fan-out",
		Status:  StatusWarn,
		Message: fmt.Sprintf("%d project(s) exceed %d direct dep(s)", len(offenders), threshold),
		Details: details,
	}
}

func (r *runner) checkBlastRadius(g *types.Graph, projects []*types.Project) Check {
	total := len(projects)
	if total == 0 {
		return Check{Name: "change blast radius", Status: StatusOK, Message: "no projects"}
	}
	const threshold = healthBlastRadiusWarn
	br := g.BlastRadius()

	exempt := make(map[string]struct{}, len(r.opts.cfg.Health.Exempt))
	for _, e := range r.opts.cfg.Health.Exempt {
		exempt[e] = struct{}{}
	}

	type offender struct {
		path  string
		count int
		pct   float64
	}
	var offenders []offender
	maxPct, maxPath := 0.0, ""
	for path, count := range br {
		if _, skip := exempt[path]; skip {
			continue
		}
		pct := float64(count) / float64(total)
		if pct > maxPct {
			maxPct, maxPath = pct, path
		}
		if pct > threshold {
			offenders = append(offenders, offender{path, count, pct})
		}
	}
	if len(offenders) == 0 {
		msg := "all projects within threshold"
		if maxPath != "" {
			msg = fmt.Sprintf("max %.0f%% blast radius (%s)", maxPct*100, maxPath)
		}
		return Check{Name: "change blast radius", Status: StatusOK, Message: msg}
	}

	slices.SortFunc(offenders, func(a, b offender) int {
		if a.pct != b.pct {
			return cmp.Compare(b.pct, a.pct)
		}
		return cmp.Compare(a.path, b.path)
	})
	details := make([]string, 0, min(5, len(offenders)))
	for i, o := range offenders {
		if i >= 5 {
			break
		}
		details = append(details, fmt.Sprintf("%s: %d/%d projects (%.0f%%)", o.path, o.count, total, o.pct*100))
	}
	if len(offenders) > 5 {
		details = append(details, fmt.Sprintf("(+%d more)", len(offenders)-5))
	}
	return Check{
		Name:    "change blast radius",
		Status:  StatusWarn,
		Message: fmt.Sprintf("%d project(s) rebuild >%.0f%% of the workspace when changed", len(offenders), threshold*100),
		Details: details,
	}
}

func (r *runner) checkDependencyTangle(g *types.Graph) Check {
	nccd := g.NCCD()
	const threshold = healthNCCDWarn
	msg := fmt.Sprintf("NCCD=%.2f (healthy: <%.1f)", nccd, threshold)
	if nccd > threshold {
		return Check{
			Name:    "dependency tangle (NCCD)",
			Status:  StatusWarn,
			Message: msg,
			Details: []string{"NCCD > 1.0 indicates more coupling than a balanced binary tree; > 2.0 suggests significant tangling that may slow CI"},
		}
	}
	return Check{Name: "dependency tangle (NCCD)", Status: StatusOK, Message: msg}
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
		// MAGUS_LEVEL is injected by magus into every subprocess (the recursion
		// depth, à la GNU Make's MAKELEVEL; see internal/run SelfVars). It's a
		// runtime signal, not a config field, so it won't be in KnownEnvVars — a
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
		Status:  StatusWarn,
		Message: fmt.Sprintf("%d unknown MAGUS_* variable(s); typos?", len(unknown)),
		Details: unknown,
	}
}

// checkTargetNameConventions warns when a workspace declares target functions
// using more than one naming convention (snake_case, camelCase, PascalCase).
// magus normalizes all of them to the same canonical kebab-case target, so this
// never breaks resolution — but a consistent source convention keeps invocations
// greppable. Single-word, all-lowercase names (build, test) are convention-neutral
// and ignored.
func (r *runner) checkTargetNameConventions(projects []*types.Project) Check {
	conventions := map[string]string{} // convention → first "name (file)" example
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
		Status: StatusWarn,
		Message: fmt.Sprintf("target names mix %d naming conventions; magus normalizes all to "+
			"kebab-case so resolution still works, but pick one for greppable invocations", len(conventions)),
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
		return "" // all lowercase, no delimiter — neutral
	}
	if unicode.IsUpper(rune(name[0])) {
		return "PascalCase"
	}
	return "camelCase"
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
// legitimately use embedding-only constructs — top-level statements and
// unlabeled host calls — that upstream-strict parsing rejects; the check still
// catches the unconditional strict-parity errors (untyped params, reserved-word
// bindings, omitted return arrows, non-optional fiber yields) and plain syntax
// errors.
//
// Every magusfile is parsed before the check returns, so a single run yields a
// comprehensive report of everything wrong rather than stopping at the first
// failure. This is what makes it useful in the CI preflight target: one `magus
// doctor` surfaces all magusfile problems in one pass.
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
// attach to a target with a ":" suffix (magus run lint:rw), so a charm that
// shares a target name makes invocations ambiguous to read and a pain to debug:
// `magus run cd` (the target) versus `magus run build:cd` (the charm).
// The charm set is magus's reserved built-ins (write, cd) plus every charm a
// target body branches on via has_charm; collisions are compared on the canonical
// name both sides normalize to.
func (r *runner) checkCharmTargetCollision(projects []*types.Project) Check {
	targets := map[string]string{} // normalized name → first raw name seen
	charms := map[string]string{}  // normalized name → first raw name seen
	for _, c := range types.ReservedCharms() {
		charms[types.NormalizeCharmName(c)] = c
	}
	for _, p := range projects {
		for _, f := range magusfileSourcesInDir(p.Dir) {
			for _, name := range declaredTargetNames(f) {
				n := types.NormalizeCharmName(name)
				if _, seen := targets[n]; !seen {
					targets[n] = name
				}
			}
			for _, name := range declaredCharmNames(f) {
				n := types.NormalizeCharmName(name)
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
		Status: StatusWarn,
		Message: fmt.Sprintf("%d charm name(s) also name a target; the `target:charm` suffix "+
			"makes these ambiguous to read and debug — rename one side", len(details)),
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

// checkMergeDriver warns when one or more projects declare Outputs but the
// VCS merge driver is not installed. This check is informational (warn, not
// fail): conflicts in generated outputs are annoying but resolvable manually.
func (r *runner) checkMergeDriver() Check {
	if r.ws == nil {
		return Check{Name: "merge driver", Status: StatusOK, Message: "workspace unavailable; skipped"}
	}
	// Count projects with declared outputs.
	var withOutputs int
	for _, p := range r.ws.All() {
		if len(p.Outputs) > 0 {
			withOutputs++
		}
	}
	if withOutputs == 0 {
		return Check{Name: "merge driver", Status: StatusOK, Message: "no projects declare Outputs; nothing to wire"}
	}

	res, err := vcs.Resolve(context.Background(), r.root, "", r.ws.VCSOptions())
	if err != nil || res.VCS == nil {
		return Check{Name: "merge driver", Status: StatusOK, Message: "vcs unavailable; skipped"}
	}

	installer, ok := res.VCS.(types.MergeDriverInstaller)
	if !ok {
		return Check{
			Name:    "merge driver",
			Status:  StatusOK,
			Message: fmt.Sprintf("%s does not support automatic merge-driver installation", res.Name),
		}
	}

	installed, err := installer.CheckMergeDriver(context.Background(), r.root)
	if err != nil {
		return Check{Name: "merge driver", Status: StatusWarn, Message: fmt.Sprintf("check failed: %v", err)}
	}
	if !installed {
		return Check{
			Name:    "merge driver",
			Status:  StatusWarn,
			Message: fmt.Sprintf("%d project(s) declare Outputs but the merge driver is not installed", withOutputs),
			Details: []string{
				"run `magus init` once per clone to regenerate conflicted outputs automatically",
			},
		}
	}
	return Check{
		Name:    "merge driver",
		Status:  StatusOK,
		Message: fmt.Sprintf("installed (%d project(s) with declared outputs)", withOutputs),
	}
}

func (r *runner) checkMCPServer() Check {
	s := r.opts.mcpStatus
	if s == nil || !s.Compiled {
		return Check{
			Name:    "MCP server",
			Status:  StatusOK,
			Message: "not compiled in (build without -tags mcp)",
		}
	}
	if !s.Enabled {
		return Check{
			Name:    "MCP server",
			Status:  StatusOK,
			Message: "disabled via mcp.enabled=false",
		}
	}
	addr := s.Address
	if addr == "" {
		addr = "127.0.0.1:7391"
	}
	if !s.DaemonUp {
		return Check{
			Name:    "MCP server",
			Status:  StatusWarn,
			Message: "MCP is only served in daemon mode; run `magus server start` to enable",
			Details: []string{
				"once running, MCP will be available at http://" + addr + "/mcp",
				`Claude Desktop / IDE: { "type": "streamable-http", "url": "http://` + addr + `/mcp" }`,
			},
		}
	}
	return Check{
		Name:    "MCP server",
		Status:  StatusOK,
		Message: "daemon is running; MCP available at http://" + addr + "/mcp",
	}
}

// checkDaemonReachability dials the configured daemon socket and reports
// whether it's alive. Warns if the socket is configured but unreachable, or
// if the daemon's version differs from this binary's version. No daemon is not
// an error — this check is informational.
func (r *runner) checkDaemonReachability() Check {
	d := r.opts.daemonInfo
	if d == nil {
		return Check{Name: "daemon", Status: StatusOK, Message: "no daemon configured"}
	}
	if !d.Reachable {
		return Check{
			Name:    "daemon",
			Status:  StatusWarn,
			Message: fmt.Sprintf("daemon not responding at %s", d.SockAddr),
		}
	}
	msg := fmt.Sprintf("pid %d  capacity %d  in-use %d  waiting %d", d.ParentPID, d.Capacity, d.InUse, d.Waiting)
	if d.DaemonVersion != "" && r.opts.version != "" && d.DaemonVersion != r.opts.version {
		return Check{
			Name:    "daemon",
			Status:  StatusWarn,
			Message: msg,
			Details: []string{
				fmt.Sprintf("version skew: daemon %s vs CLI %s", d.DaemonVersion, r.opts.version),
				"run `magus server stop && magus server start` to restart with the current binary",
			},
		}
	}
	return Check{Name: "daemon", Status: StatusOK, Message: msg}
}

// checkConcurrencyBudget warns when this workspace's configured concurrency
// exceeds the daemon's effective capacity, or when the pool is currently
// saturated. Skipped when no daemon is running.
func (r *runner) checkConcurrencyBudget() Check {
	d := r.opts.daemonInfo
	if d == nil || !d.Reachable {
		return Check{Name: "concurrency budget", Status: StatusOK, Message: "no daemon running"}
	}
	wsConcurrency := r.opts.cfg.Concurrency
	if wsConcurrency <= 0 {
		wsConcurrency = 0 // "default" — not a mismatch
	}
	var details []string
	if wsConcurrency > 0 && wsConcurrency > d.Capacity {
		details = append(details, fmt.Sprintf(
			"workspace concurrency %d exceeds daemon capacity %d — lower concurrency in magus.yaml or restart daemon with higher --concurrency",
			wsConcurrency, d.Capacity,
		))
	}
	if d.Waiting > 0 && d.InUse >= d.Capacity {
		details = append(details, fmt.Sprintf(
			"%d task(s) waiting for a slot — daemon pool is saturated (capacity %d)",
			d.Waiting, d.Capacity,
		))
	}
	if len(details) > 0 {
		return Check{
			Name:    "concurrency budget",
			Status:  StatusWarn,
			Message: fmt.Sprintf("daemon capacity %d / in-use %d / waiting %d", d.Capacity, d.InUse, d.Waiting),
			Details: details,
		}
	}
	return Check{
		Name:    "concurrency budget",
		Status:  StatusOK,
		Message: fmt.Sprintf("daemon capacity %d  in-use %d  waiting %d", d.Capacity, d.InUse, d.Waiting),
	}
}

// checkWorkspaceRegistration reports whether this workspace is currently
// loaded in the multi-workspace daemon and how many other workspaces are
// present. Informational only — a workspace not yet loaded is normal (it
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

// checkStaleSockets scans the magus socket directory and warns when there are
// stale (dead) per-process sockets alongside a running stable daemon, or when
// multiple live daemons are detected. With --fix, removes dead sockets.
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
		return Check{Name: "sockets", Status: StatusWarn, Message: fmt.Sprintf("scan %s: %v", sockDir, err)}
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

	if r.opts.fix && len(stale) > 0 {
		for _, p := range stale {
			_ = os.Remove(p)
		}
		details = append(details, fmt.Sprintf("removed %d stale socket(s)", len(stale)))
	}

	status := StatusWarn
	if len(live) > 1 {
		status = StatusFail
	}
	msg := fmt.Sprintf("%d stale socket(s)", len(stale))
	if len(live) > 1 {
		msg = fmt.Sprintf("%d live daemon sockets — multiple daemons running", len(live))
	}
	return Check{Name: "sockets", Status: status, Message: msg, Details: details}
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

// applyFixes attempts to auto-remediate fixable checks.
func (*runner) applyFixes(_ []*types.Project) []FixResult {
	return nil
}
