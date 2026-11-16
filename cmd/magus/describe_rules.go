package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/types"
)

// `magus describe rules` answers "what does this workspace enforce", which had no answer
// before it: a rule announced itself only by firing, so the only way to learn the set was
// to trip each one.
//
// Folded into describe rather than given to `magus shell --rules`, because describe is
// already the verb for "define a concept and list every entity of that kind" over eleven
// nouns. A second catalog on the command whose job is judging one line is exactly
// the growth this workspace's fold rule exists to prevent.

// describeRules lists the catalog, or details one rule when named.
//
// Parsed through commandParse like every other describe noun, so `-o json` and the rest of
// the display set work after the noun exactly as they do on `describe targets`. A
// hand-rolled loop here accepted the flags nowhere and made this the one noun that lied
// about its flags.
func describeRules(ctx context.Context, args []string) error {
	names, err := commandParse("describe rules", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe rule[s] [<name>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The guard rules this workspace enforces: what each one catches, the")
			fmt.Fprintln(os.Stderr, "tier magus compiles in, and what magus\\guard.builtins sets instead. A")
			fmt.Fprintln(os.Stderr, "verdict names its rule in brackets; pass that name to detail one.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	if len(names) > 1 {
		return exit.Usagef("magus describe rule: names one rule (got %d); the bare noun lists them all", len(names))
	}
	declared, loadErr := declaredBuiltins()
	if len(names) == 0 {
		rules := guard.Rules()
		for i := range rules {
			rules[i].Workspace = declared[rules[i].Name]
		}
		return errors.Join(emitRuleList(opts, rules), loadErr)
	}
	doc, ok := guard.Rule(names[0])
	if !ok {
		// Names the nearest instead of only refusing: a reader typing this has a rule
		// name off a verdict, and the ways to get it wrong are a typo and a plural.
		slog.ErrorContext(ctx, fmt.Sprintf("no rule named %q", names[0]), attr.Notice(""), attr.Component("magus describe rule"))
		if near := hint.Nearest(names[0], guard.RuleNames()); near != "" {
			slog.InfoContext(ctx, fmt.Sprintf("did you mean %q?", near), attr.Notice(""))
		}
		slog.InfoContext(ctx, fmt.Sprintf("`%s` lists every rule this workspace enforces", hint.DescribeRules), attr.Notice(""))
		return exit.Reported(exit.Usage)
	}
	doc.Workspace = declared[doc.Name]
	return errors.Join(emitRuleDetail(opts, doc), loadErr)
}

// declaredBuiltins is what the cwd workspace's root magusfile sets through
// magus\guard.builtins, rendered per rule, and empty outside a workspace or when it sets
// nothing. A magusfile whose guard declarations do not load is an error, returned for the
// caller to report after the catalog: the defaults are still worth printing, and a
// WORKSPACE column left empty without saying why would read as "this workspace sets
// nothing".
func declaredBuiltins() (map[string]string, error) {
	rules, err := loadGuardRules(context.Background(), "")
	if err != nil {
		return map[string]string{}, fmt.Errorf("magus describe rules: the root magusfile's guard declarations do not load, so no workspace setting is shown: %w", err)
	}
	if rules == nil {
		return map[string]string{}, nil
	}
	return rules.DeclaredBuiltinSettings(), nil
}

// emitRuleList renders the catalog as a table, denies first.
func emitRuleList(opts output.Options, rules []types.RuleDoc) error {
	// -o name is one id per line on every other describe noun, and a caller piping this
	// into a loop wants the names rather than the table.
	if opts.Format == outputName {
		return emitItemNames(os.Stdout, rules, func(r types.RuleDoc) string { return r.Name })
	}
	if opts.Format != outputText {
		return writeFormatted(os.Stdout, opts, rules)
	}
	return guard.WriteRuleTable(os.Stdout, rules)
}

// emitRuleDetail renders one rule in the requested format.
func emitRuleDetail(opts output.Options, doc types.RuleDoc) error {
	if opts.Format == outputName {
		return emitNames(os.Stdout, []string{doc.Name})
	}
	if opts.Format != outputText {
		return writeFormatted(os.Stdout, opts, doc)
	}
	guard.WriteRuleDetail(os.Stdout, doc)
	return nil
}
