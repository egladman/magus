package tty

import "github.com/egladman/magus/internal/interactive/screen"

// Small readers over the emulator, so the assertions below stay about what a
// reader would see rather than about accessor spelling.
func cursorRowOf(s *screen.Screen) int { r, _ := s.Cursor(); return r }
func cursorColOf(s *screen.Screen) int { _, c := s.Cursor(); return c }
func scrollTopOf(s *screen.Screen) int { t, _ := s.ScrollRegion(); return t }
func scrollBotOf(s *screen.Screen) int { _, b := s.ScrollRegion(); return b }
