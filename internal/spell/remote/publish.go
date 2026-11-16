package remote

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/oci"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// BuildResult is what build and push report. Digest is the manifest digest, the
// one value a pinned reference names; the rest describe the bytes behind it.
type BuildResult struct {
	Digest      digest.Digest     `json:"digest"`
	Reference   string            `json:"reference,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Layer       digest.Digest     `json:"layer"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations"`
}

// BuildDir packs dir with the provenance of the revision holding it. build and push
// share it so the digest build prints is the one push writes. opts are the workspace's
// VCS options, or the zero value when no workspace loads.
func BuildDir(ctx context.Context, opts types.VCSOptions, dir, source string) (oci.Artifact, BuildResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return oci.Artifact{}, BuildResult{}, err
	}
	driver, err := ResolveVCS(ctx, opts, abs)
	if err != nil {
		return oci.Artifact{}, BuildResult{}, err
	}
	prov, err := ReadProvenance(ctx, driver, abs)
	if err != nil {
		return oci.Artifact{}, BuildResult{}, err
	}
	if source != "" {
		prov.Source = source
	}
	content, err := Build(ctx, abs, driver, prov)
	if err != nil {
		return oci.Artifact{}, BuildResult{}, err
	}
	res, err := Describe(content)
	return content, res, err
}

// BuildShipped is the artifact a release publishes for the spell magus ships as
// name, exactly as Shipped packs it, so a push writes the digest eject stamps.
func BuildShipped(name string) (oci.Artifact, BuildResult, error) {
	dir, err := ShippedDir(name)
	if err != nil {
		return oci.Artifact{}, BuildResult{}, err
	}
	_, content, err := Shipped(name, dir)
	if err != nil {
		return oci.Artifact{}, BuildResult{}, err
	}
	res, err := Describe(content)
	return content, res, err
}

// Describe reads the digests and annotations a build reports off its artifact.
func Describe(content oci.Artifact) (BuildResult, error) {
	m, err := content.Manifest()
	if err != nil {
		return BuildResult{}, err
	}
	layer := content.Blobs[0].Data
	return BuildResult{
		Digest:      m.Digest,
		Layer:       digest.FromBytes(layer),
		Size:        int64(len(layer)),
		Annotations: content.Annotations,
	}, nil
}

// ShippedDir is where the spell magus ships as name lives in its embedded tree.
func ShippedDir(name string) (string, error) {
	dir, ok := spell.ShippedDir(name)
	if !ok {
		return "", fmt.Errorf("magus ships no spell %q, `magus spell ls %s` lists them", name, ShippedPath)
	}
	return dir, nil
}

// ShippedPath is what `spell ls` takes to list the spells magus ships: the
// magus/spell/<name> import form with no name.
var ShippedPath = strings.TrimSuffix(spells.ModulePrefix, "/")

// PullResult is what pull reports: the pinned reference it verified, and where the
// verified files are.
type PullResult struct {
	Reference string        `json:"reference"`
	Digest    digest.Digest `json:"digest"`
	Dir       string        `json:"dir"`
	// Override is the magus.yaml entry that makes a copy of a built-in replace it, set
	// only when pulling one (magus/spell/<name>). Pull never writes magus.yaml: the
	// declaration is the acknowledgment.
	Override Override `json:"override,omitzero"`
}

// Override is one `spells: {<import>: {path: <path>}}` entry.
type Override struct {
	Import string `json:"import"`
	Path   string `json:"path"`
}

// EjectShipped copies the spell magus ships as name into dst for the workspace at
// wsRoot ("" when there is none) to own. next says how the workspace puts the copy to use.
func EjectShipped(wsRoot, name, dst string) (res PullResult, next string, err error) {
	dir, err := ShippedDir(name)
	if err != nil {
		return PullResult{}, "", err
	}
	abs, err := filepath.Abs(dst)
	if err != nil {
		return PullResult{}, "", err
	}
	ref, err := Eject(name, dir, abs)
	if err != nil {
		return PullResult{}, "", err
	}
	res = PullResult{Reference: ref.String(), Digest: ref.Digest, Dir: abs}
	rel := abs
	if wsRoot != "" {
		if r, err := filepath.Rel(wsRoot, abs); err == nil && filepath.IsLocal(r) {
			rel = filepath.ToSlash(r)
		}
	}
	if _, builtin := spell.Builtins()[name]; !builtin {
		return res, fmt.Sprintf("%s ships as source only: it imports a host module, so there is no built-in to replace. Import the copy by its path: import %q;\n",
			spells.ModulePath(name), rel), nil
	}
	res.Override = Override{Import: spells.ModulePath(name), Path: rel}
	return res, fmt.Sprintf("to run this copy in place of the built-in, declare it in magus.yaml:\n\nspells:\n  %s:\n    path: %s\n",
		res.Override.Import, res.Override.Path), nil
}

