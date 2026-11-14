package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/types"
)

// queryOutputStdin is `magus query output --stdin`.
func queryOutputStdin(ctx context.Context, qf *flaggen.QueryFlags, pos []string) error {
	if len(pos) != 1 || pos[0] != hint.QueryOutput.Leaf() {
		return exit.Usagef("magus query: --stdin applies only to `%s --stdin`, which takes no ref", hint.QueryOutput)
	}
	if qf.Attempts || qf.Identity || qf.Publish || qf.Open || qf.Print {
		// A record on stdin is untrusted: printed, never published or opened.
		return exit.Usagef("magus query output --stdin: --attempts, --identity, --publish, --open and --print act on a stored run; a record on stdin is only printed")
	}
	outOpts, oerr := outputOptionsOrDefault()
	if oerr != nil {
		return oerr
	}
	if outOpts.Format != outputText {
		return exit.Usagef("magus query output --stdin takes no -o: it prints each untrusted record neutralized, and the records on stdin are already the structured form")
	}
	return printOutputRecords(os.Stdin, os.Stdout, noticeLines{ctx: ctx, level: slog.LevelInfo})
}

// queryOutputCommand is `magus query output <ref>`; pos follows `query`.
func queryOutputCommand(ctx context.Context, root string, qf *flaggen.QueryFlags, pos []string) error {
	if len(pos) != 2 {
		slog.ErrorContext(ctx, fmt.Sprintf("expected exactly one ref (e.g. %s)", hint.QueryOutput.With("out1a2b3c")), attr.Notice(hint.QueryOutput.String()))
		return exit.Reported(exit.Usage)
	}
	ref := pos[1]
	if !cache.LooksLikeRef(ref) {
		if !trail.ValidRef(ref) {
			msg := fmt.Sprintf("%q is not an output ref (expected out<hex> for a run, or a stored payload such as grd<hex> or mcp<hex>)", ref)
			slog.ErrorContext(ctx, "", attr.Notice("magus query output"), attr.Error(types.DiagnosticErrorf(types.OutputRefMalformed, "%s", msg)))
			return exit.Reported(exit.Usage)
		}
		if qf.Attempts || qf.Identity || qf.Publish || qf.Open || qf.Print {
			return exit.Usagef("magus query output: %s is a stored payload, not a run; --attempts, --identity, --publish, --open and --print apply only to out<hex> refs", ref)
		}
		outOpts, oerr := outputOptionsOrDefault()
		if oerr != nil {
			return oerr
		}
		return queryTrailPayload(ctx, root, ref, outOpts)
	}
	outOpts, oerr := outputOptionsOrDefault()
	if oerr != nil {
		return oerr
	}
	exclusive := 0
	for _, set := range []bool{qf.Attempts, qf.Identity, qf.Publish, qf.Open || qf.Print} {
		if set {
			exclusive++
		}
	}
	if exclusive > 1 {
		slog.ErrorContext(ctx, "--attempts, --identity, --publish, and --open/--print are distinct actions; pick one", attr.Notice("magus query output"))
		return exit.Reported(exit.Usage)
	}
	return queryOutputRef(ctx, root, ref, outputRefOpts{open: qf.Open, printURL: qf.Print, viewerBase: qf.URL, attempts: qf.Attempts, identity: qf.Identity, publish: qf.Publish, out: outOpts})
}

// queryInvocationCommand is `magus query invocation <id>`; pos follows `query`.
func queryInvocationCommand(ctx context.Context, root string, qf *flaggen.QueryFlags, pos []string) error {
	if len(pos) != 2 {
		slog.ErrorContext(ctx, fmt.Sprintf("expected exactly one invocation id (e.g. %s)", hint.QueryInvocation.With("invmsm3vcou1")),
			attr.Notice(hint.QueryInvocation.String()))
		return exit.Reported(exit.Usage)
	}
	inv := pos[1]
	if !cache.LooksLikeInvocationID(inv) {
		slog.ErrorContext(ctx, fmt.Sprintf("%q is not an invocation id (expected inv<id>, e.g. invmsm3vcou1); a run prints one as `inv:` in %s",
			inv, hint.QueryOutput.With("<ref> --identity")), attr.Notice("magus query invocation"))
		return exit.Reported(exit.Usage)
	}
	outOpts, oerr := outputOptionsOrDefault()
	if oerr != nil {
		return oerr
	}
	return queryInvocation(ctx, root, inv, qf.Secrets, outOpts)
}

