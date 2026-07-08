package knowledge

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/types"
)

// Ownership is EXTRACTED from a committed CODEOWNERS file, never inferred: the
// graph takes what the file declares (owner, path pattern, line) and nothing more.
// Blame-derived ownership is deliberately excluded - that is analytics, insight's
// job, not a verifiable graph edge. CODEOWNERS is committed and deterministic, so
// the shard is remote-shareable like the other extracted shards (unlike @runtime).

// OwnersShardName is the singleton shard holding CODEOWNERS owner nodes and the
// owns edges to the projects and files they cover.
const OwnersShardName = "@owners"

// codeownersPaths are the locations GitHub recognizes for a CODEOWNERS file, in
// precedence order (first found wins); we read the first that exists.
var codeownersPaths = []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"}

// ownedNode is one path-bearing node CODEOWNERS patterns are matched against: a
// project or a buzz file. Its ID gets the owns edge; its Path drives the match.
type ownedNode struct {
	ID   string
	Path string
}

// codeownersRule is one CODEOWNERS line: the path pattern, its owners, and the
// 1-based line it came from (for provenance). Rules are kept in file order because
// CODEOWNERS is last-match-wins: a node's effective owners come from the LAST rule
// whose pattern matches it.
type codeownersRule struct {
	pattern string
	owners  []string
	line    int
}

// assembleOwners reads the workspace CODEOWNERS (if any) and emits an owner node
// per distinct owner plus an owns edge to each project/file the owner covers under
// last-match-wins. An absent or empty CODEOWNERS yields an empty shard (dropped by
// the caller). Owners are matched only against nodes that already exist, so a rule
// covering paths outside the graph adds no dangling edge.
func assembleOwners(root string, nodes []ownedNode) Shard {
	s := Shard{Name: OwnersShardName}
	rules := readCodeowners(root)
	if len(rules) == 0 || len(nodes) == 0 {
		return s
	}

	seenOwner := map[string]bool{}
	for _, n := range nodes {
		rule, ok := lastMatch(rules, n.Path)
		if !ok {
			continue
		}
		prov := "CODEOWNERS:" + strconv.Itoa(rule.line)
		for _, owner := range rule.owners {
			oID := ownerID(owner)
			if !seenOwner[owner] {
				seenOwner[owner] = true
				s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: oID, Kind: types.KindOwner, Label: owner})
			}
			s.Edges = append(s.Edges, extractedEdge(oID, n.ID, types.RelationOwns, prov))
		}
	}
	return s
}

// readCodeowners loads the first CODEOWNERS file found, parsed into ordered rules.
// Comment and blank lines are skipped; a line with a pattern but no owners is a
// valid "unset ownership" entry and is kept so it can override an earlier rule.
func readCodeowners(root string) []codeownersRule {
	var path string
	for _, p := range codeownersPaths {
		full := filepath.Join(root, p)
		if _, err := os.Stat(full); err == nil {
			path = full
			break
		}
	}
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var rules []codeownersRule
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		rules = append(rules, codeownersRule{pattern: fields[0], owners: fields[1:], line: line})
	}
	return rules
}

// lastMatch returns the last rule whose pattern matches path (CODEOWNERS
// last-match-wins), and whether one matched with a non-empty owner set. A matching
// rule with no owners (ownership deliberately unset) shadows earlier rules and
// yields ok=false, so no owns edge is emitted for that path.
func lastMatch(rules []codeownersRule, path string) (codeownersRule, bool) {
	for i := len(rules) - 1; i >= 0; i-- {
		if codeownersMatch(rules[i].pattern, path) {
			return rules[i], len(rules[i].owners) > 0
		}
	}
	return codeownersRule{}, false
}

// codeownersMatch reports whether a CODEOWNERS pattern covers a workspace-relative
// path. It implements the common subset of the gitignore-style syntax CODEOWNERS
// uses: a bare "*" matches everything; a trailing "/" is a directory prefix; a glob
// (containing * ? [) matches by segment, also anchored at any depth so "*.go"
// covers nested files; a plain path matches itself or anything beneath it.
func codeownersMatch(pattern, path string) bool {
	pattern = strings.TrimPrefix(pattern, "/")
	if pattern == "" || pattern == "*" || pattern == "**" {
		return true
	}
	if strings.HasSuffix(pattern, "/") {
		dir := strings.TrimSuffix(pattern, "/")
		return path == dir || strings.HasPrefix(path, dir+"/")
	}
	if strings.ContainsAny(pattern, "*?[") {
		if ok, _ := doublestar.Match(pattern, path); ok {
			return true
		}
		ok, _ := doublestar.Match("**/"+pattern, path)
		return ok
	}
	return path == pattern || strings.HasPrefix(path, pattern+"/")
}
