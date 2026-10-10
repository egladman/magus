package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"cli/tty"
)

var report = func() {
	fmt.Fprintln(os.Stderr, "magus: at init") // want `writes to os.Stderr directly`
}

var printUsage = func() { fmt.Fprintln(os.Stderr, "Usage: magus") }

func notices(w io.Writer) {
	fmt.Fprintln(os.Stderr, "magus: wrote 3 files")      // want `writes to os.Stderr directly`
	fmt.Fprintf(os.Stderr, "hint: %s\n", "pass --force") // want `writes to os.Stderr directly`
	fmt.Fprint((os.Stderr), "x")                         // want `writes to os.Stderr directly`
	_, _ = io.WriteString(os.Stderr, "x")                // want `writes to os.Stderr directly`
	_, _ = os.Stderr.WriteString("x")                    // want `writes to os.Stderr directly`
	_, _ = os.Stderr.Write([]byte("x"))                  // want `writes to os.Stderr directly`
	fmt.Fprintln(os.Stdout, "a result")
	fmt.Fprintln(w, "a caller's writer")
	_ = os.Stderr.Sync()
}

func printJobUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus job <verb>")
	func() { fmt.Fprintln(os.Stderr, "  ls  list jobs") }()
}

func command(args []string) {
	fs := flag.NewFlagSet("job", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus job ls")
	}
	usage := func() { fmt.Fprintln(os.Stderr, "Usage: magus job rm") }
	usage()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "magus: nothing to do") // want `writes to os.Stderr directly`
	}
	printConsoleLine(os.Stderr, "job-1") // want `hands os.Stderr to printConsoleLine`
	printConsoleLine(os.Stdout, "job-1")
	printJobHelp(os.Stderr)
	_ = newDisplay(os.Stderr)
	tty.Probe(os.Stderr)
	_ = flag.NewFlagSet("x", flag.ContinueOnError).Output()
	fs.SetOutput(os.Stderr)
	fs.Usage = printFlags
	_ = command2{Usage: func() { fmt.Fprintln(os.Stderr, "Usage: magus x") }}
}

type command2 struct{ Usage func() }

func printFlags() { fmt.Fprintln(os.Stderr, "  -q  quiet") }

func printConsoleLine(w io.Writer, id string) { fmt.Fprintln(w, "console:", id) }

func printJobHelp(w io.Writer) { fmt.Fprintln(w, "Usage: magus job") }

func newDisplay(w io.Writer) io.Writer { return w }
