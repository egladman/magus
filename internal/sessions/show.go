package sessions

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/safetyscissors/session"
)

// CountedName is one repeated thing and how often it appeared.
type CountedName struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// commandGroup is one program a session ran, with what today's rules say about
// the commands it ran under that name.
//
// WouldDeny counts what the rules WOULD refuse now; HostDenied counts what the host
// recorded as actually refused. The gap between them is the audit: a command the
// rules deny that ran anyway was never judged, because the guard was not wired,
// was too old to judge, or the rule arrived after the command did.
type commandGroup struct {
	Program    string   `json:"program"`
	Count      int      `json:"count"`
	Pass       int      `json:"pass"`
	Advise     int      `json:"advise"`
	WouldDeny  int      `json:"would_deny"`
	HostDenied int      `json:"host_denied"`
	Rules      []string `json:"rules,omitempty"`
}

// ShowSummary is what the loaded events of one session say. The guard trail and the
// cost of the session are joined on by the caller, which holds the packages that read
// them.
type ShowSummary struct {
	Session string `json:"session"`
	Host    string `json:"host,omitempty"`
	// Agent is the pair off the newest event that named one: what this session
	// ended on, which is what today's guard rules would be checked against. Zero
	// when no loaded event named either.
	Agent        session.Agent  `json:"agent,omitzero"`
	Events       int            `json:"events"`
	FirstMs      int64          `json:"first_ms,omitempty"`
	LastMs       int64          `json:"last_ms,omitempty"`
	ByKind       map[string]int `json:"by_kind,omitempty"`
	Commands     []commandGroup `json:"commands,omitempty"`
	Skills       []CountedName  `json:"skills,omitempty"`
	FilesRead    []CountedName  `json:"files_read,omitempty"`
	FilesWritten []CountedName  `json:"files_written,omitempty"`
	HookOutputs  int            `json:"hook_outputs"`
	Transcript   string         `json:"transcript,omitempty"`
}

// SummarizeSession folds one session's loaded events into the report.
func SummarizeSession(hostSession string, events []session.AgentEvent) ShowSummary {
	out := ShowSummary{Session: hostSession, Events: len(events), ByKind: map[string]int{}}
	byProgram := map[string]*commandGroup{}
	skills, read, written := map[string]int{}, map[string]int{}, map[string]int{}

	var pairAtMs int64
	for _, ev := range events {
		out.ByKind[ev.Kind]++
		if out.Host == "" {
			out.Host = ev.Host
		}
		if out.Transcript == "" {
			out.Transcript = ev.Transcript
		}
		if ev.AtMs > 0 {
			if out.FirstMs == 0 || ev.AtMs < out.FirstMs {
				out.FirstMs = ev.AtMs
			}
			if ev.AtMs > out.LastMs {
				out.LastMs = ev.AtMs
			}
		}
		if ev.Agent != (session.Agent{}) && ev.AtMs >= pairAtMs {
			out.Agent, pairAtMs = ev.Agent, ev.AtMs
		}
		switch ev.Kind {
		case session.EventShellCommand:
			g := byProgram[ev.Program]
			if g == nil {
				g = &commandGroup{Program: ev.Program}
				byProgram[ev.Program] = g
			}
			g.Count++
			switch ev.Verdict {
			case session.VerdictDeny:
				g.WouldDeny++
			case session.VerdictAdvise:
				g.Advise++
			default:
				g.Pass++
			}
			if ev.Denied {
				g.HostDenied++
			}
			if ev.Rule != "" && !slices.Contains(g.Rules, ev.Rule) {
				g.Rules = append(g.Rules, ev.Rule)
			}
		case session.EventSkillLoad:
			skills[ev.Text]++
		case session.EventFileRead:
			read[ev.Text]++
		case session.EventFileWrite:
			written[ev.Text]++
		case session.EventHookOutput:
			out.HookOutputs++
		}
	}

	for _, g := range byProgram {
		slices.Sort(g.Rules)
		out.Commands = append(out.Commands, *g)
	}
	slices.SortFunc(out.Commands, func(a, b commandGroup) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return strings.Compare(a.Program, b.Program)
	})
	out.Skills, out.FilesRead, out.FilesWritten = Counted(skills), Counted(read), Counted(written)
	return out
}

// Counted renders a tally most-frequent first, ties broken by name so two runs
// over one session render identically.
func Counted(tally map[string]int) []CountedName {
	out := make([]CountedName, 0, len(tally))
	for name, n := range tally {
		out = append(out, CountedName{Name: name, Count: n})
	}
	slices.SortFunc(out, func(a, b CountedName) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// showListCap bounds the file and skill lists in the text view. The tail is a
// session's whole reach, which is a different question with an answer of its own.
const showListCap = 10

// RenderShow prints the events half of `session show`, ending on the hook-output count.
func RenderShow(w io.Writer, s ShowSummary) {
	fmt.Fprintf(w, "%s  host %s  %d event(s)\n", s.Session, OrDash(s.Host), s.Events)
	if s.Agent != (session.Agent{}) {
		fmt.Fprintf(w, "model %s  host version %s\n", OrDash(s.Agent.Model), OrDash(s.Agent.HostVersion))
	}
	if s.FirstMs > 0 {
		fmt.Fprintf(w, "%s to %s\n",
			time.UnixMilli(s.FirstMs).Format("2006-01-02 15:04:05"),
			time.UnixMilli(s.LastMs).Format("2006-01-02 15:04:05"))
	}
	if s.Transcript != "" {
		fmt.Fprintf(w, "transcript %s\n", s.Transcript)
	}
	fmt.Fprintln(w)
	for _, kind := range session.EventKinds {
		if n := s.ByKind[kind]; n > 0 {
			fmt.Fprintf(w, "  %-14s %d\n", kind, n)
		}
	}
	if len(s.Commands) > 0 {
		fmt.Fprintln(w, "\nCommands by program, judged against today's rules:")
		for _, g := range s.Commands {
			fmt.Fprintf(w, "  %-12s %4d  pass %d  advise %d  deny %d  (host recorded %d denied)",
				OrDash(g.Program), g.Count, g.Pass, g.Advise, g.WouldDeny, g.HostDenied)
			if len(g.Rules) > 0 {
				fmt.Fprintf(w, "  %s", strings.Join(g.Rules, ", "))
			}
			fmt.Fprintln(w)
		}
	}
	renderCounted(w, "Skills loaded", s.Skills)
	renderCounted(w, "Files read", s.FilesRead)
	renderCounted(w, "Files written", s.FilesWritten)
	fmt.Fprintf(w, "\nhook outputs: %d\n", s.HookOutputs)
}

func renderCounted(w io.Writer, title string, items []CountedName) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s:\n", title)
	for _, item := range items[:min(len(items), showListCap)] {
		fmt.Fprintf(w, "  %4d  %s\n", item.Count, item.Name)
	}
	if extra := len(items) - showListCap; extra > 0 {
		fmt.Fprintf(w, "  and %d more; `%s` lists them all\n", extra, hint.SessionShow.With("<session>", "-o", "json"))
	}
}
