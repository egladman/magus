package knowledge

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/docs"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
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
func assembleDocs(root string, spells types.SpellsOutput) Shard {
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

	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		content := string(src)
		dID := docID(rel)
		node := types.KnowledgeNode{ID: dID, Kind: types.KindDoc, Label: rel, Source: rel}

		// Surface the page's frontmatter title/tags onto the node so a query result
		// reads as the doc's human name, not just its path. Best-effort: a page with
		// no frontmatter (README.md, a stub) simply carries neither attr.
		if fm, ok := docs.ParseFrontmatter(content); ok {
			docAttrs := map[string]string{}
			if fm.Title != "" {
				docAttrs[AttrTitle] = fm.Title
			}
			if len(fm.Tags) > 0 {
				docAttrs[AttrTags] = strings.Join(fm.Tags, ",")
			}
			node.Attrs = nilIfEmpty(docAttrs)
		}

		if code, ok := diagnosticFromPath(rel); ok {
			s.Edges = append(s.Edges, extractedEdge(dID, diagnosticID(code), types.RelationDocuments, rel))
		}
		if name, ok := spellFromPath(rel); ok {
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
	}
	return s
}

// diagnosticFromPath returns the diagnostic code a docs/codes/**/MGSxxxx.md page
// documents.
func diagnosticFromPath(rel string) (string, bool) {
	if !strings.HasPrefix(rel, "docs/codes/") {
		return "", false
	}
	stem := strings.TrimSuffix(filepath.Base(rel), ".md")
	if mgsExactRe.MatchString(stem) {
		return stem, true
	}
	return "", false
}

// spellFromPath returns the spell a docs/spells/<name>.md page documents.
func spellFromPath(rel string) (string, bool) {
	return namedDocUnder(rel, "docs/spells")
}

// moduleFromPath returns the module a docs/buzz/modules/<name>.md page documents.
func moduleFromPath(rel string) (string, bool) {
	return namedDocUnder(rel, "docs/buzz/modules")
}

// namedDocUnder returns the <name> of a <dir>/<name>.md page, excluding index/
// README pages that name a section rather than an entity.
func namedDocUnder(rel, dir string) (string, bool) {
	if filepath.ToSlash(filepath.Dir(rel)) != dir || !strings.HasSuffix(rel, ".md") {
		return "", false
	}
	stem := strings.TrimSuffix(filepath.Base(rel), ".md")
	switch strings.ToLower(stem) {
	case "readme", "index":
		return "", false
	}
	return stem, true
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

// findDocFiles returns every markdown doc path (rel to root), sorted: everything
// under docs/, plus the top-level README.md, skipping ignore dirs.
//
// MAGUS.md is deliberately NOT ingested. It is a generated catalog (rendered by
// `magus describe graph -o markdown`) whose body carries live node/edge counts, and
// its doc node would emit body-derived edges (MGS codes, backticked spell names,
// markdown links). Ingesting it makes it both an input and an output: regenerating
// the counts changes the body, which changes the edge count, which changes the
// counts - no single-pass fixpoint. Everything in MAGUS.md is already a first-class
// node in the graph, so excluding it loses nothing.
func findDocFiles(root string) []string {
	var out []string
	for _, top := range []string{"README.md"} {
		if _, err := os.Stat(filepath.Join(root, top)); err == nil {
			out = append(out, top)
		}
	}
	_ = filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // WalkDir: skip unreadable entries, continue walking
		}
		if d.IsDir() {
			if project.IsIgnoreDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			if rel, err := filepath.Rel(root, path); err == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	slices.Sort(out)
	return out
}

// uniqSortedStrings returns the sorted unique values of xs.
func uniqSortedStrings(xs []string) []string {
	slices.Sort(xs)
	return slices.Compact(xs)
}
