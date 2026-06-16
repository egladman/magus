package types

import (
	"errors"
	"testing"
)

func TestVCSErrorSentinels(t *testing.T) {
	for _, sentinel := range []error{
		ErrVCSUnsupported,
		ErrVCSUnknown,
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
	sources := []VCSSource{
		VCSSourceExplicit,
		VCSSourceAuto,
		VCSSourceDefault,
		VCSSourceDisabled,
	}
	seen := map[VCSSource]bool{}
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
	var r VCSResolution
	if r.VCS != nil {
		t.Error("zero VCSResolution should have nil VCS")
	}
	if r.Name != "" {
		t.Errorf("zero VCSResolution.Name = %q, want empty", r.Name)
	}
}
