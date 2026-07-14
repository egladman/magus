package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/render/md"
	"github.com/egladman/magus/types"
)

// memoryTool (magus_memory) is the durable counterpart to magus_scratchpad:
// three plain-markdown files (status, progress, decisions) that persist across
// sessions, models, and agent hosts. They live in the user's XDG state
// directory, NOT the repo and NOT the cache - the repo is shared with other
// contributors (one developer's working memory does not belong in it) and the
// cache is evictable. Keyed by the repository (worktrees of one repo share a
// key), so switching branches or worktrees never hides what an earlier session
// recorded; entries are date-stamped instead.
type memoryTool struct{ opts Options }

func (t *memoryTool) Name() string { return ToolMemory.String() }

// memoryFiles is the closed set of durable files. Adding a name here is an API
// decision (agents across every host see it), so the set stays deliberate:
// status = current snapshot (overwrite), progress = dated work journal
// (append), decisions = dated decision log with the why (append).
var memoryFiles = map[string]bool{"status": true, "progress": true, "decisions": true}

// memoryResult extends the scratchpad wire shape with the on-disk path, so a
// human can be pointed at the file and other tooling can read it directly.
type memoryResult struct {
	scratchpadResult
	File string `json:"file"`
	Path string `json:"path"`
}

func (t *memoryTool) Invoke(_ context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	file := paramString(req.Params, "file", "")
	if !memoryFiles[file] {
		return types.InvokeResponse{}, errors.New("mcp: memory file must be one of status, progress, decisions")
	}
	op := paramString(req.Params, "op", "read")
	content := paramString(req.Params, "content", "")

	dir, err := memoryDir(t.opts.Magus.Root())
	if err != nil {
		return types.InvokeResponse{}, err
	}
	// Appends to the journals are date-stamped server-side, so the entry order
	// and timeline survive any model or host - the agent cannot forget to date
	// an entry. An optional title folds into the heading, so `grep '^## '` over
	// the file reads as a table of contents instead of bare dates. status is a
	// snapshot; write replaces it undated.
	if op == "append" && (file == "progress" || file == "decisions") {
		heading := time.Now().Format("2006-01-02")
		if title := strings.TrimSpace(paramString(req.Params, "title", "")); title != "" {
			heading += " - " + title
		}
		var b md.Builder
		b.Heading(2, heading)
		b.Paragraph(strings.TrimRight(content, "\n"))
		content = string(b.Bytes())
	}

	path := filepath.Join(dir, file+".md")
	res, err := fileOp(path, op, content)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	return types.InvokeResponse{Data: memoryResult{scratchpadResult: res, File: file, Path: path}}, nil
}

// memoryDir resolves the per-repository memory directory:
// <XDG state>/magus/memory/<repo-basename>-<hash12>. The hash keys on the
// repository identity, not the checkout path: a linked git worktree's .git FILE
// points at the main repository's git dir, and that target is what gets hashed,
// so every worktree of a repo shares one memory. Repos without that shape
// (plain checkouts, hg, no VCS) key on the workspace root path.
func memoryDir(root string) (string, error) {
	base, err := config.UserStateDir()
	if err != nil {
		return "", fmt.Errorf("mcp: memory state dir: %w", err)
	}
	id := repoIdentity(root)
	sum := sha256.Sum256([]byte(id))
	name := filepath.Base(id) + "-" + hex.EncodeToString(sum[:])[:12]
	return filepath.Join(base, "magus", "memory", name), nil
}

// repoIdentity returns the path that identifies the repository behind root. A
// linked worktree's .git is a file holding "gitdir: <main>/.git/worktrees/<n>";
// resolve it to <main> so worktrees share identity. Anything else identifies as
// the root itself.
func repoIdentity(root string) string {
	b, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return root // .git is a directory (plain checkout) or absent (other VCS)
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	if gitdir == "" {
		return root
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	// <main>/.git/worktrees/<name> -> <main>
	if i := strings.Index(filepath.ToSlash(gitdir), "/.git/worktrees/"); i >= 0 {
		return filepath.Clean(gitdir[:i])
	}
	return filepath.Clean(gitdir)
}

// fileOp applies a scratchpad-style op to one file path. It reuses
// scratchpadOp's semantics but targets an exact file rather than a fixed name
// under a directory.
func fileOp(path, op, content string) (scratchpadResult, error) {
	dir, name := filepath.Split(path)
	res, err := scratchpadOpFile(dir, name, op, content)
	if err != nil {
		return scratchpadResult{}, err
	}
	return res, nil
}

var _ types.SpellDriver = (*memoryTool)(nil)
