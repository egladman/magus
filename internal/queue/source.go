package queue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/egladman/magus/internal/queue/types"
)

// SourceUsage documents apply's operand; ParseSource implements it.
const SourceUsage = `<source> is where apply reads the plan and the verdicts:
  <dir>        the directory validate --verdicts wrote
  run:<run>    the artifacts of a validation run, <run> as the provider names it
               (github: <owner>/<name>/runs/<id>)
Text before the first ":" that reads as a URL scheme of two or more characters is
one, and an unknown scheme is an error; write ./<dir> for a directory named like one.
`

// RunScheme is the scheme of a source that names a validation run.
const RunScheme = "run"

// Source is apply's operand: a verdict directory, or a validation run whose artifacts
// hold one. Exactly one of Dir and Run is set.
type Source struct {
	Dir string
	Run string // what the provider's list_artifacts reads
}

// ParseSource reads arg by the grammar in SourceUsage. A one-letter scheme is a Windows
// drive, as git reads it, so C:\queue stays a path.
func ParseSource(arg string) (Source, error) {
	if arg == "" {
		return Source{}, errors.New("<source> is empty")
	}
	scheme, spec, ok := strings.Cut(arg, ":")
	if !ok || len(scheme) < 2 || !isURLScheme(scheme) {
		return Source{Dir: arg}, nil
	}
	if scheme != RunScheme {
		return Source{}, fmt.Errorf("source %q: unknown scheme %q, write ./%s for a directory", arg, scheme, arg)
	}
	if strings.TrimSpace(spec) == "" {
		return Source{}, fmt.Errorf("source %q: want %s:<run>", arg, RunScheme)
	}
	return Source{Run: spec}, nil
}

// isURLScheme applies RFC 3986's scheme syntax.
func isURLScheme(s string) bool {
	for i, r := range s {
		switch {
		case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z':
		case i > 0 && ('0' <= r && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return s != ""
}

// PlanSource is a verdict source that also carries the plan its verdicts answer to.
type PlanSource interface {
	types.VerdictSource
	Plan(ctx context.Context) (types.Plan, bool, error)
}

// RemoteHost is the host a remote URL names, "" when it names none. It reads URL forms
// and git's scp form (user@host:path), and never returns the userinfo a URL may carry.
func RemoteHost(remote string) string {
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		return u.Hostname()
	}
	if _, rest, ok := strings.Cut(remote, "@"); ok {
		if host, _, ok := strings.Cut(rest, ":"); ok {
			return host
		}
	}
	return ""
}

// ReadChangesFile reads a changes document from file, or from stdin when file is "-".
func ReadChangesFile(file string, stdin io.Reader) (types.Changes, error) {
	if file == "-" {
		return ReadChanges(stdin)
	}
	f, err := os.Open(file)
	if err != nil {
		return types.Changes{}, err
	}
	defer f.Close()
	return ReadChanges(f)
}
