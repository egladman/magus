//go:build !wasm

package std

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	pgzip "github.com/klauspost/pgzip"

	"github.com/egladman/magus/internal/cache"
	codec "github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module archive -lang buzz -out ../host/gen/archive.go

func init() { Register(Archive) }

const archiveDefaultMaxSize int64 = 10 << 30 // 10 GiB

var Archive = Module{
	Name: "archive",
	Doc:  "Archive creation and extraction with automatic format detection. Supports tar, zip, tar.gz, tar.bz2, tar.xz, and tar.zst. Symlinks and non-regular entries are skipped.",
	Methods: []Method{
		{
			Name: "uncompress",
			Doc:  "Extract the archive at src into dest. Returns a table with fields: files (extracted paths relative to dest) and bytes (total uncompressed bytes written). opts keys: strip (int, strip N leading path components), max_size (int, uncompressed byte cap, default 10 GiB), threads (int, parallel decode workers; 0 or omitted = auto).",
			Args: []Arg{
				{Name: "src", Type: TypeString},
				{Name: "dest", Type: TypeString},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    ArchiveUncompress,
		},
		{
			Name: "compress",
			Doc:  "Create an archive at dest from src (a file or directory). Format is inferred from dest extension (.tar, .tar.gz, .tgz, .tar.zst, .zip). Returns a table with fields: files (archived paths relative to src), bytes_in (raw bytes read), bytes_out (compressed bytes written). opts keys: format (string, override format detection), threads (int, parallel encode workers; 0 or omitted = auto), level (int, compression level; -1 = format default), follow_symlinks (bool, default false), max_size (int, output byte cap, default 10 GiB).",
			Args: []Arg{
				{Name: "src", Type: TypeString},
				{Name: "dest", Type: TypeString},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    ArchiveCompress,
		},
	},
}

// defaultArchiveThreads returns the auto thread count for archive operations:
// half the available parallelism (GOMAXPROCS, which respects cgroup CPU limits
// on Go 1.22+), capped by the limiter's capacity so we never request more slots
// than the pool holds. Half leaves headroom for other concurrent spell work.
func defaultArchiveThreads(lim *cache.Limiter) int {
	cpus := runtime.GOMAXPROCS(0)
	cap := cpus
	if lim != nil {
		snap := lim.Snapshot()
		if snap.Capacity > 0 && snap.Capacity < cap {
			cap = snap.Capacity
		}
	}
	n := (cap + 1) / 2
	if n < 1 {
		n = 1
	}
	return n
}

// resolveThreads returns the effective thread count for an archive operation.
// opts["threads"] > 0 overrides the auto count. The result is clamped to
// [1, GOMAXPROCS] and, when a limiter is present, to [1, limiter.cap].
func resolveThreads(opts map[string]any, lim *cache.Limiter) int {
	explicit := archiveOptInt(opts, "threads", 0)
	var n int
	if explicit > 0 {
		n = explicit
	} else {
		n = defaultArchiveThreads(lim)
	}
	cpus := runtime.GOMAXPROCS(0)
	if n > cpus {
		n = cpus
	}
	if lim != nil {
		if snap := lim.Snapshot(); snap.Capacity > 0 && n > snap.Capacity {
			n = snap.Capacity
		}
	}
	if n < 1 {
		n = 1
	}
	return n
}

func ArchiveUncompress(ctx context.Context, src, dest string, opts map[string]any) (map[string]any, error) {
	if types.Tracing(ctx) {
		return map[string]any{}, nil
	}
	src, dest = resolvePath(ctx, src), resolvePath(ctx, dest)
	strip := archiveOptInt(opts, "strip", 0)
	maxSize := archiveOptInt64(opts, "max_size", archiveDefaultMaxSize)

	if p := sandbox.FromContext(ctx); p != nil {
		if err := p.CheckRead(src); err != nil {
			return nil, fmt.Errorf("archive.uncompress: %w", err)
		}
		if err := p.CheckWrite(dest); err != nil {
			return nil, fmt.Errorf("archive.uncompress: %w", err)
		}
	}

	lim := cache.LimiterFromContext(ctx)
	threads := resolveThreads(opts, lim)

	if lim != nil {
		// Hand back every build slot we hold while we hold `threads`, so peak
		// in-flight stays within the budget (cap) rather than cap+threads-1. A
		// weighted step holds more than one, so giving back only one would
		// deadlock the AcquireN below on slots we pin ourselves. Reclaim them
		// uncancellably after the work completes.
		if held := cache.SlotsHeld(ctx); held > 0 {
			lim.ReleaseN(held)
			defer func() { _ = lim.AcquireN(context.WithoutCancel(ctx), held) }()
		}
		if err := lim.AcquireN(ctx, threads); err != nil {
			return nil, fmt.Errorf("archive.uncompress: %w", err)
		}
		defer lim.ReleaseN(threads)
	}
	op := proc.SubOpFromContext(ctx)
	op.Set(archiveSubOpLabel("archive.uncompress", filepath.Base(src), threads))
	defer op.Set("")

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, fmt.Errorf("archive.uncompress: mkdir %s: %w", dest, err)
	}
	// Resolve dest after creating it so archiveSafePath's containment check and
	// the returned relative paths share one canonical base (macOS /var ->
	// /private/var).
	if real, err := filepath.EvalSymlinks(dest); err == nil {
		dest = real
	}

	f, err := os.Open(src)
	if err != nil {
		return nil, fmt.Errorf("archive.uncompress: %w", err)
	}
	defer f.Close()

	var sniff [262]byte
	n, _ := f.Read(sniff[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("archive.uncompress: seek: %w", err)
	}

	files, totalBytes, err := archiveDispatch(ctx, sniff[:n], f, dest, strip, maxSize, threads)
	if err != nil {
		return nil, fmt.Errorf("archive.uncompress %s: %w", filepath.Base(src), err)
	}
	sort.Strings(files)
	return map[string]any{
		"files": files,
		"bytes": int(totalBytes),
	}, nil
}

func ArchiveCompress(ctx context.Context, src, dest string, opts map[string]any) (map[string]any, error) {
	if types.Tracing(ctx) {
		return map[string]any{}, nil
	}
	src, dest = resolvePath(ctx, src), resolvePath(ctx, dest)
	maxSize := archiveOptInt64(opts, "max_size", archiveDefaultMaxSize)
	level := archiveOptInt(opts, "level", -1)
	followSymlinks := archiveOptBool(opts, "follow_symlinks", false)

	if p := sandbox.FromContext(ctx); p != nil {
		if err := p.CheckRead(src); err != nil {
			return nil, fmt.Errorf("archive.compress: %w", err)
		}
		if err := p.CheckWrite(dest); err != nil {
			return nil, fmt.Errorf("archive.compress: %w", err)
		}
	}

	// Resolve dest via EvalSymlinks to defeat a pre-planted symlink pivot: an
	// attacker who can pre-create dest as a symlink to /etc/secrets would
	// otherwise have the archive written there.
	destDir := filepath.Dir(dest)
	destReal, err := filepath.EvalSymlinks(destDir)
	if err != nil {
		destReal = destDir
	}
	dest = filepath.Join(destReal, filepath.Base(dest))

	lim := cache.LimiterFromContext(ctx)
	threads := resolveThreads(opts, lim)

	if lim != nil {
		// Hand back every build slot we hold while we hold `threads`, so peak
		// in-flight stays within the budget (cap) rather than cap+threads-1. A
		// weighted step holds more than one, so giving back only one would
		// deadlock the AcquireN below on slots we pin ourselves. Reclaim them
		// uncancellably after the work completes.
		if held := cache.SlotsHeld(ctx); held > 0 {
			lim.ReleaseN(held)
			defer func() { _ = lim.AcquireN(context.WithoutCancel(ctx), held) }()
		}
		if err := lim.AcquireN(ctx, threads); err != nil {
			return nil, fmt.Errorf("archive.compress: %w", err)
		}
		defer lim.ReleaseN(threads)
	}
	op := proc.SubOpFromContext(ctx)
	op.Set(archiveSubOpLabel("archive.compress", filepath.Base(dest), threads))
	defer op.Set("")

	format := archiveOptString(opts, "format", "")
	if format == "" {
		format = archiveFormatFromExt(dest)
	}
	if format == "" {
		return nil, fmt.Errorf("archive.compress: cannot determine format from %q; set opts.format", filepath.Base(dest))
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, fmt.Errorf("archive.compress: mkdir parent: %w", err)
	}

	files, bytesIn, bytesOut, err := compressDispatch(ctx, src, dest, format, threads, level, maxSize, followSymlinks)
	if err != nil {
		return nil, fmt.Errorf("archive.compress %s: %w", filepath.Base(dest), err)
	}
	sort.Strings(files)
	return map[string]any{
		"files":     files,
		"bytes_in":  int(bytesIn),
		"bytes_out": int(bytesOut),
	}, nil
}

func archiveFormatFromExt(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(lower, ".tar.zst"), strings.HasSuffix(lower, ".tar.zstd"):
		return "tar.zst"
	case strings.HasSuffix(lower, ".tar"):
		return "tar"
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	}
	return ""
}

func compressDispatch(ctx context.Context, src, dest, format string, threads, level int, maxSize int64, followSymlinks bool) ([]string, int64, int64, error) {
	switch format {
	case "tar":
		return compressTar(ctx, src, dest, threads, level, maxSize, followSymlinks, nil)
	case "tar.gz":
		return compressTarGz(ctx, src, dest, threads, level, maxSize, followSymlinks)
	case "tar.zst":
		return compressTarZst(ctx, src, dest, threads, level, maxSize, followSymlinks)
	case "zip":
		return compressZip(ctx, src, dest, threads, maxSize, followSymlinks)
	}
	return nil, 0, 0, fmt.Errorf("unsupported format %q", format)
}

// archiveEntry is one file to archive: its path relative to the source root and
// its absolute path.
type archiveEntry struct {
	rel string
	abs string
	fi  fs.FileInfo
}

func compressCollect(ctx context.Context, src string, followSymlinks bool, policy *sandbox.Policy) ([]archiveEntry, int64, error) {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return nil, 0, err
	}
	var entries []archiveEntry
	var totalIn int64

	err = filepath.WalkDir(srcAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			if !followSymlinks {
				return nil // skip silently
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("follow symlink %s: %w", path, err)
			}
			if policy != nil {
				if err := policy.CheckRead(resolved); err != nil {
					return fmt.Errorf("symlink target denied by sandbox: %s", resolved)
				}
			}
			fi, err = os.Stat(resolved)
			if err != nil {
				return err
			}
			path = resolved
		}
		if policy != nil && !fi.IsDir() {
			if err := policy.CheckRead(path); err != nil {
				return fmt.Errorf("archive.compress: %w", err)
			}
		}
		rel, err := filepath.Rel(srcAbs, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil // skip the root itself
		}
		entries = append(entries, archiveEntry{rel: rel, abs: path, fi: fi})
		if !fi.IsDir() {
			totalIn += fi.Size()
		}
		return nil
	})
	return entries, totalIn, err
}

