package knowledge

import (
	"go/scanner"
	gotoken "go/token"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// linksShardName is the singleton shard holding the citation layer: every URL a code
// comment or a markdown page points at, and the source a generated page names in its
// frontmatter. One shard, because classifying a citation is the same question wherever
// it was written.
const linksShardName = "@links"

// A citation lands in exactly one of three classes:
//
//	docs     names a page this workspace holds  -> edge to that doc or docsection node
//	source   names a path this workspace holds  -> edge to that file or dir node
//	upstream everything else                    -> edge to a `link` node keyed by the URL
//
// The first two cross-link to a node that already exists instead of minting a URL node,
// so "what does this comment point at" and "what else points there" are one query rather
// than two, and a docs reorg moves the edge with the page. Only `upstream` mints a node,
// which is what keeps the link kind meaning "outside this workspace".
//
// Nothing here fetches. Classification reads the workspace and the URL's own text; a link
// node asserts that something here points there, never that anything is at the other end.

// maxURLLen caps one citation. A URL becomes a node ID and a label, so an unbounded token
// lifted out of prose would be an unbounded key. The longest real citation measured in
// this tree is 62 bytes.
const maxURLLen = 512

// urlRe finds an absolute http(s) URL. It only ever runs over text a LEXER already
// classified as a comment, never over a whole file, so a URL inside a string literal is
// structurally out of reach rather than filtered out after the fact. The stop set is the
// prose and markup punctuation that cannot appear unescaped in a URL a human typed into
// a sentence.
var urlRe = regexp.MustCompile("https?://[^\\s\"'`<>()\\[\\]{}\\\\]+")

// blobPathRe matches a forge's "view this file at this ref" URL path:
// /<owner>/<repo>/blob/<ref>/<path>, plus /tree/ and /raw/, and GitLab's /-/ infix. The
// captured tail is a candidate repository-relative path.
var blobPathRe = regexp.MustCompile(`^/[^/]+/[^/]+/(?:-/)?(?:blob|tree|raw)/[^/]+/(.+)$`)

// citation is a parsed, vetted URL. URL is the identity a link node is keyed by; the rest
// is kept apart from it because resolution reads those and identity does not.
type citation struct {
	URL      string
	Host     string
	Path     string // URL path, no leading slash
	Fragment string
}

// parseCitation vets one raw URL and returns its normalized form. Every rule below
// rejects something this workspace actually contains: comments here are full of
// URL-SHAPED prose written to show a format rather than to cite a page.
func parseCitation(raw string) (citation, bool) {
	// Trailing sentence punctuation belongs to the prose, not the URL.
	raw = strings.TrimRight(raw, ".,;:!?")
	if raw == "" || len(raw) > maxURLLen {
		return citation{}, false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return citation{}, false
	}
	// A closed scheme set. A comment can carry file:, data:, javascript: or mailto:; only
	// the two web schemes name a document a reader can open, and a closed set is what
	// keeps an executable payload out of a node ID.
	if u.Scheme != "http" && u.Scheme != "https" {
		return citation{}, false
	}
	// Userinfo is credential-shaped and is never part of a document's identity. Measured:
	// console/src/lib/daemon.ts carries http://127.0.0.1:7391@evil.com in a comment as the
	// spoofing case its origin check rejects. Indexing that as an upstream doc would be
	// exactly backwards.
	if u.User != nil {
		return citation{}, false
	}
	host := strings.ToLower(u.Hostname())
	if !citableHost(host) {
		return citation{}, false
	}
	frag := u.Fragment
	// A fragment is a position INSIDE a document, not a second document, so it is dropped
	// from a link node's identity. It is carried out separately because for this
	// workspace's OWN pages an anchor does name a distinct node (a docsection).
	u.Fragment, u.RawFragment = "", ""
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	if u.Path == "" {
		u.Path = "/"
	}
	return citation{URL: u.String(), Host: host, Path: strings.TrimPrefix(u.Path, "/"), Fragment: frag}, true
}

// citableHost reports whether a host names a real, durable document. Measured in this
// tree's comments: http://<host, https://s3.<region, https://.../magus/logs/,
// https://host/magus/, https://endpoint/bucket/key, http://127.0.0.1:PORT/path,
// https://github.example.com/api/v3. Each is a template, not a citation, and a link node
// minted from one is a permanent phantom nothing can ever retire, so the shape has to
// earn its place before anything is built on it.
func citableHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	// An IP literal is an address, not a citation, and every one written here is a
	// loopback example.
	if net.ParseIP(host) != nil {
		return false
	}
	labels := strings.Split(host, ".")
	// A single label is a placeholder ("host", "endpoint") or a name only one machine
	// resolves; either way it cites nothing a second reader can open.
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || strings.HasPrefix(l, "-") || strings.HasSuffix(l, "-") {
			return false
		}
		for _, r := range l {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 || strings.Trim(tld, "abcdefghijklmnopqrstuvwxyz") != "" {
		return false
	}
	// RFC 2606 and RFC 6761 set these aside for documentation and local use, which is
	// precisely what a placeholder in a comment is. The "example" label is checked
	// anywhere in the name because that is how a placeholder is spelled in practice.
	switch tld {
	case "localhost", "local", "test", "invalid", "example", "internal":
		return false
	}
	return !slices.Contains(labels, "example")
}

// linkID keys a link node by its normalized URL, so two comments citing the same page
// reach the same node whatever they wrote around it.
func linkID(u string) string { return types.KindLink + ":" + u }

// docIndex resolves a URL back onto the workspace's own prose. It is built from the doc
// shard's FINISHED nodes, so a page is citable exactly when the graph holds it, and a
// page the doc walk skipped can never be cross-linked to by accident.
type docIndex struct {
	byURL    map[string][]string // clean-URL suffix -> doc rel paths
	sections map[string]bool     // "<rel>#<anchor>"
	docs     map[string]bool     // doc rel paths, for resolving a source path that IS a page
}

func newDocIndex(nodes []types.KnowledgeNode) docIndex {
	idx := docIndex{byURL: map[string][]string{}, sections: map[string]bool{}, docs: map[string]bool{}}
	for _, n := range nodes {
		switch n.Kind {
		case types.KindDoc:
			idx.docs[n.Source] = true
			for _, key := range cleanURLKeys(n.Source) {
				if !slices.Contains(idx.byURL[key], n.Source) {
					idx.byURL[key] = append(idx.byURL[key], n.Source)
				}
			}
		case types.KindDocSection:
			idx.sections[n.Source] = true
		}
	}
	return idx
}

// cleanURLKeys returns the path suffixes a rendered page could be served under. Nothing in
// the graph's inputs declares where the site is deployed OR which directory holds the docs
// tree, so both ends are unknown and resolution matches SUFFIX against suffix: the URL
// drops its site base path, the page drops its docs/ prefix, and what is left has to be the
// same. That survives a move of the site and a reorg of the tree, which a hardcoded base
// URL would not.
//
// Two segments minimum on both sides, so a one-word tail cannot collide its way into an edge.
func cleanURLKeys(rel string) []string {
	stem := strings.TrimSuffix(rel, ".md")
	forms := []string{stem}
	// An index/README page is served as its directory, so it answers to both spellings.
	if base := strings.ToLower(path.Base(stem)); base == "index" || base == "readme" {
		if d := path.Dir(stem); d != "." {
			forms = append(forms, d)
		}
	}
	out := make([]string, 0, len(forms))
	for _, f := range forms {
		out = append(out, pathSuffixes(f)...)
	}
	return out
}

// pathSuffixes returns every trailing run of at least two segments, longest first.
func pathSuffixes(p string) []string {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	var out []string
	for i := 0; i+2 <= len(segs); i++ {
		out = append(out, strings.Join(segs[i:], "/"))
	}
	return out
}

// resolveDocsURL resolves a citation to this workspace's own doc or docsection node, taking
// the LONGEST URL-path suffix that names exactly one page.
//
// An AMBIGUOUS suffix resolves to nothing, and the walk stops there rather than falling
// back to a shorter one, which could only be vaguer. Measured: a bare "console/" matches
// both console/README.md and docs/reference/console.md, and a coincidence must not become
// an edge that reads as verified.
func (idx docIndex) resolveDocsURL(c citation) (string, bool) {
	for _, key := range pathSuffixes(c.Path) {
		rels := idx.byURL[key]
		if len(rels) > 1 {
			return "", false
		}
		if len(rels) == 0 {
			continue
		}
		rel := rels[0]
		if c.Fragment != "" && idx.sections[rel+"#"+c.Fragment] {
			return docSectionID(rel, c.Fragment), true
		}
		return docID(rel), true
	}
	return "", false
}

// docCitations is what the docs pass saw and does not itself resolve: the absolute URLs a
// page links to, and the source its frontmatter names. Both are citations out of prose,
// so both resolve here rather than in two places with two answers.
type docCitations struct {
	URLs    []docURLCite
	Sources []docSourceCite
}

type docURLCite struct {
	Doc string // citing page, workspace-relative
	URL string // the markdown link target, as written
}

type docSourceCite struct {
	Doc  string
	Spec string // one generated_from entry, as written
}

// sourceCite is one citation found in a source file's comments.
type sourceCite struct {
	Line int
	Cite citation
}

// assembleLinks builds the citation shard: comment URLs from Go and Buzz sources,
// absolute markdown links from every doc page, and the source each generated page
// declares in its frontmatter.
//
// A citation from code is attributed to the FILE that holds it, never to the symbol whose
// doc comment it sits in. Symbol ranges come from a SCIP index, which is cache state under
// the gitignored cache dir and rides a lazily loaded @symbols shard; keying this shard on
// it would make the committed graph differ between a checkout that had run the scip op and
// one that had not, which is the exact drift @dirs and @io were repaired to avoid.
//
// The shard mints its own file and dir nodes for both ends of a citation, the way a symbol
// shard does, so an edge and its endpoints appear together or not at all.
func assembleLinks(root string, projects []types.TargetGraphProject, idx docIndex, cites docCitations) Shard {
	s := Shard{Name: linksShardName}
	seen := map[string]bool{}
	// noteFile mints a path-bearing node and its containment chain once per path. Only the
	// two languages this pass lexes get a language attr; guessing one from an extension
	// would invent vocabulary the SCIP and buzz file nodes never use.
	noteFile := func(rel string) string {
		id, kind := fileID(rel), types.KindFile
		var attrs map[string]string
		switch path.Ext(rel) {
		case ".go":
			attrs = map[string]string{attrLanguage: "go"}
		case ".buzz":
			attrs = map[string]string{attrLanguage: "buzz"}
		}
		if isDir(root, rel) {
			id, kind, attrs = dirID(rel), types.KindDir, nil
		}
		if seen[id] {
			return id
		}
		seen[id] = true
		s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: id, Kind: kind, Label: rel, Source: rel, Attrs: attrs})
		if owner, ok := owningProjectPath(rel, projects); ok {
			dn, de := containsChain(owner, rel, id)
			s.Nodes = append(s.Nodes, dn...)
			s.Edges = append(s.Edges, de...)
		}
		return id
	}
	noteLink := func(c citation) string {
		id := linkID(c.URL)
		if !seen[id] {
			seen[id] = true
			// The label keeps the scheme: the routing index prints anchors in backticks,
			// and the docs conventions read a schemeless `host/path` there as a repo path
			// that does not exist.
			s.Nodes = append(s.Nodes, types.KnowledgeNode{
				ID: id, Kind: types.KindLink, Label: c.URL, Source: c.URL,
				Attrs: map[string]string{attrHost: c.Host},
			})
		}
		return id
	}
	// resolveTarget maps one citation to the node it cites, minting whatever that needs.
	// A path that is both a markdown page and a file on disk always resolves to its DOC
	// node: the graph models a page as prose, and the file node behind it would be the
	// same page with nothing said about it.
	// Comment citations are read up front, so every path a citation could name is known
	// before any is resolved: the VCS is asked ONCE which of them it ignores. A path on
	// disk the VCS ignores is a build product, not a source the workspace holds, and
	// without this the built ./magus binary at the root turned the docs site's own URL
	// into a file node here and a link node in CI, and the committed graph drifted
	// between the two.
	type sourceCites struct {
		rel   string
		found []sourceCite
	}
	var comments []sourceCites
	for _, rel := range findCommentSources(root) {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		if found := commentCitations(rel, src); len(found) > 0 {
			comments = append(comments, sourceCites{rel, found})
		}
	}
	var candidates []string
	for _, sc := range comments {
		for _, cite := range sc.found {
			if rel, ok := workspaceSourcePath(root, cite.Cite.Path); ok {
				candidates = append(candidates, rel)
			}
		}
	}
	for _, dc := range cites.URLs {
		if c, ok := parseCitation(dc.URL); ok {
			if rel, ok := workspaceSourcePath(root, c.Path); ok {
				candidates = append(candidates, rel)
			}
		}
	}
	slices.Sort(candidates)
	held := map[string]bool{}
	for _, rel := range dropVCSIgnored(root, slices.Compact(candidates)) {
		held[rel] = true
	}

	resolveTarget := func(c citation) (id, class string) {
		if target, ok := idx.resolveDocsURL(c); ok {
			return target, "docs"
		}
		if rel, ok := workspaceSourcePath(root, c.Path); ok && held[rel] {
			if idx.docs[rel] {
				return docID(rel), "source"
			}
			return noteFile(rel), "source"
		}
		return noteLink(c), "upstream"
	}

	for _, sc := range comments {
		rel, found := sc.rel, sc.found
		fID := noteFile(rel)
		for _, sc := range found {
			target, _ := resolveTarget(sc.Cite)
			if target == fID {
				continue // a file citing its own path says nothing
			}
			s.Edges = append(s.Edges, extractedEdge(fID, target, types.RelationReferences, rel+":"+strconv.Itoa(sc.Line)))
		}
	}

	for _, dc := range cites.URLs {
		c, ok := parseCitation(dc.URL)
		if !ok {
			continue
		}
		target, class := resolveTarget(c)
		if target == docID(dc.Doc) {
			continue
		}
		// A page that cites a source path is describing it; a page that cites another page
		// or an upstream vendor is only pointing at it. The relation follows that
		// difference rather than flattening both into one predicate.
		rel := types.RelationReferences
		if class == "source" {
			rel = types.RelationDocuments
		}
		s.Edges = append(s.Edges, extractedEdge(docID(dc.Doc), target, rel, dc.Doc))
	}

	for _, sc := range cites.Sources {
		rel, ok := generatedFromPath(root, sc.Spec)
		if !ok || rel == sc.Doc {
			continue
		}
		target := docID(rel)
		if !idx.docs[rel] {
			target = noteFile(rel)
		}
		s.Edges = append(s.Edges, extractedEdge(docID(sc.Doc), target, types.RelationDocuments, sc.Doc+" generated_from"))
	}
	return s
}

