package main

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	json "github.com/egladman/magus/internal/json"
)

// shippedHookConfigs are the host hook configs this repository owns: the one it
// dogfoods, and the one it ships for a reader to copy.
var shippedHookConfigs = map[string]string{
	"claude-code": ".claude/settings.json",
	"codex":       "docs/guides/integrations/agents/codex-hooks.json",
}

// hookSettings is a host hook config keyed by event name.
type hookSettings struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// buzzGlueIsWiredAsPlainArgv refuses a `NAME=value` prefix on any hook command
// that runs the Buzz glue.
//
// A leading assignment is shell syntax, and a hook command is not a shell line:
// the host splits it into argv itself, so the prefix only works where something
// later re-joins and re-parses it. The sh copies genuinely need their environment
// knobs, because `sh` is what runs them and `sh` is what reads the variable; the
// Buzz ports take the same two knobs from the event they already read and from
// their own argv, which `magus buzz` forwards after `--`.
func buzzGlueIsWiredAsPlainArgv(fsys fs.FS) []finding {
	var findings []finding
	for _, host := range slices.Sorted(maps.Keys(shippedHookConfigs)) {
		p := shippedHookConfigs[host]
		raw, bad := readFile(fsys, p)
		if bad != nil {
			findings = append(findings, bad...)
			continue
		}
		var cfg hookSettings
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			findings = append(findings, finding{path: p, problem: "does not parse: " + err.Error()})
			continue
		}
		for _, event := range slices.Sorted(maps.Keys(cfg.Hooks)) {
			for _, entry := range cfg.Hooks[event] {
				for _, h := range entry.Hooks {
					if !strings.Contains(h.Command, ".buzz") {
						continue
					}
					fields := strings.Fields(h.Command)
					if len(fields) == 0 {
						continue
					}
					name, _, isAssignment := strings.Cut(fields[0], "=")
					if !isAssignment || !isEnvName(name) {
						continue
					}
					findings = append(findings, finding{path: p, line: lineOf(raw, fields[0]),
						problem: fmt.Sprintf("wires the Buzz glue on %s %q behind a %s= prefix", event, entry.Matcher, name),
						fix: "A Buzz hook command is a plain argv: " + host + " runs magus buzz -s <file> [-- flags].\n" +
							"The glue reads the raw-event question off the event itself and takes its\n" +
							"flags from the argv after --, so there is nothing left for a variable to say."})
				}
			}
		}
	}
	return findings
}

// isEnvName mirrors internal/agent's isEnvName: shaped like an environment
// variable name. Copied rather than exported, because exporting it would publish
// a shell-syntax predicate from a package whose point here is that the Buzz
// wiring has no shell.
func isEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
