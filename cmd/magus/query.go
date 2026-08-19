package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/url"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

// query/explain/path are the knowledge-graph retrieval verbs. They reuse
// prior-art vocabulary (graph tooling generally) and sit on the same cache-first
// substrate as `magus graph export`. query resolves terms to nodes and returns
// the neighborhood; explain shows one node's context; path connects two nodes.
//
// query also doubles as the retrieval subcommand for target-output reference ids through
// an EXPLICIT `output` subcommand: `magus query output out1a2b3c` prints that
// execution's captured output instead of searching the graph. It is a subcommand,
// not a shape-routed positional, so a free-text search term can never collide with a
// ref id (`magus query refactor` always searches the graph).

// defaultLogViewerURL is the hosted, data-agnostic log viewer that `magus query
// output <ref> --open` points a browser at, with the captured output delivered
// PRIVATELY in a URL fragment (never uploaded). Override with --url for a self-hosted
// mirror.
const defaultLogViewerURL = "https://eli.gladman.cc/magus/console/logs/"

func queryCmd(ctx context.Context, root string, args []string) error {
	var qf *gen.QueryFlags
	pos, err := cmdParse("query", args, func(fs *flag.FlagSet) {
		qf = gen.BindQuery(fs, gen.QueryDefaults{URL: defaultLogViewerURL})
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus query <terms> [flags]")
			fmt.Fprintln(os.Stderr, "       magus query output <ref> [-o json] [--open] [--attempts] [--meta] [--publish]")
			fmt.Fprintln(os.Stderr, "       magus query invocation <id> [-o json] [--secrets]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeQueryDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Terms are free text plus field filters: kind:spell, project:pkg/foo,")
			fmt.Fprintln(os.Stderr, "relation:uses, id:build, and negation (-kind:op). Example:")
			fmt.Fprintln(os.Stderr, "  magus query kind:spell go")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintf(os.Stderr, "`%s <ref>` retrieves one target execution's captured output by its\n", clihint.QueryOutput.Leaf())
			fmt.Fprintln(os.Stderr, "reference id (out1a2b3c), shown when the target ran:")
			fmt.Fprintf(os.Stderr, "  %-38s print the exact bytes (pipe anywhere)\n", clihint.QueryOutput.With("out1a2b3c"))
			fmt.Fprintf(os.Stderr, "  %-38s the descriptor + output as a record\n", clihint.QueryOutput.With("out1a2b3c", "-o json"))
			fmt.Fprintf(os.Stderr, "  %-38s open it in the browser log viewer\n", clihint.QueryOutput.With("out1a2b3c", "--open"))
			fmt.Fprintf(os.Stderr, "  %-38s list the ref's stored executions\n", clihint.QueryOutput.With("out1a2b3c", "--attempts"))
			fmt.Fprintf(os.Stderr, "  %-38s the run's identity + cache-key digests\n", clihint.QueryOutput.With("out1a2b3c", "--meta"))
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintf(os.Stderr, "`%s <id>` reads one run's journal back by the id shown as\n", clihint.QueryInvocation.Leaf())
			fmt.Fprintf(os.Stderr, "`inv:` in %s:\n", clihint.QueryOutput.With("<ref>", "--meta"))
			fmt.Fprintf(os.Stderr, "  %-38s the run's events, newest last\n", clihint.QueryInvocation.With("invmsm3vcou1"))
			fmt.Fprintf(os.Stderr, "  %-38s only the credential reads (audit)\n", clihint.QueryInvocation.With("invmsm3vcou1", "--secrets"))
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "--open respects the BROWSER environment variable to pick the browser")
			fmt.Fprintln(os.Stderr, "(e.g. BROWSER=firefox); otherwise it uses your desktop's default handler.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	// Output-reference retrieval is an EXPLICIT subcommand - `magus query output <ref>` - not a
	// shape-routed positional, so a search term can never collide with a ref id.
	if len(pos) >= 1 && pos[0] == clihint.QueryOutput.Leaf() {
		if len(pos) != 2 {
			fmt.Fprintf(os.Stderr, "%s: expected exactly one ref (e.g. %s)\n", clihint.QueryOutput, clihint.QueryOutput.With("out1a2b3c"))
			return errSilent{exitCode: 2}
		}
		ref := pos[1]
		if !cache.LooksLikeRef(ref) {
			msg := fmt.Sprintf("%q is not a target-output reference (expected out<hex>, e.g. out1a2b3c)", ref)
			fmt.Fprintf(os.Stderr, "magus query output: %s\n", types.DiagnosticErrorf(types.OutputRefMalformed, "%s", msg).Error())
			return errSilent{exitCode: 2}
		}
		outOpts, oerr := outputOptionsOrDefault()
		if oerr != nil {
			return oerr
		}
		exclusive := 0
		for _, set := range []bool{qf.Attempts, qf.Meta, qf.Publish, qf.Open || qf.Print} {
			if set {
				exclusive++
			}
		}
		if exclusive > 1 {
			fmt.Fprintf(os.Stderr, "magus query output: --attempts, --meta, --publish, and --open/--print are distinct actions; pick one\n")
			return errSilent{exitCode: 2}
		}
		return queryOutputRef(ctx, root, ref, outputRefOpts{open: qf.Open, printURL: qf.Print, viewerBase: qf.URL, attempts: qf.Attempts, meta: qf.Meta, publish: qf.Publish, out: outOpts})
	}
	// `magus query invocation <id>` - the sibling of `query output <ref>`, and explicit for
	// the same reason: an id is shape-routed nowhere, so a search term cannot collide with one.
	if len(pos) >= 1 && pos[0] == clihint.QueryInvocation.Leaf() {
		if len(pos) != 2 {
			fmt.Fprintf(os.Stderr, "%s: expected exactly one invocation id (e.g. %s)\n",
				clihint.QueryInvocation, clihint.QueryInvocation.With("invmsm3vcou1"))
			return errSilent{exitCode: 2}
		}
		inv := pos[1]
		if !cache.LooksLikeInvocationID(inv) {
			fmt.Fprintf(os.Stderr, "magus query invocation: %q is not an invocation id (expected inv<id>, e.g. invmsm3vcou1); a run prints one as `inv:` in %s\n",
				inv, clihint.QueryOutput.With("<ref> --meta"))
			return errSilent{exitCode: 2}
		}
		outOpts, oerr := outputOptionsOrDefault()
		if oerr != nil {
			return oerr
		}
		return queryInvocation(ctx, root, inv, qf.Secrets, outOpts)
	}
	// An invocation id handed to the graph grammar finds nothing and reports `matches: 0`,
	// which reads as "that run does not exist" rather than "wrong command". magus printed the
	// id, so it can recognize it coming back.
	if len(pos) == 1 && cache.LooksLikeInvocationID(pos[0]) {
		fmt.Fprintf(os.Stderr, "magus query: %q is an invocation id, not a graph term. Read it with: %s\n",
			pos[0], clihint.QueryInvocation.With(pos[0]))
		return errSilent{exitCode: 2}
	}
	if qf.Open || qf.Print || qf.Attempts || qf.Meta || qf.Publish {
		// --open/--print/--attempts/--meta only apply to `query output <ref>`. Set on a graph
		// search, they were a mistake; stop rather than silently ignore them.
		fmt.Fprintf(os.Stderr, "magus query: --open/--print/--attempts/--meta/--publish apply only to `%s <ref>`. To open the knowledge graph in a browser, use `%s`.\n", clihint.QueryOutput, clihint.GraphExport.With("--open"))
		return errSilent{exitCode: 2}
	}
	if len(pos) == 0 && qf.Kind == "" {
		fmt.Fprintln(os.Stderr, "magus query: requires search terms")
		return errSilent{exitCode: 2}
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	input := strings.Join(pos, " ")
	for _, k := range splitCSV(qf.Kind) {
		input += " kind:" + k
	}

	seedsSymbols := knowledge.SeedsSymbols(input)
	g, err := loadKnowledgeGraph(ctx, root, qf.Refresh, qf.Global, seedsSymbols)
	if err != nil {
		return err
	}
	out := g.Query(input, qf.Budget)
	reason, gaps := symbolCoverage(ctx, root, input, seedsSymbols)
	out.Answer = types.Answer(out.MatchCount > 0, reason, gaps)

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		for _, m := range out.Matches {
			fmt.Println(m.ID)
		}
		return nil
	}

	fmt.Printf("query: %s\n", out.Query)
	fmt.Printf("matches: %d  (neighborhood budget %d)\n\n", out.MatchCount, out.Budget)
	if out.MatchCount == 0 {
		// query's exit status stays 0 whatever the verdict: an empty result set is a
		// legitimate answer to a search, and every script that runs `magus query` would
		// break if it became a failure. The verdict rides the output instead.
		printVerdict(os.Stdout, out.Answer, clihint.Refs.With("<name>"))
		return nil
	}
	shown := out.Matches
	if len(shown) > 20 {
		shown = shown[:20]
	}
	for _, m := range shown {
		fmt.Printf("  %-7d %s  [%s]\n", m.Score, m.ID, m.Kind)
	}
	if len(out.Matches) > len(shown) {
		fmt.Printf("  ... and %d more\n", len(out.Matches)-len(shown))
	}
	fmt.Printf("\nneighborhood: %d nodes, %d edges\n", len(out.Nodes), len(out.Links))
	fmt.Println("Run with -o json for the full subgraph.")
	return nil
}

// outputRefOpts carries the options for `magus query output <ref>`.
type outputRefOpts struct {
	open       bool          // open the browser log viewer instead of printing
	printURL   bool          // with open, print the URL instead of launching a browser
	viewerBase string        // log viewer base URL
	attempts   bool          // list the ref's stored executions instead of printing output
	meta       bool          // show the run's identity (descriptor, lineage, key digests)
	publish    bool          // upload this run's output to the remote as a signed bundle
	out        OutputOptions // -o: text prints raw bytes, json/yaml prints the descriptor record
}

// outputRefRecord is the -o json/yaml projection of a stored output: its descriptor plus the
// captured output as an opaque verbatim field. The descriptor DESCRIBES the run (project/target/
// status/timing); the output is the payload, never parsed - so structure lives in the record,
// not in an interpretation of the bytes.
type outputRefRecord struct {
	magus.OutputDescriptor
	Output string `json:"output"`
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
	if o.meta {
		return showOutputMeta(ctx, m, ref, o.out)
	}
	if o.publish {
		published, perr := m.PublishOutput(ctx, ref)
		if perr != nil {
			if errors.Is(perr, fs.ErrNotExist) {
				// Not the generic lookup path: its hint suggests --publish, which is
				// the command that just failed.
				msg := fmt.Sprintf("no stored output for ref %q to publish; it may have aged out of the cache, or the ref is mistyped", ref)
				fmt.Fprintf(os.Stderr, "magus query output: %s\n", types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg).Error())
				return errSilent{exitCode: 2}
			}
			return fmt.Errorf("magus query output: publish %s: %w", ref, perr)
		}
		fmt.Printf("published %s to the remote cache\n", published)
		fmt.Printf("a teammate with the same trust set can now run: %s\n", clihint.QueryOutput.With(published))
		return nil
	}
	if o.open {
		// The viewer ingests a magus.viewer.v1alpha1 Journal, so hand it the ref's display events -
		// the browser renders pretty from structure.
		data, desc, err := m.OutputByRef(ref)
		if err != nil {
			return reportRefLookupError(ctx, m, ref, err)
		}
		events := console.StitchDisplayEvents(data, cache.OutputDescriptor{
			Ref: desc.Ref, Project: desc.Project, Target: desc.Target, Inv: desc.Inv,
			Failed: desc.Failed, ErrMsg: desc.ErrMsg, TimestampMs: desc.TimestampMs, DurationMs: desc.DurationMs,
		})
		var inv magus.Invocation
		if desc.Inv != "" {
			inv, _ = m.InvocationByID(desc.Inv) // best-effort lineage; omitted if the run log aged out
		}
		// The run's per-class key digests ride along so the viewer can show a
		// machine-vs-machine key comparison. Best-effort: a run predating key-input
		// persistence just opens without them.
		var keyDigests string
		if lines, lerr := m.OutputKeyInputs(ref); lerr == nil {
			pairs := make([]console.KeyClassDigest, 0, len(cache.ClassDigests(lines)))
			for _, c := range cache.ClassDigests(lines) {
				pairs = append(pairs, console.KeyClassDigest{Class: c.Class, Digest: c.Digest})
			}
			keyDigests = console.KeyDigestsParam(pairs)
		}
		return openOutputInViewer(desc, events, inv, keyDigests, o)
	}
	// Remote-aware: a ref unknown locally may have been published from CI or a
	// teammate's machine, so the print path consults the remote bundle namespace
	// before reporting it missing.
	data, desc, err := m.OutputByRefRemote(ctx, ref)
	if err != nil {
		return reportRefLookupError(ctx, m, ref, err)
	}
	if o.out.Format == FormatJSON || o.out.Format == FormatYAML {
		return emitFormatted(o.out, outputRefRecord{OutputDescriptor: desc, Output: string(data)})
	}
	// On stderr, after the bytes: stdout stays pipe-clean, and a reader who has just
	// looked at what a run PRODUCED is one step from wanting to run it. Only worth
	// saying when the descriptor records a target to reproduce.
	if desc.Target != "" {
		interactive.Emit(os.Stderr, fmt.Sprintf("reproduce this invocation here with `magus x %s`", ref))
	}
	_, err = os.Stdout.Write(data) // default: verbatim bytes, pipe-clean
	return err
}

// listOutputAttempts renders the keep-last-K executions behind one portable ref, newest
// first. Every attempt of a step shares the step's ref; the attempt id is the
// execution-unique handle, and passing a full attempt id to `magus query output`
// retrieves that exact execution's bytes.
func listOutputAttempts(ctx context.Context, m *magus.Magus, ref string, out OutputOptions) error {
	list, err := m.OutputAttempts(ref)
	if err != nil {
		return reportRefLookupError(ctx, m, ref, err)
	}
	if out.Format == FormatJSON || out.Format == FormatYAML {
		return emitFormatted(out, list)
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
		fmt.Printf("\nRetrieve one attempt's exact output: %s\n", clihint.QueryOutput.With(list[len(list)-1].Attempt))
	}
	return nil
}

// outputMetaRecord is the -o json/yaml projection of `query output <ref> --meta`: the
// stored descriptor, the producing invocation's lineage when its run log survives, and
// the cache key's component-class digests when the run persisted its key inputs.
// invocationRecord is the machine-readable projection of one run: its header plus the events
// an audit cares about. The events are already redacted - journal.Emit scrubs Text on the way
// in - and a secret event carries only the reference and the provider by construction.
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
// magus prints (`inv:`) resolved to nothing - so the trail was a claim rather than a record.
func queryInvocation(ctx context.Context, root, inv string, secretsOnly bool, out OutputOptions) error {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	header, events, err := m.InvocationEventsByID(inv)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Aged out is the ordinary case, not a typo, so say which it might be. The cap is
			// the daemon's RotateLogs job; a missing log is indistinguishable from a bad id.
			fmt.Fprintf(os.Stderr, "magus query invocation: no run log for %q; it may have aged out of the cache, or the id is mistyped\n", inv)
			return errSilent{exitCode: 2}
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
		fmt.Println(header.ID)
		return nil
	}

	fmt.Printf("inv:     %s\n", header.ID)
	if len(header.Command.Arguments) > 0 {
		fmt.Printf("command: magus %s\n", strings.Join(header.Command.Arguments, " "))
	}
	if header.Command.Cwd != "" {
		fmt.Printf("cwd:     %s\n", header.Command.Cwd)
	}
	if header.StartedMs > 0 {
		line := time.UnixMilli(header.StartedMs).Format("2006-01-02 15:04:05")
		if header.FinishedMs > header.StartedMs {
			line += fmt.Sprintf(" (%s)", (time.Duration(header.FinishedMs-header.StartedMs) * time.Millisecond).Round(time.Millisecond))
		}
		fmt.Printf("started: %s\n", line)
	}
	if header.Status != "" {
		fmt.Printf("status:  %s\n", header.Status)
	}
	if header.MagusVersion != "" {
		fmt.Printf("magus:   %s\n", header.MagusVersion)
	}

	fmt.Println()
	if len(secrets) == 0 {
		// Say it plainly rather than printing an empty heading. "No credential reads" is a
		// real audit answer, and the reader must be able to tell it apart from "not recorded".
		// Same noun as the populated branch below, and as the rest of the CLI surface: this
		// line used to say "no credential was resolved", which read as a different fact.
		fmt.Println("secrets: no credential reads during this run")
	} else {
		fmt.Printf("secrets: %d credential read(s)\n", len(secrets))
		for _, e := range secrets {
			where := e.Project
			if e.Target != "" {
				where += " " + e.Target
			}
			fmt.Printf("  %s  %-28s %s\n", time.UnixMilli(e.Ts).Format("15:04:05"), where, e.Text)
		}
	}
	if secretsOnly {
		return nil
	}

	fmt.Println()
	fmt.Printf("events:  %d\n", len(events))
	for _, e := range events {
		if e.Kind == journal.KindOutput {
			continue // the captured bytes belong to `query output <ref>`, not here
		}
		detail := e.Text
		if e.Status != "" {
			detail = strings.TrimSpace(e.Status + " " + detail)
		}
		fmt.Printf("  %s  %-9s %s\n", time.UnixMilli(e.Ts).Format("15:04:05"), e.Kind, detail)
	}
	return nil
}

