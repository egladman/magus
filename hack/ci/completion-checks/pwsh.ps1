# Exercises the PowerShell completion script magus ships, inside an official
# Microsoft image. Run by the `completion-test` target with the repo mounted at the
# working directory; there is no magus binary in the container, so this asserts the
# STATIC candidates only - project completions shell out to `magus ls` and are
# legitimately empty.
$ErrorActionPreference = 'Stop'

. ./cmd/magus/completions/magus.ps1

function Assert-Completes {
    param([string]$Line, [string[]]$Expect)

    $result = TabExpansion2 -inputScript $Line -cursorColumn $Line.Length
    $got = $result.CompletionMatches.CompletionText
    if ($got.Count -eq 0) {
        Write-Error "no candidates for '$Line'"
        exit 1
    }
    foreach ($want in $Expect) {
        if ($got -notcontains $want) {
            Write-Error "'$Line' is missing $want (got: $($got -join ', '))"
            exit 1
        }
    }
}

# Both names must complete, not just magus: mgs is a symlink to the same binary, so a
# completion that knows only one name is half broken.
Assert-Completes -Line 'magus ' -Expect @('run', 'self')
Assert-Completes -Line 'mgs ' -Expect @('run', 'self')

# Asserting a specific candidate keeps this honest: an empty result and a stale one
# look identical to a count check.
Assert-Completes -Line 'magus self ' -Expect @('update', 'install-shorthand')

Write-Output 'pwsh: ok'
