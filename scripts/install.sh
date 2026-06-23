#!/bin/sh
# morgward installer / runner for Linux & macOS — no manual download needed.
#
#   curl -fsSL https://raw.githubusercontent.com/UberMorgott/morgward/main/scripts/install.sh | sh
#
# Downloads the latest release binary (with a curl progress bar), verifies its
# SHA-256 against the release checksums.txt, installs it to ~/.local/bin/morgward,
# and launches it. Set MORGWARD_NO_LAUNCH=1 to install without launching.
set -eu

repo="UberMorgott/morgward"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in
    linux)  os=linux ;;
    darwin) os=darwin ;;
    *) echo "morgward: unsupported OS: $os" >&2; exit 1 ;;
esac
case "$arch" in
    x86_64|amd64)  arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "morgward: unsupported arch: $arch" >&2; exit 1 ;;
esac

asset="morgward-${os}-${arch}"
base="https://github.com/${repo}/releases/latest/download"
dest_dir="${MORGWARD_DIR:-$HOME/.local/bin}"
dest="${dest_dir}/morgward"
mkdir -p "$dest_dir"

echo "morgward: downloading latest ($asset)..."
curl -fL --progress-bar -o "$dest" "${base}/${asset}"
chmod +x "$dest"

# Verify SHA-256 against the release checksums.txt — fail closed on a mismatch.
sums=$(mktemp)
if curl -fsSL -o "$sums" "${base}/checksums.txt"; then
    want=$(grep " ${asset}\$" "$sums" | awk '{print $1}' | head -n1 || true)
    if command -v sha256sum >/dev/null 2>&1; then
        got=$(sha256sum "$dest" | awk '{print $1}')
    else
        got=$(shasum -a 256 "$dest" | awk '{print $1}')
    fi
    if [ -n "$want" ] && [ "$want" = "$got" ]; then
        echo "morgward: checksum OK ($got)"
    elif [ -n "$want" ]; then
        rm -f "$dest"
        echo "morgward: checksum MISMATCH (got $got, want $want) — deleted download." >&2
        exit 1
    else
        echo "morgward: checksum entry not found — skipping verification." >&2
    fi
fi
rm -f "$sums"

echo "morgward: installed -> $dest"
case ":$PATH:" in
    *":$dest_dir:"*) : ;;
    *) echo "morgward: add '$dest_dir' to PATH, or run it as '$dest'." ;;
esac

if [ -z "${MORGWARD_NO_LAUNCH:-}" ]; then
    echo "morgward: launching..."
    exec "$dest" "$@"
fi
