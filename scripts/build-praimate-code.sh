#!/usr/bin/env bash
# Build "PrAImate Code" — our version-pinned, rebranded build of
# OpenCode (https://github.com/sst/opencode, MIT). Produces a single
# self-contained executable (Bun runtime + web UI + agent all baked in)
# named `praimate-code`, dropped into the directory given by $OUT
# (default: dist/<goos>-<goarch>/).
#
# LICENSE / ATTRIBUTION (important): OpenCode is MIT licensed, which
# permits forking, modifying and redistributing — INCLUDING under a
# different product name — provided the original copyright notice is
# retained. This script therefore copies OpenCode's LICENSE next to the
# binary and writes a NOTICE recording the upstream source + commit.
# Do NOT remove those. We rebrand the product NAME, not the attribution.
#
# Requires: bun (>=1.3), ~8GB disk, network (bun pulls the JS dep tree).
# The OpenCode SOURCE is vendored in-repo (third_party/opencode), so no
# git clone of upstream is needed — bun install fetches node_modules from
# npm, reproducible from the committed bun.lock.
#
# Usage:
#   scripts/build-praimate-code.sh                 # native target → dist/<triplet>/
#   OUT=/some/dir scripts/build-praimate-code.sh    # custom output dir
#   OPENCODE_SRC=/path/to/opencode scripts/build-praimate-code.sh  # source override
#   OPENCODE_REF=v1.17.4 scripts/build-praimate-code.sh  # clone this ref instead of vendored
#   PRAIMATE_CODE_TARGET=windows-amd64 scripts/build-praimate-code.sh
#   BASELINE=1 scripts/build-praimate-code.sh       # no-AVX2 build → praimate-code-baseline
#
# BASELINE builds: Bun's default x64 binaries require AVX2; on older
# CPUs and VMs without AVX2 passthrough they crash with an illegal
# instruction (0xc000001d on Windows). BASELINE=1 compiles upstream's
# "-baseline" target instead — the installer downloads that asset when
# it detects a no-AVX2 host. Only meaningful on x64.

set -euo pipefail
cd "$(dirname "$0")/.."
REPO_ROOT="$(pwd)"

# Vendored OpenCode source (pristine mirror at the pinned ref). See
# third_party/README.md. OPENCODE_REF, when set, falls back to cloning
# that ref from upstream instead — used to bump/re-vendor.
VENDORED_OPENCODE="$REPO_ROOT/third_party/opencode"
OPENCODE_REF="${OPENCODE_REF:-}"
OPENCODE_URL="https://github.com/sst/opencode"
export OPENCODE_VERSION="${OPENCODE_VERSION:-1.18.1}"
export OPENCODE_CHANNEL="${OPENCODE_CHANNEL:-latest}"

NATIVE_GOOS="$(go env GOOS 2>/dev/null || uname -s | tr '[:upper:]' '[:lower:]')"
NATIVE_GOARCH="$(go env GOARCH 2>/dev/null || echo amd64)"
TARGET="${PRAIMATE_CODE_TARGET:-$NATIVE_GOOS-$NATIVE_GOARCH}"
GOOS="${TARGET%-*}"
GOARCH="${TARGET#*-}"
case "$GOOS-$GOARCH" in
  linux-amd64|linux-arm64|windows-amd64|windows-arm64|darwin-arm64|darwin-amd64) ;;
  *) echo "error: unsupported PRAIMATE_CODE_TARGET: $TARGET" >&2; exit 2 ;;
esac
OUT="${OUT:-$REPO_ROOT/dist/$GOOS-$GOARCH}"
EXT=""
[ "$GOOS" = "windows" ] && EXT=".exe"

command -v bun >/dev/null 2>&1 || { echo "error: bun not found on PATH (install from https://bun.sh)"; exit 1; }

# Exercise the actual Bun transport on the Linux release host, where OpenSSL
# is available for ephemeral CA fixtures. Other targets use the same helper.
if [ "$NATIVE_GOOS" = "linux" ]; then
  bun test scripts/praimate-code/host-tls.test.mjs
