package image

import (
	"context"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/json"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// AttachArtifacts adds SBOM and/or provenance documents as artifact manifests on an
// Image Index layout. A single-platform layout is wrapped into an index first.
func AttachArtifacts(ctx context.Context, layout Layout, sbom *SBOMDoc, provenance *ProvenanceDoc) (Layout, error) {
	if err := ctx.Err(); err != nil {
		return Layout{}, fmt.Errorf("image.attach: %w", err)
	}
	if sbom == nil && provenance == nil {
		return Layout{}, fmt.Errorf("image.attach: need sbom or provenance")
	}
	opened, err := OpenLayout(layout.Path)
	if err != nil {
		return Layout{}, fmt.Errorf("image.attach: %w", err)
	}
	var manifests []ocispec.Descriptor
	var blobs []blob
	switch opened.Descriptor.MediaType {
	case ocispec.MediaTypeImageIndex:
		raw, err := os.ReadFile(blobPath(opened.Path, opened.Descriptor.Digest))
		if err != nil {
			return Layout{}, fmt.Errorf("image.attach: read index: %w", err)
		}
		var idx ocispec.Index
		if err := json.Unmarshal(raw, &idx); err != nil {
			return Layout{}, fmt.Errorf("image.attach: decode index: %w", err)
		}
		manifests = append(manifests, idx.Manifests...)
		blobs = append(blobs, bytesBlob(opened.Descriptor.MediaType, raw))
	case ocispec.MediaTypeImageManifest:
		desc := opened.Descriptor
		manifests = append(manifests, desc)
	default:
		return Layout{}, fmt.Errorf("image.attach: unsupported media type %q", opened.Descriptor.MediaType)
	}

	add := func(docPath, artifactType, name string) error {
		data, err := os.ReadFile(docPath)
		if err != nil {
			return fmt.Errorf("image.attach: read %s: %w", name, err)
		}
		layerDesc := ocispec.Descriptor{
			MediaType: "application/vnd.in-toto.predicate.v0+json",
			Digest:    digest.FromBytes(data),
			Size:      int64(len(data)),
			Annotations: map[string]string{
				ocispec.AnnotationTitle: name,
			},
		}
		blobs = append(blobs, bytesBlob(layerDesc.MediaType, data))
		cfg := []byte("{}")
		cfgDesc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromBytes(cfg), Size: 2}
		blobs = append(blobs, bytesBlob(cfgDesc.MediaType, cfg))
		man := ocispec.Manifest{
			Versioned:    specs.Versioned{SchemaVersion: 2},
			MediaType:    ocispec.MediaTypeImageManifest,
			ArtifactType: artifactType,
			Config:       cfgDesc,
			Layers:       []ocispec.Descriptor{layerDesc},
			Subject:      &opened.Descriptor,
		}
		manData, err := json.Marshal(man)
		if err != nil {
			return err
		}
		manDesc := ocispec.Descriptor{
			MediaType:    ocispec.MediaTypeImageManifest,
			ArtifactType: artifactType,
			Digest:       digest.FromBytes(manData),
			Size:         int64(len(manData)),
		}
		blobs = append(blobs, bytesBlob(manDesc.MediaType, manData))
		manifests = append(manifests, manDesc)
		return nil
	}
	if sbom != nil {
		if err := add(sbom.Path, "application/spdx+json", "sbom.spdx.json"); err != nil {
			return Layout{}, err
		}
	}
	if provenance != nil {
		if err := add(provenance.Path, "application/vnd.in-toto+json", "provenance.json"); err != nil {
			return Layout{}, err
		}
	}

	indexData, err := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests,
	})
	if err != nil {
		return Layout{}, fmt.Errorf("image.attach: marshal index: %w", err)
	}
	root := blobDescriptor(ocispec.MediaTypeImageIndex, indexData)
	blobs = append(blobs, bytesBlob(root.MediaType, indexData))
	dest := opened.Path
	if err := writeLayout(ctx, dest, root, blobs); err != nil {
		return Layout{}, fmt.Errorf("image.attach: %w", err)
	}
	return Layout{
		Path:       dest,
		Descriptor: root,
		Op:         OpAttachArtifacts,
	}, nil
}
