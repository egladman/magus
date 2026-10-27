package image

import (
	"fmt"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Platform is an image's target platform as an image index and an image config spell
// it. Its values are Go's GOOS and GOARCH, so one Platform drives both the compiler and
// the manifest. The zero value is invalid: Validate refuses an empty OS or
// Architecture rather than assuming linux/amd64.
type Platform struct {
	OS           string
	Architecture string
	Variant      string
}

// ParsePlatform reads "os/architecture" or "os/architecture/variant", the form
// String writes and `docker --platform` takes. The result is validated.
func ParsePlatform(s string) (Platform, error) {
	parts := strings.Split(s, "/")
	if len(parts) < 2 || len(parts) > 3 {
		return Platform{}, fmt.Errorf("image: platform %q: want os/architecture[/variant]", s)
	}
	p := Platform{OS: parts[0], Architecture: parts[1]}
	if len(parts) == 3 {
		p.Variant = parts[2]
	}
	if err := p.Validate(); err != nil {
		return Platform{}, err
	}
	return p, nil
}

// ParsePlatforms parses each of specs with ParsePlatform and refuses two that name the
// same platform once normalized, which is the set a multi-arch build fans out over.
func ParsePlatforms(specs ...string) ([]Platform, error) {
	out := make([]Platform, 0, len(specs))
	seen := map[Platform]string{}
	for _, s := range specs {
		p, err := ParsePlatform(s)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[p.Normalize()]; dup {
			return nil, fmt.Errorf("image: platforms %q and %q name the same platform", prev, s)
		}
		seen[p.Normalize()] = s
		out = append(out, p)
	}
	return out, nil
}

// String renders p as ParsePlatform reads it.
func (p Platform) String() string {
	s := p.OS + "/" + p.Architecture
	if p.Variant != "" {
		s += "/" + p.Variant
	}
	return s
}

// MarshalText renders p as String does; an invalid platform is an error.
func (p Platform) MarshalText() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return []byte(p.String()), nil
}

// UnmarshalText parses text with ParsePlatform.
func (p *Platform) UnmarshalText(text []byte) error {
	parsed, err := ParsePlatform(string(text))
	if err != nil {
		return err
	}
	*p = parsed
	return nil
}

// Normalize fills the variant a platform implies: image-spec lists arm64's as v8, and
// registries publish arm64/v8, so linux/arm64 and linux/arm64/v8 compare equal after it.
// Use it to compare platforms; a written platform keeps what the caller gave.
func (p Platform) Normalize() Platform {
	if p.Architecture == "arm64" && p.Variant == "" {
		p.Variant = "v8"
	}
	return p
}

// Validate accepts linux on amd64, or on arm64 with no variant or v8: the platforms a
// layer magus writes can run on.
func (p Platform) Validate() error {
	if p.OS != "linux" {
		return fmt.Errorf("image: platform os %q: accepted: linux", p.OS)
	}
	switch p.Architecture {
	case "amd64":
		if p.Variant != "" {
			return fmt.Errorf("image: platform variant %q for amd64: accepted: none", p.Variant)
		}
	case "arm64":
		if p.Variant != "" && p.Variant != "v8" {
			return fmt.Errorf("image: platform variant %q for arm64: accepted: none, v8", p.Variant)
		}
	default:
		return fmt.Errorf("image: platform architecture %q: accepted: amd64, arm64", p.Architecture)
	}
	return nil
}

// GoEnv is the environment that makes `go build` target p: GOOS and GOARCH. arm64 v8 is
// Go's only arm64, so the variant adds nothing.
func (p Platform) GoEnv() (map[string]string, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return map[string]string{"GOOS": p.OS, "GOARCH": p.Architecture}, nil
}

func (p Platform) oci() *ocispec.Platform {
	return &ocispec.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}
}

func platformOf(p *ocispec.Platform) Platform {
	if p == nil {
		return Platform{}
	}
	return Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}
}
