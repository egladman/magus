#!/usr/bin/env bash
# fetch.sh: download SWE-bench Verified into verified.jsonl and print its sha256.
#
# The table is about 9 MB, so it is fetched into an ignored path rather than
# vendored; verified.sha256 beside this script is the digest a previous fetch
# observed, and a fetch that disagrees with it exits non-zero so a changed
# upstream table is noticed rather than silently graded against.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="$HERE/verified.jsonl"
PIN="$HERE/verified.sha256"
API='https://datasets-server.huggingface.co/rows?dataset=SWE-bench%2FSWE-bench_Verified&config=default&split=test'

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

for offset in 0 100 200 300 400; do
    curl -fsSL --retry 5 --retry-all-errors --retry-delay 2 \
        "$API&offset=$offset&length=100" -o "$TMP/page-$offset.json"
    if ! jq -e '(.rows | length) == 100' "$TMP/page-$offset.json" >/dev/null; then
        printf 'fetch.sh: page at offset %s did not return 100 rows\n' "$offset" >&2
        exit 1
    fi
done

# hints_text is the issue's comment thread, which the benchmark never shows an
# agent; dropping it keeps the file a third smaller and the prompt contract clear.
jq -c -s 'map(.rows[].row | del(.hints_text)) | sort_by(.instance_id) | .[]' \
    "$TMP"/page-*.json >"$OUT"

rows=$(wc -l <"$OUT" | tr -d ' ')
if [[ $rows != 500 ]]; then
    printf 'fetch.sh: expected 500 rows, got %s\n' "$rows" >&2
    exit 1
fi

if command -v sha256sum >/dev/null; then
    sum=$(sha256sum "$OUT" | cut -d' ' -f1)
else
    sum=$(shasum -a 256 "$OUT" | cut -d' ' -f1)
fi
printf '%s  verified.jsonl\n' "$sum"

if [[ -f $PIN ]]; then
    pinned=$(cut -d' ' -f1 "$PIN")
    if [[ $pinned != "$sum" ]]; then
        printf 'fetch.sh: verified.jsonl digest %s differs from the pinned %s; the upstream table changed\n' \
            "$sum" "$pinned" >&2
        exit 1
    fi
    printf 'fetch.sh: digest matches %s\n' "$PIN" >&2
fi
