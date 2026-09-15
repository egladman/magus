package textindex

import (
	"io"
	"os"
	"sync"
)

// mmapFloor is the size below which a copying read beats a mapping.
//
// A mapping costs a syscall plus a page fault per page touched; a small file is one
// read() into an already-warm buffer. The floor is a guess until BenchmarkTextIndexBuildFromDisk
// says otherwise, which is why it is one named constant rather than a condition inline.
const mmapFloor = 16 << 10

// Reader serves file bytes to Build and Scan, mapping rather than copying where that is
// cheaper.
//
// A scan reads every searched file once and keeps none of them, which is the shape
// mmap exists for: the kernel already holds these pages in its page cache, and a copying
// read asks it to duplicate every one of them into the Go heap.
//
// optimization: map the file instead of copying it into the heap.
//
//	measured: BenchmarkTextIndexBuildFromDisk{Mapped,Copied} at 200x64KB: B/op 129.3MB ->
//	          114.6MB (-11%, exactly the bytes read), ns/op unchanged within noise (n=20).
//	trade-off: the returned bytes are the mapping and die at Close, so no consumer may
//	          retain them; and the win is memory only. Build is dominated by the trigram
//	          loop, not the read, so do not expect time back here. The scan path, which
//	          is read-and-match with no index, is where the time case still has to be
//	          made separately.
//	assumes:  unix for the mapping; every other platform falls back to a copying read.
//
// Mappings stay live until Close, because the bytes handed out ARE the mapping. Callers
// must not retain them past Close; every consumer in this package copies what it keeps
// (Match.Text is a string), so the contract holds by construction here.
type Reader struct {
	mu   sync.Mutex
	maps [][]byte
}

// NewReader returns a Reader whose mappings the caller closes.
func NewReader() *Reader { return &Reader{} }

// Read returns path's bytes, mapped when that is worthwhile and copied otherwise.
//
// Every mmap failure falls back to a copying read rather than surfacing: a file magus
// cannot map is still a file it can search, and a text search that failed because of a
// memory-mapping detail would be the "unknown" answer this package exists to never give.
func (r *Reader) Read(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return nil, nil
	}
	// A mapping is indexed by int, so a file larger than the address space this build can
	// index is read the ordinary way rather than truncated.
	if !mmapSupported || size < mmapFloor || int64(int(size)) != size {
		return io.ReadAll(f)
	}
	b, err := mapFile(f, int(size))
	if err != nil {
		return io.ReadAll(f)
	}
	r.mu.Lock()
	r.maps = append(r.maps, b)
	r.mu.Unlock()
	return b, nil
}

// Close releases every mapping. Bytes returned by Read are invalid afterwards.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for _, b := range r.maps {
		if err := unmapFile(b); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	r.maps = nil
	return firstErr
}
