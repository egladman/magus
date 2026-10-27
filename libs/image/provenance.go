package image

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/opencontainers/go-digest"
)

// ProvenanceMaterial is one content-addressed input to an image write.
type ProvenanceMaterial struct {
	URI    string
	Digest Digest
}

// ProvenanceSpec is the explicit material for an in-toto SLSA-ish statement.
type ProvenanceSpec struct {
	SourceRevision string
	BuilderID      string
	Materials      []ProvenanceMaterial
}

// ProvenanceDoc is a written in-toto statement.
type ProvenanceDoc struct {
	Path      string
	MediaType string
	Digest    Digest
}

// WriteProvenance writes a deterministic in-toto statement for layout to dest.
func WriteProvenance(ctx context.Context, layout Layout, spec ProvenanceSpec, dest string) (ProvenanceDoc, error) {
	if err := ctx.Err(); err != nil {
		return ProvenanceDoc{}, fmt.Errorf("image.provenance: %w", err)
	}
	layout, err := provenanceLayout(layout)
	if err != nil {
		return ProvenanceDoc{}, err
	}
	if spec.BuilderID == "" {
		return ProvenanceDoc{}, fmt.Errorf("image.provenance: builderId is empty")
	}
	data, err := json.Marshal(provenanceStatement(layout, spec))
	if err != nil {
		return ProvenanceDoc{}, fmt.Errorf("image.provenance: marshal: %w", err)
	}
	if err := os.WriteFile(dest, append(data, '\n'), 0o644); err != nil {
		return ProvenanceDoc{}, fmt.Errorf("image.provenance: write %q: %w", dest, err)
	}
	return ProvenanceDoc{
		Path:      dest,
		MediaType: "application/vnd.in-toto+json",
		Digest:    MustDigest(digest.FromBytes(data)),
	}, nil
}

func provenanceLayout(layout Layout) (Layout, error) {
	if layout.Path == "" {
		return Layout{}, fmt.Errorf("image.provenance: layout path is empty")
	}
	if layout.Descriptor.Digest != "" {
		return layout, nil
	}
	opened, err := OpenLayout(layout.Path)
	if err != nil {
		return Layout{}, fmt.Errorf("image.provenance: %w", err)
	}
	return opened, nil
}

func provenanceStatement(layout Layout, spec ProvenanceSpec) map[string]any {
	materials := make([]map[string]any, 0, len(spec.Materials))
	for _, m := range spec.Materials {
		materials = append(materials, map[string]any{
			"uri":    m.URI,
			"digest": map[string]string{"sha256": m.Digest.Encoded()},
		})
	}
	epoch := time.Unix(0, 0).UTC().Format(time.RFC3339)
	return map[string]any{
		"_type":         "https://in-toto.io/Statement/v0.1",
		"predicateType": "https://slsa.dev/provenance/v0.2",
		"subject": []map[string]any{{
			"name":   layout.Path,
			"digest": map[string]string{"sha256": layout.Descriptor.Digest.Encoded()},
		}},
		"predicate": map[string]any{
			"builder": map[string]any{"id": spec.BuilderID},
			"metadata": map[string]any{
				"buildStartedOn":  epoch,
				"buildFinishedOn": epoch,
			},
			"materials": materials,
			"invocation": map[string]any{
				"configSource": map[string]any{
					"uri":        spec.SourceRevision,
					"digest":     map[string]string{},
					"entryPoint": "image.assemble",
				},
			},
		},
	}
}
