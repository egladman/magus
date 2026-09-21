package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/oci"
	"github.com/egladman/magus/types"
)

// The knowledge graph as something a team SHARES rather than each member rebuilds.
//
// A container registry is the store because it is already the one every collaborator can
// reach: it is content-addressed, it serves a public repository to an anonymous reader,
// and anyone who can clone the repository can already see its packages. Nothing new to
// host, nothing new to authenticate against.
//
// The artifact is a set of blobs with media types of our own, not an image. A registry
// that understands OCI 1.1 stores it happily; one that insists on images would reject it,
// and that refusal is the honest answer rather than something to work around.
//
// It carries the knowledge store SHARD BY SHARD rather than as one merged graph, because
// the reader's problem is not "fetch the graph" but "fetch the half I am missing". Shards
// are content-addressed by fingerprint, so a reader names the shards it lacks and the
// registry answers with exactly those blobs.

const (
	// graphArtifactType is what the manifest says the artifact IS. A puller checks it
	// before reading any layer, so a tag someone else's tooling wrote to fails loudly
	// instead of yielding bytes that merely parse as JSON.
	graphArtifactType = "application/vnd.magus.knowledge-graph.v1+json"
	// graphStoreMediaType is the knowledge store's routing manifest: shard name to
	// content fingerprint. A reader takes this first, because it is what turns a shard
	// name into the layer title that carries it.
	graphStoreMediaType = "application/vnd.magus.knowledge-store.v1+json"
	// graphShardMediaType is one shard file. Deliberately uncompressed: the point of
	// publishing is that anyone can read it, and a plain JSON layer is one unauthenticated
	// GET away from usable without knowing how magus framed it.
	graphShardMediaType = "application/vnd.magus.knowledge-shard.v1+json"
	// graphStoreLayer titles the routing manifest's layer. Every other layer is titled
	// with a shard's content fingerprint, so this name cannot collide with one.
	graphStoreLayer = "manifest.json"
	// graphFloatingTag is what a plain pull reads and every push moves.
	graphFloatingTag = "latest"
)

// graphRegistryTimeout bounds the whole exchange. A registry that has not answered by
// then is one a build should stop waiting on, and the graph is an optimization: no push
// and no pull is ever the thing that must succeed.
const graphRegistryTimeout = 2 * time.Minute

// errNoGraphDestination is what a push or pull says when nobody named a destination.
//
// NOTHING here derives a registry from the origin remote. A published artifact is a
// decision about where this workspace's knowledge goes, and deriving it would mean a
// `graph push` in a fork silently addressing the fork's own namespace, or a `graph pull`
// reading whatever happens to sit at a host nobody chose. The magusfile is where a
// registry is declared, beside the ones the images already use, so adding one stays a
// one-line change in one file.
func errNoGraphDestination(verb string) error {
	return fmt.Errorf("graph %s: no destination. Pass --ref <registry>/<repository>:<tag>, "+
		"or declare it in the magusfile beside the image registries and run the target that supplies it", verb)
}

// readToken takes the registry token from STDIN, the way `docker login --password-stdin`
// does, so a secret never becomes a flag argument that lands in a process listing, a
// shell history, or a run log. The caller resolving it is the magusfile, through
// magus\secret.read, which is what keeps the credential source pluggable: a workspace
// picks its provider and this never learns which one.
func readToken() (string, error) {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read token from stdin: %w", err)
	}
	tok := strings.TrimSpace(string(raw))
	if tok == "" {
		return "", fmt.Errorf("no token on stdin; pipe one in the way `image-login` does (magus\\secret.read resolves it)")
	}
	return tok, nil
}