// outputRefOpts carries the options for `magus query output <ref>`.
type outputRefOpts struct {
	open       bool           // open the browser log viewer instead of printing
	printURL   bool           // with open, print the URL instead of launching a browser
	viewerBase string         // log viewer base URL
	attempts   bool           // list the ref's stored executions instead of printing output
	identity   bool           // show the run's identity (descriptor, lineage, key digests)
	publish    bool           // upload this run's output to the remote as a signed bundle
	out        output.Options // -o: text prints raw bytes, json/yaml prints the descriptor record
}

// queryOutputRef retrieves a target's captured output by reference id (or unique prefix). The
// default prints the exact bytes to stdout (pipe-friendly); -o json/yaml prints the descriptor
// record; --open hands it to the browser log viewer. The bytes never leave the machine: --open
// rides them in a URL fragment, exactly like `magus graph export --open`.
func queryOutputRef(ctx context.Context, root, ref string, o outputRefOpts) error {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	if o.attempts {
		return listOutputAttempts(ctx, m, ref, o.out)
	}
	if o.identity {
		return showOutputIdentity(ctx, m, ref, o.out)
	}
	if o.publish {
		return publishOutputRef(ctx, m, ref)
	}
	if o.open {
		return openOutputRef(ctx, m, ref, o)
	}
	// Remote-aware: a ref unknown locally may have been published from CI or a
	// teammate's machine, so the print path consults the remote bundle namespace
	// before reporting it missing.
	if o.out.Format != outputText {
		rec, rerr := m.OutputRecordByRef(ctx, ref)
		if rerr != nil {
			return reportRefLookupError(ctx, m, ref, rerr)
		}
		if o.out.Format == outputJSONL {
			// A slice, or writeJSONL would stream one of the record's list fields.
			return emitFormatted(o.out, []types.StoredOutput{rec})
		}
		return emitRecord(o.out, rec, []string{rec.Ref})
	}
	data, desc, err := m.OutputByRefRemote(ctx, ref)
	if err != nil {
		return reportRefLookupError(ctx, m, ref, err)
	}
	// On stderr, after the bytes: stdout stays pipe-clean, and a reader who has just
	// looked at what a run PRODUCED is one step from wanting to run it. Only worth
	// saying when the descriptor records a target to reproduce.
	if desc.Target != "" {
		interactive.Hint(ctx, fmt.Sprintf("reproduce this invocation here with `%s`", hint.X.With(ref)))
	}
	_, err = os.Stdout.Write(data) // default: verbatim bytes, pipe-clean
	if err == nil && desc.Failed && desc.ErrMsg != "" {
		// A failure's reason often lives only in the descriptor (a drift check prints
		// nothing), and only json showed it. It follows the bytes as a notice on stderr,
		// neutralized as an imported record's is, so stdout stays exactly what the run wrote.
		msg, _ := cache.NeutralizeTerminal([]byte(strings.TrimRight(desc.ErrMsg, "\n")), false)
		slog.ErrorContext(ctx, string(msg), attr.Notice(""), attr.Component("magus query output"))
		noticeFailedNeed(ctx, desc)
	}
	return err
}

func publishOutputRef(ctx context.Context, m *magus.Magus, ref string) error {
	published, perr := m.PublishOutput(ctx, ref)
	if perr != nil {
		if errors.Is(perr, fs.ErrNotExist) {
			// The lookup path's hint suggests --publish, which just failed.
			msg := fmt.Sprintf("no stored output for ref %q to publish; it may have aged out of the cache, or the ref is mistyped", ref)
			slog.ErrorContext(ctx, "", attr.Notice("magus query output"), attr.Error(types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg)))
			return exit.Reported(exit.Usage)
		}
		return fmt.Errorf("magus query output: publish %s: %w", ref, perr)
	}
	fmt.Printf("published %s to the remote cache\n", published)
	fmt.Printf("a teammate with the same trust set can now run: %s\n", hint.QueryOutput.With(published))
	return nil
}

