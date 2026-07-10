package journal

// Trigger values for [Command.Trigger] - how a run was spawned (lineage).
const (
	TriggerRun      = "run"
	TriggerAffected = "affected"
	TriggerCI       = "ci"
	TriggerX        = "x"
	TriggerWatch    = "watch"
	TriggerDirect   = "direct"
)

// Command is the invoking command line and context, captured at the CLI edge so the viewer
// can show a run's lineage - what was asked of magus.
type Command struct {
	Verb    string   `json:"verb"`              // the subcommand: run, affected, ci, x, watch, ...
	Args    []string `json:"args,omitempty"`    // the argument vector after the verb
	Cwd     string   `json:"cwd,omitempty"`     // directory the command was invoked in
	Trigger string   `json:"trigger,omitempty"` // one of the Trigger* constants
}

// Invocation is one magus command, launch to exit - the thing that produces a journal of
// events. It is not stored on its own: it is reconstructed from the stream's lifecycle
// events (the started event carries the command, version, and start time; the finished
// event carries the end time and overall outcome), and projected onto the wire as
// magus.viewer.v1.Invocation.
type Invocation struct {
	ID           string  `json:"id"`
	Command      Command `json:"command"`
	StartedMs    int64   `json:"started_ms"`            // unix milliseconds
	FinishedMs   int64   `json:"finished_ms,omitempty"` // unix milliseconds; 0 while running
	Status       string  `json:"status,omitempty"`      // overall outcome (pass|fail), from the finished event
	MagusVersion string  `json:"magus_version,omitempty"`
}

// InvocationFromEvents reconstructs a run's header from its event stream, reading the two
// lifecycle events that bracket it: the started event (the first event) supplies the
// command, magus version, and start time; the finished event supplies the end time and
// overall outcome. id names the invocation (its log file's basename). This is the read side
// of folding the run's identity into the stream - there is no separate metadata file to
// parse. If the stream has no finished event (an interrupted run), the finish time falls
// back to the last event's timestamp, and FinishedMs stays 0 only for an empty stream.
func InvocationFromEvents(id string, events []Event) Invocation {
	inv := Invocation{ID: id}
	for _, e := range events {
		switch e.Kind {
		case KindStarted:
			inv.StartedMs = e.Ts
			inv.MagusVersion = e.MagusVersion
			if e.Command != nil {
				inv.Command = *e.Command
			}
		case KindFinished:
			inv.FinishedMs = e.Ts
			inv.Status = e.Status
		}
	}
	if inv.FinishedMs == 0 && len(events) > 0 {
		inv.FinishedMs = events[len(events)-1].Ts
	}
	return inv
}