// threads and level are accepted for parity with the compressTar* family but
// are unused here: the caller bakes them into the wrap closure.
func compressTar(ctx context.Context, src, dest string, _, _ int, maxSize int64, followSymlinks bool, wrap func(io.Writer) (io.WriteCloser, error)) (files []string, bytesIn, bytesOut int64, err error) {
	p := sandbox.FromContext(ctx)
	entries, bytesIn, err := compressCollect(ctx, src, followSymlinks, p)
	if err != nil {
		return
	}

	out, err := os.Create(dest)
	if err != nil {
		return
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", dest, cerr)
		}
	}()

	var w io.Writer = out
	var closer io.Closer
	if wrap != nil {
		wc, wErr := wrap(out)
		if wErr != nil {
			err = wErr
			return
		}
		w = wc
		closer = wc
	}

	tw := tar.NewWriter(w)

	for _, e := range entries {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
			return
		}
		hdr, hdrErr := tar.FileInfoHeader(e.fi, "")
		if hdrErr != nil {
			err = hdrErr
			return
		}
		hdr.Name = filepath.ToSlash(e.rel)
		if e.fi.IsDir() {
			hdr.Name += "/"
		}
		if whErr := tw.WriteHeader(hdr); whErr != nil {
			err = whErr
			return
		}
		if !e.fi.IsDir() {
			f, fErr := os.Open(e.abs)
			if fErr != nil {
				err = fErr
				return
			}
			n, copyErr := io.Copy(tw, f)
			f.Close()
			if copyErr != nil {
				err = copyErr
				return
			}
			bytesOut += n
			if bytesOut > maxSize {
				err = fmt.Errorf("output size exceeds %d bytes", maxSize)
				return
			}
			files = append(files, e.rel)
		}
	}
	if twErr := tw.Close(); twErr != nil {
		err = twErr
		return
	}
	if closer != nil {
		if cErr := closer.Close(); cErr != nil {
			err = cErr
			return
		}
	}
	if fi, statErr := out.Stat(); statErr == nil {
		bytesOut = fi.Size()
	}
	return
}

