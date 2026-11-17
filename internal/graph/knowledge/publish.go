package knowledge

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/embedtable"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/libs/oci"
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
	// GraphArtifactType is what the manifest says the artifact IS. A puller checks it
	// before reading any layer, so a tag someone else's tooling wrote to fails loudly
	// instead of yielding bytes that merely parse as JSON.
	GraphArtifactType = "application/vnd.magus.knowledge-graph.v1+json"
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
	// GraphFloatingTag is what a plain pull reads and every push moves.
	GraphFloatingTag = "latest"
)

// RegistryTimeout bounds the whole exchange. A registry that has not answered by
// then is one a build should stop waiting on, and the graph is an optimization: no push
// and no pull is ever the thing that must succeed.
const RegistryTimeout = 2 * time.Minute

// NoGraphDestinationError is what a push or pull says when nobody named a destination.
//
// NOTHING here derives a registry from the origin remote. A published artifact is a
// decision about where this workspace's knowledge goes, and deriving it would mean a
// `graph push` in a fork silently addressing the fork's own namespace, or a `graph pull`
// reading whatever happens to sit at a host nobody chose. The magusfile is where a
// registry is declared, beside the ones the images already use, so adding one stays a
// one-line change in one file.
func NoGraphDestinationError(verb string) error {
	return fmt.Errorf("graph %s: no destination, pass --ref <registry>/<repository>:<tag> "+
		"or declare it in the magusfile beside the image registries and run the target that supplies it", verb)
}

// ReadToken takes the registry token from STDIN, the way `docker login --password-stdin`
// does, so a secret never becomes a flag argument that lands in a process listing, a
// shell history, or a run log. The caller resolving it is the magusfile, through
// magus\secret.read, which is what keeps the credential source pluggable: a workspace
// picks its provider and this never learns which one. The token never passed through
// this run's resolver, so it is registered there for redaction.
func ReadToken(ctx context.Context) (types.Secret, error) {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return types.Secret{}, fmt.Errorf("read token from stdin: %w", err)
	}
	tok := types.NewSecret(strings.TrimSpace(string(raw)))
	if tok.IsZero() {
		return types.Secret{}, fmt.Errorf("no token on stdin, pipe one in the way graph-generate does (magus\\secret.read resolves it)")
	}
	secret.ResolverFromContext(ctx).Register(tok)
	return tok, nil
}

// RegistryOutput is the record a push or pull emits.
type RegistryOutput struct {
	Ref    string `json:"ref"`
	Table  bool   `json:"table"`
	Pushed bool   `json:"pushed,omitempty"`
	Shards int    `json:"shards"`
	Bytes  int    `json:"bytes"`
	Path   string `json:"path,omitempty"`
}

// PushGraph publishes the knowledge graph to ref. layers builds the artifact's content,
// after the destination is known to parse and before any credential is read.
func PushGraph(ctx context.Context, ref, user string, layers func() ([]oci.Blob, error)) (RegistryOutput, error) {
	var none RegistryOutput
	if ref == "" {
		return none, NoGraphDestinationError("push")
	}
	// The tag --ref carries is the tag written. Recomputing a tag set here would mean two
	// implementations of tag policy, and the caller's would lose; the magusfile already
	// has channel(), commit() and version(), the same vocabulary the image targets name
	// their tags with, which is what lets a graph and the image from one merge share a
	// string.
	dest, err := GraphReference(ref, GraphFloatingTag)
	if err != nil {
		return none, err
	}
	blobs, err := layers()
	if err != nil {
		return none, err
	}
	var size int
	for _, l := range blobs {
		size += len(l.Data)
	}

	pass, err := ReadToken(ctx)
	if err != nil {
		return none, fmt.Errorf("graph push: %w", err)
	}
	client := &oci.Client{
		HTTP:        &http.Client{Timeout: RegistryTimeout},
		Credentials: oci.Credentials{Username: user, Password: pass},
	}

	ctx, cancel := context.WithTimeout(ctx, RegistryTimeout)
	defer cancel()
	repo := oci.Reference{Registry: dest.Registry, Repository: dest.Repository}
	if _, err := client.PushArtifact(ctx, repo, oci.Artifact{ArtifactType: GraphArtifactType, Blobs: blobs}, dest.Tag); err != nil {
		return none, fmt.Errorf("graph push: %w", err)
	}
	slog.InfoContext(ctx, fmt.Sprintf("pushed %s (%d shards, %d bytes)", dest, len(blobs)-1, size), attr.Notice(""))
	return RegistryOutput{Ref: dest.String(), Pushed: true, Shards: len(blobs) - 1, Bytes: size}, nil
}