func graphPush(ctx context.Context, root string, args []string) error {
	var ref, user string
	var refresh bool
	_, err := cmdParse("graph push", args, func(fs *flag.FlagSet) {
		fs.StringVar(&ref, "ref", "", "the artifact to push to, as <registry>/<repository>:<tag> (required)")
		fs.StringVar(&user, "username", "", "the registry username; the token is read from stdin")
		fs.BoolVar(&refresh, "refresh", false, "rebuild the graph before pushing instead of exporting what is cached")
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
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	if ref == "" {
		return errNoGraphDestination("push")
	}
	// The tag --ref carries is the tag written. Recomputing a tag set here would mean two
	// implementations of tag policy, and the caller's would lose; the magusfile already
	// has channel(), commit() and version(), the same vocabulary the image targets name
	// their tags with, which is what lets a graph and the image from one merge share a
	// string.
	dest, err := graphReference(ref, graphFloatingTag)
	if err != nil {
		return err
	}
	layers, err := graphLayers(ctx, root, refresh)
	if err != nil {
		return err
	}
	var size int
	for _, l := range layers {
		size += len(l.Payload)
	}

	pass, err := readToken()
	if err != nil {
		return fmt.Errorf("graph push: %w", err)
	}
	client := &oci.Client{
		HTTP:     &http.Client{Timeout: graphRegistryTimeout},
		Username: user,
		Password: pass,
	}

	ctx, cancel := context.WithTimeout(ctx, graphRegistryTimeout)
	defer cancel()
	if _, err := client.Push(ctx, dest, graphArtifactType, layers...); err != nil {
		return fmt.Errorf("graph push: %w", err)
	}
	fmt.Fprintf(os.Stderr, "pushed %s (%d shards, %d bytes)\n", dest, len(layers)-1, size)
	return nil
}

// graphLayers is the published artifact's content: the knowledge store's routing manifest,
// then one layer per shard titled with the content fingerprint the store addresses it by.
//
// One layer per shard is what makes the fetch incremental. A reader takes the routing
// manifest, subtracts the shards it already holds, and asks for the rest by fingerprint;
// nothing here is merged, so a fresh worktree never pays for the whole graph to get the
// half it lacks.
//
// The build runs first, WITH symbols, so the store on disk is current before it is read.
// The SCIP shards are the expensive half and are never committed, which is the whole
// reason to publish; gen/knowledge-graph.json is a local build output that already
// carries the domain graph, so republishing that alone would add nothing.
func graphLayers(ctx context.Context, root string, refresh bool) ([]oci.Layer, error) {
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
	cacheDir, err := magus.ResolveCacheDir(ws.Root(), magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return nil, fmt.Errorf("graph push: %w", err)
	}
	export, err := knowledge.ReadStoreExport(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("graph push: %w", err)
	}
	layers := make([]oci.Layer, 0, len(export.Shards)+1)
	layers = append(layers, oci.Layer{Name: graphStoreLayer, MediaType: graphStoreMediaType, Payload: export.Manifest})
	for _, sh := range export.Shards {
		layers = append(layers, oci.Layer{Name: sh.Key, MediaType: graphShardMediaType, Payload: sh.Bytes})
	}
	return layers, nil
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
func seedFromPublishedGraph(ws types.Inspector) {
	ref := globalCfg.Knowledge.PublishedRef
	if ref == "" {
		return
	}
	src, err := graphReference(ref, graphFloatingTag)
	if err != nil {
		// A ref nobody can parse is a misconfiguration, not a quiet miss: the user asked
		// for this pull by writing the key, so the key being wrong is worth their
		// attention even though the build carries on without it.
		interactive.Emit(os.Stderr, fmt.Sprintf("knowledge.published_ref %q does not parse, so no graph was pulled: %v", ref, err))
		return
	}

	fmt.Fprintf(os.Stderr, "magus: fetching the published knowledge graph from %s (knowledge.published_ref; unset it to build locally)\n", src.Registry)
	// The timeout rides on the HTTP client rather than a ctx bounded here, because the
	// requests happen later, inside the build, and a deadline started now would expire
	// against whatever else that build has to do first.
	client := &oci.Client{HTTP: &http.Client{Timeout: graphRegistryTimeout}}
	magus.UsePublishedShards(ws, magus.PublishedShards(client, src, graphArtifactType, slog.Default()))
}

func graphPull(ctx context.Context, root string, args []string) error {
	var ref, out string
	_, err := cmdParse("graph pull", args, func(fs *flag.FlagSet) {
		fs.StringVar(&ref, "ref", "", "the artifact to pull, as <registry>/<repository>:<tag> (required)")
		fs.StringVar(&out, "out", "", "write the graph here instead of stdout")
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
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	if ref == "" {
		return errNoGraphDestination("pull")
	}
	src, err := graphReference(ref, graphFloatingTag)
	if err != nil {
		return err
	}
	// No credentials on purpose: a public artifact is the case this exists for, and a
	// client that quietly used an ambient token would work for whoever built it and fail
	// for everyone they handed it to.
	client := &oci.Client{HTTP: &http.Client{Timeout: graphRegistryTimeout}}
	ctx, cancel := context.WithTimeout(ctx, graphRegistryTimeout)
	defer cancel()

	raw, err := pullMergedGraph(ctx, client, src, root)
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
	fmt.Fprintf(os.Stderr, "pulled %s into %s (%d bytes)\n", src, out, len(raw))
	return nil
}

// graphReference parses the declared destination. A reference with no tag takes the
// floating one: the DESTINATION is the decision worth being explicit about, and which of
// a push's own tags a reader means is not.
func graphReference(ref, tag string) (oci.Reference, error) {
	if !strings.Contains(ref[strings.LastIndex(ref, "/")+1:], ":") {
		ref += ":" + tag
	}
	return oci.ParseReference(ref)
}

// pullMergedGraph fetches every shard the artifact names and merges them into the
// node-link JSON `magus graph export -o json` emits.
//
// The artifact stores shards, not a merged graph, so the merge happens on this side
// rather than costing every publish a second copy of the same content. That makes
// `graph pull` the expensive read by design: it wants the whole graph, where the store
// wants the few shards it is missing.
//
// The symbol shards come too. The SCIP indexes are the expensive half to build, they are
// never committed, and a collaborator who has to reindex to use the graph has been handed
// the cheap half. So does the git history each shard carries (types.KnowledgeVCS per file),
// which is what lets the pulled graph answer ownership and churn without the repository.
func pullMergedGraph(ctx context.Context, client *oci.Client, src oci.Reference, root string) ([]byte, error) {
	art, err := client.Artifact(ctx, src, graphArtifactType)
	if err != nil {
		return nil, err
	}
	raw, err := art.Layer(ctx, graphStoreLayer)
	if err != nil {
		return nil, err
	}
	keys, err := knowledge.ShardKeys(raw)
	if err != nil {
		return nil, err
	}
	g := knowledge.NewGraph()
	// Sorted by shard name, for the reason the store's own Load sorts: a merge is
	// first-writer-wins, so the order decides which shard supplies a node's provenance,
	// and an unsorted merge yields a different export run to run.
	for _, name := range slices.Sorted(maps.Keys(keys)) {
		b, err := art.Layer(ctx, keys[name])
		if err != nil {
			return nil, fmt.Errorf("shard %q: %w", name, err)
		}
		if err := knowledge.MergeShardFile(g, b); err != nil {
			return nil, fmt.Errorf("shard %q: %w", name, err)
		}
	}
	out := g.Output()
	out.SourceBaseURL = deriveSourceBase(ctx, root)
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode graph: %w", err)
	}
	return body, nil
}
