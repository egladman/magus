package vcs

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// ChangedRegions implements types.RegionReporter. The line ranges come from one `git diff
// -U0` against base; the declaration enclosing each line comes from git's own funcname
// matching, run through gitPlaceLines on the version of the file that holds the line.
//
// The hunk header is not the answer: git takes it from the lines BEFORE the hunk, so a hunk
// adding a whole new function reads as the function above it.
//
// Untracked files count as new, as ChangedFiles counts them. Binary files and submodules
// yield nothing: neither has lines to place.
func (v gitVCS) ChangedRegions(ctx context.Context, root, base string, paths []string) ([]types.ChangedRegion, error) {
	if err := checkRequiredRev(base); err != nil {
		return nil, err
	}
	// From the merge base, as ChangedFiles measures, so a job's paths and its regions describe
	// one diff; against base itself a branch behind base would report base's own changes,
	// reversed. Unlike ChangedFiles it never fetches to recover a missing merge base.
	mergeBase, err := gitOutput(ctx, root, gitOpts{}, "merge-base", base, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git merge-base %s HEAD: %w", base, err)
	}
	base = mergeBase
	// Each flag pins something the box's config would otherwise change about the parse:
	// diff.algorithm and diff.indentHeuristic move hunk boundaries, diff.interHunkContext
	// merges neighbouring hunks, diff.relative and diff.srcPrefix rewrite the paths, and
	// diff.renames, diff.ignoreSubmodules, diff.external and textconv change which files
	// appear and what their lines are.
	args := []string{"diff", "-U0", "--inter-hunk-context=0", "--diff-algorithm=histogram",
		"--indent-heuristic", "--no-renames", "--ignore-submodules=all", "--no-relative",
		"--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", base, "--"}
	out, err := gitOutput(ctx, root, gitOpts{Literal: true, KeepLeadingSpace: true}, append(args, paths...)...)
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", base, err)
	}
	files, err := parseZeroContextPatch(out)
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", base, err)
	}
	untracked, err := gitOutput(ctx, root, gitOpts{Literal: true, KeepLeadingSpace: true},
		append([]string{"ls-files", "--others", "--exclude-standard", "-z", "--"}, paths...)...)
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	working := map[string][]byte{}
	for _, p := range splitNUL(untracked) {
		body, ok := readWorkingFile(root, p)
		if !ok || isBinaryContent(body) {
			continue
		}
		if n := countLines(body); n > 0 {
			working[p] = body
			files = append(files, patchFile{path: p, hunks: []patchHunk{{newStart: 1, newCount: n}}})
		}
	}
	if len(files) == 0 {
		return nil, nil
	}

	drivers, err := gitPathDrivers(ctx, root, files)
	if err != nil {
		return nil, err
	}
	var funcnames []string
	if len(drivers) > 0 {
		if funcnames, err = gitFuncnameConfig(ctx, root); err != nil {
			return nil, err
		}
	}
	tmp, err := os.MkdirTemp("", "magus-regions-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	var regions []types.ChangedRegion
	for _, f := range files {
		driver := drivers[f.path]
		place := func(side types.RegionSide) ([]string, error) {
			if driver == "" {
				return nil, nil
			}
			if side == types.RegionOld {
				body, err := v.ReadFileAt(ctx, root, base, f.path)
				if err != nil {
					return nil, err
				}
				return gitPlaceLines(ctx, tmp, driver, funcnames, []byte(body))
			}
			body, ok := working[f.path]
			if !ok {
				if body, ok = readWorkingFile(root, f.path); !ok {
					return nil, nil
				}
			}
			return gitPlaceLines(ctx, tmp, driver, funcnames, body)
		}
		var oldDecls, newDecls []string
		if f.hasSide(types.RegionOld) {
			if oldDecls, err = place(types.RegionOld); err != nil {
				return nil, err
			}
		}
		if f.hasSide(types.RegionNew) {
			if newDecls, err = place(types.RegionNew); err != nil {
				return nil, err
			}
		}
		for _, h := range f.hunks {
			regions = appendRegions(regions, f.path, types.RegionOld, h.oldStart, h.oldCount, driver, oldDecls)
			regions = appendRegions(regions, f.path, types.RegionNew, h.newStart, h.newCount, driver, newDecls)
		}
	}
	slices.SortStableFunc(regions, func(a, b types.ChangedRegion) int {
		return cmp.Or(strings.Compare(a.Path, b.Path),
			cmp.Compare(sideOrder(a.Side), sideOrder(b.Side)),
			cmp.Compare(a.Lines[0], b.Lines[0]))
	})
	return regions, nil
}

