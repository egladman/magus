package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/url"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
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

// splitQueryNegations pulls the query grammar's negation terms (-kind:op, -docker) out
// of args before flag parsing. A negation shares the flag prefix, so stdlib flag.Parse
// rejects it as an unknown flag: the documented syntax was unreachable from the CLI. A
// dash token is a term rather than a flag exactly when the full query flag set does not
// know its name; registered flags, their values, "-name=value" spellings (left for
// flag.Parse to report, since the grammar spells fields with a colon), and everything
// after "--" pass through untouched.
func splitQueryNegations(args []string) (kept, negations []string) {
	fs := flag.NewFlagSet("query-prescan", flag.ContinueOnError)
	gen.BindFlags(fs, &globalCfg)
	bindDisplayFlags(fs)
	gen.BindQuery(fs, gen.QueryDefaults{URL: defaultLogViewerURL})
	flags, positionals := partitionFlags(fs, args)
	kept = make([]string, 0, len(args))
	for i := 0; i < len(flags); i++ {
		a := flags[i]
		if len(a) >= 2 && a[0] == '-' && !strings.ContainsRune(a, '=') {
			name := strings.TrimLeft(a, "-")
			// flag.Parse answers -h and -help itself without registering them, so a lookup
			// misses them; read as negations they searched the graph for "-h".
			if name == "h" || name == "help" {
				kept = append(kept, a)
				continue
			}
			if f := fs.Lookup(name); f == nil {
				negations = append(negations, a)
				continue
			} else if !flagIsBool(f) && i+1 < len(flags) {
				// A value flag's value may itself start with a dash; consume it
				// paired, the same way partitionFlags did, so it is never read
				// as a negation.
				kept = append(kept, a, flags[i+1])
				i++
				continue
			}
		}
		kept = append(kept, a)
	}
	return append(kept, positionals...), negations
}

