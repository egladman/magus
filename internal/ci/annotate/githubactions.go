package annotate

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// githubActions writes GitHub Actions workflow commands. It is the only
// type in the Go tree that knows this syntax; core reaches it through
// [Annotator].
//
// Annotations stay in Go rather than the github spell (which carries the
// Actions cache backend) because they are a few bytes of formatting
// emitted from the middle of the run loop, and because they must work
// with no magusfile wiring at all. A spell provider overrides this; see
// [RegisterOpener].
type githubActions struct {
	w io.Writer
	// open tracks whether StartGroup actually emitted a fold. Actions has
	// no expanded section, so an expanded request emits nothing - and an
	// unmatched ::endgroup:: would then close a group the surrounding
	// workflow opened, folding away output magus knows nothing about.
	open bool
}

func newGitHubActions(w io.Writer) Annotator { return &githubActions{w: w} }

// Active reports whether this process runs inside a GitHub Actions job.
// Actions sets GITHUB_ACTIONS=true for every step; matching the exact
// value rather than mere presence keeps another host that exports the
// name from being mistaken for Actions.
func (*githubActions) Active() bool { return os.Getenv("GITHUB_ACTIONS") == "true" }

// StartGroup opens a fold.
//
// Actions has no expanded group: ::group:: always starts collapsed. A
// caller asking for an expanded section is asking for something the
// reader must not have to click for, so rather than fold it anyway the
// group is skipped and the content is left inline. Honouring the request
// badly would be worse than not honouring it: a folded failure is a
// failure nobody sees.
func (g *githubActions) StartGroup(grp Group) error {
	if !grp.Collapsed {
		return nil
	}
	if _, err := fmt.Fprintf(g.w, "::group::%s\n", ghaEscapeData(grp.Title)); err != nil {
		return err
	}
	g.open = true
	return nil
}

// EndGroup closes the current fold. Actions tracks only one open group,
// so the id is unused; it is part of the signature for providers that
// key sections by name.
func (g *githubActions) EndGroup(string) error {
	if !g.open {
		return nil
	}
	g.open = false
	_, err := fmt.Fprintln(g.w, "::endgroup::")
	return err
}

func (g *githubActions) Annotate(a Annotation) error {
	title := a.Title
	if a.Code != "" {
		// Actions has no dedicated code field, so the code leads the title
		// where it stays greppable in the annotations list.
		title = strings.TrimSpace(a.Code + " " + title)
	}
	props := []string{}
	addProp := func(k, v string) {
		if v != "" {
			props = append(props, k+"="+ghaEscapeProp(v))
		}
	}
	addNum := func(k string, v int) {
		if v > 0 {
			props = append(props, k+"="+strconv.Itoa(v))
		}
	}
	addProp("file", a.File)
	addNum("line", a.Line)
	addNum("endLine", a.EndLine)
	addNum("col", a.Col)
	addNum("endColumn", a.EndCol)
	addProp("title", title)

	var sb strings.Builder
	sb.WriteString("::" + a.Level.String())
	if len(props) > 0 {
		sb.WriteString(" " + strings.Join(props, ","))
	}
	sb.WriteString("::" + ghaEscapeData(a.Message) + "\n")
	_, err := io.WriteString(g.w, sb.String())
	return err
}

// Concurrency caps parallelism on GitHub-hosted runners, which are far
// smaller than a developer machine. Self-hosted runners are the user's
// own hardware, so magus offers no opinion there.
func (*githubActions) Concurrency() int {
	if os.Getenv("RUNNER_ENVIRONMENT") == "self-hosted" {
		return 0
	}
	return 4
}

// Quote neutralises workflow commands in replayed subprocess output.
//
// Actions provides stop-commands for exactly this, but it needs an
// end token that cannot appear in the text, and a wrapper is awkward
// around output magus interleaves with its own lines. Breaking the "::"
// prefix is simpler and local: the runner only interprets a command at
// the start of a line, so a zero-width-free substitution that keeps the
// text readable is enough to defuse it.
func (*githubActions) Quote(text string) string {
	if !strings.Contains(text, "::") {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		trimmed := strings.TrimLeft(ln, " \t")
		if strings.HasPrefix(trimmed, "::") {
			indent := ln[:len(ln)-len(trimmed)]
			lines[i] = indent + ":" + trimmed
		}
	}
	return strings.Join(lines, "\n")
}

// ghaEscapeData encodes the characters Actions treats as structural in a
// command's message. A raw newline would end the command early and spill
// the remainder into the log as loose text, so multi-line messages are
// encoded rather than truncated: a failure's cause is usually the part
// after the first line.
//
// Percent is replaced first, or it would corrupt the escapes introduced
// after it.
func ghaEscapeData(s string) string {
	return strings.NewReplacer(
		"%", "%25",
		"\r", "%0D",
		"\n", "%0A",
	).Replace(s)
}

// ghaEscapeProp additionally encodes the separators that delimit
// properties, so a title containing a comma or colon cannot be read as
// the start of another property.
func ghaEscapeProp(s string) string {
	return strings.NewReplacer(
		"%", "%25",
		"\r", "%0D",
		"\n", "%0A",
		":", "%3A",
		",", "%2C",
	).Replace(s)
}