// openOutputRef hands the viewer the ref's display events, which
// it renders from structure as a magus.viewer.v1alpha1 Journal.
func openOutputRef(ctx context.Context, m *magus.Magus, ref string, o outputRefOpts) error {
	data, desc, err := m.OutputByRef(ctx, ref)
	if err != nil {
		return reportRefLookupError(ctx, m, ref, err)
	}
	events := console.StitchDisplayEvents(data, cache.OutputDescriptor{
		Ref: desc.Ref, Project: desc.Project, Target: desc.Target, Inv: desc.Inv,
		Failed: desc.Failed, ErrMsg: desc.ErrMsg, TimestampMs: desc.TimestampMs, DurationMs: desc.DurationMs,
	})
	var inv magus.Invocation
	if desc.Inv != "" {
		// Best-effort lineage: omitted if the run log aged out.
		inv, _ = m.InvocationByID(desc.Inv)
	}
	// The run's per-class key digests let the viewer compare keys across
	// machines. Best-effort: a run predating key-input persistence opens
	// without them.
	var keyDigests string
	if lines, lerr := m.OutputKeyInputs(ctx, ref); lerr == nil {
		pairs := make([]console.KeyClassDigest, 0, len(cache.ClassDigests(lines)))
		for _, c := range cache.ClassDigests(lines) {
			pairs = append(pairs, console.KeyClassDigest{Class: c.Class, Digest: c.Digest})
		}
		keyDigests = console.KeyDigestsParam(pairs)
	}
	return openOutputInViewer(ctx, desc, events, inv, keyDigests, o)
}

// noticeFailedNeed names the ctx.needs member a composed target failed on and the command
// that reads its output, which holds the findings the parent's own output and error only
// restate. It says nothing for a descriptor that records no member.
func noticeFailedNeed(ctx context.Context, d magus.OutputDescriptor) {
	if d.FailedNeedRef == "" {
		return
	}
	slog.InfoContext(ctx, fmt.Sprintf("the failing step is %s, a ctx.needs member; its output is under its own ref:", d.FailedNeedTarget),
		attr.Notice(""), attr.Component("magus query output"), attr.Next(hint.QueryOutput.With(d.FailedNeedRef)))
}

// trailPayloadRecord is the -o json/yaml projection of a stored payload: no run stands
// behind it, so the ref and the bytes are the whole record.
type trailPayloadRecord struct {
	Ref    string `json:"ref"`
	Output string `json:"output"`
}

// queryTrailPayload prints a payload the activity trail stored under a non-out prefix: a
// guard verdict (grd), an MCP request or response (mcp). The ref names its own store, so a
// miss here is final and the run-output store is not consulted.
func queryTrailPayload(ctx context.Context, root, ref string, out output.Options) error {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	data, err := m.PayloadByRef(ctx, ref)
	if errors.Is(err, fs.ErrNotExist) {
		msg := err.Error() + "; the activity trail may have rotated it out, or the ref is mistyped"
		slog.ErrorContext(ctx, "", attr.Notice("magus query output"), attr.Error(types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg)))
		return exit.Reported(exit.Usage)
	}
	if err != nil {
		return fmt.Errorf("magus query output: read %s: %w", ref, err)
	}
	if out.Format != outputText {
		return emitRecord(out, trailPayloadRecord{Ref: ref, Output: string(data)}, []string{ref})
	}
	// A verdict is stored without a trailing newline, so it would run into the prompt.
	if !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	_, err = os.Stdout.Write(data)
	return err
}

