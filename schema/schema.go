package schema

import (
	"strings"

	"github.com/kkyr/fig"

	"github.com/egladman/magus/schema/gen"
)

// EnvPrefix is the prefix shared by every env var in [Fields].
const EnvPrefix = "MAGUS"

// Fields is the precomputed inventory of every config-backed CLI flag and MAGUS_* env var.
var Fields = gen.Fields

// UseEnv returns a [fig.Option] for schema's env scheme (equivalent to fig.UseEnv(EnvPrefix)).
func UseEnv() fig.Option {
	return fig.UseEnv(EnvPrefix)
}

// ParseBool parses a boolean environment variable value using a
// case-insensitive comparison. "true", "1", "yes" → true; "false", "0", "no"
// → false. Any unrecognized value returns fallback unchanged.
func ParseBool(v string, fallback bool) bool {
	switch strings.ToLower(v) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	}
	return fallback
}
