package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/types"
)

// notifyCommand turns one attention event into a stable types.Event record. It is a
// local command: a notification belongs on the caller's desktop, never on a
// server host. A producer sends either the canonical JSON envelope or plain
// text on stdin. A host-specific wrapper is responsible for shaping its own
// event before it crosses this boundary.
//
// Notification delivery is deliberately best effort. Hosts must be able to
// invoke this from a hook without a missing desktop notifier breaking the agent
// session that the notification is meant to help.
//
// This is the ONLY producer of attention requests. An event that means the work
// has stopped until a person acts also opens a durable request `magus session attention`
// lists; there is deliberately no `attention raise` twin, because one ingest path
// is what keeps the queue's contents traceable to a single normalization.
func notifyCommand(ctx context.Context, root string, in io.Reader, out io.Writer, args []string) error {
	fset := flag.NewFlagSet("session notify", flag.ContinueOnError)
	bindDisplayFlags(fset)
	nf := flaggen.BindSessionNotify(fset)
	fset.Usage = func() { notifyUsage(os.Stderr) }
	if err := fset.Parse(commands.ReorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return exit.Usagef("magus session notify: takes no positional arguments (read the event from stdin)")
	}
	opts, err := output.Parse(global.output)
	if err != nil {
		return err
	}

	body, readError := io.ReadAll(in)
	if readError != nil {
		return fmt.Errorf("magus session notify: read stdin: %w", readError)
	}

	ev := sessions.EventFromStdin(ctx, body)
	if nf.Outcome != "" {
		ev.Outcome = sessions.ClassifyOutcome(nf.Outcome)
	} else {
		ev.Outcome = sessions.ClassifyOutcome(string(ev.Outcome))
	}
	sessions.NormalizeEvent(&ev)

	held, err := fileAttention(ctx, root, ev)
	if err != nil {
		noteAttentionOpenFailure(err)
	}

	// A repeat of a block the queue already holds would ask for a yes on it twice.
	if nf.Desktop && !held {
		_ = raiseDesktop(ctx, ev)
	}

	switch opts.Format {
	case outputText:
		title, message := sessions.RenderNotification(ev)
		fmt.Fprintf(out, "%s: %s\n", title, message)
		return nil
	case outputName:
		return emitNames(os.Stdout, []string{string(ev.Outcome)})
	}
	return writeFormatted(out, opts, ev)
}

func notifyUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus session notify [--outcome <vocab>] [--desktop]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Raise one canonical attention event. Input may be plain text, a complete")
	fmt.Fprintln(w, "types.Event JSON envelope. --desktop additionally raises an OS notification.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "An event whose outcome is waiting or permission also opens a durable request")
	fmt.Fprintf(w, "in this repository, which `%s` lists and only a person closes.\n", hint.SessionAttention.String())
	fmt.Fprintln(w, "A repeat of a request the queue already holds does not raise another desktop")
	fmt.Fprintln(w, "notification. A failure still does.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintf(w, "  printf '%%s\\n' 'needs approval' | magus session notify --outcome permission --desktop\n")
	fmt.Fprintf(w, "  printf '%%s\\n' '{\"outcome\":\"permission\",\"source\":{\"kind\":\"agent\"},\"message\":\"needs approval\"}' | magus session notify --desktop\n")
}

// raiseDesktop posts the OS notification. Tests replace it; production calls
// [raiseDesktopNotification].
var raiseDesktop = raiseDesktopNotification

func raiseDesktopNotification(ctx context.Context, ev types.Event) error {
	title, body := sessions.RenderNotification(ev)
	switch runtime.GOOS {
	case "darwin":
		//nolint:gosec // fixed program; the event text is an argument, not a shell
		return exec.CommandContext(ctx, "osascript", "-e", `on run argv
	display notification item 1 of argv with title item 2 of argv
end run`, body, title).Run()
	case "linux":
		return exec.CommandContext(ctx, "notify-send", title, body).Run() //nolint:gosec // fixed program; the event text is passed as arguments, not a shell
	}
	return nil
}
