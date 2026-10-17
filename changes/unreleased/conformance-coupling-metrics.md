### Added

- **Four coupling conformance metrics.** `shared-body` (anchor 94), `shared-reach` (7), `caller-spread` (3) and the `mode-knobs` list (0). Measured on this tree, 7 of 731 functions that two or more other packages call carry a knob, 1 is past the relative rule (`spawn.Exec` in `internal/proc/spawn`), and none is past the anchors.
