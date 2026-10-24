package guard

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/types"
)

// patchOp is what a file section of an apply_patch body does to its path.
type patchOp int

const (
	patchOpAdd patchOp = iota
	patchOpUpdate
	patchOpDelete
	// patchOpMove is the destination of an update that moves its file.
	patchOpMove
)

// patchFile is one file section of an apply_patch body.
type patchFile struct {
	Path string
	Op   patchOp
	// Added is the body of an add section with the leading "+" removed. Empty for every
	// other section.
	Added []string
}

const patchBegin = "*** Begin Patch"

// patchSections are the header lines that open a file section, by the op each names.
var patchSections = []struct {
	header string
	op     patchOp
}{
	{header: "*** Add File:", op: patchOpAdd},
	{header: "*** Update File:", op: patchOpUpdate},
	{header: "*** Delete File:", op: patchOpDelete},
	{header: "*** Move to:", op: patchOpMove},
}

// patchTexts are the tool_input fields a host puts a patch in: one host sends it as
// `command`, another as `patchText`. `command` is a shell tool's field too, so it is read
// only for a tool its row names a writer.
var patchTexts = []string{"patchText", "command"}

// envelopePatch reads the files a patch tool_input names. ok reports that tool_input holds
// a patch at all, which may name no file. A field is a patch only when it opens with the
// patch marker, and `command` only when writer says the tool's row names it a writer: a
// shell line can open with the marker too, and bash runs the lines after it.
//
// A section that moves a file yields two entries, the source and the destination, since
// both are written.
func envelopePatch(input map[string]any, writer bool) (files []patchFile, ok bool) {
	for _, key := range patchTexts {
		if key == "command" && !writer {
			continue
		}
		if text := envelopeString(input, key); isPatchText(text) {
			return parsePatch(text), true
		}
	}
	return nil, false
}

// isPatchText reports whether text is an apply_patch body, which a shell rule must not
// read: the added lines are file content, and a denied command quoted in one is not run.
func isPatchText(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), patchBegin)
}

// parsePatch lists the file sections of a patch in order, each path once. A path the
// patch names twice keeps its first position and, for an add, its first body.
func parsePatch(text string) []patchFile {
	var files []patchFile
	seen := map[string]bool{}
	// current is the add section whose "+" lines are being collected, -1 outside one.
	current := -1
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "*** ") {
			if current >= 0 && strings.HasPrefix(line, "+") {
				files[current].Added = append(files[current].Added, line[1:])
			}
			continue
		}
		current = -1
		for _, s := range patchSections {
			path, ok := strings.CutPrefix(line, s.header)
			if path = strings.TrimSpace(path); !ok || path == "" || seen[path] {
				continue
			}
			seen[path] = true
			files = append(files, patchFile{Path: path, Op: s.op})
			if s.op == patchOpAdd {
				current = len(files) - 1
			}
		}
	}
	return files
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
	if f.Op != patchOpAdd || len(f.Added) == 0 {
		return ""
	}
	return strings.Join(f.Added, "\n") + "\n"
}

// judgePatchRest judges every file of a patch after its first, which the call itself is
// judged by, as the write a host's write tool would make of each: the first refusal wins,
// named by its path, then the first advice. A pass when every one passes.
func judgePatchRest(ctx context.Context, deps Dependencies, req Request, who hookAttribution, callDir, lease string, files []patchFile) types.GuardJudgment {
	advised := types.GuardJudgment{Decision: decisionPass}
	for _, f := range files[min(1, len(files)):] {
		target := f.Path
		if !filepath.IsAbs(target) && callDir != "" {
			target = filepath.Join(callDir, target)
		}
		switch v := judgeFileWrite(ctx, deps, req, who, callDir, lease, target, f.addedContent()); v.Decision {
		case decisionDeny:
			v.Reason = fmt.Sprintf("%s: %s", f.Path, v.Reason)
			return v
		case decisionAdvise:
			if advised.Decision == decisionPass {
				v.Context = fmt.Sprintf("%s: %s", f.Path, v.Context)
				advised = v
			}
		}
	}
	return advised
}
