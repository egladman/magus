package vcs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/stamp"
)

// managedMarkers names one kind of magus-managed section. Four kinds, not one:
// generated-output routing, graph-refresh hooks, drift-notice hooks, and owed-regeneration
// hooks must coexist in the same file (.gitattributes, .hg/hgrc, .sl/config, or a shell
// hook).
//
// Matchers use begin alone (a prefix of the opening LINE), so a section written by an
// older magus whose banner wording differed is still found and rewritten rather than
// duplicated below.
type managedMarkers struct{ begin, end string }

var (
	generatedMarkers = managedMarkers{begin: "# BEGIN magus-generated", end: "# END magus-generated"}
	refreshMarkers   = managedMarkers{begin: "# BEGIN magus-refresh", end: "# END magus-refresh"}
	driftMarkers     = managedMarkers{begin: "# BEGIN magus-drift-notice", end: "# END magus-drift-notice"}
	// The begin is a prefix of the "magus-regenerate-owed" banner an earlier magus
	// wrote, so a write over that section replaces it rather than stacking below it.
	regenMarkers = managedMarkers{begin: "# BEGIN magus-regenerate", end: "# END magus-regenerate"}
)

// section wraps body, which ends in a newline, in m's unstamped banner and end lines.
func (m managedMarkers) section(body string) string {
	return m.banner(stamp.Writer{}) + body + m.end + "\n"
}

// banner is m's opening line, naming w as the writer when w is known. The stamp sits on
// the begin line because an older magus matches that line by prefix, so it still finds
// and replaces a stamped section rather than stacking a second one below it.
func (m managedMarkers) banner(w stamp.Writer) string {
	line := m.begin + " - do not edit this section manually"
	if w.Known() {
		line += "; written by " + w.String()
	}
	return line + "\n"
}

// WriteJudge is the stamp.Judge for a write into root's repository: this binary is the
// writer, ordered against a recorded one by git ancestry when root is in a git
// repository holding both commits, and by version and date otherwise.
func WriteJudge(ctx context.Context, root string) stamp.Judge {
	return stamp.Judge{
		Self: stamp.Self(ctx),
		Ancestry: func(ancestor, descendant string) (bool, error) {
			return gitVCS{}.IsAncestor(ctx, root, ancestor, descendant)
		},
	}
}

// sectionBanner picks the banner a write of body into text's m section carries.
//
// A section whose content already equals body keeps its banner, and so its writer: a
// rewrite that changes only the stamp would churn a tracked .gitattributes on every
// rebuild. Any other write is refused when a section's recorded writer is newer than
// j.Self, and otherwise stamps j.Self. An unstamped section predates stamps and is
// replaced.
func sectionBanner(path, text string, m managedMarkers, body string, j stamp.Judge) (string, error) {
	spans, err := managedSpans(text, m)
	if err != nil {
		return "", fmt.Errorf("vcs: %s: %w", path, err)
	}
	if len(spans) == 1 {
		head, rest, _ := strings.Cut(text[spans[0].start:spans[0].end], "\n")
		if rest == body+m.end+"\n" || rest == body+m.end {
			return head + "\n", nil
		}
	}
	if err := checkSectionWriters(path, text, spans, j); err != nil {
		return "", err
	}
	return m.banner(j.Self), nil
}

// checkSectionWriters refuses when any of spans was written by a magus newer than j.Self.
func checkSectionWriters(path, text string, spans []span, j stamp.Judge) error {
	for _, s := range spans {
		head, _, _ := strings.Cut(text[s.start:s.end], "\n")
		if rec, ok := stamp.Parse(head); ok {
			if err := j.Check(path, rec); err != nil {
				return err
			}
		}
	}
	return nil
}

// managedFileKind selects the file-specific steps writeManagedSection adds around the
// section surgery.
type managedFileKind int

const (
	// configFile is .gitattributes, .hg/hgrc or .sl/config.
	configFile managedFileKind = iota
	// hookFile is a shell hook: it must start with a POSIX-sh shebang and stay executable.
	hookFile
)

// replaceManagedSection replaces the m section of text with newSection, or appends it when
// the text holds none.
//
// It collapses every managed section, not just the first. The end marker used to be
// located by a plain Index over the whole text, which finds the FIRST section's end even
// when the matched begin sits further down; that computed endIdx < startIdx, failed the
// ordering test, and appended instead, once per invocation. A real .gitattributes
// reached four stacked sections this way, each still applying merge=magus for globs the
// workspace had dropped. Searching for end after begin stops the accumulation; sweeping
// the rest heals a file that already accumulated.
func replaceManagedSection(text, newSection string, m managedMarkers) (string, error) {
	spans, err := managedSpans(text, m)
	if err != nil {
		return "", err
	}

	// head is everything before the section's position, body everything after it with
	// any later duplicates removed. With no section present the whole text is head, so
	// the section lands at the end.
	head, body := text, ""
	if len(spans) > 0 {
		head = text[:spans[0].start]
		var rest strings.Builder
		prev := spans[0].end
		for _, s := range spans[1:] {
			rest.WriteString(text[prev:s.start])
			prev = s.end
		}
		rest.WriteString(text[prev:])
		body = rest.String()
	}

	// One blank line separates the section from its surroundings on both paths.
	// Normalizing both the same way makes a repeated write converge: they disagreed
	// before, so the first write appended with a blank line and later ones replaced
	// without it, rewriting the file forever.
	head = strings.TrimRight(head, "\n")
	if head != "" {
		head += "\n\n"
	}
	body = strings.TrimLeft(body, "\n")
	if body != "" {
		body = "\n" + body
	}
	return head + newSection + body, nil
}

