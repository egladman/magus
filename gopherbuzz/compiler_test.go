package buzz_test

import (
	"testing"

	"github.com/egladman/gopherbuzz"
)

func TestCompileWith_SimpleFunction(t *testing.T) {
	prog, err := buzz.Parse(`fun add(a: int, b: int) > int { return a + b; }`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	if err != nil {
		t.Fatalf("CompileWith: %v", err)
	}
	if chunk == nil {
		t.Fatal("CompileWith returned nil chunk")
	}
	if len(chunk.Code) == 0 {
		t.Error("compiled chunk has no instructions")
	}
}

func TestCompileWith_EmptyProgram(t *testing.T) {
	prog, err := buzz.Parse("")
	if err != nil {
		t.Fatalf("Parse empty: %v", err)
	}
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	if err != nil {
		t.Fatalf("CompileWith empty: %v", err)
	}
	if chunk == nil {
		t.Fatal("CompileWith returned nil chunk for empty program")
	}
}
