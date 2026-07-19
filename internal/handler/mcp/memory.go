package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	// read_all is the explicit opt-in to the WHOLE journal. The default read op
	// windows progress/decisions (a table of contents plus the last N entries) to
	// bound session-start cost as the journals grow; read_all bypasses the window.
	// It is an ordinary read at the file layer - only the post-read shaping differs.
	full := op == "read_all"
	if full {
		op = "read"
	}

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

	// The durable memory files share the scratchpad's exact read/write/append/clear file
	// semantics, just targeted at file+".md" under the per-repo memory dir.
	name := file + ".md"
	path := filepath.Join(dir, name)
	res, err := scratchpadOpFile(dir, name, op, content)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	// Windowed read: status is a snapshot and always returns in full, but the dated
	// journals (progress/decisions) grow unbounded while every session reads them at
	// ramp. The default read therefore returns a table of contents plus the last
	// memoryReadWindow entries; read_all opts back into the full file.
	if op == "read" && !full && (file == "progress" || file == "decisions") {
		windowed := windowMemory(res.Content, memoryReadWindow)
		res.Content = windowed
		res.Bytes = len(windowed)
	}
	return types.InvokeResponse{Data: memoryResult{scratchpadResult: res, File: file, Path: path}}, nil
}

// memoryReadWindow is how many of the most recent journal entries a default read
// returns in full (older entries collapse into the table of contents). Five is
// enough to re-establish immediate context without paying for the whole history.
const memoryReadWindow = 5

// memoryProgressWindow is how many recent progress entries the rotate-memory job
// keeps in the live journal; anything older moves to the archive sidecar. The
// decisions log is NEVER compacted (an explicit decision), so this applies to
// progress only.
const memoryProgressWindow = 50

// memoryEntry is one dated section of an append-journal: its heading text (the
// "2026-01-02 - title" the append stamped, without the leading "## ") and the whole
// verbatim section from the heading line to the blank line before the next. Entries
// are the unit the windowed read slices and the rotate-memory job compacts.
type memoryEntry struct {
	heading string
	text    string
}

// entryHeadingRE matches ONLY the stamped entry-heading shape the append op writes:
// a level-2 ATX heading whose text begins with an ISO date ("## 2026-01-02", with or
// without a "- title" suffix). It is the entry boundary. A plain "## " line inside an
// entry body (an agent pasting a markdown subsection into its note) is NOT a boundary,
// so it stays part of the entry rather than splitting one entry into two.
var entryHeadingRE = regexp.MustCompile(`^## \d{4}-\d\d-\d\d`)

// parseMemoryEntries splits an append-journal into its dated sections in file order.
// Appends stamp each entry as a level-2 ATX heading led by an ISO date (md.Builder), so
// a line matching entryHeadingRE starts a new entry; any other "## " line is ordinary
// body. Any preamble before the first heading is returned separately so a rewrite can
// preserve it; a file with no dated headings yields no entries and the whole content as
// preamble. Each entry's text is right-trimmed of blank lines so callers can rejoin with
// a uniform "\n\n" separator.
func parseMemoryEntries(content string) (preamble string, entries []memoryEntry) {
	var preLines, curLines []string
	var curHeading string
	started := false
	flush := func() {
		if started {
			entries = append(entries, memoryEntry{
				heading: curHeading,
				text:    strings.TrimRight(strings.Join(curLines, "\n"), "\n"),
			})
		}
	}
	for _, ln := range strings.Split(content, "\n") {
		if entryHeadingRE.MatchString(ln) {
			flush()
			started = true
			curHeading = strings.TrimSpace(strings.TrimPrefix(ln, "## "))
			curLines = []string{ln}
			continue
		}
		if started {
			curLines = append(curLines, ln)
		} else {
			preLines = append(preLines, ln)
		}
	}
	flush()
	return strings.TrimRight(strings.Join(preLines, "\n"), "\n"), entries
}

