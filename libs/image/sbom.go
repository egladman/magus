package image

import (
	"context"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/opencontainers/go-digest"
)

// SBOMDoc is a written SPDX document describing one layout.
type SBOMDoc struct {
	Path      string
	MediaType string
	Digest    Digest
	Platform  Platform
}

// WriteSBOM writes a deterministic SPDX 2.3 JSON document for layout to dest.
// The document names the layout's manifest digest as the package; layer file
// inventories are left to a scanner that reads the published image.
func WriteSBOM(ctx context.Context, layout Layout, dest string) (SBOMDoc, error) {
	if err := ctx.Err(); err != nil {
		return SBOMDoc{}, fmt.Errorf("image.sbom: %w", err)
	}
	if layout.Path == "" {
		return SBOMDoc{}, fmt.Errorf("image.sbom: layout path is empty")
	}
	if layout.Descriptor.Digest == "" {
		opened, err := OpenLayout(layout.Path)
		if err != nil {
			return SBOMDoc{}, fmt.Errorf("image.sbom: %w", err)
		}
		layout = opened
	}
	doc := map[string]any{
		"spdxVersion":       "SPDX-2.3",
		"dataLicense":       "CC0-1.0",
		"SPDXID":            "SPDXRef-DOCUMENT",
		"name":              "magus-image-" + layout.Descriptor.Digest.String(),
		"documentNamespace": "https://github.com/egladman/magus/spdx/" + layout.Descriptor.Digest.Encoded(),
		"creationInfo": map[string]any{
			"created":  time.Unix(0, 0).UTC().Format(time.RFC3339),
			"creators": []string{"Tool: magus-image"},
		},
		"packages": []map[string]any{{
			"SPDXID":           "SPDXRef-Package",
			"name":             path.Base(layout.Path),
			"downloadLocation": "NOASSERTION",
			"filesAnalyzed":    false,
			"versionInfo":      layout.Descriptor.Digest.String(),
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return SBOMDoc{}, fmt.Errorf("image.sbom: marshal: %w", err)
	}
	if err := os.WriteFile(dest, append(data, '\n'), 0o644); err != nil {
		return SBOMDoc{}, fmt.Errorf("image.sbom: write %q: %w", dest, err)
	}
	return SBOMDoc{
		Path:      dest,
		MediaType: "application/spdx+json",
		Digest:    MustDigest(digest.FromBytes(data)),
		Platform:  layout.Platform,
	}, nil
}
