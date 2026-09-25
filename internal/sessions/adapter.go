package sessions

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

// Running a declared transcript adapter. `magus session load` is the ingest point and
// this is the scheduler for it: the commands in knowledge.sessions.adapters, run from the
// workspace root before the graph is assembled.
//
// Why a subprocess and not a reader in this package: a transcript belongs to the host
// that wrote it, and hosts change their formats on their own release cadence. An adapter
// absorbs that, so a host renaming a tool costs a workspace one line of YAML rather than
// costing magus a release, and a host magus has never heard of is supported by whoever
// needs it without asking. The ready-made adapters magus documents are shell scripts for
// exactly this reason.

// adapterTimeout bounds one adapter.
//
// Generous because the first run of an adapter is the expensive one: it reads a host's
// entire transcript history, where every run after it reads only the tail. Measured on
// this repository, a first run over 13 sessions took 11s. It is a ceiling on a stuck
// adapter, not a budget anything is expected to approach.
const adapterTimeout = 5 * time.Minute

// adapterWaitDelay bounds how long Wait keeps reading the output pipe after the deadline
// killed the process group. Short: by this point the answer is already "this adapter
// failed", and what is left on the pipe belongs to something that outlived a SIGKILL to
// its whole group.
const adapterWaitDelay = 5 * time.Second

// adapterOutputLimit caps what one adapter's output can cost. A summary is three lines;
// anything approaching this is an adapter logging per file, which is a bug in the adapter
// rather than something to carry into a caller's memory and the server's log.
const adapterOutputLimit = 64 << 10

// Adapter is one host's transcript loader: a command to run from the workspace root.
//
// Declared here rather than taken as config.SessionAdapter. This package owns the session
// store; the config package owns the workspace schema; and a store package that names the
// configuration schema's types puts the two one import away from a cycle for no gain,
// since running a command needs two strings. The composition root maps one to the other,
// which is the same division internal/graph/knowledge draws with FileCoverage and
// AgentContact.
type Adapter struct {
	Host string // the host whose transcripts this reads, for reporting
	// Argv is the command and its arguments, run from the workspace root. ARGV rather
	// than a command line, because this is a command magus EXECUTES, and that is how magus
	// represents those: job.CatalogEntry.Argv is the same shape for the same reason. A
	// string belongs on the other side of the boundary, in the hook commands magus writes
	// into a host's own config document, where the HOST does the parsing.
	//
	// It is also what retires the injection surface rather than filtering it. Executed as
	// argv there is no shell to interpret `;`, `|`, `$(...)` or a redirect, so a declared
	// adapter runs one program with the arguments it names and cannot become a second
	// command. Anything wanting a pipeline writes a script and names the script.
	Argv []string
}

// AdapterResult is what one adapter did, and is reported rather than returned as an
// error: an adapter that fails leaves the previous ingest in place, which is a graph
// missing its newest sessions and not a graph that is wrong.
type AdapterResult struct {
	Host string `json:"host" yaml:"host"`
	// Err is why the adapter did not finish, or nil. Not serialized: an error has no
	// stable wire shape, and a caller rendering these has the Output, which is where an
	// adapter says what went wrong in its own words.
	Err    error  `json:"-" yaml:"-"`
	Output string `json:"output,omitempty" yaml:"output,omitempty"` // combined stdout and stderr, trimmed
}

// RunAdapters runs each adapter and returns one result per adapter, in the order given.
//
// It returns no error, and that is the contract rather than an omission: these are
// independent operations where the first failure must not abort the rest, so a single
// error would force the caller to invent a meaning for partial success. Whether ingestion
// is switched off at all is the caller's question, asked before it gets here.
//
// Adapters run SEQUENTIALLY. They are I/O bound on one machine's transcript store and
// they all append to one session store, so running them at once would trade a clean
// ordering for no measurable time.
func RunAdapters(ctx context.Context, root string, adapters []Adapter) []AdapterResult {
	if root == "" {
		return nil
	}
	var out []AdapterResult
	for _, a := range adapters {
		// Rechecked per adapter, not just inside one: a cancelled build would otherwise
		// walk the whole list spawning shells that die immediately, and report one failure
		// per adapter for a run nobody is waiting on any more.
		if ctx.Err() != nil {
			break
		}
		if len(a.Argv) == 0 || strings.TrimSpace(a.Argv[0]) == "" {
			continue
		}
		out = append(out, runAdapter(ctx, root, a))
	}
	return out
}

// runAdapter runs one adapter from the workspace root.
//
// The environment is INHERITED whole, HOME included, which is the one thing worth
// stating: an adapter's whole job is to read a transcript store that lives outside the
// workspace, and every host puts that under the user's home directory. A probe-style
// scratch HOME would make every adapter report an empty store.
func runAdapter(ctx context.Context, root string, a Adapter) AdapterResult {
	runCtx, cancel := context.WithTimeout(ctx, adapterTimeout)
	defer cancel()

	// No shell. The program and its arguments are passed straight to exec, so nothing in a
	// declared adapter can start a second command, expand a variable, or redirect.
	cmd := exec.CommandContext(runCtx, a.Argv[0], a.Argv[1:]...)
	cmd.Dir = root

	// The timeout alone does NOT bound this, and that is not a subtlety worth
	// rediscovering. Writing into a buffer makes os/exec allocate a pipe and a copier
	// goroutine, and Wait blocks until every holder of the write end closes it, while
	// CommandContext's default cancel signals the direct child only. So an adapter that
	// backgrounds anything leaves a grandchild holding the pipe: sh dies at the deadline,
	// the grandchild does not, and Run never returns. In the server that wedges a run slot
	// for the process's life, and the maintenance scheduler only submits while idle, so
	// one such adapter silently ends log rotation and every other scheduled job too.
	//
	// killGroup is per-platform because process groups are: see adapter_unix.go and
	// adapter_other.go. WaitDelay is not, and it is the backstop either way: Wait gives up
	// on the pipe rather than blocking on it forever.
	killGroup(cmd)
	cmd.WaitDelay = adapterWaitDelay

	// Capped, and both streams share it: an adapter's useful output is a summary line at
	// the end, while its failure mode is per-file logging over a whole transcript store.
	// Uncapped, that lands in memory and then in the server's log in full.
	buf := &cappedBuffer{limit: adapterOutputLimit}
	cmd.Stdout, cmd.Stderr = buf, buf
	err := cmd.Run()
	return AdapterResult{Host: a.Host, Err: err, Output: strings.TrimSpace(buf.String())}
}

// cappedBuffer keeps the LAST limit bytes written to it.
//
// The last rather than the first: an adapter reports what it did on its final line, so
// truncating the head keeps the summary and drops the per-file chatter, where an
// io.LimitWriter would keep the chatter and lose the summary.
type cappedBuffer struct {
	buf     bytes.Buffer
	limit   int
	dropped bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if _, err := c.buf.Write(p); err != nil {
		return 0, err
	}
	if c.buf.Len() > c.limit {
		c.dropped = true
		keep := c.buf.Bytes()[c.buf.Len()-c.limit:]
		rest := bytes.Clone(keep)
		c.buf.Reset()
		c.buf.Write(rest)
	}
	return n, nil
}

func (c *cappedBuffer) String() string {
	if c.dropped {
		return "[earlier output dropped]\n" + c.buf.String()
	}
	return c.buf.String()
}
