package agent

import (
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/types"
)

// WriteHarnessPlan prints what one harness's host files need and the one command that merges it.
func WriteHarnessPlan(w io.Writer, plan types.HarnessInstallation) error {
	var err error
	printf := func(format string, args ...any) {
		if err == nil {
			_, err = fmt.Fprintf(w, format, args...)
		}
	}
	if plan.Current() {
		printf("%s harness: current\n", plan.ID)
	} else {
		printf("%s harness: %d file(s) to merge\n", plan.ID, len(plan.Files))
		for _, path := range slices.Sorted(maps.Keys(plan.Files)) {
			file := plan.Files[path]
			state := "missing"
			if file.Exists {
				state = "exists"
			}
			printf("  %s (%s)\n", path, state)
			for _, change := range file.Changes {
				if change.Op == types.HarnessWrite {
					printf("    write the whole file\n")
					continue
				}
				value, encErr := codec.Marshal(change.Value)
				if encErr != nil && err == nil {
					err = encErr
				}
				printf("    %s %s: %s\n", change.Op, change.Key, value)
			}
		}
		printf("apply the installation:\n  %s\n", plan.Merge)
	}
	for _, wired := range plan.Wired {
		printf("wired in %s %s:\n", wired.File, wired.Key)
		for _, entry := range wired.Entries {
			matcher, commands := EntryCommands(entry)
			if matcher == "" {
				matcher = "any"
			}
			for _, command := range commands {
				printf("  %s: %s\n", matcher, command)
			}
		}
	}
	if plan.MCPHint != "" {
		printf("mcp %s (user-owned; Magus does not write host MCP config):\n%s\n", plan.ID, plan.MCPHint)
	}
	for _, h := range plan.AgentHints {
		printf("agents %s (user-owned model choice): %s\n", plan.ID, h)
	}
	return err
}

// WriteHarnessVerificationText prints one harness's verification as the lines a person reads.
func WriteHarnessVerificationText(w io.Writer, typed HarnessVerification) error {
	_, err := fmt.Fprintf(w, "%s %s harness: %s", typed.Status, typed.ID, typed.Path)
	if err == nil && typed.Reason != "" {
		_, err = fmt.Fprintf(w, " (%s)", typed.Reason)
	}
	if err == nil {
		_, err = fmt.Fprintln(w)
	}
	if err == nil && typed.Prompt.Status != "" {
		_, err = fmt.Fprintf(w, "%s %s approval prompt", typed.Prompt.Status, typed.ID)
		if err == nil && typed.Prompt.Reason != "" {
			_, err = fmt.Fprintf(w, " (%s)", typed.Prompt.Reason)
		}
		if err == nil {
			_, err = fmt.Fprintln(w)
		}
	}
	if err == nil && typed.Setting.Status != "" {
		_, err = fmt.Fprintf(w, "%s %s settings", typed.Setting.Status, typed.ID)
		if err == nil && typed.Setting.Reason != "" {
			_, err = fmt.Fprintf(w, " (%s)", typed.Setting.Reason)
		}
		if err == nil {
			_, err = fmt.Fprintln(w)
		}
	}
	if err == nil && typed.Plugin.Status != "" {
		_, err = fmt.Fprintf(w, "%s %s plugin", typed.Plugin.Status, typed.ID)
		if err == nil && typed.Plugin.Reason != "" {
			_, err = fmt.Fprintf(w, " (%s)", typed.Plugin.Reason)
		}
		if err == nil {
			_, err = fmt.Fprintln(w)
		}
	}
	if err == nil && typed.Hooks.Status != "" {
		_, err = fmt.Fprintf(w, "%s %s hooks", typed.Hooks.Status, typed.ID)
		if err == nil && typed.Hooks.Reason != "" {
			_, err = fmt.Fprintf(w, " (%s)", typed.Hooks.Reason)
		}
		if err == nil {
			_, err = fmt.Fprintln(w)
		}
	}
	if err == nil && typed.MCP.Status != "" {
		_, err = fmt.Fprintf(w, "%s %s mcp guidance: %s", typed.MCP.Status, typed.ID, typed.MCP.Reason)
		if err == nil {
			_, err = fmt.Fprintln(w)
		}
	}
	return err
}

// VerificationFailure is the first reason `magus agent harness verify` fails one harness,
// or nil when it passes.
//
// Skills-only is not a failure: a descriptor that wires no guard at all (an
// empty config.path) has nothing to be uncovered, and treating it as one
// would make every skills-only host block `magus agent harness verify`
// forever. It must still never print as "verified"; see WriteHarnessVerificationText.
// A missing approval prompt fails even a skills-only host: the guard then refuses
// every call that needs the person's approval, and verify is where that is said.
func VerificationFailure(result HarnessVerification) error {
	switch {
	case result.Status != HarnessVerified && result.Status != HarnessSkillsOnly:
		return fmt.Errorf("magus agent harness verify: %s (%s)", result.Status, result.Reason)
	case result.Prompt.Failing():
		return fmt.Errorf("magus agent harness verify: %s approval prompt for %s (%s)", result.Prompt.Status, result.ID, result.Prompt.Reason)
	case result.Setting.Failing():
		return fmt.Errorf("magus agent harness verify: %s settings for %s (%s)", result.Setting.Status, result.ID, result.Setting.Reason)
	case result.Plugin.Failing():
		return fmt.Errorf("magus agent harness verify: %s plugin for %s (%s)", result.Plugin.Status, result.ID, result.Plugin.Reason)
	case result.Hooks.Failing():
		return fmt.Errorf("magus agent harness verify: %s hooks for %s (%s)", result.Hooks.Status, result.ID, result.Hooks.Reason)
	}
	return nil
}
