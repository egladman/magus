package config

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"net/url"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/oci"
	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/go-playground/validator/v10"
)

// validate is the package-wide validator instance; reused because validator caches reflection metadata.
var validate = newValidator()

func newValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	// Use yaml tag names in error messages so paths match the user's
	// config file (cache.dir, not Cache.Dir).
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		tag := fld.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			return ""
		}
		name := strings.Split(tag, ",")[0]
		return name
	})

	// shard_count: -1 (unlimited) or [1, 256].
	_ = v.RegisterValidation("shard_count", func(fl validator.FieldLevel) bool {
		n := fl.Field().Int()
		return n == -1 || (n >= 1 && n <= 256)
	})

	// The magus_endpoint field must be a unix:// URL; bare paths are rejected here (the runtime parser is more lenient for back-compat).
	_ = v.RegisterValidation("magus_endpoint", func(fl validator.FieldLevel) bool {
		s := fl.Field().String()
		if !strings.HasPrefix(s, "unix://") {
			return false
		}
		_, err := endpoint.Parse(s)
		return err == nil
	})

	// mcp_address must be a valid host:port parseable by netip.ParseAddrPort.
	// unix:// URLs, bare paths, and http(s):// URLs are all rejected.
	_ = v.RegisterValidation("mcp_address", func(fl validator.FieldLevel) bool {
		_, err := netip.ParseAddrPort(fl.Field().String())
		return err == nil
	})

	// registry_host is a bare, lowercase host[:port], the form an OCI reference names
	// and the only form a lookup matches. A scheme, path or userinfo would match no
	// reference, so the credential would silently never be sent.
	_ = v.RegisterValidation("registry_host", func(fl validator.FieldLevel) bool {
		s := fl.Field().String()
		if s != strings.ToLower(s) || strings.ContainsAny(s, "/@?#* \t\r\n") {
			return false
		}
		u, err := url.Parse("//" + s)
		return err == nil && u.Host == s
	})

	return v
}

// Validate checks cfg against its validate struct tags and the spell import
// declarations, which no tag can express; returns *ValidationError on failure.
func Validate(cfg Config) error {
	var failures []FieldFailure
	if err := validate.Struct(cfg); err != nil {
		var fe validator.ValidationErrors
		if !errors.As(err, &fe) {
			return fmt.Errorf("validate: %w", err)
		}
		failures = fieldFailures(fe)
	}
	failures = append(failures, spellImportFailures(cfg.Spells.Imports)...)
	failures = append(failures, cacheWriteFailures(cfg.Cache)...)
	failures = append(failures, mcpHTTPFailures(cfg.MCP)...)
	failures = append(failures, conformanceFailures(cfg.Knowledge.Conformance)...)
	if !cfg.Broker.Valid() {
		failures = append(failures, FieldFailure{Field: "broker", Tag: "oneof",
			Param: strings.Join(cfg.Broker.Values(), " "), Value: string(cfg.Broker)})
	}
	if len(failures) == 0 {
		return nil
	}
	slices.SortFunc(failures, func(a, b FieldFailure) int {
		return cmp.Compare(a.Field, b.Field)
	})
	return &ValidationError{Failures: failures}
}

// spellsReservedKeys are the fields that share the spells mapping with the inline
// import declarations. A key that is neither a spell path nor one of these is most
// likely a misspelling of one.
var spellsReservedKeys = []string{"allow_shadow", "registries"}

