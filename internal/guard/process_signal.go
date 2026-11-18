package guard

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

const denyRuleProcessSignal denyRuleName = "process-signal"

// Signal-list flags print documentation even when followed by a signal name.
// After -- they are operands, so `kill -- -l` still sends a signal.
func signalList(c hint.Invocation) bool {
	for _, arg := range c.Args {
		if arg == "--" {
			break
		}
		if slices.Contains([]string{"-l", "-L", "--list", "--table"}, arg) {
			return true
		}
	}
	return false
}

func processProbe(c hint.Invocation) bool {
	if path.Base(c.Name) != "kill" || signalList(c) {
		return false
	}
	for i, arg := range c.Args {
		if arg == "--" {
			break
		}
		if arg == "-0" || arg == "--signal=0" {
			return true
		}
		if (arg == "-s" || arg == "-n" || arg == "--signal") && i+1 < len(c.Args) {
			return c.Args[i+1] == "0"
		}
	}
	return false
}

func processSignalFires(cmds []hint.Invocation) bool {
	for _, c := range cmds {
		switch path.Base(c.Name) {
		case "kill":
			if len(c.Args) > 0 && !signalList(c) && !processProbe(c) {
				return true
			}
		case "pkill", "killall":
			if len(c.Args) > 0 {
				return true
			}
		}
	}
	return false
}

var processSignalVerdict = ShellVerdict{
	Deny: "a PID or process-name pattern does not prove you own the process; use the host's cancel action for the shell task you started, or ask the person to stop it.",
	Why:  "A broad pkill can terminate another session's fork or the host-managed MCP connection. A slow or locked call is not permission to signal its holder.",
	Rule: denyRule{Name: denyRuleProcessSignal},
}.withRemedy(
	"a PID or name does not prove ownership; cancel your shell task through its host, or ask the person to stop it. Inspect the tracked work once.",
	hint.NextForDenyRemedy(string(denyRuleProcessSignal), hint.ServerStatus.Argv(),
		"status reads the live pool once and exits; it does not stop any process."),
)

// recordedGroups reads the process groups this user's broker recorded, each with the job
// it runs for. A var so a test stands groups in without a broker.
var recordedGroups = func(ctx context.Context) ([]broker.Group, error) {
	ctx, cancel := context.WithTimeout(ctx, budgetReadTimeout)
	defer cancel()
	r, err := broker.QueryWork(ctx, broker.DefaultAddr())
	return r.Groups, err
}

// processGroup is the process group process pid is in. A var so a test needs no process.
var processGroup = broker.ProcessGroup

// signalTargets is each process and each process group the kill commands in cmds name. A
// dash operand is a group once the signal is given or after --, as kill reads it.
func signalTargets(cmds []hint.Invocation) (pids, groups []int) {
	for _, c := range cmds {
		if path.Base(c.Name) != "kill" {
			continue
		}
		operands, signaled := false, false
		for i := 0; i < len(c.Args); i++ {
			arg := c.Args[i]
			switch {
			case !operands && arg == "--":
				operands = true
			case !operands && (arg == "-s" || arg == "-n" || arg == "--signal"):
				signaled = true
				i++
			case strings.HasPrefix(arg, "-"):
				n, err := strconv.Atoi(arg[1:])
				if err == nil && n > 0 && (operands || signaled) {
					groups = append(groups, n)
				}
				signaled = true
			default:
				if n, err := strconv.Atoi(arg); err == nil && n > 0 {
					pids = append(pids, n)
				}
			}
		}
	}
	return pids, groups
}

// processSignalDenial is the process-signal denial for cmds. When a process or group
// they signal is in a group the broker recorded for a job, the remedy is `magus job stop`
// for that job, which proves what a PID cannot.
func processSignalDenial(ctx context.Context, cmds []hint.Invocation) ShellVerdict {
	pids, pgids := signalTargets(cmds)
	if len(pids)+len(pgids) == 0 {
		return processSignalVerdict
	}
	recorded, err := recordedGroups(ctx)
	if err != nil {
		v := processSignalVerdict
		if !errors.Is(err, broker.ErrUnavailable) {
			v.Why += fmt.Sprintf(" magus could not read the process groups its broker recorded for jobs, so it cannot say whether `%s` would stop one: %v.", hint.JobStop.With("<job>"), err)
		}
		return v
	}
	for _, pid := range pids {
		if pgid, err := processGroup(pid); err == nil {
			pgids = append(pgids, pgid)
		}
	}
	pgids = append(pgids, pids...)
	var leases []string
	for _, g := range recorded {
		if g.Lease != "" && slices.Contains(pgids, g.PGID) && !slices.Contains(leases, g.Lease) {
			leases = append(leases, g.Lease)
		}
	}
	if len(leases) == 0 {
		return processSignalVerdict
	}
	slices.Sort(leases)
	v := processSignalVerdict
	v.Deny = fmt.Sprintf("that process is in a group the broker recorded for job %s; a PID does not prove it is still that group, and `%s` does.",
		strings.Join(leases, ", "), hint.JobStop.With(leases[0]))
	next := make([]types.NextStep, 0, len(leases))
	for _, lease := range leases {
		next = append(next, hint.NextForDenyRemedy(string(denyRuleProcessSignal), hint.JobStop.Argv(lease),
			"job stop signals only the groups the broker recorded for the job, after checking each is still that group, then frees their seats."))
	}
	return v.withRemedy("stop the job's recorded process groups through the broker rather than by PID.", next...)
}
