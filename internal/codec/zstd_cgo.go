//go:build cgo

package codec

// zstd compress/decompress via libzstd (cgo, pkg-config: libzstd).
// Requires libzstd >= 1.4.0 (ZSTD_compressStream2); >= 1.5.0 for ZSTD_c_nbWorkers.

/*
#cgo pkg-config: libzstd
#include <zstd.h>
#include <stdlib.h>

// mg_zstd_compress_step runs one step of streaming compression.
//
// Returns bytes written to dst. Sets *srcConsumed to bytes consumed from src.
// Sets *done=1 when the frame is fully flushed (only meaningful when flush=1).
// Sets *errFlag=1 on any ZSTD error.
static size_t mg_zstd_compress_step(
    ZSTD_CCtx*  cctx,
    void*       dst,  size_t dstCap,
    const void* src,  size_t srcLen,
    int         flush,
    size_t*     srcConsumed,
    int*        done,
    int*        errFlag)
{
    *errFlag = 0;
    *done    = 0;
    ZSTD_inBuffer  in  = { src, srcLen, 0 };
    ZSTD_outBuffer out = { dst, dstCap, 0 };
    size_t hint = ZSTD_compressStream2(cctx, &out, &in,
        flush ? ZSTD_e_end : ZSTD_e_continue);
    *srcConsumed = in.pos;
    if (ZSTD_isError(hint)) { *errFlag = 1; return 0; }
    if (flush && hint == 0)  *done = 1;
    return out.pos;
}

// mg_zstd_decompress_step runs one step of streaming decompression.
//
// Returns bytes written to dst. Sets *srcConsumed to bytes consumed from src.
// Sets *errFlag=1 on any ZSTD error.
static size_t mg_zstd_decompress_step(
    ZSTD_DCtx*  dctx,
    void*       dst,  size_t dstCap,
    const void* src,  size_t srcLen,
    size_t*     srcConsumed,
    int*        errFlag)
{
    *errFlag = 0;
    ZSTD_inBuffer  in  = { src, srcLen, 0 };
    ZSTD_outBuffer out = { dst, dstCap, 0 };
    size_t hint = ZSTD_decompressStream(dctx, &out, &in);
    *srcConsumed = in.pos;
    if (ZSTD_isError(hint)) { *errFlag = 1; return 0; }
    return out.pos;
}
*/
import "C"

import (
	"fmt"
	"io"
	"unsafe"
)

var (
	// zstdInSize is the recommended input chunk size for the streaming compressor.
	zstdInSize = int(C.ZSTD_CStreamInSize())
	// zstdOutSize is the maximum output bytes produced by one compress step.
	zstdOutSize = int(C.ZSTD_CStreamOutSize())
)

// zstdCGOWriter wraps a ZSTD_CCtx to implement io.WriteCloser.
type zstdCGOWriter struct {
	w      io.Writer
	cctx   *C.ZSTD_CCtx
	outBuf []byte // staging buffer for compressed output (len = zstdOutSize)
}

// newZstdWriter returns a streaming zstd compressor that writes to w.
// level is the user-specified compression level (-1 = default, 1-19).
// threads controls encoder concurrency (0 = single-threaded).
func newZstdWriter(w io.Writer, level, threads int) (io.WriteCloser, error) {
	cctx := C.ZSTD_createCCtx()
	if cctx == nil {
		return nil, fmt.Errorf("zstd: ZSTD_createCCtx failed")
	}

	clevel := C.int(zstdCGOLevel(level))
	C.ZSTD_CCtx_setParameter(cctx, C.ZSTD_c_compressionLevel, clevel)
	if threads > 0 {
		// ZSTD_c_nbWorkers may not be supported if libzstd was built without
		// multithreading. The error is safe to ignore — compression works
		// single-threaded in that case.
		C.ZSTD_CCtx_setParameter(cctx, C.ZSTD_c_nbWorkers, C.int(threads))
	}

	return &zstdCGOWriter{
		w:      w,
		cctx:   cctx,
		outBuf: make([]byte, zstdOutSize),
	}, nil
}

func (z *zstdCGOWriter) Write(p []byte) (int, error) {
	if z.cctx == nil {
		return 0, fmt.Errorf("zstd: write on closed writer")
	}
	total := 0
	for len(p) > 0 {
		var consumed C.size_t
		var done, errFlag C.int
		written := C.mg_zstd_compress_step(
			z.cctx,
			unsafe.Pointer(&z.outBuf[0]), C.size_t(len(z.outBuf)),
			unsafe.Pointer(&p[0]), C.size_t(len(p)),
			0, // ZSTD_e_continue
			&consumed, &done, &errFlag,
		)
		if errFlag != 0 {
			return total, fmt.Errorf("zstd: compress error")
		}
		if n := int(written); n > 0 {
			if _, err := z.w.Write(z.outBuf[:n]); err != nil { //nolint:gocritic // uncheckedInlineErr false positive: err is checked; gocritic mis-maps positions in this cgo file
				return total, err
			}
		}
		n := int(consumed)
		p = p[n:]
		total += n
		if consumed == 0 && written == 0 {
			// No forward progress on non-empty input. Returning nil here would
			// be a silent short write (total < original len), violating the
			// io.Writer contract; surface it as an error instead.
			return total, fmt.Errorf("zstd: compressor made no progress with %d bytes remaining", len(p))
		}
	}
	return total, nil
}

