# core-loop.tape - the README hero GIF: what magus does in three commands.
#
# The story is cold-then-warm. `magus run ci` fans lint/build/test across four
# projects and reports "0 cached, 4 ran"; the identical second run reports
# "4 cached, 0 ran" and replays the captured test output instead of re-running
# it. That replay is the whole point of the tool, and it is the one thing a
# screenshot cannot show.
#
# Render: magus run tapes   (or: vhs tapes/core-loop.tape, from the repo root)

Output assets/gen/core-loop.gif

Require magus

Set Shell bash
Set FontSize 16
Set Width 1200
# Height is set by the payoff, not by the opening: the last frame has to hold
# BOTH summary lines at once (854ms vs 140ms is the entire argument), and the
# cold run is ~22 lines. Trimming further scrolls the first summary off the top.
Set Height 700
Set Padding 20
Set Theme "Catppuccin Mocha"
# Typing is slowed a little past the default so the commands read as typed
# rather than pasted; the pauses after each are where the eye actually lands.
Set TypingSpeed 60ms

# Build the throwaway workspace off-camera. The recording opens on a prompt in
# a cold, freshly committed repo - see tapes/demo-init.sh for why it is
# generated rather than checked in.
Hide
Type "source tapes/demo-init.sh"
Enter
Type "clear"
Enter
Show

Sleep 1s

# Beat 1: what is in here. Four projects, three of them Go.
Type "magus ls -o name"
Enter
Sleep 2s

# Beat 2: cold. Every project runs; note "0 cached, 4 ran".
Type "magus run ci"
Enter
Sleep 4s

# Beat 3: the payoff. Same command, nothing to do; note "4 cached, 0 ran"
# and the test output replayed straight from the cache.
Type "magus run ci"
Enter
Sleep 5s
