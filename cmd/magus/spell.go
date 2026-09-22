package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/opencontainers/go-digest"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/oci"
	"github.com/egladman/magus/internal/secret"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// spellCmd is the home of a spell as an artifact: what a workspace publishes so another
// pulls it by digest. Authoring a spell stays `magus init spell`.
func spellCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		spellUsage()
		return flag.ErrHelp
	}
	switch args[0] {
	case "build":
		return spellBuild(ctx, root, args[1:])
	case "push":
		return spellPush(ctx, root, args[1:])
	case "pull":
		return spellPull(ctx, root, args[1:])
	case "ls":
		return spellLs(ctx, root, args[1:])
	default:
		return usagef("magus spell: unknown subcommand %q (want build, push, pull or ls)", args[0])
	}
}

func spellUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus spell <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  build    pack a spell directory and print the manifest digest a push would produce")
	fmt.Fprintln(os.Stderr, "  push     publish a spell directory as an OCI artifact and print its pinned reference")
	fmt.Fprintln(os.Stderr, "  pull     fetch and verify a published spell, into the cache or a directory")
	fmt.Fprintln(os.Stderr, "  ls       list a spell repository's tags")
}

// registryHTTP is the transport every spell verb uses. A variable so a test can point it
// at a TLS fixture whose certificate the default transport does not trust.
var registryHTTP = func() *http.Client { return &http.Client{Timeout: graphRegistryTimeout} }

// spellBuildResult is what build and push report. Digest is the manifest digest, the
// one value a pinned reference names; the rest describe the bytes behind it.
type spellBuildResult struct {
	Digest      digest.Digest     `json:"digest"`
	Reference   string            `json:"reference,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Layer       digest.Digest     `json:"layer"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations"`
}

