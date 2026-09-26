package main

import (
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/types"
)

const (
	rootMagusfile = "magusfile.buzz"
	// embeddedSkillDir is the shipped set: one directory per skill magus installs.
	embeddedSkillDir = "internal/agent/skills"
	// fullTwinSuffix mirrors internal/agent's: an install writes each skill's
	// always-full twin as <name><suffix>. Copied rather than imported, because
	// internal/agent links the socket directory; main_test.go pins the two equal.
	fullTwinSuffix = "-full"
)

// magusfileSkipDirs are trees whose magusfiles are not this repository's
// projects: installed agent copies, dependencies, fixtures and state.
var magusfileSkipDirs = map[string]bool{
	".git": true, ".magus": true, ".claude": true, ".agents": true, ".opencode": true,
	"node_modules": true, "gen": true, "testdata": true,
}

// installCall matches a package-manager install run from a magusfile, directly,
// through an install() helper, or through a spell's install op.
var installCall = regexp.MustCompile(`\binstall\(\)|"(pnpm|npm|yarn)",\s*\["(install|ci)"|\["(pnpm-install|npm-ci|go-mod-download|uv-sync|cargo-fetch)"\]`)

// exportedTarget matches the head of a target definition, capturing its name.
var exportedTarget = regexp.MustCompile(`(?m)^export fun (\w+)\(`)

// packageInstallTargetsNeverReplay: a target that installs packages must never
// replay. Its effect is node_modules, which no cache entry records, so a hit on a
// fresh checkout (a restored CI store, a remote-tier entry) restores nothing and
// the next target runs without its packages. That is how the console shard of
// #268 failed: preflight hit, build ran, esbuild could not resolve
// @connectrpc/connect.
func packageInstallTargetsNeverReplay(fsys fs.FS) []finding {
	var files []string
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		if d.IsDir() {
			if magusfileSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() == rootMagusfile {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		return []finding{{path: ".", problem: "no magusfile.buzz found", fix: "Run from the repository root."}}
	}

	var findings []finding
	checked := 0
	for _, p := range files {
		src, bad := readFile(fsys, p)
		if bad != nil {
			findings = append(findings, bad...)
			continue
		}
		for _, m := range exportedTarget.FindAllStringSubmatchIndex(src, -1) {
			name := src[m[2]:m[3]]
			if !installCall.MatchString(funBody(src, m[1])) {
				continue
			}
			checked++
			if regexp.MustCompile(`"` + regexp.QuoteMeta(name) + `":\s*\{\s*"skip_cache"`).MatchString(src) {
				continue
			}
			findings = append(findings, finding{path: p, line: lineAt(src, m[0]),
				problem: fmt.Sprintf("target %q installs packages but may replay", name),
				fix: "Its effect is node_modules, which no cache entry records, so a hit on a fresh checkout\n" +
					"restores nothing. Declare \"" + name + "\": {\"skip_cache\": \"<why>\"} in the project's targets."})
		}
	}
	if checked == 0 {
		findings = append(findings, finding{path: ".", problem: "no install target found",
			fix: "The installCall pattern no longer matches how projects install; update it."})
	}
	return findings
}

// funBody returns the brace-balanced body starting at the first "{" at or after from.
func funBody(src string, from int) string {
	open := strings.IndexByte(src[from:], '{')
	if open < 0 {
		return ""
	}
	depth := 0
	for i := from + open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[from+open : i+1]
			}
		}
	}
	return src[from+open:]
}

// wholeTreeFootprints are the root targets that compile or analyze the entire Go
// module, mapped to the globs whose absence from the target's own footprint would
// make a Go edit replay instead of re-measure. Every other readsFiles in the root
// magusfile narrows deliberately (compress-cgo-test to one package, lint-build to
// libs/testlayout) and must keep its narrow footprint.
var wholeTreeFootprints = map[string][]string{
	"test": {"**/*.go", "go.mod", "go.sum"},
	"lint": {"**/*.go", "go.mod", "go.sum", ".golangci.yml"},
}

