package types

// ImagePlatform is an image's target platform as OCI spells it.
type ImagePlatform struct {
	OS           string
	Architecture string
	Variant      string
}

// ImageFile places one host file in the image.
type ImageFile struct {
	Source      string // host path, resolved against the magusfile's cwd
	Destination string // absolute POSIX path inside the image
	Mode        int    // permission bits, e.g. 0o755; type bits are rejected
	UID         int
	GID         int
}

// ImageSymlink places one symbolic link in the image.
type ImageSymlink struct {
	Destination string
	Target      string
	UID         int
	GID         int
}

// ImageDirPlacement expands a host directory into layer entries.
type ImageDirPlacement struct {
	Source      string
	Destination string
	Strip       int
	Include     []string
	Exclude     []string
	MaxDepth    int
	ModePolicy  string
	FileMode    int
	DirMode     int
	UID         int
	GID         int
	Symlinks    string
	EmptyDirs   bool
	MaxFiles    int
	MaxBytes    int
}

// ImageSpec is everything one write needs. Empty fields inherit from Base.
type ImageSpec struct {
	Base            string // OCI layout dir holding exactly one manifest; "" is scratch
	Platform        ImagePlatform
	Files           []ImageFile
	Dirs            []ImageDirPlacement
	Symlinks        []ImageSymlink
	Env             map[string]string // merged over the base's by key
	UnsetEnv        []string
	User            string
	WorkingDir      string
	Entrypoint      []string
	ResetEntrypoint bool     // true clears the base's entrypoint before Entrypoint applies
	Command         []string // OCI config Cmd (argv); spelled out so it is not confused with magus.cmd
	ResetCommand    bool
	Labels          map[string]string // config labels, merged over the base's
	Annotations     map[string]string // manifest annotations
	Created         string            // RFC 3339; "" keeps the base's
}

// ImageDigest is an OCI content digest at the Buzz boundary: algorithm + encoded.
// Empty Algorithm and Encoded mean unset. Only sha256 is produced today.
type ImageDigest struct {
	Algorithm string
	Encoded   string
}

// ImageLayout is an OCI image layout on disk and the facts of the operation that
// wrote it. It is not a log.
type ImageLayout struct {
	Path         string
	Digest       ImageDigest
	MediaType    string
	Size         int
	Platform     ImagePlatform // zero for an index
	Op           string
	Base         string
	BaseDigest   ImageDigest
	FileCount    int
	SymlinkCount int
	Layer        ImageLayer // zero when assemble added none
	ConfigDigest ImageDigest
	Created      string // RFC 3339 from image config; empty when omitted
}

// ImageLayer is one OCI image layer at the Buzz boundary.
type ImageLayer struct {
	Digest    ImageDigest // compressed blob
	DiffID    ImageDigest // uncompressed tar (rootfs.diff_id)
	Size      int
	MediaType string
}

// ImagePublished is what a registry now holds.
type ImagePublished struct {
	Repository string
	Digest     ImageDigest
	Tags       []string
	Op         string
}

// ImageSecret is a credential at the Buzz boundary for registry auth.
type ImageSecret struct {
	Value string
}

// ImageRegistryAuth authenticates pull and publish.
type ImageRegistryAuth struct {
	Username string
	Password ImageSecret
}

// ImageSBOM is a written SPDX document.
type ImageSBOM struct {
	Path      string
	MediaType string
	Digest    ImageDigest
}

// ImageProvenanceSpec is the explicit material for an in-toto statement.
type ImageProvenanceSpec struct {
	SourceRevision string
	BuilderID      string
}

// ImageProvenance is a written in-toto statement.
type ImageProvenance struct {
	Path      string
	MediaType string
	Digest    ImageDigest
}
