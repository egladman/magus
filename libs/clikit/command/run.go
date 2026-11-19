package command

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/egladman/magus/libs/clikit/exit"
)

// Func is what a command does once its command line parsed. Its error decides
// the exit status, as exit.Code reads it.
type Func func(ctx context.Context, inv Invocation) error

// Streams are the standard streams a command reads and writes.
type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Invocation is one parsed command line. The flags are in their Targets.
type Invocation struct {
	Streams
	Path string   // the command as typed, without flags: "magus-utils config"
	Args []string // the operands after the flags
}

// Option configures Execute.
type Option func(*options)

type options struct {
	version string
}

// WithVersion declares --version on the root, which prints line to stdout and
// exits OK. A root flag may be an alias of it, such as -v.
func WithVersion(line string) Option {
	return func(o *options) { o.version = line }
}

// Main runs root's command line from os.Args and exits with its status. The
// context Execute gets is canceled on the first SIGINT or SIGTERM, which also
// stops the catching, so a second one kills a command that ignores it.
func Main(root Command, opts ...Option) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	code := Execute(ctx, root, os.Args[1:], Streams{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}, opts...)
	stop()
	os.Exit(code)
}

// Execute is Invoke that prints the error as "<path>: <error>" to stderr,
// unless it is help or exit.AlreadyReported says it was explained, and returns
// the exit status exit.Code gives it. It never exits the process itself.
func Execute(ctx context.Context, root Command, args []string, streams Streams, opts ...Option) int {
	path, err := Invoke(ctx, root, args, streams, opts...)
	if err != nil && !errors.Is(err, flag.ErrHelp) && !exit.AlreadyReported(err) {
		fmt.Fprintf(streams.Stderr, "%s: %v\n", path, err)
	}
	return exit.Code(err)
}

// Invoke parses args against root and runs the command they name, returning
// that command's path and its error.
//
// Flags come before operands at each level, as the flag package parses them.
// A command with Children takes its first operand as a child's name when one
// matches; with no Run, the operand must name one. -h and --help print the
// command's help to stdout and return flag.ErrHelp. A misuse returns one error
// naming the command's -h, which exits Usage. A declaration Check refuses, or a
// command with neither Run nor Children, fails before anything runs.
func Invoke(ctx context.Context, root Command, args []string, streams Streams, opts ...Option) (string, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	showVersion := false
	if o.version != "" {
		if slices.ContainsFunc(root.Flags, func(f Flag) bool { return f.Name == "version" }) {
			return root.Name, errors.New("WithVersion declares --version, which the root already declares")
		}
		root.Flags = append(slices.Clone(root.Flags), Flag{Name: "version", Kind: Bool, Default: false, Doc: "Print the version and exit", Target: &showVersion})
	}
	if err := checkRunnable(root); err != nil {
		return root.Name, err
	}

	c, path := root, root.Name
	for {
		fs := flag.NewFlagSet(path, flag.ContinueOnError)
		fs.SetOutput(streams.Stderr)
		if err := c.BindFlags(fs); err != nil {
			return path, err
		}
		fs.Usage = func() { writeHelp(streams.Stdout, c, path, fs) }
		if err := parse(fs, args, path); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return path, err
			}
			return path, exit.Usagef("%w", err)
		}
		if showVersion {
			fmt.Fprintln(streams.Stdout, o.version)
			return path, nil
		}
		args = fs.Args()
		if child, ok := childNamed(c, args); ok {
			c, path, args = child, path+" "+child.Name, args[1:]
			continue
		}
		if c.Run == nil {
			return path, exit.Usagef("%s: `%s -h` lists them", commandWant(c, args), path)
		}
		return path, c.Run(ctx, Invocation{Streams: streams, Path: path, Args: args})
	}
}

