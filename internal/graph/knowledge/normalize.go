package knowledge

import "github.com/egladman/magus/internal/file"

// normalizePaths rewrites the path-SHAPED parts of a parsed query to the
// workspace-relative slash form node IDs carry, so "./a/b", "/a/b", an absolute path
// under the workspace root, and "a\b" all resolve the node a bare "a/b" resolves.
//
// Free-text terms and the EXACT values of id/project only. A =~ value is a regex, where
// a backslash escapes and path.Clean would silently corrupt the pattern, so reFields is
// never touched; kind, language, role and relation values are enumerations, not paths.
//
// Applied in Resolve, which every surface reaches - the CLI verbs, the MCP tools, and
// GraphService all answer through Graph methods that funnel there - so no caller has to
// remember to normalize, and none can reach a different answer about one graph.
func (g *Graph) normalizePaths(q parsedQuery) parsedQuery {
	// parseQuery mints these slices per call, so rewriting in place shares nothing.
	norm := func(vals []string) {
		for i, v := range vals {
			if out, ok := file.NormalizeWorkspacePath(v, g.root); ok {
				vals[i] = out
			}
		}
	}
	norm(q.terms)
	norm(q.negTerms)
	for _, f := range []string{"id", "project"} {
		norm(q.fields[f])
		norm(q.negFields[f])
	}
	return q
}
