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

// ImageSpec is everything one write needs. Empty fields inherit from Base.
type ImageSpec struct {
	Base            string // OCI layout dir holding exactly one manifest; "" is scratch
	Platform        ImagePlatform
	Files           []ImageFile
	Symlinks        []ImageSymlink
	Env             map[string]string // merged over the base's by key
	UnsetEnv        []string
	User            string
	WorkingDir      string
	Entrypoint      []string
	ResetEntrypoint bool // true clears the base's entrypoint before Entrypoint applies
	Cmd             []string
	ResetCmd        bool
	Labels          map[string]string // config labels, merged over the base's
	Annotations     map[string]string // manifest annotations
	Created         string            // RFC 3339; "" keeps the base's
}

// ImageLayout is an OCI image layout on disk and the one manifest or index it holds.
type ImageLayout struct {
	Path      string
	Digest    string // manifest or index digest, "sha256:<hex>"
	MediaType string
	Size      int
	Platform  ImagePlatform // zero for an index
}

// ImagePublished is what a registry now holds.
type ImagePublished struct {
	Repository string
	Digest     string
	Tags       []string
}