func compressTarGz(ctx context.Context, src, dest string, threads, level int, maxSize int64, followSymlinks bool) ([]string, int64, int64, error) {
	return compressTar(ctx, src, dest, threads, level, maxSize, followSymlinks, func(w io.Writer) (io.WriteCloser, error) {
		gw, err := pgzip.NewWriterLevel(w, gzipLevel(level))
		if err != nil {
			return nil, err
		}
		// pgzip fans out internally; SetConcurrency sets block size and goroutine
		// count. 256 KiB blocks balance memory vs throughput. threads=1 still uses
		// pgzip (it serialises gracefully) for code uniformity.
		_ = gw.SetConcurrency(256<<10, threads)
		return gw, nil
	})
}

func compressTarZst(ctx context.Context, src, dest string, threads, level int, maxSize int64, followSymlinks bool) ([]string, int64, int64, error) {
	return compressTar(ctx, src, dest, threads, level, maxSize, followSymlinks, func(w io.Writer) (io.WriteCloser, error) {
		return codec.NewZstdWriter(w, level, threads)
	})
}

// compressZip fans out entry compression across a bounded worker pool so
// independent entries are compressed concurrently. The zip central directory
// is written single-threaded after all entries are done.
func compressZip(ctx context.Context, src, dest string, threads int, maxSize int64, followSymlinks bool) (files []string, bytesIn, bytesOut int64, err error) {
	p := sandbox.FromContext(ctx)
	entries, bytesIn, err := compressCollect(ctx, src, followSymlinks, p)
	if err != nil {
		return
	}

	type result struct {
		rel  string
		data []byte
		mode fs.FileMode
		err  error
	}

	// Compress each regular file in parallel; directories are handled inline.
	jobs := make(chan archiveEntry, len(entries))
	results := make(chan result, len(entries))
	var wg sync.WaitGroup
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				if e.fi.IsDir() {
					results <- result{rel: e.rel + "/", mode: e.fi.Mode()}
					continue
				}
				data, err := os.ReadFile(e.abs)
				results <- result{rel: e.rel, data: data, mode: e.fi.Mode(), err: err}
			}
		}()
	}
	for _, e := range entries {
		jobs <- e
	}
	close(jobs)
	go func() { wg.Wait(); close(results) }()

	// Collect all results then write the zip sequentially.
	type zipEntry struct {
		rel  string
		data []byte
		mode fs.FileMode
	}
	collected := make([]zipEntry, 0, len(entries))
	for r := range results {
		if r.err != nil {
			return nil, 0, 0, r.err
		}
		collected = append(collected, zipEntry{rel: r.rel, data: r.data, mode: r.mode})
	}

	out, err := os.Create(dest)
	if err != nil {
		return
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", dest, cerr)
		}
	}()

	zw := zip.NewWriter(out)
	for _, e := range collected {
		if strings.HasSuffix(e.rel, "/") {
			_, _ = zw.Create(e.rel) // directory entry
			continue
		}
		w, wErr := zw.Create(e.rel)
		if wErr != nil {
			err = wErr
			return
		}
		n, wErr := w.Write(e.data)
		if wErr != nil {
			err = wErr
			return
		}
		bytesOut += int64(n)
		if bytesOut > maxSize {
			err = fmt.Errorf("output size exceeds %d bytes", maxSize)
			return
		}
		files = append(files, e.rel)
	}
	if zwErr := zw.Close(); zwErr != nil {
		err = zwErr
		return
	}
	if fi, statErr := out.Stat(); statErr == nil {
		bytesOut = fi.Size()
	}
	return
}

