#!/usr/bin/env bash
# images.sh <base-image> <platform> <magus-binary>: build the trial image for
# one instance on its base, pulling the base first, and print the image tag.
#
# Built once per (base, magus binary) and reused: the base pulls are large, so a
# manifest of 24 instances times 8 runs must not pay for the layer more than 24
# times. The tag carries a digest of the magus binary, so swapping the build
# under test yields a new image rather than a stale hit on the old one, and the
# base's architecture label, so the two builds of one instance never collide.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
base=$1
platform=$2
bin=$3

id=${base##*.}
case $platform in
    linux/arm64) node_arch=arm64 ;;
    *) node_arch=x64 ;;
esac

if command -v sha256sum >/dev/null; then
    digest=$(sha256sum "$bin" | cut -c1-12)
else
    digest=$(shasum -a 256 "$bin" | cut -c1-12)
fi
tag="magus-bench/swebench:$id-${platform#linux/}-$digest"

if docker image inspect "$tag" >/dev/null 2>&1; then
    printf '%s\n' "$tag"
    exit 0
fi

ctx="$(mktemp -d)"
trap 'rm -rf "$ctx"' EXIT
cp "$bin" "$ctx/magus"
chmod 755 "$ctx/magus"

docker build --platform "$platform" --pull --build-arg "BASE=$base" --build-arg "NODE_ARCH=$node_arch" \
    -t "$tag" -f "$HERE/bench.Dockerfile" "$ctx" >&2
printf '%s\n' "$tag"