// StoreLayers is the published artifact's content: the knowledge store's routing manifest,
// then one layer per shard titled with the content fingerprint the store addresses it by.
//
// One layer per shard is what makes the fetch incremental. A reader takes the routing
// manifest, subtracts the shards it already holds, and asks for the rest by fingerprint;
// nothing here is merged, so a fresh worktree never pays for the whole graph to get the
// half it lacks.
//
// The caller builds the graph first, WITH symbols, so the store at cacheDir is current
// before it is read. The SCIP shards are the expensive half and are never committed,
// which is the whole reason to publish; gen/knowledge-graph.json is a local build output
// that already carries the domain graph, so republishing that alone would add nothing.
func StoreLayers(cacheDir string) ([]oci.Blob, error) {
	export, err := ReadStoreExport(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("graph push: %w", err)
	}
	layers := make([]oci.Blob, 0, len(export.Shards)+1)
	layers = append(layers, oci.Blob{Title: graphStoreLayer, MediaType: graphStoreMediaType, Data: export.Manifest})
	for _, sh := range export.Shards {
		layers = append(layers, oci.Blob{Title: sh.Key, MediaType: graphShardMediaType, Data: sh.Bytes})
	}
	return layers, nil
}

// GraphReference parses the declared destination. A reference with no tag takes the
// floating one: the DESTINATION is the decision worth being explicit about, and which of
// a push's own tags a reader means is not.
func GraphReference(ref, tag string) (oci.Reference, error) {
	if !strings.Contains(ref[strings.LastIndex(ref, "/")+1:], ":") {
		ref += ":" + tag
	}
	return oci.ParseReference(ref)
}

// TablePush publishes the cached embedding table under its pin. The release runs it
// after `magus run table libs/train`; a table that does not hash to the pin is
// refused, never published under a tag of its own.
func TablePush(ctx context.Context, repo, user string, registry *http.Client) (RegistryOutput, error) {
	var none RegistryOutput
	dest, err := embedtable.Reference(repo)
	if err != nil {
		return none, fmt.Errorf("graph push -table: %w", err)
	}
	path, err := embedtable.Options{}.Path()
	if err != nil {
		return none, fmt.Errorf("graph push -table: %w", err)
	}
	pass, err := ReadToken(ctx)
	if err != nil {
		return none, fmt.Errorf("graph push -table: %w", err)
	}
	client := &oci.Client{HTTP: registry, Credentials: oci.Credentials{Username: user, Password: pass}}
	ctx, cancel := context.WithTimeout(ctx, RegistryTimeout)
	defer cancel()
	pushed, err := embedtable.Publish(ctx, client, dest, path)
	if err != nil {
		return none, fmt.Errorf("graph push -table: %w", err)
	}
	rec := RegistryOutput{Ref: dest.String(), Table: true, Pushed: pushed, Path: path}
	if !pushed {
		slog.InfoContext(ctx, fmt.Sprintf("kept %s, already published with the pinned table", dest), attr.Notice(""))
		return rec, nil
	}
	slog.InfoContext(ctx, fmt.Sprintf("pushed %s from %s", dest, path), attr.Notice(""))
	return rec, nil
}

// TablePull fetches the pinned embedding table into the cache, anonymously for the
// reason graph pull is.
func TablePull(ctx context.Context, repo string, registry *http.Client) (RegistryOutput, error) {
	var none RegistryOutput
	src, err := embedtable.Reference(repo)
	if err != nil {
		return none, fmt.Errorf("graph pull -table: %w", err)
	}
	opts := embedtable.Options{}
	path, err := opts.Path()
	if err != nil {
		return none, fmt.Errorf("graph pull -table: %w", err)
	}
	client := &oci.Client{HTTP: registry}
	ctx, cancel := context.WithTimeout(ctx, RegistryTimeout)
	defer cancel()
	if err := embedtable.Pull(ctx, client, src, opts); err != nil {
		return none, fmt.Errorf("graph pull -table: %w", err)
	}
	slog.InfoContext(ctx, fmt.Sprintf("pulled %s into %s", src, path), attr.Notice(""))
	return RegistryOutput{Ref: src.String(), Table: true, Path: path}, nil
}

// PullMergedGraph fetches every shard the artifact names and merges them into the
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
func PullMergedGraph(ctx context.Context, client *oci.Client, src oci.Reference, sourceBase string) ([]byte, error) {
	art, err := client.GetArtifact(ctx, src, GraphArtifactType)
	if err != nil {
		return nil, err
	}
	raw, err := art.Layer(ctx, graphStoreLayer)
	if err != nil {
		return nil, err
	}
	keys, err := ShardKeys(raw)
	if err != nil {
		return nil, err
	}
	g := NewGraph()
	// Sorted by shard name, for the reason the store's own Load sorts: a merge is
	// first-writer-wins, so the order decides which shard supplies a node's provenance,
	// and an unsorted merge yields a different export run to run.
	for _, name := range slices.Sorted(maps.Keys(keys)) {
		b, err := art.Layer(ctx, keys[name])
		if err != nil {
			return nil, fmt.Errorf("shard %q: %w", name, err)
		}
		if err := MergeShardFile(g, b); err != nil {
			return nil, fmt.Errorf("shard %q: %w", name, err)
		}
	}
	out := g.Output()
	out.SourceBaseURL = sourceBase
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode graph: %w", err)
	}
	return body, nil
}
