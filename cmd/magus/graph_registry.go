package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus/internal/oci"
	"github.com/egladman/magus/vcs"
)

// The knowledge graph as something a team SHARES rather than each member rebuilds.
//
// A container registry is the store because it is already the one every collaborator can
// reach: it is content-addressed, it serves a public repository to an anonymous reader,
// and anyone who can clone the repository can already see its packages. Nothing new to
// host, nothing new to authenticate against.
//
// The artifact is one blob with a media type of our own, not an image. A registry that
// understands OCI 1.1 stores it happily; one that insists on images would reject it, and
// that refusal is the honest answer rather than something to work around.

const (
	// graphArtifactType is what the manifest says the artifact IS. A puller checks it
	// before reading the layer, so a tag someone else's tooling wrote to fails loudly
	// instead of yielding bytes that merely parse as JSON.
	graphArtifactType = "application/vnd.magus.knowledge-graph.v1+json"
	// graphLayerMediaType is what the layer itself is. Deliberately uncompressed: the
	// point of publishing is that anyone can read it, and a plain JSON layer is one
	// unauthenticated GET away from usable without knowing how magus framed it.
	graphLayerMediaType = "application/vnd.magus.knowledge-graph.v1+json"
	// graphArtifactName is the repository suffix under the owner's namespace.
	graphArtifactName = "knowledge-graph"
	// graphFloatingTag is what a plain pull reads and every push moves.
	graphFloatingTag = "latest"
)

// graphRegistryTimeout bounds the whole exchange. A registry that has not answered by
// then is one a build should stop waiting on, and the graph is an optimization: no push
// and no pull is ever the thing that must succeed.
const graphRegistryTimeout = 2 * time.Minute

// graphDestinationUndeclared is what a push or pull says when nobody named a destination.
//
// NOTHING here derives a registry from the origin remote. A published artifact is a
// decision about where this workspace's knowledge goes, and deriving it would mean a
// `graph push` in a fork silently addressing the fork's own namespace, or a `graph pull`
// reading whatever happens to sit at a host nobody chose. The magusfile is where a
// registry is declared, beside the ones the images already use, so adding one stays a
// one-line change in one file.
func graphDestinationUndeclared(verb string) error {
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

// graphTags is the tag set one push writes: the SHORT COMMIT, the floating tag, and
// whatever --tag named.
//
// The short commit is the same tag cd.yaml's per-commit image publishes (magusfile's
// `commit()`, which is vcs\commit().short), so a graph and the image built from the same
// merge into main are addressable by one string. A reader holding a commit can ask for
// the graph of exactly that tree rather than whatever `latest` has since become.
//
// The floating tag is always written too, because a reader who passes no tag must get
// something, and "the most recent push" is the only answer that stays true.
func graphTags(commit, extra string) []string {
	var tags []string
	seen := map[string]bool{}
	for _, t := range append([]string{commit, graphFloatingTag}, strings.Split(extra, ",")...) {
		t = strings.TrimSpace(t)
		if t == "" || t == "unknown" || seen[t] {
			continue
		}
		seen[t] = true
		tags = append(tags, t)
	}
	return tags
}

// headShortCommit is the revision this push describes, in the same spelling the image
// tags use, or "" when the backend cannot say. Distinct from shortCommit, which truncates
// an id it was handed rather than resolving one.
func headShortCommit(ctx context.Context, root string) string {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return ""
	}
	res, err := vcs.Resolve(ctx, ws.Root(), "", ws.VCSOptions())
	if err != nil || res.VCS == nil {
		return ""
	}
	meta, err := res.VCS.Metadata(ctx, ws.Root())
	if err != nil {
		return ""
	}
	return meta.Short
}

func graphPush(ctx context.Context, root string, args []string) error {
	var ref, tag, user string
	var refresh bool
	_, err := cmdParse("graph push", args, func(fs *flag.FlagSet) {
		fs.StringVar(&ref, "ref", "", "the artifact to push to, as <registry>/<repository>:<tag> (required)")
		fs.StringVar(&user, "username", "", "the registry username; the token is read from stdin")
		fs.StringVar(&tag, "tag", "", "an extra tag to write beside latest, repeatable or comma-separated")
		fs.BoolVar(&refresh, "refresh", false, "rebuild the graph before pushing instead of exporting what is cached")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph push [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Publish this workspace's knowledge graph to a container registry as an OCI")
			fmt.Fprintln(os.Stderr, "artifact, so collaborators can `magus graph pull` it instead of rebuilding.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The destination is never derived: --ref names it, and the magusfile is")
			fmt.Fprintln(os.Stderr, "where a workspace declares one, beside the registries its images use.")
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
	return graphPushTo(ctx, root, ref, tag, user, refresh)
}

// graphPushTo is the push itself, shared with `graph build --push` so the two cannot
// drift into publishing different bytes to different places.
func graphPushTo(ctx context.Context, root, ref, tag, user string, refresh bool) error {
	if ref == "" {
		return graphDestinationUndeclared("push")
	}
	dest, err := graphReference(ref, graphFloatingTag)
	if err != nil {
		return err
	}
	raw, nodes, edges, err := graphExportBytes(ctx, root, refresh)
	if err != nil {
		return err
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

	commit := headShortCommit(ctx, root)
	ctx, cancel := context.WithTimeout(ctx, graphRegistryTimeout)
	defer cancel()
	for _, t := range graphTags(commit, tag) {
		at := dest
		at.Tag = t
		if err := client.Push(ctx, at, raw, graphArtifactType, graphLayerMediaType); err != nil {
			return fmt.Errorf("graph push: %w", err)
		}
		fmt.Fprintf(os.Stderr, "pushed %s (%d nodes, %d edges, %d bytes)\n", at, nodes, edges, len(raw))
	}
	return nil
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
		return graphDestinationUndeclared("pull")
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

	raw, err := client.Pull(ctx, src, graphArtifactType)
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

// graphExportBytes produces the node-link JSON a reader would have exported locally,
// WITH the two things that make the published copy worth fetching.
//
// The symbol shards are included (loadKnowledgeGraph's last argument), which the plain
// `graph export` leaves out because they can dwarf the domain graph. Here that size is
// the point: the SCIP indexes are the expensive half to build, they are never committed,
// and a collaborator who has to reindex to use the graph has been handed the cheap half.
//
// The git history rides along in the same output (types.KnowledgeVCS per file: commit
// count, last commit, last author, last modified), so the published graph answers
// ownership and churn questions without the puller holding the repository. Nothing here
// passes the reproducible flag, which is what would strip it.
func graphExportBytes(ctx context.Context, root string, refresh bool) (raw []byte, nodes, edges int, err error) {
	g, err := loadKnowledgeGraph(ctx, root, refresh, false, true /* includeSymbols */)
	if err != nil {
		return nil, 0, 0, err
	}
	out := g.Output()
	out.SourceBaseURL = deriveSourceBase(ctx, root)
	raw, err = json.Marshal(out)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("encode graph: %w", err)
	}
	return raw, out.NodeCount, out.EdgeCount, nil
}
