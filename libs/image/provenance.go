package image

import (
	"context"
	"fmt"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/json"
)

// Provenance is what an in-toto statement says built an image. Both strings are
// required: a statement with no builder or no source attests nothing.
type Provenance struct {
	BuilderID      string // SLSA runDetails.builder.id, e.g. a CI workflow's URL
	SourceRevision string // the commit the image was built from
	// Materials are further inputs. The digest of every base the image was assembled
	// over is added from the layout; a caller does not repeat them.
	Materials []Material
}

// Material is one content-addressed input.
type Material struct {
	URI    string
	Digest digest.Digest
}

const buildType = "https://github.com/egladman/magus/image/assemble@v1"

// WriteProvenance writes a deterministic in-toto Statement v1 with a SLSA v1 provenance
// predicate for the image at layout to path. The subject is named by its root digest,
// never a host path, so two checkouts write the same bytes.
func WriteProvenance(ctx context.Context, layout string, p Provenance, path string) (Document, error) {
	if err := p.validate(); err != nil {
		return Document{}, fmt.Errorf("image.provenance: %w", err)
	}
	l, err := OpenLayout(ctx, layout)
	if err != nil {
		return Document{}, fmt.Errorf("image.provenance: %w", err)
	}
	bases, err := baseMaterials(ctx, l)
	if err != nil {
		return Document{}, fmt.Errorf("image.provenance: %w", err)
	}
	deps := make([]map[string]any, 0, len(p.Materials)+len(bases))
	for _, m := range append(bases, p.Materials...) {
		dep := map[string]any{"digest": map[string]string{m.Digest.Algorithm().String(): m.Digest.Encoded()}}
		// A base assembled from a local layout has no name; SLSA takes a digest alone.
		if m.URI != "" {
			dep["uri"] = m.URI
		}
		deps = append(deps, dep)
	}
	d := l.Descriptor.Digest
	statement := map[string]any{
		"_type": "https://in-toto.io/Statement/v1",
		"subject": []map[string]any{{
			"name":   d.String(),
			"digest": map[string]string{d.Algorithm().String(): d.Encoded()},
		}},
		"predicateType": "https://slsa.dev/provenance/v1",
		"predicate": map[string]any{
			"buildDefinition": map[string]any{
				"buildType":            buildType,
				"externalParameters":   map[string]any{"sourceRevision": p.SourceRevision},
				"resolvedDependencies": deps,
			},
			"runDetails": map[string]any{
				"builder": map[string]any{"id": p.BuilderID},
			},
		},
	}
	out, err := writeDocument(path, MediaTypeInToto, statement)
	if err != nil {
		return Document{}, fmt.Errorf("image.provenance: %w", err)
	}
	return out, nil
}

func (p Provenance) validate() error {
	if p.BuilderID == "" || p.SourceRevision == "" {
		return fmt.Errorf("builderID %q and sourceRevision %q are both required", p.BuilderID, p.SourceRevision)
	}
	for i, m := range p.Materials {
		if m.URI == "" {
			return fmt.Errorf("materials[%d]: uri is empty", i)
		}
		if err := checkDigest(m.Digest); err != nil {
			return fmt.Errorf("materials[%d]: %w", i, err)
		}
	}
	return nil
}

// baseMaterials reads org.opencontainers.image.base.digest (and base.name) from each
// platform manifest of l, in manifest order, once per digest.
func baseMaterials(ctx context.Context, l Layout) ([]Material, error) {
	manifests, err := l.Manifests(ctx)
	if err != nil {
		return nil, err
	}
	var out []Material
	seen := map[digest.Digest]bool{}
	for _, m := range manifests {
		data, err := l.readBlob(ctx, m)
		if err != nil {
			return nil, err
		}
		var man ocispec.Manifest
		if err := json.Unmarshal(data, &man); err != nil {
			return nil, fmt.Errorf("manifest %s: %w", m.Digest, err)
		}
		raw := man.Annotations[ocispec.AnnotationBaseImageDigest]
		if raw == "" {
			continue
		}
		d, err := ParseDigest(raw)
		if err != nil {
			return nil, fmt.Errorf("manifest %s: %w", m.Digest, err)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, Material{URI: man.Annotations[ocispec.AnnotationBaseImageName], Digest: d})
	}
	return out, nil
}