type outputMetaRecord struct {
	magus.OutputDescriptor
	Invocation   *magus.Invocation   `json:"invocation,omitempty"`
	ClassDigests []cache.ClassDigest `json:"class_digests,omitempty"`
}

// showOutputMeta renders a stored run's IDENTITY rather than its output: what ran,
// how it ended, which invocation produced it, and the digests of its cache key's
// component classes - the machine-comparable half of the works-on-my-machine story
// (two machines compare digests to learn WHICH class disagrees; `describe target
// --cache --against` then names the exact line).
func showOutputMeta(ctx context.Context, m *magus.Magus, ref string, out OutputOptions) error {
	desc, err := m.OutputDescriptorByRef(ref)
	if err != nil {
		return reportRefLookupError(ctx, m, ref, err)
	}
	var digests []cache.ClassDigest
	lines, lerr := m.OutputKeyInputs(ref)
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
		return emitFormatted(out, outputMetaRecord{OutputDescriptor: desc, Invocation: inv, ClassDigests: digests})
	case outputName:
		fmt.Println(desc.Ref)
		return nil
	}
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
		// The cache key pins a tree STATE, never a commit - this is the one place
		// that names one, by comparing the descriptor's revision against HEAD now.
		// PROVENANCE only, never instruction: on a cache HIT, recordOutput never runs,
		// so the descriptor still carries the revision of whichever run FIRST minted
		// this key - which can differ from HEAD even though the current tree
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
		return nil
	}
	fmt.Println("\nkey components:")
	for _, d := range digests {
		noun := "lines"
		if d.Count == 1 {
			noun = "line"
		}
		fmt.Printf("  %-16s %s  %d %s\n", d.Class, d.Digest, d.Count, noun)
	}
	return nil
}

