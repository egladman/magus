package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/egladman/magus/libs/proofread/internal/xdg"
)

const (
	ewtRelease = "r2.18"
	ewtBaseURL = "https://raw.githubusercontent.com/UniversalDependencies/UD_English-EWT/" + ewtRelease + "/"
)

// The digests of the r2.18 files. A tag can be moved; a digest cannot.
var ewtSHA256 = map[string]string{
	"en_ewt-ud-train.conllu": "d68e06122a702464c613076523d56740f047e5bbe89dd90ec32737e04d952143",
	"en_ewt-ud-dev.conllu":   "39239e0a60db3ae68f4b7036189f11b6692741d10ff8240dd91f74f2760d90f8",
	"en_ewt-ud-test.conllu":  "fa024f43dc5da3c5ac02563bc9bd0e974f46cbb1560823976a8f342a37dc494a",
}

// fetch returns the bytes of one EWT file, from the cache when its digest
// matches and from the network otherwise.
func fetch(name string) ([]byte, error) {
	want, ok := ewtSHA256[name]
	if !ok {
		return nil, fmt.Errorf("no pinned digest for %s", name)
	}

	cache, err := xdg.CacheDir()
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(cache, "magus", "postag", "UD_English-EWT-"+ewtRelease)

	path := filepath.Join(dir, name)
	if b, err := os.ReadFile(path); err == nil && digest(b) == want {
		return b, nil
	}

	client := &http.Client{Timeout: 5 * time.Minute}

	resp, err := client.Get(ewtBaseURL + name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: %s", ewtBaseURL+name, resp.Status)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", ewtBaseURL+name, err)
	}

	if got := digest(b); got != want {
		return nil, fmt.Errorf("%s: sha256 %s, want %s", name, got, want)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	// The digest already matched, so the bytes are good whether or not the
	// cache keeps them. A per-run name keeps two trainers from sharing a temp
	// file.
	tmp, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return nil, err
	}

	_, writeErr := tmp.Write(b)
	chmodErr := tmp.Chmod(0o644)
	closeErr := tmp.Close()

	if err := errors.Join(writeErr, chmodErr, closeErr); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
	}

	return b, nil
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