// appendRegions splits count lines from start on side into one region per run of lines
// sharing a declaration. decls is indexed by line-1 and nil when the lines were not placed.
func appendRegions(regions []types.ChangedRegion, path string, side types.RegionSide, start, count int, driver string, decls []string) []types.ChangedRegion {
	if decls == nil {
		driver = ""
	}
	declAt := func(line int) string {
		if line-1 < len(decls) {
			return decls[line-1]
		}
		return ""
	}
	for line := start; line < start+count; {
		decl := declAt(line)
		end := line
		for end+1 < start+count && declAt(end+1) == decl {
			end++
		}
		regions = append(regions, types.ChangedRegion{
			Path: path, Side: side, Lines: [2]int{line, end}, Declaration: decl, Driver: driver,
		})
		line = end + 1
	}
	return regions
}

func sideOrder(s types.RegionSide) int {
	if s == types.RegionOld {
		return 0
	}
	return 1
}

// patchFile is one file's hunks from a -U0 patch.
type patchFile struct {
	path  string
	hunks []patchHunk
}

// patchHunk is one -U0 hunk. A zero count means the side has no lines in it, and its
// start is then the line the other side's lines follow, which names no line here.
type patchHunk struct {
	oldStart, oldCount int
	newStart, newCount int
}

func (f patchFile) hasSide(side types.RegionSide) bool {
	return slices.ContainsFunc(f.hunks, func(h patchHunk) bool {
		if side == types.RegionOld {
			return h.oldCount > 0
		}
		return h.newCount > 0
	})
}

// parseZeroContextPatch reads the files and hunks of a `git diff -U0` patch. A file with no
// hunks (binary, mode-only, empty) is left out. Hunk bodies are skipped by their counts, so
// a removed line that reads "-- x" is never taken for a "--- " file header.
func parseZeroContextPatch(patch string) ([]patchFile, error) {
	var files []patchFile
	var oldPath, newPath string
	lines := strings.Split(patch, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "diff --git "):
			oldPath, newPath = "", ""
		case strings.HasPrefix(line, "--- "):
			oldPath = patchPath(line[4:], "a/")
		case strings.HasPrefix(line, "+++ "):
			newPath = patchPath(line[4:], "b/")
		case strings.HasPrefix(line, "@@ "):
			h, err := parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
			path := cmp.Or(newPath, oldPath)
			if path == "" {
				return nil, fmt.Errorf("hunk %q before a file header", line)
			}
			if len(files) == 0 || files[len(files)-1].path != path {
				files = append(files, patchFile{path: path})
			}
			files[len(files)-1].hunks = append(files[len(files)-1].hunks, h)
			for body := h.oldCount + h.newCount; body > 0 && i+1 < len(lines); i++ {
				if !strings.HasPrefix(lines[i+1], `\`) {
					body--
				}
			}
		}
	}
	return files, nil
}

// patchPath reads a "--- " or "+++ " name: "" for /dev/null, else the path without its
// prefix. git appends a tab to a name containing a space and C-quotes one containing a
// quote, a backslash or a control character, even with core.quotePath=false.
func patchPath(name, prefix string) string {
	name = strings.TrimSuffix(name, "\t")
	if name == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(name, `"`) {
		if unquoted, err := strconv.Unquote(name); err == nil {
			name = unquoted
		}
	}
	return strings.TrimPrefix(name, prefix)
}

// parseHunkHeader reads "@@ -a[,b] +c[,d] @@[ funcname]". An omitted count is 1.
func parseHunkHeader(line string) (patchHunk, error) {
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[3] != "@@" || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return patchHunk{}, fmt.Errorf("malformed hunk header %q", line)
	}
	oldStart, oldCount, err1 := parseHunkRange(fields[1][1:])
	newStart, newCount, err2 := parseHunkRange(fields[2][1:])
	if err1 != nil || err2 != nil {
		return patchHunk{}, fmt.Errorf("malformed hunk header %q", line)
	}
	return patchHunk{oldStart: oldStart, oldCount: oldCount, newStart: newStart, newCount: newCount}, nil
}

func parseHunkRange(s string) (start, count int, err error) {
	startText, countText, found := strings.Cut(s, ",")
	if start, err = strconv.Atoi(startText); err != nil {
		return 0, 0, err
	}
	if !found {
		return start, 1, nil
	}
	count, err = strconv.Atoi(countText)
	return start, count, err
}

// gitPathDrivers maps each file with a named diff driver to that driver, from the same
// attributes `git diff` reads. A path whose attribute is unspecified, set without a name,
// or unset has none.
func gitPathDrivers(ctx context.Context, root string, files []patchFile) (map[string]string, error) {
	var stdin bytes.Buffer
	for _, f := range files {
		stdin.WriteString(f.path)
		stdin.WriteByte(0)
	}
	out, err := gitOutput(ctx, root, gitOpts{Stdin: stdin.Bytes(), KeepLeadingSpace: true},
		"check-attr", "-z", "--stdin", "diff")
	if err != nil {
		return nil, fmt.Errorf("git check-attr diff: %w", err)
	}
	drivers := map[string]string{}
	// Records are <path> NUL <attribute> NUL <value> NUL.
	f := strings.Split(out, "\x00")
	for i := 0; i+2 < len(f); i += 3 {
		switch f[i+2] {
		case "unspecified", "set", "unset":
		default:
			drivers[f[i]] = f[i+2]
		}
	}
	return drivers, nil
}