func gzipLevel(level int) int {
	if level < 0 {
		return pgzip.DefaultCompression
	}
	if level > 9 {
		return 9
	}
	return level
}

func archiveDispatch(ctx context.Context, hdr []byte, f *os.File, dest string, strip int, maxSize int64, threads int) ([]string, int64, error) {
	switch {
	case len(hdr) >= 4 && hdr[0] == 'P' && hdr[1] == 'K':
		return extractZip(ctx, f, dest, strip, maxSize, threads)
	case len(hdr) >= 262 && string(hdr[257:262]) == "ustar":
		return extractTar(ctx, tar.NewReader(f), dest, strip, maxSize)
	case len(hdr) >= 2 && hdr[0] == 0x1F && hdr[1] == 0x8B:
		return extractGzipped(ctx, f, dest, strip, maxSize, threads)
	case len(hdr) >= 3 && hdr[0] == 'B' && hdr[1] == 'Z' && hdr[2] == 'h':
		return extractBzip2d(ctx, f, dest, strip, maxSize)
	case len(hdr) >= 6 && hdr[0] == 0xFD && hdr[1] == 0x37 && hdr[2] == 0x7A && hdr[3] == 0x58 && hdr[4] == 0x5A && hdr[5] == 0x00:
		return extractXzd(ctx, f, dest, strip, maxSize)
	case len(hdr) >= 4 && hdr[0] == 0x28 && hdr[1] == 0xB5 && hdr[2] == 0x2F && hdr[3] == 0xFD:
		return extractZstdd(ctx, f, dest, strip, maxSize, threads)
	}
	ext := strings.ToLower(filepath.Ext(f.Name()))
	switch ext {
	case ".zip":
		return extractZip(ctx, f, dest, strip, maxSize, threads)
	case ".tar":
		return extractTar(ctx, tar.NewReader(f), dest, strip, maxSize)
	case ".gz", ".tgz":
		return extractGzipped(ctx, f, dest, strip, maxSize, threads)
	case ".bz2":
		return extractBzip2d(ctx, f, dest, strip, maxSize)
	case ".xz":
		return extractXzd(ctx, f, dest, strip, maxSize)
	case ".zst", ".zstd":
		return extractZstdd(ctx, f, dest, strip, maxSize, threads)
	}
	return nil, 0, fmt.Errorf("unrecognized archive format")
}