func (z *zstdCGOWriter) Close() error {
	if z.cctx == nil {
		return nil
	}
	defer func() {
		C.ZSTD_freeCCtx(z.cctx)
		z.cctx = nil
	}()
	// Flush the frame: call with ZSTD_e_end until hint==0.
	for {
		var consumed C.size_t
		var done, errFlag C.int
		written := C.mg_zstd_compress_step(
			z.cctx,
			unsafe.Pointer(&z.outBuf[0]), C.size_t(len(z.outBuf)),
			nil, 0, // empty input
			1, // ZSTD_e_end
			&consumed, &done, &errFlag,
		)
		if errFlag != 0 {
			return fmt.Errorf("zstd: flush error")
		}
		if n := int(written); n > 0 {
			if _, err := z.w.Write(z.outBuf[:n]); err != nil { //nolint:gocritic // uncheckedInlineErr false positive: err is checked; gocritic mis-maps positions in this cgo file
				return err
			}
		}
		if done != 0 {
			return nil
		}
	}
}

// zstdCGOReader wraps a ZSTD_DCtx to implement io.ReadCloser.
type zstdCGOReader struct {
	r        io.Reader
	dctx     *C.ZSTD_DCtx
	inBuf    []byte // buffered compressed data read from r
	inStart  int
	inEnd    int
	outBuf   []byte // decompressed staging buffer
	outStart int
	outEnd   int
	eof      bool // underlying reader returned io.EOF
	closed   bool
}

// newZstdReader returns a streaming zstd decompressor reading from r.
// threads controls decoder concurrency (0 = single-threaded).
func newZstdReader(r io.Reader, threads int) (io.ReadCloser, error) {
	dctx := C.ZSTD_createDCtx()
	if dctx == nil {
		return nil, fmt.Errorf("zstd: ZSTD_createDCtx failed")
	}
	if threads > 0 {
		C.ZSTD_DCtx_setParameter(dctx, C.ZSTD_d_windowLogMax, 31) // allow large frames
	}
	return &zstdCGOReader{
		r:      r,
		dctx:   dctx,
		inBuf:  make([]byte, zstdInSize),
		outBuf: make([]byte, zstdOutSize),
	}, nil
}

func (z *zstdCGOReader) Read(p []byte) (int, error) {
	if z.closed {
		return 0, fmt.Errorf("zstd: read on closed reader")
	}
	for {
		if z.outStart < z.outEnd {
			n := copy(p, z.outBuf[z.outStart:z.outEnd])
			z.outStart += n
			return n, nil
		}
		z.outStart = 0
		z.outEnd = 0

		if z.eof && z.inStart >= z.inEnd {
			return 0, io.EOF
		}

		if z.inStart >= z.inEnd {
			n, err := z.r.Read(z.inBuf)
			if n > 0 {
				z.inStart = 0
				z.inEnd = n
			}
			if err == io.EOF {
				z.eof = true
				if n == 0 {
					return 0, io.EOF
				}
			} else if err != nil {
				return 0, err
			}
		}

		if z.inStart >= z.inEnd {
			continue
		}
		var consumed C.size_t
		var errFlag C.int
		written := C.mg_zstd_decompress_step(
			z.dctx,
			unsafe.Pointer(&z.outBuf[0]), C.size_t(len(z.outBuf)),
			unsafe.Pointer(&z.inBuf[z.inStart]), C.size_t(z.inEnd-z.inStart),
			&consumed, &errFlag,
		)
		if errFlag != 0 {
			return 0, fmt.Errorf("zstd: decompression error")
		}
		z.inStart += int(consumed)
		z.outEnd = int(written)
	}
}

func (z *zstdCGOReader) Close() error {
	if z.closed {
		return nil
	}
	z.closed = true
	C.ZSTD_freeDCtx(z.dctx)
	z.dctx = nil
	return nil
}

// zstdCGOLevel maps a user level int to a libzstd compression level (1–22).
func zstdCGOLevel(level int) int {
	switch {
	case level < 0:
		return 3 // ZSTD_CLEVEL_DEFAULT
	case level == 0:
		return 1
	case level > 19:
		return 19
	default:
		return level
	}
}
