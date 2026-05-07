package buzz_test

import (
	"testing"

	"github.com/egladman/gopherbuzz"
)

func TestParse_ValidProgram(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"empty", ""},
		{"literal", `var x: int = 42;`},
		{"function", `fun add(a: int, b: int) > int { return a + b; }`},
		{"if statement", `if (true) { var x: int = 1; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := buzz.Parse(tc.src)
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error: %v", tc.src, err)
			}
			if prog == nil {
				t.Fatal("Parse returned nil program without error")
			}
		})
	}
}

func TestParse_InvalidSyntax(t *testing.T) {
	cases := []string{
		`fun (`, // incomplete function
		`var x: = ;`, // missing type
	}
	for _, src := range cases {
		_, err := buzz.Parse(src)
		if err == nil {
			t.Errorf("Parse(%q): expected error, got nil", src)
		}
	}
}
