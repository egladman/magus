package std

import (
	"context"
	"encoding/hex"
	"fmt"
)

//go:generate go run ../cmd/magus-utils bindings -module hex -lang buzz -out ../internal/interp/bindings/gen/hex.go

func init() { Register(Hex) }

// Hex is the "encoding/hex" host module. Buzz strings carry a `.hex()` method
// already; this is the decode half, which the language has no form of, plus the
// encode half so a caller reaches both through one import rather than switching
// between a string method and a module.
var Hex = Module{
	Name: "hex",
	Path: "encoding/hex",
	Doc:  "Hex text codec.",
	Methods: []Method{
		{
			Name:    "encode",
			Doc:     "Encode data as lowercase hex.",
			Args:    []Arg{{Name: "data", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    HexEncode,
		},
		{
			Name:    "decode",
			Doc:     "Decode a hex string; errors on malformed input.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    HexDecode,
		},
	},
}

// HexEncode encodes data as lowercase hex.
func HexEncode(_ context.Context, data string) (string, error) {
	return hex.EncodeToString([]byte(data)), nil
}

// HexDecode decodes a hex string.
func HexDecode(_ context.Context, s string) (string, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("hex.decode: %w", err)
	}
	return string(b), nil
}
