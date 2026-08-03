# core-loop.tape - the README hero GIF: what magus does in four commands.
#
# Two payoffs, in order. First cold-then-warm: `magus run ci` fans lint/build/test
# across four projects and reports "0 cached, 4 ran"; the identical second run
# reports "4 cached, 0 ran" and replays the captured test output instead of
# re-running it. Then affected: touching ONE file and running the same pipeline
# narrows it to the project that changed.
#
# Those are the two things a screenshot cannot show, and they are the two reasons
# to reach for the tool. Everything else in the CLI reads fine as prose.
#
# Render: magus run tapes   (or: vhs tapes/core-loop.tape, from the repo root)

Output assets/gen/core-loop.gif

Require magus

Set Shell bash
# 14px rather than 16: the GIF is embedded at well under its native width in the
# README, so the extra pixels bought nothing and cost height.
Set FontSize 14
Set Width 1100
# Height is set by the LAST frame, because a GIF loops and the final frame is what
# someone glancing at it sees. That frame is now the affected run, which is short,
# so this no longer has to hold the whole cold run at once. The cold run scrolls
# mid-recording, which is fine - it is not what anyone freezes on.
Set Height 520
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

# Beat 1: what is in here. Four projects across libs/, services/ and apps/.
Type "magus ls -o name"
Enter
Sleep 2s

# Beat 2: cold. Every project runs; note "0 cached, 4 ran".
Type "magus run ci"
Enter
Sleep 4s

# Beat 3: the cache payoff. Same command, nothing to do; note "4 cached, 0 ran"
# and the test output replayed straight from the cache.
Type "magus run ci"
Enter
Sleep 5s

# Beat 4: the affected payoff, on a clean screen so it reads on its own. Change
# one file in one project, then run the SAME pipeline. The closing line is
# "4 cached, 1 ran": only the edited project actually did work, and the rest
# came straight back off the cache. Note that this is deliberately NOT phrased as
# "skips the rest" - the unaffected projects are still reported, as cache hits,
# which is the honest description of what the summary shows.
Type "clear"
Enter
Sleep 500ms
Type "echo '// tighten the overdraft guard' >> services/ledger/ledger.go"
Enter
Sleep 1s
Type "magus affected ci"
Enter
Sleep 5s
