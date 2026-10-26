package image

import (
	"fmt"
	"io/fs"
	"math"
	"path"
	"slices"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Platform is an image's target platform as OCI spells it.
type Platform struct{ OS, Architecture, Variant string }

// File places one host file in the image.
type File struct {
	Source, Destination string
	Mode                fs.FileMode
	UID, GID            int
}

// Symlink places one symbolic link in the image.
type Symlink struct {
	Destination, Target string
	UID, GID            int
}

// Spec is everything one write needs.
type Spec struct {
	Base            string
	Platform        Platform
	Files           []File
	Symlinks        []Symlink
	Env             map[string]string
	UnsetEnv        []string
	User            string
	WorkingDir      string
	Entrypoint      []string
	ResetEntrypoint bool
	Cmd             []string
	ResetCmd        bool
	Labels          map[string]string
	Annotations     map[string]string
	Created         string
}

// Layout is an OCI image layout on disk and the descriptor of the one manifest
// or index it holds.
type Layout struct {
	Path       string
	Descriptor ocispec.Descriptor
	Platform   Platform
}

// Published is what a registry confirmed after a push.
type Published struct {
	Repository string
	Digest     string
	Tags       []string
}

// Validate applies R1: linux on amd64, or on arm64 with no variant or v8.
func (p Platform) Validate() error {
	if p.OS != "linux" {
		return fmt.Errorf("image: platform os %q: accepted: linux", p.OS)
	}
	switch p.Architecture {
	case "amd64":
		if p.Variant != "" {
			return fmt.Errorf("image: platform variant %q for amd64: accepted: \"\"", p.Variant)
		}
	case "arm64":
		if p.Variant != "" && p.Variant != "v8" {
			return fmt.Errorf("image: platform variant %q for arm64: accepted: \"\", v8", p.Variant)
		}
	default:
		return fmt.Errorf("image: platform architecture %q: accepted: amd64, arm64", p.Architecture)
	}
	return nil
}

// Validate checks the spec's own shape (R1, R3, R4, R7, R8, R17 keys) without
// touching the host or the base layout.
func (s Spec) Validate() error {
	if err := s.Platform.Validate(); err != nil {
		return fmt.Errorf("image.write: %w", err)
	}
	var dests []string
	for _, f := range s.Files {
		if err := validateDestination(f.Destination); err != nil {
			return err
		}
		if f.Mode&^0o7777 != 0 {
			return fmt.Errorf("image.write: file %q: mode %#o: only permission, setuid, setgid and sticky bits are allowed", f.Destination, uint32(f.Mode))
		}
		dests = append(dests, f.Destination)
	}
	for _, l := range s.Symlinks {
		if err := validateDestination(l.Destination); err != nil {
			return err
		}
		if l.Target == "" {
			return fmt.Errorf("image.write: symlink %q: target is empty", l.Destination)
		}
		if strings.ContainsRune(l.Target, 0) {
			return fmt.Errorf("image.write: symlink %q: target contains NUL", l.Destination)
		}
		dests = append(dests, l.Destination)
	}
	if err := checkOverlap(dests); err != nil {
		return err
	}
	for k := range s.Env {
		if err := validateEnvKey(k); err != nil {
			return err
		}
	}
	for _, k := range s.UnsetEnv {
		if err := validateEnvKey(k); err != nil {
			return err
		}
	}
	return nil
}

func validateDestination(d string) error {
	switch {
	case d == "" || d[0] != '/':
		return fmt.Errorf("image.write: destination %q: must be absolute", d)
	case d == "/":
		return fmt.Errorf("image.write: destination %q: must not be the root", d)
	case strings.ContainsRune(d, 0):
		return fmt.Errorf("image.write: destination %q: contains NUL", d)
	case strings.HasSuffix(d, "/"):
		return fmt.Errorf("image.write: destination %q: trailing slash", d)
	case slices.Contains(strings.Split(d, "/"), ".."):
		return fmt.Errorf("image.write: destination %q: contains a .. element", d)
	case path.Clean(d) != d:
		return fmt.Errorf("image.write: destination %q: not clean, want %q", d, path.Clean(d))
	}
	return nil
}

// checkOverlap compares neighbors after sorting: a destination beneath another
// entry sorts right after it, and a symlink counts as a non-directory like a file.
func checkOverlap(dests []string) error {
	sorted := slices.Sorted(slices.Values(dests))
	for i := 1; i < len(sorted); i++ {
		prev, cur := sorted[i-1], sorted[i]
		if prev == cur {
			return fmt.Errorf("image.write: destination %q: placed twice", cur)
		}
		if strings.HasPrefix(cur, prev+"/") {
			return fmt.Errorf("image.write: destination %q: below %q, which is not a directory", cur, prev)
		}
	}
	return nil
}

func validateEnvKey(k string) error {
	if k == "" || strings.Contains(k, "=") {
		return fmt.Errorf("image.write: env key %q: must be non-empty and contain no =", k)
	}
	return nil
}

var specKeys = []string{
	"base", "platform", "files", "symlinks", "env", "unsetEnv", "user", "workingDir",
	"entrypoint", "resetEntrypoint", "cmd", "resetCmd", "labels", "annotations", "created",
}

// DecodeSpec reads the map a Buzz ImageSpec arrives as. Keys are the Buzz field
// names; an unknown key or a value of the wrong type is an error, and an absent
// key leaves the field zero.
func DecodeSpec(m map[string]any) (Spec, error) {
	var s Spec
	for k := range m {
		if !slices.Contains(specKeys, k) {
			return Spec{}, fmt.Errorf("image.write: spec: unknown key %q", k)
		}
	}
	d := decoder{what: "spec"}
	s.Base = d.str(m, "base")
	s.User = d.str(m, "user")
	s.WorkingDir = d.str(m, "workingDir")
	s.Created = d.str(m, "created")
	s.ResetEntrypoint = d.boolean(m, "resetEntrypoint")
	s.ResetCmd = d.boolean(m, "resetCmd")
	s.Entrypoint = d.strs(m, "entrypoint")
	s.Cmd = d.strs(m, "cmd")
	s.UnsetEnv = d.strs(m, "unsetEnv")
	s.Env = d.strMap(m, "env")
	s.Labels = d.strMap(m, "labels")
	s.Annotations = d.strMap(m, "annotations")
	if raw, ok := m["platform"]; ok {
		pm, isMap := raw.(map[string]any)
		if !isMap {
			d.fail("platform", "an object", raw)
		} else {
			s.Platform, d.err = decodePlatform(pm, d.err)
		}
	}
	for i, raw := range d.list(m, "files") {
		fm, ok := raw.(map[string]any)
		if !ok {
			d.fail(fmt.Sprintf("files[%d]", i), "an object", raw)
			continue
		}
		s.Files = append(s.Files, d.file(i, fm))
	}
	for i, raw := range d.list(m, "symlinks") {
		lm, ok := raw.(map[string]any)
		if !ok {
			d.fail(fmt.Sprintf("symlinks[%d]", i), "an object", raw)
			continue
		}
		s.Symlinks = append(s.Symlinks, d.symlink(i, lm))
	}
	if d.err != nil {
		return Spec{}, d.err
	}
	return s, nil
}

// DecodePlatform checks the keys of a platform object and reads it; it does not
// validate the platform.
func DecodePlatform(m map[string]any) (Platform, error) {
	return decodePlatform(m, nil)
}

func decodePlatform(m map[string]any, prior error) (Platform, error) {
	d := decoder{what: "spec.platform", err: prior}
	d.only(m, "os", "architecture", "variant")
	p := Platform{OS: d.str(m, "os"), Architecture: d.str(m, "architecture"), Variant: d.str(m, "variant")}
	return p, d.err
}

// decoder keeps the first error so a decode reads as a list of fields rather
// than a check after each.
type decoder struct {
	what string
	err  error
}

func (d *decoder) fail(key, want string, got any) {
	if d.err == nil {
		d.err = fmt.Errorf("image.write: %s: %s: want %s, got %T", d.what, key, want, got)
	}
}

func (d *decoder) only(m map[string]any, keys ...string) {
	for k := range m {
		if !slices.Contains(keys, k) && d.err == nil {
			d.err = fmt.Errorf("image.write: %s: unknown key %q", d.what, k)
		}
	}
}

func (d *decoder) str(m map[string]any, key string) string {
	raw, ok := m[key]
	if !ok {
		return ""
	}
	v, ok := raw.(string)
	if !ok {
		d.fail(key, "a string", raw)
	}
	return v
}

func (d *decoder) boolean(m map[string]any, key string) bool {
	raw, ok := m[key]
	if !ok {
		return false
	}
	v, ok := raw.(bool)
	if !ok {
		d.fail(key, "a bool", raw)
	}
	return v
}

func (d *decoder) integer(m map[string]any, key string) int {
	raw, ok := m[key]
	if !ok {
		return 0
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
			return int(v)
		}
	}
	d.fail(key, "an int", raw)
	return 0
}

