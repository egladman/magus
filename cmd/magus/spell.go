package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/secret"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/oci"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// spellCommand is the home of a spell as an artifact: what a workspace publishes so another
// imports it by registry path. Authoring a spell stays `magus init spell`.
func spellCommand(ctx context.Context, root string, args []string) error {
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
	case "lock":
		return spellLock(ctx, root, args[1:])
	default:
		return exit.Usagef("magus spell: unknown subcommand %q (want build, push, pull, ls or lock)", args[0])
	}
}

func spellUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus spell <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  build    pack a spell directory and print the manifest digest a push would produce")
	fmt.Fprintln(os.Stderr, "  push     publish a spell directory, or one magus ships, as an OCI artifact and print its pinned reference")
	fmt.Fprintln(os.Stderr, "  pull     fetch and verify a published spell into the cache or a directory, or copy out one magus ships")
	fmt.Fprintln(os.Stderr, "  ls       list a spell repository's tags, or the spells magus ships")
	fmt.Fprintln(os.Stderr, "  lock     verify magus.lock against magus.yaml, or rewrite it with --update")
}

// registryHTTP is the transport every spell verb uses. A variable so a test can point it
// at a TLS fixture whose certificate the default transport does not trust.
var registryHTTP = func() *http.Client { return &http.Client{Timeout: knowledge.RegistryTimeout} }

