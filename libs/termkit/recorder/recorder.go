package recorder

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/termkit/screen"
)

// Pace scales a frame's reading time with its added output.
type Pace struct {
	Base    float64
	PerLine float64
	Max     float64
	Last    float64
}

// Hold gives the final frame its own duration and caps earlier frames.
func (p Pace) Hold(index, count, added int) float64 {
	if index == count-1 {
		return p.Last
	}
	return min(p.Base+p.PerLine*float64(max(added, 0)), p.Max)
}

// Commands keeps startup bytes on the first command's frame.
func Commands(capture, prompt string) []string {
	parts := strings.Split(capture, prompt)
	if len(parts) < 2 {
		return nil
	}
	segments := make([]string, 0, max(len(parts)-1, 0))
	for i, part := range parts[1:] {
		prefix := ""
		if i == 0 {
			prefix = parts[0]
		}
		segments = append(segments, prefix+prompt+part)
	}
	return segments
}

// Marked discards teardown after the final frame marker.
func Marked(capture, marker string) []string {
	parts := strings.Split(capture, marker)
	if len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// Animate replays one cumulative terminal and gives all frames the same height.
func Animate(segments []string, width, height int, pace Pace, theme screen.Theme) (string, error) {
	if width <= 0 || height <= 0 {
		return "", fmt.Errorf("recorder: terminal dimensions must be positive, got %dx%d", width, height)
	}
	terminal := screen.New(width, height)
	frames := make([]*screen.Terminal, 0, len(segments))
	used := 0
	for i, segment := range segments {
		if _, err := terminal.Write([]byte(segment)); err != nil {
			return "", fmt.Errorf("replay segment %d: %w", i, err)
		}
		frame := terminal.Snapshot()
		frames = append(frames, frame)
		used = max(used, frame.LastUsedRow())
	}
	for i, frame := range frames {
		frames[i] = frame.Crop(used)
	}
	holds := make([]float64, len(frames))
	previousRows, previousScrolled := 0, 0
	for i, frame := range frames {
		rows, scrolled := frame.LastUsedRow(), frame.Scrolled()
		added := rows - previousRows + scrolled - previousScrolled
		if i == 0 {
			added = rows + 1
		}
		holds[i] = pace.Hold(i, len(frames), added)
		previousRows, previousScrolled = rows, scrolled
	}
	return screen.Animate(frames, holds, screen.SVGOptions{Theme: theme})
}
