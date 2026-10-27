package image

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// Spec is one Image Manifest: a base, at most one new layer built from the
// placements, and the image config. Build it as a struct literal; the zero value is
// rejected (no base, no platform).
type Spec struct {
	// Base is an OCI Image Layout directory to build over: one image manifest, or an
	// index holding Platform. Exactly one of Base and Scratch is set.
	Base    string
	Scratch bool
	// Platform the image runs on. Over a base it must match the base's, normalized.
	Platform Platform

	// The new layer's entries. An empty set adds no layer: a config-only change.
	Files       []File
	Trees       []Tree
	Symlinks    []Symlink
	Directories []Directory

	Config Config
	// Annotations are the manifest's own. Over a base, org.opencontainers.image.base.digest
	// and, when the base came from Pull, base.name are added.
	Annotations map[string]string
	// Created is RFC 3339. Empty keeps the base's; on scratch the config omits it, which
	// keeps the image byte-identical across runs.
	Created string
}

// File places one host file at Path.
type File struct {
	Source   string // host path
	Path     string // absolute POSIX path in the image
	Mode     Mode
	UID, GID int // 0 is root
}

// Tree places the CONTENTS of the host directory Source at Path: Source/a/b lands at
// Path/a/b. Only files and symlinks become entries; an empty host directory is not
// placed, and a directory that must exist with its own mode is a Directory.
type Tree struct {
	Source string
	Path   string // "/" places the contents at the root
	// Include and Exclude are doublestar patterns over each file's path relative to
	// Source. Empty Include takes every file; Exclude wins over Include.
	Include, Exclude []string
	ModePolicy       ModePolicy
	Symlinks         SymlinkPolicy
	UID, GID         int
}

// Symlink places one symbolic link at Path pointing at Target.
type Symlink struct {
	Path, Target string
	UID, GID     int
}

// Directory places a directory entry with its own mode and owner, such as /tmp at
// ModeSticky or a user's home owned by them.
type Directory struct {
	Path     string
	Mode     Mode
	UID, GID int
}

// Config is the image config's `config` object, field for field. A nil field inherits
// the base's value and an empty one clears it. Env and Labels merge over the base's by
// key; UnsetEnv then removes keys.
type Config struct {
	User         *string
	WorkingDir   *string
	StopSignal   *string
	Entrypoint   *[]string
	Cmd          *[]string
	ExposedPorts *[]string // "8080/tcp", "53/udp"
	Volumes      *[]string
	Env          map[string]string
	UnsetEnv     []string
	Labels       map[string]string
}

// ModePolicy decides the mode of each file a Tree places. There is no "keep the host
// mode": that depends on the umask of whoever checked the files out.
type ModePolicy string

const (
	// ModePolicyFixed places every file at ModeFile.
	ModePolicyFixed ModePolicy = "fixed"
	// ModePolicyExecutableBit places a file whose host owner-execute bit is set at
	// ModeExecutable and any other at ModeFile; that bit is in the cache key already.
	ModePolicyExecutableBit ModePolicy = "executable-bit"
)

// SymlinkPolicy decides what a Tree does with a host symlink.
type SymlinkPolicy string

const (
	// SymlinkPolicyRefuse fails the assemble.
	SymlinkPolicyRefuse SymlinkPolicy = "refuse"
	// SymlinkPolicyKeep places the link. Its target must be relative and stay inside the
	// tree: an absolute target names a host path.
	SymlinkPolicyKeep SymlinkPolicy = "keep"
	// SymlinkPolicyFollow places the regular file the link resolves to.
	SymlinkPolicyFollow SymlinkPolicy = "follow"
)

func (m ModePolicy) validate() error {
	switch m {
	case ModePolicyFixed, ModePolicyExecutableBit:
		return nil
	case "":
		return fmt.Errorf("modePolicy is unset, want %s or %s", ModePolicyFixed, ModePolicyExecutableBit)
	}
	return fmt.Errorf("modePolicy %q, want %s or %s", string(m), ModePolicyFixed, ModePolicyExecutableBit)
}

func (s SymlinkPolicy) validate() error {
	switch s {
	case SymlinkPolicyRefuse, SymlinkPolicyKeep, SymlinkPolicyFollow:
		return nil
	case "":
		return fmt.Errorf("symlinks is unset, want %s, %s or %s", SymlinkPolicyRefuse, SymlinkPolicyKeep, SymlinkPolicyFollow)
	}
	return fmt.Errorf("symlinks %q, want %s, %s or %s", string(s), SymlinkPolicyRefuse, SymlinkPolicyKeep, SymlinkPolicyFollow)
}

// Validate checks the spec's own shape without touching the host or the base.
func (s Spec) Validate() error {
	if err := s.validate(); err != nil {
		return fmt.Errorf("image.assemble: %w", err)
	}
	return nil
}

func (s Spec) validate() error {
	switch {
	case s.Base == "" && !s.Scratch:
		return fmt.Errorf("set base to a layout or scratch to true")
	case s.Base != "" && s.Scratch:
		return fmt.Errorf("base %q and scratch are both set", s.Base)
	}
	if err := s.Platform.Validate(); err != nil {
		return err
	}
	if _, _, err := s.created(); err != nil {
		return err
	}
	if err := s.validatePlacements(); err != nil {
		return err
	}
	return s.Config.validate()
}

