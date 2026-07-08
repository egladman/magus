package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// committedGraphPath is the canonical repo-relative location of a committed
// node-link graph.json (from `magus graph export -o json`). When it exists and the
// repo's raw URL is derivable, MAGUS.md links the hosted Graph Explorer to it.
const committedGraphPath = "docs/graph.json"

// hostedExplorerURL is the hosted, data-agnostic Graph Explorer (the same page
// `magus graph open` targets). Any repo's committed graph.json loads in it via
// #src=, so this is a constant, not the repo's own site.
const hostedExplorerURL = defaultExploreURL

// graphExplorerLink returns a Graph Explorer URL preloaded (via #src=) with the
// repo's committed docs/graph.json, or "" when that file is absent or the repo's
// raw URL is not derivable (so MAGUS.md never emits a link that 404s).
func graphExplorerLink(ctx context.Context, root string) string {
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(committedGraphPath))); err != nil {
		return ""
	}
	raw := githubRawBase(deriveSourceBase(ctx, root))
	if raw == "" {
		return ""
	}
	return hostedExplorerURL + "#src=" + url.QueryEscape(raw+"/"+committedGraphPath)
}

// githubRawBase turns a github.com blob base into its raw.githubusercontent.com
// equivalent ("https://github.com/O/R/blob/B" -> "https://raw.githubusercontent.com/O/R/B").
// Only github.com is mapped; other forges (enterprise, gitlab) use different raw
// hosts, so they return "" rather than a wrong URL.
func githubRawBase(blobBase string) string {
	const p = "https://github.com/"
	if !strings.HasPrefix(blobBase, p) {
		return ""
	}
	return "https://raw.githubusercontent.com/" + strings.Replace(strings.TrimPrefix(blobBase, p), "/blob/", "/", 1)
}

// deriveSourceBase resolves the workspace's VCS remote (via the optional
// types.RemoteReporter capability) and turns it into a forge blob-URL base, so a
// node's relative `source` path can be linked to the RIGHT repository. Returns ""
// (no link) whenever the remote is missing, the backend lacks the capability, or
// the forge is not one we can build a browse URL for - never a guessed/wrong link.
func deriveSourceBase(ctx context.Context, root string) string {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return ""
	}
	res, err := vcs.Resolve(ctx, ws.Root(), "", ws.VCSOptions())
	if err != nil || res.VCS == nil {
		return ""
	}
	reporter, ok := res.VCS.(types.RemoteReporter)
	if !ok {
		return ""
	}
	remote, err := reporter.RemoteURL(ctx, ws.Root())
	if err != nil || remote == "" {
		return ""
	}
	branch := ""
	if meta, err := res.VCS.Metadata(ctx, ws.Root()); err == nil {
		branch = meta.Branch
	}
	return forgeBlobBase(remote, branch)
}

// forgeBlobBase turns a git remote URL + branch into a blob-URL base like
// "https://github.com/owner/repo/blob/main". Only GitHub-style forges are handled;
// anything else returns "" so we never emit a broken link (GitLab's /-/blob/ and
// Bitbucket's /src/ differ, and self-hosted conventions vary - a future addition).
func forgeBlobBase(remote, branch string) string {
	host, owner, repo := parseGitRemote(remote)
	if owner == "" || repo == "" || !strings.Contains(strings.ToLower(host), "github") {
		return ""
	}
	if branch == "" || branch == "HEAD" {
		branch = "main"
	}
	return "https://" + host + "/" + owner + "/" + repo + "/blob/" + branch
}

// parseGitRemote extracts (host, owner, repo) from an scp-style (git@host:owner/repo)
// or URL-style (scheme://[user@]host[:port]/owner/repo) git remote, trimming a
// trailing ".git". Returns empty strings when it can't parse a host + owner + repo.
func parseGitRemote(remote string) (host, owner, repo string) {
	remote = strings.TrimSuffix(strings.TrimSpace(remote), ".git")
	var path string
	switch {
	case strings.Contains(remote, "://"):
		u, err := url.Parse(remote)
		if err != nil {
			return "", "", ""
		}
		host = u.Hostname()
		path = strings.TrimPrefix(u.Path, "/")
	case strings.Contains(remote, "@") && strings.Contains(remote, ":"):
		rest := remote[strings.Index(remote, "@")+1:]
		i := strings.Index(rest, ":")
		if i < 0 {
			return "", "", ""
		}
		host = rest[:i]
		path = strings.TrimPrefix(rest[i+1:], "/")
	default:
		return "", "", ""
	}
	parts := strings.SplitN(path, "/", 3)
	if host == "" || len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return host, "", ""
	}
	return host, parts[0], parts[1]
}
