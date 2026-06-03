// Package targetgraph extracts a magusfile's target dependency graph statically,
// without evaluating any target body. Every `export fun` is a node, its leading
// doc comment is the node's description, and the `magus.depends_on` /
// `magus.target.expand_globs` calls in its body are its edges. Because it reads
// the source rather than running it, it is deterministic and sees *both* arms of
// a runtime branch (e.g. the `container` charm toggle on `build`) — a runtime
// trace would only ever see the arm taken.
//
// It is a static-analysis helper for tooling, not part of the execution core:
// `magus describe graph` renders it and reports cycles (via Cycle), including the
// `-o markdown` MAGUS.md doc. Run-time cycle enforcement is a separate concern
// owned by the dispatch pool. Node and dependency names are normalized with the
// same kebab-case normalizer the run path registers targets under, so a node and
// an edge that name the same target always reconcile.
//
// The extractor understands the Buzz syntax (`export fun`, `//` comments,
// `[...]` lists); callers gate on the engine, so a project on any other engine
// is skipped until an extractor for it lands.
package targetgraph

import (
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

var (
	exportFunRe = regexp.MustCompile(`^export\s+fun\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	dependsRe   = regexp.MustCompile(`magus\.depends_on\(\s*\[([^\]]*)\]`)
	expandRe    = regexp.MustCompile(`expand_globs\(([^)]*)\)`)
	quotedRe    = regexp.MustCompile(`"([^"]+)"`)
	hasCharmRe  = regexp.MustCompile(`has_charm\(\s*"([^"]+)"`)
)

// Node is one target: a runnable `magus run <Name>`. Deps are the resolved
// dependency target names (literal depends_on names first, in source order, then
// the names matched by each expand_globs pattern); self-edges and duplicates are
// dropped. Charms are the charm names the body branches on (via magus.has_charm,
// e.g. has_charm("rw") for the built-in read→write toggle), sorted; this catches
// only the charms a target's own code reads, not those its spells declare.
type Node struct {
	Name   string   `json:"name"`
	Doc    string   `json:"doc,omitempty"`
	Deps   []string `json:"deps,omitempty"`
	Charms []string `json:"charms,omitempty"`
}

// Graph is the target dependency graph for one magusfile, nodes in source order.
type Graph struct {
	Nodes []Node `json:"nodes"`
}

// Extract parses a Buzz magusfile's source into a Graph (best-effort, never
// errors — malformed source yields a partial graph). Named Extract, not Build, to
// signal static-source extraction and to not collide with depgraph.Build, which
// constructs a different graph. Pass the concatenated contents of all of a
// project's magusfile sources (load order).
func Extract(source string) Graph {
	lines := strings.Split(source, "\n")

	type raw struct {
		node     Node
		depGlobs []string
	}
	// Node and dep names both go through the run path's kebab-case normalizer, so a
	// node and an edge that name the same target always reconcile.
	norm := types.DefaultTargetNameNormalizer.NormalizeTargetName
	var raws []raw
	for i, line := range lines {
		m := exportFunRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		r := raw{node: Node{Name: norm(m[1]), Doc: leadingDoc(lines, i)}}
		body := codeBody(lines, i)
		for _, dm := range dependsRe.FindAllStringSubmatch(body, -1) {
			for _, q := range quotedRe.FindAllStringSubmatch(dm[1], -1) {
				r.node.Deps = appendUniq(r.node.Deps, norm(q[1]))
			}
		}
		for _, em := range expandRe.FindAllStringSubmatch(body, -1) {
			for _, q := range quotedRe.FindAllStringSubmatch(em[1], -1) {
				r.depGlobs = appendUniq(r.depGlobs, q[1])
			}
		}
		for _, cm := range hasCharmRe.FindAllStringSubmatch(body, -1) {
			r.node.Charms = appendUniq(r.node.Charms, cm[1])
		}
		slices.Sort(r.node.Charms)
		raws = append(raws, r)
	}

	g := Graph{Nodes: make([]Node, 0, len(raws))}
	names := make([]string, len(raws))
	for i, r := range raws {
		names[i] = r.node.Name
	}
	for _, r := range raws {
		for _, glob := range r.depGlobs {
			re := globRe(glob)
			for _, n := range names {
				if n != r.node.Name && re.MatchString(n) {
					r.node.Deps = appendUniq(r.node.Deps, n)
				}
			}
		}
		g.Nodes = append(g.Nodes, r.node)
	}
	return g
}

