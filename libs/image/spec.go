package image

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
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

// Spec is everything one assembleLayout needs. Prefer NewSpec and With* in Go.
type Spec struct {
	Base            string
	Platform        Platform
	Files           []File
	Dirs            []DirPlacement
	Symlinks        []Symlink
	Env             map[string]string
	UnsetEnv        []string
	User            string
	WorkingDir      string
	Entrypoint      []string
	ResetEntrypoint bool
	Command         []string // OCI config Cmd (argv); not magus.cmd / ShellCommand
	ResetCommand    bool
	Labels          map[string]string
	Annotations     map[string]string
	Created         string
}

// expandDirs folds DirPlacements into Files/Symlinks.
func (s *Spec) expandDirs(ctx context.Context) error {
	for i, d := range s.Dirs {
		files, symlinks, _, err := d.expand(ctx)
		if err != nil {
			return fmt.Errorf("image.assemble: dirs[%d]: %w", i, err)
		}
		s.Files = append(s.Files, files...)
		s.Symlinks = append(s.Symlinks, symlinks...)
	}
	s.Dirs = nil
	return nil
}

// Layout is an OCI image layout on disk and the facts of the operation that
// wrote it. It is not a log: callers that want prose about the op print their own.
type Layout struct {
	Path         string
	Descriptor   ocispec.Descriptor
	Platform     Platform
	Op           Op
	Base         string // layout path or registry reference used as base; empty for scratch
	BaseDigest   Digest
	FileCount    int
	SymlinkCount int
	Layer        Layer  // new OCI Layer; zero when assemble added none
	ConfigDigest Digest // ManifestDigest is Descriptor.Digest
	// Created is the image config Created timestamp (RFC 3339), empty when the
	// config omits it. Callers compare it to wall clock to decide whether to :update.
	Created string
}

// Published is what a registry confirmed after a push.
type Published struct {
	Repository string
	Digest     Digest
	Tags       []string
	Op         Op
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
		return fmt.Errorf("image.assemble: %w", err)
	}
	var dests []string
	for _, f := range s.Files {
		if err := validateDestination(f.Destination); err != nil {
			return err
		}
		if f.Mode&^0o7777 != 0 {
			return fmt.Errorf("image.assemble: file %q: mode %#o: only permission, setuid, setgid and sticky bits are allowed", f.Destination, uint32(f.Mode))
		}
		dests = append(dests, f.Destination)
	}
	for _, l := range s.Symlinks {
		if err := validateDestination(l.Destination); err != nil {
			return err
		}
		if l.Target == "" {
			return fmt.Errorf("image.assemble: symlink %q: target is empty", l.Destination)
		}
		if strings.ContainsRune(l.Target, 0) {
			return fmt.Errorf("image.assemble: symlink %q: target contains NUL", l.Destination)
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
		return fmt.Errorf("image.assemble: destination %q: must be absolute", d)
	case d == "/":
		return fmt.Errorf("image.assemble: destination %q: must not be the root", d)
	case strings.ContainsRune(d, 0):
		return fmt.Errorf("image.assemble: destination %q: contains NUL", d)
	case strings.HasSuffix(d, "/"):
		return fmt.Errorf("image.assemble: destination %q: trailing slash", d)
	case slices.Contains(strings.Split(d, "/"), ".."):
		return fmt.Errorf("image.assemble: destination %q: contains a .. element", d)
	case path.Clean(d) != d:
		return fmt.Errorf("image.assemble: destination %q: not clean, want %q", d, path.Clean(d))
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
			return fmt.Errorf("image.assemble: destination %q: placed twice", cur)
		}
		if strings.HasPrefix(cur, prev+"/") {
			return fmt.Errorf("image.assemble: destination %q: below %q, which is not a directory", cur, prev)
		}
	}
	return nil
}

func validateEnvKey(k string) error {
	if k == "" || strings.Contains(k, "=") {
		return fmt.Errorf("image.assemble: env key %q: must be non-empty and contain no =", k)
	}
	return nil
}

var specKeys = []string{
	"base", "platform", "files", "dirs", "symlinks", "env", "unsetEnv", "user", "workingDir",
	"entrypoint", "resetEntrypoint", "command", "resetCommand", "labels", "annotations", "created",
}

