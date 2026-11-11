#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMPDIR_TEST="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_TEST"' EXIT

BUNDLE="$TMPDIR_TEST/bundle"
DEST="$TMPDIR_TEST/install space/bin"
HOME_TEST="$TMPDIR_TEST/home"
CONFIG_TEST="$TMPDIR_TEST/config"
DATA_TEST="$TMPDIR_TEST/data"
mkdir -p "$BUNDLE/samples" "$HOME_TEST/Desktop"
cp "$REPO_ROOT/docs/assets/monke-icon.png" "$BUNDLE/praimate.png"
# These files must never run during a desktop launch.
printf 'exit 73\n' > "$HOME_TEST/.profile"
printf 'exit 74\n' > "$HOME_TEST/.zshrc"

make_executable() {
  printf '#!/usr/bin/env bash\nexit 0\n' > "$1"
  chmod +x "$1"
}

for binary in praimate wpc praimate-gui praimate-cli praimate-code praimate-graphify; do
  make_executable "$BUNDLE/$binary"
done
printf 'old sample\n' > "$BUNDLE/samples/obsolete.txt"
printf 'license\n' > "$BUNDLE/PRAIMATE-CODE-LICENSE"
printf 'notice\n' > "$BUNDLE/PRAIMATE-CODE-NOTICE"

install_bundle() {
  (
    cd "$BUNDLE"
    HOME="$HOME_TEST" XDG_CONFIG_HOME="$CONFIG_TEST" XDG_DATA_HOME="$DATA_TEST" SHELL=/bin/bash \
      bash "$REPO_ROOT/scripts/install.sh" --prefix "$DEST" --yes "$@"
  ) >/dev/null
}

if bash "$REPO_ROOT/scripts/install.sh" --edition invalid --yes >"$TMPDIR_TEST/invalid.log" 2>&1; then
  echo "installer accepted an unknown edition" >&2
  exit 1
fi

install_bundle
test -x "$DEST/praimate-gui"
test -x "$DEST/praimate-code"
test -x "$CONFIG_TEST/praimate/bin/praimate-graphify"
test -f "$TMPDIR_TEST/install space/share/praimate/samples/obsolete.txt"
test -L "$DEST/praimate-gui-launch"
test -f "$DATA_TEST/applications/praimate.desktop"

# Reject another edition before overwriting an existing installation.
printf 'server\n' > "$BUNDLE/praimate-edition"
printf 'preserve this installation\n' > "$DEST/praimate"
if install_bundle --edition desktop; then
  echo "desktop installer accepted a server bundle" >&2
  exit 1
fi
grep -Fx 'preserve this installation' "$DEST/praimate"
install_bundle --edition server
grep -Fx server "$DEST/praimate-edition"
# An update without --edition must retain Server, rather than default to Desktop.
install_bundle
grep -Fx server "$DEST/praimate-edition"
printf 'desktop\n' > "$BUNDLE/praimate-edition"
if install_bundle --edition server; then
  echo "server installer accepted a desktop bundle" >&2
  exit 1
fi
install_bundle --edition desktop
grep -Fx desktop "$DEST/praimate-edition"
test ! -e "$DATA_TEST/applications/praimate-gui.desktop"

# Launch the installed wrapper with shell files that would exit immediately.
HOME="$HOME_TEST" SHELL=/bin/bash "$DEST/praimate-gui-launch"
grep -F "Exec=/usr/bin/env bash \"$DEST/praimate-gui-launch\"" "$DATA_TEST/applications/praimate.desktop"
grep -Fx 'Icon=praimate' "$DATA_TEST/applications/praimate.desktop"
grep -Fx 'StartupWMClass=praimate' "$DATA_TEST/applications/praimate.desktop"
test -f "$DATA_TEST/icons/hicolor/512x512/apps/praimate.png"
if command -v desktop-file-validate >/dev/null; then
  desktop-file-validate "$DATA_TEST/applications/praimate.desktop"
fi

rm "$BUNDLE/praimate-code" "$BUNDLE/praimate-graphify"
rm "$BUNDLE/PRAIMATE-CODE-LICENSE" "$BUNDLE/PRAIMATE-CODE-NOTICE"
rm "$BUNDLE/samples/obsolete.txt"
printf 'new sample\n' > "$BUNDLE/samples/current.txt"

install_bundle
test -x "$DEST/praimate-gui"
test -L "$DEST/praimate-gui-launch"
test ! -e "$DEST/praimate-code"
test ! -e "$DEST/PRAIMATE-CODE-LICENSE"
test ! -e "$DEST/PRAIMATE-CODE-NOTICE"
test ! -e "$CONFIG_TEST/praimate/bin/praimate-graphify"
test ! -e "$TMPDIR_TEST/install space/share/praimate/samples/obsolete.txt"
test -f "$TMPDIR_TEST/install space/share/praimate/samples/current.txt"
test -f "$DATA_TEST/applications/praimate.desktop"

rm "$BUNDLE/praimate-gui"
if install_bundle; then
  echo "installer accepted a GUI-less bundle" >&2
  exit 1
fi

echo "install update cleanup: ok"