func queryCmd(ctx context.Context, root string, args []string) error {
	args, negations := splitQueryNegations(args)
	var qf *gen.QueryFlags
	pos, err := cmdParse("query", args, func(fs *flag.FlagSet) {
		qf = gen.BindQuery(fs, gen.QueryDefaults{URL: defaultLogViewerURL})
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus query <terms> [flags]")
			fmt.Fprintln(os.Stderr, "       magus query output <ref> [-o json|jsonl] [--open] [--attempts] [--identity] [--publish]")
			fmt.Fprintln(os.Stderr, "       magus query output --stdin")
			fmt.Fprintln(os.Stderr, "       magus query invocation <id> [-o json] [--secrets]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeQueryDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Terms are free text plus field matchers: kind=spell, project=pkg/foo,")
			fmt.Fprintln(os.Stderr, "relation=uses, id=build. Negate with != (kind!=op) and match a regex")
			fmt.Fprintln(os.Stderr, "with =~ (id=~build$). The : grammar (kind:spell, -kind:op) still works.")
			fmt.Fprintln(os.Stderr, "  magus query kind=spell go")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintf(os.Stderr, "`%s <ref>` retrieves one target run's captured output by its\n", hint.QueryOutput.Leaf())
			fmt.Fprintln(os.Stderr, "output ref (out1a2b3c), shown when the target ran:")
			fmt.Fprintf(os.Stderr, "  %-38s print the exact bytes (pipe anywhere)\n", hint.QueryOutput.With("out1a2b3c"))
			fmt.Fprintf(os.Stderr, "  %-38s the descriptor + output as a record\n", hint.QueryOutput.With("out1a2b3c", "-o json"))
			fmt.Fprintf(os.Stderr, "  %-38s open it in the browser log viewer\n", hint.QueryOutput.With("out1a2b3c", "--open"))
			fmt.Fprintf(os.Stderr, "  %-38s list the ref's stored attempts\n", hint.QueryOutput.With("out1a2b3c", "--attempts"))
			fmt.Fprintf(os.Stderr, "  %-38s the run's identity + cache-key digests\n", hint.QueryOutput.With("out1a2b3c", "--identity"))
			fmt.Fprintf(os.Stderr, "  %-38s print records from -o jsonl; writes nothing\n", hint.QueryOutput.With("--stdin"))
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintf(os.Stderr, "`%s <id>` reads one run's journal back by the id shown as\n", hint.QueryInvocation.Leaf())
			fmt.Fprintf(os.Stderr, "`inv:` in %s:\n", hint.QueryOutput.With("<ref>", "--identity"))
			fmt.Fprintf(os.Stderr, "  %-38s the run's events, newest last\n", hint.QueryInvocation.With("invmsm3vcou1"))
			fmt.Fprintf(os.Stderr, "  %-38s only the credential reads (audit)\n", hint.QueryInvocation.With("invmsm3vcou1", "--secrets"))
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "--open respects the BROWSER environment variable to pick the browser")
			fmt.Fprintln(os.Stderr, "(e.g. BROWSER=firefox); otherwise it uses your desktop's default handler.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	// Appended rather than interleaved: the grammar joins terms into one string, so
	// relative order between a positive term and a negation carries no meaning.
	pos = append(pos, negations...)

	// Output-reference retrieval is an EXPLICIT subcommand (`magus query output <ref>`), not a
	// shape-routed positional, so a search term can never collide with a ref id.
	if qf.Stdin {
		if len(pos) != 1 || pos[0] != hint.QueryOutput.Leaf() {
			return usagef("magus query: --stdin applies only to `%s --stdin`, which takes no ref", hint.QueryOutput)
		}
		if qf.Attempts || qf.Identity || qf.Publish || qf.Open || qf.Print {
			// A record on stdin is untrusted: printed, never published or handed to a browser.
			return usagef("magus query output --stdin: --attempts, --identity, --publish, --open and --print act on a stored run; a record on stdin is only printed")
		}
		outOpts, oerr := outputOptionsOrDefault()
		if oerr != nil {
			return oerr
		}
		if outOpts.Format != FormatText {
			return usagef("magus query output --stdin: -o %s is not supported; it prints each record's output, and the records themselves are already the structured form", outOpts.Format)
		}
		return printOutputRecords(os.Stdin, os.Stdout, noticeLines{ctx: ctx, level: slog.LevelInfo})
	}
	if len(pos) >= 1 && pos[0] == hint.QueryOutput.Leaf() {
		if len(pos) != 2 {
			slog.ErrorContext(ctx, fmt.Sprintf("expected exactly one ref (e.g. %s)", hint.QueryOutput.With("out1a2b3c")), attr.Notice(hint.QueryOutput.String()))
			return errSilent{exitCode: 2}
		}
		ref := pos[1]
		if !cache.LooksLikeRef(ref) {
			if !trail.ValidRef(ref) {
				msg := fmt.Sprintf("%q is not an output ref (expected out<hex> for a run, or a stored payload such as grd<hex> or mcp<hex>)", ref)
				slog.ErrorContext(ctx, types.DiagnosticErrorf(types.OutputRefMalformed, "%s", msg).Error(), attr.Notice("magus query output"))
				return errSilent{exitCode: 2}
			}
			if qf.Attempts || qf.Identity || qf.Publish || qf.Open || qf.Print {
				return usagef("magus query output: %s is a stored payload, not a run; --attempts, --identity, --publish, --open and --print apply only to out<hex> refs", ref)
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
			return errSilent{exitCode: 2}
		}
		return queryOutputRef(ctx, root, ref, outputRefOpts{open: qf.Open, printURL: qf.Print, viewerBase: qf.URL, attempts: qf.Attempts, identity: qf.Identity, publish: qf.Publish, out: outOpts})
	}
	// `magus query invocation <id>`, the sibling of `query output <ref>`, and explicit for
	// the same reason: an id is shape-routed nowhere, so a search term cannot collide with one.
	if len(pos) >= 1 && pos[0] == hint.QueryInvocation.Leaf() {
		if len(pos) != 2 {
			slog.ErrorContext(ctx, fmt.Sprintf("expected exactly one invocation id (e.g. %s)", hint.QueryInvocation.With("invmsm3vcou1")),
				attr.Notice(hint.QueryInvocation.String()))
			return errSilent{exitCode: 2}
		}
		inv := pos[1]
		if !cache.LooksLikeInvocationID(inv) {
			slog.ErrorContext(ctx, fmt.Sprintf("%q is not an invocation id (expected inv<id>, e.g. invmsm3vcou1); a run prints one as `inv:` in %s",
				inv, hint.QueryOutput.With("<ref> --identity")), attr.Notice("magus query invocation"))
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
		slog.ErrorContext(ctx, fmt.Sprintf("%q is an invocation id, not a graph term. Read it with: %s",
			pos[0], hint.QueryInvocation.With(pos[0])), attr.Notice("magus query"))
		return errSilent{exitCode: 2}
	}
	if qf.Open || qf.Print || qf.Attempts || qf.Identity || qf.Publish {
		// --open/--print/--attempts/--identity only apply to `query output <ref>`. Set on a graph
		// search, they were a mistake; stop rather than silently ignore them.
		slog.ErrorContext(ctx, fmt.Sprintf("--open/--print/--attempts/--identity/--publish apply only to `%s <ref>`. To open the knowledge graph in a browser, use `%s`.", hint.QueryOutput, hint.GraphExport.With("--open")), attr.Notice("magus query"))
		return errSilent{exitCode: 2}
	}
	if len(pos) == 0 && qf.Kind == "" {
		slog.ErrorContext(ctx, "requires search terms", attr.Notice("magus query"))
		return errSilent{exitCode: 2}
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	input := strings.Join(pos, " ")
	for _, k := range splitCSV(qf.Kind) {
		input += " kind=" + k
	}

	read := graphRead{Input: input, Budget: qf.Budget, Nearest: opts.Format == FormatText}
	var res queryResult
	if qf.Refresh || qf.Global || !askServer(ctx, root, readQuery, &read, &res) {
		ws, err := openForRead(ctx, root)
		if err != nil {
			return err
		}
		if res, err = searchGraph(ctx, ws, globalCfg, read, qf.Refresh, qf.Global); err != nil {
			return err
		}
	}
	out := res.Out

	stop := traceFromContext(ctx).phase("query.next")
	nx := newNextGate(root)
	next := nx.served(hint.NextForQuery(out))
	stop()

	// The status is decided once, for every format: a rule that held only for text would
	// leave the callers most likely to branch on it (`-o name` in a chain, `-o json` in a
	// script) reading a blind spot as an emptiness.
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		if err := emitFormatted(opts, queryWithNext{KnowledgeQueryOutput: out, Next: next}); err != nil {
			return err
		}
		// Before exitForQuery, which passes a POPULATED answer whatever its verdict: a
		// short list from a stale index is exactly the shape that reads as complete.
		// The notice goes to stderr so the record on stdout stays parseable.
		if err := reportIndexStaleness(noticeLines{ctx: ctx, level: slog.LevelError}, out.Answer); err != nil {
			return err
		}
		return exitForQuery(out)
	case outputName:
		if err := emitItemNames(out.Matches, func(m types.KnowledgeMatch) string { return m.ID }); err != nil {
			return err
		}
		if err := reportIndexStaleness(noticeLines{ctx: ctx, level: slog.LevelError}, out.Answer); err != nil {
			return err
		}
		return exitForQuery(out)
	}

	fmt.Printf("query: %s\n", out.Query)
	fmt.Printf("matches: %d  (neighborhood budget %d)\n\n", out.MatchCount, out.Budget)
	if out.MatchCount == 0 {
		printVerdict(os.Stdout, out.Answer, hint.Refs.With("<name>"))
		emitNearest(ctx, res.Nearest)
		if err := reportIndexStaleness(os.Stdout, out.Answer); err != nil {
			return err
		}
		// An empty result set is still a legitimate answer to a search, so this exits 0 on
		// `absent` and only fails on `unknown`. See exitForQuery.
		return exitForQuery(out)
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
	printNext(os.Stdout, nx, next)
	return reportIndexStaleness(os.Stdout, out.Answer)
}

// outputRefOpts carries the options for `magus query output <ref>`.
type outputRefOpts struct {
	open       bool          // open the browser log viewer instead of printing
	printURL   bool          // with open, print the URL instead of launching a browser
	viewerBase string        // log viewer base URL
	attempts   bool          // list the ref's stored executions instead of printing output
	identity   bool          // show the run's identity (descriptor, lineage, key digests)
	publish    bool          // upload this run's output to the remote as a signed bundle
	out        OutputOptions // -o: text prints raw bytes, json/yaml prints the descriptor record
}

// queryOutputRef retrieves a target's captured output by reference id (or unique prefix). The
// default prints the exact bytes to stdout (pipe-friendly); -o json/yaml prints the descriptor
// record; --open hands it to the browser log viewer. The bytes never leave the machine: --open
// rides them in a URL fragment, exactly like `magus graph export --open`.
func queryOutputRef(ctx context.Context, root, ref string, o outputRefOpts) error {
	// What this verb emits by default is the raw bytes a run captured, and bytes have no
	// fields to template and no identity to name. Only the record WRAPPING them has a
	// shape, which is why json, jsonl and yaml work and the rest are refused rather than
	// silently answered with the bytes: the accepted-and-ignored failure -o exists to
	// avoid, and worse here than elsewhere because the fallback looks like real output.
	switch o.out.Format {
	case FormatText, FormatJSON, FormatJSONL, FormatYAML:
	default:
		return usagef("magus query output: -o %s is not supported; a captured log has no fields to render, so json, jsonl and yaml (the record around the bytes) are the only structured forms", o.out.Format)
	}
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
		published, perr := m.PublishOutput(ctx, ref)
		if perr != nil {
			if errors.Is(perr, fs.ErrNotExist) {
				// Not the generic lookup path: its hint suggests --publish, which is
				// the command that just failed.
				msg := fmt.Sprintf("no stored output for ref %q to publish; it may have aged out of the cache, or the ref is mistyped", ref)
				slog.ErrorContext(ctx, types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg).Error(), attr.Notice("magus query output"))
				return errSilent{exitCode: 2}
			}
			return fmt.Errorf("magus query output: publish %s: %w", ref, perr)
		}
		fmt.Printf("published %s to the remote cache\n", published)
		fmt.Printf("a teammate with the same trust set can now run: %s\n", hint.QueryOutput.With(published))
		return nil
	}
	if o.open {
		// The viewer ingests a magus.viewer.v1alpha1 Journal, so hand it the ref's display events:
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
		return openOutputInViewer(ctx, desc, events, inv, keyDigests, o)
	}
	// Remote-aware: a ref unknown locally may have been published from CI or a
	// teammate's machine, so the print path consults the remote bundle namespace
	// before reporting it missing.
	if o.out.Format != FormatText {
		rec, rerr := m.OutputRecordByRef(ctx, ref)
		if rerr != nil {
			return reportRefLookupError(ctx, m, ref, rerr)
		}
		if o.out.Format == FormatJSONL {
			// A slice, or writeJSONL would stream one of the record's list fields.
			return emitFormatted(o.out, []types.StoredOutput{rec})
		}
		return emitFormatted(o.out, rec)
	}
	data, desc, err := m.OutputByRefRemote(ctx, ref)
	if err != nil {
		return reportRefLookupError(ctx, m, ref, err)
	}
	// On stderr, after the bytes: stdout stays pipe-clean, and a reader who has just
	// looked at what a run PRODUCED is one step from wanting to run it. Only worth
	// saying when the descriptor records a target to reproduce.
	if desc.Target != "" {
		interactive.Hint(ctx, "reproduce this invocation here with `"+hint.X.With(ref)+"`")
	}
	_, err = os.Stdout.Write(data) // default: verbatim bytes, pipe-clean
	return err
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
func queryTrailPayload(ctx context.Context, root, ref string, out OutputOptions) error {
	switch out.Format {
	case FormatText, FormatJSON, FormatYAML:
	default:
		return usagef("magus query output: -o %s is not supported; a stored payload has no fields to render, so json and yaml are the only structured forms", out.Format)
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	data, err := m.PayloadByRef(ref)
	if errors.Is(err, fs.ErrNotExist) {
		msg := err.Error() + "; the activity trail may have rotated it out, or the ref is mistyped"
		slog.ErrorContext(ctx, types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg).Error(), attr.Notice("magus query output"))
		return errSilent{exitCode: 2}
	}
	if err != nil {
		return fmt.Errorf("magus query output: read %s: %w", ref, err)
	}
	if out.Format == FormatJSON || out.Format == FormatYAML {
		return emitFormatted(out, trailPayloadRecord{Ref: ref, Output: string(data)})
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
func queryInvocation(ctx context.Context, root, inv string, secretsOnly bool, out OutputOptions) error {
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
		return emitNames([]string{header.ID})
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
		// Same noun as the populated branch below, and as the rest of the CLI: this
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
func showOutputIdentity(ctx context.Context, m *magus.Magus, ref string, out OutputOptions) error {
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
		return emitFormatted(out, outputIdentityRecord{OutputDescriptor: desc, Invocation: inv, ClassDigests: digests})
	case outputName:
		return emitNames([]string{desc.Ref})
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
// workspace target(s) that could have minted it; see printIdentifyRefSuggestion. m may be
// nil at call sites with no loaded Magus in scope; the suggestion is then skipped, not
// attempted against a nil receiver.
func reportRefLookupError(ctx context.Context, m *magus.Magus, ref string, err error) error {
	var amb *cache.AmbiguousRefError
	switch {
	case errors.As(err, &amb):
		slog.ErrorContext(ctx, types.DiagnosticErrorf(types.OutputRefAmbiguous, "%s", amb.Error()).Error(), attr.Notice("magus query"))
		return errSilent{exitCode: 2}
	case errors.Is(err, fs.ErrNotExist):
		// Name the stores consulted when the lookup knows them: a foreign ref that was
		// never published reads exactly like a mistyped one otherwise. Render the message
		// ONCE: RefNotFoundError.Error() already includes "consulted: <stores>".
		msg := fmt.Sprintf("no stored output for ref %q. It may have aged out of the cache, or the ref is mistyped; re-run the target to regenerate it.", ref)
		var missing *cache.RefNotFoundError
		if errors.As(err, &missing) {
			msg = missing.Error()
		}
		slog.ErrorContext(ctx, types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg).Error(), attr.Notice("magus query"))
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
		slog.InfoContext(ctx, "No target in this workspace keys to that ref at the current tree, which means the run that printed it had different inputs (a different commit, uncommitted change, or environment).\n"+
			"Once you know which target it should be: "+hint.DescribeTargets.With("<target>", "--cache", "--against", ref), attr.Notice(""))
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
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		slog.ErrorContext(ctx, "requires a node ID or name", attr.Notice("magus explain"))
		return errSilent{exitCode: 2}
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	read := graphRead{Input: pos[0]}
	var res explainResult
	if xf.Refresh || xf.Global || !askServer(ctx, root, readExplain, &read, &res) {
		ws, err := openForRead(ctx, root)
		if err != nil {
			return err
		}
		if res, err = explainNode(ctx, ws, globalCfg, read, xf.Refresh, xf.Global); err != nil {
			return err
		}
	}
	if !res.Found {
		slog.ErrorContext(ctx, fmt.Sprintf("no node matches %q", pos[0]), attr.Notice("magus explain"))
		missLines := noticeLines{ctx: ctx, level: slog.LevelError}
		printVerdict(missLines, res.Answer, hint.Refs.With(pos[0]))
		emitNearest(ctx, res.Nearest)
		return exitForVerdict(res.Answer.Verdict)
	}
	out := res.Out

	stop := traceFromContext(ctx).phase("explain.next")
	nx := newNextGate(root)
	next := nx.served(hint.NextForExplain(out))
	stop()

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, explainWithNext{
			KnowledgeExplainOutput: out, Next: next, AgentSessions: sessionContact(root, out.Node),
		})
	case outputName:
		return emitNames([]string{out.Node.ID})
	}

	fmt.Print(resolutionNote(pos[0], out))
	fmt.Print(render.ExplainText(out))
	printSessionContact(os.Stdout, root, out.Node)

	// Complementary deep-link: focus this node in the live Graph Explorer with a
	// blast view (the console's own analogue of `magus explain`). Symbol nodes are
	// excluded from the live full graph the explorer loads, so a link to one would
	// open to an empty focus; omit it. The link is always printed for other kinds;
	// the server may not be up when the browser opens it, hence the hint.
	if out.Node.Kind != types.KindSymbol {
		link := liveExplorerLink(url.GraphLinkOpts{View: "blast", Node: out.Node.ID})
		fmt.Printf("\nView in Graph Explorer: %s\n", link)
		fmt.Printf("%s\n", authHint(link))
		fmt.Printf("(start the magus server if the graph does not load)\n")
	}
	printNext(os.Stdout, nx, next)
	return reportIndexStaleness(os.Stdout, res.Answer)
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
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		slog.ErrorContext(ctx, "requires two node IDs or names", attr.Notice("magus path"))
		return errSilent{exitCode: 2}
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	ws, err := openForRead(ctx, root)
	if err != nil {
		return err
	}
	g, err := loadWorkspaceGraph(ctx, ws, globalCfg, pf.Refresh, pf.Global, knowledge.SeedsLazyLayer(pos[0]) || knowledge.SeedsLazyLayer(pos[1]))
	if err != nil {
		return err
	}
	out, ok := g.Path(pos[0], pos[1])
	if !ok {
		slog.ErrorContext(ctx, fmt.Sprintf("could not resolve %q or %q to a node", pos[0], pos[1]), attr.Notice("magus path"))
		return errSilent{exitCode: 2}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	}

	fmt.Print(render.PathText(out))
	return nil
}

// A running server answers query, explain and refs for the CLI. It holds the workspace
// open with its process warm, so the read skips process start, the workspace load and the
// tool probes. It still runs the same cache-first build a local read runs, whose stamps
// decide freshness against the tree as it is at that moment; nothing is trusted to a file
// watcher, so the answer is the one a local read would give. A server answers only for the
// client's exact build (proc refuses another) and only when the client reads under the
// configuration the server reads under (serveGraphRead declines otherwise). No server, a
// refusal and a broken socket all fall back to the local read.
const (
	readQuery   = "query"
	readExplain = "explain"
	readRefs    = "refs"
)

// graphRead is one read's parameters, as a client sends them to a server.
type graphRead struct {
	Input  string `json:"input"`
	Budget int    `json:"budget,omitempty"`
	// Nearest asks query for the closest node id when nothing matches. Only the text view
	// prints one, and finding it is a fuzzy pass over the whole graph.
	Nearest bool `json:"nearest,omitempty"`
	// Occurrences asks refs for the occurrence read as well as the reference list.
	Occurrences bool `json:"occurrences,omitempty"`
	// Config is readConfigDigest of the configuration the client reads under.
	Config string `json:"config"`
	// Env is envDigests of the client's environment. An index freshness verdict reads the
	// process environment (the cache key folds in allowed variables, and PATH picks the
	// binary a version probe runs), so the server compares the variables its verdict
	// depends on, without any value crossing the socket.
	Env map[string]string `json:"env"`
}

// queryResult is what one `magus query` search found: everything its output formats
// render apart from the breadcrumbs, which depend on who is asking.
type queryResult struct {
	Out     types.KnowledgeQueryOutput `json:"out"`
	Nearest string                     `json:"nearest,omitempty"`
}

// explainResult is what one `magus explain` lookup found.
type explainResult struct {
	Out   types.KnowledgeExplainOutput `json:"out"`
	Found bool                         `json:"found"`
	// Answer judges the lookup: on a miss, whether the node is absent or merely unseen; on
	// a hit, whether the index it came from is current. explain's record carries no answer
	// of its own, so this is the one every other client builds.
	Answer  types.KnowledgeAnswer `json:"answer"`
	Nearest string                `json:"nearest,omitempty"`
}

func searchGraph(ctx context.Context, ws graphWorkspace, cfg config.Config, read graphRead, refresh, global bool) (queryResult, error) {
	tr := traceFromContext(ctx)
	seeded := knowledge.SeedsLazyLayer(read.Input)
	var g *knowledge.Graph
	var out types.KnowledgeQueryOutput
	var err error
	if seeded && !global {
		// The answer loadWorkspaceGraph then Query give, ranked from the symbol names index so only
		// the shards the answer touches are decoded.
		stop := tr.phase("query.load_and_search")
		if refresh {
			seedFromPublishedGraph(ctx, ws)
		}
		out, g, err = magus.QueryKnowledgeGraph(ctx, ws, ws.Root(), cfg, refresh, read.Input, read.Budget, slog.Default())
		stop()
		if err != nil {
			return queryResult{}, err
		}
	} else {
		stop := tr.phase("query.load_graph")
		g, err = loadWorkspaceGraph(ctx, ws, cfg, refresh, global, seeded)
		stop()
		if err != nil {
			return queryResult{}, err
		}
		stop = tr.phase("query.search")
		out = g.Query(read.Input, read.Budget)
		stop()
	}
	stop := tr.phase("query.coverage")
	out.Answer = knowledge.Answer(read.Input, out.MatchCount > 0, measureSymbolCoverage(ctx, ws, cfg, read.Input, seeded))
	stop()
	res := queryResult{Out: out}
	if out.MatchCount == 0 && read.Nearest {
		res.Nearest = g.NearestNode(read.Input)
	}
	return res, nil
}

func explainNode(ctx context.Context, ws graphWorkspace, cfg config.Config, read graphRead, refresh, global bool) (explainResult, error) {
	tr := traceFromContext(ctx)
	seeded := knowledge.SeedsLazyLayer(read.Input)
	stop := tr.phase("explain.load_graph")
	g, err := loadWorkspaceGraph(ctx, ws, cfg, refresh, global, seeded)
	stop()
	if err != nil {
		return explainResult{}, err
	}
	stop = tr.phase("explain.search")
	out, found := g.Explain(read.Input)
	stop()
	// A bare name does not seed symbols, so a miss was resolved against a graph that held
	// no code symbols. Reporting that as "no node matches" is how a real symbol comes to
	// look nonexistent, so the answer says so when the input could have named one, and a
	// typo'd `kind:target` still gets the absent verdict it deserves.
	stop = tr.phase("explain.coverage")
	cov := measureSymbolCoverage(ctx, ws, cfg, read.Input, seeded)
	if found {
		// One symbol: only its own language's indexes could have missed a site of it.
		cov = cov.Narrow(g.SymbolLanguage(read.Input))
	}
	res := explainResult{Out: out, Found: found, Answer: knowledge.Answer(read.Input, found, cov)}
	stop()
	if !found {
		res.Nearest = g.NearestNode(read.Input)
	}
	return res, nil
}

// graphWorkspace is what a graph read needs of its workspace: the root, and the Inspector
// facts a rebuild reads. The *magus.Magus a server holds satisfies it, and so does the
// *magus.LazyWorkspace a CLI read opens, which evaluates the workspace only if the stored
// graph turns out stale.
type graphWorkspace interface {
	types.Inspector
	Root() string
}

// openForRead opens the workspace for a graph read no server answered, lazily: the read
// gets a handle that evaluates the workspace on the first call that needs the model, and
// a read the stored graph answers makes none. Measured 2026-10-02 on this repository, the
// eager open was 425ms of a warm `magus query`'s 590ms. When startup already opened the
// workspace, that handle answers, as it always did.
//
// The open, when it comes, is the one openWorkspaceForRead makes: the cache-backed handle
// whose remote shard backing a rebuild pushes to.
func openForRead(ctx context.Context, root string) (graphWorkspace, error) {
	stop := traceFromContext(ctx).phase("read.open")
	defer stop()
	if m, ok := loadedMagus(); ok {
		return m, nil
	}
	wsRoot, err := magus.FindRoot(root)
	if err != nil {
		return nil, err
	}
	return magus.NewLazyWorkspace(wsRoot, func(ctx context.Context) (*magus.Magus, error) {
		ws, err := openWorkspaceForRead(ctx, root)
		if err != nil {
			return nil, err
		}
		return asMagus(ws)
	}), nil
}

// openWorkspaceForRead opens the workspace eagerly for a read that needs the whole
// set of workspace operations (refs, which reads symbols and classifies files). It is the same
// Open-loaded handle startup used to preload for these verbs, so the local read is the one
// it always was; the preload just no longer runs for a read a server answers.
func openWorkspaceForRead(ctx context.Context, root string) (types.WorkspaceRepository, error) {
	// inspectWorkspace reuses this handle when the open succeeded, and reports why when it
	// did not, so the error has one place to come from.
	_, _ = loadMagus(ctx, root)
	return inspectWorkspace(ctx, root)
}

// asMagus is the *magus.Magus behind a workspace handle. Open and Inspect both return one,
// so anything else is a programming error worth naming rather than a nil to chase.
func asMagus(ws types.WorkspaceRepository) (*magus.Magus, error) {
	m, ok := ws.(*magus.Magus)
	if !ok {
		return nil, fmt.Errorf("the workspace handle is a %T, not a *magus.Magus", ws)
	}
	return m, nil
}

// fullWorkspace is the whole set of workspace operations behind a graph workspace, opening a lazy
// one: for the reads that need more than the Inspector (the global graph unions
// WorkspaceRepository handles).
func fullWorkspace(ctx context.Context, ws graphWorkspace) (types.WorkspaceRepository, error) {
	switch w := ws.(type) {
	case types.WorkspaceRepository:
		return w, nil
	case *magus.LazyWorkspace:
		return w.Magus(ctx)
	}
	return nil, fmt.Errorf("%T is not a workspace", ws)
}

// askServer has a running server answer verb into reply and reports whether it did. A
// false return is never a failure: no server, another build, another configuration and a
// broken socket all mean the caller reads locally, at debug level only.
func askServer(ctx context.Context, root, verb string, read *graphRead, reply any) bool {
	if !globalCfg.Server.Enabled {
		return false
	}
	stop := traceFromContext(ctx).phase("read.server")
	defer stop()
	sock := os.Getenv(proc.SocketEnv)
	if sock == "" {
		var ok bool
		if sock, ok = proc.LookupServerSocket(ctx); !ok {
			return false
		}
	}
	wsRoot := resolveRootOrEmpty(root)
	read.Config = readConfigDigest(globalCfg)
	read.Env = envDigests()
	if wsRoot == "" || read.Config == "" {
		return false
	}
	if err := proc.Read(ctx, sock, version, wsRoot, verb, read, reply); err != nil {
		slog.With(attr.Component("magus")).DebugContext(ctx, "the server did not answer this read; reading locally", slog.String("error", err.Error()))
		return false
	}
	return true
}

// serveGraphRead answers one CLI graph read for m, a workspace the server holds, reading
// under cfg, the configuration the server resolves for m's root now.
func serveGraphRead(ctx context.Context, m *magus.Magus, cfg config.Config, verb string, request []byte) ([]byte, error) {
	var read graphRead
	if err := json.Unmarshal(request, &read); err != nil {
		return nil, fmt.Errorf("magus server: decode the %s read: %w", verb, err)
	}
	if digest := readConfigDigest(cfg); digest == "" || read.Config != digest {
		return nil, fmt.Errorf("%w: the client reads under another configuration", proc.ErrNotAdoptable)
	}
	// m was opened under the configuration its root had then. The cache directory is the
	// setting m itself reads from that copy, so one moved since means a workspace the
	// server must reopen before it answers for it.
	if dir, err := magus.ResolveCacheDir(m.Root(), magus.WithLoadedConfig(cfg)); err != nil || dir != m.CacheDir() {
		return nil, fmt.Errorf("%w: the workspace was opened under an older configuration", proc.ErrNotAdoptable)
	}
	for _, name := range m.SymbolFreshnessEnv() {
		v, set := os.LookupEnv(name)
		got, sent := read.Env[name]
		if set != sent || (set && got != envDigest(v)) {
			return nil, fmt.Errorf("%w: the client's %s differs from the server's", proc.ErrNotAdoptable, name)
		}
	}
	var res any
	var err error
	switch verb {
	case readQuery:
		res, err = searchGraph(ctx, m, cfg, read, false, false)
	case readExplain:
		res, err = explainNode(ctx, m, cfg, read, false, false)
	case readRefs:
		var g *knowledge.Graph
		if g, err = loadRefsGraph(ctx, m, cfg, false, read.Input); err == nil {
			res = lookupRefs(ctx, m, cfg, g, read)
		}
	default:
		return nil, fmt.Errorf("%w: no %q read", proc.ErrNotAdoptable, verb)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(res)
}

// envDigests maps each variable in this process's environment to envDigest of its value.
func envDigests() map[string]string {
	out := make(map[string]string)
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		out[name] = envDigest(value)
	}
	return out
}

func envDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}

// readConfigDigest fingerprints the configuration a graph read depends on. The fields
// cleared here shape the process (logging, concurrency, the server and its endpoints) and
// never an answer; a difference anywhere else makes a server decline. "" when cfg cannot
// be encoded, which no server accepts.
func readConfigDigest(cfg config.Config) string {
	cfg.Telemetry = config.Telemetry{}
	cfg.Server = config.Server{}
	cfg.MCP = config.MCP{}
	cfg.Console = config.Console{}
	cfg.Report = config.Report{}
	cfg.Log = config.Log{}
	cfg.Hints = config.Hints{}
	cfg.Jobs = config.Jobs{}
	cfg.Concurrency = 0
	cfg.ConcurrencyProfile = ""
	cfg.Broker = ""
	cfg.ShutdownGrace = 0
	cfg.MaxFailures = 0
	cfg.TargetTimeout = 0
	cfg.StallTimeout = 0
	cfg.DryRun = false
	cfg.DefaultCharms = nil
	raw, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
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
