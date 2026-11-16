package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/embedtable"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/oci"
	"github.com/egladman/magus/types"
)

// The registry artifact, its media types and its transport live in
// internal/graph/knowledge (publish.go); this file is the verbs' flags and wiring.

// graphRegistryTimeout bounds the whole exchange for every registry verb in this binary.
const graphRegistryTimeout = knowledge.RegistryTimeout

// readToken takes the registry token from STDIN; spell.go's verbs read theirs the same way.
func readToken(ctx context.Context) (types.Secret, error) { return knowledge.ReadToken(ctx) }

func graphPush(ctx context.Context, root string, args []string) error {
	var ref, user string
	var refresh, table bool
	_, err := commandParse("graph push", args, func(fs *flag.FlagSet) {
		fs.StringVar(&ref, "ref", "", "the artifact to push to, as <registry>/<repository>:<tag> (required); with -table, a <registry>/<repository> that defaults to "+embedtable.Repository)
		fs.StringVar(&user, "username", "", "the registry username; the token is read from stdin")
		fs.BoolVar(&refresh, "refresh", false, "rebuild the graph before pushing instead of exporting what is cached")
		fs.BoolVar(&table, "table", false, "publish the embedding table from the cache instead of the graph, tagged with its pin, write-once")
		command.NegatedBoolVar(fs, &refresh, "refresh")
		command.NegatedBoolVar(fs, &table, "table")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph push [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Publish this workspace's knowledge graph to a container registry as an OCI")
			fmt.Fprintln(os.Stderr, "artifact, so collaborators can `magus graph pull` it instead of rebuilding.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The destination is never derived: --ref names it, tag included, and the")
			fmt.Fprintln(os.Stderr, "magusfile is where a workspace declares one, beside the registries its")
			fmt.Fprintln(os.Stderr, "images use. One invocation writes one tag; a caller that wants several")
			fmt.Fprintln(os.Stderr, "calls this once per tag, where its own naming vocabulary lives.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The token is read from STDIN, the way `docker login --password-stdin` takes")
			fmt.Fprintln(os.Stderr, "one, so it never lands in a process listing or a run log. Whatever resolves")
			fmt.Fprintln(os.Stderr, "it (magus\\secret.read, a CI secret, a password manager) stays the caller's")
			fmt.Fprintln(os.Stderr, "choice; this never learns which.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A newly created package is PRIVATE until someone makes it public; until then")
			fmt.Fprintln(os.Stderr, "`magus graph pull` cannot read it anonymously.")
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

	var rec knowledge.RegistryOutput
	if table {
		if refresh {
			return errors.New("graph push: -refresh rebuilds the graph, and -table publishes no graph")
		}
		rec, err = knowledge.TablePush(ctx, ref, user, registryHTTP())
	} else {
		rec, err = pushGraph(ctx, root, ref, user, refresh)
	}
	if err != nil || opts.Format == outputText {
		return err
	}
	return emitRecord(opts, rec, []string{rec.Ref})
}

// pushGraph publishes the knowledge graph to ref, rebuilding it first under refresh.
func pushGraph(ctx context.Context, root, ref, user string, refresh bool) (knowledge.RegistryOutput, error) {
	return knowledge.PushGraph(ctx, ref, user, func() ([]oci.Blob, error) {
		if _, err := loadKnowledgeGraph(ctx, root, refresh, false, true /* includeSymbols */); err != nil {
			return nil, err
		}
		// Off the workspace's own root, not the caller's --root argument, which may be empty
		// or name a directory inside the workspace; the store the build just wrote is the one
		// that root resolves to.
		ws, err := inspectWorkspace(ctx, root)
		if err != nil {
			return nil, err
		}
		cacheDir, err := magus.ResolveCacheDir(ws.Root(), workspace.WithLoadedConfig(globalCfg))
		if err != nil {
			return nil, fmt.Errorf("graph push: %w", err)
		}
		return knowledge.StoreLayers(cacheDir)
	})
}

// seedFromPublishedGraph points the workspace's knowledge store at the published artifact,
// so the rebuild that follows fetches a missing shard instead of recomputing it. It fetches
// nothing itself: the store asks, shard by shard and by fingerprint, for only what it lacks.
//
// It ANNOUNCES ITSELF BEFORE any request, naming the host the build is about to reach. This
// is the only place magus talks to a network the user did not ask it to talk to: it fires
// from a branch switch, through the VCS refresh hook, and an unexplained pause there is
// indistinguishable from a hang. Saying so afterwards is too late to be the explanation,
// and saying it at debug level says it to nobody.
//
// The OUTCOME is best-effort and quiet. No published graph, no network, a private package,
// a tag nobody pushed: each means the graph is built locally, which is what would have
// happened anyway. Only the attempt is loud, because only the attempt costs the user
// something they did not ask for.
//
// Does nothing unless knowledge.published_ref names an artifact. Opt-in per repository,
// because reading a graph decides what magus answers about this tree.
func seedFromPublishedGraph(ctx context.Context, ws types.Inspector) {
	ref := globalCfg.Knowledge.PublishedRef
	if ref == "" {
		return
	}
	src, err := knowledge.GraphReference(ref, knowledge.GraphFloatingTag)
	if err != nil {
		// A ref nobody can parse is a misconfiguration, not a quiet miss: the user asked
		// for this pull by writing the key, so the key being wrong is worth their
		// attention even though the build carries on without it.
		interactive.Hint(ctx, fmt.Sprintf("knowledge.published_ref %q does not parse, so no graph was pulled: %v", ref, err))
		return
	}

	slog.InfoContext(ctx, fmt.Sprintf("fetching the published knowledge graph from %s (knowledge.published_ref; unset it to build locally)", src.Registry),
		attr.Notice(""), attr.Component("magus"))
	// The timeout rides on the HTTP client rather than a ctx bounded here, because the
	// requests happen later, inside the build, and a deadline started now would expire
	// against whatever else that build has to do first.
	client := &oci.Client{HTTP: &http.Client{Timeout: graphRegistryTimeout}}
	workspacegraph.UsePublishedShards(ws, workspacegraph.PublishedShards(client, src, knowledge.GraphArtifactType, slog.Default()))
}

func graphPull(ctx context.Context, root string, args []string) error {
	var ref, out string
	var table bool
	_, err := commandParse("graph pull", args, func(fs *flag.FlagSet) {
		fs.StringVar(&ref, "ref", "", "the artifact to pull, as <registry>/<repository>:<tag> (required); with -table, a <registry>/<repository> that defaults to "+embedtable.Repository)
		fs.StringVar(&out, "out", "", "write the graph here instead of stdout")
		fs.BoolVar(&table, "table", false, "fetch the embedding table the binary pins into the cache instead of the graph")
		command.NegatedBoolVar(fs, &table, "table")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph pull [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Fetch a knowledge graph someone published with `magus graph push`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A PUBLIC artifact needs no credentials at all. The graph is written to")
			fmt.Fprintln(os.Stderr, "stdout unless --out names a file, so it composes with anything that reads")
			fmt.Fprintln(os.Stderr, "the node-link JSON `magus graph export -o json` emits.")
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

	if table {
		if out != "" {
			return errors.New("graph pull: -table writes the table to the cache, so --out names nothing")
		}
		rec, err := knowledge.TablePull(ctx, ref, registryHTTP())
		if err != nil || opts.Format == outputText {
			return err
		}
		return emitRecord(opts, rec, []string{rec.Ref})
	}
	if ref == "" {
		return knowledge.NoGraphDestinationError("pull")
	}
	if out == "" && opts.Format != outputText {
		return exit.Usagef("magus graph pull: -o needs --out, since stdout is the graph")
	}
	src, err := knowledge.GraphReference(ref, knowledge.GraphFloatingTag)
	if err != nil {
		return err
	}
	// No credentials on purpose: a public artifact is the case this exists for, and a
	// client that quietly used an ambient token would work for whoever built it and fail
	// for everyone they handed it to.
	client := &oci.Client{HTTP: &http.Client{Timeout: graphRegistryTimeout}}
	ctx, cancel := context.WithTimeout(ctx, graphRegistryTimeout)
	defer cancel()

	raw, err := knowledge.PullMergedGraph(ctx, client, src, deriveSourceBase(ctx, root))
	if err != nil {
		return fmt.Errorf("graph pull: %w", err)
	}
	if out == "" {
		_, err := os.Stdout.Write(raw)
		return err
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		return fmt.Errorf("graph pull: %w", err)
	}
	slog.InfoContext(ctx, fmt.Sprintf("pulled %s into %s (%d bytes)", src, out, len(raw)), attr.Notice(""))
	if opts.Format != outputText {
		return emitRecord(opts, knowledge.RegistryOutput{Ref: src.String(), Bytes: len(raw), Path: out}, []string{src.String()})
	}
	return nil
}