// listOutputAttempts renders the keep-last-K executions behind one portable ref, newest
// first. Every attempt of a step shares the step's ref; the attempt id is the
// execution-unique handle, and passing a full attempt id to `magus query output`
// retrieves that exact execution's bytes.
func listOutputAttempts(ctx context.Context, m *magus.Magus, ref string, out output.Options) error {
	list, err := m.OutputAttempts(ctx, ref)
	if err != nil {
		return reportRefLookupError(ctx, m, ref, err)
	}
	if out.Format != outputText {
		names := make([]string, len(list))
		for i, d := range list {
			names[i] = d.Attempt
		}
		return emitRecord(out, list, names)
	}
	// Head the listing with the STEP's identity: the portable ref when a v2
	// descriptor carries the key, else the ref as the user typed it. list[0].Ref
	// would name one arbitrary execution for a pre-portable directory.
	stepRef := ref
	for _, d := range list {
		if d.Key != "" {
			stepRef = d.Ref
			break
		}
	}
	fmt.Printf("attempts for %s (newest first):\n", stepRef)
	for _, d := range list {
		status := "pass"
		if d.Failed {
			status = "fail"
		}
		when := time.UnixMilli(d.TimestampMs).Format("2006-01-02 15:04:05")
		dur := (time.Duration(d.DurationMs) * time.Millisecond).Round(time.Millisecond)
		fmt.Printf("  %s  %s  %8s  %s  %s\n", d.Attempt, status, dur, when, d.Inv)
	}
	// The bare ref already answers with the newest attempt; the hint earns its keep
	// for reaching an OLDER one.
	if len(list) > 1 {
		fmt.Printf("\nRetrieve one attempt's exact output: %s\n", hint.QueryOutput.With(list[len(list)-1].Attempt))
	}
	return nil
}

// outputIdentityRecord is the -o json/yaml projection of `query output <ref> --identity`: the
// stored descriptor, the producing invocation's lineage when its run log survives, and
// the cache key's component-class digests when the run persisted its key inputs.
// invocationRecord is the machine-readable projection of one run: its header plus the events
// an audit cares about. The events are already redacted (journal.Emit scrubs Text on the way
// in), and a secret event carries only the reference and the provider by construction.
type invocationRecord struct {
	magus.Invocation
	Secrets []magus.Event `json:"secrets,omitempty"`
	Events  []magus.Event `json:"events,omitempty"`
}

// queryInvocation reads one run's journal back. `--secrets` narrows it to the credential
// reads, which is the question the secrets page promises an answer to: "which credentials did
// this run reach for, and through which backend".
//
// This exists because the events were written and unreachable. The reference and provider were
// recorded on every read, docs/concepts/secrets.md offered that as the audit trail, and the id
// magus prints (`inv:`) resolved to nothing, so the trail was a claim rather than a record.
func queryInvocation(ctx context.Context, root, inv string, secretsOnly bool, out output.Options) error {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	header, events, err := m.InvocationEventsByID(inv)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Aged out is the ordinary case, not a typo, so say which it might be. The cap is
			// the server's RotateLogs job; a missing log is indistinguishable from a bad id.
			slog.ErrorContext(ctx, fmt.Sprintf("no run log for %q; it may have aged out of the cache, or the id is mistyped", inv), attr.Notice("magus query invocation"))
			return exit.Reported(exit.Usage)
		}
		return fmt.Errorf("magus query invocation: read run log for %s: %w", inv, err)
	}

	var secrets []magus.Event
	for _, e := range events {
		if e.Kind == journal.KindSecret {
			secrets = append(secrets, e)
		}
	}

	switch out.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		rec := invocationRecord{Invocation: header, Secrets: secrets}
		if !secretsOnly {
			rec.Events = events
		}
		return emitFormatted(out, rec)
	case outputName:
		return emitNames(os.Stdout, []string{header.ID})
	}

	printInvocationText(os.Stdout, header, secrets, events, secretsOnly)
	return nil
}

