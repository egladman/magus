package parsecache

import (
	"sync"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/egladman/magus/libs/gopherbuzz/x/literal"
)

// scanner is built once because parse caches key on the Scanner's pointer: two
// scanners with the same literals would each lex every source.
var scanner = sync.OnceValues(func() (*token.Scanner, error) {
	return token.NewScanner(literal.Raw)
})

// Session returns the options every Buzz session magus creates passes: the shared
// parse cache and the scanner that adds raw strings.
func Session() ([]buzz.Option, error) {
	sc, err := scanner()
	if err != nil {
		return nil, err
	}
	return []buzz.Option{buzz.WithParseCache(shared), buzz.WithScanner(sc)}, nil
}

// Parser returns the parser every magus parse outside a session uses.
func Parser() (buzz.Parser, error) {
	sc, err := scanner()
	if err != nil {
		return buzz.Parser{}, err
	}
	return buzz.Parser{Scanner: sc, Cache: shared}, nil
}

// Tokenize lexes src with the same scanner Session and Parser use, bypassing the cache.
func Tokenize(src string) ([]token.Token, error) {
	sc, err := scanner()
	if err != nil {
		return nil, err
	}
	return sc.Tokenize(src)
}
