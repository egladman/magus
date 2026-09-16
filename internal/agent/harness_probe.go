package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// probeTimeout bounds one guard-command probe. Matches the shipped hook templates'
// own 10s hook timeout: a script that has to load the workspace to answer needs
// that long, but verify must not hang a command or a doctor report on a wedged
// subprocess.
const probeTimeout = 10 * time.Second

// guardUnavailableNotice is the literal English magus's own shipped guard scripts
// print when they could not resolve or run a working binary. It is magus's own
// diagnostic vocabulary, authored once and reused verbatim across every template
// (see docs/guides/integrations/agents/magus-hook-command.sh and cursor-hook.sh),
// not a host's, so matching it here reads a fact the script already states rather
// than guessing at a host's reply dialect.
const guardUnavailableNotice = "magus guard is NOT running"

// The three synthetic events below are byte-identical to fixtures
// cmd/magus/testdata/script/guard_templates.txtar already uses to pin the shipped
// guard scripts end to end (deny-command.json, advise-path.json,
// cursor-shell-deny.json). They are copied rather than read from that archive,
// which is test-only source and does not ship in the binary; harness_probe_test.go
// asserts the copies stay byte-identical to the archive so a future edit to the
// shared fixtures cannot drift out from under this probe silently.
const (
	// probeDenyCommandEvent triggers the built-in whole-tree-stash deny, which is a
	// VCS-hygiene rule with no workspace declaration to satisfy: it denies in any
	// workspace, which is what makes it usable as a universal probe event.
	probeDenyCommandEvent = `{"session_id": "s1", "tool_name": "Bash", "tool_input": {"command": "git stash"}}`
	// probeAdvisePathEvent triggers the built-in cross-host-instruction-file advisory
	// (adviseMemoryWrite), matched on the bare filename AGENTS.md regardless of any
	// workspace declaration; also universal.
	probeAdvisePathEvent = `{"session_id": "s1", "tool_name": "Write", "tool_input": {"file_path": "AGENTS.md"}}`
	// probeCursorShellDenyEvent is the same universal deny, shaped for the
	// self-contained script that reads its own event envelope instead of magus's
	// generic tool_input.command convention.
	probeCursorShellDenyEvent = `{"hook_event_name": "beforeShellExecution", "conversation_id": "c1", "command": "git stash", "cwd": ".", "sandbox": false}`
)

// probeable reports whether command is a guard-shaped invocation that renders a
// verdict worth probing. A lifecycle script (observe, checkpoint, rehydrate) never
// renders one (see their own doc comments), so presence is all there is to check
// for those, and the exact-entry match VerifyHarness already ran is that check.
func probeable(command string) bool {
	switch {
	case strings.Contains(command, "magus-hook-observe.sh"),
		strings.Contains(command, "magus-checkpoint.sh"),
		strings.Contains(command, "magus-rehydrate.sh"):
		return false
	default:
		return invokesMagus(command)
	}
}

// supportsHostResponseOverride reports whether command invokes one of the two
// shared templates that build their reply from the HOST_RESPONSE environment
// variable (see the "Plain assignment, NOT ${VAR:=default}" comment in both
// files). The probe overrides it with a bare decision template so the reply is
// host-neutral BY CONSTRUCTION: magus never has to parse a host's JSON envelope
// to know what decision came back.
func supportsHostResponseOverride(command string) bool {
	return strings.Contains(command, "magus-hook-command.sh") || strings.Contains(command, "magus-hook-path.sh")
}

// probeEventFor picks the synthetic event a command's own script expects, and
// (for a script the probe can steer with HOST_RESPONSE) the exact decision that
// event must produce. A command matching neither shipped shared template is
// judged only for "it runs and answers something" (wantDecisions is nil), because
// the probe has no way to know a third-party or self-contained script's reply
// dialect. Matched on shipped script basenames, the same convention invokesMagus
// already uses: a filename is a shipped artifact, not a host.
func probeEventFor(command string) (event string, wantDecisions []string) {
	switch {
	case strings.Contains(command, "magus-hook-command.sh"):
		return probeDenyCommandEvent, []string{"deny"}
	case strings.Contains(command, "magus-hook-path.sh"):
		return probeAdvisePathEvent, []string{"advise"}
	case strings.Contains(command, "cursor-hook.sh"):
		return probeCursorShellDenyEvent, nil
	default:
		return probeDenyCommandEvent, nil
	}
}

