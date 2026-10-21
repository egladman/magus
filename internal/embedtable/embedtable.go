package embedtable

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/oci"
	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/libs/xdg"
)

const (
	// Repository is where the release publishes the table, one tag per pin.
	Repository = "ghcr.io/egladman/magus-embed"
	// ArtifactType is what the table's manifest says it is; a pull refuses any other.
	ArtifactType = "application/vnd.magus.embed-table.v1"
	// LayerMediaType is the table layer's media type: the bytes embed.ReadTable decodes.
	LayerMediaType = "application/vnd.magus.embed-table.layer.v1"
	// FileName is the table's name in the cache root and its layer's title, the file
	// `magus run table libs/embed/train` hashes.
	FileName = "potion-code-16M-v2.table"
)

// ErrAbsent reports that the cache holds no table. It is a normal state, never one
// Load recovers from by pulling: the error wrapping it names the command that fills
// the cache.
var ErrAbsent = errors.New("embedtable: no embedding table in the cache")

// Options locates the cache root.
type Options struct {
	// Dir is the cache root holding FileName. Empty means the magus-embed directory
	// under xdg.CacheDir, the root `magus run table libs/embed/train` writes.
	Dir string
}

// Path returns the table file's path under the cache root.
func (o Options) Path() (string, error) {
	dir := o.Dir
	if dir == "" {
		cache, err := xdg.CacheDir()
		if err != nil {
			return "", fmt.Errorf("embedtable: resolve the cache root: %w", err)
		}
		dir = filepath.Join(cache, "magus-embed")
	}
	return filepath.Join(dir, FileName), nil
}

// Reference returns the table's reference in repository, tagged with the pin so no
// release names a tag by hand. An empty repository means Repository.
func Reference(repository string) (oci.Reference, error) {
	pin, err := embed.Pin()
	if err != nil {
		return oci.Reference{}, err
	}
	if repository == "" {
		repository = Repository
	}
	ref, err := oci.ParseRepository(repository)
	if err != nil {
		return oci.Reference{}, err
	}
	ref.Tag = pin
	return ref, nil
}

// Load reads the cached table and decodes it once its sha256 equals embed.Pin(). A
// missing file returns an error wrapping ErrAbsent; a file of other bytes returns a
// different error and is not decoded. Load never reaches a registry.
func Load(ctx context.Context, opts Options) (*embed.Table, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pin, err := embed.Pin()
	if err != nil {
		return nil, err
	}
	path, err := opts.Path()
	if err != nil {
		return nil, err
	}
	return load(path, pin)
}

func load(path, pin string) (*embed.Table, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s is missing, run `magus graph pull -table`", ErrAbsent, path)
	}
	if err != nil {
		return nil, fmt.Errorf("embedtable: %w", err)
	}
	if got := sum(data); got != pin {
		return nil, fmt.Errorf("embedtable: %s hashes to %s, the pin is %s: run `magus graph pull -table` to replace it", path, got, pin)
	}
	return embed.ReadTable(data)
}

// Pull fetches ref's table layer into the cache, writing by rename so no reader sees
// a partial file. A layer whose digest is not embed.Pin() is refused before any byte
// of it is fetched, and the cache is left as it was.
func Pull(ctx context.Context, client *oci.Client, ref oci.Reference, opts Options) error {
	pin, err := embed.Pin()
	if err != nil {
		return err
	}
	path, err := opts.Path()
	if err != nil {
		return err
	}
	return pull(ctx, client, ref, path, pin)
}

func pull(ctx context.Context, client *oci.Client, ref oci.Reference, path, pin string) error {
	art, err := client.Artifact(ctx, ref, ArtifactType)
	if err != nil {
		return err
	}
	desc, err := pinnedLayer(art, ref, pin)
	if err != nil {
		return err
	}
	data, err := art.Blob(ctx, desc)
	if err != nil {
		return err
	}
	return writeByRename(path, data)
}

func writeByRename(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("embedtable: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("embedtable: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("embedtable: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("embedtable: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("embedtable: %w", err)
	}
	return nil
}

// Publish pushes the table at tablePath to ref, write-once, and reports whether it
// pushed. ref's tag must be embed.Pin() and the file must hash to it. A tag that is
// already published is kept when its layer is the pin and is an error otherwise; it is
// never overwritten.
func Publish(ctx context.Context, client *oci.Client, ref oci.Reference, tablePath string) (bool, error) {
	pin, err := embed.Pin()
	if err != nil {
		return false, err
	}
	return publish(ctx, client, ref, tablePath, pin)
}

func publish(ctx context.Context, client *oci.Client, ref oci.Reference, tablePath, pin string) (bool, error) {
	if ref.Tag != pin || ref.Digest != "" {
		return false, fmt.Errorf("embedtable: publish %s: the tag must be the pin %s", ref, pin)
	}
	data, err := os.ReadFile(tablePath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("%w: %s is missing, run `magus run table libs/embed/train`", ErrAbsent, tablePath)
	}
	if err != nil {
		return false, fmt.Errorf("embedtable: %w", err)
	}
	if got := sum(data); got != pin {
		return false, fmt.Errorf("embedtable: %s hashes to %s, the pin is %s: the conversion did not reproduce, and a table is never published under another tag", tablePath, got, pin)
	}
	published, err := hasTag(ctx, client, ref)
	if err != nil {
		return false, err
	}
	if published {
		art, err := client.Artifact(ctx, ref, ArtifactType)
		if err != nil {
			return false, err
		}
		if _, err := pinnedLayer(art, ref, pin); err != nil {
			return false, fmt.Errorf("%w; a published tag is never overwritten", err)
		}
		return false, nil
	}
	content := oci.Content{ArtifactType: ArtifactType, Layers: []oci.Layer{{Name: FileName, MediaType: LayerMediaType, Payload: data}}}
	if _, err := client.Push(ctx, ref, content); err != nil {
		return false, err
	}
	return true, nil
}

func pinnedLayer(art *oci.Artifact, ref oci.Reference, pin string) (ocispec.Descriptor, error) {
	desc, ok := art.Find(FileName)
	if !ok {
		return desc, fmt.Errorf("%s: %s: %w", ref, FileName, oci.ErrLayerMiss)
	}
	if want := digest.NewDigestFromEncoded(digest.SHA256, pin); desc.Digest != want {
		return desc, fmt.Errorf("embedtable: %s carries layer %s, the pin is %s", ref, desc.Digest, want)
	}
	return desc, nil
}

func hasTag(ctx context.Context, client *oci.Client, ref oci.Reference) (bool, error) {
	repo := oci.Reference{Registry: ref.Registry, Repository: ref.Repository}
	tags, err := client.Tags(ctx, repo)
	// The distribution spec answers a repository never pushed to with NAME_UNKNOWN, and
	// oci reports a failed status only as text.
	if err != nil && strings.Contains(err.Error(), "NAME_UNKNOWN") {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("embedtable: list %s, so whether the table is published is unknown: %w", repo, err)
	}
	return slices.Contains(tags, ref.Tag), nil
}

func sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}