// wholeTreeTargetsKeyOnTheGoTree pins the footprint replacement that once made
// this repository's own suite report a false green.
//
// ctx.readsFiles REPLACES a target's footprint rather than adding to it: buildStep
// keeps the magusfiles and the target's spell sources, drops the project and spell
// globs, then folds the declared refs in. So a call added to key four files the
// project does not claim silently deleted **/*.go from the key of the target that
// runs every Go test, and ~8,400 new lines of *_test.go replayed against the cached
// verdict. `magus affected` still selected "." for those edits, because project
// claims and a target's key are computed separately, which is what kept it invisible.
//
// Structural rather than a text scan: it reads the same static extraction buildStep
// depends on, so a footprint moved into a helper or reworded still gets graded.
func wholeTreeTargetsKeyOnTheGoTree(fsys fs.FS) []finding {
	src, bad := readFile(fsys, rootMagusfile)
	if bad != nil {
		return bad
	}
	nodes := describe.Extract(src)
	if len(nodes) == 0 {
		return []finding{{path: rootMagusfile, problem: "yielded no target nodes; the parse failed"}}
	}
	byName := make(map[string]types.TargetGraphNode, len(nodes))
	for _, n := range nodes {
		byName[n.Name] = n
	}

	var findings []finding
	for _, target := range slices.Sorted(maps.Keys(wholeTreeFootprints)) {
		line := lineOf(src, "export fun "+target+"(")
		node, ok := byName[target]
		if !ok {
			findings = append(findings, finding{path: rootMagusfile, problem: fmt.Sprintf("declares no %q target", target),
				fix: "Update wholeTreeFootprints if the target was renamed."})
			continue
		}
		if len(node.ReadsFiles) == 0 {
			continue // no replacement, so the target still inherits the project baseline
		}
		var declared []string
		for _, ref := range node.ReadsFiles {
			declared = append(declared, ref.Glob)
		}
		for _, glob := range wholeTreeFootprints[target] {
			if slices.Contains(declared, glob) {
				continue
			}
			findings = append(findings, finding{path: rootMagusfile, line: line,
				problem: fmt.Sprintf("target %q declares a ctx.readsFiles footprint that does not name %q", target, glob),
				fix: "A footprint REPLACES the project and spell source globs, so an edit to it produces a cache HIT\n" +
					"and the target replays a verdict computed against different code. Restate the tree the\n" +
					"target actually reads, or drop the footprint declaration entirely."})
		}
	}
	return findings
}

// rootSourcesBlock captures the root project's declared source globs. One
// `"sources"` key exists in the root magusfile, so the first match is it.
var rootSourcesBlock = regexp.MustCompile(`(?s)"sources":\s*\[(.*?)\]`)

// quotedString matches one glob inside that block; a trailing `// ...` carries
// no quotes, so comments are excluded by construction.
var quotedString = regexp.MustCompile(`"([^"]+)"`)

// rootToolConfigs are the paths that change what a root target DOES while
// matching no spell source glob, each with the tool that reads it.
//
// Deliberately absent: LICENSE is read only by release-build, which is
// skip_cache, so declaring it would invalidate build/test/lint to key nothing;
// .gitignore is read by no target at all. Both keep seeding by containment.
var rootToolConfigs = map[string]string{
	".golangci.yml":       "golangci-lint, through the go spell's lint op",
	".mockery.yaml":       "`go tool mockery`, in the generate target",
	".markdownlintignore": "markdownlint, through the markdown spell's lint op",
	"dprint-base.json":    "dprint (dprint.json extends it; the spell declares only dprint.json)",
	"mise.toml":           "every op in this project, through the toolchain it pins",
	"magus.yaml":          "every run, through the charm/cache/sandbox policy it resolves",
	"Dockerfile":          "image-build's amd64 variant",
	"Dockerfile.static":   "image-build's static multi-arch variant",
}

// rootProjectDeclaresTheConfigsItsToolsRead is this repository declining to be
// the example in its own diagnostic. An undeclared config produces a cache HIT
// on an edit to it (golangci-lint re-run under a new rule set, replaying the
// verdict computed under the old one) while still seeding the root project
// through directory containment and rerunning everything for nothing (MGS1028,
// and doctor's undeclared-seeding advice).
func rootProjectDeclaresTheConfigsItsToolsRead(fsys fs.FS) []finding {
	src, bad := readFile(fsys, rootMagusfile)
	if bad != nil {
		return bad
	}
	block := rootSourcesBlock.FindStringSubmatchIndex(src)
	if block == nil {
		return []finding{{path: rootMagusfile, problem: "declares no project-wide sources"}}
	}
	var declared []string
	for _, m := range quotedString.FindAllStringSubmatch(src[block[2]:block[3]], -1) {
		declared = append(declared, m[1])
	}
	var findings []finding
	for _, p := range slices.Sorted(maps.Keys(rootToolConfigs)) {
		if slices.Contains(declared, p) {
			continue
		}
		findings = append(findings, finding{path: rootMagusfile, line: lineAt(src, block[0]),
			problem: fmt.Sprintf("%s is read by %s but no root source glob names it", p, rootToolConfigs[p]),
			fix:     "Editing it reruns every root target while keying none of them. Declare it in the project's sources."})
	}
	return findings
}

