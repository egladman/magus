package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/egladman/magus/cmd/magus/gen"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// pauseCmd records where the work stands, for whoever comes back to it.
//
// It is one command with two callers and no branch between them. A person runs it
// with a --note before putting something down; an agent host's stop hook pipes its
// event envelope in. Both produce the same record, because what a reader needs -
// which revision, which branch, whether the tree is dirty, and a sentence about it -
// does not depend on who stopped working.
//
// magus reads no transcript, resolves no session anywhere, and starts nothing. The
// host's session id and transcript path are recorded as opaque pointers so a PERSON
// can follow them with the tool that wrote them.
func pauseCmd(ctx context.Context, root string, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("pause", flag.ContinueOnError)
	bindDisplayFlags(fset)
	pf := gen.BindSessionPause(fset)
	fset.Usage = func() { pauseUsage(os.Stderr) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return usagef("magus session pause: takes no positional arguments (pass --note, or pipe a host's hook envelope in)")
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
		return fmt.Errorf("magus session pause: no workspace here: the session store is keyed by repository, so run from inside one or pass --root <path>")
	}

	p := sessions.Pause{Workspace: wsRoot, Note: pf.Note}
	if env, ok := readPauseEnvelope(in, pf.Note); ok {
		p.Session = env.SessionID
		p.Transcript = env.TranscriptPath
	}
	// An explicit flag outranks the envelope: a wrapper that passed one meant it.
	overlay(&p.Host, pf.AgentName)
	overlay(&p.Session, pf.Session)
	overlay(&p.Transcript, pf.Transcript)

	res, err := vcs.Resolve(ctx, wsRoot, "", vcsOpts)
	if err != nil {
		return fmt.Errorf("magus session pause: %w", err)
	}
	if p.At, err = vcs.Checkpoint(ctx, wsRoot, res); err != nil {
		return fmt.Errorf("magus session pause: %w", err)
	}

	dir, err := sessions.Dir(wsRoot)
	if err != nil {
		return err
	}
	spawn := trail.SpawnFromEnv()
	recorded, err := sessions.RecordPause(dir, p, sessions.SessionStart{
		Host:      p.Host,
		Workspace: wsRoot,
		Version:   version,
		Lease:     spawn.Lease,
		TraceID:   spawn.TraceID,
		SpanID:    trail.NewSpanID(),
		Spawner:   spawn.Spawner,
	})
	if err != nil {
		return fmt.Errorf("magus session pause: %w", err)
	}

	switch opts.Format {
	case FormatText:
		fmt.Fprintln(out, pauseLine(p, recorded))
		return nil
	case FormatName:
		fmt.Fprintln(out, p.At.Revision)
		return nil
	}
	return writeFormatted(out, opts, p)
}

// readPauseEnvelope decodes a host's hook payload from in for the two pointers only a
// host knows - its session id and its transcript path - reporting whether one arrived.
//
// Nothing in the payload becomes the note. A host's closing message is the model's
// prose, and copying it in would make the record indistinguishable from a sentence a
// person wrote, in a store a person reads. A wrapper that wants the model's words
// passes --note and owns that decision.
//
// note being set is what stops the read, and a terminal is never read at all. Both
// guard the same failure: `magus session pause --note "..."` from a Makefile, a CI step,
// or any shell holding an open pipe would otherwise block on an EOF nobody is going to
// send. stdin is this command's optional enrichment, not its input.
func readPauseEnvelope(in io.Reader, note string) (hookEnvelope, bool) {
	if note != "" || stdinIsTerminal() {
		return hookEnvelope{}, false
	}
	body, err := io.ReadAll(in)
	if err != nil {
		return hookEnvelope{}, false
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" || trimmed[0] != '{' {
		return hookEnvelope{}, false
	}
	var env hookEnvelope
	if json.Unmarshal([]byte(trimmed), &env) != nil {
		return hookEnvelope{}, false
	}
	return env, true
}

// overlay writes v over dst when v was set, leaving whatever the envelope supplied when
// it was not.
func overlay(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

// pauseLine is the terminal reading of one pause: where the work sits, and whether
// this call changed anything.
func pauseLine(p sessions.Pause, recorded bool) string {
	var b strings.Builder
	if recorded {
		b.WriteString("paused at ")
	} else {
		b.WriteString("unchanged since the last pause at ")
	}
	b.WriteString(shortRevision(p.At))
	if p.At.Branch != "" {
		fmt.Fprintf(&b, " on %s", p.At.Branch)
	}
	if p.At.Dirty {
		b.WriteString(", uncommitted changes")
	}
	if p.Note != "" {
		fmt.Fprintf(&b, ": %s", p.Note)
	}
	return b.String()
}

// shortRevision abbreviates for reading. The stored revision stays full, because that
// is the one a reader feeds back to a VCS.
func shortRevision(cp types.VCSCheckpoint) string {
	if len(cp.Revision) > 12 {
		return cp.Revision[:12]
	}
	return cp.Revision
}

func pauseUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus session pause [--note <text>] [flags]")
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
