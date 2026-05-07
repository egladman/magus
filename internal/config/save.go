package config

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/internal/file"
)

// nameSegment is the literal placeholder used in dotted keys for a
// slice-of-struct entry's identifier.
// Users substitute the actual name when calling `magus config set`.
const nameSegment = "<name>"

// durationType identifies time.Duration fields so they parse as "6h"/"30m".
var durationType = reflect.TypeOf(time.Duration(0))

type fieldSchema struct {
	dotted      string // e.g. "cache.dir" or "ci.max_shards"
	typeName    string // "string", "int", "bool", "[]string"
	sliceParent string // when set, dotted is a slice-of-struct leaf rooted here
}

// schemaFields is lazily constructed via sync.OnceValue; only config-set callers pay the reflect cost.
var schemaFields = sync.OnceValue(func() []fieldSchema {
	return collectSchema(reflect.TypeOf(Config{}), nil, "")
})

func collectSchema(t reflect.Type, prefix []string, sliceParent string) []fieldSchema {
	var result []fieldSchema
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		path := append(append([]string{}, prefix...), name)
		switch f.Type.Kind() {
		case reflect.Struct:
			result = append(result, collectSchema(f.Type, path, sliceParent)...)
		case reflect.String:
			result = append(result, fieldSchema{dotted: strings.Join(path, "."), typeName: "string", sliceParent: sliceParent})
		case reflect.Int:
			result = append(result, fieldSchema{dotted: strings.Join(path, "."), typeName: "int", sliceParent: sliceParent})
		case reflect.Bool:
			result = append(result, fieldSchema{dotted: strings.Join(path, "."), typeName: "bool", sliceParent: sliceParent})
		case reflect.Float64:
			result = append(result, fieldSchema{dotted: strings.Join(path, "."), typeName: "float64", sliceParent: sliceParent})
		case reflect.Int64:
			tn := "int64"
			if f.Type == durationType {
				tn = "duration"
			}
			result = append(result, fieldSchema{dotted: strings.Join(path, "."), typeName: tn, sliceParent: sliceParent})
		case reflect.Ptr:
			// Tri-state pointer scalars (nil = inherit default). Only *bool occurs today.
			if f.Type.Elem().Kind() == reflect.Bool {
				result = append(result, fieldSchema{dotted: strings.Join(path, "."), typeName: "*bool", sliceParent: sliceParent})
			}
		case reflect.Map:
			if f.Type.Key().Kind() == reflect.String && f.Type.Elem().Kind() == reflect.String {
				result = append(result, fieldSchema{dotted: strings.Join(path, "."), typeName: "map[string]string", sliceParent: sliceParent})
			}
		case reflect.Slice:
			elem := f.Type.Elem()
			switch elem.Kind() {
			case reflect.String:
				result = append(result, fieldSchema{dotted: strings.Join(path, "."), typeName: "[]string", sliceParent: sliceParent})
			case reflect.Struct:
				// Only emit templated keys when the element has a
				// `name` field; that's the lookup key the
				// name-addressed setSliceEntry uses. Slices keyed
				// by other fields (like watch.ignore, where each
				// entry is just {type, pattern}) are excluded from
				// `magus config set` and must be hand-edited.
				if !hasNameField(elem) {
					continue
				}
				slicePath := strings.Join(path, ".")
				childPrefix := append(append([]string{}, path...), nameSegment)
				result = append(result, collectSchema(elem, childPrefix, slicePath)...)
			}
		}
	}
	return result
}

// hasNameField reports whether t is a struct with a yaml-tagged
// "name" field. Used to gate slice-of-struct schema templating: only
// slices whose elements are name-addressable can be set via the
// dotted CLI syntax. Other slices must be hand-edited.
func hasNameField(t reflect.Type) bool {
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		name := strings.Split(tag, ",")[0]
		if name == "name" {
			return true
		}
	}
	return false
}

// KnownKeys returns all recognized dotted config keys in sorted order.
func KnownKeys() []string {
	fields := schemaFields()
	keys := make([]string, len(fields))
	for i, s := range fields {
		keys[i] = s.dotted
	}
	slices.Sort(keys)
	return keys
}

