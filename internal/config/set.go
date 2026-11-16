package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/file/watch"
)

// SetOutput is the record `magus config set` prints under -o.
type SetOutput struct {
	Path  string `json:"path"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ParseSetArg parses a single "key=<key>,value=<value>" argument.
// Commas inside the value must be backslash-escaped (\,).
func ParseSetArg(s string) (key, value string, err error) {
	parts := watch.SplitEscapedCommas(s)
	for _, part := range parts {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			return "", "", fmt.Errorf("%q is not in key=value form (expected key=<key>,value=<value>)", part)
		}
		k := strings.TrimSpace(part[:eq])
		v := part[eq+1:]
		switch k {
		case "key":
			key = v
		case "value":
			value = v
		default:
			return "", "", fmt.Errorf("unknown field %q (allowed: key, value)", k)
		}
	}
	if key == "" {
		return "", "", fmt.Errorf("key is required (use key=<key>,value=<value>)")
	}
	return key, value, nil
}

// StringOrDef returns s, or def when s is empty.
func StringOrDef(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// IntOrDef returns n as text, or def when n is zero: zero means nothing was
// configured, not that zero builds may run.
func IntOrDef(n int, def string) string {
	if n == 0 {
		return def
	}
	return strconv.Itoa(n)
}
