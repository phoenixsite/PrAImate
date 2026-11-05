#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TEST_TMP="$(mktemp -d)"
trap 'rm -rf "$TEST_TMP"' EXIT
mkdir -p "$TEST_TMP/bin"

cat > "$TEST_TMP/bin/bun" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

case "$1" in
  install)
    if [ -n "${PRAIMATE_TEST_WORKSPACE_RECORD:-}" ]; then
      printf '%s\n' "$PWD" > "$PRAIMATE_TEST_WORKSPACE_RECORD"
    fi
    for asset in apple-touch-icon-v3.png favicon-v3.ico site.webmanifest social-share.png; do
      test -f "packages/app/public/$asset"
      test ! -L "packages/app/public/$asset"
    done
    ;;
  run)
    grep -q 'praimate:host-tls' src/provider/provider.ts
    test -f src/provider/praimate-host-tls.ts
    target=''
    baseline=0
    for arg in "$@"; do
      case "$arg" in
        --target=*) target="${arg#--target=}" ;;
        --baseline) baseline=1 ;;
      esac
    done
    case "$target" in *-amd64) target="${target%-amd64}-x64" ;; esac
    ext=''
    case "$target" in windows-*) ext='.exe' ;; esac
    mkdir -p "dist/opencode-$target/bin"
    printf '%s\n' "$target" > "dist/opencode-$target/bin/opencode$ext"
    chmod +x "dist/opencode-$target/bin/opencode$ext"
    if [ "$baseline" = 1 ]; then
      mkdir -p "dist/opencode-$target-baseline/bin"
      printf '%s\n' "$target-baseline" > "dist/opencode-$target-baseline/bin/opencode$ext"
      chmod +x "dist/opencode-$target-baseline/bin/opencode$ext"
      case "$target" in
        linux-*)
          mkdir -p "dist/opencode-$target-baseline-musl/bin"
          printf '%s\n' 'wrong-musl-ABI' > "dist/opencode-$target-baseline-musl/bin/opencode"
          chmod +x "dist/opencode-$target-baseline-musl/bin/opencode"
          ;;
      esac
    fi
    ;;
  */praimate-code-tls.mjs)
    exec node "$@"
    ;;
esac
EOF
chmod +x "$TEST_TMP/bin/bun"

cat > "$TEST_TMP/bin/df" <<'EOF'
#!/usr/bin/env bash
printf 'Filesystem 1024-blocks Used Available Capacity Mounted on\n'
printf 'testfs 20971520 0 20971520 0%% /tmp\n'
EOF
chmod +x "$TEST_TMP/bin/df"

PATH="$TEST_TMP/bin:$PATH" XDG_CONFIG_HOME="$TEST_TMP/config" OUT="$TEST_TMP/out" \
  bash "$REPO_ROOT/scripts/build-praimate-code.sh" >/dev/null

test -x "$TEST_TMP/out/praimate-code"
echo "praimate-code public assets: ok"

# Upstream --baseline also emits a musl variant. Never select it for the
# glibc release, even when directory iteration puts it before the native one.
cat > "$TEST_TMP/bin/find" <<'EOF'
#!/usr/bin/env bash
/usr/bin/find "$@" | LC_ALL=C sort
EOF
chmod +x "$TEST_TMP/bin/find"
PATH="$TEST_TMP/bin:$PATH" PRAIMATE_CODE_TARGET=linux-amd64 XDG_CONFIG_HOME="$TEST_TMP/config" OUT="$TEST_TMP/baseline-out" BASELINE=1 \
  bash "$REPO_ROOT/scripts/build-praimate-code.sh" >/dev/null
test "$(cat "$TEST_TMP/baseline-out/praimate-code-baseline")" = 'linux-x64-baseline'
echo "praimate-code baseline ABI selection: ok"

cat > "$TEST_TMP/bin/go" <<'EOF'
#!/usr/bin/env bash
case "$*" in
  'env GOOS') printf 'windows\n' ;;
  'env GOARCH') printf 'amd64\n' ;;
  *) exit 1 ;;
esac
EOF
cat > "$TEST_TMP/bin/cygpath" <<'EOF'
#!/usr/bin/env bash
test "$1" = -u
printf '%s\n' "$2"
EOF
chmod +x "$TEST_TMP/bin/go" "$TEST_TMP/bin/cygpath"
mkdir -p "$TEST_TMP/runner"
PATH="$TEST_TMP/bin:$PATH" RUNNER_TEMP="$TEST_TMP/runner" \
  PRAIMATE_TEST_WORKSPACE_RECORD="$TEST_TMP/workspace" \
  PRAIMATE_BUILD_DIR='' XDG_CONFIG_HOME="$TEST_TMP/long-windows-user-config-directory" OUT="$TEST_TMP/windows-out" \
  bash "$REPO_ROOT/scripts/build-praimate-code.sh" >/dev/null
test -x "$TEST_TMP/windows-out/praimate-code.exe"
workspace="$(cat "$TEST_TMP/workspace")"
case "$workspace" in
  "$TEST_TMP/runner/"*) ;;
  *) echo "Windows CI build used the long user profile instead of runner temp: $workspace" >&2; exit 1 ;;
esac
test "${#workspace}" -lt "$((${#TEST_TMP} + 30))"
echo "praimate-code Windows build path: ok"