// probeHarnessCommands actually runs every guard-shaped command the config carries
// against a synthetic event, instead of trusting that the command's mere presence
// means it works. Presence is what VerifyHarness already checked before calling
// this; this is what makes the difference between a config that WOULD have been
// reported "verified" while its guard command was silently broken, and one that
// genuinely answers.
func probeHarnessCommands(ctx context.Context, root string, config map[string]any) (HarnessStatus, string) {
	var all []string
	collectCommands(config, &all)

	seen := make(map[string]bool, len(all))
	var commands []string
	for _, c := range all {
		if seen[c] || !probeable(c) {
			continue
		}
		seen[c] = true
		commands = append(commands, c)
	}
	if len(commands) == 0 {
		// Every managed command here is a lifecycle hook with no verdict to check
		// (or the config invokes magus in a shape this probe does not recognize as
		// guard-bearing). configInvokesMagus already required at least one command
		// that invokes magus; there is nothing further this probe can add.
		return HarnessVerified, ""
	}
	slices.Sort(commands)

	if reason, ok := checkProbeEnvironment(root); !ok {
		return HarnessUnprobed, reason
	}

	var uncovered, unprobed []string
	for _, command := range commands {
		status, reason := probeOneCommand(ctx, root, command)
		switch status {
		case HarnessVerified:
		case HarnessUnprobed:
			unprobed = append(unprobed, fmt.Sprintf("%q: %s", command, reason))
		default:
			uncovered = append(uncovered, fmt.Sprintf("%q: %s", command, reason))
		}
	}
	switch {
	case len(unprobed) > 0:
		return HarnessUnprobed, strings.Join(unprobed, "; ")
	case len(uncovered) > 0:
		return HarnessUncovered, "the wired command did not answer a synthetic event: " + strings.Join(uncovered, "; ")
	default:
		return HarnessVerified, ""
	}
}

// checkProbeEnvironment answers the three ways a probe cannot run at all, BEFORE
// spawning anything: no POSIX shell, no jq (every shipped script needs it to read
// its event), or no magus binary the script could possibly resolve (its own root
// walk, then PATH). Answering this up front means a probe that cannot run says so
// exactly, rather than the ambiguous empty output a missing binary alone would
// otherwise leave behind on a script (magus-hook-path.sh) that is silent by
// design when it cannot find one.
func checkProbeEnvironment(root string) (reason string, ok bool) {
	if _, err := exec.LookPath("sh"); err != nil {
		return `the "sh" interpreter is not on PATH, so the wired guard command cannot run`, false
	}
	if _, err := exec.LookPath("jq"); err != nil {
		return "jq is not on PATH, so the wired guard command cannot read its event", false
	}
	if !hasExecutableMagus(root) {
		return "no magus binary was found in this workspace or on PATH, so the wired guard command cannot judge anything", false
	}
	return "", true
}

// hasExecutableMagus mirrors the shipped scripts' own resolution: prefer the
// workspace's own ./magus, fall back to PATH. root is already the workspace root
// VerifyHarness resolved, so no further upward walk is needed here.
func hasExecutableMagus(root string) bool {
	name := "magus"
	if runtime.GOOS == "windows" {
		name = "magus.exe"
	}
	if info, err := os.Stat(filepath.Join(root, name)); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
		return true
	}
	_, err := exec.LookPath("magus")
	return err == nil
}

// probeOneCommand actually executes command (a full shell invocation, exactly as
// a host would run it) against a synthetic event, in an isolated environment, and
// reports whether a verdict came back in the shape expected.
//
// Read-only: it neither writes into root nor touches the real user config or
// activity trail. HOME, XDG_STATE_HOME, XDG_CONFIG_HOME and TMPDIR are all
// redirected to a throwaway directory removed when the probe returns, so
// whatever `magus shell` would otherwise record (an activity event, a
// guard-notice-once marker) lands there instead of in the machine's real state.
func probeOneCommand(ctx context.Context, root, command string) (HarnessStatus, string) {
	scratch, err := os.MkdirTemp("", "magus-harness-probe-*")
	if err != nil {
		return HarnessUnprobed, "could not create an isolated probe environment: " + err.Error()
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	event, wantDecisions := probeEventFor(command)
	cmd := exec.CommandContext(probeCtx, "sh", "-c", command)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(event)
	env := append(os.Environ(),
		"HOME="+scratch,
		"XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"XDG_CONFIG_HOME="+filepath.Join(scratch, "config"),
		"TMPDIR="+scratch,
	)
	if supportsHostResponseOverride(command) {
		// HOST_ADVISE_BRANCH='' is not "suppress the advisory": it is "do not append
		// the branch this template would otherwise splice into HOST_RESPONSE",
		// because HOST_RESPONSE is now fully ours. Without clearing it, the script's
		// own `[ -n "$HOST_RESPONSE" ] ||` short-circuit already skips building the
		// branch at all; this is here for the day either template changes that.
		env = append(env, "HOST_RESPONSE={{.decision}}", "HOST_ADVISE_BRANCH=")
	}
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	combined := out.String()

	if line, ok := firstLineContaining(combined, guardUnavailableNotice); ok {
		return HarnessUnprobed, line
	}
	trimmed := strings.TrimSpace(combined)
	if runErr != nil && trimmed == "" {
		return HarnessUncovered, "the wired command failed to run: " + runErr.Error()
	}
	if trimmed == "" {
		return HarnessUncovered, "the wired command produced no output for a synthetic event expected to render a verdict"
	}
	if len(wantDecisions) > 0 && !slices.Contains(wantDecisions, trimmed) {
		return HarnessUncovered, fmt.Sprintf("the wired command answered %q, not the %q a working guard would render for this event", trimmed, wantDecisions[0])
	}
	return HarnessVerified, ""
}

func firstLineContaining(text, substr string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, substr) {
			return strings.TrimSpace(line), true
		}
	}
	return "", false
}
