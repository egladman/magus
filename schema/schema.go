package schema

import (
	"strings"

	"github.com/kkyr/fig"

	"github.com/egladman/magus/schema/gen"
)

// EnvPrefix is the prefix shared by every env var in [Fields].
const EnvPrefix = "MAGUS"

// Fields is the precomputed inventory of every config-backed CLI flag and MAGUS_* env var.
var Fields []Field

var byEnv map[string]int    // O(1) index by env-var name
var byGoPath map[string]int // O(1) index by Go field path (e.g. "Cache.Dir")

func init() {
	Fields = gen.Fields
	byEnv = make(map[string]int, len(Fields))
	byGoPath = make(map[string]int, len(Fields))
	for i, f := range Fields {
		byEnv[f.EnvVar] = i
		byGoPath[f.GoPath] = i
	}
}

// UseEnv returns a [fig.Option] for schema's env scheme (equivalent to fig.UseEnv(EnvPrefix)).
func UseEnv() fig.Option {
	return fig.UseEnv(EnvPrefix)
}

// FieldByEnv returns the [Field] for the given MAGUS_* env-var name.
// Returns false if the name is not a known config-backed env var.
func FieldByEnv(name string) (Field, bool) {
	i, ok := byEnv[name]
	if !ok {
		return Field{}, false
	}
	return Fields[i], true
}

// FieldByGoPath returns the [Field] for the given Go field path (e.g. "Cache.Dir").
// Returns false if the path is not a known config-backed field.
func FieldByGoPath(path string) (Field, bool) {
	i, ok := byGoPath[path]
	if !ok {
		return Field{}, false
	}
	return Fields[i], true
}

// ParseBool parses a boolean environment variable value using a
// case-insensitive comparison. "true", "1", "yes" → true; "false", "0", "no"
// → false. Any unrecognised value returns fallback unchanged.
func ParseBool(v string, fallback bool) bool {
	switch strings.ToLower(v) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	}
	return fallback
}
