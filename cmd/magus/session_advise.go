package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
)

// directiveStore opens the advisories store of the repository root names.
func directiveStore(root string) (sessions.DirectiveStore, error) {
	root, err := attentionRoot(root)
	if err != nil {
		return sessions.DirectiveStore{}, err
	}
	return sessions.OpenDirectiveStore(root)
}

// requirePerson refuses verb without an interactive terminal. The guard denies
// it to an agent too, but fails open where no guard is wired.
func requirePerson(verb string) error {
	if isInteractiveTTY() {
		return nil
	}
	slog.ErrorContext(context.Background(), fmt.Sprintf("session %s records a person's words, so it needs an interactive terminal", verb),
		attr.Notice(""), attr.Component("magus"))
	return exit.Reported(exit.Usage)
}

func adviseCommand(root string, args []string) error {
	var flags *flaggen.SessionAdviseFlags
	rest, err := commandParse("session advise", args, func(fs *flag.FlagSet) {
		flags = flaggen.BindSessionAdvise(fs)
	})
	if err != nil {
		return err
	}
	expect := flags.Expect
	text := strings.TrimSpace(strings.Join(rest, " "))
	if text == "" {
		return exit.Usagef(`magus session advise: needs the text to show every session, as in magus session advise "push what you have"`)
	}
	if err := requirePerson("advise"); err != nil {
		return err
	}
	store, err := directiveStore(root)
	if err != nil {
		return err
	}
	a, err := store.Post(context.Background(), text, expect, localOrigin(types.EntryPointCLI).User, time.Now())
	if err != nil {
		return err
	}
	return emitDirective(a, "posted")
}

func retractCommand(root string, args []string) error {
	rest, err := commandParse("session retract", args, func(*flag.FlagSet) {})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return exit.Usagef("magus session retract: needs exactly one advisory id (got %d); `magus session advisories` lists them", len(rest))
	}
	if err := requirePerson("retract"); err != nil {
		return err
	}
	store, err := directiveStore(root)
	if err != nil {
		return err
	}
	a, err := store.Retract(context.Background(), rest[0], localOrigin(types.EntryPointCLI).User, time.Now())
	if errors.Is(err, sessions.ErrNoDirective) {
		return fmt.Errorf("magus session retract: %w, run `magus session advisories --all` to list every id", err)
	}
	if err != nil {
		return fmt.Errorf("magus session retract: %w", err)
	}
	return emitDirective(a, "retracted")
}

func emitDirective(a sessions.Directive, verb string) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitRecord(opts, a, []string{a.ID})
	}
	fmt.Fprintf(os.Stdout, "%s %s\n  %s\n", verb, a.ID, attentionOneLine(a.Text))
	return nil
}

// advisoriesOutput is the JSON form of `session advisories`.
type advisoriesOutput struct {
	Advisories []sessions.Directive `json:"advisories"`
	Store      string               `json:"store"`
}

func advisoriesCommand(root string, args []string) error {
	var flags *flaggen.SessionAdvisoriesFlags
	rest, err := commandParse("session advisories", args, func(fs *flag.FlagSet) {
		flags = flaggen.BindSessionAdvisories(fs)
	})
	if err != nil {
		return err
	}
	all := flags.All
	if len(rest) > 0 {
		return exit.Usagef("magus session advisories: takes no arguments (got %q)", rest[0])
	}
	store, err := directiveStore(root)
	if err != nil {
		return err
	}
	dir := store.Dir()
	loaded, err := store.Load()
	if err != nil {
		return err
	}
	shown := make([]sessions.Directive, 0, len(loaded))
	for _, a := range loaded {
		if all || a.Open() {
			shown = append(shown, a)
		}
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText:
		renderAdvisoriesText(shown, dir)
		return nil
	case outputName:
		names := make([]string, 0, len(shown))
		for _, a := range shown {
			names = append(names, a.ID)
		}
		return emitNames(os.Stdout, names)
	}
	return emitFormatted(opts, advisoriesOutput{Advisories: shown, Store: dir})
}

func renderAdvisoriesText(shown []sessions.Directive, dir string) {
	if len(shown) == 0 {
		fmt.Fprintf(os.Stdout, "no open advisories in %s\n", dir)
		return
	}
	for _, a := range shown {
		state := "open"
		if !a.Open() {
			state = "retracted"
		}
		fmt.Fprintf(os.Stdout, "%s  %s  posted %s ago by %s  shown to %d  %s\n", a.ID, state,
			orDash(formatDur(time.Since(time.UnixMilli(a.PostedMs)))), orDash(a.By), len(a.Delivered), actedLine(a))
		fmt.Fprintf(os.Stdout, "  %s\n", attentionOneLine(a.Text))
	}
}

func actedLine(a sessions.Directive) string {
	if a.Expect == "" {
		return "acted: not measured (no --expect)"
	}
	return fmt.Sprintf("acted %d of %d (%q)", len(a.Acted), len(a.Delivered), a.Expect)
}
