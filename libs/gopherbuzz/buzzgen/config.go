package buzzgen

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigVersion is the only .buzzgen.yaml schema version this package reads.
const ConfigVersion = "v1alpha1"

// Config is a parsed .buzzgen.yaml: which Go types and host modules
// cross into Buzz, and where each output is written. Settings layer: the
// top level, then a package's config, then a type's config, each
// overriding the one before.
type Config struct {
	Version string `yaml:"version"`
	// Settings at the top level are every package's defaults.
	Settings `yaml:",inline"`
	// Error is the Go type every host error crosses as, as package.Name.
	Error    string             `yaml:"error"`
	Modules  ModulesConfig      `yaml:"modules"`
	Packages map[string]Package `yaml:"packages"`
	Outputs  map[string]Output  `yaml:"outputs"`
}

// Settings are the keys any level may set. A nil field inherits.
type Settings struct {
	// Name is the Buzz name, when it differs from the Go name. Only a
	// type may set it.
	Name *string `yaml:"name,omitempty"`
	// Module is the import path of the Buzz module a mirror is declared in,
	// such as http or magus/agent. Empty bundles it where it is reached.
	Module *string `yaml:"module,omitempty"`
	// Open is why a named string type crosses as str, not as an enum.
	Open *string `yaml:"open,omitempty"`
	// Required names the fields a decoder refuses a value without.
	Required []string `yaml:"required,omitempty"`
	// TS also writes the type to every typescript output.
	TS *bool `yaml:"ts,omitempty"`
	// ZeroCase names an enum's case for the value "". An empty string
	// declares the enum without one.
	ZeroCase *string `yaml:"zeroCase,omitempty"`
	// Cases maps a value to the case name it takes, overriding the
	// derived name. Levels merge by key.
	Cases map[string]string `yaml:"cases,omitempty"`
	// CasesFrom lists the packages that declare the type's cases. Unset,
	// the type's own package is read.
	CasesFrom []string `yaml:"casesFrom,omitempty"`
	// PkgName is the Go package name a go or go-decode output declares.
	PkgName *string `yaml:"pkgname,omitempty"`
	// Mirror names the struct in the same package whose mirror this one
	// crosses as, for a type the host converts before it reaches Buzz.
	// Only a type may set it.
	Mirror *string `yaml:"mirror,omitempty"`
}

// DefaultZeroCase names the zero case when no level sets zeroCase.
const DefaultZeroCase = "none"

// over returns s with every field child sets replaced.
func (s Settings) over(child Settings) Settings {
	out := s
	if child.Name != nil {
		out.Name = child.Name
	}
	if child.Module != nil {
		out.Module = child.Module
	}
	if child.Open != nil {
		out.Open = child.Open
	}
	if child.TS != nil {
		out.TS = child.TS
	}
	if child.ZeroCase != nil {
		out.ZeroCase = child.ZeroCase
	}
	if child.PkgName != nil {
		out.PkgName = child.PkgName
	}
	if child.CasesFrom != nil {
		out.CasesFrom = child.CasesFrom
	}
	if child.Required != nil {
		out.Required = child.Required
	}
	if child.Mirror != nil {
		out.Mirror = child.Mirror
	}
	if child.Cases != nil {
		out.Cases = map[string]string{}
		maps.Copy(out.Cases, s.Cases)
		maps.Copy(out.Cases, child.Cases)
	}
	return out
}

// ModulesConfig locates the host module list.
type ModulesConfig struct {
	// Package and Variable name the []host.Module value buzzgen reads.
	Package  string `yaml:"package"`
	Variable string `yaml:"variable"`
	// Dynamic maps `<module>.<function>` to why that function may take or
	// return an untyped value. A blank reason is an error.
	Dynamic map[string]string `yaml:"dynamic"`
}

// Package lists the types one Go package contributes.
type Package struct {
	Config Settings              `yaml:"config"`
	Types  map[string]TypeConfig `yaml:"types"`
}

