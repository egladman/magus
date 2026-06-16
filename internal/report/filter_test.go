package report

import (
	"testing"
)

func TestParseFilter_Nil_AdmitsAll(t *testing.T) {
	f, err := ParseFilter(nil)
	if err != nil {
		t.Fatalf("ParseFilter(nil): %v", err)
	}
	if f != nil {
		t.Error("ParseFilter(nil) should return nil to signal admit-all")
	}
}

func TestParseFilter_BlankTerms_Nil(t *testing.T) {
	f, err := ParseFilter([]string{"", "  "})
	if err != nil {
		t.Fatalf("ParseFilter blank: %v", err)
	}
	if f != nil {
		t.Error("ParseFilter(blank terms) should return nil")
	}
}

func TestFilter_Admit_DefaultAllow(t *testing.T) {
	f, err := ParseFilter([]string{"-run"})
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	if f.Admit("log") == false {
		t.Error("default-allow: 'log' should be admitted")
	}
	if f.Admit("run") == true {
		t.Error("excluded type 'run' should not be admitted")
	}
}

func TestFilter_Admit_DefaultDeny(t *testing.T) {
	f, err := ParseFilter([]string{"+log"})
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	if !f.Admit("log") {
		t.Error("included type 'log' should be admitted")
	}
	if f.Admit("run") {
		t.Error("default-deny: 'run' should not be admitted")
	}
}