func printInvocationText(w io.Writer, header magus.Invocation, secrets, events []magus.Event, secretsOnly bool) {
	fmt.Fprintf(w, "inv:     %s\n", header.ID)
	if len(header.Command.Arguments) > 0 {
		fmt.Fprintf(w, "command: magus %s\n", strings.Join(header.Command.Arguments, " "))
	}
	if header.Command.Cwd != "" {
		fmt.Fprintf(w, "cwd:     %s\n", header.Command.Cwd)
	}
	if header.StartedMs > 0 {
		line := time.UnixMilli(header.StartedMs).Format("2006-01-02 15:04:05")
		if header.FinishedMs > header.StartedMs {
			line += fmt.Sprintf(" (%s)", (time.Duration(header.FinishedMs-header.StartedMs) * time.Millisecond).Round(time.Millisecond))
		}
		fmt.Fprintf(w, "started: %s\n", line)
	}
	if header.Status != "" {
		fmt.Fprintf(w, "status:  %s\n", header.Status)
	}
	if header.MagusVersion != "" {
		fmt.Fprintf(w, "magus:   %s\n", header.MagusVersion)
	}

	fmt.Fprintln(w)
	if len(secrets) == 0 {
		// Say it plainly rather than printing an empty heading. "No credential reads" is a
		// real audit answer, and the reader must be able to tell it apart from "not recorded".
		// Same noun as the populated branch below, and as the rest of the CLI: this
		// line used to say "no credential was resolved", which read as a different fact.
		fmt.Fprintln(w, "secrets: no credential reads during this run")
	} else {
		fmt.Fprintf(w, "secrets: %d credential read(s)\n", len(secrets))
		for _, e := range secrets {
			where := e.Project
			if e.Target != "" {
				where += " " + e.Target
			}
			fmt.Fprintf(w, "  %s  %-28s %s\n", time.UnixMilli(e.Timestamp).Format("15:04:05"), where, e.Text)
		}
	}
	if secretsOnly {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "events:  %d\n", len(events))
	for _, e := range events {
		if e.Kind == journal.KindOutput {
			continue // the captured bytes belong to `query output <ref>`, not here
		}
		detail := e.Text
		if e.Kind == journal.KindResult {
			// A result is the line a reader acts on: which target, and the ref that reads
			// its output, so a failure never needs a second lookup to find its log.
			first, _, _ := strings.Cut(e.Text, "\n")
			detail = strings.Join(slices.DeleteFunc([]string{e.Status, e.Project, e.Target, e.Ref, first}, func(s string) bool { return s == "" }), " ")
		} else if e.Status != "" {
			detail = strings.TrimSpace(fmt.Sprintf("%s %s", e.Status, detail))
		}
		fmt.Fprintf(w, "  %s  %-9s %s\n", time.UnixMilli(e.Timestamp).Format("15:04:05"), e.Kind, detail)
	}
}

type outputIdentityRecord struct {
	magus.OutputDescriptor
	Invocation   *magus.Invocation   `json:"invocation,omitempty"`
	ClassDigests []cache.ClassDigest `json:"class_digests,omitempty"`
}

// showOutputIdentity renders a stored run's IDENTITY rather than its output: what ran,
// how it ended, which invocation produced it, and the digests of its cache key's
// component classes: the machine-comparable half of the works-on-my-machine story
// (two machines compare digests to learn WHICH class disagrees; `describe target
// --cache --against` then names the exact line).
func showOutputIdentity(ctx context.Context, m *magus.Magus, ref string, out output.Options) error {
	desc, err := m.OutputDescriptorByRef(ctx, ref)
	if err != nil {
		return reportRefLookupError(ctx, m, ref, err)
	}
	var digests []cache.ClassDigest
	lines, lerr := m.OutputKeyInputs(ctx, ref)
	switch {
	case lerr == nil:
		digests = cache.ClassDigests(lines)
	case !errors.Is(lerr, fs.ErrNotExist):
		// Absent lines are ordinary (a run predating persistence); anything else is a
		// real read failure and must not masquerade as one.
		return fmt.Errorf("magus query output: read stored key inputs for %s: %w", ref, lerr)
	}
	var inv *magus.Invocation
	if desc.Inv != "" {
		if got, ierr := m.InvocationByID(desc.Inv); ierr == nil {
			inv = &got
		}
	}
	switch out.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(out, outputIdentityRecord{OutputDescriptor: desc, Invocation: inv, ClassDigests: digests})
	case outputName:
		return emitNames(os.Stdout, []string{desc.Ref})
	}
	printOutputIdentityText(ctx, m, desc, inv, digests)
	return nil
}

