#!/usr/bin/env sh

set -eu

VERSION="${VERSION:-latest}"
BASE_URL="https://github.com/egladman/magus/releases"

TARGET_OS="${TARGET_OS:-$(uname -s)}"
TARGET_ARCH="${TARGET_ARCH:-$(uname -m)}"

INSTALL_PREFIX="${INSTALL_PREFIX:-${HOME:?}/.local}"

MAGUS_RELEASE_PUBKEY_HEX="ffbb8fa6f36274defd12888093c6a322c2532bc545016f495ab4955decb66779"

DRY_RUN=false
VERBOSE=false

die() {
    printf 'fatal: %s\n' "$*" >&2
    exit 1
}

is_true() {
    case "$1" in
        1|true|TRUE|yes|YES|on|ON)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

log() {
    if is_true "$VERBOSE"; then
        printf '%s\n' "$*"
    fi
}

require() {
    command -v "$1" >/dev/null 2>&1 ||
        die "missing required command: $1"
}

usage() {
    cat <<EOF
Usage:
  install.sh [options]

Options:
  --version VERSION   Install a specific release tag
  --dry-run           Show what would be installed
  --verbose           Print additional verification details
  --help              Show this help message

Environment:
  VERSION             Release version (default: latest)
  INSTALL_PREFIX      Installation prefix (default: ~/.local)
  TARGET_OS           The operating system's binary to be installed  (default: auto-detected)
  TARGET_ARCH         The architecture's binary to be installed  (default: auto-detected)
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version)
            [ "$#" -ge 2 ] ||
                die "--version requires a value"

            VERSION="$2"
            shift 2
            ;;

        --dry-run)
            DRY_RUN=true
            shift
            ;;

        --verbose)
            VERBOSE=true
            shift
            ;;

        --help)
            usage
            exit 0
            ;;

        *)
            die "unknown argument: $1"
            ;;
    esac
done

curl_get() {
    curl \
        --fail \
        --silent \
        --show-error \
        --location \
        --proto '=https' \
        --max-redirs 5 \
        "$@"
}

# Returns the resolved release tag URL.
#
# Examples:
#   https://github.com/egladman/magus/releases/tag/v0.2.1
#   https://github.com/egladman/magus/releases/tag/v1.0.0
find_release() {
    case "$VERSION" in
        latest|"")
            curl_get \
                -o /dev/null \
                -w "%{redirect_url}" \
                "${BASE_URL}/latest"
            ;;

        v*)
            printf '%s/tag/%s\n' "$BASE_URL" "$VERSION"
            ;;

        *)
            die "unexpected version '$VERSION' (expected 'latest' or a tag beginning with 'v')"
            ;;
    esac
}

release_version() {
    url="${1:?}"
    printf '%s\n' "${url##*/}"
}

detect_platform() {
    case "$TARGET_OS" in
        Linux)
            OS=linux
            ;;

        Darwin)
            OS=darwin
            ;;

        *)
            die "unsupported operating system: $TARGET_OS"
            ;;
    esac

    case "$TARGET_ARCH" in
        x86_64|amd64)
            ARCH=amd64
            ;;

        arm64|aarch64)
            ARCH=arm64
            ;;

        *)
            die "unsupported architecture: $TARGET_ARCH"
            ;;
    esac
}

# Downloads release metadata only.
#
# The metadata must be authenticated before downloading any binary artifact.
download_metadata() {
    # Usage: download_metadata <release-url>

    base_url="${1:?}"

    printf 'Downloading release metadata...\n'

    curl_get \
        "${base_url}/SHA256SUMS" \
        -o "${work_dir}/SHA256SUMS"

    curl_get \
        "${base_url}/SHA256SUMS.sig" \
        -o "${work_dir}/SHA256SUMS.sig"
}

# Verifies that SHA256SUMS was signed by the Magus release key.
#
# No hashes are trusted until this succeeds.
verify_metadata() {
    cd "$work_dir"

    log "Verifying release metadata signature..."

    printf '302a300506032b6570032100%s' \
        "${MAGUS_RELEASE_PUBKEY_HEX}" \
        | xxd -r -p > pub.der

    {
        echo "-----BEGIN PUBLIC KEY-----"
        openssl base64 -A < pub.der
        echo
        echo "-----END PUBLIC KEY-----"
    } > pub.pem

    openssl pkeyutl \
        -verify \
        -pubin \
        -inkey pub.pem \
        -rawin \
        -in SHA256SUMS \
        -sigfile SHA256SUMS.sig \
        || die "release metadata signature verification failed"

    log "Release metadata signature verified."
}

# Downloads the release artifact after metadata has been authenticated.
download_artifact() {
    # Usage: download_artifact <release-url>

    release_url="${1:?}"
    version="$(release_version "$release_url")"

    artifact="magus_${version}_${OS}_${ARCH}.tar.gz"

    printf 'Downloading %s...\n' "$artifact"

    curl_get \
        "${BASE_URL}/download/${version}/${artifact}" \
        -o "${work_dir}/${artifact}"

    printf '%s\n' "$artifact"
}

verify_artifact() {
    # Usage: verify_artifact <artifact>

    artifact="${1:?}"

    cd "$work_dir"

    log "Verifying artifact checksum..."

    grep " ${artifact}\$" SHA256SUMS \
        | sha256sum --check --status \
        || die "checksum verification failed"

    log "Artifact checksum verified."
}

install_release() {
    # Usage: install_release <artifact>

    archive="${1:?}"

    verify_artifact "$archive"

    printf '\n'
    printf 'Installation plan:\n'
    printf '  Version: %s\n' "$(release_version "$url")"
    printf '  Platform: %s/%s\n' "$OS" "$ARCH"
    printf '  Destination: %s/magus\n' "$BIN_DIR"

if is_true "$DRY_RUN"; then
        return
    fi

    cd "$work_dir"

    mkdir -p "${INSTALL_PREFIX}/bin"

    tar -xzf "$archive"

    install -m 0755 \
        magus \
        "${INSTALL_PREFIX}/bin/magus"

    printf '\n'
    printf 'Successfully installed Magus to:\n'
    printf '  %s/magus\n' "${INSTALL_PREFIX}/bin"

    case ":$PATH:" in
    *":${INSTALL_PREFIX}/bin:"*)
            ;;

        *)
            printf '\n'
            printf 'NOTE:\n'
            printf '  %s is not on your PATH.\n' "${INSTALL_PREFIX}/bin"
            printf '  Add it to your shell profile to invoke "magus" directly.\n'
            ;;
    esac
}

main() {
    require curl
    require tar
    require install
    require openssl
    require grep
    require xxd
    require uname
    require mktemp
    require sha256sum

    umask 022

    detect_platform

    url="$(find_release)"

    work_dir="$(mktemp -d)"

    cleanup() {
        rm -rf "$work_dir"
    }

    trap cleanup EXIT HUP INT TERM

    download_metadata "$url"

    verify_metadata

    artifact="$(download_artifact "$url")"

    install_release "$artifact"
}

main "$@"
