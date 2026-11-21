package main

import (
	"os"
	"testing"

	"github.com/egladman/magus/libs/termkit/recorder"
	"github.com/egladman/magus/libs/termkit/screen"
	"github.com/stretchr/testify/require"
)

func TestRecorderMatchesOriginalByteForByte(t *testing.T) {
	for _, name := range []string{"core-loop", "showcase"} {
		capture, err := os.ReadFile("../../tapes/" + name + ".capture")
		require.NoError(t, err)
		for _, variant := range screen.ThemeVariants {
			t.Run(name+variant.Suffix, func(t *testing.T) {
				segments := recorder.Commands(string(capture), prompt)
				width, height, pacing := cols, rows, corePace
				original := render
				if name == "showcase" {
					segments = recorder.Marked(string(capture), frameMark)
					width, height, pacing = showCols, showRows, showcasePace
					original = renderShowcase
				}
				want, err := original(string(capture), variant.Theme)
				require.NoError(t, err)
				got, err := recorder.Animate(segments, width, height,
					recorder.Pace{Base: pacing.base, PerLine: pacing.perLine, Max: pacing.max, Last: pacing.last}, variant.Theme)
				require.NoError(t, err)
				require.Equal(t, want, got)
			})
		}
	}
}