func spellBuild(ctx context.Context, root string, args []string) error {
	var out, source string
	pos, err := commandParse("spell build", args, func(fs *flag.FlagSet) {
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
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return exit.Usagef("magus spell build: want <dir>, got %d argument(s)", len(pos))
	}
	opts, err := output.Parse(global.output)
	if err != nil {
		return err
	}
	content, res, err := remotespell.BuildDir(ctx, spellVCSOptions(ctx, root), pos[0], source)
	if err != nil {
		return fmt.Errorf("spell build: %w", err)
	}
	if out != "" {
		if err := os.WriteFile(out, content.Blobs[0].Data, 0o644); err != nil {
			return fmt.Errorf("spell build: %w", err)
		}
	}
	if opts.Format == outputText || opts.Format == outputName {
		return emitNames(os.Stdout, []string{res.Digest.String()})
	}
	return emitFormatted(opts, res)
}

func spellPush(ctx context.Context, root string, args []string) error {
	var user, source string
	var tags stringList
	pos, err := commandParse("spell push", args, func(fs *flag.FlagSet) {
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
			fmt.Fprintln(os.Stderr, "       magus spell push magus/spell/<name> <registry>/<repository>:<tag>")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Publish a spell magus ships, packed from this binary: the artifact whose digest")
			fmt.Fprintln(os.Stderr, "`magus spell pull magus/spell/<name>` stamps on a copy.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Credentials come from the spells.registries entry for the host, resolved")
			fmt.Fprintln(os.Stderr, "through the workspace's secret provider, unless --username is given, in which")
			fmt.Fprintln(os.Stderr, "case the password is read from STDIN.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return exit.Usagef("magus spell push: want <dir>|magus/spell/<name> <registry>/<repository>:<tag>, got %d argument(s)", len(pos))
	}
	opts, err := output.Parse(global.output)
	if err != nil {
		return err
	}
	dest, err := oci.ParseReference(pos[1])
	if err != nil {
		return err
	}
	if dest.Tag == "" || dest.Digest != "" {
		return exit.Usagef("magus spell push: %s must name a tag and no digest", pos[1])
	}
	var content oci.Artifact
	var res remotespell.BuildResult
	if name, ok := strings.CutPrefix(pos[0], spells.ModulePrefix); ok {
		if source != "" {
			return exit.Usagef("magus spell push %s: --source does not apply; a spell magus ships records %s", pos[0], remotespell.ShippedProvenance(name).Source)
		}
		content, res, err = remotespell.BuildShipped(name)
	} else {
		content, res, err = remotespell.BuildDir(ctx, spellVCSOptions(ctx, root), pos[0], source)
	}
	if err != nil {
		return fmt.Errorf("spell push: %w", err)
	}
	client, err := registryClient(ctx, root, dest.Registry, user)
	if err != nil {
		return fmt.Errorf("spell push: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, knowledge.RegistryTimeout)
	defer cancel()
	repo := oci.Reference{Registry: dest.Registry, Repository: dest.Repository}
	d, err := client.PushArtifact(ctx, repo, content, append([]string{dest.Tag}, tags...)...)
	if err != nil {
		return fmt.Errorf("spell push: %w", err)
	}
	pinned := dest
	pinned.Tag, pinned.Digest = "", d
	res.Reference = pinned.String()
	res.Tags = append([]string{dest.Tag}, tags...)
	if opts.Format == outputText || opts.Format == outputName {
		return emitNames(os.Stdout, []string{res.Reference})
	}
	return emitFormatted(opts, res)
}

func spellPull(ctx context.Context, root string, args []string) error {
	var user string
	pos, err := commandParse("spell pull", args, func(fs *flag.FlagSet) {
		fs.StringVar(&user, "username", "", "the registry username; the password is then read from stdin, overriding spells.registries")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus spell pull <registry>/<repository>[:<tag>|@sha256:<digest>] [<dir>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Fetch a published spell, verify its manifest and layer digests, and print the")
			fmt.Fprintln(os.Stderr, "pinned reference it resolved to, then the directory holding its files: <dir>")
			fmt.Fprintln(os.Stderr, "when given (it must be empty or absent), otherwise the user cache. A bare")
			fmt.Fprintln(os.Stderr, "registry path, as a magusfile imports it, pulls the digest magus.lock pins.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "       magus spell pull magus/spell/<name> <dir>")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Copy a spell magus ships into <dir> for this workspace to own and edit: the files")
			fmt.Fprintln(os.Stderr, "its published artifact holds, read from this binary with no network, spell.buzz")
			fmt.Fprintln(os.Stderr, "opening on a `// magus:origin` line naming that artifact. It prints what a")
			fmt.Fprintln(os.Stderr, "registry pull prints, and on stderr the magus.yaml entry that makes the copy")
			fmt.Fprintln(os.Stderr, "replace the built-in; pull never writes magus.yaml.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		return exit.Usagef("magus spell pull: want <reference> [<dir>], got %d argument(s)", len(pos))
	}
	opts, err := output.Parse(global.output)
	if err != nil {
		return err
	}
	if name, ok := strings.CutPrefix(pos[0], spells.ModulePrefix); ok {
		if len(pos) != 2 {
			return exit.Usagef("magus spell pull %s: want the <dir> to write the copy into", pos[0])
		}
		res, next, err := remotespell.EjectShipped(resolveRootOrEmpty(root), name, pos[1])
		if err != nil {
			return fmt.Errorf("spell pull: %w", err)
		}
		if err := emitPull(opts, res); err != nil {
			return err
		}
		slog.InfoContext(ctx, strings.TrimSuffix(next, "\n"), attr.Notice(""))
		return nil
	}
	ref, err := remotespell.PullReference(resolveRootOrEmpty(root), pos[0])
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
	res, err := remotespell.PullSpell(ctx, client, knowledge.RegistryTimeout, ref, "", dst)
	if err != nil {
		return fmt.Errorf("spell pull: %w", err)
	}
	return emitPull(opts, res)
}

func emitPull(opts output.Options, res remotespell.PullResult) error {
	switch opts.Format {
	case outputText:
		return emitNames(os.Stdout, []string{res.Reference, res.Dir})
	case outputName:
		return emitNames(os.Stdout, []string{res.Reference})
	default:
		return emitFormatted(opts, res)
	}
}

// spellLockResult is the -o json shape of lock: every pin magus.lock now holds.
type spellLockResult struct {
	Spells []remotespell.LockSummary `json:"spells" jsonl:"primary"`
}

func spellLock(ctx context.Context, root string, args []string) error {
	var update bool
	pos, err := commandParse("spell lock", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&update, "update", false, "ask the registry what each declared tag names now, and rewrite magus.lock")
		command.NegatedBoolVar(fs, &update, "update")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus spell lock [--update] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Without --update, check that magus.lock pins every remote spell magus.yaml")
			fmt.Fprintln(os.Stderr, "declares, for the tag it declares, and pull and verify each pinned digest. No tag")
			fmt.Fprintln(os.Stderr, "is resolved. With --update, resolve each declared tag to the manifest it names")
			fmt.Fprintln(os.Stderr, "now, pull and verify it, and rewrite magus.lock. A workspace wires this to the")
			fmt.Fprintln(os.Stderr, "update charm on the target that declares magus.lock as its output.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The workspace is not loaded, since loading it reads the pins this repairs, so a")
			fmt.Fprintln(os.Stderr, "spells.registries password resolves through the environment secret provider.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return exit.Usagef("magus spell lock: takes no arguments, got %d", len(pos))
	}
	opts, err := output.Parse(global.output)
	if err != nil {
		return err
	}
	wsRoot, spells, err := remotespell.WorkspaceSpells(resolveRootOrEmpty(root))
	if err != nil {
		return fmt.Errorf("spell lock: %w", err)
	}
	loadOpts := remotespell.LoadOptions{Client: remotespell.LockClient(registryHTTP, spells)}
	if update {
		err = remotespell.UpdateLock(ctx, wsRoot, func(remotespell.Lock) (remotespell.Lock, error) {
			return remotespell.Relock(ctx, spells, loadOpts)
		})
	} else {
		err = remotespell.VerifyLock(ctx, wsRoot, spells, loadOpts)
	}
	if err != nil {
		return fmt.Errorf("spell lock: %w", err)
	}
	lock, err := remotespell.ReadLock(wsRoot)
	if err != nil {
		return fmt.Errorf("spell lock: %w", err)
	}
	res := spellLockResult{Spells: remotespell.Summarize(lock)}
	if opts.Format == outputText || opts.Format == outputName {
		return emitItemNames(os.Stdout, res.Spells, func(e remotespell.LockSummary) string { return fmt.Sprintf("%s@%s", e.Path, e.Digest.String()) })
	}
	return emitFormatted(opts, res)
}

// spellLsResult is the -o json shape of ls.
type spellLsResult struct {
	Repository string   `json:"repository"`
	Tags       []string `json:"tags" jsonl:"primary"`
}

func spellLs(ctx context.Context, root string, args []string) error {
	var user string
	pos, err := commandParse("spell ls", args, func(fs *flag.FlagSet) {
		fs.StringVar(&user, "username", "", "the registry username; the password is then read from stdin, overriding spells.registries")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus spell ls <registry>/<repository> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "List a spell repository's tags, one per line, following the registry's")
			fmt.Fprintln(os.Stderr, "pagination to the end.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "       magus spell ls magus/spell")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "List every spell magus ships, one pinned reference per line: the artifact a")
			fmt.Fprintln(os.Stderr, "release publishes for it, computed from this binary with no network.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return exit.Usagef("magus spell ls: want <registry>/<repository> or magus/spell, got %d argument(s)", len(pos))
	}
	opts, err := output.Parse(global.output)
	if err != nil {
		return err
	}
	if remotespell.ListsShipped(pos[0]) {
		shipped, err := remotespell.ListShipped()
		if err != nil {
			return fmt.Errorf("spell ls: %w", err)
		}
		if opts.Format == outputText || opts.Format == outputName {
			return emitItemNames(os.Stdout, shipped, func(s remotespell.ShippedSpell) string { return s.Reference })
		}
		return emitFormatted(opts, shippedSpellsResult{Spells: shipped})
	}
	repo, err := oci.ParseRepository(pos[0])
	if err != nil {
		return err
	}
	client, err := registryClient(ctx, root, repo.Registry, user)
	if err != nil {
		return fmt.Errorf("spell ls: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, knowledge.RegistryTimeout)
	defer cancel()
	tags, err := client.Tags(ctx, repo)
	if err != nil {
		return fmt.Errorf("spell ls: %w", err)
	}
	switch opts.Format {
	case outputText, outputName:
		return emitNames(os.Stdout, tags)
	default:
		return emitFormatted(opts, spellLsResult{Repository: repo.String(), Tags: tags})
	}
}

// shippedSpellsResult is the -o json shape of `spell ls magus/spell`.
type shippedSpellsResult struct {
	Spells []remotespell.ShippedSpell `json:"spells" jsonl:"primary"`
}

// registryClient builds the client for one registry host from the process's config,
// stdin and workspace.
func registryClient(ctx context.Context, root, host, username string) (*oci.Client, error) {
	return remotespell.NewClient(ctx, registryHTTP(), globalCfg.Spells, host, username, remotespell.Login{
		ReadToken: knowledge.ReadToken,
		Secrets: func(ctx context.Context) (context.Context, *secret.Resolver, error) {
			m, err := loadMagus(ctx, root)
			if err != nil {
				return ctx, nil, err
			}
			ctx = m.ContextWithSecrets(ctx)
			return ctx, secret.ResolverFromContext(ctx), nil
		},
	})
}

// spellVCSOptions is the workspace's VCS options when a workspace loads, else none.
func spellVCSOptions(ctx context.Context, root string) types.VCSOptions {
	if ws, err := inspectWorkspace(ctx, root); err == nil {
		return ws.VCSOptions()
	}
	return types.VCSOptions{}
}
