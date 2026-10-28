package image

import (
	"fmt"
	"slices"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Platform is an image's target platform as an image index and an image config spell
// it. Its values are Go's GOOS and GOARCH, so one Platform drives both the compiler and
// the manifest. The zero value is invalid: Validate refuses an empty OS or
// Architecture rather than assuming linux/amd64. OSVersion and OSFeatures are carried
// as the index or config states them and take no part in matching.
type Platform struct {
	OS           string
	Architecture string
	Variant      string
	OSVersion    string
	OSFeatures   []string
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
	seen := map[string]string{}
	for _, s := range specs {
		p, err := ParsePlatform(s)
		if err != nil {
			return nil, err
		}
		key := p.Normalize().String()
		if prev, dup := seen[key]; dup {
			return nil, fmt.Errorf("image: platforms %q and %q name the same platform", prev, s)
		}
		seen[key] = s
		out = append(out, p)
	}
	return out, nil
}

// String renders p as ParsePlatform reads it.
func (p Platform) String() string {
	parts := []string{p.OS, p.Architecture}
	if p.Variant != "" {
		parts = append(parts, p.Variant)
	}
	return strings.Join(parts, "/")
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

// Normalize spells the variant a platform implies one way: image-spec lists arm64's as
// v8, which registries publish, and amd64's baseline as v1, which registries omit. So
// linux/arm64 equals linux/arm64/v8 and linux/amd64 equals linux/amd64/v1 after it. Use
// it to compare platforms; a written platform keeps what the caller gave.
func (p Platform) Normalize() Platform {
	switch {
	case p.Architecture == "arm64" && p.Variant == "":
		p.Variant = "v8"
	case p.Architecture == "amd64" && p.Variant == "v1":
		p.Variant = ""
	}
	return p
}

// Matches reports whether p and q name the same os, architecture and variant once
// normalized.
func (p Platform) Matches(q Platform) bool {
	return p.Normalize().String() == q.Normalize().String()
}

// Validate accepts linux on amd64 with no variant or v1, v2 or v3, or on arm64 with no
// variant or v8: the platforms a layer magus writes can run on.
func (p Platform) Validate() error {
	if p.OS != "linux" {
		return fmt.Errorf("image: platform os %q: accepted: linux", p.OS)
	}
	switch p.Architecture {
	case "amd64":
		if !slices.Contains([]string{"", "v1", "v2", "v3"}, p.Variant) {
			return fmt.Errorf("image: platform variant %q for amd64: accepted: none, v1, v2, v3", p.Variant)
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

// GoEnv is the environment that makes `go build` target p: GOOS, GOARCH, and GOAMD64
// for an amd64 variant. arm64 v8 is Go's only arm64, so its variant adds nothing.
func (p Platform) GoEnv() (map[string]string, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	env := map[string]string{"GOOS": p.OS, "GOARCH": p.Architecture}
	if p.Architecture == "amd64" && p.Variant != "" {
		env["GOAMD64"] = p.Variant
	}
	return env, nil
}

func (p Platform) oci() *ocispec.Platform {
	return &ocispec.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant, OSVersion: p.OSVersion, OSFeatures: p.OSFeatures}
}

func platformFromOCI(p *ocispec.Platform) Platform {
	if p == nil {
		return Platform{}
	}
	return Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant, OSVersion: p.OSVersion, OSFeatures: p.OSFeatures}
}

func platformFromConfig(cfg ocispec.Image) Platform {
	return platformFromOCI(&cfg.Platform)
}