// spellImportFailures checks each spells.<path> entry. A remote path names exactly one
// of a tag or a workspace path; an embedded path can only be replaced, since magus
// ships it and there is no tag to track. Two remote paths may not nest, because each
// materializes as a directory named by its path and one would sit inside the other.
func spellImportFailures(imports map[string]SpellImport) []FieldFailure {
	var out []FieldFailure
	keys := slices.Sorted(maps.Keys(imports))
	var remote []string
	for _, key := range keys {
		imp := imports[key]
		field := "spells." + key
		switch {
		case spells.IsEmbeddedImport(key):
			if imp.Tag != "" {
				out = append(out, FieldFailure{Field: field + ".tag", Tag: "spell_embedded_tag", Value: imp.Tag})
			}
			if imp.Path == "" {
				out = append(out, FieldFailure{Field: field + ".path", Tag: "required"})
			}
		case spells.IsRemoteImport(key):
			if _, err := oci.ParseRepository(key); err != nil || key != strings.ToLower(key) {
				out = append(out, FieldFailure{Field: field, Tag: "spell_import_path", Value: key})
				continue
			}
			if (imp.Tag == "") == (imp.Path == "") {
				out = append(out, FieldFailure{Field: field, Tag: "spell_tag_or_path"})
				continue
			}
			if imp.Tag != "" {
				if err := oci.ValidateTag(imp.Tag); err != nil {
					out = append(out, FieldFailure{Field: field + ".tag", Tag: "spell_tag", Value: imp.Tag})
				}
				remote = append(remote, key)
			}
		default:
			out = append(out, FieldFailure{Field: field, Tag: "spell_import_path", Param: hint.Nearest(key, spellsReservedKeys), Value: key})
			continue
		}
		if imp.Path != "" && !filepath.IsLocal(filepath.FromSlash(imp.Path)) {
			out = append(out, FieldFailure{Field: field + ".path", Tag: "spell_override_path", Value: imp.Path})
		}
	}
	// remote is sorted, so a path nesting inside another follows it directly or after
	// siblings that share its prefix.
	for i, outer := range remote {
		for _, inner := range remote[i+1:] {
			if strings.HasPrefix(inner, outer+"/") {
				out = append(out, FieldFailure{Field: "spells." + inner, Tag: "spell_path_nested", Param: outer, Value: inner})
			}
		}
	}
	return out
}

// cacheWriteFailures refuses writing the remote tier with the local tier off: a
// remote-tier entry is exported from the local-tier one, so it could never take effect.
func cacheWriteFailures(c Cache) []FieldFailure {
	if c.Remote.Write.Enabled == nil || !*c.Remote.Write.Enabled || c.WriteEnabled() {
		return nil
	}
	return []FieldFailure{{Field: "cache.remote.write.enabled", Tag: "cache_write_required", Value: "true"}}
}

// mcpHTTPFailures refuses serving MCP over HTTP with MCP off: mcp.enabled false starts no
// MCP transport the server owns, so the explicit true could never take effect.
func mcpHTTPFailures(m MCP) []FieldFailure {
	if m.HTTP == nil || !*m.HTTP || m.Enabled == nil || *m.Enabled {
		return nil
	}
	return []FieldFailure{{Field: "mcp.http", Tag: "mcp_enabled_required", Value: "true"}}
}

// conformanceFailures checks what no tag can: the ratchet path, the exclusions, and the
// metric, population and scope keys of knowledge.conformance.checks. An unknown key is named,
// since a check that is never read would pass without anyone noticing.
func conformanceFailures(c ConformanceConfig) []FieldFailure {
	const root = "knowledge.conformance"
	var out []FieldFailure
	if c.Ratchet != "" && !filepath.IsLocal(filepath.FromSlash(c.Ratchet)) {
		out = append(out, FieldFailure{Field: root + ".ratchet", Tag: "conformance_ratchet_path", Value: c.Ratchet})
	}
	for i, pattern := range c.Exclude {
		field := fmt.Sprintf("%s.exclude[%d]", root, i)
		switch {
		case pattern == "":
			out = append(out, FieldFailure{Field: field, Tag: "required"})
		case !doublestar.ValidatePattern(pattern):
			out = append(out, FieldFailure{Field: field, Tag: "conformance_exclude_glob", Value: pattern})
		}
	}
	for _, metric := range slices.Sorted(maps.Keys(c.Checks)) {
		field := fmt.Sprintf("%s.checks.%s", root, string(metric))
		if !metric.Valid() {
			out = append(out, FieldFailure{Field: field, Tag: "conformance_metric", Param: strings.Join(metric.Values(), " "), Value: string(metric)})
			continue
		}
		pops := c.Checks[metric]
		for _, pop := range slices.Sorted(maps.Keys(pops)) {
			field := fmt.Sprintf("%s.%s", field, string(pop))
			if !pop.Valid() {
				out = append(out, FieldFailure{Field: field, Tag: "conformance_population", Param: strings.Join(pop.Values(), " "), Value: string(pop)})
				continue
			}
			out = append(out, conformanceLimitFailures(field, metric, pops[pop])...)
		}
	}
	return out
}

