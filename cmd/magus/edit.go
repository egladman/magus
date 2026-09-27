package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/edit"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// editCmd is `magus edit`: glue between the flags, the workspace and internal/edit,
// which owns every rule about what a set may do.
func editCmd(ctx context.Context, root string, args []string) error {
	var f *gen.EditFlags
	pos, err := cmdParse("edit", args, func(fs *flag.FlagSet) {
		f = gen.BindEdit(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus edit --stdin [--check] < edits.json")
			fmt.Fprintln(os.Stderr, "       magus edit --undo <receipt-id> [--check]")
			fmt.Fprintln(os.Stderr, "       magus edit --schema")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Apply an edit set: every site is checked against the file on disk first,")
			fmt.Fprintln(os.Stderr, "then every file is written or none is. The receipt holds the undo.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if f.Schema {
		schema, err := edit.Schema()
		if err != nil {
			return err
		}
		fmt.Print(schema)
		return nil
	}
	if len(pos) > 0 {
		return usagef("magus edit: unexpected argument %q; the set is read from --stdin", pos[0])
	}
	if f.Stdin == (f.Undo != "") {
		return usagef("magus edit: pass exactly one of --stdin or --undo <receipt-id>")
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	cacheDir, err := magus.ResolveCacheDir(ws.Root(), magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return err
	}
	receipts := edit.ReceiptDir(cacheDir)

	var set types.EditSet
	if f.Stdin {
		if set, err = edit.DecodeSet(os.Stdin); err != nil {
			return usagef("magus edit: %s (`%s` prints the schema it must satisfy)", err, hint.Edit.With("--schema"))
		}
	} else {
		r, err := edit.LoadReceipt(receipts, f.Undo)
		if err != nil {
			return usagef("magus edit: %s", err)
		}
		set = *r.Undo
	}

	plan := edit.Resolve(ws.Root(), set,
		edit.WithRefusal(declaredOutputRefusal(ctx, ws)),
		edit.WithCheckpoint(func() string {
			token, _ := checkoutBaseToken(ctx, root)
			return token
		}))
	if len(plan.Refused()) > 0 {
		return editRefused(opts, plan.Receipt())
	}
	if f.Check {
		return editEmit(opts, plan.Receipt())
	}

	var recorded string
	receipt, err := plan.Apply(func(r types.EditReceipt) error {
		recorded = r.ID
		return edit.SaveReceipt(receipts, r)
	})
	switch {
	case errors.Is(err, edit.ErrRefused):
		return editRefused(opts, receipt)
	case errors.Is(err, edit.ErrRestoreFailed):
		return fmt.Errorf("%w; receipt %s keeps the undo set and both digests", err, recorded)
	case err != nil:
		// Every file is back as it was, so the receipt would offer an undo of nothing.
		if recorded != "" {
			_ = edit.RemoveReceipt(receipts, recorded)
		}
		return err
	}
	// The receipt was recorded before the renames; this adds the checkpoint taken after.
	if err := edit.SaveReceipt(receipts, receipt); err != nil {
		fmt.Fprintf(os.Stderr, "magus edit: applied, but %s\n", err)
	}
	return editEmit(opts, receipt)
}

// declaredOutputRefusal refuses a path the workspace declares as a target's output.
// A classification failure refuses too: an unknown role is not a license to hand-edit.
func declaredOutputRefusal(ctx context.Context, ws types.WorkspaceRepository) func(string) string {
	return func(path string) string {
		entries, err := ws.ClassifyFiles(ctx, []string{path})
		if err != nil || len(entries) != 1 {
			return fmt.Sprintf("could not classify it against the workspace's declarations: %v", err)
		}
		e := entries[0]
		if e.Role != types.DiffRoleOutput {
			return ""
		}
		var by []string
		for _, c := range e.Claims {
			if c.Role == types.DiffRoleOutput {
				by = append(by, strings.TrimSuffix(c.Project+":"+c.Target, ":"))
			}
		}
		return "a declared output of " + strings.Join(by, ", ") + ": regenerate it, never hand-edit"
	}
}

func editRefused(opts OutputOptions, r types.EditReceipt) error {
	if opts.Format != outputText && opts.Format != outputName {
		if err := emitFormatted(opts, r); err != nil {
			return err
		}
	}
	fmt.Fprintln(os.Stderr, "magus edit: refused, nothing written")
	for _, ref := range r.Refused {
		fmt.Fprintln(os.Stderr, "  "+edit.FormatRefusal(ref))
	}
	return errSilent{exitCode: 1}
}

func editEmit(opts OutputOptions, r types.EditReceipt) error {
	switch opts.Format {
	case outputText:
	case outputName:
		if r.ID == "" {
			return nil
		}
		return emitNames([]string{r.ID})
	default:
		return emitFormatted(opts, r)
	}
	verb := "would change"
	if r.Applied {
		verb = "changed"
	}
	fmt.Printf("%s %d files, %d sites\n", verb, len(r.Files), len(r.Spans))
	for _, s := range r.Spans {
		fmt.Printf("  %s:%d:%d-%d:%d  edit %d\n", s.Path, s.Start.Line, s.Start.Col, s.End.Line, s.End.Col, s.Edit)
	}
	if r.Applied {
		fmt.Printf("receipt %s; undo: %s\n", r.ID, hint.Edit.With("--undo", r.ID))
	} else {
		fmt.Println("nothing written (--check)")
	}
	return nil
}
