// Package record stores a small struct as ONE FILE of name-then-value lines, tab
// separated:
//
//	$ cat locks/a6d4b12e/lock.owner
//	command	magus run ci .
//	pid	41221
//	started	2026-08-24T09:53:02-04:00
//
// Runtime state a human is most likely to be staring at while something is stuck, such
// as who holds a lock or what has claimed the machine's memory, reads with cat and
// nothing else.
//
// It was a DIRECTORY with one file per field, which read the same way per field and cost
// a mkdir, a create per field, a RemoveAll of the previous record and a rename - about
// twenty-six syscalls, measured at 670us per write on macOS. A lock acquisition writes one
// and its release removes it, so a run over many projects paid that per project, per lock.
// The file shape is a create and a rename: measured 112us, six times faster, and the whole
// record now arrives in ONE cat rather than five.
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
	"slices"
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

// Write encodes v and stores it at path, replacing any record already there.
//
// A record is never observed HALF written: it is filled under a temporary name and renamed
// into place, and rename on a FILE is atomic and replacing. A reader therefore sees either
// the whole previous record or the whole new one, never neither and never a mix - which the
// directory shape this replaced could not promise, since it had to remove the old record
// before renaming the new one into its place.
func Write(path string, v any) error {
	fields, err := marshal(v)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	// Sorted so a record's bytes depend only on its values. An unordered map made every
	// rewrite of an unchanged record a different file, which is noise to anyone diffing or
	// watching one.
	slices.Sort(names)
	var b strings.Builder
	for _, k := range names {
		b.WriteString(k)
		b.WriteByte('\t')
		b.WriteString(escape(fields[k]))
		b.WriteByte('\n')
	}

	parent, name := filepath.Dir(path), filepath.Base(path)
	tmp, err := os.CreateTemp(parent, partialPrefix+name+"-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(b.String()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	// rename REPLACES a file, unlike the directory this used to be, so there is no window
	// where the old record is gone and the new one is not yet there. Read's ErrNotFound still
	// covers a record that genuinely is not there, but it no longer has to cover a record
	// caught mid-replacement, because that state cannot occur.
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// escape and unescape keep a value on one line. A record's fields are short scalars, but a
// command line is one of them and nothing stops an argument holding a newline or a tab, which
// would otherwise be read back as a different field or a truncated one.
//
// optimization: one package-level replacer; NewReplacer builds a lookup table per call, and
// escape runs once per field.
//
//	measured: BenchmarkWriteReplace 35.9 KiB/op -> 2.0 KiB/op, 59 -> 23 allocs/op
//	          (n=5, -benchtime=200x). The ns/op change was inside run-to-run noise;
//	          the allocation is the whole claim.
//	trade-off: none. strings.Replacer is documented safe for concurrent use.
var escaper = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\t", "\\t")

func escape(s string) string { return escaper.Replace(s) }

func unescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// Read decodes the record at path into v, a non-nil pointer to a struct.
//
// ErrNotFound when the file or a required field is absent. Any other failure is returned as
// itself, so a caller can tell a record it may delete from one it must leave alone.
func Read(path string, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("record: Read wants a non-nil struct pointer, got %T", v)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("record: not found at %s: %w", path, ErrNotFound)
		}
		return err
	}
	stored := make(map[string]string)
	for line := range strings.SplitSeq(strings.TrimRight(string(raw), "\n"), "\n") {
		k, val, ok := strings.Cut(line, "\t")
		if !ok {
			// A line with no separator is not a field. Skipped rather than failed: a record
			// is read while diagnosing something already broken, and refusing the whole
			// thing over one unparseable line withholds the fields that are fine.
			continue
		}
		stored[k] = unescape(val)
	}
	rv = rv.Elem()
	rt := rv.Type()
	for i := range rt.NumField() {
		name, omitempty, ok := fieldName(rt.Field(i))
		if !ok {
			continue
		}
		val, present := stored[name]
		if !present {
			if omitempty {
				continue
			}
			return fmt.Errorf("record: field %s missing from %s: %w", name, path, ErrNotFound)
		}
		if err := setField(rv.Field(i), strings.TrimSpace(val)); err != nil {
			return fmt.Errorf("record: field %s: %w", name, err)
		}
	}
	return nil
}

// Remove deletes a record. One that is not there is not an error.
//
// RemoveAll rather than Remove: a record written by an older magus is a directory, and a
// caller reaping stale state must be able to clear one.
func Remove(dir string) error {
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

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