func conformanceLimitFailures(field string, metric types.ConformanceMetric, l ConformanceLimitConfig) []FieldFailure {
	var out []FieldFailure
	// The list metrics judge a norm of none, not a distribution: anchor 0 names every item
	// holding any of what it measures, and percentile 0 measures no proposal.
	list := slices.Contains([]types.ConformanceMetric{types.ConformanceModeKnobs, types.ConformanceExportScope,
		types.ConformanceMainLogic, types.ConformanceStorePathExport}, metric)
	// file-split is a floor, which a percentile cannot propose.
	unproposed := list || metric == types.ConformanceFileSplit
	switch {
	case unproposed && l.Percentile < 0:
		out = append(out, FieldFailure{Field: field + ".percentile", Tag: "gte", Param: "0", Value: fmt.Sprint(l.Percentile)})
	case !unproposed && l.Percentile <= 0:
		out = append(out, FieldFailure{Field: field + ".percentile", Tag: "gt", Param: "0", Value: fmt.Sprint(l.Percentile)})
	}
	if l.Percentile > 100 {
		out = append(out, FieldFailure{Field: field + ".percentile", Tag: "lte", Param: "100", Value: fmt.Sprint(l.Percentile)})
	}
	switch {
	case list && l.Anchor < 0:
		out = append(out, FieldFailure{Field: field + ".anchor", Tag: "gte", Param: "0", Value: fmt.Sprint(l.Anchor)})
	case !list && l.Anchor < 1:
		out = append(out, FieldFailure{Field: field + ".anchor", Tag: "gte", Param: "1", Value: fmt.Sprint(l.Anchor)})
	}
	for _, scope := range slices.Sorted(maps.Keys(l.Scopes)) {
		scoped := fmt.Sprintf("%s.scopes.%s", field, scope)
		switch {
		case metric != types.ConformanceDepFanout:
			out = append(out, FieldFailure{Field: scoped, Tag: "conformance_scope_metric", Param: string(types.ConformanceDepFanout), Value: scope})
		case !filepath.IsLocal(filepath.FromSlash(scope)) || path.Clean(scope) != scope:
			out = append(out, FieldFailure{Field: scoped, Tag: "conformance_scope_path", Value: scope})
		}
		if l.Scopes[scope] < 1 {
			out = append(out, FieldFailure{Field: scoped, Tag: "gte", Param: "1", Value: fmt.Sprint(l.Scopes[scope])})
		}
	}
	return out
}

// ValidationError is the structured error type returned by Validate
// when one or more fields fail their validate tags.
type ValidationError struct {
	Failures []FieldFailure
}

// FieldFailure describes one validation failure.
type FieldFailure struct {
	Field string // dotted yaml path, e.g. "cache.dir"
	Tag   string // validate tag that rejected the value, e.g. "oneof"
	Param string // tag parameter, if any
	Value string // offending value as string
}

