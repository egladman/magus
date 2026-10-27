package image

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/libs/oci"
)

// Document is a written file and the descriptor of its bytes: what WriteSBOM and
// WriteProvenance return and AttachReferrers takes. Descriptor.MediaType becomes the
// referrer's artifactType.
type Document struct {
	Path       string
	Descriptor ocispec.Descriptor
}

// AttachReferrers writes a NEW layout at dest holding the image at subject, every blob
// it reaches, and one OCI 1.1 referrer per document: an image manifest whose subject is
// the image's root descriptor, whose config is the empty descriptor, and whose one
// layer is the document. subject is never edited. index.json lists the image first and
// its referrers after it, the ones subject already had included. Each document is
// checked against its descriptor, so a file changed since it was written is refused.
func AttachReferrers(ctx context.Context, subject string, docs []Document, dest string) (Layout, error) {
	if len(docs) == 0 {
		return Layout{}, fmt.Errorf("image.attachReferrers: no documents")
	}
	l, err := OpenLayout(ctx, subject, "")
	if err != nil {
		return Layout{}, fmt.Errorf("image.attachReferrers: %w", err)
	}
	if filepath.Clean(subject) == filepath.Clean(dest) {
		return Layout{}, fmt.Errorf("image.attachReferrers: dest %s is the subject: write the referrers to a new layout", dest)
	}
	st, err := newStaging(dest)
	if err != nil {
		return Layout{}, fmt.Errorf("image.attachReferrers: %w", err)
	}
	out, err := attachInto(ctx, st, l, docs)
	if err == nil {
		err = st.commit(append([]ocispec.Descriptor{out.Descriptor}, out.Referrers...)...)
	}
	if err != nil {
		st.discard()
		return Layout{}, fmt.Errorf("image.attachReferrers: %w", err)
	}
	out.Path = dest
	return out, nil
}

func attachInto(ctx context.Context, st *staging, l Layout, docs []Document) (Layout, error) {
	if err := copyImage(ctx, st, l); err != nil {
		return Layout{}, err
	}
	subject := ocispec.Descriptor{MediaType: l.Descriptor.MediaType, Digest: l.Descriptor.Digest, Size: l.Descriptor.Size}
	if _, err := st.put(ocispec.MediaTypeEmptyJSON, ocispec.DescriptorEmptyJSON.Data); err != nil {
		return Layout{}, err
	}
	referrers := append([]ocispec.Descriptor(nil), l.Referrers...)
	for i, doc := range docs {
		desc, err := attachDocument(ctx, st, subject, doc)
		if err != nil {
			return Layout{}, fmt.Errorf("documents[%d] %s: %w", i, doc.Path, err)
		}
		referrers = append(referrers, desc)
	}
	return Layout{Descriptor: l.Descriptor, Referrers: referrers}, nil
}

// copyImage links every blob l's image and its referrers reach into st, except a
// non-distributable layer, which a layout need not hold.
func copyImage(ctx context.Context, st *staging, l Layout) error {
	return l.walk(ctx, func(d ocispec.Descriptor, _ bool) error {
		if isNonDistributable(d.MediaType) {
			return nil
		}
		return st.link(ctx, l.Path, d)
	})
}

func attachDocument(ctx context.Context, st *staging, subject ocispec.Descriptor, doc Document) (ocispec.Descriptor, error) {
	switch doc.Descriptor.MediaType {
	case MediaTypeSPDX, MediaTypeInToto:
	default:
		return ocispec.Descriptor{}, fmt.Errorf("media type %q: accepted: %s, %s", doc.Descriptor.MediaType, MediaTypeSPDX, MediaTypeInToto)
	}
	data, err := os.ReadFile(doc.Path)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := oci.VerifyBlob(ctx, bytes.NewReader(data), doc.Descriptor); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("changed since it was written: %w", err)
	}
	layer, err := st.put(doc.Descriptor.MediaType, data)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	layer.Annotations = map[string]string{ocispec.AnnotationTitle: filepath.Base(doc.Path)}
	desc, err := st.putJSON(ocispec.MediaTypeImageManifest, ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: doc.Descriptor.MediaType,
		Config:       ocispec.DescriptorEmptyJSON,
		Layers:       []ocispec.Descriptor{layer},
		Subject:      &subject,
	})
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	desc.ArtifactType = doc.Descriptor.MediaType
	return desc, nil
}