func printOutputIdentityText(ctx context.Context, m *magus.Magus, desc magus.OutputDescriptor, inv *magus.Invocation, digests []cache.ClassDigest) {
	fmt.Printf("ref:     %s\n", desc.Ref)
	fmt.Printf("project: %s\n", desc.Project)
	if desc.Target != "" {
		fmt.Printf("target:  %s\n", desc.Target)
	}
	status := "pass"
	if desc.Failed {
		status = "fail"
	}
	fmt.Printf("status:  %s (%s) at %s\n", status,
		(time.Duration(desc.DurationMs) * time.Millisecond).Round(time.Millisecond),
		time.UnixMilli(desc.TimestampMs).Format("2006-01-02 15:04:05"))
	if desc.ErrMsg != "" {
		fmt.Printf("error:   %s\n", desc.ErrMsg)
	}
	if desc.FailedNeedRef != "" {
		fmt.Printf("failed:  ctx.needs %s, read it with %s\n", desc.FailedNeedTarget, hint.QueryOutput.With(desc.FailedNeedRef))
	}
	if desc.Attempt != "" {
		fmt.Printf("attempt: %s\n", desc.Attempt)
	}
	if desc.Inv != "" {
		fmt.Printf("inv:     %s", desc.Inv)
		if inv != nil && len(inv.Command.Arguments) > 0 {
			fmt.Printf("  (magus %s)", strings.Join(inv.Command.Arguments, " "))
		}
		fmt.Println()
	}
	if desc.Key != "" {
		fmt.Printf("key:     %s (keyVersion %d)\n", desc.Key, desc.KeyVersion)
	}
	if desc.MagusVersion != "" {
		fmt.Printf("magus:   %s\n", desc.MagusVersion)
	}
	if desc.Revision != "" {
		fmt.Printf("rev:     %s", magus.ShortRevision(desc.Revision))
		if desc.Dirty {
			fmt.Printf(" (dirty: uncommitted changes at capture time; the revision alone may not reproduce it)")
		}
		fmt.Println()
		// The cache key pins a tree STATE, never a commit; this is the one place
		// that names one, by comparing the descriptor's revision against HEAD now.
		// PROVENANCE only, never instruction: on a cache HIT, recordOutput never runs,
		// so the descriptor still carries the revision of whichever run FIRST minted
		// this key, which can differ from HEAD even though the current tree
		// reproduces the output perfectly (that reproduction is exactly why it was a
		// hit). Telling the user to check out the recorded commit would therefore be
		// wrong on the most common path that reaches this line: a ref that resolved
		// already has its bytes, and the KEY, not the commit, is what decided that.
		// Only print when the two actually differ: same revision is the common case
		// and needs no callout.
		if _, cur, _ := m.CurrentRevision(ctx); cur != "" && cur != desc.Revision {
			fmt.Printf("recorded at %s, you are on %s.\n", magus.ShortRevision(desc.Revision), magus.ShortRevision(cur))
		}
	}
	if len(digests) == 0 {
		fmt.Println("\nkey components: unavailable (run predates key-input persistence; re-run the target to record them)")
		return
	}
	fmt.Println("\nkey components:")
	for _, d := range digests {
		noun := "lines"
		if d.Count == 1 {
			noun = "line"
		}
		fmt.Printf("  %-16s %s  %d %s\n", d.Class, d.Digest, d.Count, noun)
	}
}

// reportRefLookupError renders the standard output-ref resolution failures (ambiguous prefix,
// missing/aged-out, or an unexpected error) as a coded diagnostic + exit code. On the
// missing-ref path it also prints a best-effort suggestion inverting the ref back to the
// workspace target(s) that could have minted it; see printIdentifyRefSuggestion. m may be
// nil at call sites with no loaded Magus in scope; the suggestion is then skipped, not
// attempted against a nil receiver.
func reportRefLookupError(ctx context.Context, m *magus.Magus, ref string, err error) error {
	var amb *cache.AmbiguousRefError
	switch {
	case errors.As(err, &amb):
		slog.ErrorContext(ctx, "", attr.Notice("magus query"), attr.Error(types.DiagnosticErrorf(types.OutputRefAmbiguous, "%s", amb.Error())))
		return exit.Reported(exit.Usage)
	case errors.Is(err, fs.ErrNotExist):
		// Name the stores consulted when the lookup knows them: a foreign ref that was
		// never published reads exactly like a mistyped one otherwise. Render the message
		// ONCE: RefNotFoundError.Error() already includes "consulted: <stores>".
		msg := fmt.Sprintf("no stored output for ref %q. It may have aged out of the cache, or the ref is mistyped; re-run the target to regenerate it.", ref)
		var missing *cache.RefNotFoundError
		if errors.As(err, &missing) {
			msg = missing.Error()
		}
		slog.ErrorContext(ctx, "", attr.Notice("magus query"), attr.Error(types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg)))
		printIdentifyRefSuggestion(ctx, m, ref)
		return exit.Reported(exit.Usage)
	default:
		return fmt.Errorf("magus query: look up output ref %q: %w", ref, err)
	}
}

