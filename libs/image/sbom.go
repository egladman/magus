package image

import (
	"context"
	"fmt"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
)

const (
	// MediaTypeSPDX is an SPDX JSON document, the artifactType of an SBOM referrer.
	MediaTypeSPDX = "application/spdx+json"
	// MediaTypeInToto is an in-toto Statement, the artifactType of a provenance referrer.
	MediaTypeInToto = "application/vnd.in-toto+json"
)

// WriteSBOM writes a deterministic SPDX 2.3 JSON document for the image at layout to
// path. It names the image by its root digest, so two checkouts write the same bytes;
// a file inventory is left to a scanner that reads the image itself.
func WriteSBOM(ctx context.Context, layout, path string) (Document, error) {
	l, err := OpenLayout(ctx, layout)
	if err != nil {
		return Document{}, fmt.Errorf("image.sbom: %w", err)
	}
	d := l.Descriptor.Digest
	doc := map[string]any{
		"spdxVersion":       "SPDX-2.3",
		"dataLicense":       "CC0-1.0",
		"SPDXID":            "SPDXRef-DOCUMENT",
		"name":              d.String(),
		"documentNamespace": "https://github.com/egladman/magus/spdx/" + d.Encoded(),
		"creationInfo": map[string]any{
			"created":  time.Unix(0, 0).UTC().Format(time.RFC3339),
			"creators": []string{"Tool: magus-image"},
		},
		"packages": []map[string]any{{
			"SPDXID":           "SPDXRef-Package",
			"name":             d.String(),
			"downloadLocation": "NOASSERTION",
			"filesAnalyzed":    false,
			"versionInfo":      d.String(),
		}},
	}
	out, err := writeDocument(path, MediaTypeSPDX, doc)
	if err != nil {
		return Document{}, fmt.Errorf("image.sbom: %w", err)
	}
	return out, nil
}

// writeDocument writes v as JSON plus a newline and describes exactly the bytes on disk.
func writeDocument(path, mediaType string, v any) (Document, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return Document{}, err
	}
	data = append(data, '\n')
	if err := file.WriteFileAtomic(path, data, 0o644); err != nil {
		return Document{}, err
	}
	return Document{Path: path, Descriptor: blobDescriptor(mediaType, data)}, nil
}

func blobDescriptor(mediaType string, data []byte) ocispec.Descriptor {
	return ocispec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(data), Size: int64(len(data))}
}