// DecodeSpec reads the map a Buzz ImageSpec arrives as. Keys are the Buzz field
// names; an unknown key or a value of the wrong type is an error, and an absent
// key leaves the field zero.
func DecodeSpec(m map[string]any) (Spec, error) {
	for k := range m {
		if !slices.Contains(specKeys, k) {
			return Spec{}, fmt.Errorf("image.assemble: spec: unknown key %q", k)
		}
	}
	d := decoder{what: "spec"}
	s := decodeSpecScalars(&d, m)
	if raw, ok := m["platform"]; ok {
		pm, isMap := raw.(map[string]any)
		if !isMap {
			d.fail("platform", "an object", raw)
		} else {
			s.Platform, d.err = decodePlatform(pm, d.err)
		}
	}
	decodeSpecPlacements(&d, m, &s)
	if d.err != nil {
		return Spec{}, d.err
	}
	return s, nil
}

func decodeSpecScalars(d *decoder, m map[string]any) Spec {
	return Spec{
		Base:            d.str(m, "base"),
		User:            d.str(m, "user"),
		WorkingDir:      d.str(m, "workingDir"),
		Created:         d.str(m, "created"),
		ResetEntrypoint: d.boolean(m, "resetEntrypoint"),
		ResetCommand:    d.boolean(m, "resetCommand"),
		Entrypoint:      d.strs(m, "entrypoint"),
		Command:         d.strs(m, "command"),
		UnsetEnv:        d.strs(m, "unsetEnv"),
		Env:             d.strMap(m, "env"),
		Labels:          d.strMap(m, "labels"),
		Annotations:     d.strMap(m, "annotations"),
	}
}

func decodeSpecPlacements(d *decoder, m map[string]any, s *Spec) {
	for i, raw := range d.list(m, "files") {
		fm, ok := raw.(map[string]any)
		if !ok {
			d.fail(fmt.Sprintf("files[%d]", i), "an object", raw)
			continue
		}
		s.Files = append(s.Files, d.file(i, fm))
	}
	for i, raw := range d.list(m, "dirs") {
		dm, ok := raw.(map[string]any)
		if !ok {
			d.fail(fmt.Sprintf("dirs[%d]", i), "an object", raw)
			continue
		}
		s.Dirs = append(s.Dirs, d.dir(i, dm))
	}
	for i, raw := range d.list(m, "symlinks") {
		lm, ok := raw.(map[string]any)
		if !ok {
			d.fail(fmt.Sprintf("symlinks[%d]", i), "an object", raw)
			continue
		}
		s.Symlinks = append(s.Symlinks, d.symlink(i, lm))
	}
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
		d.err = fmt.Errorf("image.assemble: %s: %s: want %s, got %T", d.what, key, want, got)
	}
}