func (s Spec) validatePlacements() error {
	for i, f := range s.Files {
		if f.Source == "" {
			return fmt.Errorf("files[%d]: source is empty", i)
		}
		if err := checkImagePath(f.Path); err != nil {
			return fmt.Errorf("files[%d]: %w", i, err)
		}
		if err := f.Mode.Validate(); err != nil {
			return fmt.Errorf("files[%d] %s: %w", i, f.Path, err)
		}
		if err := checkOwner(f.UID, f.GID); err != nil {
			return fmt.Errorf("files[%d] %s: %w", i, f.Path, err)
		}
	}
	for i, t := range s.Trees {
		if err := t.validate(); err != nil {
			return fmt.Errorf("trees[%d]: %w", i, err)
		}
	}
	for i, l := range s.Symlinks {
		if err := checkImagePath(l.Path); err != nil {
			return fmt.Errorf("symlinks[%d]: %w", i, err)
		}
		if l.Target == "" || strings.ContainsRune(l.Target, 0) {
			return fmt.Errorf("symlinks[%d] %s: target %q is empty or contains NUL", i, l.Path, l.Target)
		}
		if err := checkOwner(l.UID, l.GID); err != nil {
			return fmt.Errorf("symlinks[%d] %s: %w", i, l.Path, err)
		}
	}
	for i, d := range s.Directories {
		if err := checkImagePath(d.Path); err != nil {
			return fmt.Errorf("directories[%d]: %w", i, err)
		}
		if err := d.Mode.Validate(); err != nil {
			return fmt.Errorf("directories[%d] %s: %w", i, d.Path, err)
		}
		if err := checkOwner(d.UID, d.GID); err != nil {
			return fmt.Errorf("directories[%d] %s: %w", i, d.Path, err)
		}
	}
	return nil
}

func (t Tree) validate() error {
	if t.Source == "" {
		return fmt.Errorf("source is empty")
	}
	if t.Path != "/" {
		if err := checkImagePath(t.Path); err != nil {
			return err
		}
	}
	if err := t.ModePolicy.validate(); err != nil {
		return err
	}
	if err := t.Symlinks.validate(); err != nil {
		return err
	}
	for _, p := range slices.Concat(t.Include, t.Exclude) {
		if !doublestar.ValidatePattern(p) {
			return fmt.Errorf("pattern %q is not a valid doublestar glob", p)
		}
	}
	return checkOwner(t.UID, t.GID)
}

var portPattern = regexp.MustCompile(`^[0-9]{1,5}(/(tcp|udp|sctp))?$`)

func (c Config) validate() error {
	for k := range c.Env {
		if err := checkEnvKey(k); err != nil {
			return err
		}
	}
	for _, k := range c.UnsetEnv {
		if err := checkEnvKey(k); err != nil {
			return err
		}
	}
	if c.ExposedPorts != nil {
		for _, p := range *c.ExposedPorts {
			if !portPattern.MatchString(p) {
				return fmt.Errorf("config.exposedPorts: %q: want <port>[/tcp|/udp|/sctp]", p)
			}
		}
	}
	if c.Volumes != nil {
		for _, v := range *c.Volumes {
			if !path.IsAbs(v) {
				return fmt.Errorf("config.volumes: %q: want an absolute path", v)
			}
		}
	}
	return nil
}

// created parses Created; ok is false when it is empty.
func (s Spec) created() (t time.Time, ok bool, err error) {
	if s.Created == "" {
		return time.Time{}, false, nil
	}
	t, err = time.Parse(time.RFC3339, s.Created)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("created %q: want RFC 3339: %w", s.Created, err)
	}
	return t, true, nil
}

func checkImagePath(p string) error {
	switch {
	case p == "" || p[0] != '/':
		return fmt.Errorf("path %q: must be absolute", p)
	case p == "/":
		return fmt.Errorf("path %q: must not be the root", p)
	case strings.ContainsRune(p, 0):
		return fmt.Errorf("path %q: contains NUL", p)
	case path.Clean(p) != p:
		return fmt.Errorf("path %q: not clean, want %q", p, path.Clean(p))
	}
	return checkWhiteout(p)
}

// checkWhiteout refuses a basename starting with .wh.: image-spec layer.md makes such an
// entry a whiteout, which deletes the base's path of that name (or, as .wh..wh..opq,
// the whole directory), and magus places no whiteouts.
func checkWhiteout(p string) error {
	if strings.HasPrefix(path.Base(p), ".wh.") {
		return fmt.Errorf("path %q: a name starting with .wh. is an OCI whiteout, which would delete a base path", p)
	}
	return nil
}

func checkOwner(uid, gid int) error {
	if uid < 0 || gid < 0 {
		return fmt.Errorf("uid %d and gid %d must not be negative", uid, gid)
	}
	return nil
}

func checkEnvKey(k string) error {
	if k == "" || strings.Contains(k, "=") {
		return fmt.Errorf("config.env key %q: must be non-empty and contain no =", k)
	}
	return nil
}

// checkPlacements refuses a path placed twice and an entry below a file or symlink.
// It looks up each entry's ancestors in a set: comparing sorted neighbors misses
// "/app" and "/app/x" when "/app-b" sorts between them.
func checkPlacements(entries []entry) error {
	kinds := make(map[string]entryKind, len(entries))
	for _, e := range entries {
		if err := checkWhiteout(e.path); err != nil {
			return fmt.Errorf("image.assemble: %w", err)
		}
		if _, dup := kinds[e.path]; dup {
			return fmt.Errorf("image.assemble: %s is placed twice", e.path)
		}
		kinds[e.path] = e.kind
	}
	for _, e := range entries {
		for dir := path.Dir(e.path); dir != "/"; dir = path.Dir(dir) {
			if k, ok := kinds[dir]; ok && k != kindDirectory {
				return fmt.Errorf("image.assemble: %s is below %s, which is not a directory", e.path, dir)
			}
		}
	}
	return nil
}