// String returns a human-readable description of the failure.
func (f FieldFailure) String() string {
	return fmt.Sprintf("%s: %s", f.Field, humanReason(f))
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	b.WriteString("invalid config:\n")
	for _, f := range e.Failures {
		b.WriteString("  ")
		b.WriteString(f.Field)
		b.WriteString(": ")
		b.WriteString(humanReason(f))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func humanReason(f FieldFailure) string {
	switch f.Tag {
	case "required":
		return "required (got empty)"
	case "oneof":
		return fmt.Sprintf("must be one of: %s (got %q)", strings.Join(strings.Fields(f.Param), ", "), f.Value)
	case "gte":
		return fmt.Sprintf("must be >= %s (got %s)", f.Param, f.Value)
	case "lte":
		return fmt.Sprintf("must be <= %s (got %s)", f.Param, f.Value)
	case "gt":
		return fmt.Sprintf("must be > %s (got %s)", f.Param, f.Value)
	case "lt":
		return fmt.Sprintf("must be < %s (got %s)", f.Param, f.Value)
	case "gtfield":
		// The param is the Go field name; the reader knows the yaml key.
		var key strings.Builder
		for i, r := range f.Param {
			if 'A' <= r && r <= 'Z' {
				if i > 0 {
					key.WriteByte('_')
				}
				r += 'a' - 'A'
			}
			key.WriteRune(r)
		}
		return fmt.Sprintf("must be greater than %s beside it (got %s)", key.String(), f.Value)
	case "conformance_ratchet_path":
		return fmt.Sprintf("must be a file inside the workspace, relative to magus.yaml (got %q)", f.Value)
	case "conformance_exclude_glob":
		return fmt.Sprintf("is not a valid glob, so it would exclude nothing (got %q)", f.Value)
	case "conformance_metric":
		return fmt.Sprintf("is not a metric magus measures; want one of: %s (got %q)", strings.Join(strings.Fields(f.Param), ", "), f.Value)
	case "conformance_population":
		return fmt.Sprintf("is not a population magus measures; want one of: %s (got %q)", strings.Join(strings.Fields(f.Param), ", "), f.Value)
	case "conformance_scope_metric":
		return fmt.Sprintf("a scope applies to %s only (got %q)", f.Param, f.Value)
	case "conformance_scope_path":
		return fmt.Sprintf("must be a clean directory inside the workspace, relative to magus.yaml (got %q)", f.Value)
	case "shard_count":
		return fmt.Sprintf("must be -1 or in [1, 256] (got %s)", f.Value)
	case "magus_endpoint":
		return fmt.Sprintf("must be a unix:// URL (got %q)", f.Value)
	case "mcp_address":
		return fmt.Sprintf("must be a valid host:port, e.g. 127.0.0.1:7391 (got %q)", f.Value)
	case "registry_host":
		return fmt.Sprintf("must be a bare lowercase registry host[:port], e.g. ghcr.io (got %q)", f.Value)
	case "unique":
		return fmt.Sprintf("each entry needs a distinct %s", strings.ToLower(f.Param))
	case "spell_import_path":
		msg := fmt.Sprintf("is not a spell import path: a remote spell is a lowercase registry path (ghcr.io/team/spells/lint), an embedded one starts with magus/spell/ (got %q)", f.Value)
		if f.Param != "" {
			msg += fmt.Sprintf("; did you mean %q?", f.Param)
		}
		return msg
	case "spell_tag_or_path":
		return "declare exactly one of tag (the remote spell magus.lock pins) or path (a workspace directory that replaces it)"
	case "spell_embedded_tag":
		return fmt.Sprintf("an embedded spell ships inside magus and has no tag; declare path to replace it with a workspace copy (got %q)", f.Value)
	case "spell_tag":
		return fmt.Sprintf("must be a registry tag, [A-Za-z0-9_][A-Za-z0-9._-]{0,127} (got %q)", f.Value)
	case "spell_override_path":
		return fmt.Sprintf("must be a directory inside the workspace, relative to magus.yaml (got %q)", f.Value)
	case "cache_write_required":
		return "cannot be true while cache.write.enabled is false: the remote cache tier is written from the local tier, so there would be nothing to write; enable cache.write.enabled or set this false"
	case "mcp_enabled_required":
		return "cannot be true while mcp.enabled is false: the server serves no MCP at all then, so there is no HTTP endpoint to mount; enable mcp.enabled or drop this key"
	case "spell_path_nested":
		return fmt.Sprintf("nests inside spells.%s; each remote spell is laid out as a directory named by its path, so one cannot hold another", f.Param)
	default:
		if f.Param != "" {
			return fmt.Sprintf("failed %s=%s (got %q)", f.Tag, f.Param, f.Value)
		}
		return fmt.Sprintf("failed %s (got %q)", f.Tag, f.Value)
	}
}

func fieldFailures(fe validator.ValidationErrors) []FieldFailure {
	out := make([]FieldFailure, 0, len(fe))
	for _, e := range fe {
		out = append(out, FieldFailure{
			Field: yamlNamespace(e.Namespace()),
			Tag:   e.Tag(),
			Param: e.Param(),
			Value: fmt.Sprintf("%v", e.Value()),
		})
	}
	return out
}

// yamlNamespace strips the top-level struct name from a validator namespace
// ("Config.Cache.Mode" → "Cache.Mode"). Slice indices like "[N]" are kept verbatim.
func yamlNamespace(ns string) string {
	parts := strings.Split(ns, ".")
	if len(parts) == 0 {
		return ns
	}
	parts = parts[1:] // drop top-level struct name
	return strings.Join(parts, ".")
}
