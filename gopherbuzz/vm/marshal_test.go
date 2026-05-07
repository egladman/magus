package vm_test

import (
	"testing"

	"github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
)

func TestMarshalRoundTrip(t *testing.T) {
	prog, err := buzz.Parse(`var x: int = 42;`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	if err != nil {
		t.Fatalf("CompileWith: %v", err)
	}

	data, err := chunk.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Marshal produced empty output")
	}

	chunk2, err := vm.UnmarshalChunk(data)
	if err != nil {
		t.Fatalf("UnmarshalChunk: %v", err)
	}
	if chunk2 == nil {
		t.Fatal("UnmarshalChunk returned nil")
	}
	if len(chunk2.Code) == 0 {
		t.Error("unmarshalled chunk has no instructions")
	}
}

func TestUnmarshalChunk_InvalidData(t *testing.T) {
	_, err := vm.UnmarshalChunk([]byte("not bytecode"))
	if err == nil {
		t.Error("UnmarshalChunk(garbage): expected error, got nil")
	}
}