// gitFuncnameConfig returns every diff.<driver>.funcname and xfuncname the repository
// sees as -c arguments, since gitPlaceLines runs outside the repository and would
// otherwise lose a driver defined in its config.
func gitFuncnameConfig(ctx context.Context, root string) ([]string, error) {
	out, err := gitOutput(ctx, root, gitOpts{KeepLeadingSpace: true},
		"config", "-z", "--get-regexp", `^diff\..+\.x?funcname$`)
	if exitCode(err) == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("git config diff funcnames: %w", err)
	}
	var args []string
	// Records are <key> LF <value> NUL.
	for _, rec := range splitNUL(out) {
		key, value, _ := strings.Cut(rec, "\n")
		args = append(args, "-c", key+"="+value)
	}
	return args, nil
}

// regionMarker is the line gitPlaceLines interleaves. \x01 keeps it off any real line
// without the NUL that would make git call the copy binary.
const regionMarker = "\x01magus-region\x01"

// gitPlaceLines returns the declaration enclosing each line of body, indexed by line-1, as
// driver's funcname pattern matches it.
//
// It diffs body against a copy with regionMarker inserted after every line. Each insertion
// is its own hunk, and git names a hunk by searching up from the last line before it, so
// the header of the insertion after line N is the declaration enclosing N, N included.
// The minimal algorithm keeps every insertion where it was made: a heuristic one is free to
// pair lines differently once the file is large.
//
// It runs outside the repository with only driver's attribute: the repository's own
// .gitattributes outranks core.attributesFile, and a `*` rule there would replace the driver.
func gitPlaceLines(ctx context.Context, tmp, driver string, funcnames []string, body []byte) ([]string, error) {
	lines := strings.SplitAfter(string(body), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{}, nil
	}
	var orig, marked strings.Builder
	for _, l := range lines {
		if !strings.HasSuffix(l, "\n") {
			l += "\n"
		}
		orig.WriteString(l)
		marked.WriteString(l)
		marked.WriteString(regionMarker + "\n")
	}
	attrs, a, b := filepath.Join(tmp, "attributes"), filepath.Join(tmp, "a"), filepath.Join(tmp, "b")
	for name, content := range map[string]string{attrs: "* diff=" + driver + "\n", a: orig.String(), b: marked.String()} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			return nil, err
		}
	}
	args := slices.Concat(funcnames, []string{"-c", "core.attributesFile=" + attrs,
		"diff", "--no-index", "-U0", "--diff-algorithm=minimal", "--inter-hunk-context=0",
		"--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", "--", "a", "b"})
	env := []string{"GIT_CEILING_DIRECTORIES=" + filepath.Dir(tmp), "GIT_ATTR_NOSYSTEM=1"}
	out, err := gitExec(ctx, tmp, gitOpts{Env: env}, args...).Output()
	// --no-index exits 1 when the files differ, which they always do here.
	if err != nil && exitCode(err) != 1 {
		return nil, fmt.Errorf("git diff --no-index: %w", gitStderr(err))
	}
	decls := make([]string, len(lines))
	for l := range strings.SplitSeq(string(out), "\n") {
		if !strings.HasPrefix(l, "@@ ") {
			continue
		}
		h, err := parseHunkHeader(l)
		if err != nil || h.oldCount != 0 || h.oldStart < 1 || h.oldStart > len(decls) {
			continue
		}
		if _, after, ok := strings.Cut(l[2:], " @@"); ok {
			decls[h.oldStart-1] = strings.TrimSpace(after)
		}
	}
	return decls, nil
}

// readWorkingFile reads a regular file at repository-relative path p under root. Anything
// else (a symlink, a directory, a file gone since the diff) has no lines to place.
func readWorkingFile(root, p string) ([]byte, bool) {
	name := filepath.Join(root, filepath.FromSlash(p))
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	body, err := os.ReadFile(name)
	return body, err == nil
}

// isBinaryContent is git's own test: a NUL in the first 8000 bytes.
func isBinaryContent(body []byte) bool {
	return bytes.IndexByte(body[:min(len(body), 8000)], 0) >= 0
}

// countLines counts lines as git does: a last line without a newline still counts.
func countLines(body []byte) int {
	n := bytes.Count(body, []byte("\n"))
	if len(body) > 0 && body[len(body)-1] != '\n' {
		n++
	}
	return n
}
