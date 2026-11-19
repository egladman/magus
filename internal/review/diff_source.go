package review

import (
	"os"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/clikit/exit"
)

// SourceKind is where the patch comes from.
type SourceKind int

const (
	SourceWorkingTree SourceKind = iota
	SourceStdin
	SourceFile
	SourceRevRange
)

// Source names a patch source and how to describe it to the reader.
type Source struct {
	Kind  SourceKind
	Path  string // for SourceFile
	Base  string // for SourceRevRange
	Head  string // for SourceRevRange
	Label string
}

// Addressable reports whether magus can name what this source contains well enough to record a
// reading of it.
//
// The working tree and a revision range are both tree states magus can re-derive and re-digest; a
// patch on stdin and a patch in a file are bytes somebody handed over, describing files that may
// not exist here at all. That is the line --ack has always drawn; it was just spelled "is this the
// working tree" back when the working tree was the only addressable source.
func (in Source) Addressable() bool {
	return in.Kind == SourceWorkingTree || in.Kind == SourceRevRange
}

// RiskBase is the revision this input's files are tiered against: HEAD for the
// working tree, the base of a range. A patch from stdin or a file has none.
func (in Source) RiskBase() string {
	switch in.Kind {
	case SourceWorkingTree:
		return "HEAD"
	case SourceRevRange:
		return in.Base
	case SourceFile, SourceStdin:
	}
	return ""
}

// revRange resolves --rev, which is written base...head the way git and the branch-overlap
// reader already spell a symmetric difference.
//
// Two dots are REFUSED rather than quietly accepted as three. They are a different question (what
// head has that base has, including everything base gained meanwhile), and a reviewer who typed the
// git spelling out of habit would get a diff padded with commits the branch author never wrote.
func revRange(rev string) (Source, error) {
	base, head, ok := strings.Cut(rev, "...")
	if !ok {
		if b, _, two := strings.Cut(rev, ".."); two {
			return Source{}, exit.Usagef("magus diff: --rev takes base...head with three dots, got %q. "+
				"Three dots is what head added since it diverged; two dots would also count everything "+
				"%s gained meanwhile, which the branch author did not write", rev, b)
		}
		return Source{}, exit.Usagef("magus diff: --rev takes a range written base...head, got %q", rev)
	}
	if base == "" || head == "" {
		return Source{}, exit.Usagef("magus diff: --rev needs both ends, got %q. "+
			"magus does not fill in a default: which branch you are comparing against is the "+
			"question, not a detail", rev)
	}
	return Source{Kind: SourceRevRange, Base: base, Head: head, Label: "the range " + rev}, nil
}

// ScopeFromArgs reads the positionals as repo-relative paths that narrow the changeset.
//
// A bare `-` is still stdin and not a path, because a file actually named "-" does not happen and
// every pipe already types it. Anything else is taken at face value: magus does NOT check the path
// exists, because a path that exists in the changeset and not in the working tree is exactly the
// case a range review is for.
func ScopeFromArgs(rest []string) ([]string, error) {
	// Checked FIRST, because git hands an external differ seven positionals and every one of
	// them is path-shaped. Once positionals are paths this no longer dead-ends on an arity
	// error, so without this the integration would silently narrow the changeset to git's
	// temp-file arguments and render nothing, which reads as "no changes" rather than as a
	// misconfiguration.
	if err := gitExternalDiffRefusal(rest); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rest))
	for _, a := range rest {
		if a == "-" {
			return nil, exit.Usagef("magus diff: `-` reads a patch on stdin, so it is a source rather than a path. " +
				"Write it as `--patch -`, and keep the positionals for the paths you want to narrow to")
		}
		if strings.HasPrefix(a, "-") {
			return nil, exit.Usagef("magus diff: %q looks like a flag magus does not have. "+
				"Positionals are paths that narrow the changeset; the changeset itself is named by "+
				"--rev, --patch, or the working tree by default", a)
		}
		out = append(out, a)
	}
	return out, nil
}

