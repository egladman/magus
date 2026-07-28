package tty

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProbe is the test Probe with controllable answers. It lives in
// _test.go so no production build can reach it.
type fakeProbe struct {
	isTTY   bool
	width   int
	height  int
	sizeErr error
}

func (f fakeProbe) IsTerminal(uintptr) bool { return f.isTTY }
func (f fakeProbe) Size(uintptr) (int, int, error) {
	return f.width, f.height, f.sizeErr
}

// compile-time proof that both implementations satisfy the interface.
var (
	_ Probe = fakeProbe{}
	_ Probe = systemProbe{}
)

func TestSystemProbeAnswersForARealDescriptor(t *testing.T) {
	t.Parallel()
	// The test runner's stdout is typically a pipe, so the only stable
	// assertion is that a descriptor the probe calls a terminal also
	// reports a usable size.
	if !SystemProbe.IsTerminal(os.Stdout.Fd()) {
		return
	}
	w, h, err := SystemProbe.Size(os.Stdout.Fd())
	require.NoError(t, err, "a terminal must report a size")
	assert.Positive(t, w, "terminal width")
	assert.Positive(t, h, "terminal height")
}

func TestFdReportsDescriptorForAFile(t *testing.T) {
	t.Parallel()
	fd, ok := Fd(os.Stdout)
	require.True(t, ok, "os.Stdout is backed by a descriptor")
	assert.Equal(t, os.Stdout.Fd(), fd)
}

// TestFdDistinguishesNoDescriptorFromStdin is the reason Fd returns a
// bool: descriptor 0 is stdin, so a lone uintptr has no spare value left
// to mean "this writer has none".
func TestFdDistinguishesNoDescriptorFromStdin(t *testing.T) {
	t.Parallel()

	_, ok := Fd(&bytes.Buffer{})
	assert.False(t, ok, "a bytes.Buffer has no descriptor")

	fd, ok := Fd(os.Stdin)
	require.True(t, ok, "os.Stdin has a descriptor")
	assert.Equal(t, uintptr(0), fd, "stdin is descriptor 0, a real value Fd must still report")
}

func TestIsTerminalWriterRequiresBothADescriptorAndATerminal(t *testing.T) {
	t.Parallel()
	yes := fakeProbe{isTTY: true}
	no := fakeProbe{}

	assert.True(t, IsTerminalWriter(&ttyBuf{}, yes), "a descriptor the probe calls a terminal")
	assert.False(t, IsTerminalWriter(&ttyBuf{}, no), "a descriptor the probe calls a pipe")
	assert.False(t, IsTerminalWriter(&bytes.Buffer{}, yes),
		"a writer with no descriptor is never a terminal, whatever the probe says")
}

// TestWantsColorHonoursNoColor pins the https://no-color.org contract:
// any non-empty value disables colour, including "0" and "false", which
// are values a user might expect to mean "off".
func TestWantsColorHonoursNoColor(t *testing.T) {
	terminal := fakeProbe{isTTY: true}

	t.Run("unset means colour on a terminal", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		assert.True(t, WantsColor(&ttyBuf{}, terminal))
	})

	for _, v := range []string{"1", "0", "false", "anything"} {
		t.Run("set to "+v+" disables colour", func(t *testing.T) {
			t.Setenv("NO_COLOR", v)
			assert.False(t, WantsColor(&ttyBuf{}, terminal),
				"any non-empty NO_COLOR disables colour")
		})
	}
}

func TestWantsColorIsFalseOffATerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	assert.False(t, WantsColor(&bytes.Buffer{}, fakeProbe{isTTY: true}),
		"a pipe gets no escapes even when NO_COLOR is unset")
}

func TestFakeProbeSizeErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("ioctl failed")
	_, _, err := fakeProbe{sizeErr: want}.Size(1)
	assert.ErrorIs(t, err, want)
}
