package notes

import (
	"fmt"
	"strings"
	"time"

	"github.com/egladman/magus/libs/text/plural"
)

// FirstNonEmpty returns a when it holds anything but whitespace, else b.
func FirstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func AnchorSummary(n Note) string {
	parts := make([]string, 0, len(n.Anchors))
	for _, a := range n.Anchors {
		parts = append(parts, fmt.Sprintf("%s:%s", string(a.Kind), a.Target))
	}
	return strings.Join(parts, ", ")
}

func PrintNote(n Note, scope Scope) {
	fmt.Printf("%s  [%s]\n%s\n", n.Name, scope, n.Title)
	if len(n.Tags) != 0 {
		fmt.Printf("tags: %s\n", strings.Join(n.Tags, ", "))
	}
	fmt.Println("anchors:")
	for _, a := range n.Anchors {
		line := fmt.Sprintf("  %s: %s", a.Kind, a.Target)
		if a.Commit != "" {
			line += fmt.Sprintf("  (reviewed at %s)", a.Commit)
		}
		fmt.Println(line)
	}
	if n.Body != "" {
		fmt.Printf("\n%s\n", n.Body)
	}
	fmt.Printf("\nmodified: %s\n", n.Modified.UTC().Format(time.RFC3339))
}

func PrintIssues(issues []Issue, strict bool) error {
	for _, issue := range issues {
		glyph := "[warn]"
		if issue.Severity == "error" || (strict && issue.Code == CodeDanglingAnchor) {
			glyph = "[fail]"
		}
		fmt.Printf("%s %s: %s\n  %s\n", glyph, issue.Path, issue.Message, issue.Hint)
	}
	return IssuesError(issues, strict)
}

// IssueSubjects names what each issue is about, for `-o name`.
func IssueSubjects(issues []Issue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		if i.Note != "" {
			out = append(out, i.Note)
			continue
		}
		out = append(out, i.Path)
	}
	return out
}

// IssuesError decides the exit status. An invalid note always fails: the store could not
// be READ as declared, so anything acting on the result is acting on a partial
//
// Under --strict a dangling anchor fails too, and nothing else ever does. The line is drawn
// there because of what each finding costs to act on: a dangling anchor names something that
// was renamed or deleted in a diff someone is already looking at, and the fix is bounded. A
// drift finding asks a person to re-read prose against code and decide, and a gate that
// blocks a merge pending a judgment call is one people route around permanently. The only
// actively-maintained tool in this space is a CI check that fails a pull request when covered
// files move: the gate is the mechanism that works, and keeping it narrow is what keeps it.
func IssuesError(issues []Issue, strict bool) error {
	var failures, dangling int
	for _, issue := range issues {
		if issue.Severity == "error" {
			failures++
		}
		if strict && issue.Code == CodeDanglingAnchor {
			dangling++
		}
	}
	if failures == 0 && dangling != 0 {
		return fmt.Errorf("magus notes verify: %d dangling anchor%s, re-anchor the note or remove the anchor",
			dangling, plural.Word(dangling, "", "s"))
	}
	if failures != 0 {
		return fmt.Errorf("magus notes verify: %d invalid note%s", failures, plural.Word(failures, "", "s"))
	}
	return nil
}
