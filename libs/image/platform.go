package image

// NewLinuxAmd64 is linux/amd64 with no variant.
func NewLinuxAmd64() Platform { return Platform{OS: "linux", Architecture: "amd64"} }

// NewLinuxArm64 is linux/arm64 with no variant.
func NewLinuxArm64() Platform { return Platform{OS: "linux", Architecture: "arm64"} }
