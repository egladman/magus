package knowledge

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/egladman/magus/internal/docs"
	"github.com/egladman/magus/types"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// markersShardName is the singleton shard holding every `magus:<family>` declaration
// in the workspace. It reads files only, never the symbol index, so it is complete in a
// checkout that has never built one.
const markersShardName = "@markers"

// attrMarkerUnresolved says why a marker has no references edge: a figure no page
// embeds, a destination that is not a directory. The marker node stays, so the
// declaration is still findable and the reason travels with it.
const attrMarkerUnresolved = "unresolved"

// maxMarkerFileBytes skips a file too large to be hand-written source.
const maxMarkerFileBytes = 1 << 20

// markerTokenRe matches the token itself, anchored where the line scan found "magus:".
// A family is one lowercase word, so `magus:theme-set` and `magus:notice:<msg>` are not
// marker-shaped and stay prose.
var markerTokenRe = regexp.MustCompile(`^magus:([a-z]+)(?::(begin|end))?(?:\s|$)`)

// markerPairRe is one stamp pair: `k=v` or the `k: v` spelling the AGENTS.md stamp uses.
var markerPairRe = regexp.MustCompile(`^([a-z][a-z0-9_.-]*)\s*(?:=|:\s)\s*(\S.*)$`)

// figureEmbedRe is a docs page placing a figure, alone on its line.
var figureEmbedRe = regexp.MustCompile(`^<!--diagram:([^\s>]+)-->$`)

// markerReservedAttrs are the keys the scanner writes itself; a stamp pair never
// overwrites one.
var markerReservedAttrs = []string{
	types.AttrMarkerFamily, types.AttrMarkerVerb, types.AttrMarkerArgs,
	types.AttrLine, types.AttrEndLine, attrMarkerUnresolved,
}

// declaredEdge is an edge a person asserted in a marker (confidence declared, score 1.0).
func declaredEdge(source, target string, relation types.RelationID, provenance string) types.KnowledgeEdge {
	return types.KnowledgeEdge{
		Source: source, Target: target, Relation: relation,
		Confidence: types.ConfidenceDeclared, Score: 1.0, Provenance: provenance,
	}
}

// markerHit is one token as found on its line, before begin/end folding.
type markerHit struct {
	family string
	verb   string // "", "begin" or "end"
	args   string
	line   int
}

// scanMarkers returns the marker tokens in one file. A token counts only when nothing
// but punctuation and space precedes it on the line: that prefix is the host language's
// comment opener, whatever the language. Prose that mentions a marker mid-sentence, or
// a string literal holding one, has a letter or a quote before the token.
//
// Markdown fenced code blocks are skipped, since a fence quotes an example.
func scanMarkers(rel string, src []byte) []markerHit {
	markdown := strings.HasSuffix(rel, ".md")
	var fence string
	var out []markerHit
	for i, raw := range bytes.Split(src, []byte("\n")) {
		line := strings.TrimRight(string(raw), "\r")
		if markdown {
			if f := fenceOpener(line); f != "" {
				switch {
				case fence == "":
					fence = f
				case strings.HasPrefix(f, fence):
					fence = ""
				}
				continue
			}
			if fence != "" {
				continue
			}
		}
		at := strings.Index(line, "magus:")
		if at < 0 || !commentOpenerOnly(line[:at]) {
			continue
		}
		m := markerTokenRe.FindStringSubmatchIndex(line[at:])
		if m == nil {
			continue
		}
		verb := ""
		if m[4] >= 0 {
			verb = line[at+m[4] : at+m[5]]
		}
		out = append(out, markerHit{
			family: line[at+m[2] : at+m[3]],
			verb:   verb,
			args:   markerArgs(line[at+m[1]:]),
			line:   i + 1,
		})
	}
	return out
}

// fenceOpener returns the fence run a markdown line opens or closes with, or "".
func fenceOpener(line string) string {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 {
		return ""
	}
	for _, c := range []string{"`", "~"} {
		n := len(t) - len(strings.TrimLeft(t, c))
		if n >= 3 {
			return strings.Repeat(c, n)
		}
	}
	return ""
}

