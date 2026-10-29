package guard

import (
	"context"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/cmdline"
	"github.com/egladman/magus/types"
)

// caller is who this call comes from, as the job store keys a binding.
func (who hookAttribution) caller() job.Caller {
	return job.Caller{Host: who.Host, Session: who.Session, Agent: who.Agent}
}

// boundJob is the caller's own record, tombstone included: for an identified caller the
// one keyed on exactly its host, session and agent, and for a caller with neither id this
// checkout's. Zero when no record answers.
func boundJob(who hookAttribution, at location) job.Binding {
	if who.caller().Identified() && at.workspace == "" {
		return job.Binding{}
	}
	return job.NewStore(at.jobs()).Binding(who.caller())
}

// resolveActingLease is the lease a call acts under. See [resolveLease].
func resolveActingLease(who hookAttribution, at location, explicit string) (string, types.LeaseSource) {
	return resolveLease(who, explicit, boundJob(who, at).Job)
}

// resolveLease resolves by job.LeaseQuery with the answer only the guard holds: the
// caller's record, bound. A tombstone's job answers too, so the verdict names the job the
// binding ended on rather than reading as unbound.
//
// Exact, with no fallback between keys. A host that reports subagents hands them their
// parent's session id, so a subagent that read its session's record would be graded as
// its parent, and a parent that read the checkout's would be graded as whichever
// identity-less caller took a job here last.
func resolveLease(who hookAttribution, explicit, bound string) (string, types.LeaseSource) {
	q := job.LeaseQuery{Flag: explicit, Claim: trail.LeaseFromEnv()}
	if who.caller().Identified() {
		q.CallerJob = bound
	} else {
		q.CheckoutJob = bound
	}
	return q.Resolve()
}

// execTarget is the job a command line takes, through `magus job exec <job>` or a client
// script's magus\job.register, or "" when it takes none.
func execTarget(command string) string {
	cmds, ok := ParseCommands(command)
	if !ok {
		return ""
	}
	for _, c := range cmds {
		if c.Name == hint.ToolClient.String() {
			if params := mcpParams(c.Args); params["op"] == jobOpRegister {
				return strings.TrimSpace(params["id"])
			}
			continue
		}
		if path.Base(c.Name) != "magus" || magusFlag(c.Args, "h") || magusFlag(c.Args, "help") {
			continue
		}
		if hint.JobExec.MatchedBy(magusSubcommandWords(c.Args)) {
			return execOperand(c.Args)
		}
	}
	return ""
}

// execOperand is the job a `magus job exec` argv names: its first word after the verb,
// with the value of every flag that takes one skipped, the verb's own `--base` included.
// magusSubcommandWords knows only the global flags, so it reads `--base <rev>`'s value as
// the job.
func execOperand(args []string) string {
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if len(a) < 2 || a[0] != '-' {
			words = append(words, a)
			continue
		}
		name, _, joined := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !joined && execFlagTakesValue(name) {
			i++
		}
	}
	if len(words) < 3 {
		return ""
	}
	return words[2]
}

// execFlagTakesValue reports whether name is a global flag or a `magus job exec` flag that
// takes a value, read from the registry the CLI parses with.
func execFlagTakesValue(name string) bool {
	if takes, known := MagusFlagTakesValue(name); known {
		return takes
	}
	for _, group := range commands.Root.Children {
		if group.Name != hint.JobExec.Head() {
			continue
		}
		for _, verb := range group.Children {
			if verb.Name != hint.JobExec.Leaf() {
				continue
			}
			i := slices.IndexFunc(verb.Flags, func(f cmdline.Flag) bool { return f.Name == name })
			return i >= 0 && verb.Flags[i].Kind != cmdline.Bool
		}
	}
	return false
}

// bindOnExec records that the caller acts under the job command takes, when it takes a
// live one its record does not already name. It runs only on a command the guard lets
// through, and is the one place a binding is made outside a spawn.
//
// The GUARD records it, not `magus job exec`: a binding is a fact about who is acting, and
// only the hook reads the host's session and agent ids. The CLI knows neither, so every
// binding it wrote was keyed on the checkout, and one subagent's exec there graded its
// parent as that subagent from the parent's next call.
//
// Whether the caller may take the job is decided before this runs: denyLeaseScopedRebind
// refuses another job while the one it holds is still declared or running.
func bindOnExec(ctx context.Context, at location, who hookAttribution, command string) {
	id := execTarget(command)
	if !types.ValidJobID(id) {
		return
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return
	}
	i := slices.IndexFunc(rows, func(row types.Job) bool { return row.ID == id })
	if i < 0 || !rows[i].State.Live() {
		return
	}
	if who.caller().Identified() && at.workspace == "" {
		return
	}
	store := job.NewStore(at.jobs())
	if store.Bound(who.caller()) != id {
		_ = store.Bind(who.caller(), id)
	}
	// A row the caller takes itself is no longer one it handed out, so its `job exit`
	// leaves the caller orchestrating only what others still hold.
	if at.cacheDir != "" {
		facts := hint.NewGate(at.cacheDir, who.factsKey())
		if st := readCostState(facts); st.forget(id) {
			writeCostState(facts, st)
		}
	}
}

// releaseOnEnd unbinds the caller from each job `job wait` or `job rm` ends,
// before the command runs. An exited row still grades its holder's writes,
// so `job exit` keeps the binding.
func releaseOnEnd(at location, who hookAttribution, command string) {
	if who.caller().Identified() && at.workspace == "" {
		return
	}
	cmds, ok := ParseCommands(command)
	if !ok {
		return
	}
	var store *job.Store
	for _, c := range cmds {
		// A help, schema or dry-run read ends nothing.
		if path.Base(c.Name) != "magus" || magusFlag(c.Args, "h", "help", "schema", "dry-run") {
			continue
		}
		words := magusSubcommandWords(c.Args)
		for _, verb := range []hint.Command{hint.JobWait, hint.JobRm} {
			if !verb.MatchedBy(words) {
				continue
			}
			operands, _ := verbArgv(c.Args, verb)
			if len(operands) == 0 || !types.ValidJobID(operands[0]) {
				continue
			}
			if store == nil {
				store = job.NewStore(at.jobs())
			}
			store.Release(who.caller(), operands[0])
		}
	}
}

// bindingUnlessEnded is b, or no binding when b names a job that ended in a verdict (pass,
// fail or no_return), for the caller to release. A wait ends a job from its orchestrator's
// session, so releaseOnEnd never sees the holder's call; the holder's own next call
// releases it instead.
//
// An exited row still holds its holder, since a rejected wait sends it back to the same
// paths, until the caller's BAGGAGE claims another job still declared or running. The
// holder filed its result, so nothing of the exited job is left to grade, and keeping the
// binding graded a resumed worker under the job it had finished. A binding to a job in
// flight still outranks the claim: a worker cannot rewrite its own grading from a shell.
func bindingUnlessEnded(ctx context.Context, at location, b job.Binding) job.Binding {
	if b.Job == "" || b.Gone {
		return b
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return b
	}
	state := func(id string) types.JobState {
		if i := slices.IndexFunc(rows, func(row types.Job) bool { return row.ID == id }); i >= 0 {
			return rows[i].State
		}
		return ""
	}
	switch held := state(b.Job); {
	case held.Terminal():
		return job.Binding{}
	case held == types.StateExited:
		if claim := trail.LeaseFromEnv(); claim != b.Job && state(claim).Editing() {
			return job.Binding{}
		}
	}
	return b
}
