package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/clikit/exit"
)

// jobStop implements `magus job stop <job>`: the broker signals only the process groups
// it recorded for the job, then frees the claims and seats they held.
func jobStop(ctx context.Context, args []string) error {
	spec, _ := commandAt("job stop")
	pos, err := commandParse("job stop", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: "+spec.Usage)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, cmp.Or(spec.Description, spec.Short))
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return exit.Usagef("magus job stop: requires exactly one job (`%s` lists them)", hint.LsJobs)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	stop, err := broker.StopJob(ctx, broker.DefaultAddr(), pos[0])
	if errors.Is(err, broker.ErrUnavailable) {
		return fmt.Errorf("magus job stop: no broker is running, so none recorded a process group for job %s: %w", pos[0], err)
	}
	if err != nil {
		return fmt.Errorf("magus job stop: %w", err)
	}
	switch opts.Format {
	case outputName:
		return emitNames(os.Stdout, []string{stop.Lease})
	case outputText:
		printJobStop(os.Stdout, stop)
		return nil
	default:
		return emitFormatted(opts, stop)
	}
}

// printJobStop says what a job stop signaled, released and freed.
func printJobStop(w io.Writer, stop broker.JobStop) {
	for _, g := range stop.Stopped {
		fmt.Fprintf(w, "job %s: signaled process group %d (`%s`): SIGTERM now, SIGKILL after a grace\n", stop.Lease, g.PGID, g.Command)
	}
	for _, g := range stop.Gone {
		fmt.Fprintf(w, "job %s: released process group %d (`%s`), which had already ended\n", stop.Lease, g.PGID, g.Command)
	}
	fmt.Fprintf(w, "job %s: freed %d validation seat(s)\n", stop.Lease, stop.Seats)
}