fi

resolve_work_parent() {
  if [ -n "${PRAIMATE_BUILD_DIR:-}" ]; then
    printf '%s\n' "$PRAIMATE_BUILD_DIR"
    return
  fi
  # MSBuild's native dependency builds still have a 260-character path limit.
  # GitHub's short runner temp avoids nesting them below the Windows profile.
  if [ "$NATIVE_GOOS" = "windows" ] && [ -n "${RUNNER_TEMP:-}" ]; then
    cygpath -u "$RUNNER_TEMP/p"
    return
  fi
  printf '%s\n' "${XDG_CONFIG_HOME:-$HOME/.config}/praimate/build"
}

require_work_space() {
  local dir="$1"
  local min_kib=$((8 * 1024 * 1024))
  local avail_kib
  avail_kib="$(df -Pk "$dir" | awk 'NR==2 {print $4}')"
  if [ -n "$avail_kib" ] && [ "$avail_kib" -lt "$min_kib" ]; then
    echo "error: not enough free space for PrAImate Code build under $dir" >&2
    echo "       available: $((avail_kib / 1024)) MiB; required: $((min_kib / 1024)) MiB" >&2
    echo "       set PRAIMATE_BUILD_DIR=/path/on/a/larger/disk and retry" >&2
    exit 1
  fi
}

WORK_PARENT="$(resolve_work_parent)"
mkdir -p "$WORK_PARENT"
require_work_space "$WORK_PARENT"
WORK_PATTERN="praimate-code-XXXXXX"
SRC_NAME="opencode"
if [ "$NATIVE_GOOS" = "windows" ]; then
  WORK_PATTERN="p-XXXXXX"
  SRC_NAME="s"
fi
WORK="$(mktemp -d "$WORK_PARENT/$WORK_PATTERN")"
trap 'rm -rf "$WORK"' EXIT
SRC="$WORK/$SRC_NAME"
mkdir -p "$WORK/tmp"
export TMPDIR="$WORK/tmp"
echo "→ scratch dir: $WORK"

# bun install mutates the tree, so always build from a scratch copy —
# never touch the committed vendored source in place.
if [ -n "$OPENCODE_REF" ]; then
  echo "→ cloning OpenCode $OPENCODE_REF (override) into $WORK"
  git clone --depth 1 --branch "$OPENCODE_REF" "$OPENCODE_URL" "$SRC" 2>&1 | tail -2
elif [ -d "${OPENCODE_SRC:-$VENDORED_OPENCODE}" ]; then
  SRC_DIR="${OPENCODE_SRC:-$VENDORED_OPENCODE}"
  echo "→ using vendored OpenCode source: $SRC_DIR"
  cp -a "$SRC_DIR" "$SRC"
  # Drop any stray node_modules from a prior local build so bun installs clean.
  find "$SRC" -name node_modules -type d -prune -exec rm -rf {} + 2>/dev/null || true
  # OpenCode's build.ts calls `git branch --show-current` to pick a
  # release channel. The vendored source has no .git (it's a stripped
  # mirror), so init a throwaway repo in the scratch copy to make that
  # command succeed — exactly what a fresh clone would have provided.
  git init -q "$SRC" 2>/dev/null || true
else
  echo "error: no vendored OpenCode at $VENDORED_OPENCODE and no OPENCODE_REF set" >&2
  exit 1
fi

# The vendored app/public directory is expected to contain copies of the
# shared UI favicon and social-preview assets. Some archive tools preserve
# their contents as malformed symlinks, which makes Vite fail while copying
# its public directory. Restore them in the disposable build copy from the
# canonical assets already vendored alongside the app.
restore_app_public_assets() {
  local public="$SRC/packages/app/public"
  local favicon="$SRC/packages/ui/src/assets/favicon"
  local images="$SRC/packages/ui/src/assets/images"
  local asset

  for asset in \
    apple-touch-icon-v3.png apple-touch-icon.png \
    favicon-96x96-v3.png favicon-96x96.png \
    favicon-v3.ico favicon-v3.svg favicon.ico favicon.svg \
    site.webmanifest web-app-manifest-192x192.png web-app-manifest-512x512.png
  do
    install -m 0644 "$favicon/$asset" "$public/$asset"
  done
  for asset in social-share.png social-share-zen.png; do
    install -m 0644 "$images/$asset" "$public/$asset"
  done
}

