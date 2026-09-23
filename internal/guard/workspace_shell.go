package guard

import (
	"path/filepath"
	"slices"

	"github.com/egladman/magus/internal/hint"
)

// WorkspaceShellRule is one additive shell rule declared by magus\guard.shell in
// the root magusfile. Rules strengthen only: they can deny or advise more, never
// disable a compiled built-in. Match criteria are judged against
// ParseCommands' resolved invocations (program + arg subset), not a raw line
// regex.
type WorkspaceShellRule struct {
	Name     string
	Decision string // "deny" or "advise"
	Program  string
	Args     []string
	Reason   string
	Dialect  string
}

// workspaceShellPrefix namespaces workspace rule ids on Verdict.Rule so the
// activity trail and doctor's recurring-guard-denials check never confuse them
// with built-ins.
const workspaceShellPrefix = "workspace:"

// matchWorkspaceShell returns the strongest workspace match for command, or an
// empty verdict. Unparseable lines fail open: workspace rules have no regex
// fallback, because a raw-line pattern is exactly the tokenizing mistake
// ParseCommands exists to retire.
//
// Among matches, the first deny in declaration order wins; otherwise the first
// advise. Callers merge with a built-in verdict via strengthenWithWorkspace.
func matchWorkspaceShell(rules []WorkspaceShellRule, command string, d Dialect) ShellVerdict {
	if len(rules) == 0 {
		return ShellVerdict{}
	}
	cmds, parsed := ParseCommandsDialect(command, d)
	if !parsed {
		return ShellVerdict{}
	}
	var advise ShellVerdict
	for _, r := range rules {
		if !workspaceShellMatches(r, cmds) {
			continue
		}
		switch r.Decision {
		case "deny":
			return ShellVerdict{
				Deny: r.Reason,
				Rule: denyRule{Name: denyRuleName(workspaceShellPrefix + r.Name)},
			}
		case "advise":
			if advise.Context == "" {
				advise = ShellVerdict{Context: r.Reason}
			}
		}
	}
	return advise
}

// workspaceShellMatches reports whether any resolved command has the rule's
// program name and carries every listed arg (order-free subset), matching the
// magusInvokes idiom rather than inventing a line regex.
func workspaceShellMatches(r WorkspaceShellRule, cmds []hint.Invocation) bool {
	prog := filepath.Base(r.Program)
	if prog == "" || prog == "." {
		return false
	}
	for _, c := range cmds {
		name := c.Name
		if filepath.Base(name) != prog && name != prog {
			continue
		}
		if len(r.Args) == 0 {
			return true
		}
		if !slices.ContainsFunc(r.Args, func(w string) bool { return !slices.Contains(c.Args, w) }) {
			return true
		}
	}
	return false
}

// strengthenWithWorkspace merges a built-in verdict with a workspace match.
// Built-in deny stands. Workspace deny escalates advise or pass. Workspace
// advise fills silence only.
func strengthenWithWorkspace(builtIn, workspace ShellVerdict) ShellVerdict {
	if builtIn.Deny != "" {
		return builtIn
	}
	if workspace.Deny != "" {
		return workspace
	}
	if builtIn.Context != "" {
		return builtIn
	}
	if workspace.Context != "" {
		return workspace
	}
	return builtIn
}

// shellDialectFromRules returns the last non-empty dialect declared on a rule
// list, or "" when none did.
func shellDialectFromRules(rules []WorkspaceShellRule) Dialect {
	var d Dialect
	for _, r := range rules {
		if r.Dialect != "" {
			d = Dialect(r.Dialect)
		}
	}
	return d
}
