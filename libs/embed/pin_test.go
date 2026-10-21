package embed

import (
	"strings"
	"testing"
)

func TestPin(t *testing.T) {
	got, err := Pin()
	if err != nil {
		t.Fatalf("Pin() error = %v", err)
	}
	if got+"\n" != tableDigest {
		t.Errorf("Pin() = %q, want the line in gen/table.digest %q", got, tableDigest)
	}
}

func TestParsePin(t *testing.T) {
	valid := strings.Repeat("0123456789abcdef", 4)
	tests := []struct {
		name    string
		text    string
		want    string
		wantErr string
	}{
		{name: "one line", text: valid + "\n", want: valid},
		{name: "missing", text: "", wantErr: "is empty"},
		{name: "no newline", text: valid, wantErr: "want one line"},
		{name: "two lines", text: valid + "\n\n", wantErr: "want one line"},
		{name: "short", text: valid[:63] + "\n", wantErr: "want one line"},
		{name: "uppercase", text: strings.ToUpper(valid) + "\n", wantErr: "want one line"},
		{name: "not hex", text: "g" + valid[1:] + "\n", wantErr: "want one line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePin(tt.text)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parsePin(%q) error = %v, want one containing %q", tt.text, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parsePin(%q) = %q, %v, want %q, nil", tt.text, got, err, tt.want)
			}
		})
	}
}
