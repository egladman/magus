package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/types"
)

// codeDocsMarker is the path segment every MGS docs URL routes through, and the hinge
// between the rendered site and the tree: <site>/reference/codes/<area>/<CODE>/ is
// docs/reference/codes/<area>/<CODE>.md in the sources.
const codeDocsMarker = "/reference/codes/"

// remediationSections are the section titles that count as a next step, normalized by
// normalizeSectionTitle. The pages already spell it five ways ("Resolution", "Fix",
// "The fix", "Fixing it", "Resolve it"), so the normalizer collapses those rather than
// this set enumerating them.
//
// Matching is on the section TITLE, never on the body prose or on a diagnostic's message:
// a gate that greps a sentence for encouraging words grades wording, and every author then
// writes to the grep. A section is a structure the page either declares or does not.
var remediationSections = map[string]bool{
	"fix":         true,
	"fixing":      true,
	"remediation": true,
	"resolution":  true,
	"resolve":     true,
	"resolving":   true,
	"what to do":  true,
}

// checkDiagnosticDocs reports diagnostic codes a reader cannot act on: one with no docs
// page behind the URL magus prints, and one whose page explains the failure without ever
// naming a next step.
//
// magus's claim about its own errors is that one leaving you with no next step is a
// defect. That only means anything if something can measure it, and nothing did: an MGS
// code renders a "see:" URL unconditionally, so a code whose page was never written
// prints a link into a 404 and reads exactly like one that works.
//
// The three questions are asked in the order a stuck reader hits them - does the code
// route to a page, does the page exist, does the page tell me what to do - and graded by
// what a fix costs. The first two are mechanical and fail; the third is prose somebody has
// to write, so it is advice, listing the pages by name rather than blocking on them.
//
// Scoped to the tree that OWNS these pages. The MGS pages ship with magus's own sources,
// so a workspace without that tree is not a workspace with 68 gaps - it is somebody else's
// repo, and the check reports itself skipped there. The scope is the TREE's existence,
// never a count of pages resolved: no page resolving is equally what a code-to-page mapping
// broken end to end produces, and skipping on that reports the total failure as a pass.
//
// codeURL is a parameter rather than a direct types.CodeURL call so a test can hand it a
// domain that routes a code nowhere; that branch is unreachable through the real one,
// which is the point of guarding it.
func checkDiagnosticDocs(root string, codes []types.DiagnosticCode, codeURL func(types.DiagnosticCode) string) types.DoctorCheck {
	const name = "diagnostic-docs"

	if _, err := os.Stat(codeDocsRoot(root)); err != nil {
		return types.DoctorCheck{
			Name:     name,
			Status:   types.DoctorOK,
			Evidence: types.EvidenceUnknown,
			Message:  "no docs" + codeDocsMarker + " tree; skipped (the MGS pages ship with magus's own sources)",
		}
	}

	var unroutable, missing, silent []string
	pages := 0
	for _, code := range codes {
		url := codeURL(code)
		page, ok := codeDocPath(root, code, url)
		if !ok {
			unroutable = append(unroutable, fmt.Sprintf(
				"%s: no see: target - its URL %q does not end at %s%s/; route the code's prefix to its area in types/diagnostic.go",
				code, url, codeDocsMarker[1:], code))
			continue
		}
		md, err := os.ReadFile(page)
		if err != nil {
			missing = append(missing, fmt.Sprintf(
				"%s: no docs page; the printed see: target 404s until %s exists, with a section naming what the reader does next",
				code, relToRoot(root, page)))
			continue
		}
		pages++
		if !hasRemediation(string(md)) {
			silent = append(silent, fmt.Sprintf(
				"%s: %s explains the failure but declares no remediation section; add a `## Resolution` naming the next step",
				code, relToRoot(root, page)))
		}
	}

	if broken := append(unroutable, missing...); len(broken) > 0 {
		msg := fmt.Sprintf(
			"%d of %d diagnostic code(s) print a see: target that leads nowhere, so the reader is left with the code and no way to look it up",
			len(broken), len(codes))
		if len(silent) > 0 {
			msg += fmt.Sprintf("; a further %d have a page that never names a next step", len(silent))
		}
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorFail,
			Message: msg,
			Details: append(broken, silent...),
		}
	}
	if len(silent) > 0 {
		return types.DoctorCheck{
			Name:   name,
			Status: types.DoctorAdvice,
			Message: fmt.Sprintf(
				"%d of %d diagnostic code(s) have a page that says what fires them and not what to do about it",
				len(silent), len(codes)),
			Details: silent,
		}
	}
	return types.DoctorCheck{
		Name:    name,
		Status:  types.DoctorOK,
		Message: fmt.Sprintf("%d diagnostic code(s) resolve to a docs page carrying a remediation section", pages),
	}
}

// checkDiagnosticDocs reports diagnostic codes whose docs page is missing or names no
// next step.
func (r *runner) checkDiagnosticDocs() types.DoctorCheck {
	return checkDiagnosticDocs(r.root, types.AllDiagnosticCodes(), types.CodeURL)
}