// printIdentifyRefSuggestion best-effort-inverts a missing ref back to the workspace
// target(s) that could have minted it (Magus.IdentifyRef) and prints the finding to
// stderr. A nil m, or IdentifyRef itself erroring (e.g. types.ErrNoCache on an Inspect
// workspace), both skip silently: a best-effort suggestion must never turn a lookup
// error into a different one.
func printIdentifyRefSuggestion(ctx context.Context, m *magus.Magus, ref string) {
	if m == nil {
		return
	}
	matches, err := m.IdentifyRef(ctx, ref)
	if err != nil {
		return
	}
	switch len(matches) {
	case 0:
		// Informative, not a failure to try: nothing here keys to the ref because the
		// run that printed it had different inputs, not because this lookup is broken.
		slog.InfoContext(ctx, fmt.Sprintf("No target in this workspace keys to that ref at the current tree, which means the run that printed it had different inputs (a different commit, uncommitted change, or environment).\nOnce you know which target it should be: %s", hint.DescribeTargets.With("<target>", "--cache", "--against", ref)), attr.Notice(""))
	case 1:
		slog.InfoContext(ctx, "Nothing has produced it here, but this workspace would print it for:\n  "+m.RefMatchCommand(matches[0]), attr.Notice(""))
	default:
		var cmds strings.Builder
		for _, mt := range matches {
			cmds.WriteString("\n  " + m.RefMatchCommand(mt))
		}
		slog.InfoContext(ctx, "Nothing has produced it here, but this workspace would print it for any of:"+cmds.String(), attr.Notice(""))
	}
	// Reachable from every branch, not just the zero-match one: even a matched target
	// may be nondeterministic or expensive enough that the exact bytes from whoever
	// already has them beat a local re-run.
	slog.InfoContext(ctx, "If someone else has it, they can share it with: "+hint.QueryOutput.With(ref, "--publish"), attr.Notice(""))
}

// openOutputInViewer builds the viewer URL and opens a browser; --print emits the
// URL instead. It warns when the link nears browser URL-length limits.
func openOutputInViewer(ctx context.Context, desc magus.OutputDescriptor, events []journal.Event, inv magus.Invocation, keyDigests string, o outputRefOpts) error {
	if err := hostedPageAllowed(ctx, "log viewer", "magus query output --open", o.viewerBase, o.printURL); err != nil {
		return err
	}
	rawInv := journal.Invocation{
		ID:           inv.ID,
		Command:      journal.Command{Arguments: inv.Command.Arguments, Cwd: inv.Command.Cwd, Trigger: inv.Command.Trigger},
		StartedMs:    inv.StartedMs,
		FinishedMs:   inv.FinishedMs,
		Status:       inv.Status,
		MagusVersion: inv.MagusVersion,
	}
	openURL, err := console.LogViewerURL(o.viewerBase, desc.Ref, events, rawInv, keyDigests)
	if err != nil {
		return err
	}
	if len(openURL) > fragmentWarnBytes {
		slog.WarnContext(ctx, fmt.Sprintf("this link is %d KB, near or past what Safari and older\n"+
			"Firefox accept in a URL (Chrome is fine). If the page does not load, pipe it instead:\n"+
			"  magus query output %s | less. Continuing.", len(openURL)/1024, desc.Ref), attr.Notice("magus query"))
	}
	if o.printURL {
		fmt.Println(openURL)
		return nil
	}
	slog.InfoContext(ctx, fmt.Sprintf("opening the log viewer for %s; the output rides in the link fragment and never leaves your machine.", desc.Ref), attr.Notice(""))
	if err := openBrowser(openURL); err != nil {
		slog.ErrorContext(ctx, "could not open a browser", attr.Notice("magus query"), attr.Error(err),
			attr.Why("Re-run with --print to get the URL."))
		return exit.Reported(exit.Failure)
	}
	return nil
}
