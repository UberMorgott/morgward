#!/usr/bin/env sh
# Remove morgward, its desktop entry and icons (mirror of install.sh).
#
# Usage:
#   sudo ./uninstall.sh          system-wide
#   ./uninstall.sh --user        current user
#   PREFIX=/opt ./uninstall.sh   custom prefix
set -eu

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

rm -f "$bindir/morgward"
rm -f "$appdir/morgward.desktop"
rm -f "$icondir"/*/apps/morgward.png "$icondir"/scalable/apps/morgward.svg

command -v gtk-update-icon-cache >/dev/null 2>&1 && \
  gtk-update-icon-cache -q -t -f "$icondir" 2>/dev/null || true
command -v update-desktop-database >/dev/null 2>&1 && \
  update-desktop-database -q "$appdir" 2>/dev/null || true

echo "morgward removed."
