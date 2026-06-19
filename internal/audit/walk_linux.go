package audit

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// dirEntKind tells walkDir what to do with a directory entry.
type dirEntKind uint8

const (
	_ dirEntKind = iota // not a file/dir we care about
	dentDir
	dentRegular
)

// readEntsBufPool reuses the 8 KiB getdents64 buffer across walks.
// One buffer per goroutine; walks are not concurrent.
var readEntsBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 8192)
		return &b
	},
}

// readDirEnts opens dirPath, issues getdents64 in a pooled 8 KiB buffer, and invokes fn
// for each entry with its name (aliasing pool memory; callers must not retain) and kind.
// DT_UNKNOWN entries are classified via a fallback lstat (some filesystems report
// DT_UNKNOWN for every entry). Requires Linux ≥ 2.6.4.
func readDirEnts(dirPath string, fn func(name []byte, kind dirEntKind)) error {
	fd, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		if err == unix.ENOENT {
			return nil
		}
		return err
	}
	defer unix.Close(fd)

	bufp := readEntsBufPool.Get().(*[]byte) //nolint:forcetypeassert // readEntsBufPool only ever holds *[]byte
	defer readEntsBufPool.Put(bufp)
	buf := *bufp

	for {
		n, err := unix.Getdents(fd, buf)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return err
		}
		if n <= 0 {
			return nil
		}
		for off := 0; off < n; {
			// linux_dirent64: u64 d_ino; s64 d_off; u16 d_reclen; u8 d_type; char d_name[];
			rec := (*unix.Dirent)(unsafe.Pointer(&buf[off]))
			reclen := int(rec.Reclen)
			// Name is at offset 19 (8+8+2+1); reinterpret [256]int8 as []byte.
			nameStart := off + 19
			// Find null terminator within the record.
			nameEnd := nameStart
			for nameEnd < off+reclen && buf[nameEnd] != 0 {
				nameEnd++
			}
			name := buf[nameStart:nameEnd]
			off += reclen
			if isDotOrDotDot(name) {
				continue
			}
			var kind dirEntKind
			switch rec.Type {
			case unix.DT_REG:
				kind = dentRegular
			case unix.DT_DIR:
				kind = dentDir
			case unix.DT_UNKNOWN:
				// Some filesystems return DT_UNKNOWN for every entry; lstat to
				// classify so the audit doesn't silently under-report files.
				var st unix.Stat_t
				if unix.Lstat(dirPath+"/"+string(name), &st) != nil {
					continue
				}
				switch st.Mode & unix.S_IFMT {
				case unix.S_IFREG:
					kind = dentRegular
				case unix.S_IFDIR:
					kind = dentDir
				default:
					continue // symlink/fifo/etc — skip (matches DT_LNK handling)
				}
			default:
				// DT_LNK / DT_FIFO etc — skip.
				continue
			}
			fn(name, kind)
		}
	}
}

// isDotOrDotDot returns true for "." and ".." entries.
func isDotOrDotDot(name []byte) bool {
	if len(name) == 1 && name[0] == '.' {
		return true
	}
	if len(name) == 2 && name[0] == '.' && name[1] == '.' {
		return true
	}
	return false
}
