// Package image holds the image module's records at the Buzz boundary. The
// package namespaces them in Go as the module does in Buzz (image\Spec).
package image

import (
	"fmt"

	"github.com/egladman/magus/spells"
)

// OS is an OCI platform os; OSNone is unset and refused.
type OS string

// Architecture is an OCI platform architecture; ArchitectureNone is unset and
// refused.
type Architecture string

// Variant is an OCI platform variant; VariantNone is no variant.
type Variant string

const (
	OSNone            OS           = ""
	OSLinux           OS           = "linux"
	ArchitectureNone  Architecture = ""
	ArchitectureAMD64 Architecture = "amd64"
	ArchitectureARM64 Architecture = "arm64"
	VariantNone       Variant      = ""
	VariantV1         Variant      = "v1"
	VariantV2         Variant      = "v2"
	VariantV3         Variant      = "v3"
	VariantV8         Variant      = "v8"
)

// MediaType names the media types magus writes, which Buzz reads as image\MediaType.
// A descriptor's MediaType and ArtifactType are plain strings: a pulled base or a
// referrer may carry a type this set does not name.
type MediaType string

const (
	MediaTypeManifest  MediaType = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeIndex     MediaType = "application/vnd.oci.image.index.v1+json"
	MediaTypeConfig    MediaType = "application/vnd.oci.image.config.v1+json"
	MediaTypeLayer     MediaType = "application/vnd.oci.image.layer.v1.tar"
	MediaTypeLayerGzip MediaType = "application/vnd.oci.image.layer.v1.tar+gzip"
	MediaTypeLayerZstd MediaType = "application/vnd.oci.image.layer.v1.tar+zstd"
	MediaTypeEmpty     MediaType = "application/vnd.oci.empty.v1+json"
	MediaTypeSPDX      MediaType = "application/spdx+json"
	MediaTypeInToto    MediaType = "application/vnd.in-toto+json"
)

// ModePolicy is how a Tree sets each file's mode; unset is refused.
type ModePolicy string

// SymlinkPolicy is what a Tree does with a host symlink; unset is refused.
type SymlinkPolicy string

const (
	ModePolicyNone          ModePolicy    = ""
	ModePolicyFixed         ModePolicy    = "fixed"
	ModePolicyExecutableBit ModePolicy    = "executable-bit"
	SymlinkPolicyNone       SymlinkPolicy = ""
	SymlinkPolicyRefuse     SymlinkPolicy = "refuse"
	SymlinkPolicyKeep       SymlinkPolicy = "keep"
	SymlinkPolicyFollow     SymlinkPolicy = "follow"
)

// Platform is an OCI platform. Its values are Go's GOOS and GOARCH.
type Platform struct {
	OS           OS
	Architecture Architecture
	Variant      Variant
	OSVersion    string
	OSFeatures   []string
}

// Digest is an OCI content digest. Empty fields mean unset.
type Digest struct {
	Algorithm string
	Encoded   string
}

// String is the digest as OCI writes it, "sha256:<hex>", and "" when unset. The Buzz
// mirror's string() method returns the same.
func (d Digest) String() string {
	if d.Algorithm == "" && d.Encoded == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s", d.Algorithm, d.Encoded)
}

// File places one host file in the image.
type File struct {
	Source string // host path, resolved against the magusfile's directory
	Path   string // absolute path in the image
	Mode   int    // permission bits from fs\mode("0755"); 0 is refused
	UID    int    `buzz:"uid"` // 0 is root
	GID    int    `buzz:"gid"`
}

// Tree places the contents of a host directory at an image path.
type Tree struct {
	Source     string
	Path       string
	Include    []string
	Exclude    []string
	ModePolicy ModePolicy
	Symlinks   SymlinkPolicy
	UID        int `buzz:"uid"`
	GID        int `buzz:"gid"`
}

// Symlink places one symbolic link in the image.
type Symlink struct {
	Path   string
	Target string
	UID    int `buzz:"uid"`
	GID    int `buzz:"gid"`
}

// Directory places a directory entry with its own mode and owner.
type Directory struct {
	Path string
	Mode int
	UID  int `buzz:"uid"`
	GID  int `buzz:"gid"`
}

// Config is the OCI image config's config object. null inherits the base's value;
// an empty list or string clears it.
type Config struct {
	User         *string
	WorkingDir   *string
	StopSignal   *string
	Entrypoint   *[]string
	Cmd          *[]string
	ExposedPorts *[]string
	Volumes      *[]string
	Env          map[string]string
	UnsetEnv     []string
	Labels       map[string]string
}

// Spec is one image manifest to assemble: exactly one of base and scratch.
type Spec struct {
	Base        string
	Scratch     bool
	Platform    Platform
	Files       []File
	Trees       []Tree
	Symlinks    []Symlink
	Directories []Directory
	Config      Config
	Annotations map[string]string
	Created     string
}

// Descriptor is an OCI descriptor.
type Descriptor struct {
	MediaType    string
	ArtifactType string
	Digest       Digest
	Size         int
	Platform     *Platform
	Annotations  map[string]string
}

// EntryKind is the kind of one tar entry.
type EntryKind string

const (
	EntryKindNone      EntryKind = ""
	EntryKindFile      EntryKind = "file"
	EntryKindSymlink   EntryKind = "symlink"
	EntryKindDirectory EntryKind = "directory"
)

// Entry is one tar entry of the new layer, as its header is written.
// Origin names the Spec item that placed it, such as "files[0]" or
// "trees[1] etc/ssl/ca.pem". A directory scratch adds is "parent".
type Entry struct {
	Path    string // absolute; the tar name drops the leading slash
	Kind    EntryKind
	Mode    string // four octal digits, as fs\mode reads them
	UID     int    `buzz:"uid"`
	GID     int    `buzz:"gid"`
	Size    int    // 0 except for a file
	Target  string // a symlink's
	ModTime string // RFC 3339; always the Unix epoch
	Origin  string
}

// AddedLayer is the layer an assemble wrote. Under image\layout and a dry
// run it is the layer one would write, with no Descriptor or DiffID yet.
type AddedLayer struct {
	Descriptor  Descriptor
	DiffID      Digest
	Entries     []Entry
	Files       int
	Symlinks    int
	Directories int
}

// Layout is an OCI Image Layout on disk. Platform, Config, Layers and
// Created are set when the image is one manifest. Added is set only by
// assemble and layout, and layout sets nothing but Platform and Added.
type Layout struct {
	Path       string
	Descriptor Descriptor
	Referrers  []Descriptor
	Platform   *Platform
	Config     *Descriptor
	Layers     []Descriptor
	Added      *AddedLayer
	Created    string
}

// RegistryAuth authenticates a pull or a push. The password is the Secret a
// provider resolved, never a bare string.
type RegistryAuth struct {
	Username string
	Password spells.Secret
}

// Pushed is what a registry holds after a push.
type Pushed struct {
	Repository string
	Digest     Digest
	Tags       []string
	Reference  string // "<repository>@<digest>", what a signer or a deploy consumes
}

// Dependency is one content-addressed input to a provenance statement: a SLSA v1
// resolved dependency.
type Dependency struct {
	URI    string `buzz:"uri"`
	Digest Digest
}

// Provenance is what a provenance statement says built the image.
type Provenance struct {
	BuilderID      string
	SourceRevision string
	Dependencies   []Dependency
}

// Document is a written SBOM or provenance file and the descriptor of its bytes.
type Document struct {
	Path       string
	Descriptor Descriptor
}
