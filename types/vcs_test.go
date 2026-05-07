package types_test

import (
	"errors"
	"testing"

	"github.com/egladman/magus/types"
)

func TestVCSErrorSentinels(t *testing.T) {
	for _, sentinel := range []error{
		types.ErrVCSUnsupported,
		types.ErrVCSUnknown,
	} {
		if sentinel == nil {
			t.Errorf("%v is nil", sentinel)
		}
		if sentinel.Error() == "" {
			t.Errorf("%v.Error() is empty", sentinel)
		}
		if !errors.Is(sentinel, sentinel) {
			t.Errorf("errors.Is identity check failed for %v", sentinel)
		}
	}
}

func TestVCSSourceConstants(t *testing.T) {
	sources := []types.VCSSource{
		types.VCSSourceExplicit,
		types.VCSSourceAuto,
		types.VCSSourceDefault,
		types.VCSSourceDisabled,
	}
	seen := map[types.VCSSource]bool{}
	for _, s := range sources {
		if string(s) == "" {
			t.Errorf("VCSSource constant is empty")
		}
		if seen[s] {
			t.Errorf("duplicate VCSSource value %q", s)
		}
		seen[s] = true
	}
}

func TestVCSResolution_ZeroValue(t *testing.T) {
	var r types.VCSResolution
	if r.VCS != nil {
		t.Error("zero VCSResolution should have nil VCS")
	}
	if r.Name != "" {
		t.Errorf("zero VCSResolution.Name = %q, want empty", r.Name)
	}
}