// Cycle returns a dependency cycle as a path of node names ending where it began
// (e.g. ["a","b","a"]), or nil if the graph is acyclic. Edges to undeclared
// targets are ignored here — that is a separate "unknown target" error the run
// path already raises.
func (g Graph) Cycle() []string {
	deps := make(map[string][]string, len(g.Nodes))
	for _, n := range g.Nodes {
		deps[n.Name] = n.Deps
	}
	// Classic 3-color DFS: white (unvisited) is the implicit 0 zero-value, grey is
	// on the current DFS stack (a back-edge to grey is a cycle), black is fully done.
	const (
		grey  = 1
		black = 2
	)
	state := map[string]int{}
	var stack []string
	var visit func(n string) []string
	visit = func(n string) []string {
		state[n] = grey
		stack = append(stack, n)
		for _, d := range deps[n] {
			if _, declared := deps[d]; !declared {
				continue
			}
			switch state[d] {
			case grey:
				// Back-edge: the cycle is the stack suffix from d, closed with d.
				for i, s := range stack {
					if s == d {
						return append(append([]string(nil), stack[i:]...), d)
					}
				}
			case 0:
				if c := visit(d); c != nil {
					return c
				}
			}
		}
		state[n] = black
		stack = stack[:len(stack)-1]
		return nil
	}
	for _, n := range g.Nodes {
		if state[n.Name] == 0 {
			if c := visit(n.Name); c != nil {
				return c
			}
		}
	}
	return nil
}

// leadingDoc returns the first sentence of the contiguous `//` comment block
// directly above line i. Contiguity is strict — a blank line breaks the block —
// so a section divider a blank line above its functions never bleeds in. Divider
// lines (the `── … ──` rules) are dropped.
func leadingDoc(lines []string, i int) string {
	var block []string
	for j := i - 1; j >= 0; j-- {
		s := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(s, "//") {
			break
		}
		block = append(block, s)
	}
	var prose []string
	for k := len(block) - 1; k >= 0; k-- {
		s := strings.TrimSpace(strings.TrimPrefix(block[k], "//"))
		if s == "" || strings.Contains(s, "──") {
			continue
		}
		prose = append(prose, s)
	}
	return firstSentence(strings.Join(prose, " "))
}

// firstSentence returns s up to and including the first period that ends a
// sentence (followed by a space or end of string), godoc-style. A period inside a
// token like `extra.fs.watch` is not a sentence end, so it is left intact. Byte
// iteration is safe: `.` and space are ASCII and never appear as a UTF-8
// continuation byte, so a multibyte rune can't be mis-split.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && (i == len(s)-1 || s[i+1] == ' ') {
			return s[:i+1]
		}
	}
	return s
}

// codeBody returns the function's text from its `export fun` line through the
// brace that closes its body, found by balancing braces. Braces and `//`
// comments inside string literals are ignored, and trailing `//` comments are
// stripped — so a `}` in a string can't truncate the body and a depends_on
// mentioned in a comment can't fake an edge. (Buzz has no block comments.)
func codeBody(lines []string, start int) string {
	var b strings.Builder
	depth, opened := 0, false
	inStr, esc := false, false
	for j := start; j < len(lines); j++ {
		line := lines[j]
		comment := false
		for k := 0; k < len(line); k++ {
			c := line[k]
			switch {
			case comment:
				// rest of the line is a // comment — drop it
			case inStr:
				b.WriteByte(c)
				switch {
				case esc:
					esc = false
				case c == '\\':
					esc = true
				case c == '"':
					inStr = false
				}
			case c == '/' && k+1 < len(line) && line[k+1] == '/':
				comment = true
			case c == '"':
				inStr = true
				b.WriteByte(c)
			case c == '{':
				depth, opened = depth+1, true
				b.WriteByte(c)
			case c == '}':
				depth--
				b.WriteByte(c)
			default:
				b.WriteByte(c)
			}
		}
		b.WriteByte('\n')
		if opened && depth <= 0 {
			break
		}
	}
	return b.String()
}

// globRe compiles a target-name glob (only `*` is special) to an anchored regexp.
func globRe(pattern string) *regexp.Regexp {
	parts := strings.Split(pattern, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile("^" + strings.Join(parts, ".*") + "$")
}

func appendUniq(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}
