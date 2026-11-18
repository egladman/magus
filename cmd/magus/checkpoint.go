package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// checkpointCommand records where the work stands, for whoever comes back to it.
//
// It is one command with two callers and no branch between them. A person runs it with
// a --note before putting something down; an agent host's stop hook pipes its event
// envelope in. Both produce the same record, because what a reader needs (which
// revision, which branch, whether the tree is dirty, and a sentence about it) does not
// depend on who stopped working.
//
// It records the same thing `magus vcs checkpoint` computes, and keeps it. magus reads
// no transcript, resolves no session anywhere, and starts nothing: the host's session id
// and transcript path are opaque pointers, recorded so a PERSON can follow them with the
// tool that wrote them.
func checkpointCommand(ctx context.Context, root string, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("session checkpoint", flag.ContinueOnError)
	bindDisplayFlags(fset)
	cf := flaggen.BindSessionCheckpoint(fset)
	fset.Usage = func() { checkpointUsage(os.Stderr) }
	if err := fset.Parse(commands.ReorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return exit.Usagef("magus session checkpoint: takes no positional arguments (pass --note, or pipe a host's hook envelope in)")
	}
	opts, err := output.Parse(global.output)
	if err != nil {
		return err
	}

	// A magusfile that will not load must not cost you the record. The rest of the
	// session family loads no workspace at all, and this one needs it only for the VCS
	// options; falling back to the plain root keeps a stop hook working through the
	// bootstrap state where every other magus command is failing, which is exactly when
	// a person most wants to know where the work stopped.
	//
	// The RESOLVED root, never the --root override: an empty override sends every VCS
	// call to the process cwd, which is a different repository the moment this runs from
	// anywhere but the top. Same reasoning as vcs checkpoint.
	wsRoot, vcsOpts := resolveRootOrEmpty(root), types.VCSOptions{}
	if ws, err := inspectWorkspace(ctx, root); err == nil {
		wsRoot, vcsOpts = ws.Root(), ws.VCSOptions()
	}
	if wsRoot == "" {
		return errors.New("magus session checkpoint: no workspace here: the session store is keyed by repository, so run from inside one or pass --root <path>")
	}

	hostSession, transcript := guard.ReadCheckpointEnvelope(in, termkit.StdinIsTerminal())
	c := sessions.Checkpoint{
		Workspace:   wsRoot,
		Note:        cf.Note,
		HostSession: cmp.Or(cf.Session, hostSession),
		Transcript:  cmp.Or(cf.Transcript, transcript),
		Host:        cf.AgentName,
	}

	// A tree with no revision to report still gets a record. Three ordinary states
	// reach here (a repository before its first commit, a directory under no VCS at
	// all, and VCS disabled by config), and this runs as a stop hook on every
	// documented host, so failing would mean the hook errors every turn and files
	// nothing in exactly the trees where "where was I" is hardest to answer.
	if res, err := vcs.Resolve(ctx, wsRoot, "", vcsOpts); err == nil {
		// Never preserves: this runs as a stop hook on every turn, and a hook that minted
		// an object per turn would fill the backend with captures nobody asked for.
		c.Tree, _ = vcs.Checkpoint(ctx, wsRoot, res, false)
	}

	dir, err := sessions.Dir(wsRoot)
	if err != nil {
		return err
	}
	spawn := recorder.SpawnFromEnv()
	lease, leaseFrom, err := guard.CheckoutLease(ctx, wsRoot, jobPlace)
	if err != nil {
		return fmt.Errorf("magus session checkpoint: %w", err)
	}
	origin := recorder.LocalOrigin(ctx)
	origin.Host, origin.Session = c.Host, c.HostSession
	stored, recorded, err := sessions.RecordCheckpoint(ctx, dir, c, session.InvocationStart{
		Origin:    origin,
		Workspace: wsRoot,
		Version:   version,
		Lease:     lease,
		LeaseFrom: leaseFrom,
		TraceID:   spawn.TraceID,
		SpanID:    recorder.NewSpanID(),
		Spawner:   spawn.Spawner,
	})
	if err != nil {
		return fmt.Errorf("magus session checkpoint: %w", err)
	}

	switch opts.Format {
	case outputText:
		fmt.Fprintln(out, sessions.CheckpointRecordedLine(stored, recorded))
		return nil
	case outputName:
		return emitNames(os.Stdout, []string{stored.Tree.Revision})
	}
	// recorded travels with the record: a wrapper reading the structured form has no
	// other way to tell a new checkpoint from one identical to the last.
	return writeFormatted(out, opts, struct {
		sessions.Checkpoint
		Recorded bool `json:"recorded"`
	}{Checkpoint: stored, Recorded: recorded})
}

func checkpointUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus session checkpoint [--note <text>] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Record where the work stands, so whoever comes back to it does not have to")
	fmt.Fprintln(w, "reconstruct it. Run it before you put something down; an agent host can wire")
	fmt.Fprintln(w, "it to a stop hook and pipe its event envelope in.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  --note <text>          where the work stands, in a sentence")
	fmt.Fprintln(w, "  --agent-name <name>    the agent host this ran on, when one did")
	fmt.Fprintln(w, "  --session <id>         the host's own session id")
	fmt.Fprintln(w, "  --transcript <path>    the host's log of this session, recorded as a pointer")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Read them back with `magus session`.")
}
