package guard

import (
	"context"
	"path/filepath"
	"strings"
)

// patchFile is one file section of an apply_patch body.
type patchFile struct {
	Path string
	// Added is the body of an `*** Add File:` section with the leading "+" removed.
	// Empty for every other section.
	Added []string
	// IsAdd is whether the section creates the file.
	IsAdd bool
}

const (
	patchBegin  = "*** Begin Patch"
	patchAdd    = "*** Add File:"
	patchUpdate = "*** Update File:"
	patchDelete = "*** Delete File:"
	patchMove   = "*** Move to:"
)

// patchTexts are the tool_input fields a host puts a patch in: one host sends it as
// `command`, another as `patchText`.
var patchTexts = []string{"patchText", "command"}

// envelopePatch reads the files a patch tool_input names, or nil when tool_input holds no
// patch. A field is a patch only when it opens with the patch marker, so a shell line in
// `command` is never mistaken for one.
//
// A section that moves a file yields two entries, the source and the destination, since
// both are written.
func envelopePatch(input map[string]any) []patchFile {
	for _, key := range patchTexts {
		if text := envelopeString(input, key); isPatchText(text) {
			return parsePatch(text)
		}
	}
	return nil
}

// isPatchText reports whether text is an apply_patch body, which a shell rule must not
// read: the added lines are file content, and a denied command quoted in one is not run.
func isPatchText(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), patchBegin)
}

// envelopeHoldsPatch is whether tool_input carries a patch body, even one that names no
// file.
func envelopeHoldsPatch(input map[string]any) bool {
	for _, key := range patchTexts {
		if isPatchText(envelopeString(input, key)) {
			return true
		}
	}
	return false
}

// parsePatch lists the file sections of a patch in order, each path once. A path the
// patch names twice keeps its first position and, for an add, its first body.
func parsePatch(text string) []patchFile {
	var files []patchFile
	seen := map[string]bool{}
	// current is the add section whose "+" lines are being collected, -1 outside one.
	current := -1
	add := func(path string, isAdd bool) {
		path = strings.TrimSpace(path)
		current = -1
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		files = append(files, patchFile{Path: path, IsAdd: isAdd})
		if isAdd {
			current = len(files) - 1
		}
	}
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, patchAdd):
			add(strings.TrimPrefix(line, patchAdd), true)
		case strings.HasPrefix(line, patchUpdate):
			add(strings.TrimPrefix(line, patchUpdate), false)
		case strings.HasPrefix(line, patchDelete):
			add(strings.TrimPrefix(line, patchDelete), false)
		case strings.HasPrefix(line, patchMove):
			add(strings.TrimPrefix(line, patchMove), false)
		case strings.HasPrefix(line, "*** "):
			current = -1
		case current >= 0 && strings.HasPrefix(line, "+"):
			files[current].Added = append(files[current].Added, line[1:])
		}
	}
	return files
}

// patchWritePaths is the path of every file section, in patch order.
func patchWritePaths(files []patchFile) []string {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	return paths
}

// patchContent is the text a patch writes when it creates exactly one file and does
// nothing else, so a content rule can read it as it reads a whole-file write. Any other
// patch yields "": its added lines are fragments of files magus has not read.
func patchContent(files []patchFile) string {
	if len(files) != 1 {
		return ""
	}
	return files[0].addedContent()
}

// addedContent is the text an add section creates its file with, "" for any other section.
func (f patchFile) addedContent() string {
	if !f.IsAdd || len(f.Added) == 0 {
		return ""
	}
	return strings.Join(f.Added, "\n") + "\n"
}

// judgePatchRest judges every file of a patch after its first, which the call itself is
// judged by, as the write a host's write tool would make of each: the first refusal wins,
// named by its path, then the first advice. A pass when every one passes.
func judgePatchRest(ctx context.Context, deps Dependencies, req Request, who hookAttribution, callDir, lease string, files []patchFile) Verdict {
	advised := Verdict{Decision: decisionPass}
	for _, f := range files[min(1, len(files)):] {
		target := f.Path
		if !filepath.IsAbs(target) && callDir != "" {
			target = filepath.Join(callDir, target)
		}
		switch v := judgeFileWrite(ctx, deps, req, who, callDir, lease, target, f.addedContent()); v.Decision {
		case decisionDeny:
			v.Reason = f.Path + ": " + v.Reason
			return v
		case decisionAdvise:
			if advised.Decision == decisionPass {
				v.Context = f.Path + ": " + v.Context
				advised = v
			}
		}
	}
	return advised
}
