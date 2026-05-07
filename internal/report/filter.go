package report

import (
	"fmt"
	"log/slog"
	"strings"
)

// Filter restricts which event types reach the channel. Terms: "+type"/bare=include, "-type"=exclude.
// Any "+" term sets default-deny; otherwise default-allow with "-" subtracting.
type Filter struct {
	defaultAllow bool
	include      map[string]struct{}
	exclude      map[string]struct{}
}

// ParseFilter parses terms into a Filter. Empty/blank input returns nil (admit all).
func ParseFilter(terms []string) (*Filter, error) {
	cleaned := make([]string, 0, len(terms))
	for _, t := range terms {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		cleaned = append(cleaned, t)
	}
	if len(cleaned) == 0 {
		return nil, nil //nolint:nilnil // a nil *Filter is the documented sentinel for "admit every event" (see ParseFilter doc)
	}
	f := &Filter{
		defaultAllow: true,
		include:      map[string]struct{}{},
		exclude:      map[string]struct{}{},
	}
	for _, t := range cleaned {
		switch t[0] {
		case '+':
			name := strings.TrimSpace(t[1:])
			if name == "" {
				slog.Warn("report: ignoring malformed filter term", "term", t)
				continue
			}
			f.include[name] = struct{}{}
			f.defaultAllow = false
		case '-':
			name := strings.TrimSpace(t[1:])
			if name == "" {
				slog.Warn("report: ignoring malformed filter term", "term", t)
				continue
			}
			f.exclude[name] = struct{}{}
		default:
			f.include[t] = struct{}{}
			f.defaultAllow = false
		}
	}
	if len(f.include) == 0 && len(f.exclude) == 0 {
		return nil, fmt.Errorf("report: no valid filter terms in %v", terms)
	}
	return f, nil
}

// Admit reports whether t passes the filter.
func (f *Filter) Admit(t string) bool {
	if f == nil {
		return true
	}
	if _, denied := f.exclude[t]; denied {
		return false
	}
	if f.defaultAllow {
		return true
	}
	_, allowed := f.include[t]
	return allowed
}