// generatedFromPath resolves one `generated_from` entry to the workspace path it names.
//
// Two of the three shapes resolve to nothing on purpose. A value ending in "/" is an
// in-site section path, which names no file at all (the renderer already tells the two
// apart by that slash). A GLOB names a SET whose membership changes with the tree, so
// expanding it would attach one page to hundreds of files and drown the attribution the
// field exists to make; only an exact path is a claim about one source.
func generatedFromPath(root, spec string) (string, bool) {
	if spec == "" || strings.HasSuffix(spec, "/") || strings.ContainsAny(spec, "*?[") {
		return "", false
	}
	return workspaceSourcePath(root, spec)
}

// workspaceSourcePath maps a citation's path onto a path THIS workspace holds, returning
// it workspace-relative. A forge URL is unwrapped to its repository-relative tail first.
//
// The match is on the PATH, never on the host or the owner/repo in the URL. A fork, a
// mirror, or a move between forges changes those, while the claim being checked is only
// ever "the workspace holds this file". A URL naming some other repository's file simply
// does not exist here and falls through to an upstream link, which is the right answer
// for it; the residual false positive needs the identical path to exist in both trees,
// and costs a link classified as internal rather than any action taken on it.
func workspaceSourcePath(root, p string) (string, bool) {
	p = strings.SplitN(p, "?", 2)[0]
	if m := blobPathRe.FindStringSubmatch("/" + strings.TrimPrefix(p, "/")); m != nil {
		p = m[1]
	}
	p = strings.Trim(p, "/")
	if p == "" || !workspaceContainsPath(p) {
		return "", false
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
		return "", false
	}
	return p, true
}