func commentOpenerOnly(prefix string) bool {
	for _, r := range prefix {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '"' || r == '\'' || r == '`' {
			return false
		}
	}
	return true
}

// markerArgs is the text after the token with a trailing comment closer removed.
func markerArgs(rest string) string {
	rest = strings.TrimSpace(rest)
	for _, closer := range []string{"-->", "*/"} {
		rest = strings.TrimSpace(strings.TrimSuffix(rest, closer))
	}
	return rest
}

// parseMarkerArgs splits a marker's argument text into positional words and stamp
// pairs. Segments are `;`-separated; a whole segment may be one pair, and the first
// segment's words are positional unless a word is itself `k=v`.
func parseMarkerArgs(args string) ([]string, map[string]string) {
	var positional []string
	stamps := map[string]string{}
	for i, seg := range strings.Split(args, ";") {
		seg = strings.TrimSpace(seg)
		if m := markerPairRe.FindStringSubmatch(seg); m != nil {
			stamps[m[1]] = strings.TrimSpace(m[2])
			continue
		}
		if i > 0 {
			continue
		}
		for _, w := range strings.Fields(seg) {
			if k, v, ok := strings.Cut(w, "="); ok && v != "" && markerPairRe.MatchString(w) {
				stamps[k] = v
				continue
			}
			positional = append(positional, w)
		}
	}
	for _, k := range markerReservedAttrs {
		delete(stamps, k)
	}
	return positional, stamps
}

// foldedMarker is one marker node's worth of facts: a point, or a begin/end pair.
type foldedMarker struct {
	markerHit
	endLine    int
	unresolved string
}

// foldMarkers pairs each `:end` with the latest open `:begin` of its family. The begin
// line keys the node and carries the args; an unpaired half keeps its node and says so.
func foldMarkers(hits []markerHit) []foldedMarker {
	var out []foldedMarker
	open := map[string][]int{} // family -> indexes into out of unclosed begins
	for _, h := range hits {
		switch h.verb {
		case "begin":
			open[h.family] = append(open[h.family], len(out))
			out = append(out, foldedMarker{markerHit: h})
		case "end":
			stack := open[h.family]
			if len(stack) == 0 {
				out = append(out, foldedMarker{markerHit: h, unresolved: "magus:" + h.family + ":end has no :begin above it"})
				continue
			}
			out[stack[len(stack)-1]].endLine = h.line
			open[h.family] = stack[:len(stack)-1]
		default:
			out = append(out, foldedMarker{markerHit: h})
		}
	}
	for family, stack := range open {
		for _, i := range stack {
			out[i].unresolved = "magus:" + family + ":begin has no :end below it"
		}
	}
	return out
}

// unknownMarker is one token whose family is not a MarkerFamily.
type unknownMarker struct {
	rel    string
	line   int
	family string
}

func unknownMarkerFamilyError(unknown []unknownMarker) error {
	locs := make([]string, 0, len(unknown))
	for _, u := range unknown {
		locs = append(locs, fmt.Sprintf("%s:%d: magus:%s", u.rel, u.line, u.family))
	}
	return types.DiagnosticErrorf(types.UnknownMarkerFamily,
		"unknown marker family: %s; the families are %s",
		strings.Join(locs, ", "), strings.Join(types.MarkerFamily("").Values(), ", "))
}

