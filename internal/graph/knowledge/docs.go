package knowledge

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/docs"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// DocsShardName is the singleton shard holding markdown doc nodes and the edges
// that tie them to what they document.
const DocsShardName = "@docs"

var (
	// mgsRe finds MGS#### diagnostic-code mentions in doc bodies.
	mgsRe = regexp.MustCompile(`\bMGS\d{4}\b`)
	// mgsExactRe matches a filename stem that is exactly a diagnostic code.
	mgsExactRe = regexp.MustCompile(`^MGS\d{4}$`)
	// mdLinkRe finds markdown inline links: the captured group is the target.
	mdLinkRe = regexp.MustCompile(`\]\(([^)]+)\)`)
)

// assembleDocs scans the workspace's markdown docs and links each to what it
// documents. Path-convention edges (docs/codes/**/MGSxxxx.md, docs/spells/<name>.md,
// docs/buzz/modules/<name>.md) are EXTRACTED; in-body mentions (MGS#### codes,
// backtick-wrapped spell names) are INFERRED; markdown links to other scanned docs
// are references. Extracted edges win over inferred on dedup, so a code page's own
// path edge is not weakened by the same code appearing in its body.
func assembleDocs(root string, spells types.SpellsOutput, projects []types.TargetGraphProject) Shard {
	s := Shard{Name: DocsShardName}
	files := findDocFiles(root)
	scanned := make(map[string]bool, len(files))
	for _, f := range files {
		scanned[f] = true
	}
	spellNames := make([]string, 0, len(spells.Spells))
	for _, sp := range spells.Spells {
		spellNames = append(spellNames, sp.Name)
	}
	slices.Sort(spellNames)

	knownCode := make(map[string]bool)
	for _, c := range types.AllDiagnosticCodes() {
		knownCode[string(c)] = true
	}

	spellSet := make(map[string]bool, len(spellNames))
	for _, n := range spellNames {
		spellSet[n] = true
	}

	// "magus <sub>" -> the manpage doc documenting it; a doc mentioning `magus run` then
	// references that manpage (the command's doc IS its node). Sorted keys -> deterministic.
	manCmds, manDoc := manpageCommands(files)

	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		content := string(src)
		dID := docID(rel)
		node := types.KnowledgeNode{ID: dID, Kind: types.KindDoc, Label: rel, Source: rel}

		// Every doc carries a role - what the markdown IS (readme/agent/changelog/...),
		// from a universal filename convention - plus its frontmatter title/tags where
		// present, so a query result reads as the doc's human name and an agent can ask
		// `kind:doc role:agent` in any repo. A page with no frontmatter (a README, a stub)
		// simply carries no title/tags.
		docAttrs := map[string]string{AttrRole: roleFromRel(rel)}
		if sec := sectionFromRel(rel); sec != "" {
			docAttrs[AttrSection] = sec
		}
		if fm, ok := docs.ParseFrontmatter(content); ok {
			if fm.Title != "" {
				docAttrs[AttrTitle] = fm.Title
			}
			if len(fm.Tags) > 0 {
				docAttrs[AttrTags] = strings.Join(fm.Tags, ",")
			}
		}
		node.Attrs = docAttrs

		// Attach the doc to the project whose directory holds it - structural containment,
		// exactly as a source file attaches (project -> contains -> file). This is the
		// contextual link: from a project you reach its README and design notes, with the
		// role attr telling you which is which. It never claims the doc "documents" the
		// project (a spell page documents the spell, not the root project it sits under).
		if owner, ok := owningProjectPath(rel, projects); ok {
			dn, de := containsChain(owner, rel, dID)
			s.Nodes = append(s.Nodes, dn...)
			s.Edges = append(s.Edges, de...)
		}

		if code, ok := diagnosticFromPath(rel); ok && knownCode[code] {
			s.Edges = append(s.Edges, extractedEdge(dID, diagnosticID(code), types.RelationDocuments, rel))
		}
		if name, ok := spellFromPath(rel, spellSet); ok {
			s.Edges = append(s.Edges, extractedEdge(dID, spellID(name), types.RelationDocuments, rel))
		}
		if name, ok := moduleFromPath(rel); ok {
			s.Edges = append(s.Edges, extractedEdge(dID, moduleID(name), types.RelationDocuments, rel))
		}

		// A body mention of an MGS#### code links to its diagnostic node only when
		// the code is registered. A mention of an unregistered code (typo, removed,
		// never defined) has no node to link to, so record it on the doc as MGS7002
		// instead of emitting a dangling edge to a phantom diagnostic.
		var unknownCodes []string
		for _, code := range uniqSortedStrings(mgsRe.FindAllString(content, -1)) {
			if knownCode[code] {
				s.Edges = append(s.Edges, inferredEdge(dID, diagnosticID(code), types.RelationDocuments, rel, 0.6))
			} else {
				unknownCodes = append(unknownCodes, code)
			}
		}
		if len(unknownCodes) > 0 {
			if node.Attrs == nil {
				node.Attrs = map[string]string{}
			}
			node.Attrs[AttrDiagnostic] = string(types.DanglingDocReference)
			node.Attrs["unknown_codes"] = strings.Join(unknownCodes, ",")
		}
		s.Nodes = append(s.Nodes, node)

		for _, name := range spellNames {
			if strings.Contains(content, "`"+name+"`") {
				s.Edges = append(s.Edges, inferredEdge(dID, spellID(name), types.RelationDocuments, rel, 0.5))
			}
		}
		for _, m := range mdLinkRe.FindAllStringSubmatch(content, -1) {
			if target, ok := resolveDocLink(rel, m[1], scanned); ok {
				s.Edges = append(s.Edges, extractedEdge(dID, docID(target), types.RelationReferences, rel))
			}
		}

		// A body mention of a `magus <sub>` command references its manpage doc - the
		// doc<->command interconnection. Skip the manpage's self-reference.
		for _, cmd := range manCmds {
			docPath := manDoc[cmd]
			if docPath == rel {
				continue
			}
			if strings.Contains(content, "`"+cmd+"`") {
				s.Edges = append(s.Edges, inferredEdge(dID, docID(docPath), types.RelationReferences, rel, 0.6))
			}
		}
	}
	return s
}