// TypeConfig is one mirrored Go type.
type TypeConfig struct {
	Config Settings `yaml:"config"`
}

// OutputKind is what an output holds.
type OutputKind string

// The output kinds a config may name.
const (
	OutputGo         OutputKind = "go"
	OutputGoDecode   OutputKind = "go-decode"
	OutputBuzzDecls  OutputKind = "buzz-decls"
	OutputBuzzTypes  OutputKind = "buzz-types"
	OutputTypeScript OutputKind = "typescript"
)

var outputKinds = []OutputKind{OutputGo, OutputGoDecode, OutputBuzzDecls, OutputBuzzTypes, OutputTypeScript}

// Output is one generated tree or file.
type Output struct {
	Kind OutputKind `yaml:"kind"`
	Path string     `yaml:"path"`
	// PkgName overrides the Go package name for this output alone.
	PkgName string `yaml:"pkgname"`
	// Packages, for a buzz-types output, are the packages whose listed
	// types it holds.
	Packages []string `yaml:"packages"`
}

// TypeRef names one listed type with its settings already layered.
type TypeRef struct {
	Package  string
	Name     string
	Settings Settings
}

// BuzzName is the name the type is declared under in Buzz.
func (r TypeRef) BuzzName() string {
	if r.Settings.Name != nil && *r.Settings.Name != "" {
		return *r.Settings.Name
	}
	return r.Name
}

// OpenReason is why the type crosses as str, or "" for an enum.
func (r TypeRef) OpenReason() string {
	if r.Settings.Open == nil {
		return ""
	}
	return *r.Settings.Open
}

// ZeroCase is the name of the enum's "" case, or "" for none.
func (r TypeRef) ZeroCase() string {
	if r.Settings.ZeroCase == nil {
		return DefaultZeroCase
	}
	return *r.Settings.ZeroCase
}

// ReadConfig reads and validates the config at path.
func ReadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("buzzgen: %w", err)
	}
	defer f.Close()
	cfg, err := ParseConfig(f)
	if err != nil {
		return Config{}, fmt.Errorf("buzzgen: %s: %w", path, err)
	}
	return cfg, nil
}

// ParseConfig decodes a config, refusing unknown keys, and validates it.
func ParseConfig(r io.Reader) (Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Config{}, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, cfg.Validate()
}

// Validate reports every problem in the config at once.
func (c Config) Validate() error {
	var errs []error
	if c.Version != ConfigVersion {
		errs = append(errs, fmt.Errorf("version is %q; want %q", c.Version, ConfigVersion))
	}
	if c.Settings.Name != nil {
		errs = append(errs, errors.New("name is set at the top level; only a type may set it"))
	}
	if c.Settings.Mirror != nil {
		errs = append(errs, errors.New("mirror is set at the top level; only a type may set it"))
	}
	for _, pkg := range sortedKeys(c.Packages) {
		if c.Packages[pkg].Config.Name != nil {
			errs = append(errs, fmt.Errorf("packages.%s.config.name is set; only a type may set it", pkg))
		}
		if c.Packages[pkg].Config.Mirror != nil {
			errs = append(errs, fmt.Errorf("packages.%s.config.mirror is set; only a type may set it", pkg))
		}
	}
	for _, name := range sortedKeys(c.Modules.Dynamic) {
		if strings.TrimSpace(c.Modules.Dynamic[name]) == "" {
			errs = append(errs, fmt.Errorf("modules.dynamic.%s has no reason", name))
		}
	}
	for _, name := range sortedKeys(c.Outputs) {
		errs = append(errs, c.Outputs[name].validate(name, c.Packages)...)
	}
	return errors.Join(errs...)
}