// assembleMarkers builds the marker shard from every source and docs page in the
// workspace. docPages are the doc shard's page paths, which reach dot-directories the
// source walk skips. An unknown family sets Err and nothing else, because a declaration
// nothing honors must stop the build rather than vanish from it.
func assembleMarkers(w *TreeWalk, projects []types.TargetGraphProject, docPages []string, idx docIndex) Shard {
	root := w.root
	s := Shard{Name: markersShardName}
	type fileHits struct {
		rel  string
		hits []markerHit
	}
	var found []fileHits
	var unknown []unknownMarker
	for _, rel := range w.markerSources(docPages) {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || !bytes.Contains(src, []byte("magus:")) || bytes.IndexByte(src, 0) >= 0 {
			continue
		}
		hits := scanMarkers(rel, src)
		for _, h := range hits {
			if !types.MarkerFamily(h.family).Valid() {
				unknown = append(unknown, unknownMarker{rel, h.line, h.family})
			}
		}
		if len(hits) > 0 {
			found = append(found, fileHits{rel, hits})
		}
	}
	if len(unknown) > 0 {
		s.Err = unknownMarkerFamilyError(unknown)
		return s
	}

	seen := map[string]bool{}
	notePath := func(rel, id, kind string) string {
		if seen[id] {
			return id
		}
		seen[id] = true
		s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: id, Kind: kind, Label: rel, Source: rel})
		if rel == "." {
			return id
		}
		owner, ok := owningProjectPath(rel, projects)
		switch {
		case !ok:
		case owner == rel:
			// containsChain walks up from the leaf's parent, which here is above the project.
			s.Edges = append(s.Edges, extractedEdge(projectID(owner), id, types.RelationContains, rel))
		default:
			dn, de := containsChain(owner, rel, id)
			s.Nodes = append(s.Nodes, dn...)
			s.Edges = append(s.Edges, de...)
		}
		return id
	}
	var embeds map[string][]string
	calls := map[[2]string][]string{} // (caller dir, dst dir) -> transports
	callProv := map[[2]string]string{}

	for _, fh := range found {
		// A page the doc shard holds already has its node; a second file node for the
		// same path would split its markers from its sections.
		fID := docID(fh.rel)
		if !idx.docs[fh.rel] {
			fID = notePath(fh.rel, fileID(fh.rel), types.KindFile)
		}
		for _, m := range foldMarkers(fh.hits) {
			id := markerID(fh.rel, m.line)
			prov := fh.rel + ":" + strconv.Itoa(m.line)
			positional, stamps := parseMarkerArgs(m.args)
			attrs := stamps
			attrs[types.AttrMarkerFamily] = m.family
			attrs[types.AttrLine] = strconv.Itoa(m.line)
			attrs[types.AttrMarkerVerb] = string(types.MarkerPoint)
			if m.verb != "" {
				attrs[types.AttrMarkerVerb] = string(types.MarkerBlock)
			}
			if m.endLine > 0 {
				attrs[types.AttrEndLine] = strconv.Itoa(m.endLine)
			}
			if m.args != "" {
				attrs[types.AttrMarkerArgs] = sanitize(m.args, maxLabelLen)
			}
			var targets []string
			unresolved := m.unresolved
			switch types.MarkerFamily(m.family) {
			case types.MarkerDiagram:
				if len(positional) == 0 {
					unresolved = "magus:diagram names no figure id"
					break
				}
				if embeds == nil {
					embeds = figureEmbeds(root, docPages, idx)
				}
				targets = embeds[positional[0]]
				if len(targets) == 0 {
					unresolved = "no docs page embeds figure " + positional[0]
				}
			case types.MarkerCalls:
				dst, transport, reason := resolveCall(root, positional)
				if reason != "" {
					unresolved = reason
					break
				}
				dstID := notePath(dst, dirID(dst), types.KindDir)
				targets = []string{dstID}
				caller := path.Dir(fh.rel)
				notePath(caller, dirID(caller), types.KindDir)
				k := [2]string{caller, dst}
				if !slices.Contains(calls[k], transport) {
					calls[k] = append(calls[k], transport)
				}
				if _, ok := callProv[k]; !ok {
					callProv[k] = prov
				}
			}
			if unresolved != "" {
				attrs[attrMarkerUnresolved] = unresolved
			}
			label := "magus:" + m.family
			// Only diagram and calls name a subject; the stamp families' words are prose.
			if f := types.MarkerFamily(m.family); (f == types.MarkerDiagram || f == types.MarkerCalls) && len(positional) > 0 {
				label += " " + positional[0]
			}
			s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: id, Kind: types.KindMarker, Label: label, Source: prov, Attrs: attrs})
			s.Edges = append(s.Edges, declaredEdge(fID, id, types.RelationContains, prov))
			for _, t := range targets {
				s.Edges = append(s.Edges, declaredEdge(id, t, types.RelationReferences, prov))
			}
		}
	}
	// One edge per package pair, since edges dedupe on (source, target, relation); every
	// transport declared for the pair rides on it.
	pairs := slices.Collect(maps.Keys(calls))
	slices.SortFunc(pairs, func(a, b [2]string) int {
		if c := strings.Compare(a[0], b[0]); c != 0 {
			return c
		}
		return strings.Compare(a[1], b[1])
	})
	for _, k := range pairs {
		ts := calls[k]
		slices.Sort(ts)
		e := declaredEdge(dirID(k[0]), dirID(k[1]), types.RelationCalls, callProv[k])
		e.Attrs = map[string]string{types.AttrTransport: strings.Join(ts, ",")}
		s.Edges = append(s.Edges, e)
	}
	return s
}

