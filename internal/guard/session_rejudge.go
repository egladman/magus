package guard

import (
	"context"
	"sync"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/project"
)

// NewRejudge judges by the built-in catalog and the shell rules of the workspace found from
// root, loaded once, on the first command. loadRules reads those rules.
//
// It calls the pure rule set rather than the hook, so no subprocess runs and no
// live workspace state is read. The rules that DO read the filesystem (the sibling
// checkout, the stale binary notice) are deliberately skipped: they describe the
// machine at the moment of the call, and applying today's machine to a command
// from three weeks ago would be inventing a verdict nobody ever saw.
//
// The rule's argument is dropped along with the text: it renders the resolved
// argv, which is the content this whole path exists to keep out of the store.
func NewRejudge(ctx context.Context, root string, loadRules func(ctx context.Context, root string) ([]WorkspaceShellRule, Dialect)) sessions.RejudgeFunc {
	// Same catalog + workspace bash rules the live hook uses; Inspect and cache
	// facts are unused on this pure command path.
	deps := sync.OnceValue(func() Dependencies {
		shellRules, shellDialect := loadRules(ctx, root)
		return Dependencies{
			Spells:       project.DefaultSpellRegistry().All,
			ShellRules:   shellRules,
			ShellDialect: shellDialect,
			Builtins:     BuiltinSettings(ctx, nil, root),
		}
	})
	return func(text string) (program, verdict, rule string) {
		program = commandProgram(text)
		v := Evaluate(deps(), text)
		switch {
		case v.Deny != "":
			return program, sessions.VerdictDeny, v.RuleName()
		case v.Context != "":
			return program, sessions.VerdictAdvise, v.AdvisoryKind()
		}
		return program, sessions.VerdictPass, ""
	}
}

// commandProgram names the program a line runs, wrappers already peeled. A line the
// shell parser cannot read falls back to the trail's reducer, which skips the VAR=value
// prefix a credential is likeliest to sit in rather than storing it as the program.
func commandProgram(text string) string {
	if cmds, parsed := ParseCommands(text); parsed && len(cmds) > 0 {
		return cmds[0].Name
	}
	return trail.CommandProgram(text)
}