func (o Output) validate(name string, packages map[string]Package) []error {
	var errs []error
	if !slices.Contains(outputKinds, o.Kind) {
		errs = append(errs, fmt.Errorf("outputs.%s.kind is %q; want one of %v", name, o.Kind, outputKinds))
	}
	if o.Path == "" {
		errs = append(errs, fmt.Errorf("outputs.%s has no path", name))
	}
	if len(o.Packages) > 0 && o.Kind != OutputBuzzTypes {
		errs = append(errs, fmt.Errorf("outputs.%s sets packages, which only a %s output reads", name, OutputBuzzTypes))
	}
	if o.PkgName != "" && o.Kind != OutputGo && o.Kind != OutputGoDecode {
		errs = append(errs, fmt.Errorf("outputs.%s sets pkgname, which only a Go output declares", name))
	}
	for _, pkg := range o.Packages {
		if _, ok := packages[pkg]; !ok {
			errs = append(errs, fmt.Errorf("outputs.%s names package %s, which packages does not list", name, pkg))
		}
	}
	return errs
}

// Types lists every configured type with its settings layered, ordered by
// package path then Go name, so every output that walks them is stable.
func (c Config) Types() []TypeRef {
	var refs []TypeRef
	for _, path := range sortedKeys(c.Packages) {
		pkg := c.Packages[path]
		base := c.packageSettings(path)
		for _, name := range sortedKeys(pkg.Types) {
			refs = append(refs, TypeRef{Package: path, Name: name, Settings: base.over(pkg.Types[name].Config)})
		}
	}
	return refs
}

// Ref is the type pkg.name with its settings layered. A type the config
// does not list takes its package's and the top level's settings.
func (c Config) Ref(pkg, name string) TypeRef {
	return TypeRef{Package: pkg, Name: name, Settings: c.packageSettings(pkg).over(c.Packages[pkg].Types[name].Config)}
}

// packageSettings layers the top level under the package's config. The top
// level's module is the root a package's namespace derives from, never a
// default, so it is dropped.
func (c Config) packageSettings(path string) Settings {
	top := c.Settings
	top.Module = nil
	out := top.over(c.Packages[path].Config)
	if out.Module == nil {
		if derived, ok := c.DerivedModule(path); ok {
			out.Module = &derived
		}
	}
	return out
}

// DerivedModule is the module of a package whose config sets none: the
// top-level module, a slash and the package's last path element. Only a
// package whose parent package the config lists derives one.
func (c Config) DerivedModule(path string) (string, bool) {
	if c.Settings.Module == nil || *c.Settings.Module == "" {
		return "", false
	}
	parent, name, ok := cutLast(path, "/")
	if _, listed := c.Packages[parent]; !ok || !listed {
		return "", false
	}
	return *c.Settings.Module + "/" + name, true
}

func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return "", s, false
	}
	return s[:i], s[i+len(sep):], true
}

// Namespace is the Buzz namespace the type is declared in, module with each
// slash a backslash, or "" when it is bundled where it is reached.
func (r TypeRef) Namespace() string {
	if r.Settings.Module == nil {
		return ""
	}
	return strings.ReplaceAll(*r.Settings.Module, "/", `\`)
}

// MirrorName is the struct whose mirror the type crosses as, or "".
func (r TypeRef) MirrorName() string {
	if r.Settings.Mirror == nil {
		return ""
	}
	return *r.Settings.Mirror
}

// PkgName is the Go package name the named output declares: its own
// pkgname, else the top level's, else "" for the generator's default.
func (c Config) PkgName(output string) string {
	if out := c.Outputs[output]; out.PkgName != "" {
		return out.PkgName
	}
	if c.Settings.PkgName != nil {
		return *c.Settings.PkgName
	}
	return ""
}

// PackagePaths lists every package the config names, and the extra ones
// given, sorted.
func (c Config) PackagePaths(extra ...string) []string {
	paths := sortedKeys(c.Packages)
	extra = append(slices.Clone(extra), c.Modules.Package)
	for _, ref := range c.Types() {
		extra = append(extra, ref.Settings.CasesFrom...)
	}
	for _, path := range extra {
		if path != "" && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