// resolveCall reads a calls marker's `<dst dir> <transport>`. The destination must be a
// directory this workspace holds; anything else is a reason, not an edge.
func resolveCall(root string, positional []string) (dst, transport, reason string) {
	if len(positional) < 2 {
		return "", "", "magus:calls needs <dst dir> <transport>"
	}
	dst = path.Clean(strings.TrimPrefix(positional[0], "./"))
	if dst != "." && !workspaceContainsPath(dst) {
		return "", "", "magus:calls destination " + positional[0] + " is outside the workspace"
	}
	if !isDir(root, dst) {
		return "", "", "magus:calls destination " + positional[0] + " is not a directory"
	}
	return dst, positional[1], ""
}

// figureEmbeds maps a figure id to the docsection (or, above every heading, the doc)
// holding its `<!--diagram:<id>-->` line. Only nodes the doc shard holds are returned.
func figureEmbeds(root string, docPages []string, idx docIndex) map[string][]string {
	out := map[string][]string{}
	for _, rel := range docPages {
		if !idx.docs[rel] {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || !bytes.Contains(src, []byte("<!--diagram:")) {
			continue
		}
		body := []byte(docs.StripFrontmatter(string(src)))
		type heading struct {
			start  int
			anchor string
		}
		var headings []heading
		_ = ast.Walk(headingMD.Parser().Parse(text.NewReader(body)), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			h, ok := n.(*ast.Heading)
			if !ok || !entering {
				return ast.WalkContinue, nil
			}
			raw, ok := h.AttributeString("id")
			id, _ := raw.([]byte)
			if ok && len(id) > 0 && h.Lines().Len() > 0 {
				headings = append(headings, heading{h.Lines().At(0).Start, string(id)})
			}
			return ast.WalkSkipChildren, nil
		})
		offset := 0
		for _, line := range bytes.Split(body, []byte("\n")) {
			if m := figureEmbedRe.FindSubmatch(bytes.TrimSpace(line)); m != nil {
				target := docID(rel)
				for _, h := range headings {
					if h.start > offset {
						break
					}
					if idx.sections[rel+"#"+h.anchor] {
						target = docSectionID(rel, h.anchor)
					}
				}
				if id := string(m[1]); !slices.Contains(out[id], target) {
					out[id] = append(out[id], target)
				}
			}
			offset += len(line) + 1
		}
	}
	for id := range out {
		slices.Sort(out[id])
	}
	return out
}

// markerSources returns every workspace file the marker scan reads, sorted: the source
// walk plus docPages, less whatever the VCS ignores. Unlike the discovery walk it descends
// into gen/, where generated blocks carry their stamps (see dirScans for the directories
// it skips). Go test files are skipped, because a fixture quoting a marker declares
// nothing, and so is anything over maxMarkerFileBytes.
func (w *TreeWalk) markerSources(docPages []string) []string {
	out := slices.Clone(docPages)
	for _, f := range w.files {
		if f.scans&walkMarkers == 0 || !f.info.Mode().IsRegular() || strings.HasSuffix(f.rel, "_test.go") {
			continue
		}
		if f.info.Size() > maxMarkerFileBytes {
			continue
		}
		out = append(out, f.rel)
	}
	slices.Sort(out)
	return w.dropIgnored(slices.Compact(out))
}