func (d *decoder) list(m map[string]any, key string) []any {
	raw, ok := m[key]
	if !ok {
		return nil
	}
	v, ok := raw.([]any)
	if !ok {
		d.fail(key, "a list", raw)
	}
	return v
}

func (d *decoder) strs(m map[string]any, key string) []string {
	var out []string
	for i, raw := range d.list(m, key) {
		v, ok := raw.(string)
		if !ok {
			d.fail(fmt.Sprintf("%s[%d]", key, i), "a string", raw)
			continue
		}
		out = append(out, v)
	}
	return out
}

func (d *decoder) strMap(m map[string]any, key string) map[string]string {
	raw, ok := m[key]
	if !ok {
		return nil
	}
	in, ok := raw.(map[string]any)
	if !ok {
		d.fail(key, "a map of strings", raw)
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		sv, ok := v.(string)
		if !ok {
			d.fail(fmt.Sprintf("%s.%s", key, k), "a string", v)
			continue
		}
		out[k] = sv
	}
	return out
}

func (d *decoder) file(i int, m map[string]any) File {
	sub := decoder{what: fmt.Sprintf("spec.files[%d]", i), err: d.err}
	sub.only(m, "source", "destination", "mode", "uid", "gid")
	mode := sub.integer(m, "mode")
	if mode < 0 || mode > math.MaxUint32 {
		sub.fail("mode", "a permission value", mode)
	}
	f := File{
		Source: sub.str(m, "source"), Destination: sub.str(m, "destination"),
		Mode: fs.FileMode(mode), UID: sub.integer(m, "uid"), GID: sub.integer(m, "gid"),
	}
	d.err = sub.err
	return f
}

func (d *decoder) symlink(i int, m map[string]any) Symlink {
	sub := decoder{what: fmt.Sprintf("spec.symlinks[%d]", i), err: d.err}
	sub.only(m, "destination", "target", "uid", "gid")
	l := Symlink{
		Destination: sub.str(m, "destination"), Target: sub.str(m, "target"),
		UID: sub.integer(m, "uid"), GID: sub.integer(m, "gid"),
	}
	d.err = sub.err
	return l
}