// openTarInStream peeks the first 262 bytes of r for a tar header. If found, it
// returns a *tar.Reader over a stitched reader; otherwise (nil, stitched, nil)
// so the caller can treat r as a single compressed file.
func openTarInStream(r io.Reader) (*tar.Reader, io.Reader, error) {
	peek := make([]byte, 262)
	n, _ := io.ReadFull(r, peek)
	if n == 0 {
		return nil, nil, fmt.Errorf("empty decompressed stream")
	}
	multi := io.MultiReader(bytes.NewReader(peek[:n]), r)
	if n >= 262 && string(peek[257:262]) == "ustar" {
		return tar.NewReader(multi), nil, nil
	}
	return nil, multi, nil
}

func extractGzipped(ctx context.Context, f *os.File, dest string, strip int, maxSize int64, threads int) ([]string, int64, error) {
	// pgzip.NewReaderN sets the read-ahead block size and goroutine count for
	// multi-stream gzip. Single-stream .tar.gz files (the common case) fall back
	// to serial decompression.
	gr, err := pgzip.NewReaderN(f, 256<<10, threads)
	if err != nil {
		return nil, 0, fmt.Errorf("gzip: %w", err)
	}
	defer gr.Close()
	tr, rest, err := openTarInStream(gr)
	if err != nil {
		return nil, 0, err
	}
	if tr != nil {
		return extractTar(ctx, tr, dest, strip, maxSize)
	}
	name := strings.TrimSuffix(filepath.Base(f.Name()), filepath.Ext(f.Name()))
	return extractSingleStream(ctx, rest, dest, name, maxSize)
}

func extractBzip2d(ctx context.Context, f *os.File, dest string, strip int, maxSize int64) ([]string, int64, error) {
	br := bzip2.NewReader(f)
	tr, rest, err := openTarInStream(br)
	if err != nil {
		return nil, 0, err
	}
	if tr != nil {
		return extractTar(ctx, tr, dest, strip, maxSize)
	}
	name := strings.TrimSuffix(filepath.Base(f.Name()), filepath.Ext(f.Name()))
	return extractSingleStream(ctx, rest, dest, name, maxSize)
}