// checkRunnable is Check plus: every command has a Run or Children to
// dispatch to.
func checkRunnable(root Command) error {
	errs := Check(root)
	if root.Run == nil && len(root.Children) == 0 {
		errs = errors.Join(errs, fmt.Errorf("command:%s has neither a Run nor Children", root.Name))
	}
	Walk(root, func(path string, c Command) {
		if c.Run == nil && len(c.Children) == 0 {
			errs = errors.Join(errs, fmt.Errorf("command:%s has neither a Run nor Children", path))
		}
	})
	return errs
}

func childNamed(c Command, args []string) (Command, bool) {
	if len(args) == 0 {
		return Command{}, false
	}
	i := slices.IndexFunc(c.Children, func(child Command) bool { return child.Name == args[0] })
	if i < 0 {
		return Command{}, false
	}
	return c.Children[i], true
}

// commandWant says which command words c dispatches to, and what args gave
// instead.
func commandWant(c Command, args []string) string {
	names := make([]string, len(c.Children))
	for i, child := range c.Children {
		names[i] = child.Name
	}
	slices.Sort(names)
	want := strings.Join(names, ", ")
	if len(args) == 0 {
		return "a command is required, one of " + want
	}
	return fmt.Sprintf("unknown command %q, want one of %s", args[0], want)
}

// writeHelp prints c's help from its declaration: the usage line, Short, Long,
// the commands it dispatches to and its flags, each alias folded into the flag
// it spells.
func writeHelp(w io.Writer, c Command, path string, fs *flag.FlagSet) {
	usage := c.Usage
	if usage == "" {
		usage = path + " [flags]"
		if len(c.Children) > 0 {
			usage += " <command>"
		}
	}
	fmt.Fprintf(w, "Usage: %s\n", usage)
	for _, text := range []string{c.Short, strings.TrimSpace(c.Long)} {
		if text != "" {
			fmt.Fprintf(w, "\n%s\n", text)
		}
	}
	if len(c.Children) > 0 {
		fmt.Fprintf(w, "\nCommands:\n")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, child := range c.Children {
			fmt.Fprintf(tw, "  %s\t%s\n", child.Name, child.Short)
		}
		_ = tw.Flush()
	}
	groups, byGroup := flagGroups(c, fs)
	for _, group := range groups {
		heading := "Flags"
		if group != "" {
			heading = group
		}
		fmt.Fprintf(w, "\n%s:\n", heading)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, h := range byGroup[group] {
			name := h.Name
			if h.Value != "" {
				name += " " + h.Value
			}
			usage := h.Usage
			if h.Default != "" {
				usage += " (default " + h.Default + ")"
			}
			fmt.Fprintf(tw, "  %s\t%s\n", name, usage)
		}
		_ = tw.Flush()
	}
}

// flagGroups is fs's help in c's declared groups, ungrouped first, each alias
// folded into the line of the flag it spells as "-c, --check".
func flagGroups(c Command, fs *flag.FlagSet) ([]string, map[string][]Help) {
	declared := make(map[string]Flag, len(c.Flags))
	aliases := map[string][]string{}
	for _, f := range c.Flags {
		declared[f.Name] = f
		if f.AliasOf != "" {
			aliases[f.AliasOf] = append(aliases[f.AliasOf], dashes(f.Name)+f.Name)
		}
	}
	var groups []string
	byGroup := map[string][]Help{}
	for _, h := range FlagHelp(fs, func(name string) bool { return declared[name].AliasOf != "" }) {
		name := strings.TrimLeft(strings.TrimPrefix(h.Name, "--[no-]"), "-")
		if spellings := aliases[name]; len(spellings) > 0 {
			h.Name = strings.Join(spellings, ", ") + ", " + h.Name
		}
		group := declared[name].Group
		if _, seen := byGroup[group]; !seen {
			groups = append(groups, group)
		}
		byGroup[group] = append(byGroup[group], h)
	}
	slices.SortStableFunc(groups, func(a, b string) int {
		switch {
		case a == b:
			return 0
		case a == "":
			return -1
		case b == "":
			return 1
		}
		return 0
	})
	return groups, byGroup
}