func isDir(root, rel string) bool {
	fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && fi.IsDir()
}

// commentCitations returns the vetted URLs in one source file's comments, by language.
func commentCitations(rel string, src []byte) []sourceCite {
	switch path.Ext(rel) {
	case ".go":
		return goCommentCitations(rel, src)
	case ".buzz":
		return buzzCommentCitations(string(src))
	}
	return nil
}

// goCommentCitations runs the Go SCANNER rather than a line regex: the scanner is what
// knows a "//" inside a string or a raw literal is not a comment, so this pass never sees
// text the language does not call one. A file that fails to lex yields nothing, the same
// posture assembleBuzz takes on a parse error.
func goCommentCitations(rel string, src []byte) []sourceCite {
	fset := gotoken.NewFileSet()
	f := fset.AddFile(rel, fset.Base(), len(src))
	var sc scanner.Scanner
	sc.Init(f, src, nil, scanner.ScanComments)
	var out []sourceCite
	for {
		pos, tok, lit := sc.Scan()
		if tok == gotoken.EOF {
			break
		}
		if tok != gotoken.COMMENT {
			continue
		}
		line := fset.Position(pos).Line
		for _, raw := range urlRe.FindAllString(lit, -1) {
			if c, ok := parseCitation(raw); ok {
				out = append(out, sourceCite{Line: line, Cite: c})
			}
		}
	}
	return out
}