// span is one managed section's half-open byte range, from the start of its begin line
// through the newline ending its end line.
type span struct{ start, end int }

// managedSpans locates every m section in order. Both markers match only at the start of
// a line, so prose that quotes a marker is not a section.
//
// A begin with no end before the next begin or the end of the file is an error. Pairing
// it with a later end would swallow everything between them, and skipping it would leave
// a half section in the file while a second one is appended beside it.
func managedSpans(text string, m managedMarkers) ([]span, error) {
	var spans []span
	for offset := 0; offset < len(text); {
		start := indexAtLine(text, m.begin, offset)
		if start < 0 {
			break
		}
		afterBegin := nextLineStart(text, start)
		endAt := indexAtLine(text, m.end, afterBegin)
		nextBegin := indexAtLine(text, m.begin, afterBegin)
		if endAt < 0 || (nextBegin >= 0 && nextBegin < endAt) {
			return nil, fmt.Errorf("line %d: %q has no %q before the next begin marker or the end of the file; delete the torn section by hand and rerun",
				strings.Count(text[:start], "\n")+1, m.begin, m.end)
		}
		stop := nextLineStart(text, endAt)
		spans = append(spans, span{start: start, end: stop})
		offset = stop
	}
	return spans, nil
}

// indexAtLine finds marker at the start of a line at or after offset.
func indexAtLine(text, marker string, offset int) int {
	for offset <= len(text) {
		rel := strings.Index(text[offset:], marker)
		if rel < 0 {
			return -1
		}
		start := offset + rel
		if start == 0 || text[start-1] == '\n' {
			return start
		}
		offset = start + 1
	}
	return -1
}

// nextLineStart returns the byte index of the start of the line after the one that
// contains pos, or len(text) when pos is on the last line.
func nextLineStart(text string, pos int) int {
	if nl := strings.Index(text[pos:], "\n"); nl >= 0 {
		return pos + nl + 1
	}
	return len(text)
}

// removeManagedSection deletes every m section from path, keeping the rest, and reports
// whether path changed. A missing file, or one holding no section, is left alone. The
// caller holds withRepoLock. A section a magus newer than j.Self wrote is refused.
func removeManagedSection(path string, m managedMarkers, j stamp.Judge) (bool, error) {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	data, err := os.ReadFile(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("vcs: read %s: %w", path, err)
	}
	text := string(data)
	spans, err := managedSpans(text, m)
	if err != nil {
		return false, fmt.Errorf("vcs: %s: %w", path, err)
	}
	if len(spans) == 0 {
		return false, nil
	}
	if err := checkSectionWriters(path, text, spans, j); err != nil {
		return false, err
	}
	var kept strings.Builder
	prev := 0
	for _, s := range spans {
		kept.WriteString(text[prev:s.start])
		prev = s.end
	}
	kept.WriteString(text[prev:])
	// A section sits between blank lines, and removing it leaves those behind.
	next := strings.TrimRight(kept.String(), "\n") + "\n"
	info, err := os.Stat(target)
	if err != nil {
		return false, fmt.Errorf("vcs: stat %s: %w", path, err)
	}
	if err := file.WriteFileAtomic(target, []byte(next), info.Mode().Perm()); err != nil {
		return false, fmt.Errorf("vcs: write %s: %w", path, err)
	}
	return true, nil
}

// managedSectionPresent reports whether path holds a complete m section, using the same
// parser the writer does. A missing file holds none; a torn section is an error.
func managedSectionPresent(path string, m managedMarkers) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("vcs: read %s: %w", path, err)
	}
	spans, err := managedSpans(string(data), m)
	if err != nil {
		return false, fmt.Errorf("vcs: %s: %w", path, err)
	}
	return len(spans) > 0, nil
}

// managedLockName is the repository's managed-section lock file, in its metadata dir.
const managedLockName = "magus-managed.lock"

// withRepoLock runs fn holding the managed-section lock of the repository whose metadata
// directory is metaDir (git's common dir, .hg, or .sl), so concurrent installs from any
// worktree or process cannot drop each other's section. One lock covers every managed
// file of the repository, and it lives in the metadata dir so it never shows as untracked.
// A managed write is a small file rewrite, so the wait is file.LockWait. The lock is not
// reentrant: fn must not call withRepoLock.
func withRepoLock(ctx context.Context, metaDir string, fn func() error) error {
	// One key per repository, however the caller spelled the path to it.
	dir, err := filepath.EvalSymlinks(metaDir)
	if err != nil {
		return fmt.Errorf("vcs: lock %s: %w", metaDir, err)
	}
	return file.WithLock(ctx, filepath.Join(dir, managedLockName), file.LockWait, fn)
}

