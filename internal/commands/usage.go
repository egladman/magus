package commands

import (
	"fmt"
	"io"
	"strings"
)

// The hand-written help pages below are the commands' -h text, printed as they always were;
// cmd/magus passes the stream and the flags only it binds.

// WriteBuzzUsage writes the help of `magus buzz -h`.
func WriteBuzzUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus buzz [-C dir] ... # every form below takes -C")
	fmt.Fprintln(w, "       magus buzz              # open a REPL with the magusfile loaded")
	fmt.Fprintln(w, "       magus buzz <file>       # run a script")
	fmt.Fprintln(w, "       magus buzz -            # run a script from stdin")
	fmt.Fprintln(w, "       magus buzz -e <code>    # run an inline snippet")
	fmt.Fprintln(w, "       magus buzz -t <file>... # run each file's test \"...\" {} blocks")
	fmt.Fprintln(w, "       magus buzz --check <file>...  # type-check without running")
	fmt.Fprintln(w, "       magus buzz lsp          # language server over stdio (LSP)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run Buzz source from a REPL, file, stdin, or an inline snippet. With no")
	fmt.Fprintln(w, "argument on a terminal it opens a REPL with the magusfile at cwd loaded,")
	fmt.Fprintln(w, "its targets and bindings ready; a piped or redirected stdin runs as a")
	fmt.Fprintln(w, "script. The Buzz stdlib, every magus host module (fs, os, http, markdown,")
	fmt.Fprintln(w, "...), and the magus.* namespace are available in both.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  -e <code>   execute code given on the command line; it takes the --embedded rules, so no main")
	fmt.Fprintln(w, "  -t, -test   run each file's test \"...\" {} blocks and report pass/fail; each run is")
	fmt.Fprintln(w, "              recorded as `buzz -t <file>` and its ref printed, as --record does")
	fmt.Fprintln(w, "  --record    keep what the run printed in the output store and print its ref")
	fmt.Fprintln(w, "  --check     parse and type-check the named files without running them")
	fmt.Fprintln(w, "  --profile   print where compile and import time went, after the script runs")
	fmt.Fprintln(w, "  --embedded  relax upstream strictness (top-level statements, optional")
	fmt.Fprintln(w, "              argument labels) to match the magusfile engine")
	fmt.Fprintln(w, "  --no-autoload  start the REPL without executing the magusfile")
	fmt.Fprintln(w, "  -C <dir>    change to dir before anything else, as go -C does; script")
	fmt.Fprintln(w, "              paths and imports resolve from it (lsp: only as the first flag)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Parsing is upstream-strict by default. A file written for the magusfile")
	fmt.Fprintln(w, "engine needs --embedded, or it fails on rules upstream Buzz enforces and")
	fmt.Fprintln(w, "magus does not (most often: \"argument N must be labeled\").")
}

// WriteSessionUsage writes the help of `magus session -h`.
func WriteSessionUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus session [ls] [--all] [--limit <n>] [--since <when>]")
	fmt.Fprintln(w, "       magus session --brief             # this checkout's state, for a session that lost its history")
	fmt.Fprintln(w, "       magus session show <session-id>")
	fmt.Fprintln(w, "       magus session hints               # uptake per hint id, over the loaded sessions")
	fmt.Fprintln(w, "       magus session load [--file <path>]")
	fmt.Fprintln(w, "       magus session attention [flags]")
	fmt.Fprintln(w, "       magus session dispose <id> [-reason <text>]")
	fmt.Fprintln(w, "       magus session advise <text> [--expect <command prefix>]")
	fmt.Fprintln(w, "       magus session advisories [--all]")
	fmt.Fprintln(w, "       magus session retract <id>")
	fmt.Fprintln(w, "       magus session checkpoint [--note <text>]")
	fmt.Fprintln(w, "       magus session notify [flags]    # machine: event ingest, wired by agent hosts")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "One store, two sides. Humans read it: `session` lists what recent magus")
	fmt.Fprintln(w, "invocations did in this checkout (--all: every checkout of this repository),")
	fmt.Fprintln(w, "each with the host session it ran in when a host delivered one; `session show` reports one host")
	fmt.Fprintln(w, "session in full, `session attention` lists the blocks agents raised, and")
	fmt.Fprintln(w, "`session dispose` closes one - nothing closes a request automatically.")
	fmt.Fprintln(w, "Humans write it too: `session checkpoint` records where work stands before")
	fmt.Fprintln(w, "you put it down. Agent hosts write it from hooks, through `session notify`,")
	fmt.Fprintln(w, "and `session load` takes a normalized event stream extracted from a host's")
	fmt.Fprintln(w, "own transcript.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "`session --brief` answers the same question the listing opens with, for a")
	fmt.Fprintln(w, "session rather than a person: where this checkout stands, read off disk, for")
	fmt.Fprintln(w, "a model whose history a host replaced with a summary.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run `magus session <subcommand> -h` for each subverb's flags.")
}

// WriteRootUsage writes the help of `magus -h`; flags prints the root flags the binary binds.
func WriteRootUsage(w io.Writer, flags func(io.Writer)) {
	fmt.Fprintln(w, "Usage: magus [flags] <subcommand> [args]")
	group := ""
	for _, c := range Root.Spec.Children {
		if c.Group != group {
			group = c.Group
			fmt.Fprintf(w, "\n%s:\n", group)
		}
		fmt.Fprintf(w, "  %-14s %s\n", c.Name, c.Short)
	}
	fmt.Fprintf(w, "  %-14s %s\n", Help.Name, Help.Short)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Global flags (work before or after the subcommand):")
	fmt.Fprintln(w, "  --help, -h           show help (top-level or subcommand)")
	fmt.Fprintln(w, "  -q, --quiet          suppress progress; only print errors + dump failing project output")
	fmt.Fprintln(w, "  -s, --silent         like -q, but bound failing dumps (tail + log path) and bubble up only 'magus:notice:' lines")
	flags(w)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run `magus <subcommand> -h` for subcommand-specific flags.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Config file: magus.yaml (CWD or $XDG_CONFIG_HOME/magus/)")
	fmt.Fprintln(w, "Env vars: MAGUS_* (see magus help for the full list).")
}

