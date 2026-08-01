package spellruntime

import (
	"testing"

	"github.com/egladman/magus/spells"

	"github.com/stretchr/testify/assert"
)

func TestValidatePatch(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		assert.NoError(t, spells.ValidatePatch(nil))
	})
	t.Run("add end", func(t *testing.T) {
		assert.NoError(t, spells.ValidatePatch([]spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}))
	})
	t.Run("replace index", func(t *testing.T) {
		assert.NoError(t, spells.ValidatePatch([]spells.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}))
	})
	t.Run("remove index", func(t *testing.T) {
		assert.NoError(t, spells.ValidatePatch([]spells.PatchOp{{Op: "remove", Path: "/2"}}))
	})
	t.Run("move", func(t *testing.T) {
		assert.NoError(t, spells.ValidatePatch([]spells.PatchOp{{Op: "move", Path: "/0", From: "/1"}}))
	})
	t.Run("copy", func(t *testing.T) {
		assert.NoError(t, spells.ValidatePatch([]spells.PatchOp{{Op: "copy", Path: "/0", From: "/1"}}))
	})
	t.Run("test", func(t *testing.T) {
		assert.NoError(t, spells.ValidatePatch([]spells.PatchOp{{Op: "test", Path: "/0", Value: "go"}}))
	})
	t.Run("unknown op", func(t *testing.T) {
		assert.Error(t, spells.ValidatePatch([]spells.PatchOp{{Op: "patch", Path: "/0"}}))
	})
	t.Run("root path rejected", func(t *testing.T) {
		assert.Error(t, spells.ValidatePatch([]spells.PatchOp{{Op: "replace", Path: "", Value: "x"}}))
	})
	t.Run("path without slash", func(t *testing.T) {
		assert.Error(t, spells.ValidatePatch([]spells.PatchOp{{Op: "add", Path: "0", Value: "x"}}))
	})
	t.Run("move without from", func(t *testing.T) {
		assert.Error(t, spells.ValidatePatch([]spells.PatchOp{{Op: "move", Path: "/0"}}))
	})
	t.Run("copy bad from", func(t *testing.T) {
		assert.Error(t, spells.ValidatePatch([]spells.PatchOp{{Op: "copy", Path: "/0", From: "1"}}))
	})
}

func TestDescriptor_TargetNames(t *testing.T) {
	m := spells.Descriptor{
		Name: "test",
		Ops: map[string]spells.Op{
			"vet":   {},
			"build": {},
			"test":  {},
		},
	}
	assert.Equal(t, []string{"build", "test", "vet"}, m.OpNames())
}

func TestDescriptor_TargetNamesEmpty(t *testing.T) {
	m := spells.Descriptor{Name: "empty"}
	assert.Empty(t, m.OpNames(), "OpNames() on empty Ops should be empty")
}
