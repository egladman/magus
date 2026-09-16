package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
)

// `magus describe rules` answers "what does this workspace enforce", which had no answer
// before it: a rule announced itself only by firing, so the only way to learn the set was
// to trip each one.
//
// Folded into describe rather than given to `magus shell --rules`, because describe is
// already the verb for "define a concept and list every entity of that kind" over eleven
// nouns. A second catalog surface on the command whose job is judging one line is exactly
// the surface growth this workspace's fold rule exists to prevent.

// describeRules lists the catalog, or details one rule when named.
//
// Parsed through cmdParse like every other describe noun, so `-o json` and the rest of
// the display set work after the noun exactly as they do on `describe targets`. A
// hand-rolled loop here accepted the flags nowhere and made this the one noun that lied
// about the surface.
func describeRules(args []string) error {
	names, err := cmdParse("describe rules", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe rule[s] [<name>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The guard rules this workspace enforces: what each one catches, and")
			fmt.Fprintln(os.Stderr, "which tier it lands on. A verdict names its rule in brackets; pass that")
			fmt.Fprintln(os.Stderr, "name to detail one.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	switch len(names) {
	case 0:
		return emitRuleList(opts, guard.Rules())
	case 1:
		doc, ok := guard.Rule(names[0])
		if !ok {
			// Names the nearest instead of only refusing: a reader typing this has a rule
			// name off a verdict, and the ways to get it wrong are a typo and a plural.
			fmt.Fprintf(os.Stderr, "magus describe rule: no rule named %q\n", names[0])
			if near := hint.Nearest(names[0], ruleNames()); near != "" {
				fmt.Fprintf(os.Stderr, "did you mean %q?\n", near)
			}
			fmt.Fprintf(os.Stderr, "`%s` lists every rule this workspace enforces\n", hint.DescribeRules)
			return errSilent{exitCode: 2}
		}
		return emitRuleDetail(opts, doc)
	default:
		return usagef("magus describe rule: names one rule (got %d); the bare noun lists them all", len(names))
	}
}

// emitRuleList renders the catalog as a table, denies first.
func emitRuleList(opts OutputOptions, rules []guard.RuleDoc) error {
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
	fmt.Fprintln(w, "DECISION\tRULE\tCATCHES")
	denies := 0
	for _, r := range rules {
		if r.Decision == "deny" {
			denies++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.Decision, r.Name, r.Catches)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Printf("\n%d refuse, %d explain. One rule: `%s`\n",
		denies, len(rules)-denies, hint.DescribeRule.With("<rule>"))
	return nil
}

// emitRuleDetail renders one rule.
//
// Deliberately short. The long rationale a verdict used to carry belongs in the rule's
// docs page, not here: this is the answer to "what is this thing", asked by someone who
// just read the slug off a refusal.
func emitRuleDetail(opts OutputOptions, doc guard.RuleDoc) error {
	if opts.Format == FormatName {
		fmt.Println(doc.Name)
		return nil
	}
	if opts.Format != FormatText {
		return writeFormatted(os.Stdout, opts, doc)
	}
	fmt.Printf("%s (%s)\n\n", doc.Name, doc.Decision)
	fmt.Println("catches")
	fmt.Println("  " + doc.Catches)
	fmt.Println()
	if doc.Decision == "deny" {
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
