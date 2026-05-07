package config

import "strings"

// EnvName returns the env var name: parts joined with "_", uppercased, prefixed with prefix+"_" when non-empty.
func EnvName(prefix string, parts ...string) string {
	body := strings.Join(parts, "_")
	if prefix != "" {
		body = prefix + "_" + body
	}
	body = strings.ReplaceAll(body, "-", "_")
	return strings.ToUpper(body)
}

// FlagName returns the CLI flag name: parts joined with "-" with "_" replaced by "-".
func FlagName(parts ...string) string {
	return strings.ReplaceAll(strings.Join(parts, "-"), "_", "-")
}
