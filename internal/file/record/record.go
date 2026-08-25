// Package record stores a small struct as a DIRECTORY with one file per field,
// each holding a bare value:
//
//	locks/a6d4b12e/lock.owner/pid       -> "41221"
//	locks/a6d4b12e/lock.owner/command   -> "magus run ci ."
//	locks/a6d4b12e/lock.owner/started   -> "2026-08-24T09:53:02-04:00"
//
// The directory tree IS the structure, the way /proc has it, so runtime state a
// human is most likely to be staring at while something is stuck, such as who holds a
// lock or what has claimed the machine's memory, reads with cat and nothing else.
//
// Fields are described with struct tags, the way encoding/json describes them:
//
//	type owner struct {
//	    PID     int       `record:"pid"`
//	    Started time.Time `record:"started"`
//	    Inv     string    `record:"invocation,omitempty"`
//	}
//
// Tags rather than a map at each call site because the decode is where this gets
// subtly wrong: a caller hand-reading its own fields collapses "absent" and "the
// read failed" into one empty string, then deletes a record that was merely
// unreadable for a moment.
//
// Not a general serialization format. A record is a handful of short scalars
// describing a live process; anything with nesting or size wants a real encoder.
package record

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound reports that a record is absent. Distinguished from every other
// failure because reaping on "nothing is here" is correct and reaping on "here but
// unreadable" deletes a record that was merely unreadable for a moment.
var ErrNotFound = errors.New("record: not found")

// partialPrefix marks a record being built; the rename is what publishes one.
const partialPrefix = ".record-tmp-"

// Write encodes v and stores it as dir, replacing any record already there.
//
// A record is never observed HALF written: the fields are filled under a temporary
// name and renamed into place. Replacing an existing one is a different guarantee,
// because rename cannot clobber a non-empty directory: the old record is removed
// first, so a reader in that window sees no record at all and gets ErrNotFound.
// Absence therefore means "nothing to read right now", not "nothing was ever here",
// which is why a caller that reaps on it must only reap records it is not itself
// rewriting.
func Write(dir string, v any) error {
	fields, err := marshal(v)
	if err != nil {
		return err
	}
	parent, name := filepath.Dir(dir), filepath.Base(dir)
	tmp, err := os.MkdirTemp(parent, partialPrefix+name+"-")
	if err != nil {
		return err
	}
	for k, val := range fields {
		if err := os.WriteFile(filepath.Join(tmp, k), []byte(val+"\n"), 0o644); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
	}
	// rename cannot replace a non-empty directory, so an existing record goes first.
	// The window between the two is why Read reports a record missing a required
	// field as ErrNotFound rather than as corruption.
	if err := os.RemoveAll(dir); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
}

// Read decodes the record at dir into v, a non-nil pointer to a struct.
//
// ErrNotFound when the directory or a required field is absent: a record
// mid-removal looks exactly like one that was never there. Any other failure is
// returned as itself, so a caller can tell a record it may delete from one it must
// leave alone.
func Read(dir string, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("record: Read wants a non-nil struct pointer, got %T", v)
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	rv = rv.Elem()
	rt := rv.Type()
	for i := range rt.NumField() {
		name, omitempty, ok := fieldName(rt.Field(i))
		if !ok {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("record: read %s: %w", name, err)
			}
			if omitempty {
				continue
			}
			return ErrNotFound
		}
		if err := setField(rv.Field(i), strings.TrimSpace(string(raw))); err != nil {
			return fmt.Errorf("record: field %s: %w", name, err)
		}
	}
	return nil
}

// Remove deletes a record. One that is not there is not an error.
func Remove(dir string) error {
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// IsPartial reports whether name is a record being written rather than a
// published one. A directory walk must skip these.
func IsPartial(name string) bool { return strings.HasPrefix(name, partialPrefix) }

// marshal renders v's tagged fields. An omitempty field at its zero value is
// left out entirely, so a reader can tell "not applicable" from "failed to render".
func marshal(v any) (map[string]string, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("record: want a struct, got %T", v)
	}
	rt := rv.Type()
	out := make(map[string]string, rt.NumField())
	for i := range rt.NumField() {
		name, omitempty, ok := fieldName(rt.Field(i))
		if !ok {
			continue
		}
		s, empty, err := formatField(rv.Field(i))
		if err != nil {
			return nil, fmt.Errorf("record: field %s: %w", name, err)
		}
		if empty && omitempty {
			continue
		}
		out[name] = s
	}
	return out, nil
}

// fieldName reads a field's `record:"name,omitempty"` tag. An untagged field
// is skipped: the on-disk shape is stated explicitly, so renaming a Go field cannot
// silently rename a file other processes are reading.
func fieldName(f reflect.StructField) (name string, omitempty, ok bool) {
	tag, present := f.Tag.Lookup("record")
	if !present || tag == "-" || !f.IsExported() {
		return "", false, false
	}
	name, rest, _ := strings.Cut(tag, ",")
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", false, false
	}
	return name, rest == "omitempty", true
}

// formatField renders one value and reports whether it is the zero value. An
// unsupported field is an error rather than a silent skip: a record that quietly
// stopped persisting half its fields is the failure this exists to prevent.
func formatField(v reflect.Value) (s string, empty bool, err error) {
	if t, ok := v.Interface().(time.Time); ok {
		return t.Format(time.RFC3339), t.IsZero(), nil
	}
	switch v.Kind() {
	case reflect.String:
		return v.String(), v.String() == "", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), v.Int() == 0, nil
	case reflect.Bool:
		return strconv.FormatBool(v.Bool()), !v.Bool(), nil
	default:
		return "", false, fmt.Errorf("unsupported type %s", v.Type())
	}
}

func setField(v reflect.Value, raw string) error {
	if _, ok := v.Interface().(time.Time); ok {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(t))
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(raw)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		v.SetInt(n)
		return nil
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		v.SetBool(b)
		return nil
	default:
		return fmt.Errorf("unsupported type %s", v.Type())
	}
}
