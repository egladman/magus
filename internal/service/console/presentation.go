package console

import (
	"fmt"
	"strings"
)

const DefaultSurface = "dashboard"

// Presentation is a local console link an MCP client may show to its user.
type Presentation struct {
	URL     string `json:"url" yaml:"url"`
	Surface string `json:"surface" yaml:"surface"`
	Reason  string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// Present validates a surface and builds its tokenless console link.
func Present(host, surface, reason string) (Presentation, error) {
	surface = strings.TrimSpace(surface)
	if surface == "" {
		surface = DefaultSurface
	}
	if !IsSurfaceRoute(surface) {
		return Presentation{}, fmt.Errorf("console: unknown surface %q", surface)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return Presentation{}, fmt.Errorf("console: no local address")
	}
	return Presentation{
		URL:     Link(LinkOpts{Host: host, Surface: surface}),
		Surface: surface,
		Reason:  strings.TrimSpace(reason),
	}, nil
}