// buzzCommentCitations reuses the Buzz lexer the same way appendRationale does: a comment
// block reaches the next token's Doc already "//"-stripped, so a URL in a string literal
// is never reachable.
//
// The lexer attaches a block only to the token on the line DIRECTLY below it, so a comment
// followed by a blank line, or trailing the last token, carries no Doc and is missed. That
// is the same limit rationale extraction has always lived with, and the trade is
// deliberate: under-reading a comment costs a citation, while re-scanning raw lines to
// catch it would start reading string literals as prose.
func buzzCommentCitations(content string) []sourceCite {
	toks, err := token.Tokenize(content)
	if err != nil {
		return nil
	}
	var out []sourceCite
	for _, tk := range toks {
		if tk.Doc == "" {
			continue
		}
		for _, raw := range urlRe.FindAllString(tk.Doc, -1) {
			if c, ok := parseCitation(raw); ok {
				out = append(out, sourceCite{Line: tk.Line, Cite: c})
			}
		}
	}
	return out
}

// findCommentSources returns every workspace source path whose comments this pass can
// read, sorted, under findBuzzFiles's ignore rules.
//
// Go test files are excluded: a URL in a test cites the fixture's provenance, not the
// shipped code's, and the walk is the cost this pass adds to every build. TypeScript is
// excluded for a different reason. There is no TS lexer in this module, and a hand-rolled
// one has to get template literals, regex literals and division apart to tell a comment
// from a string; measured, console/src holds two comment URLs and both are loopback
// placeholders this parser rejects anyway, so the unsafe parse would buy nothing.
func findCommentSources(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // WalkDir: skip unreadable entries, continue walking
		}
		if d.IsDir() {
			if p != root && (project.IsIgnoreDir(d.Name()) || d.Name() == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".buzz") && (!strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go")) {
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