// reportRefLookupError renders the standard output-ref resolution failures (ambiguous prefix,
// missing/aged-out, or an unexpected error) as a coded diagnostic + exit code. On the
// missing-ref path it also prints a best-effort suggestion inverting the ref back to the
// workspace target(s) that could have minted it - see printIdentifyRefSuggestion. m may be
// nil at call sites with no loaded Magus in scope; the suggestion is then skipped, not
// attempted against a nil receiver.
func reportRefLookupError(ctx context.Context, m *magus.Magus, ref string, err error) error {
	var amb *cache.AmbiguousRefError
	switch {
	case errors.As(err, &amb):
		fmt.Fprintf(os.Stderr, "magus query: %s\n", types.DiagnosticErrorf(types.OutputRefAmbiguous, "%s", amb.Error()).Error())
		return errSilent{exitCode: 2}
	case errors.Is(err, fs.ErrNotExist):
		// Name the stores consulted when the lookup knows them: a foreign ref that was
		// never published reads exactly like a mistyped one otherwise. Render the message
		// ONCE - RefNotFoundError.Error() already includes "consulted: <stores>".
		msg := fmt.Sprintf("no stored output for ref %q. It may have aged out of the cache, or the ref is mistyped; re-run the target to regenerate it.", ref)
		var missing *cache.RefNotFoundError
		if errors.As(err, &missing) {
			msg = missing.Error()
		}
		fmt.Fprintf(os.Stderr, "magus query: %s\n", types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg).Error())
		printIdentifyRefSuggestion(ctx, m, ref)
		return errSilent{exitCode: 2}
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
		fmt.Fprintln(os.Stderr, "No target in this workspace keys to that ref at the current tree, which means the run that printed it had different inputs (a different commit, uncommitted change, or environment).")
		fmt.Fprintf(os.Stderr, "Once you know which target it should be: %s\n", clihint.DescribeTargets.With("<target>", "--cache", "--against", ref))
	case 1:
		fmt.Fprintln(os.Stderr, "Nothing has produced it here, but this workspace would print it for:")
		fmt.Fprintf(os.Stderr, "  %s\n", m.RefMatchCommand(matches[0]))
	default:
		fmt.Fprintln(os.Stderr, "Nothing has produced it here, but this workspace would print it for any of:")
		for _, mt := range matches {
			fmt.Fprintf(os.Stderr, "  %s\n", m.RefMatchCommand(mt))
		}
	}
	// Reachable from every branch, not just the zero-match one: even a matched target
	// may be nondeterministic or expensive enough that the exact bytes from whoever
	// already has them beat a local re-run.
	fmt.Fprintf(os.Stderr, "If someone else has it, they can share it with: %s\n", clihint.QueryOutput.With(ref, "--publish"))
}