// WriteNotesUsage writes the help of `magus notes -h`.
func WriteNotesUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus notes <subcommand> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Read the workspace's human-authored notes: prose a person wrote about the code,")
	fmt.Fprintln(w, "anchored to graph entities but derived from none of them. Notes are committed to")
	fmt.Fprintln(w, "the repository, so git attributes them and review sees them.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  ls       show notes and any repair warnings")
	fmt.Fprintln(w, "  get      show one note")
	fmt.Fprintln(w, "  edit     open one note in $VISUAL or $EDITOR (creates it if absent)")
	fmt.Fprintln(w, "  verify   check malformed notes and anchors that no longer resolve")
	fmt.Fprintln(w, "  capture  keep the review conversation, yours and the host's, as a note")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Set knowledge.notes.shared (in the repo, your team gets it) or knowledge.notes.private")
	fmt.Fprintln(w, "(anywhere, this machine only) in magus.yaml to enable a store. There is no `put`:")
	fmt.Fprintln(w, "notes are written by people, in an editor, and committed under their own name.")
	fmt.Fprintln(w, "`capture` is not an exception to that - it takes a conversation that already")
	fmt.Fprintln(w, "happened and quotes it, and the note it writes says so in its own frontmatter.")
}

// WriteSessionLoadUsage writes the help of `magus session load -h`; kinds are the event kinds a line may carry, and flags prints the command's own flags.
func WriteSessionLoadUsage(w io.Writer, kinds []string, flags func()) {
	fmt.Fprintln(w, "Usage: magus session load [--file <path>]   # the event stream arrives on stdin")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Load a normalized agent-session event stream into this repository's session")
	fmt.Fprintln(w, "store: one JSON object per line, host vocabulary already mapped onto magus's.")
	fmt.Fprintf(w, "Every line needs host, session, kind and ref; kind is one of %s.\n", strings.Join(kinds, ", "))
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Events are keyed on (host, session, kind, ref), so re-running an adapter over the same")
	fmt.Fprintln(w, "transcript loads nothing twice. Events whose cwd belongs to another repository")
	fmt.Fprintln(w, "are dropped; worktrees of this one are kept.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "A shell command's text is never stored. It is re-judged against today's guard")
	fmt.Fprintln(w, "rules and kept as its program, the verdict, the rule behind it, and a digest.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags (global flags also accepted, see `magus -h`):")
	flags()
}

// WriteJobGraphUsage writes the help of `magus job graph -h`; flags prints the command's own flags.
func WriteJobGraphUsage(w io.Writer, flags func()) {
	fmt.Fprintln(w, "Usage: magus job graph [-o svg] [--dir <dir>] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Draw the live jobs as a plan: one box per job, an edge per fork,")
	fmt.Fprintln(w, "merge and declared dependency, each box tagged with its state and")
	fmt.Fprintln(w, "naming its holder. A plan larger than one figure holds splits into an")
	fmt.Fprintln(w, "overview, one box per group of jobs, and a figure per group.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Without -o it prints each figure's boxes and edges. -o svg writes")
	fmt.Fprintln(w, "each figure to <id>.svg in --dir and prints one path per line,")
	fmt.Fprintln(w, "the overview first. The same plan writes the same bytes.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Example:")
	fmt.Fprintln(w, "  magus job graph -o svg --dir /tmp/plan")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags (global flags also accepted, see `magus -h`):")
	flags()
}

// WriteGraphUsage writes the help of `magus graph -h`.
func WriteGraphUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus graph <subcommand> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "The workspace's graphs as objects: emit, export, and measure them.")
	fmt.Fprintln(w, "(query/explain/path read the knowledge graph; graph is the graph itself.)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  build    rebuild the knowledge graph now, reindexing code symbols (runs each project's scip op)")
	fmt.Fprintln(w, "  push     publish the graph to a container registry as an OCI artifact")
	fmt.Fprintln(w, "  pull     fetch a published graph; a public one needs no credentials")
	fmt.Fprintln(w, "  deps     project dependency DAG (-o text|json|yaml|dot|tree)")
	fmt.Fprintln(w, "  export   merged knowledge graph (-o json|graphml; --select for a dot neighborhood)")
	fmt.Fprintln(w, "  stats    knowledge-graph shape: god nodes, orphans, doc coverage (--kind to scope)")
	fmt.Fprintln(w, "  diff     nodes/edges added/removed/changed vs a baseline export or --rev; PR blast-radius")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "See also: magus query/explain/path (read the graph).")
}

// WriteSessionHintsUsage writes the help of `magus session hints -h`; lookahead is how many calls a hint looks ahead, and flags prints the command's own flags.
func WriteSessionHintsUsage(w io.Writer, lookahead int, flags func()) {
	fmt.Fprintln(w, "Usage: magus session hints")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Per hint id: how many times it was served, how often the command was run")
	fmt.Fprintf(w, "within the next %d calls, how often another magus verb was run instead, and\n", lookahead)
	fmt.Fprintln(w, "how often the same command was repeated.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "It reads the sessions this repository has loaded, so a store with no")
	fmt.Fprintln(w, "transcripts in it reports nothing. Load one with `magus session load`.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags (global flags also accepted, see `magus -h`):")
	flags()
}
