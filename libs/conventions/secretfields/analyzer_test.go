package secretfields

import (
	"slices"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

var words = []string{"Password", "Passwd", "Secret", "Token", "Tokens", "Credential", "APIKey", "Bearer", "SigningKey", "PrivateKey", "HMAC"}

func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{Words: words})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "creds")
}

func TestCamelWords(t *testing.T) {
	for name, want := range map[string][]string{
		"registryToken": {"registry", "Token"},
		"APIKey":        {"API", "Key"},
		"HMAC":          {"HMAC"},
		"refresh_token": {"refresh", "token"},
		"PasswordRef":   {"Password", "Ref"},
		"v2Token":       {"v2", "Token"},
	} {
		if got := camelWords(name); !slices.Equal(got, want) {
			t.Errorf("camelWords(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestNewRejectsAnEmptyWordList(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("want an error for an empty word list")
	}
	if _, err := New(Options{Words: []string{""}}); err == nil {
		t.Fatal("want an error for an empty word")
	}
}
