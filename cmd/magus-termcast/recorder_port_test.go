package main

import (
	"os"
	"testing"

	"github.com/egladman/magus/libs/termkit/screen"
	"github.com/stretchr/testify/require"
)

func TestRenderMatchesRecordedSVG(t *testing.T) {
	for _, name := range []string{"core-loop", "showcase"} {
		capture, err := os.ReadFile("../../tapes/" + name + ".capture")
		require.NoError(t, err)
		renderCapture := render
		if name == "showcase" {
			renderCapture = renderShowcase
		}
		for _, variant := range screen.ThemeVariants {
			t.Run(name+variant.Suffix, func(t *testing.T) {
				want, err := os.ReadFile("../../assets/gen/" + name + variant.Suffix + ".svg")
				require.NoError(t, err)
				got, err := renderCapture(string(capture), variant.Theme)
				require.NoError(t, err)
				require.Equal(t, string(want), got)
			})
		}
	}
}
