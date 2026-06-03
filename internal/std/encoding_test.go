package std

import (
	"context"
	"testing"
)

func TestEncodingBase64RoundTrip(t *testing.T) {
	ctx := context.Background()
	const in = "magus extra: \x00\x01\xff bytes"
	enc, err := EncodingBase64Encode(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if want := "bWFndXMgZXh0cmE6IAAB/yBieXRlcw=="; enc != want {
		t.Fatalf("base64_encode = %q, want %q", enc, want)
	}
	back, err := EncodingBase64Decode(ctx, enc)
	if err != nil {
		t.Fatal(err)
	}
	if back != in {
		t.Fatalf("base64 round-trip = %q, want %q", back, in)
	}
}

func TestEncodingBase64URLDiffersFromStd(t *testing.T) {
	ctx := context.Background()
	// 0xFB,0xFF encodes to "+/" in std base64 and "-_" in URL-safe base64.
	in := string([]byte{0xfb, 0xff})
	std, _ := EncodingBase64Encode(ctx, in)
	url, _ := EncodingBase64URLEncode(ctx, in)
	if std == url {
		t.Fatalf("expected std and url base64 to differ for %x, both = %q", in, std)
	}
	back, err := EncodingBase64URLDecode(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if back != in {
		t.Fatalf("base64url round-trip = %q, want %q", back, in)
	}
}

func TestEncodingHexRoundTrip(t *testing.T) {
	ctx := context.Background()
	enc, err := EncodingHexEncode(ctx, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if want := "616263"; enc != want {
		t.Fatalf("hex_encode = %q, want %q", enc, want)
	}
	back, err := EncodingHexDecode(ctx, enc)
	if err != nil {
		t.Fatal(err)
	}
	if back != "abc" {
		t.Fatalf("hex round-trip = %q, want %q", back, "abc")
	}
}

func TestEncodingURLRoundTrip(t *testing.T) {
	ctx := context.Background()
	const in = "a b&c=d/e"
	enc, err := EncodingURLEncode(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if want := "a+b%26c%3Dd%2Fe"; enc != want {
		t.Fatalf("url_encode = %q, want %q", enc, want)
	}
	back, err := EncodingURLDecode(ctx, enc)
	if err != nil {
		t.Fatal(err)
	}
	if back != in {
		t.Fatalf("url round-trip = %q, want %q", back, in)
	}
}

func TestEncodingDecodeErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := EncodingBase64Decode(ctx, "not base64!!!"); err == nil {
		t.Error("base64_decode of garbage should error")
	}
	if _, err := EncodingHexDecode(ctx, "zz"); err == nil {
		t.Error("hex_decode of garbage should error")
	}
	if _, err := EncodingURLDecode(ctx, "%zz"); err == nil {
		t.Error("url_decode of a malformed escape should error")
	}
}
