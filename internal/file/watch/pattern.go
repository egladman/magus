package watch

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/types"
)

// PatternType is an alias for [types.PatternType]. Glob is the default;
// regex and literal are escape hatches for cases globs cannot express.
type PatternType = types.PatternType

const (
	PatternGlob    PatternType = "glob"    // doublestar `**`-aware glob; default for bare CLI values
	PatternRegex   PatternType = "regex"   // Go regexp; for rules globs cannot express
	PatternLiteral PatternType = "literal" // matches any path segment at any depth (like .gitignore bare entry)
)

// IgnorePattern is an alias for types.IgnorePattern kept for backwards compatibility.
type IgnorePattern = types.IgnorePattern

// ValidatePattern returns nil when p is well-formed (known type; regex compiles; non-empty pattern).
func ValidatePattern(p IgnorePattern) error {
	if p.Pattern == "" {
		return fmt.Errorf("ignore: pattern is empty")
	}
	switch p.Type {
	case PatternGlob:
		if _, err := doublestar.Match(p.Pattern, ""); err != nil { // best-effort precheck (doublestar fails lazily)
			return fmt.Errorf("ignore: invalid glob %q: %w", p.Pattern, err)
		}
		return nil
	case PatternRegex:
		if _, err := regexp.Compile(p.Pattern); err != nil {
			return fmt.Errorf("ignore: invalid regex %q: %w", p.Pattern, err)
		}
		return nil
	case PatternLiteral:
		return nil
	case "":
		return fmt.Errorf("ignore: type is empty (use glob, regex, or literal)")
	default:
		return fmt.Errorf("ignore: unknown type %q (use glob, regex, or literal)", p.Type)
	}
}

// IgnorePatterns returns a predicate that matches when any pattern matches abs path relative to wsRoot.
func IgnorePatterns(wsRoot string, patterns []IgnorePattern) func(string) bool {
	if len(patterns) == 0 {
		return func(string) bool { return false }
	}
	type compiled struct {
		typ PatternType
		pat string
		re  *regexp.Regexp
	}
	cs := make([]compiled, 0, len(patterns))
	for _, p := range patterns {
		c := compiled{typ: p.Type, pat: p.Pattern}
		if p.Type == PatternRegex {
			re, err := regexp.Compile(p.Pattern)
			if err != nil {
				continue // skip; ValidatePattern should have caught this earlier
			}
			c.re = re
		}
		cs = append(cs, c)
	}

	return func(absPath string) bool {
		rel, err := filepath.Rel(wsRoot, absPath)
		if err != nil {
			return false
		}
		rel = filepath.ToSlash(rel)
		for _, c := range cs {
			switch c.typ {
			case PatternGlob:
				if ok, _ := doublestar.Match(c.pat, rel); ok {
					return true
				}
				// Also prune ancestor dirs the glob targets (e.g. `**/scratch/*` should skip the dir itself).
				if ok, _ := doublestar.Match(c.pat+"/**", rel); ok {
					return true
				}
			case PatternRegex:
				if c.re != nil && c.re.MatchString(rel) {
					return true
				}
			case PatternLiteral:
				for _, seg := range strings.Split(rel, "/") {
					if seg == c.pat {
						return true
					}
				}
			}
		}
		return false
	}
}

// ParsePattern parses a "type=glob|regex|literal,pattern=<value>" string.
// Commas inside a value must be backslash-escaped (\,) for regex quantifiers.
func ParsePattern(s string) (IgnorePattern, error) {
	if s == "" {
		return IgnorePattern{}, fmt.Errorf("ignore: empty entry")
	}

	parts := splitEscapedCommas(s)
	out := IgnorePattern{}
	for _, part := range parts {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			return IgnorePattern{}, fmt.Errorf("ignore: %q is not in key=value form (use type=glob|regex|literal,pattern=<value>)", part)
		}
		key := strings.TrimSpace(part[:eq])
		val := part[eq+1:]
		switch key {
		case "type":
			out.Type = PatternType(strings.TrimSpace(val))
		case "pattern":
			out.Pattern = val
		default:
			return IgnorePattern{}, fmt.Errorf("ignore: unknown key %q (allowed: type, pattern)", key)
		}
	}
	if out.Type == "" {
		return IgnorePattern{}, fmt.Errorf("ignore: type is required (use type=glob, type=regex, or type=literal)")
	}
	if err := ValidatePattern(out); err != nil {
		return IgnorePattern{}, err
	}
	return out, nil
}

// splitEscapedCommas splits s on unescaped commas (\, is a literal comma).
func splitEscapedCommas(s string) []string {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) && s[i+1] == ',' {
			cur.WriteByte(',')
			i++
			continue
		}
		if c == ',' {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	parts = append(parts, cur.String())
	return parts
}
