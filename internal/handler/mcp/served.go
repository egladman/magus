package mcp

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// servedMetaKey is the key under a tool result's _meta that carries the served identity.
const servedMetaKey = "magus"

// rootsTimeout bounds the one roots/list round trip a session pays, so a client that
// declares roots and never answers costs one call two seconds, not every call.
const rootsTimeout = 2 * time.Second

// servedInfo is what every tool result carries under _meta.magus: which checkout and
// build answered, and where that differs from the caller.
type servedInfo struct {
	Root      string       `json:"root"`
	Version   string       `json:"version"`
	Commit    string       `json:"commit,omitempty"`
	Transport string       `json:"transport"`
	Skew      []servedSkew `json:"skew,omitempty"`
}

// servedSkew is one way the server differs from its caller. Kind "root": the caller's
// declared roots are other checkouts. Kind "build": the served executable was replaced
// on disk after this process started, so it answers at an older build than the tree has.
type servedSkew struct {
	Kind       string   `json:"kind"`
	Caller     []string `json:"caller,omitempty"`
	Executable string   `json:"executable,omitempty"`
}

// served stamps the served identity on tool results. Build skew comes from the executable
// this process runs, compared by stat against how it stood at startup. Root skew comes from
// the caller's MCP roots, asked once per session and only of a client that declared the
// roots capability; a client that declared none gets no root verdict, never a guess.
// Over stdio there is no root skew by construction: the host started this process in the
// checkout it serves.
type served struct {
	root  string
	build types.BuildInfo
	exe   string
	start os.FileInfo
	// roots returns the caller's declared roots, ok false when the caller declared none.
	roots func(ctx context.Context) (roots []string, ok bool)
}

// newServed records the identity of the process serving root. A missing executable (or
// one that cannot be stat'ed) leaves build skew unreported.
func newServed(root string, build types.BuildInfo) *served {
	s := &served{root: root, build: build, roots: newSessionRoots().lookup}
	if exe, err := os.Executable(); err == nil {
		s.watchExecutable(exe)
	}
	return s
}

func (s *served) watchExecutable(exe string) {
	if fi, err := os.Stat(exe); err == nil {
		s.exe, s.start = exe, fi
	}
}

// annotate stamps every result fn returns, tool errors included: a wrong-checkout answer
// most often surfaces as an error the skew explains.
func (s *served) annotate(fn handlerFn) handlerFn {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		res, err := fn(ctx, req)
		if res != nil {
			s.stamp(ctx, res)
		}
		return res, err
	}
}

// stamp sets _meta.magus on res and, when the server differs from the caller, appends
// one text line: hosts do not show _meta to the model, and a skew the model never reads
// fixes nothing. The line also drops the structured content, because a host that
// has it reads it instead of the text blocks and would never show the line.
func (s *served) stamp(ctx context.Context, res *mcplib.CallToolResult) {
	info := s.info(ctx)
	if res.Meta == nil {
		res.Meta = &mcplib.Meta{}
	}
	if res.Meta.AdditionalFields == nil {
		res.Meta.AdditionalFields = map[string]any{}
	}
	res.Meta.AdditionalFields[servedMetaKey] = info
	if line := info.skewLine(); line != "" {
		res.Content = append(res.Content, mcplib.NewTextContent(line))
		res.StructuredContent = nil
	}
}

func (s *served) info(ctx context.Context) servedInfo {
	info := servedInfo{Root: s.root, Version: s.build.Version, Commit: s.build.Commit, Transport: "http"}
	stdio := trail.CredentialFromContext(ctx).Kind == types.KindStdio
	if stdio {
		info.Transport = "stdio"
	}
	if !stdio {
		if roots, ok := s.roots(ctx); ok && !servesAny(s.root, roots) {
			info.Skew = append(info.Skew, servedSkew{Kind: "root", Caller: roots})
		}
	}
	if s.start != nil {
		if fi, err := os.Stat(s.exe); err != nil || !os.SameFile(s.start, fi) || !fi.ModTime().Equal(s.start.ModTime()) || fi.Size() != s.start.Size() {
			info.Skew = append(info.Skew, servedSkew{Kind: "build", Executable: s.exe})
		}
	}
	return info
}

// skewLine renders the skew as the one line a model reads, empty when there is none.
func (i servedInfo) skewLine() string {
	var parts []string
	for _, k := range i.Skew {
		switch k.Kind {
		case "root":
			parts = append(parts, fmt.Sprintf("this server serves %s and your session is in %s; register `./magus mcp` per checkout (magus describe harness)",
				i.Root, strings.Join(k.Caller, ", ")))
		case "build":
			parts = append(parts, fmt.Sprintf("%s was rebuilt after this server started at %s; restart the MCP server", k.Executable, i.Version))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "magus skew: " + strings.Join(parts, "; ")
}

// servesAny reports whether any of the caller's roots lies in the workspace at root. Each
// is resolved to its own workspace first, so a caller in a worktree nested under root is a
// different checkout, not a subdirectory of this one.
func servesAny(root string, roots []string) bool {
	want := canonicalPath(root)
	for _, r := range roots {
		ws, err := magus.FindRoot(r)
		if err != nil {
			ws = r
		}
		if canonicalPath(ws) == want {
			return true
		}
	}
	return false
}

func canonicalPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Clean(p)
}

// sessionRoots asks each session's client for its roots once and remembers the answer,
// failures included, so a client that times out is not asked again on every call.
type sessionRoots struct {
	mu   sync.Mutex
	seen map[string][]string
}

func newSessionRoots() *sessionRoots {
	return &sessionRoots{seen: map[string][]string{}}
}

func (r *sessionRoots) lookup(ctx context.Context) ([]string, bool) {
	session := server.ClientSessionFromContext(ctx)
	srv := server.ServerFromContext(ctx)
	if session == nil || srv == nil {
		return nil, false
	}
	withInfo, ok := session.(server.SessionWithClientInfo)
	if !ok || withInfo.GetClientCapabilities().Roots == nil {
		return nil, false
	}
	id := session.SessionID()
	r.mu.Lock()
	roots, asked := r.seen[id]
	r.mu.Unlock()
	if !asked {
		roots = requestRoots(ctx, srv)
		r.mu.Lock()
		r.seen[id] = roots
		r.mu.Unlock()
	}
	return roots, len(roots) > 0
}

// requestRoots asks the client for its roots and returns the file:// ones as paths.
func requestRoots(ctx context.Context, srv *server.MCPServer) []string {
	ctx, cancel := context.WithTimeout(ctx, rootsTimeout)
	defer cancel()
	res, err := srv.RequestRoots(ctx, mcplib.ListRootsRequest{})
	if err != nil {
		return nil
	}
	var paths []string
	for _, root := range res.Roots {
		u, err := url.Parse(root.URI)
		if err != nil || u.Scheme != "file" || u.Path == "" {
			continue
		}
		paths = append(paths, u.Path)
	}
	return paths
}
