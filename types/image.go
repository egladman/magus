package types

import (
	"fmt"

	"github.com/egladman/magus/spells"
)

// The image module's records at the Buzz boundary. The module namespaces them, so the
// registry drops the Image prefix from each Buzz name (image\Spec, image\Platform).

// ImageOS is an OCI platform os; ImageOSNone is unset and refused.
type ImageOS string

// ImageArchitecture is an OCI platform architecture; ImageArchitectureNone is unset and
// refused.
type ImageArchitecture string

// ImageVariant is an OCI platform variant; ImageVariantNone is no variant.
type ImageVariant string

const (
	ImageOSNone            ImageOS           = ""
	ImageOSLinux           ImageOS           = "linux"
	ImageArchitectureNone  ImageArchitecture = ""
	ImageArchitectureAMD64 ImageArchitecture = "amd64"
	ImageArchitectureARM64 ImageArchitecture = "arm64"
	ImageVariantNone       ImageVariant      = ""
	ImageVariantV8         ImageVariant      = "v8"
)

// ImageMediaType is the media type of a descriptor magus reads or writes.
type ImageMediaType string

const (
	ImageMediaTypeNone      ImageMediaType = ""
	ImageMediaTypeManifest  ImageMediaType = "application/vnd.oci.image.manifest.v1+json"
	ImageMediaTypeIndex     ImageMediaType = "application/vnd.oci.image.index.v1+json"
	ImageMediaTypeConfig    ImageMediaType = "application/vnd.oci.image.config.v1+json"
	ImageMediaTypeLayer     ImageMediaType = "application/vnd.oci.image.layer.v1.tar"
	ImageMediaTypeLayerGzip ImageMediaType = "application/vnd.oci.image.layer.v1.tar+gzip"
	ImageMediaTypeLayerZstd ImageMediaType = "application/vnd.oci.image.layer.v1.tar+zstd"
	ImageMediaTypeEmpty     ImageMediaType = "application/vnd.oci.empty.v1+json"
	ImageMediaTypeSPDX      ImageMediaType = "application/spdx+json"
	ImageMediaTypeInToto    ImageMediaType = "application/vnd.in-toto+json"
)

// ImageModePolicy is how a Tree sets each file's mode; unset is refused.
type ImageModePolicy string

// ImageSymlinkPolicy is what a Tree does with a host symlink; unset is refused.
type ImageSymlinkPolicy string

const (
	ImageModePolicyNone          ImageModePolicy    = ""
	ImageModePolicyFixed         ImageModePolicy    = "fixed"
	ImageModePolicyExecutableBit ImageModePolicy    = "executable-bit"
	ImageSymlinkPolicyNone       ImageSymlinkPolicy = ""
	ImageSymlinkPolicyRefuse     ImageSymlinkPolicy = "refuse"
	ImageSymlinkPolicyKeep       ImageSymlinkPolicy = "keep"
	ImageSymlinkPolicyFollow     ImageSymlinkPolicy = "follow"
)

// ImagePlatform is an OCI platform. Its values are Go's GOOS and GOARCH.
type ImagePlatform struct {
	OS           ImageOS
	Architecture ImageArchitecture
	Variant      ImageVariant
}

// ImageDigest is an OCI content digest. Empty fields mean unset.
type ImageDigest struct {
	Algorithm string
	Encoded   string
}

// String is the digest as OCI writes it, "sha256:<hex>", and "" when unset. The Buzz
// mirror's string() method returns the same.
func (d ImageDigest) String() string {
	if d.Algorithm == "" && d.Encoded == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s", d.Algorithm, d.Encoded)
}

// ImageFile places one host file in the image.
type ImageFile struct {
	Source string // host path, resolved against the magusfile's directory
	Path   string // absolute path in the image
	Mode   int    // permission bits from fs\mode("0755"); 0 is refused
	UID    int    `buzz:"uid"` // 0 is root
	GID    int    `buzz:"gid"`
}

// ImageTree places the contents of a host directory at an image path.
type ImageTree struct {
	Source   string
	Path     string
	Include  []string
	Exclude  []string
	Modes    ImageModePolicy
	Symlinks ImageSymlinkPolicy
	UID      int `buzz:"uid"`
	GID      int `buzz:"gid"`
}

// ImageSymlink places one symbolic link in the image.
type ImageSymlink struct {
	Path   string
	Target string
	UID    int `buzz:"uid"`
	GID    int `buzz:"gid"`
}

// ImageDirectory places a directory entry with its own mode and owner.
type ImageDirectory struct {
	Path string
	Mode int
	UID  int `buzz:"uid"`
	GID  int `buzz:"gid"`
}

// ImageConfig is the OCI image config's config object. null inherits the base's value;
// an empty list or string clears it.
type ImageConfig struct {
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

// ImageSpec is one image manifest to assemble: exactly one of base and scratch.
type ImageSpec struct {
	Base        string
	Scratch     bool
	Platform    ImagePlatform
	Files       []ImageFile
	Trees       []ImageTree
	Symlinks    []ImageSymlink
	Directories []ImageDirectory
	Config      ImageConfig
	Annotations map[string]string
	Created     string
}

// ImageDescriptor is an OCI descriptor.
type ImageDescriptor struct {
	MediaType    ImageMediaType
	ArtifactType ImageMediaType
	Digest       ImageDigest
	Size         int
	Platform     *ImagePlatform
	Annotations  map[string]string
}

// ImageAdded is the receipt of the layer an assemble wrote.
type ImageAdded struct {
	Layer       ImageDescriptor
	DiffID      ImageDigest
	Files       int
	Symlinks    int
	Directories int
}

// ImageLayout is an OCI Image Layout on disk. Platform, Config, Layers and Created are
// set when the image is one manifest; Added only by assemble.
type ImageLayout struct {
	Path       string
	Descriptor ImageDescriptor
	Referrers  []ImageDescriptor
	Platform   *ImagePlatform
	Config     *ImageDescriptor
	Layers     []ImageDescriptor
	Added      *ImageAdded
	Created    string
}

// ImageRegistryAuth authenticates a pull or a push. The password is the Secret a
// provider resolved, never a bare string.
type ImageRegistryAuth struct {
	Username string
	Password spells.Secret
}

// ImagePushed is what a registry holds after a push.
type ImagePushed struct {
	Repository string
	Digest     ImageDigest
	Tags       []string
	Reference  string // "<repository>@<digest>", what a signer or a deploy consumes
}

// ImageMaterial is one content-addressed input to a provenance statement.
type ImageMaterial struct {
	URI    string `buzz:"uri"`
	Digest ImageDigest
}

// ImageProvenance is what a provenance statement says built the image.
type ImageProvenance struct {
	BuilderID      string `buzz:"builderId"`
	SourceRevision string
	Materials      []ImageMaterial
}

// ImageDocument is a written SBOM or provenance file and the descriptor of its bytes.
type ImageDocument struct {
	Path       string
	Descriptor ImageDescriptor
}
