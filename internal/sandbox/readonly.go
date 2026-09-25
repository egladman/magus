package sandbox

import (
	"fmt"
	"os"

	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// ReadOnly derives the policy `magus buzz --read-only` runs under: the reads p grants,
// and no write or exec anywhere. A nil p grants every read and hides no environment
// variable, as running with the sandbox off does. p is not modified.
func ReadOnly(p *Policy) *Policy {
	if p == nil {
		return &Policy{ReadOnly: true, unconfined: true}
	}
	out := *p
	out.FS = filesystem.Ruleset{Rules: make([]filesystem.Rule, len(p.FS.Rules))}
	for i, r := range p.FS.Rules {
		r.Write, r.Exec = false, false
		out.FS.Rules[i] = r
	}
	out.ReadOnly = true
	return &out
}

// ReadOnlyHint is the remedy a read-only refusal prints.
const ReadOnlyHint = "the script runs read-only (magus buzz --read-only): it may read, compute and print, " +
	"and nothing it calls may write or start a process. Run it without --read-only to allow that."

func readOnlyDenied(path string) error {
	return fmt.Errorf("%w: %s: the run is read-only", filesystem.ErrDenied, path)
}

// ApplyReadOnly confines this process and every process it starts to reads, at the
// kernel. It adds a landlock layer granting read and execute beneath / and write on
// the null device alone; landlock layers intersect, so a workspace ruleset applied
// before or after still narrows reads. Exec stays granted because a child inherits the
// layer and so cannot write either.
//
// The restriction is permanent for the process: call it only from a one-shot command,
// never from the server or a test binary that goes on to run other work. It reports
// ErrUnsupported where landlock is unavailable (every non-Linux host, kernels before
// 5.13), which leaves the interpreter-level checks as the only enforcement.
func ApplyReadOnly() error {
	return Apply(&Policy{FS: filesystem.Ruleset{Rules: readOnlyKernelRules()}})
}

func readOnlyKernelRules() []filesystem.Rule {
	return []filesystem.Rule{
		{Path: "/", Read: true, Exec: true},
		// exec.Cmd opens the null device for writing whenever a stream is left nil.
		{Path: os.DevNull, Read: true, Write: true},
	}
}
