package container

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/image"
	"github.com/egladman/magus/libs/oci"
)

// Image is a task image pinned to the digest its tag named when it was resolved, and
// the platform a trial runs it as. The JSON fields are the ones a result entry
// records.
type Image struct {
	Ref      oci.Reference  `json:"image"`
	Digest   digest.Digest  `json:"image_digest"`
	Platform image.Platform `json:"platform"`
	// Emulated is true when Platform is not the engine's own, which makes every wall
	// time from the trial incomparable with a native one.
	Emulated bool `json:"emulated"`
}

// Pinned is the reference a pull and a create address: the repository at Digest.
func (i Image) Pinned() string {
	return oci.Reference{Registry: i.Ref.Registry, Repository: i.Ref.Repository, Digest: i.Digest}.String()
}

// ResolveImage pins name, as a task row writes it (swebench/sweb.eval.x86_64.<id> or
// with a registry and tag), to a digest through reg, and picks the platform to run it
// as on an engine whose own platform is engine.
//
// A name with no registry is Docker Hub's and a name with no tag is :latest, as the
// docker CLI reads them; Ref records the full reference either way. An image with no
// build for engine is refused unless allowEmulation is set, and then runs as the
// image's linux/amd64 build with Emulated set.
func ResolveImage(ctx context.Context, reg *oci.Client, name string, engine image.Platform, allowEmulation bool) (Image, error) {
	ref, err := oci.ParseReference(qualify(name))
	if err != nil {
		return Image{}, err
	}
	m, err := reg.Resolve(ctx, ref)
	if err != nil {
		return Image{}, err
	}
	built, err := platforms(ctx, reg, ref, m)
	if err != nil {
		return Image{}, err
	}
	img := Image{Ref: ref, Digest: m.Digest}
	if i := slices.IndexFunc(built, engine.Matches); i >= 0 {
		img.Platform = built[i]
		return img, nil
	}
	amd64 := image.Platform{OS: "linux", Architecture: "amd64"}
	i := slices.IndexFunc(built, amd64.Matches)
	if i < 0 {
		return Image{}, fmt.Errorf("container: %s has no build for %s or linux/amd64 (it has %s)", ref, engine, joinPlatforms(built))
	}
	if !allowEmulation {
		return Image{}, fmt.Errorf("container: %s is built for %s only and the engine runs %s; it would run emulated, several times slower. "+
			"Run it on a %s engine, or allow emulation explicitly so the result records emulated: true", ref, joinPlatforms(built), engine, amd64)
	}
	img.Platform = built[i]
	img.Emulated = true
	return img, nil
}

func qualify(name string) string {
	first, _, _ := strings.Cut(name, "/")
	if !strings.ContainsAny(first, ".:") && first != "localhost" {
		name = "docker.io/" + name
	}
	last := name[strings.LastIndex(name, "/")+1:]
	if !strings.ContainsAny(last, ":@") {
		name += ":latest"
	}
	return name
}

// platforms lists what m is built for: each entry of an index, or the one platform a
// single manifest's config names.
func platforms(ctx context.Context, reg *oci.Client, ref oci.Reference, m oci.RawManifest) ([]image.Platform, error) {
	switch m.MediaType {
	case ocispec.MediaTypeImageIndex, "application/vnd.docker.distribution.manifest.list.v2+json":
		var idx ocispec.Index
		if err := json.Unmarshal(m.Body, &idx); err != nil {
			return nil, fmt.Errorf("container: decode %s index: %w", ref, err)
		}
		var out []image.Platform
		for _, d := range idx.Manifests {
			// Attestation manifests sit in the index as unknown/unknown.
			if d.Platform == nil || d.Platform.OS == "unknown" {
				continue
			}
			out = append(out, image.Platform{OS: d.Platform.OS, Architecture: d.Platform.Architecture, Variant: d.Platform.Variant})
		}
		return out, nil
	default:
		var man ocispec.Manifest
		if err := json.Unmarshal(m.Body, &man); err != nil {
			return nil, fmt.Errorf("container: decode %s manifest: %w", ref, err)
		}
		var buf bytes.Buffer
		if err := reg.GetBlob(ctx, ref, man.Config, &buf); err != nil {
			return nil, err
		}
		var cfg ocispec.Image
		if err := json.Unmarshal(buf.Bytes(), &cfg); err != nil {
			return nil, fmt.Errorf("container: decode %s config: %w", ref, err)
		}
		return []image.Platform{{OS: cfg.OS, Architecture: cfg.Architecture, Variant: cfg.Variant}}, nil
	}
}

func joinPlatforms(ps []image.Platform) string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.String()
	}
	return strings.Join(names, ", ")
}