// manpageCommands indexes the manpage docs by the command they document: "magus <sub>" ->
// the <...>/manpage/magus-<sub>.md rel path, with sorted command keys for deterministic
// iteration. The manpage doc serves as the command's node for cross-referencing, so no
// separate command node kind is needed to interconnect docs with commands.
func manpageCommands(files []string) ([]string, map[string]string) {
	doc := map[string]string{}
	for _, f := range files {
		base := filepath.Base(f)
		if !hasPathSegment(f, "manpage") || !strings.HasPrefix(base, "magus-") || !strings.HasSuffix(base, ".md") {
			continue
		}
		sub := strings.TrimSuffix(strings.TrimPrefix(base, "magus-"), ".md")
		if sub == "" {
			continue
		}
		doc["magus "+sub] = f
	}
	cmds := make([]string, 0, len(doc))
	for c := range doc {
		cmds = append(cmds, c)
	}
	slices.Sort(cmds)
	return cmds, doc
}

// diagnosticFromPath returns the diagnostic code a page named MGSxxxx.md documents. The
// match is on the FILENAME, directory-agnostic: a code page documents its code wherever the
// docs tree puts it, so a docs reorg cannot silently sever the edge. The call site gates on
// the code being registered, so a stray MGSxxxx.md for an unknown code links nothing.
func diagnosticFromPath(rel string) (string, bool) {
	stem := strings.TrimSuffix(filepath.Base(rel), ".md")
	if mgsExactRe.MatchString(stem) {
		return stem, true
	}
	return "", false
}

// spellFromPath returns the spell a <...>/spells/<name>.md page documents, anchored on a
// "spells" path SEGMENT (not a fixed prefix) so it survives a docs reorg, and validated
// against the known spell set so a non-spell page under a spells dir links nothing.
func spellFromPath(rel string, known map[string]bool) (string, bool) {
	if !hasPathSegment(rel, "spells") {
		return "", false
	}
	if stem, ok := entityStem(rel); ok && known[stem] {
		return stem, true
	}
	return "", false
}

// moduleFromPath returns the module a <...>/buzz/<name>.md page documents, anchored on a
// "buzz" path segment so it survives a reorg (docs/buzz/modules -> docs/reference/buzz).
func moduleFromPath(rel string) (string, bool) {
	if !hasPathSegment(rel, "buzz") {
		return "", false
	}
	return entityStem(rel)
}

// entityStem returns a page's basename stem, rejecting section landing pages (index/README)
// that name a section rather than an entity.
func entityStem(rel string) (string, bool) {
	if !strings.HasSuffix(rel, ".md") {
		return "", false
	}
	stem := strings.TrimSuffix(filepath.Base(rel), ".md")
	switch strings.ToLower(stem) {
	case "readme", "index":
		return "", false
	}
	return stem, true
}