// codeDocsRoot is the directory every code page lives under, and the one whose absence
// puts the check out of scope.
func codeDocsRoot(root string) string {
	return filepath.Join(root, "docs", filepath.FromSlash(strings.Trim(codeDocsMarker, "/")))
}

// codeDocPath maps a code's rendered docs URL back to its Markdown source under root,
// reporting ok=false when the URL does not route through the codes tree to this code's
// own page.
func codeDocPath(root string, code types.DiagnosticCode, url string) (string, bool) {
	i := strings.Index(url, codeDocsMarker)
	if i < 0 {
		return "", false
	}
	rel := strings.TrimSuffix(url[i+1:], "/")
	if !strings.HasSuffix(rel, "/"+string(code)) {
		return "", false
	}
	return filepath.Join(root, "docs", filepath.FromSlash(rel)+".md"), true
}

// relToRoot renders path for a human reading the report, falling back to the absolute
// path when it lies outside root.
func relToRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// hasRemediation reports whether md declares a remediation section with something under
// it. An empty section is the same gap as an absent one, and is the shape a stub takes.
// The section runs until the next heading at its own level or above: a body structured
// entirely as deeper subsections (MGS4001's numbered fixes) is content, not absence - but
// the deeper heading is not the content, so a `### stub` with nothing under it is still an
// empty section.
func hasRemediation(md string) bool {
	lines := strings.Split(md, "\n")
	var page fence
	for i, line := range lines {
		if page.mark(line) {
			continue
		}
		title, level, ok := sectionTitle(line)
		if page.open() || !ok || !remediationSections[normalizeSectionTitle(title)] {
			continue
		}
		var body fence
		for _, l := range lines[i+1:] {
			if body.mark(l) {
				continue
			}
			if !body.open() {
				if _, deeper, isHeading := sectionTitle(l); isHeading {
					if deeper <= level {
						break
					}
					continue
				}
			}
			if strings.TrimSpace(l) != "" {
				return true
			}
		}
	}
	return false
}

// fence tracks an open fenced block by the backtick run that opened it, because a fence
// can wrap a shorter one: the four-backtick blocks in the docs exist to quote a
// three-backtick example, and closing on the inner run desynchronizes every heading test
// for the rest of the page.
type fence struct{ ticks int }

// open reports whether a fenced block is currently open, so the caller can tell a heading
// from a line of sample output that looks like one.
func (f *fence) open() bool { return f.ticks > 0 }

// mark consumes line when it opens or closes a block, reporting whether it did. A run
// shorter than the opener, or one trailing an info string, closes nothing and is left to
// the caller as ordinary content.
func (f *fence) mark(line string) bool {
	s := strings.TrimSpace(line)
	ticks := 0
	for ticks < len(s) && s[ticks] == '`' {
		ticks++
	}
	switch {
	case ticks < 3:
		return false
	case f.ticks == 0:
		f.ticks = ticks
		return true
	case ticks >= f.ticks && ticks == len(s):
		f.ticks = 0
		return true
	}
	return false
}

// sectionTitle returns the title and level of an ATX section heading, or ok=false for
// any other line. A page's H1 is its name rather than a section, so a single hash is
// not one. Two spellings beyond the plain form are CommonMark headings and are read as
// such: one indented by up to three spaces, and a closed one (`## Resolution ##`), whose
// trailing run is decoration rather than part of the title. Setext headings (a title
// underlined with = or -) are not supported.
func sectionTitle(line string) (string, int, bool) {
	t := strings.TrimRight(line, "\r")
	indent := len(t) - len(strings.TrimLeft(t, " "))
	// A fourth space makes it an indented code block, not a heading.
	if indent > 3 {
		return "", 0, false
	}
	t = t[indent:]
	hashes := 0
	for hashes < len(t) && t[hashes] == '#' {
		hashes++
	}
	if hashes < 2 || hashes >= len(t) || t[hashes] != ' ' {
		return "", 0, false
	}
	title := strings.TrimSpace(t[hashes+1:])
	// A closing run only closes when a space precedes it: "## C#" titles a section C#.
	if closed := strings.TrimRight(title, "#"); closed != title && (closed == "" || strings.HasSuffix(closed, " ")) {
		title = strings.TrimSpace(closed)
	}
	return title, hashes, true
}

// normalizeSectionTitle reduces a section title to the key remediationSections is keyed
// on, so the spellings already in the tree land on one entry.
//
// The result is matched EXACTLY, never as a substring: "Confirming the fix" is a
// verification step and normalizes to itself, so a page carrying only that one still
// reads as having no remediation.
func normalizeSectionTitle(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	t = strings.Trim(t, ".:!?")
	t = strings.TrimPrefix(t, "the ")
	t = strings.TrimSuffix(t, " it")
	return strings.TrimSpace(t)
}