// lockedWrite runs write under withRepoLock and returns what it reported.
func lockedWrite(ctx context.Context, metaDir string, write func() (bool, error)) (changed bool, err error) {
	err = withRepoLock(ctx, metaDir, func() error {
		var werr error
		changed, werr = write()
		return werr
	})
	return changed, err
}

// writeManagedSection brings the m section of path to body, creating path when absent,
// and reports whether path changed. It is the one writer for every managed section:
// .gitattributes, .hg/hgrc, .sl/config and shell hooks. The caller holds withRepoLock.
//
// A file that already holds the wanted bytes is not rewritten. Any other write replaces
// path atomically and keeps its mode; a hookFile always ends executable. A section a
// magus newer than j.Self wrote is refused with a *stamp.DowngradeError.
func writeManagedSection(path string, m managedMarkers, body string, kind managedFileKind, j stamp.Judge) (bool, error) {
	// A hook managed by another tool is often a symlink, and renaming over the link would
	// replace it with a copy.
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}

	mode := fs.FileMode(0o644)
	if kind == hookFile {
		mode = 0o755
	}
	info, err := os.Stat(target)
	exists := err == nil
	switch {
	case exists:
		mode = info.Mode().Perm()
		if kind == hookFile && mode&0o111 == 0 {
			mode = 0o755
		}
	case !errors.Is(err, fs.ErrNotExist):
		return false, fmt.Errorf("vcs: stat %s: %w", path, err)
	}

	existing, err := os.ReadFile(target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("vcs: read %s: %w", path, err)
	}
	next, err := renderManagedFile(path, string(existing), m, body, kind, j)
	if err != nil {
		return false, err
	}
	if exists && next == string(existing) {
		if mode == info.Mode().Perm() {
			return false, nil
		}
		if err := file.Chmod(target, mode); err != nil {
			return false, fmt.Errorf("vcs: chmod %s: %w", path, err)
		}
		return true, nil
	}
	if err := file.WriteFileAtomic(target, []byte(next), mode); err != nil {
		return false, fmt.Errorf("vcs: write %s: %w", path, err)
	}
	return true, nil
}

// renderManagedFile returns current with its m section set to body. path only names the
// file in errors.
//
// The result keeps the line ending the file already uses. .gitattributes is the one
// tracked file magus rewrites on every workspace load, and under core.autocrlf=true (the
// Git for Windows default) checkout smudges it to CRLF on disk; writing it back as LF
// marks the file modified and turns `git describe --dirty` into `<tag>-dirty`. An LF file
// comes back byte for byte.
//
// j decides whether the section may be replaced and who the new one names as its writer
// (see sectionBanner).
func renderManagedFile(path, current string, m managedMarkers, body string, kind managedFileKind, j stamp.Judge) (string, error) {
	crlf := strings.Contains(current, "\r\n")
	text := current
	if crlf {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	if kind == hookFile {
		var err error
		if text, err = ensureShShebang(path, text); err != nil {
			return "", err
		}
	}
	banner, err := sectionBanner(path, text, m, body, j)
	if err != nil {
		return "", err
	}
	next, err := replaceManagedSection(text, banner+body+m.end+"\n", m)
	if err != nil {
		return "", fmt.Errorf("vcs: %s: %w", path, err)
	}
	if crlf {
		next = strings.ReplaceAll(next, "\n", "\r\n")
	}
	return next, nil
}

// shInterpreters run the POSIX-sh body magus appends to a hook.
var shInterpreters = []string{"sh", "bash", "dash", "zsh"}

// ensureShShebang gives an LF hook text a POSIX-sh shebang when it has none. A shebang
// counts only at byte 0, where the kernel reads it. A hook whose shebang names any other
// interpreter is an error: appending sh to a python or node script breaks the script.
func ensureShShebang(path, text string) (string, error) {
	if !strings.HasPrefix(text, "#!") {
		// The blank line matches what replaceManagedSection leaves before a section, so a
		// new hook is already in the shape the next install produces.
		return "#!/bin/sh\n\n" + strings.TrimLeft(text, "\n"), nil
	}
	line, _, _ := strings.Cut(text, "\n")
	if !slices.Contains(shInterpreters, shebangInterpreter(line)) {
		return "", fmt.Errorf("vcs: hook %s runs %q, not a POSIX shell, and magus appends sh to its hooks; call that script from a sh hook instead", path, line)
	}
	return text, nil
}

// shebangInterpreter is the program a shebang line runs, looking through env and its
// flags and assignments.
func shebangInterpreter(line string) string {
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) == 0 {
		return ""
	}
	prog := filepath.Base(fields[0])
	if prog != "env" {
		return prog
	}
	for _, f := range fields[1:] {
		if !strings.HasPrefix(f, "-") && !strings.Contains(f, "=") {
			return filepath.Base(f)
		}
	}
	return ""
}
