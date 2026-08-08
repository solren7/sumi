#!/bin/sh
# Install the sumi CLI so the skill can be used in a container that only has the
# skill files. Safe to re-run: it exits early when a working sumi is on PATH.
#
# Overrides:
#   SUMI_CLI_URL  download from this exact URL instead of the GitHub release
#                 (use for a private mirror or an air-gapped network)
#   SUMI_CLI_DIR  install into this directory instead of the first writable
#                 default (/usr/local/bin, then ~/.local/bin)
#   SUMI_CLI_TAG  release tag to fetch (default: latest)
set -eu

REPO=solren7/sumi

if command -v sumi >/dev/null 2>&1 && sumi --help >/dev/null 2>&1; then
    echo "sumi is already installed at $(command -v sumi)" >&2
    exit 0
fi

# Resolve the release asset name for this machine.
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
case "$os" in
    linux | darwin) ;;
    *) echo "unsupported OS: $os" >&2; exit 1 ;;
esac
asset="sumi-${os}-${arch}"

if [ -n "${SUMI_CLI_URL:-}" ]; then
    url=$SUMI_CLI_URL
elif [ -n "${SUMI_CLI_TAG:-}" ]; then
    url="https://github.com/$REPO/releases/download/$SUMI_CLI_TAG/$asset"
else
    url="https://github.com/$REPO/releases/latest/download/$asset"
fi

# Pick an install directory we can actually write to.
if [ -n "${SUMI_CLI_DIR:-}" ]; then
    target_dir=$SUMI_CLI_DIR
    mkdir -p "$target_dir" 2>/dev/null || { echo "cannot create $target_dir" >&2; exit 1; }
else
    # /usr/local/bin first: it is on PATH almost everywhere, so the agent can call
    # `sumi` directly. Falls back to ~/.local/bin for non-root containers.
    target_dir=""
    for candidate in /usr/local/bin "$HOME/.local/bin"; do
        if mkdir -p "$candidate" 2>/dev/null && [ -w "$candidate" ]; then
            target_dir=$candidate
            break
        fi
    done
    [ -n "$target_dir" ] || { echo "no writable install dir; set SUMI_CLI_DIR" >&2; exit 1; }
fi

tmp=$(mktemp) || exit 1
trap 'rm -f "$tmp"' EXIT INT TERM

echo "downloading $url" >&2
if command -v curl >/dev/null 2>&1; then
    fetch_err=$(curl -fsSL "$url" -o "$tmp" 2>&1) || fetch_status=$?
elif command -v wget >/dev/null 2>&1; then
    fetch_err=$(wget -qO "$tmp" "$url" 2>&1) || fetch_status=$?
else
    cat >&2 <<EOF
neither curl nor wget is available in this container.

Install one and re-run this script:
  alpine:         apk add --no-cache curl
  debian/ubuntu:  apt-get update && apt-get install -y curl

Or copy the binary out of the published image instead (same binary):
  docker create --name sumi-cli ghcr.io/$REPO:latest
  docker cp sumi-cli:/app/server $target_dir/sumi
  docker rm sumi-cli
EOF
    exit 1
fi

if [ "${fetch_status:-0}" -ne 0 ]; then
    cat >&2 <<EOF
download failed: ${fetch_err:-unknown error}
  url: $url

If this release asset does not exist yet, the CLI can also be taken straight out
of the published image (same binary):

  docker create --name sumi-cli ghcr.io/$REPO:latest
  docker cp sumi-cli:/app/server $target_dir/sumi
  docker rm sumi-cli

Or point the installer at your own copy:

  SUMI_CLI_URL=https://your-host/sumi-$os-$arch sh "\$0"
EOF
    exit 1
fi

chmod +x "$tmp"
if ! "$tmp" --help >/dev/null 2>&1; then
    echo "downloaded file is not a working sumi binary (wrong architecture?)" >&2
    exit 1
fi

mv "$tmp" "$target_dir/sumi"
trap - EXIT INT TERM
echo "installed sumi to $target_dir/sumi" >&2

case ":$PATH:" in
    *":$target_dir:"*) ;;
    *) echo "note: $target_dir is not on PATH; call it as $target_dir/sumi or add it to PATH" >&2 ;;
esac
