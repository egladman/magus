package sessions

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/vcs"
)

// `magus session load` and `magus session show`: the read and write sides of a
// host transcript loaded into the session store.
//
// The guard's trail records what the hook SAW. A transcript records what actually
// ran, including the commands no hook was wired for, the skills a session loaded,
// and what the hook printed back. Neither store answers "did this session comply"
// alone, and the join is what makes the question answerable at all.
//
// Extraction is not here and never will be: a per-host adapter the reader owns
// turns a transcript into the stream below, exactly as `magus agent adoption`
// takes input rather than reading a host's logs. magus takes normalized events
// in its own vocabulary, so a host renaming a tool costs its reader one config
// line instead of costing magus a release.

// wireEvent is one line of the stream. The field names are the guard envelope's
// where they overlap, since an adapter author is already reading that contract.
type wireEvent struct {
	Host       string `json:"host"`
	Session    string `json:"session"`
	Ts         int64  `json:"ts"`
	Cwd        string `json:"cwd"`
	Kind       string `json:"kind"`
	Ref        string `json:"ref"`
	Text       string `json:"text"`
	Transcript string `json:"transcript"`
	// Agent is the adapter-supplied model/host-version pair, absent (or null,
	// which decodes the same as absent) when an adapter's host does not carry
	// either; see Agent.
	Agent   Agent `json:"agent,omitzero"`
	Outcome struct {
		Exit        int  `json:"exit"`
		Denied      bool `json:"denied"`
		Interrupted bool `json:"interrupted"`
	} `json:"outcome"`
}

// loadMaxLineBytes bounds one event. A hook's output is the largest thing a line
// legitimately carries and it is prose, so a line past this was produced by a
// adapter piping something else entirely.
const loadMaxLineBytes = 1 << 20

// loadHookTextCap bounds what a hook.output event stores. The text is magus's own
// prose, so the head of it identifies which advisory or denial fired; the rest is
// the same paragraph every session already has.
const loadHookTextCap = 2000

// LoadRejectsShown bounds the diagnostics a failed load prints. An adapter that
// emits one bad line emits thousands, and the first few say which field is wrong.
const LoadRejectsShown = 5

// LoadSummary is what a load reports, and the JSON shape `-o json` emits. The
// events themselves ride on it between the read and the store write, and stay out of
// the report: a load prints what it did, never the stream back.
type LoadSummary struct {
	Loaded  int `json:"loaded"`
	Deduped int `json:"deduped"`
	Dropped int `json:"dropped_other_repo"`
	// Rejects is one line per input line that was not loaded, with the reason.
	Rejects []string       `json:"rejects,omitempty"`
	ByKind  map[string]int `json:"by_kind,omitempty"`
	Store   string         `json:"store"`
	Events  []LoadEvent    `json:"-"`
	// Commands holds each event's shell command text, positionally, for the served-next
	// join and nothing else. Never stored: the text dies with this struct, which is the
	// whole point of storedEvent dropping it.
	Commands []string `json:"-"`
}

// RejudgeFunc runs a past command through today's rules and reports what they would say
// now: the program, the verdict, and the rule or advisory behind it.
type RejudgeFunc func(text string) (program, verdict, rule string)

// ReadLoadStream decodes the stream, judges what it must, and returns the events
// worth storing on the summary, with the counts and the per-line diagnostics.
//
// A rejected line does not stop the read, and neither does an overlong one. An adapter
// emitting one bad shape emits it for a whole transcript, and loading the rest is what
// lets the reader fix the adapter and re-run without losing what already worked.
func ReadLoadStream(in io.Reader, dir string, rejudge RejudgeFunc) (LoadSummary, error) {
	var summary LoadSummary
	reject := func(line int, format string, args ...any) {
		summary.Rejects = append(summary.Rejects, fmt.Sprintf("line %d: ", line)+fmt.Sprintf(format, args...))
	}

	sameRepo := repositoryScope(dir)
	// ReadSlice over a buffer one byte wider than the cap is what bounds memory: ReadString
	// would grow without limit on a line that never ends, and the overlong case has to be
	// reported and skipped, not read to completion.
	r := bufio.NewReaderSize(in, loadMaxLineBytes+1)
	for line := 1; ; line++ {
		raw, err := r.ReadSlice('\n')
		overlong := errors.Is(err, bufio.ErrBufferFull)
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = r.ReadSlice('\n')
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return summary, fmt.Errorf("magus session load: read stream: %w", err)
		}
		last := errors.Is(err, io.EOF)
		switch text := strings.TrimSpace(string(raw)); {
		case overlong:
			reject(line, "longer than any event (%d bytes)", loadMaxLineBytes)
		case text == "":
		default:
			if ev, command, reason := decodeLoadEvent(text, sameRepo, rejudge); reason != "" {
				reject(line, "%s", reason)
			} else if ev != nil {
				summary.Events = append(summary.Events, *ev)
				summary.Commands = append(summary.Commands, command)
			} else {
				summary.Dropped++
			}
		}
		if last {
			return summary, nil
		}
	}
}

