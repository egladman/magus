package convert

import (
	"fmt"
	"sync"
	"time"
)

// Table holds Go values a Buzz script refers to by an int handle. Handles start
// at 1, so a zero int is never a live handle.
type Table[T any] struct {
	name   string
	mu     sync.Mutex
	values []T
}

// NewTable returns an empty table whose errors name the Go type as name.
func NewTable[T any](name string) *Table[T] { return &Table[T]{name: name} }

// Put stores v and returns its handle.
func (t *Table[T]) Put(v T) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.values = append(t.values, v)
	return len(t.values)
}

// Get returns the value behind handle h, or an error naming the type when h
// was never handed out.
func (t *Table[T]) Get(h int) (T, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if h < 1 || h > len(t.values) {
		var zero T
		return zero, fmt.Errorf("no %s with handle %d", t.name, h)
	}
	return t.values[h-1], nil
}

// Int reads a Buzz number. A double with no fraction is accepted, since JSON
// and Buzz literals both reach Go as either.
func Int(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("%v is not a whole number", n)
		}
		return int(n), nil
	case nil:
		return 0, nil
	}
	return 0, fmt.Errorf("%v is %T, not a number", v, v)
}

// Float reads a Buzz number as a double.
func Float(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case nil:
		return 0, nil
	}
	return 0, fmt.Errorf("%v is %T, not a number", v, v)
}

// String reads a Buzz str. A missing field reads as "".
func String(v any) (string, error) {
	switch s := v.(type) {
	case string:
		return s, nil
	case nil:
		return "", nil
	}
	return "", fmt.Errorf("%v is %T, not a str", v, v)
}

// Bool reads a Buzz bool. A missing field reads as false.
func Bool(v any) (bool, error) {
	switch b := v.(type) {
	case bool:
		return b, nil
	case nil:
		return false, nil
	}
	return false, fmt.Errorf("%v is %T, not a bool", v, v)
}

// Map reads a Buzz map with str keys. A missing field reads as an empty map.
func Map(v any) (map[string]any, error) {
	switch m := v.(type) {
	case map[string]any:
		return m, nil
	case nil:
		return map[string]any{}, nil
	}
	return nil, fmt.Errorf("%v is %T, not a map", v, v)
}

// List reads a Buzz list. A missing field reads as an empty list.
func List(v any) ([]any, error) {
	switch l := v.(type) {
	case []any:
		return l, nil
	case nil:
		return nil, nil
	}
	return nil, fmt.Errorf("%v is %T, not a list", v, v)
}

// Strings reads a Buzz list of str.
func Strings(v any) ([]string, error) {
	if l, ok := v.([]string); ok {
		return l, nil
	}
	items, err := List(v)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(items))
	for i, it := range items {
		if out[i], err = String(it); err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
	}
	return out, nil
}

// IntAs reads a Buzz number as the named integer type T.
func IntAs[T ~int | ~int32 | ~int64 | ~uintptr | ~uint8](v any) (T, error) {
	n, err := Int(v)
	return T(n), err
}

// StringAs reads a Buzz str as the named string type T.
func StringAs[T ~string](v any) (T, error) {
	s, err := String(v)
	return T(s), err
}

// Millis reads a Buzz number of milliseconds as a duration.
func Millis(v any) (time.Duration, error) {
	n, err := Int(v)
	return time.Duration(n) * time.Millisecond, err
}

// Handle reads a Buzz handle and returns the value behind it.
func Handle[T any](t *Table[T], v any) (T, error) {
	h, err := Int(v)
	if err != nil {
		var zero T
		return zero, err
	}
	return t.Get(h)
}

// DecodeSlice reads a Buzz list, decoding each item with one.
func DecodeSlice[T any](v any, one func(any) (T, error)) ([]T, error) {
	items, err := List(v)
	if err != nil {
		return nil, err
	}
	out := make([]T, len(items))
	for i, it := range items {
		if out[i], err = one(it); err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
	}
	return out, nil
}

// EncodeList writes a Go slice as a Buzz list, encoding each item with one.
func EncodeList[T any](items []T, one func(T) any) []any {
	out := make([]any, len(items))
	for i, it := range items {
		out[i] = one(it)
	}
	return out
}

// Bytes reads a Buzz list of ints as bytes.
func Bytes(v any) ([]byte, error) {
	return DecodeSlice(v, IntAs[byte])
}

// BytesList writes bytes as a Buzz list of ints.
func BytesList(b []byte) []any {
	return EncodeList(b, func(x byte) any { return int(x) })
}

// ErrorText is an error's message, or "" for none.
func ErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Floats reads a Buzz list of numbers.
func Floats(v any) ([]float64, error) {
	if l, ok := v.([]float64); ok {
		return l, nil
	}
	items, err := List(v)
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(items))
	for i, it := range items {
		if out[i], err = Float(it); err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
	}
	return out, nil
}