// windowMemory shapes a journal for the default read: a table of contents of every
// entry heading (oldest to newest, so the full timeline stays visible) followed by
// the last n entries in full. A journal with n or fewer entries is returned verbatim
// - there is nothing to collapse, so the reader sees exactly what read_all would.
func windowMemory(content string, n int) string {
	preamble, entries := parseMemoryEntries(content)
	if len(entries) <= n {
		return content
	}
	var b md.Builder
	if preamble != "" {
		b.Paragraph(preamble)
	}
	b.Heading(2, "Table of contents")
	var toc strings.Builder
	for _, e := range entries {
		toc.WriteString("- ")
		toc.WriteString(e.heading)
		toc.WriteByte('\n')
	}
	b.Paragraph(strings.TrimRight(toc.String(), "\n"))
	b.Paragraph(fmt.Sprintf("Showing the last %d of %d entries in full. Read with op=read_all for the complete journal.", n, len(entries)))
	for _, e := range entries[len(entries)-n:] {
		b.Paragraph(e.text)
	}
	return string(b.Bytes())
}

// RotateProgress compacts the progress journal under the memory directory for root:
// it keeps the most recent memoryProgressWindow entries in progress.md and appends
// everything older to progress.archive.md beside it. The decisions log is never
// touched. A missing journal or one within the window is left as-is. It returns the
// counts kept in the live journal and moved to the archive.
//
// It is the worker behind the rotate-memory background job. The archive is appended
// BEFORE the live journal is rewritten so a crash between the two can at worst
// duplicate an entry into the archive on a later run, never lose one.
func RotateProgress(root string) (kept, archived int, err error) {
	dir, err := memoryDir(root)
	if err != nil {
		return 0, 0, err
	}
	return rotateProgressDir(dir, memoryProgressWindow)
}

func rotateProgressDir(dir string, window int) (kept, archived int, err error) {
	path := filepath.Join(dir, "progress.md")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("mcp: rotate progress: %w", err)
	}
	preamble, entries := parseMemoryEntries(string(b))
	if len(entries) <= window {
		return len(entries), 0, nil
	}
	split := len(entries) - window
	old, recent := entries[:split], entries[split:]

	var ab strings.Builder
	for _, e := range old {
		ab.WriteString(e.text)
		ab.WriteString("\n\n")
	}
	f, err := os.OpenFile(filepath.Join(dir, "progress.archive.md"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, 0, fmt.Errorf("mcp: rotate progress archive: %w", err)
	}
	if _, werr := f.WriteString(ab.String()); werr != nil {
		f.Close()
		return 0, 0, fmt.Errorf("mcp: rotate progress archive: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		return 0, 0, fmt.Errorf("mcp: rotate progress archive: %w", cerr)
	}

	var nb strings.Builder
	if preamble != "" {
		nb.WriteString(preamble)
		nb.WriteString("\n\n")
	}
	for _, e := range recent {
		nb.WriteString(e.text)
		nb.WriteString("\n\n")
	}
	// Replace progress.md atomically: write the compacted journal to a temp file in the
	// SAME directory (so the rename is a same-filesystem metadata swap) and rename it over
	// the original. A crash mid-write then leaves either the old journal or the new one
	// whole, never a truncated file - the archive was already appended above, so at worst
	// a later run re-archives an entry, and at best a compaction is a clean all-or-nothing.
	tmp, err := os.CreateTemp(dir, "progress-*.md.tmp")
	if err != nil {
		return 0, 0, fmt.Errorf("mcp: rotate progress rewrite: %w", err)
	}
	tmpName := tmp.Name()
	if _, werr := tmp.WriteString(nb.String()); werr != nil {
		tmp.Close()
		os.Remove(tmpName)
		return 0, 0, fmt.Errorf("mcp: rotate progress rewrite: %w", werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		os.Remove(tmpName)
		return 0, 0, fmt.Errorf("mcp: rotate progress rewrite: %w", cerr)
	}
	if rerr := os.Rename(tmpName, path); rerr != nil {
		os.Remove(tmpName)
		return 0, 0, fmt.Errorf("mcp: rotate progress rewrite: %w", rerr)
	}
	return len(recent), len(old), nil
}

// MemoryDir is the exported view of memoryDir: the per-repository memory directory
// holding the durable status/progress/decisions files. The console-facing MemoryService
// handler (internal/handler/memory) resolves the same directory through this, so the
// browser edit surface and the magus_memory MCP tool operate on ONE set of files - there
// is a single definition of where memory lives.
func MemoryDir(root string) (string, error) { return memoryDir(root) }

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

var _ types.SpellDriver = (*memoryTool)(nil)