// decodeLoadEvent turns one line into the event to store, and returns the shell
// command text beside it for the served-next join, "" for every other kind. A line
// that fails validation returns the reason; a well-formed line from another
// repository returns neither an event nor a reason, which is the dropped case.
func decodeLoadEvent(raw string, sameRepo func(string) bool, rejudge RejudgeFunc) (*LoadEvent, string, string) {
	var ev wireEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		return nil, "", fmt.Sprintf("not a JSON object: %v", err)
	}
	if reason := validateLoadEvent(ev); reason != "" {
		return nil, "", reason
	}
	if ev.Cwd != "" && !sameRepo(ev.Cwd) {
		return nil, "", ""
	}
	// A host names files by absolute path; graph file nodes are keyed by the path
	// inside the checkout, and every worktree of one repository shares that layout.
	// Storing the checkout-relative path is what lets the @session shard land the
	// event on a node.
	if ev.Kind == EventFileRead || ev.Kind == EventFileWrite {
		ev.Text = vcs.CheckoutRelative(ev.Text)
	}
	command := ""
	if ev.Kind == EventShellCommand {
		command = ev.Text
	}
	return &LoadEvent{Session: ev.Session, Event: storedEvent(ev, rejudge)}, command, ""
}

func validateLoadEvent(ev wireEvent) string {
	switch {
	case ev.Host == "":
		return "no host"
	case ev.Session == "":
		return "no session"
	case ev.Ref == "":
		return "no ref, so the event cannot be deduplicated"
	case !ValidEventKind(ev.Kind):
		return fmt.Sprintf("kind %q is not one of %s", ev.Kind, strings.Join(EventKinds, ", "))
	}
	if !ValidFileID(ev.Session) {
		return fmt.Sprintf("session id %q must be alphanumeric with - and _ (it names the session file)", ev.Session)
	}
	return ""
}

// storedEvent is what a wire event becomes on disk. A shell command loses its text
// here and nowhere else, so this is the one function to read when asking whether a
// command line can reach the store.
func storedEvent(ev wireEvent, rejudge RejudgeFunc) AgentEvent {
	out := AgentEvent{
		Host:        ev.Host,
		Agent:       ev.Agent,
		Kind:        ev.Kind,
		Ref:         ev.Ref,
		AtMs:        ev.Ts,
		Transcript:  ev.Transcript,
		Exit:        ev.Outcome.Exit,
		Denied:      ev.Outcome.Denied,
		Interrupted: ev.Outcome.Interrupted,
	}
	switch ev.Kind {
	case EventShellCommand:
		sum := sha256.Sum256([]byte(ev.Text))
		out.Digest = hex.EncodeToString(sum[:])
		out.Program, out.Verdict, out.Rule = rejudge(ev.Text)
	case EventHookOutput:
		out.Text = ev.Text
		if len(out.Text) > loadHookTextCap {
			out.Text = out.Text[:loadHookTextCap]
		}
	default:
		out.Text = ev.Text
	}
	return out
}

// repositoryScope reports whether a cwd belongs to the repository whose store is dir,
// memoized because a stream names a handful of checkouts across thousands of
// events and each answer costs a config read.
//
// A checkout that no longer exists identifies as its own path, so events from a
// DELETED worktree are dropped as another repository's. That is the largest known
// hole in a load: the phase 0 measurement found 45% of commands were run in
// worktrees that had since been removed. Closing it needs an identity the deleted
// path can still be resolved through, which nothing records today.
func repositoryScope(dir string) func(string) bool {
	seen := map[string]bool{}
	return func(cwd string) bool {
		if match, ok := seen[cwd]; ok {
			return match
		}
		other, err := Dir(cwd)
		match := err == nil && other == dir
		seen[cwd] = match
		return match
	}
}

// RenderLoadSummary prints what a load did.
func RenderLoadSummary(w io.Writer, s LoadSummary) {
	fmt.Fprintf(w, "loaded %d, deduped %d, dropped %d (another repository), rejected %d\n",
		s.Loaded, s.Deduped, s.Dropped, len(s.Rejects))
	for _, kind := range EventKinds {
		if n := s.ByKind[kind]; n > 0 {
			fmt.Fprintf(w, "  %-14s %d\n", kind, n)
		}
	}
	fmt.Fprintf(w, "store: %s\n", s.Store)
	if s.Loaded > 0 {
		fmt.Fprintf(w, "read one back with `%s`\n", hint.SessionShow.With("<session>"))
	}
}
