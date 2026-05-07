package main

import (
	"bytes"
	"testing"
)

func TestTailLines(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		n     int
		want  []byte
	}{
		{
			name:  "n=0 returns all",
			input: []byte("a\nb\nc\n"),
			n:     0,
			want:  []byte("a\nb\nc\n"),
		},
		{
			name:  "n greater than line count returns all",
			input: []byte("a\nb\nc\n"),
			n:     10,
			want:  []byte("a\nb\nc\n"),
		},
		{
			name:  "n equals line count returns all",
			input: []byte("a\nb\nc\n"),
			n:     3,
			want:  []byte("a\nb\nc\n"),
		},
		{
			name:  "n less than line count returns last n",
			input: []byte("a\nb\nc\nd\ne\n"),
			n:     3,
			want:  []byte("c\nd\ne\n"),
		},
		{
			name:  "n=1 returns last line",
			input: []byte("a\nb\nc\n"),
			n:     1,
			want:  []byte("c\n"),
		},
		{
			name:  "empty input",
			input: []byte{},
			n:     5,
			want:  []byte{},
		},
		{
			name:  "no trailing newline",
			input: []byte("a\nb\nc"),
			n:     2,
			want:  []byte("b\nc"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tailLines(tc.input, tc.n)
			if !bytes.Equal(got, tc.want) {
				t.Errorf("tailLines(%q, %d) = %q, want %q", tc.input, tc.n, got, tc.want)
			}
		})
	}
}