// PullReference reads pull's argument: a reference naming a tag or a digest, or the
// bare registry path a magusfile imports, which pulls the digest magus.lock pins for it
// so what lands is exactly what a workspace load would run.
func PullReference(wsRoot, arg string) (oci.Reference, error) {
	ref, err := oci.ParseReference(arg)
	if err == nil {
		return ref, nil
	}
	if _, rerr := oci.ParseRepository(arg); rerr != nil {
		return oci.Reference{}, err
	}
	wsRoot, cfg, err := WorkspaceSpells(wsRoot)
	if err != nil {
		return oci.Reference{}, err
	}
	decl := cfg.Imports[arg]
	if decl.Tag == "" {
		return oci.Reference{}, exit.Usagef("magus spell pull: %s names no tag or digest, and magus.yaml declares no remote spell by that path", arg)
	}
	lock, err := ReadLock(wsRoot)
	if err != nil {
		return oci.Reference{}, err
	}
	e, ok := lock.Spells[arg]
	if !ok || e.Tag != decl.Tag {
		return oci.Reference{}, types.DiagnosticErrorf(types.RemoteSpellLockStale,
			"spell %s: %s pins no digest for tag %q; %s", arg, LockFile, decl.Tag, UpdateHint)
	}
	pinned, err := Pinned(arg, e.Digest)
	if err != nil {
		return oci.Reference{}, err
	}
	return pinned.OCI, nil
}

// WorkspaceSpells reads the spell declarations beside the workspace's magus.lock,
// without loading the workspace: a load resolves the very pins lock exists to repair.
// wsRoot is the resolved workspace root, "" when none was found.
func WorkspaceSpells(wsRoot string) (string, config.SpellsConfig, error) {
	if wsRoot == "" {
		return "", config.SpellsConfig{}, fmt.Errorf("no workspace here to read magus.yaml from, pass --root")
	}
	cfg, err := config.LoadWithRoot("", wsRoot)
	if err != nil {
		return "", config.SpellsConfig{}, err
	}
	return wsRoot, cfg.Spells, nil
}

// PullSpell pins ref, resolves it through the verified cache under cacheRoot ("" for the
// user cache), and unpacks it into dst when one is given. A registry call that outlasts
// timeout fails.
func PullSpell(ctx context.Context, client *oci.Client, timeout time.Duration, ref oci.Reference, cacheRoot, dst string) (PullResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pinned, err := Pin(ctx, client, ref)
	if err != nil {
		return PullResult{}, err
	}
	pinned.Tag = ""
	dir, err := Resolve(ctx, Ref{Import: pinned.String(), OCI: pinned}, Options{
		CacheRoot:   cacheRoot,
		Client:      client.HTTP,
		Credentials: client.Credentials,
	})
	if err != nil {
		return PullResult{}, err
	}
	if dst != "" {
		if err := Unpack(dir, dst); err != nil {
			return PullResult{}, err
		}
		dir = dst
	}
	return PullResult{Reference: pinned.String(), Digest: pinned.Digest, Dir: dir}, nil
}

// ShippedSpell is one spell magus ships. Name is what magus/spell/<name> takes: a
// built-in's registered name, else its directory. Reference pins the artifact a release
// publishes for it.
type ShippedSpell struct {
	Name      string        `json:"name"`
	Dir       string        `json:"dir"`
	BuiltIn   bool          `json:"built_in"`
	Reference string        `json:"reference"`
	Digest    digest.Digest `json:"digest"`
}

// ListShipped lists every directory of spells.Shipped() holding a spell.buzz, sorted
// by name.
func ListShipped() ([]ShippedSpell, error) {
	builtins := map[string]string{} // dir -> registered name
	for name := range spell.Builtins() {
		if dir, ok := spell.ShippedDir(name); ok {
			builtins[dir] = name
		}
	}
	var out []ShippedSpell
	err := fs.WalkDir(spells.Shipped(), ".", func(p string, d fs.DirEntry, err error) error {
		dir, ok := strings.CutSuffix(p, "/spell.buzz")
		if err != nil || d.IsDir() || !ok {
			return err
		}
		name, builtIn := builtins[dir]
		if !builtIn {
			name = dir
		}
		ref, _, err := Shipped(name, dir)
		if err != nil {
			return err
		}
		out = append(out, ShippedSpell{Name: name, Dir: dir, BuiltIn: builtIn, Reference: ref.String(), Digest: ref.Digest})
		return nil
	})
	slices.SortFunc(out, func(a, b ShippedSpell) int { return strings.Compare(a.Name, b.Name) })
	return out, err
}

// ResolveVCS resolves the VCS that holds dir, with the workspace's VCS options when a
// workspace loads. A tree no VCS answers for is an error: packing every file on disk
// would make the digest depend on the machine.
func ResolveVCS(ctx context.Context, opts types.VCSOptions, dir string) (types.VCSDriver, error) { //nolint:ireturn // the VCS driver is picked by detection at runtime
	res, err := vcs.Resolve(ctx, dir, "", opts)
	if err != nil {
		return nil, err
	}
	if res.VCS == nil {
		return nil, fmt.Errorf("%s is under no VCS, commit the spell to a repository first", dir)
	}
	return res.VCS, nil
}
