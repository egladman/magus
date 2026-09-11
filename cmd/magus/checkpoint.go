package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// checkpointCmd records where the work stands, for whoever comes back to it.
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
func checkpointCmd(ctx context.Context, root string, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("checkpoint", flag.ContinueOnError)
	bindDisplayFlags(fset)
	cf := gen.BindSessionCheckpoint(fset)
	fset.Usage = func() { checkpointUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("magus session checkpoint: takes no positional arguments (pass --note, or pipe a host's hook envelope in)")
	}
	opts, err := ResolveOutput(global.output)
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

	hostSession, transcript := readCheckpointEnvelope(in)
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
	spawn := trail.SpawnFromEnv()
	stored, recorded, err := sessions.RecordCheckpoint(dir, c, sessions.SessionStart{
		Host:      c.Host,
		Workspace: wsRoot,
		Version:   version,
		Lease:     spawn.Lease,
		TraceID:   spawn.TraceID,
		SpanID:    trail.NewSpanID(),
		Spawner:   spawn.Spawner,
	})
	if err != nil {
		return fmt.Errorf("magus session checkpoint: %w", err)
	}

	switch opts.Format {
	case FormatText:
		fmt.Fprintln(out, checkpointRecordedLine(stored, recorded))
		return nil
	case FormatName:
		fmt.Fprintln(out, stored.Tree.Revision)
		return nil
	}
	// recorded travels with the record: a wrapper reading the structured form has no
	// other way to tell a new checkpoint from one identical to the last.
	return writeFormatted(out, opts, struct {
		sessions.Checkpoint
		Recorded bool `json:"recorded"`
	}{Checkpoint: stored, Recorded: recorded})
}

// checkpointEnvelopeWait bounds how long a host's payload may take to arrive.
//
// stdin is this command's optional enrichment, not its input, so waiting on it forever
// trades two pointers for the record it was meant to write. A hook writes its event and
// closes; anything that has sent nothing by now is a shell that inherited an idle pipe,
// which no amount of further waiting improves.
const checkpointEnvelopeWait = 2 * time.Second

// checkpointEnvelopeMax bounds how much of it is read, matching the cap the adoption
// command puts on the same class of input.
const checkpointEnvelopeMax = 4 << 20

// readCheckpointEnvelope decodes a host's hook payload from in for the two pointers only
// a host knows: its session id and its transcript path. A payload that is absent,
// unparsable, or slow to arrive yields the zero envelope, which contributes nothing.
//
// Nothing in the payload becomes the note. A host's closing message is the model's
// prose, and copying it in would make the record indistinguishable from a sentence a
// person wrote, in a store a person reads. A wrapper that wants the model's words passes
// --note and owns that decision.
//
// The read is bounded rather than skipped when --note is set. Gating it on the flag also
// kept the pointers out of the record for any wrapper that passed a note, which is the
// shape the docs invite, and it left the actual hazard open: a caller with no --note and
// an idle inherited pipe still waited forever.
func readCheckpointEnvelope(in io.Reader) (session, transcript string) {
	if stdinIsTerminal() {
		return "", ""
	}
	type read struct {
		body []byte
		err  error
	}
	done := make(chan read, 1)
	go func() {
		body, err := io.ReadAll(io.LimitReader(in, checkpointEnvelopeMax))
		done <- read{body: body, err: err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			return "", ""
		}
		return guard.HostAttribution(string(r.body))
	case <-time.After(checkpointEnvelopeWait):
		return "", ""
	}
}

// checkpointRecordedLine is the terminal reading of one write: where the work sits, and
// whether this call changed anything. The location is rendered by the same function the
// listing uses, so recording a checkpoint and reading one back describe it identically.
//
// Distinct from vcs.go's checkpointLine, which renders the VCS triple alone in fixed
// columns for `magus vcs checkpoint`. Same noun, two audiences.
func checkpointRecordedLine(c sessions.Checkpoint, recorded bool) string {
	verb := "checkpoint at"
	if !recorded {
		verb = "unchanged since the last checkpoint at"
	}
	line := verb + " " + checkpointWhere(c)
	if c.Note != "" {
		line += ": " + c.Note
	}
	return line
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
