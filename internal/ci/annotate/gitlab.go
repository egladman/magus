package annotate

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// gitlabCI writes GitLab CI section markers.
//
// It exists as much as documentation as for GitLab itself: it is the
// provider that exercises the parts of [Annotator] GitHub does not. A
// section is keyed by an id distinct from its title, carries a timestamp,
// takes collapse as a flag rather than a fixed behaviour, and GitLab has
// no in-log annotation syntax at all - so Annotate here is honestly a
// no-op, which is the normal case the contract is built to allow.
//
// Marker syntax (the \r\e[0K pairs hide the marker itself from the
// rendered log):
//
//	\e[0Ksection_start:<unix>:<id>[collapsed=true]\r\e[0K<title>
//	\e[0Ksection_end:<unix>:<id>\r\e[0K
type gitlabCI struct {
	w io.Writer
	// now is injected so a test can assert an exact marker; GitLab requires
	// a wall-clock second in every section marker.
	now func() time.Time
}

func newGitLabCI(w io.Writer) Annotator {
	return &gitlabCI{w: w, now: time.Now}
}

// Active reports whether this process runs inside a GitLab CI job. GitLab
// sets GITLAB_CI=true for every job.
func (*gitlabCI) Active() bool { return os.Getenv("GITLAB_CI") == "true" }

func (g *gitlabCI) StartGroup(grp Group) error {
	id := SanitizeID(grp.ID)
	if id == "" {
		return nil // a section with no name cannot be closed
	}
	collapsed := ""
	if grp.Collapsed {
		collapsed = "[collapsed=true]"
	}
	_, err := fmt.Fprintf(g.w, "\x1b[0Ksection_start:%d:%s%s\r\x1b[0K%s\n",
		g.now().Unix(), id, collapsed, gitlabEscape(grp.Title))
	return err
}

func (g *gitlabCI) EndGroup(id string) error {
	id = SanitizeID(id)
	if id == "" {
		return nil
	}
	_, err := fmt.Fprintf(g.w, "\x1b[0Ksection_end:%d:%s\r\x1b[0K\n", g.now().Unix(), id)
	return err
}

// Annotate does nothing. GitLab surfaces findings from report artifacts
// (Code Quality, SAST) uploaded by the job, not from anything written to
// the log, so there is no marker to emit. Reporting that as an error
// would be wrong: the provider simply does not have this capability.
func (*gitlabCI) Annotate(Annotation) error { return nil }

// Concurrency offers no opinion. GitLab runners range from a 1-vCPU
// shared instance to self-managed hardware, and nothing in the job
// environment distinguishes them, so guessing would be worse than
// deferring to the machine's own CPU count.
func (*gitlabCI) Concurrency() int { return 0 }

// Quote neutralises section markers in replayed subprocess output, so
// captured output cannot close a section magus opened or forge one of
// its own. Dropping the leading ESC is enough: without it GitLab treats
// the rest as ordinary text.
func (*gitlabCI) Quote(text string) string { return gitlabEscape(text) }

// gitlabEscape removes the escape byte that introduces a section marker.
// The visible text is preserved so a reader still sees what the process
// printed, minus its ability to control the log's structure.
func gitlabEscape(s string) string {
	if !strings.Contains(s, "\x1b[0Ksection_") {
		return s
	}
	return strings.ReplaceAll(s, "\x1b[0Ksection_", "[0Ksection_")
}
