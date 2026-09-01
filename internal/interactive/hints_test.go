package interactive

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEmit_DefaultOn(t *testing.T) {
	var buf bytes.Buffer
	Emit(&buf, "try `magus run` instead")
	assert.True(t, strings.HasPrefix(buf.String(), "hint: "), "Emit output = %q; want prefix %q", buf.String(), "hint: ")
}

func TestEmit_SetHintsEnabledFalse(t *testing.T) {
	SetHintsEnabled(false)
	t.Cleanup(func() { SetHintsEnabled(true) })
	var buf bytes.Buffer
	Emit(&buf, "try `magus run` instead")
	assert.Zero(t, buf.Len(), "Emit wrote %q with hints disabled; want nothing", buf.String())
}

func TestEmit_SetHintsEnabledTrue(t *testing.T) {
	SetHintsEnabled(true)
	var buf bytes.Buffer
	// A message distinct from the other Emit tests in this file: Emit dedupes by
	// text across the whole process, and this test wants to confirm re-enabling
	// hints works, not exercise the dedupe path.
	Emit(&buf, "try `magus doctor` instead")
	assert.True(t, strings.HasPrefix(buf.String(), "hint: "), "Emit output = %q; want prefix %q", buf.String(), "hint: ")
}

func TestEmit_DedupesRepeatedMessage(t *testing.T) {
	SetHintsEnabled(true)
	msg := "this exact hint should only teach once: " + t.Name()
	var first, second bytes.Buffer
	Emit(&first, msg)
	Emit(&second, msg)
	assert.True(t, strings.HasPrefix(first.String(), "hint: "), "first Emit of a new message must write")
	assert.Zero(t, second.String(), "second Emit of the same message must be suppressed")
}
