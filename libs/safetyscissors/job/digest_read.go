package job

import "io"

// maxDigestBytes bounds one release digest, because the hash is computed while the store
// holds its mutex; a path over the cap records unreadable, which is what it is from the
// reader's side.
const maxDigestBytes = 32 << 20

// readRegular reads the regular file at path, or names the marker Digest answers for it
// instead. The file it judges is the one it opened, never the path again, so a path
// swapped for a fifo or a larger file between a check and the read is never read past
// the cap. The open does not block on a fifo with no writer, which would wedge the store's
// mutex.
func readRegular(path string) ([]byte, string) {
	fh, err := openNonBlocking(path)
	if err != nil {
		return nil, DigestUnreadable
	}
	defer fh.Close()
	info, err := fh.Stat()
	switch {
	case err != nil:
		return nil, DigestUnreadable
	case info.IsDir():
		return nil, DigestDir
	case !info.Mode().IsRegular(), info.Size() > maxDigestBytes:
		return nil, DigestUnreadable
	}
	body, err := io.ReadAll(io.LimitReader(fh, maxDigestBytes+1))
	if err != nil || len(body) > maxDigestBytes {
		return nil, DigestUnreadable
	}
	return body, ""
}
