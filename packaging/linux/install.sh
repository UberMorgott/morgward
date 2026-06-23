#!/usr/bin/env sh
# Install morgward + its desktop entry and icons into the XDG dirs.
#
# Layout expected next to this script (the release tarball):
#   ./morgward                         the binary
#   ./morgward.desktop                 the launcher entry
#   ./icons/hicolor/<size>/apps/...    the icon theme tree
#
# Usage:
#   sudo ./install.sh            system-wide   (PREFIX=/usr/local, icons in /usr/share)
#   ./install.sh --user          current user  (~/.local)
#   PREFIX=/opt ./install.sh     custom prefix
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

user_mode=0
[ "${1:-}" = "--user" ] && user_mode=1

if [ "$user_mode" -eq 1 ]; then
  bindir="$HOME/.local/bin"
  appdir="$HOME/.local/share/applications"
  icondir="$HOME/.local/share/icons/hicolor"
else
  PREFIX="${PREFIX:-/usr/local}"
  bindir="$PREFIX/bin"
  appdir="/usr/share/applications"
  icondir="/usr/share/icons/hicolor"
fi

echo "installing morgward -> $bindir"
install -Dm755 "$here/morgward" "$bindir/morgward"

echo "installing desktop entry -> $appdir"
install -Dm644 "$here/morgward.desktop" "$appdir/morgward.desktop"

echo "installing icons -> $icondir"
for png in "$here"/icons/hicolor/*/apps/morgward.png; do
  [ -e "$png" ] || continue
  size=$(basename "$(dirname "$(dirname "$png")")")
  install -Dm644 "$png" "$icondir/$size/apps/morgward.png"
done
if [ -e "$here/icons/hicolor/scalable/apps/morgward.svg" ]; then
  install -Dm644 "$here/icons/hicolor/scalable/apps/morgward.svg" \
    "$icondir/scalable/apps/morgward.svg"
fi

# refresh caches (best-effort; harmless if the tools are absent)
command -v gtk-update-icon-cache >/dev/null 2>&1 && \
  gtk-update-icon-cache -q -t -f "$icondir" 2>/dev/null || true
command -v update-desktop-database >/dev/null 2>&1 && \
  update-desktop-database -q "$appdir" 2>/dev/null || true

echo "done. launch from your app menu or run: morgward"