// Save updates key to value in the YAML file at path and writes it back.
// The file and its parent directory are created if they do not exist.
//
// For slice-of-struct fields, key uses name-addressed syntax
// (e.g. "<parent>.<entry-name>.<leaf>"). The entry is found by matching
// its name field; if no matching entry exists, a new one is appended.
func Save(path, key, value string) error {
	fs, entryName, ok := matchSchema(key)
	if !ok {
		return fmt.Errorf("unknown config key %q (run `magus config view -o name` for valid keys)", key)
	}

	typedVal, err := parseValue(fs.typeName, value)
	if err != nil {
		return fmt.Errorf("key %s: %w", key, err)
	}

	m := map[string]interface{}{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}

	if fs.sliceParent != "" {
		if err := setSliceEntry(m, fs.sliceParent, entryName, fs.dotted, typedVal); err != nil {
			return fmt.Errorf("key %s: %w", key, err)
		}
	} else {
		setScalar(m, key, typedVal)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("marshal close: %w", err)
	}

	// Validate the merged result against the schema before writing,
	// merging defaults so a partial file still passes (e.g. cache.dir
	// inherits the default when the user only set ci.max_shards). Refuse
	// to clobber a working file with an invalid value.
	if err := validateAfterMerge(buf.Bytes()); err != nil {
		return err
	}

	if err := file.WriteFileAtomic(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// validateAfterMerge unmarshals data on top of Defaults() and runs
// the schema validator against the result. Used by Save (and Init)
// to guarantee that whatever lands on disk loads cleanly.
func validateAfterMerge(data []byte) error {
	cfg := Defaults()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("validate: re-parse: %w", err)
	}
	return Validate(cfg)
}

// matchSchema finds the fieldSchema for key. Returns the matched
// schema, the dynamic entry name (when key targets a slice-of-struct
// leaf), and true. For scalar keys, entryName is empty.
func matchSchema(key string) (fieldSchema, string, bool) {
	for _, s := range schemaFields() {
		if s.sliceParent == "" && s.dotted == key {
			return s, "", true
		}
		if s.sliceParent != "" {
			if name, ok := matchSliceKey(key, s.dotted); ok {
				return s, name, true
			}
		}
	}
	return fieldSchema{}, "", false
}

// matchSliceKey checks whether userKey conforms to the template path
// (which contains nameSegment as a placeholder) and returns the
// substituted segment if so.
func matchSliceKey(userKey, template string) (string, bool) {
	tmplParts := strings.Split(template, ".")
	userParts := strings.Split(userKey, ".")
	if len(tmplParts) != len(userParts) {
		return "", false
	}
	var name string
	for i, t := range tmplParts {
		if t == nameSegment {
			if userParts[i] == "" {
				return "", false
			}
			name = userParts[i]
			continue
		}
		if t != userParts[i] {
			return "", false
		}
	}
	return name, true
}

func setScalar(m map[string]interface{}, key string, value interface{}) {
	parts := strings.Split(key, ".")
	cur := m
	for _, seg := range parts[:len(parts)-1] {
		switch sub := cur[seg].(type) {
		case map[string]interface{}:
			cur = sub
		default:
			next := map[string]interface{}{}
			cur[seg] = next
			cur = next
		}
	}
	cur[parts[len(parts)-1]] = value
}

// setSliceEntry mutates m so that the entry named entryName under the
// dotted slice path slicePath has its leaf field (the final segment of
// templatePath) set to value. The matching entry is found by its
// "name" field; a new entry is appended when no match exists.
func setSliceEntry(m map[string]interface{}, slicePath, entryName, templatePath string, value interface{}) error {
	leafField := templatePath[strings.LastIndex(templatePath, ".")+1:]

	parts := strings.Split(slicePath, ".")
	cur := m
	for _, seg := range parts[:len(parts)-1] {
		switch sub := cur[seg].(type) {
		case map[string]interface{}:
			cur = sub
		default:
			next := map[string]interface{}{}
			cur[seg] = next
			cur = next
		}
	}
	last := parts[len(parts)-1]

	var entries []interface{}
	switch existing := cur[last].(type) {
	case []interface{}:
		entries = existing
	case nil:
		// no slice yet
	default:
		return fmt.Errorf("%s is not a sequence in %T", slicePath, existing)
	}

	for i, raw := range entries {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		if name == entryName {
			entry[leafField] = value
			entries[i] = entry
			cur[last] = entries
			return nil
		}
	}

	newEntry := map[string]interface{}{"name": entryName}
	if leafField != "name" {
		newEntry[leafField] = value
	}
	entries = append(entries, newEntry)
	cur[last] = entries
	return nil
}

// InitDefaults returns a Config seeded with every built-in value
// magus knows about, suitable for serialising to a fresh magus.yaml.
func InitDefaults() Config {
	return Defaults()
}

// Init writes a magus.yaml containing every built-in default to path.
// It refuses to overwrite an existing file unless force is true. The
// parent directory is created if missing.
func Init(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite)", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}

	cfg := InitDefaults()
	if err := Validate(cfg); err != nil {
		return fmt.Errorf("init: built-in defaults failed validation: %w", err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("marshal close: %w", err)
	}

	return file.WriteFileAtomic(path, buf.Bytes(), 0o644)
}

// parseBool parses a permissive boolean (true/1/yes, false/0/no).
func parseBool(value string) (bool, error) {
	switch strings.ToLower(value) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean %q (use true/false/1/0)", value)
}

// parseValue converts the string value into the Go type for the named schema type.
func parseValue(typeName, value string) (interface{}, error) {
	switch typeName {
	case "string":
		return value, nil
	case "int":
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("invalid integer %q", value)
		}
		return n, nil
	case "int64":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid integer %q", value)
		}
		return n, nil
	case "float64":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", value)
		}
		return f, nil
	case "duration":
		d, err := time.ParseDuration(value)
		if err != nil {
			return nil, fmt.Errorf("invalid duration %q (e.g. \"6h\", \"30m\")", value)
		}
		// yaml.v3 round-trips time.Duration via its String() form ("6h0s").
		return d, nil
	case "bool":
		return parseBool(value)
	case "*bool":
		b, err := parseBool(value)
		if err != nil {
			return nil, err
		}
		return &b, nil
	case "[]string":
		var parts []string
		for _, p := range strings.Split(value, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		return parts, nil
	case "map[string]string":
		m := map[string]string{}
		if err := yaml.Unmarshal([]byte(value), &m); err != nil {
			return nil, fmt.Errorf("invalid map %q (YAML object, e.g. \"{X: Y}\"): %w", value, err)
		}
		return m, nil
	}
	return nil, fmt.Errorf("unsupported type %q", typeName)
}
