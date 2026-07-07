package knowledge

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

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

	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		content := string(src)
		dID := docID(rel)
		s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: dID, Kind: types.KindDoc, Label: rel, Source: rel})

		if code, ok := diagnosticFromPath(rel); ok {
			s.Edges = append(s.Edges, extractedEdge(dID, diagnosticID(code), types.RelationDocuments, rel))
		}
		if name, ok := spellFromPath(rel); ok {
			s.Edges = append(s.Edges, extractedEdge(dID, spellID(name), types.RelationDocuments, rel))
		}
		if name, ok := moduleFromPath(rel); ok {
			s.Edges = append(s.Edges, extractedEdge(dID, moduleID(name), types.RelationDocuments, rel))
		}

		for _, code := range uniqSortedStrings(mgsRe.FindAllString(content, -1)) {
			s.Edges = append(s.Edges, inferredEdge(dID, diagnosticID(code), types.RelationDocuments, rel, 0.6))
		}
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
// under docs/, plus the top-level README.md and MAGUS.md, skipping ignore dirs.
func findDocFiles(root string) []string {
	var out []string
	for _, top := range []string{"README.md", "MAGUS.md"} {
		if _, err := os.Stat(filepath.Join(root, top)); err == nil {
			out = append(out, top)
		}
	}
	_ = filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
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
