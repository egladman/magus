package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/log/attr"
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
// Parsed through cmdParse like every other describe noun, so `-o json` and the rest of
// the display set work after the noun exactly as they do on `describe targets`. A
// hand-rolled loop here accepted the flags nowhere and made this the one noun that lied
// about its flags.
func describeRules(ctx context.Context, args []string) error {
	names, err := cmdParse("describe rules", args, func(fs *flag.FlagSet) {
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
		return usagef("magus describe rule: names one rule (got %d); the bare noun lists them all", len(names))
	}
	declared, loadErr := declaredBuiltins()
	if len(names) == 0 {
		rules := guard.Rules()
		for i := range rules {
			rules[i].Workspace = workspaceSetting(declared, rules[i].Name)
		}
		return errors.Join(emitRuleList(opts, rules), loadErr)
	}
	doc, ok := guard.Rule(names[0])
	if !ok {
		// Names the nearest instead of only refusing: a reader typing this has a rule
		// name off a verdict, and the ways to get it wrong are a typo and a plural.
		slog.ErrorContext(ctx, fmt.Sprintf("no rule named %q", names[0]), attr.Notice(""), attr.Component("magus describe rule"))
		if near := hint.Nearest(names[0], ruleNames()); near != "" {
			slog.InfoContext(ctx, fmt.Sprintf("did you mean %q?", near), attr.Notice(""))
		}
		slog.InfoContext(ctx, fmt.Sprintf("`%s` lists every rule this workspace enforces", hint.DescribeRules), attr.Notice(""))
		return errSilent{exitCode: 2}
	}
	doc.Workspace = workspaceSetting(declared, doc.Name)
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

// workspaceSetting renders the setting declared for name for RuleDoc.Workspace.
func workspaceSetting(declared map[string]string, name string) string {
	return declared[name]
}

// appliedDecision is the decision the working tree gives r: its workspace setting's, or
// the compiled one when it sets none.
func appliedDecision(r types.RuleDoc) string {
	if r.Workspace == "" {
		return r.Decision
	}
	decision, _, _ := strings.Cut(r.Workspace, ",")
	return decision
}

// emitRuleList renders the catalog as a table, denies first.
func emitRuleList(opts OutputOptions, rules []types.RuleDoc) error {
	// -o name is one id per line on every other describe noun, and a caller piping this
	// into a loop wants the names rather than the table.
	if opts.Format == FormatName {
		for _, r := range rules {
			fmt.Println(r.Name)
		}
		return nil
	}
	if opts.Format != FormatText {
		return writeFormatted(os.Stdout, opts, rules)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DEFAULT\tWORKSPACE\tRULE\tCATCHES")
	counts := map[string]int{}
	for _, r := range rules {
		counts[appliedDecision(r)]++
		workspace := r.Workspace
		if workspace == "" {
			workspace = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Decision, workspace, r.Name, r.Catches)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Printf("\n%d refuse, %d explain, %d off. One rule: `%s`\n",
		counts["deny"], counts["advise"], counts["off"],
		hint.DescribeRule.With("<rule>"))
	return nil
}

// emitRuleDetail renders one rule.
//
// Deliberately short. The long rationale a verdict used to carry belongs in the rule's
// docs page, not here: this is the answer to "what is this thing", asked by someone who
// just read the slug off a refusal.
func emitRuleDetail(opts OutputOptions, doc types.RuleDoc) error {
	if opts.Format == FormatName {
		fmt.Println(doc.Name)
		return nil
	}
	if opts.Format != FormatText {
		return writeFormatted(os.Stdout, opts, doc)
	}
	if doc.Workspace == "" {
		fmt.Printf("%s (%s)\n\n", doc.Name, doc.Decision)
	} else {
		fmt.Printf("%s (%s; default %s)\n\n", doc.Name, doc.Workspace, doc.Decision)
	}
	fmt.Println("catches")
	fmt.Println("  " + doc.Catches)
	fmt.Println()
	if doc.Why != "" {
		fmt.Println("why")
		tty.Prose(os.Stdout, tty.SystemProbe, doc.Why)
		fmt.Println()
	}
	switch appliedDecision(doc) {
	case "off":
		fmt.Println("This workspace turns the rule off, so it says nothing.")
		return nil
	case "deny":
		fmt.Println("A deny blocks the call and names what to run instead. Nothing magus")
		fmt.Println("refuses is a capability it removes: every one has a covered equivalent.")
		return nil
	}
	fmt.Println("An advisory blocks nothing. It attaches context and is held to one")
	fmt.Println("firing per session, so a repeat says only the short form.")
	return nil
}

// ruleNames is the catalog's names, for the near-miss suggestion.
func ruleNames() []string {
	rules := guard.Rules()
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Name)
	}
	return out
}
