#!/usr/bin/env bash
set -euo pipefail
[[ "$(uname -s)" == Linux ]] || { echo 'Linux desktop-entry test skipped'; exit 0; }
ICON_TEST_REPO="$(cd "$(dirname "$0")/.." && pwd)"
ICON_TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "$ICON_TEST_ROOT"' EXIT
mkdir -p "$ICON_TEST_ROOT/mock" "$ICON_TEST_ROOT/work"
# Copy the standalone installer away from the checkout so neither source-tree
# icons nor existing dist/ binaries can mask the release-only regression.
cp "$ICON_TEST_REPO/scripts/install.sh" "$ICON_TEST_ROOT/work/install.sh"
cat > "$ICON_TEST_ROOT/mock/curl" <<'MOCK'
#!/usr/bin/env bash
set -eu
output=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o) output="$2"; shift 2 ;;
    --retry) shift 2 ;;
    https://github.com/*/releases/download/v9.0.0/*.tar.gz) shift ;;
    -fsSL) shift ;;
    *) echo 'Unexpected network request in installer test' >&2; exit 1 ;;
  esac
done
test -n "$output"
cp "$PRAIMATE_ICON_TEST_ARCHIVE" "$output"
MOCK
chmod +x "$ICON_TEST_ROOT/mock/curl"
case "$(uname -m)" in x86_64|amd64) ICON_TEST_TARGET=linux-amd64;; aarch64|arm64) ICON_TEST_TARGET=linux-arm64;; *) exit 0;; esac
for edition in desktop server; do
  for icon in present missing; do
    testroot="$ICON_TEST_ROOT/$edition-$icon"
    bundle="$testroot/bundle/$ICON_TEST_TARGET"
    mkdir -p "$bundle" "$testroot/home/Desktop"
    for name in praimate wpc praimate-gui praimate-cli; do
      printf '#!/usr/bin/env bash\nexit 0\n' > "$bundle/$name"
      chmod +x "$bundle/$name"
    done
    printf '%s\n' "$edition" > "$bundle/praimate-edition"
    if [[ "$icon" == present ]]; then cp "$ICON_TEST_REPO/docs/assets/monke-icon.png" "$bundle/praimate.png"; fi
    tar -czf "$testroot/release.tar.gz" -C "$testroot/bundle" "$ICON_TEST_TARGET"
    (
      cd "$ICON_TEST_ROOT/work"
      HOME="$testroot/home" XDG_CONFIG_HOME="$testroot/config" XDG_DATA_HOME="$testroot/data" \
        PATH="$ICON_TEST_ROOT/mock:$PATH" RELEASE_TAG=v9.0.0 PRAIMATE_ICON_TEST_ARCHIVE="$testroot/release.tar.gz" \
        bash ./install.sh --binary --edition "$edition" --prefix "$testroot/bin" --yes > "$testroot/install.log" 2>&1
    )
    grep -Fx 'Icon=praimate' "$testroot/data/applications/praimate.desktop"
    if [[ "$icon" == present ]]; then cmp "$bundle/praimate.png" "$testroot/data/icons/hicolor/512x512/apps/praimate.png"; fi
    grep -Fx "$edition" "$testroot/bin/praimate-edition"
  done
done
echo 'Release installer icon: both editions pass with and without a bundled icon'