func spellBuild(ctx context.Context, root string, args []string) error {
	var out, source string
	pos, err := cmdParse("spell build", args, func(fs *flag.FlagSet) {
		fs.StringVar(&out, "out", "", "also write the packed layer (an uncompressed tar) to this file")
		fs.StringVar(&source, "source", "", "the org.opencontainers.image.source URL; default: the VCS remote")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus spell build <dir> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Pack a spell directory exactly as push would and print the manifest digest the")
			fmt.Fprintln(os.Stderr, "push would produce. Nothing touches the network, so CI can compare a checkout")
			fmt.Fprintln(os.Stderr, "against a published pin.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("magus spell build: want <dir>, got %d argument(s)", len(pos))
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}
	content, res, err := buildSpell(ctx, root, pos[0], source)
	if err != nil {
		return fmt.Errorf("spell build: %w", err)
	}
	if out != "" {
		if err := os.WriteFile(out, content.Layers[0].Payload, 0o644); err != nil {
			return fmt.Errorf("spell build: %w", err)
		}
	}
	if opts.Format == FormatText || opts.Format == FormatName {
		return emitNames([]string{res.Digest.String()})
	}
	return emitFormatted(opts, res)
}

// buildSpell packs dir with the provenance of the revision holding it. build and push
// share it so the digest build prints is the one push writes.
func buildSpell(ctx context.Context, root, dir, source string) (oci.Content, spellBuildResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return oci.Content{}, spellBuildResult{}, err
	}
	driver, err := spellVCS(ctx, root, abs)
	if err != nil {
		return oci.Content{}, spellBuildResult{}, err
	}
	prov, err := remotespell.ReadProvenance(ctx, driver, abs)
	if err != nil {
		return oci.Content{}, spellBuildResult{}, err
	}
	if source != "" {
		prov.Source = source
	}
	reporter, ok := driver.(types.TrackedFileReporter)
	if !ok {
		return oci.Content{}, spellBuildResult{}, fmt.Errorf("%s is under a VCS that cannot report tracked files", abs)
	}
	content, err := remotespell.Build(ctx, abs, reporter, prov)
	if err != nil {
		return oci.Content{}, spellBuildResult{}, err
	}
	_, d, err := content.Manifest()
	if err != nil {
		return oci.Content{}, spellBuildResult{}, err
	}
	layer := content.Layers[0].Payload
	return content, spellBuildResult{
		Digest:      d,
		Layer:       digest.FromBytes(layer),
		Size:        int64(len(layer)),
		Annotations: content.Annotations,
	}, nil
}

func spellPush(ctx context.Context, root string, args []string) error {
	var user, source string
	var tags stringList
	pos, err := cmdParse("spell push", args, func(fs *flag.FlagSet) {
		fs.StringVar(&user, "username", "", "the registry username; the password is then read from stdin, overriding spells.registries")
		fs.Var(&tags, "tag", "another tag to write the same manifest under; repeatable")
		fs.StringVar(&source, "source", "", "the org.opencontainers.image.source URL; default: the VCS remote")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus spell push <dir> <registry>/<repository>:<tag> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Publish a spell directory as an OCI artifact and print its pinned reference,")
			fmt.Fprintln(os.Stderr, "<registry>/<repository>@sha256:<digest>. Only files the VCS tracks are packed,")
			fmt.Fprintln(os.Stderr, "so one commit publishes to one digest on every machine. Each --tag is one more")
			fmt.Fprintln(os.Stderr, "manifest PUT; no blob is uploaded twice.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Credentials come from the spells.registries entry for the host, resolved")
			fmt.Fprintln(os.Stderr, "through the workspace's secret provider, unless --username is given, in which")
			fmt.Fprintln(os.Stderr, "case the password is read from STDIN.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usagef("magus spell push: want <dir> <registry>/<repository>:<tag>, got %d argument(s)", len(pos))
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}
	dest, err := oci.ParseReference(pos[1])
	if err != nil {
		return err
	}
	if dest.Tag == "" || dest.Digest != "" {
		return usagef("magus spell push: %s must name a tag and no digest", pos[1])
	}
	content, res, err := buildSpell(ctx, root, pos[0], source)
	if err != nil {
		return fmt.Errorf("spell push: %w", err)
	}
	client, err := registryClient(ctx, root, dest.Registry, user)
	if err != nil {
		return fmt.Errorf("spell push: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, graphRegistryTimeout)
	defer cancel()
	d, err := client.Push(ctx, dest, content, tags...)
	if err != nil {
		return fmt.Errorf("spell push: %w", err)
	}
	pinned := dest
	pinned.Tag, pinned.Digest = "", d
	res.Reference = pinned.String()
	res.Tags = append([]string{dest.Tag}, tags...)
	if opts.Format == FormatText || opts.Format == FormatName {
		return emitNames([]string{res.Reference})
	}
	return emitFormatted(opts, res)
}

// spellPullResult is what pull reports: the pinned reference it verified, and where the
// verified files are.
type spellPullResult struct {
	Reference string        `json:"reference"`
	Digest    digest.Digest `json:"digest"`
	Dir       string        `json:"dir"`
}

func spellPull(ctx context.Context, root string, args []string) error {
	var user string
	pos, err := cmdParse("spell pull", args, func(fs *flag.FlagSet) {
		fs.StringVar(&user, "username", "", "the registry username; the password is then read from stdin, overriding spells.registries")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus spell pull <registry>/<repository>(:<tag>|@sha256:<digest>) [<dir>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Fetch a published spell, verify its manifest and layer digests, and print the")
			fmt.Fprintln(os.Stderr, "pinned reference it resolved to, then the directory holding its files: <dir>")
			fmt.Fprintln(os.Stderr, "when given (it must be empty or absent), otherwise the user cache.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		return usagef("magus spell pull: want <reference> [<dir>], got %d argument(s)", len(pos))
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}
	ref, err := oci.ParseReference(pos[0])
	if err != nil {
		return err
	}
	client, err := registryClient(ctx, root, ref.Registry, user)
	if err != nil {
		return fmt.Errorf("spell pull: %w", err)
	}
	dst := ""
	if len(pos) == 2 {
		if dst, err = filepath.Abs(pos[1]); err != nil {
			return fmt.Errorf("spell pull: %w", err)
		}
	}
	res, err := pullSpell(ctx, client, ref, "", dst)
	if err != nil {
		return fmt.Errorf("spell pull: %w", err)
	}
	switch opts.Format {
	case FormatText:
		return emitNames([]string{res.Reference, res.Dir})
	case FormatName:
		return emitNames([]string{res.Reference})
	default:
		return emitFormatted(opts, res)
	}
}

// pullSpell pins ref, resolves it through the verified cache under cacheRoot ("" for the
// user cache), and unpacks it into dst when one is given.
func pullSpell(ctx context.Context, client *oci.Client, ref oci.Reference, cacheRoot, dst string) (spellPullResult, error) {
	ctx, cancel := context.WithTimeout(ctx, graphRegistryTimeout)
	defer cancel()
	pinned, err := remotespell.Pin(ctx, client, ref)
	if err != nil {
		return spellPullResult{}, err
	}
	pinned.Tag = ""
	dir, err := remotespell.Resolve(ctx, remotespell.Ref{Import: pinned.String(), OCI: pinned}, remotespell.Options{
		CacheRoot: cacheRoot,
		Client:    client.HTTP,
		Username:  client.Username,
		Password:  client.Password,
	})
	if err != nil {
		return spellPullResult{}, err
	}
	if dst != "" {
		if err := remotespell.Unpack(dir, dst); err != nil {
			return spellPullResult{}, err
		}
		dir = dst
	}
	return spellPullResult{Reference: pinned.String(), Digest: pinned.Digest, Dir: dir}, nil
}

// spellLsResult is the -o json shape of ls.
type spellLsResult struct {
	Repository string   `json:"repository"`
	Tags       []string `json:"tags" jsonl:"primary"`
}

func spellLs(ctx context.Context, root string, args []string) error {
	var user string
	pos, err := cmdParse("spell ls", args, func(fs *flag.FlagSet) {
		fs.StringVar(&user, "username", "", "the registry username; the password is then read from stdin, overriding spells.registries")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus spell ls <registry>/<repository> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "List a spell repository's tags, one per line, following the registry's")
			fmt.Fprintln(os.Stderr, "pagination to the end.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("magus spell ls: want <registry>/<repository>, got %d argument(s)", len(pos))
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}
	repo, err := oci.ParseRepository(pos[0])
	if err != nil {
		return err
	}
	client, err := registryClient(ctx, root, repo.Registry, user)
	if err != nil {
		return fmt.Errorf("spell ls: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, graphRegistryTimeout)
	defer cancel()
	tags, err := client.Tags(ctx, repo)
	if err != nil {
		return fmt.Errorf("spell ls: %w", err)
	}
	switch opts.Format {
	case FormatText, FormatName:
		return emitNames(tags)
	default:
		return emitFormatted(opts, spellLsResult{Repository: repo.String(), Tags: tags})
	}
}

// registryClient builds the client for one registry host. --username wins, with the
// password read from stdin; otherwise the spells.registries entry for host supplies a
// username and a secret reference; with neither the client is anonymous.
func registryClient(ctx context.Context, root, host, username string) (*oci.Client, error) {
	c := &oci.Client{HTTP: registryHTTP()}
	if username != "" {
		pass, err := readToken()
		if err != nil {
			return nil, err
		}
		c.Username, c.Password = username, pass
		return c, nil
	}
	if _, ok := globalCfg.Spells.Registry(host); !ok {
		return c, nil
	}
	// The workspace is loaded only when a credential is needed: its magusfile is what
	// selects the secret provider the reference resolves through.
	m, err := loadMagus(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("spells.registries %s: load the workspace to reach its secret provider: %w", host, err)
	}
	ctx = m.ContextWithSecrets(ctx)
	user, pass, err := registryCredential(ctx, globalCfg.Spells, host, secret.ResolverFromContext(ctx))
	if err != nil {
		return nil, err
	}
	c.Username, c.Password = user, pass.Reveal()
	return c, nil
}

// registryCredential resolves host's spells.registries entry through r, which registers
// the value for redaction before returning it.
func registryCredential(ctx context.Context, spells config.SpellsConfig, host string, r *secret.Resolver) (string, secret.Value, error) {
	entry, ok := spells.Registry(host)
	if !ok {
		return "", secret.Value{}, fmt.Errorf("spells.registries has no entry for %s", host)
	}
	pass, err := r.Read(ctx, entry.Password)
	if err != nil {
		return "", secret.Value{}, fmt.Errorf("spells.registries %s: %w", host, err)
	}
	return entry.Username, pass, nil
}

// spellVCS resolves the VCS that holds dir, with the workspace's VCS options when a
// workspace loads. A tree no VCS answers for is an error: packing every file on disk
// would make the digest depend on the machine.
func spellVCS(ctx context.Context, root, dir string) (types.VCSDriver, error) {
	opts := types.VCSOptions{}
	if ws, err := inspectWorkspace(ctx, root); err == nil {
		opts = ws.VCSOptions()
	}
	res, err := vcs.Resolve(ctx, dir, "", opts)
	if err != nil {
		return nil, err
	}
	if res.VCS == nil {
		return nil, fmt.Errorf("%s is under no VCS; commit the spell to a repository first", dir)
	}
	return res.VCS, nil
}