restore_app_public_assets

echo "→ bun install (this is the heavy step)"
( cd "$SRC" && bun install )

# --- Rebrand pass (name-level, functionally safe) ----------------------
# We rename the produced binary and replace the most visible product
# name in the CLI package's user-facing strings. We deliberately do NOT
# rewrite internal identifiers, package names, config-dir paths, or the
# server's API contracts — a blind global rename would break the build
# and the agent. Deeper cosmetic rebranding is incremental follow-up.
echo "→ applying name-level rebrand"
bash "$REPO_ROOT/scripts/praimate-code-rebrand.sh" "$SRC" || {
  echo "  (rebrand pass skipped/failed; continuing with upstream branding)" >&2
}

# Runtime and TLS support are required patches, separate from cosmetic rebranding.
# Fail if upstream changed the transport rather than shipping ignored consent.
bun "$REPO_ROOT/scripts/praimate-code-runtime.mjs" "$SRC"
bun "$REPO_ROOT/scripts/praimate-code-tls.mjs" "$SRC"

BASELINE="${BASELINE:-0}"
BUILD_FLAGS="--single --target=$GOOS-$GOARCH"
OUTNAME="praimate-code$EXT"
if [ "$BASELINE" = 1 ]; then
  BUILD_FLAGS="--single --target=$GOOS-$GOARCH --baseline"
  OUTNAME="praimate-code-baseline$EXT"
fi

if [ "$BASELINE" = 1 ]; then
  echo "→ building standalone binary ($GOOS-$GOARCH, baseline/no-AVX2 variant)"
else
  echo "→ building standalone binary ($GOOS-$GOARCH)"
fi
# --single restricts build.ts to the selected platform, so it doesn't
# download a Bun runtime for every OS/arch (which is slow and fragile
# over the network). Targets are built in separate runs.
# With --baseline build.ts emits BOTH the default and the -baseline
# target for this platform; the find below picks the right one.
( cd "$SRC/packages/opencode" && bun run build $BUILD_FLAGS )

# --baseline also emits a musl binary on Linux. Directory iteration order
# cannot choose the ABI: Desktop's Linux release uses the native glibc target.
BUN_ARCH="$GOARCH"
[ "$GOARCH" = amd64 ] && BUN_ARCH=x64
VARIANT="opencode-$GOOS-$BUN_ARCH"
[ "$BASELINE" = 1 ] && VARIANT="$VARIANT-baseline"
BUILT="$SRC/packages/opencode/dist/$VARIANT/bin/opencode$EXT"
if [ ! -f "$BUILT" ]; then
  echo "error: expected built opencode variant is missing: $BUILT"
  exit 1
fi

# --version does not initialize the prompt services. Verify a real completion
# against an isolated local fixture before publishing a native compiled build.
bun "$REPO_ROOT/scripts/praimate-code/smoke.mjs" "$BUILT" "$TARGET"

mkdir -p "$OUT"
install -m 0755 "$BUILT" "$OUT/$OUTNAME"
cp "$SRC/LICENSE" "$OUT/PRAIMATE-CODE-LICENSE"
cat > "$OUT/PRAIMATE-CODE-NOTICE" <<EOF
PrAImate Code is a rebranded build of OpenCode.

Upstream:  $OPENCODE_URL
Pinned ref: ${OPENCODE_REF:-v1.17.20 (vendored in third_party/opencode)}
License:   MIT (see PRAIMATE-CODE-LICENSE)

OpenCode is the work of its authors; PrAImate ships a version-pinned
build under a different product name as permitted by the MIT license.
The original copyright notice is retained in PRAIMATE-CODE-LICENSE.
EOF

echo
echo "✓ built $OUT/$OUTNAME"
ls -lh "$OUT/$OUTNAME"
