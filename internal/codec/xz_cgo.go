//go:build cgo

package codec

// xz decompress via liblzma (cgo, pkg-config: liblzma).
// Requires liblzma >= 5.0 (LZMA_CONCATENATED). Cross-compile targets must have liblzma installed.

/*
#cgo pkg-config: liblzma
#include <lzma.h>
#include <stdlib.h>
#include <string.h>

// mg_lzma_alloc allocates and zero-initialises an lzma_stream on the C heap.
// (lzma_stream contains internal C pointers; it must NOT live in Go memory.)
static lzma_stream* mg_lzma_alloc(void) {
    return (lzma_stream*)calloc(1, sizeof(lzma_stream));
}

// mg_lzma_init_decoder initialises s for xz / lzma2 stream decoding.
// Returns 0 (LZMA_OK) on success; non-zero on error.
static int mg_lzma_init_decoder(lzma_stream* s) {
    return (int)lzma_stream_decoder(s, UINT64_MAX, LZMA_CONCATENATED);
}

// mg_lzma_decode runs one step of decompression.
//
//   - Sets *consumed = bytes consumed from in.
//   - Sets *written  = bytes written to out.
//   - Returns the lzma_ret code:
//       LZMA_OK (0)          – more input / output needed
//       LZMA_STREAM_END (1)  – decoding complete
//       anything else        – error
static int mg_lzma_decode(
    lzma_stream*  s,
    void*         out, size_t outCap, size_t* written,
    const void*   in,  size_t inLen,  size_t* consumed,
    int           finish)
{
    s->next_in   = (const uint8_t*)in;
    s->avail_in  = inLen;
    s->next_out  = (uint8_t*)out;
    s->avail_out = outCap;
    lzma_ret r = lzma_code(s, finish ? LZMA_FINISH : LZMA_RUN);
    *consumed = inLen  - s->avail_in;
    *written  = outCap - s->avail_out;
    return (int)r;
}

// mg_lzma_free tears down the stream and frees its C-heap allocation.
static void mg_lzma_free(lzma_stream* s) {
    lzma_end(s);
    free(s);
}
*/
import "C"

import (
	"fmt"
	"io"
	"unsafe"
)

const (
	xzInBufSize  = 65536  // 64 KiB – input read buffer
	xzOutBufSize = 131072 // 128 KiB – decompressed staging buffer
)

// xzCGOReader wraps a C lzma_stream to implement io.ReadCloser.
type xzCGOReader struct {
	r         io.Reader
	strm      *C.lzma_stream // C-heap allocated; never touched from Go
	inBuf     []byte
	inStart   int
	inEnd     int
	outBuf    []byte
	outStart  int
	outEnd    int
	rEOF      bool // underlying reader returned io.EOF
	streamEnd bool // liblzma signalled LZMA_STREAM_END
	closed    bool
}

// newXzReader returns a streaming xz decompressor reading from r.
func newXzReader(r io.Reader) (io.ReadCloser, error) {
	strm := C.mg_lzma_alloc()
	if strm == nil {
		return nil, fmt.Errorf("liblzma: allocation failed")
	}
	if rc := C.mg_lzma_init_decoder(strm); rc != 0 {
		C.mg_lzma_free(strm)
		return nil, fmt.Errorf("liblzma: init_decoder failed (code %d)", int(rc))
	}
	return &xzCGOReader{
		r:      r,
		strm:   strm,
		inBuf:  make([]byte, xzInBufSize),
		outBuf: make([]byte, xzOutBufSize),
	}, nil
}

const (
	lzmaOK        = 0 // LZMA_OK
	lzmaStreamEnd = 1 // LZMA_STREAM_END
)

func (x *xzCGOReader) Read(p []byte) (int, error) {
	if x.closed {
		return 0, fmt.Errorf("liblzma: read on closed reader")
	}
	for {
		if x.outStart < x.outEnd {
			n := copy(p, x.outBuf[x.outStart:x.outEnd])
			x.outStart += n
			return n, nil
		}
		x.outStart = 0
		x.outEnd = 0

		if x.streamEnd {
			return 0, io.EOF
		}

		if x.inStart >= x.inEnd && !x.rEOF {
			n, err := x.r.Read(x.inBuf)
			if n > 0 {
				x.inStart = 0
				x.inEnd = n
			}
			if err == io.EOF {
				x.rEOF = true
			} else if err != nil {
				return 0, err
			}
		}

		// liblzma with LZMA_CONCATENATED only returns LZMA_STREAM_END when LZMA_FINISH is passed.
		finish := C.int(0)
		if x.rEOF && x.inStart >= x.inEnd {
			finish = C.int(1)
		}

		srcLen := x.inEnd - x.inStart
		var srcPtr unsafe.Pointer
		if srcLen > 0 {
			srcPtr = unsafe.Pointer(&x.inBuf[x.inStart])
		}
		var consumed, written C.size_t
		rc := C.mg_lzma_decode(
			x.strm,
			unsafe.Pointer(&x.outBuf[0]), C.size_t(len(x.outBuf)), &written,
			srcPtr, C.size_t(srcLen), &consumed,
			finish,
		)
		x.inStart += int(consumed)
		x.outEnd = int(written)

		switch int(rc) {
		case lzmaOK:
		case lzmaStreamEnd:
			x.streamEnd = true
		default:
			return 0, fmt.Errorf("liblzma: decode error (code %d)", int(rc))
		}
	}
}

func (x *xzCGOReader) Close() error {
	if x.closed {
		return nil
	}
	x.closed = true
	C.mg_lzma_free(x.strm)
	x.strm = nil
	return nil
}