// openOutputInViewer builds the viewer URL and opens a browser; --print emits the
// URL instead. It warns when the link nears browser URL-length limits.
func openOutputInViewer(desc magus.OutputDescriptor, events []journal.Event, inv magus.Invocation, keyDigests string, o outputRefOpts) error {
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
		fmt.Fprintf(os.Stderr, "magus query: this link is %d KB, near or past what Safari and older\n", len(openURL)/1024)
		fmt.Fprintf(os.Stderr, "Firefox accept in a URL (Chrome is fine). If the page does not load, pipe it instead:\n")
		fmt.Fprintf(os.Stderr, "  magus query output %s | less. Continuing.\n", desc.Ref)
	}
	if o.printURL {
		fmt.Println(openURL)
		return nil
	}
	fmt.Fprintf(os.Stderr, "opening the log viewer for %s; the output rides in the link fragment and never leaves your machine.\n", desc.Ref)
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "magus query: could not open a browser (%v). Re-run with --print to get the URL.\n", err)
		return errSilent{exitCode: 1}
	}
	return nil
}

func explainCmd(ctx context.Context, root string, args []string) error {
	var xf *gen.ExplainFlags
	pos, err := cmdParse("explain", args, func(fs *flag.FlagSet) {
		xf = gen.BindExplain(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus explain <node-id-or-name> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeExplainDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The argument is a node ID (target:pkg/foo:build) or a name that resolves")
			fmt.Fprintln(os.Stderr, "to one (build). Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "magus explain: requires a node ID or name")
		return errSilent{exitCode: 2}
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	seedsSymbols := knowledge.SeedsSymbols(pos[0])
	g, err := loadKnowledgeGraph(ctx, root, xf.Refresh, xf.Global, seedsSymbols)
	if err != nil {
		return err
	}
	out, ok := g.Explain(pos[0])
	if !ok {
		// A bare name does not seed symbols, so explain resolved it against a graph that
		// provably held no code symbols. Reporting that as "no node matches" is how a
		// real symbol comes to look nonexistent - but only when the input could have
		// named one, so a typo'd `kind:target` still gets the absent verdict it deserves.
		reason, gaps := symbolCoverage(ctx, root, pos[0], seedsSymbols)
		ans := types.Answer(false, reason, gaps)
		fmt.Fprintf(os.Stderr, "magus explain: no node matches %q\n", pos[0])
		printVerdict(os.Stderr, ans, clihint.Refs.With(pos[0]))
		return exitForVerdict(ans.Verdict)
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		fmt.Println(out.Node.ID)
		return nil
	}

	fmt.Print(render.ExplainText(out))

	// Complementary deep-link: focus this node in the live Graph Explorer with a
	// blast view (the console's own analogue of `magus explain`). Symbol nodes are
	// excluded from the live full graph the explorer loads, so a link to one would
	// open to an empty focus - omit it. The link is always printed for other kinds;
	// the daemon may not be up when the browser opens it, hence the hint.
	if out.Node.Kind != types.KindSymbol {
		link := liveExplorerLink(url.GraphLinkOpts{View: "blast", Node: out.Node.ID})
		fmt.Printf("\nView in Graph Explorer: %s\n", link)
		fmt.Printf("%s\n", authHint)
		fmt.Printf("(start the magus daemon if the graph does not load)\n")
	}
	return nil
}

func pathCmd(ctx context.Context, root string, args []string) error {
	var pf *gen.PathFlags
	pos, err := cmdParse("path", args, func(fs *flag.FlagSet) {
		pf = gen.BindPath(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus path <a> <b> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgePathDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Each argument is a node ID or a name that resolves to one.")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		fmt.Fprintln(os.Stderr, "magus path: requires two node IDs or names")
		return errSilent{exitCode: 2}
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	g, err := loadKnowledgeGraph(ctx, root, pf.Refresh, pf.Global, knowledge.SeedsSymbols(pos[0]) || knowledge.SeedsSymbols(pos[1]))
	if err != nil {
		return err
	}
	out, ok := g.Path(pos[0], pos[1])
	if !ok {
		fmt.Fprintf(os.Stderr, "magus path: could not resolve %q or %q to a node\n", pos[0], pos[1])
		return errSilent{exitCode: 2}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	}

	fmt.Print(render.PathText(out))
	return nil
}

// splitCSV splits a comma-separated flag value, trimming blanks.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