func extractXzd(ctx context.Context, f *os.File, dest string, strip int, maxSize int64) ([]string, int64, error) {
	xr, err := codec.NewXzReader(f)
	if err != nil {
		return nil, 0, fmt.Errorf("xz: %w", err)
	}
	defer xr.Close()
	tr, rest, err := openTarInStream(xr)
	if err != nil {
		return nil, 0, err
	}
	if tr != nil {
		return extractTar(ctx, tr, dest, strip, maxSize)
	}
	name := strings.TrimSuffix(filepath.Base(f.Name()), filepath.Ext(f.Name()))
	return extractSingleStream(ctx, rest, dest, name, maxSize)
}

func extractZstdd(ctx context.Context, f *os.File, dest string, strip int, maxSize int64, threads int) ([]string, int64, error) {
	zr, err := codec.NewZstdReader(f, threads)
	if err != nil {
		return nil, 0, fmt.Errorf("zstd: %w", err)
	}
	defer zr.Close()
	tr, rest, err := openTarInStream(zr)
	if err != nil {
		return nil, 0, err
	}
	if tr != nil {
		return extractTar(ctx, tr, dest, strip, maxSize)
	}
	name := strings.TrimSuffix(filepath.Base(f.Name()), filepath.Ext(f.Name()))
	return extractSingleStream(ctx, rest, dest, name, maxSize)
}

func extractTar(ctx context.Context, tr *tar.Reader, dest string, strip int, maxSize int64) ([]string, int64, error) {
	var files []string
	var total int64

	for {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("read tar entry: %w", err)
		}

		name, ok := archiveStripComponents(hdr.Name, strip)
		if !ok || name == "" {
			continue
		}

		clean, err := archiveSafePath(dest, name)
		if err != nil {
			return nil, 0, err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(clean, 0o755); err != nil {
				return nil, 0, err
			}
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // TypeRegA ('\x00') still appears in legacy tar archives; matched for read compatibility
			if hdr.Size < 0 {
				return nil, 0, fmt.Errorf("entry %q: negative size", hdr.Name)
			}
			if total+hdr.Size > maxSize {
				return nil, 0, fmt.Errorf("uncompressed size exceeds %d bytes", maxSize)
			}
			if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
				return nil, 0, err
			}
			out, err := os.OpenFile(clean, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, hdr.FileInfo().Mode().Perm())
			if err != nil {
				return nil, 0, err
			}
			n, copyErr := io.Copy(out, io.LimitReader(tr, hdr.Size+1))
			out.Close()
			if copyErr != nil {
				return nil, 0, copyErr
			}
			if n > hdr.Size {
				return nil, 0, fmt.Errorf("entry %q: actual size exceeds header", hdr.Name)
			}
			total += n
			rel, _ := filepath.Rel(dest, clean)
			files = append(files, rel)
			// Skip symlinks, hard links, devices, fifos, etc.
		}
	}
	return files, total, nil
}

