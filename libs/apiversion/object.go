package apiversion

import (
	"fmt"
	"slices"
	"strings"
)

// Kind names the type of record inside an API version, as `Job`.
type Kind string

// TypeMeta is the pair every record leads with, as metav1.TypeMeta, but keyed
// `api_version` and `kind` in snake_case.
// A reader that finds any other pair than its own refuses the record.
// A record embeds it, so the pair stays top-level.
type TypeMeta struct {
	APIVersion APIVersion `json:"api_version" yaml:"api_version" buzz:"apiVersion"`
	Kind       Kind       `json:"kind" yaml:"kind" buzz:"kind"`
}

// Versioned is a spec type that names its own header. Implement it on the
// value type: S is never a pointer type, because SpecHeader calls the
// methods on a zero S.
type Versioned interface {
	Kind() Kind
	APIVersion() APIVersion
}

// SpecHeader returns the TypeMeta S stamps and accepts. S must not be a
// pointer type.
func SpecHeader[S Versioned]() TypeMeta {
	var spec S
	return TypeMeta{APIVersion: spec.APIVersion(), Kind: spec.Kind()}
}

// Feature names a capability a reader must implement to act on a record.
type Feature string

// Metadata holds the ObjectMeta members a server keeps when it prunes a
// record: anything else would be dropped.
type Metadata struct {
	Name string `json:"name,omitempty" yaml:"name,omitempty" buzz:"name"`
	// Generation counts revisions of the spec.
	Generation  int64             `json:"generation,omitzero" yaml:"generation,omitempty" buzz:"generation"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty" buzz:"annotations"`
}

// Requires returns the features the annotation "<group>/requires" lists,
// in record order, or nil when it is absent or empty. A reader lacking one
// treats the record as read-only.
func (m Metadata) Requires(group string) []Feature {
	var out []Feature
	for _, name := range strings.Split(m.Annotations[group+"/requires"], ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, Feature(name))
		}
	}
	return out
}

// WithRequires returns m with the annotation "<group>/requires" listing
// features, or without it when features is empty. m's own map is not
// changed.
func (m Metadata) WithRequires(group string, features []Feature) Metadata {
	key := group + "/requires"
	annotations := make(map[string]string, len(m.Annotations)+1)
	for k, v := range m.Annotations {
		if k != key {
			annotations[k] = v
		}
	}
	if len(features) > 0 {
		names := make([]string, len(features))
		for i, f := range features {
			names[i] = string(f)
		}
		annotations[key] = strings.Join(names, ",")
	}
	m.Annotations = annotations
	if len(annotations) == 0 {
		m.Annotations = nil
	}
	return m
}

// Unmet returns the entries of Requires(group) that known does not name,
// in record order, or nil when the reader can act on the record.
func (m Metadata) Unmet(group string, known []Feature) []Feature {
	var out []Feature
	for _, feature := range m.Requires(group) {
		if !slices.Contains(known, feature) {
			out = append(out, feature)
		}
	}
	return out
}

// Object is a record: the TypeMeta S names, metadata and the spec. It has no
// MarshalJSON; New stamps APIVersion and Kind.
type Object[S Versioned] struct {
	TypeMeta `yaml:",inline"`
	Metadata Metadata `json:"metadata" yaml:"metadata" buzz:"metadata"`
	Spec     S        `json:"spec" yaml:"spec" buzz:"spec"`
}

// New returns spec as an Object with the TypeMeta S names.
func New[S Versioned](metadata Metadata, spec S) Object[S] {
	return Object[S]{TypeMeta: SpecHeader[S](), Metadata: metadata, Spec: spec}
}

// RefusedError is a reader's refusal of a record whose header is not the
// one it reads. Callers wrap it with what the record was and what to do.
type RefusedError struct {
	Want, Got TypeMeta
}

func (e *RefusedError) Error() string {
	switch {
	case e.Got.APIVersion == "":
		return fmt.Sprintf("record carries no api_version, and this reader reads only %s", describe(e.Want))
	case e.newer():
		return fmt.Sprintf("record is %s, newer than the %s this reader reads", describe(e.Got), describe(e.Want))
	default:
		return fmt.Sprintf("record is %s, and this reader reads only %s", describe(e.Got), describe(e.Want))
	}
}

// Is reports ErrNewer for a newer version of the same group.
func (e *RefusedError) Is(target error) bool {
	return target == ErrNewer && e.newer()
}

func (e *RefusedError) newer() bool {
	return e.Got.APIVersion.Group() == e.Want.APIVersion.Group() && e.Got.APIVersion.Compare(e.Want.APIVersion) > 0
}

func describe(h TypeMeta) string {
	if h.Kind == "" {
		return string(h.APIVersion)
	}
	return fmt.Sprintf("%s %s", h.APIVersion, h.Kind)
}