// hasPathSegment reports whether rel contains seg as a full slash-delimited path segment,
// so a match anchors on a meaningful directory ("spells", "buzz") without hardcoding the
// full prefix that a docs reorg would break.
func hasPathSegment(rel, seg string) bool {
	for _, p := range strings.Split(filepath.ToSlash(rel), "/") {
		if p == seg {
			return true
		}
	}
	return false
}

// resolveDocLink resolves a markdown link target (relative to the linking doc) to
// a scanned doc's rel path, dropping anchors, external URLs, and non-doc targets.
func resolveDocLink(fromRel, link string, scanned map[string]bool) (string, bool) {
	if link == "" || strings.HasPrefix(link, "#") || strings.Contains(link, "://") {
		return "", false
	}
	if i := strings.IndexByte(link, '#'); i >= 0 {
		link = link[:i]
	}
	if !strings.HasSuffix(link, ".md") {
		return "", false
	}
	target := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(fromRel), link)))
	if scanned[target] {
		return target, true
	}
	return "", false
}

// findDocFiles returns every authored markdown path (rel to root), sorted, by walking
// the whole workspace. Skipped: the dot-dirs and build/dependency dirs project.IsIgnoreDir
// already excludes (.git, .claude, node_modules, gen, vendor, target), plus dist/ (a build
// output), plus MAGUS.md at any level. Generated markdown is NOT skipped here - a generated
// page is ingested and self-labeled by its producing target's `produces` edge (see
// assembleIO); only true build-output dirs and the fixpoint file below are dropped.
//
// MAGUS.md is the one exclusion by name. It is a generated catalog (rendered by
// `magus describe graph -o markdown`) whose body carries live node/edge counts, so its
// doc node would emit body-derived edges (MGS codes, backticked spell names, markdown
// links). Ingesting it makes it both an input and an output: regenerating the counts
// changes the body, which changes the edge count, which changes the counts - no
// single-pass fixpoint. Everything in MAGUS.md is already a first-class node, so
// excluding it loses nothing.
func findDocFiles(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // WalkDir: skip unreadable entries, continue walking
		}
		if d.IsDir() {
			// Never skip the walk root itself: the workspace we are indexing is often a
			// secondary checkout (a git worktree, hg share, or jj workspace), and
			// skipDocWalkDir's secondary-checkout guard would otherwise skip everything.
			// The guard applies only to checkouts found BELOW the root.
			if p != root && skipDocWalkDir(p, d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".md") || filepath.Base(p) == "MAGUS.md" {
			return nil
		}
		if rel, err := filepath.Rel(root, p); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	slices.Sort(out)
	return out
}

// skipDocWalkDir reports whether the doc walk should not descend into dir. Unlike
// project.IsIgnoreDir (which skips ALL dot-dirs), the doc walk DOES descend into
// meaningful hidden dirs - .claude/skills holds SKILL.md agent files, .github holds
// templates - and skips only genuine noise: VCS internals, the magus cache, build and
// dependency trees, and any secondary checkout of the same repo (a git worktree, hg
// share, or jj workspace) whose files would otherwise be indexed twice.
func skipDocWalkDir(path, name string) bool {
	switch name {
	case ".git", ".magus", "node_modules", "vendor", "gen", "target", "dist":
		return true
	}
	return vcs.IsSecondaryCheckout(path)
}

// roleFromRel classifies a markdown file by what it IS, from cross-ecosystem filename
// conventions - never magus-specific names, so the same rule is meaningful in any repo.
// Anything without a recognized convention is a plain "doc".
func roleFromRel(rel string) string {
	stem := strings.ToLower(strings.TrimSuffix(filepath.Base(rel), ".md"))
	switch stem {
	case "readme":
		return "readme"
	case "agents", "claude":
		return "agent"
	case "skill":
		return "skill"
	case "changelog":
		return "changelog"
	case "contributing":
		return "contributing"
	case "license", "licence":
		return "license"
	default:
		return "doc"
	}
}

// sectionFromRel returns a doc's top-level section under a docs/ tree (docs/guides/mcp.md
// -> "guides"), so a page is queryable by where it lives without hand-tagging. Empty for
// docs outside docs/ and for top-level docs (docs/glossary.md) that name no section.
func sectionFromRel(rel string) string {
	const prefix = "docs/"
	if !strings.HasPrefix(rel, prefix) {
		return ""
	}
	rest := rel[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return ""
}

// uniqSortedStrings returns the sorted unique values of xs.
func uniqSortedStrings(xs []string) []string {
	slices.Sort(xs)
	return slices.Compact(xs)
}
