package literal

import (
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/gopherbuzz/token"
)

const maxHashes = 255

// Raw scans r"..." and r#"..."# with up to 255 '#'. It is not upstream Buzz.
// The closing run holds as many '#' as the opening one, so the text may hold '"'
// and shorter runs. Newlines are kept and no indentation is stripped. An r that
// opens nothing is declined, so identifiers that start with r are untouched.
var Raw = token.LiteralScanner{Name: "raw", Prefix: 'r', Scan: scanRaw}

func scanRaw(src string) (lit string, n int, err error) {
	hashes := 0
	for 1+hashes < len(src) && src[1+hashes] == '#' {
		hashes++
	}
	open := 1 + hashes
	if open >= len(src) || src[open] != '"' {
		return "", 0, nil
	}
	if hashes > maxHashes {
		return "", 0, fmt.Errorf("raw string opened with %d #, the limit is %d", hashes, maxHashes)
	}
	closer := `"` + strings.Repeat("#", hashes)
	body := src[open+1:]
	end := strings.Index(body, closer)
	if end < 0 {
		return "", 0, errors.New("unterminated raw string")
	}
	return body[:end], open + 1 + end + len(closer), nil
}
