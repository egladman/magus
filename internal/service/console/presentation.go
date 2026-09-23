package console

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/egladman/magus/internal/hint"
)

const DefaultSurface = "dashboard"

// Presentation is a local console link an MCP client may show to its user.
type Presentation struct {
	URL     string `json:"url" yaml:"url"`
	Surface string `json:"surface" yaml:"surface"`
	Reason  string `json:"reason,omitempty" yaml:"reason,omitempty"`
	// Open is a shell line that opens URL signed in (see [OpenCommand]). URL alone lands on
	// the console's sign-in screen, because every console route needs a token.
	Open string `json:"open" yaml:"open"`
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
	link := Link(LinkOpts{Host: host, Surface: surface})
	return Presentation{
		URL:     link,
		Surface: surface,
		Reason:  strings.TrimSpace(reason),
		// The daemon answers this, so its own argv0 means nothing to the reader.
		Open: OpenCommandAs(link, runtime.GOOS, hint.DefaultBinaryName),
	}, nil
}