func (d *decoder) only(m map[string]any, keys ...string) {
	for k := range m {
		if !slices.Contains(keys, k) && d.err == nil {
			d.err = fmt.Errorf("image.assemble: %s: unknown key %q", d.what, k)
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

func (d *decoder) dir(i int, m map[string]any) DirPlacement {
	sub := decoder{what: fmt.Sprintf("spec.dirs[%d]", i), err: d.err}
	sub.only(m, "source", "destination", "strip", "include", "exclude", "maxDepth",
		"modePolicy", "fileMode", "dirMode", "uid", "gid", "symlinks", "emptyDirs", "maxFiles", "maxBytes")
	placement := DirPlacement{
		Source:      HostDir{Value: sub.str(m, "source")},
		Destination: ImageDir{Value: sub.str(m, "destination")},
		Strip:       sub.integer(m, "strip"),
		Include:     sub.strs(m, "include"),
		Exclude:     sub.strs(m, "exclude"),
		MaxDepth:    sub.integer(m, "maxDepth"),
		ModePolicy:  ModePolicy(sub.str(m, "modePolicy")),
		FileMode:    Mode(sub.integer(m, "fileMode")),
		DirMode:     Mode(sub.integer(m, "dirMode")),
		UID:         sub.integer(m, "uid"),
		GID:         sub.integer(m, "gid"),
		Symlinks:    SymlinkPolicy(sub.str(m, "symlinks")),
		EmptyDirs:   sub.boolean(m, "emptyDirs"),
		MaxFiles:    sub.integer(m, "maxFiles"),
		MaxBytes:    int64(sub.integer(m, "maxBytes")),
	}
	d.err = sub.err
	return placement
}

// NewSpec starts a Spec for one platform. Chain With* methods, then
// AssembleLayout. This is not a Dockerfile: there is no FROM, COPY, or RUN.
func NewSpec(platform Platform) *Spec {
	return &Spec{Platform: platform}
}

// AssembleLayout builds the OCI Image Layout described by s into dest.
func (s *Spec) AssembleLayout(ctx context.Context, dest string) (Layout, error) {
	if s == nil {
		return Layout{}, fmt.Errorf("image.assemble: nil spec")
	}
	return AssembleLayout(ctx, *s, dest)
}

// WithBase sets the base Image Layout path (digest-pinned pull or prior assemble).
// Empty clears to scratch. Not Dockerfile FROM: no tags, no build stages.
func (s *Spec) WithBase(layoutPath string) *Spec {
	s.Base = layoutPath
	return s
}

// WithFile appends one host-to-image file placement (the Layer content unit).
func (s *Spec) WithFile(f File) *Spec {
	s.Files = append(s.Files, f)
	return s
}

// WithDir appends one directory expansion into Layer entries.
func (s *Spec) WithDir(d DirPlacement) *Spec {
	s.Dirs = append(s.Dirs, d)
	return s
}

// WithSymlink appends one symlink placement.
func (s *Spec) WithSymlink(l Symlink) *Spec {
	s.Symlinks = append(s.Symlinks, l)
	return s
}

// WithEnv merges key into the image config Env (over base by key).
func (s *Spec) WithEnv(key, value string) *Spec {
	if s.Env == nil {
		s.Env = map[string]string{}
	}
	s.Env[key] = value
	return s
}

// WithEnvMap merges every entry into config Env.
func (s *Spec) WithEnvMap(env map[string]string) *Spec {
	if len(env) == 0 {
		return s
	}
	if s.Env == nil {
		s.Env = maps.Clone(env)
		return s
	}
	maps.Copy(s.Env, env)
	return s
}

// WithUnsetEnv removes keys inherited from the base config Env.
func (s *Spec) WithUnsetEnv(keys ...string) *Spec {
	s.UnsetEnv = append(s.UnsetEnv, keys...)
	return s
}

// WithUser sets the image config User.
func (s *Spec) WithUser(user string) *Spec {
	s.User = user
	return s
}

// WithWorkingDir sets the image config WorkingDir.
func (s *Spec) WithWorkingDir(dir string) *Spec {
	s.WorkingDir = dir
	return s
}

// WithEntrypoint sets the image config Entrypoint (OCI Config, not a shell form).
func (s *Spec) WithEntrypoint(args ...string) *Spec {
	s.Entrypoint = append([]string(nil), args...)
	return s
}

// WithResetEntrypoint clears the base Entrypoint before WithEntrypoint applies.
func (s *Spec) WithResetEntrypoint() *Spec {
	s.ResetEntrypoint = true
	return s
}

// WithCommand sets the image config process argv (OCI Config.Cmd).
func (s *Spec) WithCommand(args ...string) *Spec {
	s.Command = append([]string(nil), args...)
	return s
}

// WithResetCommand clears the base command before WithCommand applies.
func (s *Spec) WithResetCommand() *Spec {
	s.ResetCommand = true
	return s
}

// WithLabel merges a config label.
func (s *Spec) WithLabel(key, value string) *Spec {
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	s.Labels[key] = value
	return s
}

// WithAnnotation merges a manifest annotation.
func (s *Spec) WithAnnotation(key, value string) *Spec {
	if s.Annotations == nil {
		s.Annotations = map[string]string{}
	}
	s.Annotations[key] = value
	return s
}

// WithCreated sets the image config created timestamp (RFC 3339).
func (s *Spec) WithCreated(rfc3339 string) *Spec {
	s.Created = rfc3339
	return s
}