// SourceFromFlags decides which changeset is being read. Exactly one flag may name it.
//
// A ref used to be refused here with a message teaching the pipe, because swallowing one printed a
// plausible list of the reader's OWN uncommitted edits under exit 0: the worst possible failure,
// since the output looks exactly like an answer to the question they asked. --rev is that message's
// successor: the ref has a door now, so the refusal became a feature.
func SourceFromFlags(patchFlag, revFlag string) (Source, error) {
	patch, rev := strings.TrimSpace(patchFlag), strings.TrimSpace(revFlag)
	switch {
	case patch != "" && rev != "":
		return Source{}, exit.Usagef("magus diff: --patch and --rev each name a changeset, so only one may be given")
	case rev != "":
		return revRange(rev)
	case patch == "-":
		return Source{Kind: SourceStdin, Label: "a patch on stdin"}, nil
	case patch != "":
		return Source{Kind: SourceFile, Path: patch, Label: "the patch in " + patch}, nil
	default:
		return Source{Kind: SourceWorkingTree, Label: "the working tree"}, nil
	}
}

// gitExternalDiffRefusal names the mistake when magus has been wired as GIT_EXTERNAL_DIFF or
// diff.external, and points at the git-native setting that does work.
//
// git calls an external diff once PER FILE with seven arguments, because the contract is for a
// program that renders one file's diff. Almost everything magus has to say is a property of the
// whole changeset (which projects rebuild, who owns them, what it costs, what to read first),
// so honoring that contract would mean printing the report once per file or not at all.
//
// git's pager is the same integration without the mismatch: it hands over the entire diff on
// stdin, exactly once, which is an input `magus diff -` already takes. So this refuses, and
// says which line to put in the config instead. Landing here means someone tried, which makes
// it the one moment they will read the answer.
//
// GIT_DIFF_PATH_TOTAL is the signal rather than the argument count alone: git sets it on every
// external-diff call, and seven positionals are otherwise just a typo.
func gitExternalDiffRefusal(rest []string) error {
	if len(rest) != 7 || os.Getenv("GIT_DIFF_PATH_TOTAL") == "" {
		return nil
	}
	return exit.Usagef("magus diff: git invoked this as an external diff, one file at a time (%q). "+
		"magus reports on a whole changeset, so it cannot answer per file. "+
		"Use git's pager instead, which hands over the entire diff at once: "+
		"`git config pager.diff '%s'` - then plain `git diff` renders through magus, "+
		"and `git --no-pager diff` still gets you the raw patch", rest[0], hint.Diff.With("--patch", "-"))
}

// TUITerm is the terminal the viewer was handed, split by descriptor, because the viewer
// uses two of them and one probe does not answer for both.
type TUITerm struct {
	// Reads is the shared interactive gate every stepping view asks for: stdin and stderr
	// are both terminals.
	Reads bool
	// Paints is stdout, which the viewer draws the changeset on. It is a separate condition
	// because `magus diff > file` leaves stdin a keyboard, so Reads alone would let the viewer
	// try to draw into a file.
	Paints bool
}

// ok reports whether the viewer can have this terminal.
func (t TUITerm) ok() bool { return t.Reads && t.Paints }

// TUIGate is what the flags, the config and the output format say about opening the viewer.
type TUIGate struct {
	// Tui is the flag, on by default; --no-tui clears it. Enabled is the configured default.
	Tui, Enabled       bool
	Ack, Impact, Watch bool
	Baseline           string
	// Text is whether the output format is the text one.
	Text bool
}

// WantsTUI decides whether this invocation opens the viewer.
//
// The viewer is the DEFAULT at a terminal, so every condition below is a quiet FALLBACK rather
// than a refusal. Nobody asked for the viewer: a script running `magus diff -o json`, a patch
// file, a watch loop are all ordinary invocations, and erroring at them would punish a caller
// for a default they never chose. The report prints and nothing is said.
//
// --no-tui is the one explicit answer, and it wins over the config the way a flag always does.
//
// It takes the terminal as an ARGUMENT rather than probing it, which is what makes this
// testable without a pty.
func WantsTUI(g TUIGate, src Source, term TUITerm) bool {
	switch {
	case !g.Tui, !g.Enabled:
		return false
	// All three END in output the viewer has nowhere to put: a receipt count, a report, and a
	// bump. They are requests for an answer rather than for somewhere to read.
	case g.Ack, g.Impact, g.Baseline != "":
		return false
	// A patch somebody handed us is bytes about files that may not be here, and a watch loop
	// drives the terminal itself. A revision range is neither: it is a tree state magus can
	// address, which is the whole reason the viewer opens over one.
	case !src.Addressable(), g.Watch:
		return false
	case !g.Text:
		return false
	default:
		return term.ok()
	}
}
