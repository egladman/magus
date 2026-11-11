package apiversion

import (
	"fmt"
	"slices"
	"strings"
)

// Kind names the type of record inside an API version, as `Job`.
type Kind string

// Header is the pair every record leads with. A reader that finds any
// other header than its own refuses the record.
type Header struct {
	APIVersion APIVersion `json:"apiVersion" yaml:"apiVersion" buzz:"apiVersion"`
	Kind       Kind       `json:"kind" yaml:"kind" buzz:"kind"`
}

// Versioned is a spec type that names its own header. Implement it on the
// value type: S is never a pointer type, because SpecHeader calls the
// methods on a zero S.
type Versioned interface {
	Kind() Kind
	APIVersion() APIVersion
}

// SpecHeader returns the header S stamps and accepts. S must not be a
// pointer type.
func SpecHeader[S Versioned]() Header {
	var spec S
	return Header{APIVersion: spec.APIVersion(), Kind: spec.Kind()}
}

// Feature names a capability a reader must implement to act on a record.
type Feature string

// Metadata holds the ObjectMeta members a server keeps when it prunes a
// record: anything else would be dropped.
type Metadata struct {
	Name string `json:"name,omitempty" yaml:"name,omitempty" buzz:"name"`
	// Generation counts revisions of the spec.
	Generation  int64             `json:"generation,omitempty" yaml:"generation,omitempty" buzz:"generation"`
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

// Object is a record: the header S names, metadata and the spec. It has no
// MarshalJSON; New stamps APIVersion and Kind.
type Object[S Versioned] struct {
	APIVersion APIVersion `json:"apiVersion" yaml:"apiVersion" buzz:"apiVersion"`
	Kind       Kind       `json:"kind" yaml:"kind" buzz:"kind"`
	Metadata   Metadata   `json:"metadata" yaml:"metadata" buzz:"metadata"`
	Spec       S          `json:"spec" yaml:"spec" buzz:"spec"`
}

// New returns spec as an Object with the header S names.
func New[S Versioned](metadata Metadata, spec S) Object[S] {
	header := SpecHeader[S]()
	return Object[S]{APIVersion: header.APIVersion, Kind: header.Kind, Metadata: metadata, Spec: spec}
}

// Unmarshal is the decoder signature of encoding/json and the yaml packages.
type Unmarshal func(data []byte, v any) error

// ReadHeader decodes only the header of data. unmarshal must ignore the
// members Header lacks, so a strict decoder does not belong here.
func ReadHeader(data []byte, unmarshal Unmarshal) (Header, error) {
	var header Header
	if err := unmarshal(data, &header); err != nil {
		return Header{}, fmt.Errorf("read header: %w", err)
	}
	return header, nil
}

// ReadOption adjusts Read.
type ReadOption func(*readOptions)

type readOptions struct {
	bodyUnmarshal Unmarshal
}

// WithBodyUnmarshal decodes the body with unmarshal, such as a strict
// decoder, while the header still goes through Read's own function.
func WithBodyUnmarshal(unmarshal Unmarshal) ReadOption {
	return func(o *readOptions) { o.bodyUnmarshal = unmarshal }
}

// Read decodes data as an Object[S]. It reads the header first with
// unmarshal, returns a *RefusedError for any header but SpecHeader[S](),
// then decodes the whole record.
func Read[S Versioned](data []byte, unmarshal Unmarshal, opts ...ReadOption) (Object[S], error) {
	var options readOptions
	for _, opt := range opts {
		opt(&options)
	}
	got, err := ReadHeader(data, unmarshal)
	if err != nil {
		return Object[S]{}, err
	}
	if want := SpecHeader[S](); got != want {
		return Object[S]{}, &RefusedError{Want: want, Got: got}
	}
	body := unmarshal
	if options.bodyUnmarshal != nil {
		body = options.bodyUnmarshal
	}
	var object Object[S]
	if err := body(data, &object); err != nil {
		return Object[S]{}, fmt.Errorf("read %s body: %w", got.Kind, err)
	}
	return object, nil
}

// RefusedError is a reader's refusal of a record whose header is not the
// one it reads. Callers wrap it with what the record was and what to do.
type RefusedError struct {
	Want, Got Header
}

func (e *RefusedError) Error() string {
	switch {
	case e.Got.APIVersion == "":
		return fmt.Sprintf("record carries no apiVersion, and this reader reads only %s", describe(e.Want))
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

func describe(h Header) string {
	if h.Kind == "" {
		return string(h.APIVersion)
	}
	return fmt.Sprintf("%s %s", h.APIVersion, h.Kind)
}