// handAuthoredSkills live beside the installed ones and are NOT written by
// `magus agent install`. Each is tracked through an explicit .gitignore negation,
// and magus-workspace-rules tells readers to put local rules in exactly this
// shape, so a declaration that claims them tells an author the one file they are
// supposed to edit is generated, and hands it to the regenerating merge driver
// on a conflict.
var handAuthoredSkills = []string{"magus-skill-authoring", "magus-local-development", "land-pull-requests"}

// skillOutputGlob matches the declared-output patterns in skills_generate. The
// install calls in the same body name a bare destination directory with no
// trailing pattern, so they cannot match.
var skillOutputGlob = regexp.MustCompile(`"(\.(?:claude|agents|opencode)/skills/[^"]+)"`)

// skillDestinations are the directories an install writes skills into.
var skillDestinations = []string{".claude/skills", ".agents/skills", ".opencode/skills"}

// skillsGenerateDeclaresEveryShippedSkill keeps the declared outputs in step
// with what magus actually installs, in both directions. Naming the shipped
// skills rather than `.claude/skills/**` keeps the hand-authored ones out, but
// goes stale on its own, so adding a skill fails here rather than ships an
// installed file magus calls hand-editable.
func skillsGenerateDeclaresEveryShippedSkill(fsys fs.FS) []finding {
	src, bad := readFile(fsys, rootMagusfile)
	if bad != nil {
		return bad
	}
	var globs []string
	for _, m := range skillOutputGlob.FindAllStringSubmatch(src, -1) {
		globs = append(globs, m[1])
	}
	if len(globs) == 0 {
		return []finding{{path: rootMagusfile, problem: "declares no installed-skill outputs"}}
	}
	entries, err := fs.ReadDir(fsys, embeddedSkillDir)
	if err != nil {
		return []finding{{path: embeddedSkillDir, problem: "cannot read: " + err.Error()}}
	}
	matched := func(p string) bool {
		for _, g := range globs {
			if ok, _ := doublestar.Match(g, p); ok {
				return true
			}
		}
		return false
	}
	line := lineOf(src, "export fun skills_generate(")

	var findings []finding
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		for _, dest := range skillDestinations {
			for _, name := range []string{entry.Name(), entry.Name() + fullTwinSuffix} {
				p := dest + "/" + name + "/SKILL.md"
				if matched(p) {
					continue
				}
				findings = append(findings, finding{path: rootMagusfile, line: line,
					problem: fmt.Sprintf("%s ships %s, but no declared output covers %s", embeddedSkillDir, entry.Name(), p),
					fix: "An undeclared installed skill classifies as hand-editable source, and the merge\n" +
						"driver stops covering it. Add the pattern to skills_generate."})
			}
		}
	}
	for _, name := range handAuthoredSkills {
		for _, dest := range skillDestinations {
			p := dest + "/" + name + "/SKILL.md"
			if !matched(p) {
				continue
			}
			findings = append(findings, finding{path: rootMagusfile, line: line,
				problem: fmt.Sprintf("declares %s as a generated output, but magus never writes it", p),
				fix: "describe file will call it generated and the path guard will warn an author off it.\n" +
					"Narrow the skills_generate pattern so it misses the hand-authored skill."})
		}
	}
	return findings
}

// generateDriftThrowNamesTheEngineDriftCode: the root magusfile's whole-tree
// drift throw and the engine's declared-output path (types.ClassifyDrift) both
// diagnose "generated output drifted", and must name it with the same code so a
// reader does not learn two spellings of one condition. The throw deliberately
// does not CALL ClassifyDrift (whole-tree drift has no single target's inputs to
// classify against), so this pins only the code text.
func generateDriftThrowNamesTheEngineDriftCode(fsys fs.FS) []finding {
	src, bad := readFile(fsys, rootMagusfile)
	if bad != nil {
		return bad
	}
	i := strings.Index(src, "regeneration changed generated files")
	if i < 0 {
		return []finding{{path: rootMagusfile, problem: "the whole-tree drift throw moved or was reworded",
			fix: "Update this rule's anchor text to the throw's new wording."}}
	}
	// The code is a plain-text prefix on the throw, not a magus\raise call:
	// magus\raise refuses the MGS namespace as reserved for magus's own diagnostics.
	if strings.Contains(src[max(0, i-80):i], string(types.StaleGeneratedOutput)) {
		return nil
	}
	return []finding{{path: rootMagusfile, line: lineAt(src, i),
		problem: "the whole-tree drift throw does not carry " + string(types.StaleGeneratedOutput),
		fix:     "Prefix it with the code the engine's declared-output path emits via types.ClassifyDrift."}}
}
