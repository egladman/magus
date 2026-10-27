package image

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// Mode is the permission of a placed file or directory: the nine permission bits plus
// setuid, setgid and sticky. Zero is invalid, not "no access": a forgotten mode reads as
// 0, and a file nobody can read is never what was meant.
type Mode uint32

const (
	ModeFile       Mode = 0o644
	ModeExecutable Mode = 0o755
	ModePrivate    Mode = 0o600
	ModeReadonly   Mode = 0o444
	ModeDirectory  Mode = 0o755
	ModeSticky     Mode = 0o1777 // world-writable with the sticky bit, as /tmp is
)

const modeBits = 0o7777

// ParseMode reads an octal mode ("0755", "755", "1777") or ls-style notation
// ("rwxr-xr-x", "rwsr-xr-x", "rwxrwxrwt"). It refuses zero and anything beyond the
// permission, setuid, setgid and sticky bits.
func ParseMode(s string) (Mode, error) {
	var m Mode
	if len(s) == 9 && strings.ContainsAny(s[:1], "r-") {
		parsed, err := parseSymbolic(s)
		if err != nil {
			return 0, err
		}
		m = parsed
	} else {
		n, err := strconv.ParseUint(s, 8, 32)
		if err != nil {
			return 0, fmt.Errorf("image: mode %q: want octal (0755) or rwx notation (rwxr-xr-x)", s)
		}
		m = Mode(n)
	}
	if err := m.Validate(); err != nil {
		return 0, fmt.Errorf("image: mode %q: %w", s, err)
	}
	return m, nil
}

// parseSymbolic reads three rwx triplets. An s or t in an execute slot sets setuid,
// setgid or sticky with execute; S or T sets it without.
func parseSymbolic(s string) (Mode, error) {
	var m Mode
	special := [3]Mode{0o4000, 0o2000, 0o1000}
	for i := range 3 {
		t := s[i*3 : i*3+3]
		shift := uint(6 - 3*i)
		for j, want := range "rw" {
			switch rune(t[j]) {
			case want:
				m |= 1 << (shift + uint(2-j))
			case '-':
			default:
				return 0, fmt.Errorf("image: mode %q: %q at %d is not %c or -", s, t[j], i*3+j, want)
			}
		}
		exec, set := byte('x'), byte('s')
		if i == 2 {
			set = 't'
		}
		switch t[2] {
		case exec:
			m |= 1 << shift
		case set:
			m |= 1<<shift | special[i]
		case set - 'a' + 'A':
			m |= special[i]
		case '-':
		default:
			return 0, fmt.Errorf("image: mode %q: %q at %d is not x, %c, %c or -", s, t[2], i*3+2, set, set-'a'+'A')
		}
	}
	return m, nil
}

// String renders m as four octal digits, the form ParseMode reads back.
func (m Mode) String() string { return fmt.Sprintf("%04o", uint32(m)) }

// MarshalText renders m as String does; an invalid mode is an error.
func (m Mode) MarshalText() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return []byte(m.String()), nil
}

// UnmarshalText parses text with ParseMode.
func (m *Mode) UnmarshalText(text []byte) error {
	parsed, err := ParseMode(string(text))
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// Validate refuses zero and any bit past setuid, setgid and sticky.
func (m Mode) Validate() error {
	if m == 0 {
		return fmt.Errorf("mode is 0; write the bits, e.g. %s or %s", ModeFile, ModeExecutable)
	}
	if m&^modeBits != 0 {
		return fmt.Errorf("mode %#o: only permission, setuid, setgid and sticky bits are allowed", uint32(m))
	}
	return nil
}

// FileMode is m as io/fs spells it, with setuid, setgid and sticky moved to their
// fs.FileMode bits.
func (m Mode) FileMode() fs.FileMode {
	fm := fs.FileMode(m & 0o777)
	if m&0o4000 != 0 {
		fm |= fs.ModeSetuid
	}
	if m&0o2000 != 0 {
		fm |= fs.ModeSetgid
	}
	if m&0o1000 != 0 {
		fm |= fs.ModeSticky
	}
	return fm
}
