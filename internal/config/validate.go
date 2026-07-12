package config

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/proc/endpoint"
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
		_, err := endpoint.ParseEndpoint(s)
		return err == nil
	})

	// mcp_address must be a valid host:port parseable by netip.ParseAddrPort.
	// unix:// URLs, bare paths, and http(s):// URLs are all rejected.
	_ = v.RegisterValidation("mcp_address", func(fl validator.FieldLevel) bool {
		_, err := netip.ParseAddrPort(fl.Field().String())
		return err == nil
	})

	return v
}

// Validate checks cfg against its validate struct tags; returns *ValidationError on failure.
func Validate(cfg Config) error {
	if err := validate.Struct(cfg); err != nil {
		var fe validator.ValidationErrors
		if errors.As(err, &fe) {
			return formatErrors(fe)
		}
		return fmt.Errorf("validate: %w", err)
	}
	return nil
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
	return f.Field + ": " + humanReason(f)
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
		return fmt.Sprintf("must be ≥ %s (got %s)", f.Param, f.Value)
	case "lte":
		return fmt.Sprintf("must be ≤ %s (got %s)", f.Param, f.Value)
	case "shard_count":
		return fmt.Sprintf("must be -1 or in [1, 256] (got %s)", f.Value)
	case "magus_endpoint":
		return fmt.Sprintf("must be a unix:// URL (got %q)", f.Value)
	case "mcp_address":
		return fmt.Sprintf("must be a valid host:port, e.g. 127.0.0.1:7391 (got %q)", f.Value)
	default:
		if f.Param != "" {
			return fmt.Sprintf("failed %s=%s (got %q)", f.Tag, f.Param, f.Value)
		}
		return fmt.Sprintf("failed %s (got %q)", f.Tag, f.Value)
	}
}

func formatErrors(fe validator.ValidationErrors) error {
	out := &ValidationError{Failures: make([]FieldFailure, 0, len(fe))}
	for _, e := range fe {
		out.Failures = append(out.Failures, FieldFailure{
			Field: yamlNamespace(e.Namespace()),
			Tag:   e.Tag(),
			Param: e.Param(),
			Value: fmt.Sprintf("%v", e.Value()),
		})
	}
	slices.SortFunc(out.Failures, func(a, b FieldFailure) int {
		return cmp.Compare(a.Field, b.Field)
	})
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