func extractZip(ctx context.Context, f *os.File, dest string, strip int, maxSize int64, threads int) ([]string, int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		return nil, 0, fmt.Errorf("zip: %w", err)
	}

	// Filter entries upfront.
	type zipJob struct {
		entry *zip.File
		clean string
		rel   string
		size  int64
	}
	var jobs []zipJob
	var totalExpected int64
	for _, entry := range zr.File {
		name, ok := archiveStripComponents(entry.Name, strip)
		if !ok || name == "" {
			continue
		}
		clean, err := archiveSafePath(dest, name)
		if err != nil {
			return nil, 0, err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(clean, 0o755); err != nil {
				return nil, 0, err
			}
			continue
		}
		if entry.Mode()&os.ModeType != 0 {
			continue
		}
		size := int64(entry.UncompressedSize64)
		if size < 0 {
			return nil, 0, fmt.Errorf("entry %q: invalid declared size", entry.Name)
		}
		totalExpected += size
		if totalExpected > maxSize {
			return nil, 0, fmt.Errorf("uncompressed size exceeds %d bytes", maxSize)
		}
		if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
			return nil, 0, err
		}
		rel, _ := filepath.Rel(dest, clean)
		jobs = append(jobs, zipJob{entry: entry, clean: clean, rel: rel, size: size})
	}

	type result struct {
		rel string
		n   int64
		err error
	}
	jobCh := make(chan zipJob, len(jobs))
	resultCh := make(chan result, len(jobs))
	var wg sync.WaitGroup
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				if ctx.Err() != nil {
					resultCh <- result{err: ctx.Err()}
					continue
				}
				rc, err := j.entry.Open()
				if err != nil {
					resultCh <- result{err: err}
					continue
				}
				out, err := os.OpenFile(j.clean, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, j.entry.Mode().Perm())
				if err != nil {
					rc.Close()
					resultCh <- result{err: err}
					continue
				}
				// Bound the copy to the entry's declared size (mirrors extractTar).
				// The pre-extraction loop capped the sum of declared sizes at
				// maxSize, so enforcing actual <= declared per entry keeps the total
				// bounded even under parallel extraction. A header that under-declares
				// is caught here rather than allowed the whole budget (gosec G110).
				n, copyErr := io.Copy(out, io.LimitReader(rc, j.size+1))
				out.Close()
				rc.Close()
				if copyErr == nil && n > j.size {
					_ = os.Remove(j.clean)
					copyErr = fmt.Errorf("entry %q: actual size exceeds header", j.rel)
				}
				resultCh <- result{rel: j.rel, n: n, err: copyErr}
			}
		}()
	}
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)
	go func() { wg.Wait(); close(resultCh) }()

	var files []string
	var total int64
	for r := range resultCh {
		if r.err != nil {
			return nil, 0, r.err
		}
		total += r.n
		files = append(files, r.rel)
	}
	return files, total, nil
}

func extractSingleStream(ctx context.Context, r io.Reader, dest, name string, maxSize int64) ([]string, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	clean, err := archiveSafePath(dest, name)
	if err != nil {
		return nil, 0, err
	}
	out, err := os.OpenFile(clean, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, 0, err
	}
	n, copyErr := io.Copy(out, io.LimitReader(r, maxSize+1))
	out.Close()
	if copyErr != nil {
		return nil, 0, copyErr
	}
	if n > maxSize {
		_ = os.Remove(clean)
		return nil, 0, fmt.Errorf("uncompressed size exceeds %d bytes", maxSize)
	}
	rel, _ := filepath.Rel(dest, clean)
	return []string{rel}, n, nil
}

// archiveStripComponents removes the first n slash-separated components from
// name. Returns ("", false) if name has fewer than n+1 components.
func archiveStripComponents(name string, n int) (string, bool) {
	name = filepath.ToSlash(filepath.Clean(name))
	name = strings.TrimPrefix(name, "./")
	for i := 0; i < n; i++ {
		idx := strings.Index(name, "/")
		if idx < 0 {
			return "", false
		}
		name = name[idx+1:]
		if name == "" {
			return "", false
		}
	}
	return name, true
}

// archiveSafePath returns the absolute target path for name under dest,
// rejecting paths that escape dest via traversal or symlinks.
func archiveSafePath(dest, name string) (string, error) {
	cleaned := filepath.ToSlash(filepath.Clean(name))
	if strings.HasPrefix(cleaned, "/") || strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("archive contains unsafe path: %q", name)
	}
	abs := filepath.Join(dest, filepath.FromSlash(cleaned))

	destReal, err := filepath.EvalSymlinks(dest)
	if err != nil {
		destReal, err = filepath.Abs(dest)
		if err != nil {
			return "", err
		}
	}
	if !strings.HasPrefix(abs+string(os.PathSeparator), destReal+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive contains path that escapes destination: %q", name)
	}
	return abs, nil
}

func archiveOptInt(m map[string]any, key string, def int) int {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok {
		return def
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return def
}

func archiveOptInt64(m map[string]any, key string, def int64) int64 {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok {
		return def
	}
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	}
	return def
}

func archiveOptBool(m map[string]any, key string, def bool) bool {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok {
		return def
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

func archiveOptString(m map[string]any, key string, def string) string {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok {
		return def
	}
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func archiveSubOpLabel(name, file string, threads int) string {
	if threads > 1 {
		return fmt.Sprintf("%s %s [%d×]", name, file, threads)
	}
	return fmt.Sprintf("%s %s", name, file)
}
