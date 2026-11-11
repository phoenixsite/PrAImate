#!/usr/bin/env bash
# PrAImate GUI installer for Linux and macOS.
#
# One-liner:
#   curl -fsSL https://raw.githubusercontent.com/sPROFFEs/praimate/main/scripts/install.sh | bash
#
# Or with options (after `-s --` they go to the script, not bash):
#   curl -fsSL https://… | bash -s -- --source      # build from source
#   curl -fsSL https://… | bash -s -- --user        # ~/.local/bin
#   curl -fsSL https://… | bash -s -- --system      # /usr/local/bin
#   curl -fsSL https://… | bash -s -- --yes         # auto-yes all prompts
#   curl -fsSL https://… | bash -s -- --uninstall   # remove binaries + shortcuts
#   curl -fsSL https://… | bash -s -- --uninstall --purge   # + config/DB
#
# Or run locally (from inside an extracted release archive or a repo checkout):
#   ./scripts/install.sh
#
# Flags:
#   --binary           grab the prebuilt release tarball (default in one-liner mode)
#   --source           git clone + go build (installs Go if missing, asking first)
#   --edition <name>   desktop (default) or server (remote-capable build)
#   --user             install to ~/.local/bin (no sudo)
#   --system           install to /usr/local/bin (sudo when needed)
#   --prefix <dir>     custom install dir
#   --yes              auto-confirm prompts (CI / scripted use)
#   -h, --help         show this
#
# The binary path resolves the latest GitHub release via the API.
# Set RELEASE_TAG=<tag> in the environment to pin a specific release.

set -euo pipefail

REPO="sPROFFEs/praimate"
GITHUB_URL="https://github.com"
REPO_URL="$GITHUB_URL/$REPO"
RELEASE_API_URL="https://api.github.com/repos/$REPO/releases/latest"
SOURCE_BRANCH="main"
EDITION="desktop"
EDITION_EXPLICIT=0
ASSET_PREFIX="praimate"
BUILD_TAGS=""
# Release tag to pull assets from. When unset we resolve "latest" via
# the GitHub API at download time so the installer keeps working as
# the operator publishes new versioned tags (1.0.8, 1.0.9, ...).
# Override with RELEASE_TAG=<tag> in the environment to pin a release.
RELEASE_TAG="${RELEASE_TAG:-}"

MODE=""          # binary | source (empty = ask, default binary in non-tty)
PREFIX_MODE=""   # user | system (empty = auto)
PREFIX=""        # explicit
YES=0
UNINSTALL=0
PURGE=0

# ---------- arg parsing ----------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary)    MODE="binary"  ; shift ;;
    --source)    MODE="source"  ; shift ;;
    --edition)
      [[ $# -ge 2 ]] || { printf 'missing value for --edition\n' >&2; exit 2; }
      EDITION="$2"; EDITION_EXPLICIT=1; shift 2 ;;
    --edition=*) EDITION="${1#--edition=}"; EDITION_EXPLICIT=1; shift ;;
    --user)      PREFIX_MODE="user" ; shift ;;
    --system)    PREFIX_MODE="system" ; shift ;;
    --prefix)    PREFIX="$2"   ; shift 2 ;;
    --prefix=*)  PREFIX="${1#--prefix=}" ; shift ;;
    --yes|-y)    YES=1 ; shift ;;
    --uninstall) UNINSTALL=1 ; shift ;;
    --purge)     PURGE=1 ; shift ;;
    -h|--help)
      sed -n '2,28p' "$0" | sed 's/^# \{0,1\}//'
      cat <<'EOF'
  --uninstall        remove the installed binaries, launch wrappers and
                     desktop entries (keeps your config, chats and DB)
  --purge            with --uninstall: ALSO delete config, managed tools,
                     the chat database and the build cache
EOF
      exit 0 ;;
    *) printf 'unknown arg: %s (try --help)\n' "$1" >&2; exit 2 ;;
  esac
done


# Validate explicit input before resolving a destination or requesting sudo.
case "$EDITION" in desktop|server) ;; *) printf 'unknown edition: %s (use desktop or server)\n' "$EDITION" >&2; exit 2 ;; esac

# ---------- uninstall ----------
do_uninstall() {
  local dests=() d removed=0
  if [[ -n "$PREFIX" ]]; then
    dests=("$PREFIX")
  else
    dests=("$HOME/.local/bin" "/usr/local/bin")
  fi
  local files=(praimate wpc praimate-gui praimate-code praimate-cli praimate-edition praimate-launch
               praimate-gui-launch PRAIMATE-CODE-LICENSE PRAIMATE-CODE-NOTICE)
  for d in "${dests[@]}"; do
    [[ -d "$d" ]] || continue
    local sudo_cmd=""
    [[ -w "$d" ]] || { command -v sudo >/dev/null 2>&1 && sudo_cmd="sudo"; }
    local f
    for f in "${files[@]}"; do
      if [[ -e "$d/$f" || -L "$d/$f" ]]; then
        $sudo_cmd rm -f "$d/$f" && { printf '  removed %s\n' "$d/$f"; removed=1; }
      fi
    done
    if [[ -d "$(dirname "$d")/share/praimate" ]]; then
      $sudo_cmd rm -rf "$(dirname "$d")/share/praimate" \
        && { printf '  removed %s\n' "$(dirname "$d")/share/praimate"; removed=1; }
    fi
  done
  local data_home="${XDG_DATA_HOME:-$HOME/.local/share}"
  [[ "$data_home" = /* ]] || data_home="$HOME/.local/share"
  local apps="$data_home/applications" desk_dir
  desk_dir="$(command -v xdg-user-dir >/dev/null 2>&1 && xdg-user-dir DESKTOP || echo "$HOME/Desktop")"
  local e
  for e in praimate.desktop praimate-gui.desktop; do
    for d in "$apps" "$desk_dir"; do
      [[ -f "$d/$e" ]] && rm -f "$d/$e" && printf '  removed %s\n' "$d/$e"
    done
  done
  rm -f "$data_home/icons/praimate.png" "$data_home/icons/hicolor/512x512/apps/praimate.png" "$data_home/pixmaps/praimate.png" 2>/dev/null || true
  command -v update-desktop-database >/dev/null 2>&1 \
    && update-desktop-database "$apps" 2>/dev/null || true

  if [[ "$(uname -s)" == Darwin ]]; then
    local app_dir="$HOME/Applications/PrAImate.app"
    if [[ -f "$app_dir/Contents/Info.plist" ]] && grep -q '<string>com.praimate.desktop</string>' "$app_dir/Contents/Info.plist"; then
      rm -rf "$app_dir"
    fi
    if [[ -L "$HOME/Desktop/PrAImate.app" && "$(readlink "$HOME/Desktop/PrAImate.app")" == "$app_dir" ]]; then
      rm -f "$HOME/Desktop/PrAImate.app"
    fi
  fi

  if [[ "$PURGE" == 1 ]]; then
    local cfg="${XDG_CONFIG_HOME:-$HOME/.config}"
    local cache="${XDG_CACHE_HOME:-$HOME/.cache}"
    local p
    for p in "$cfg/praimate" "$cfg/clade" "$HOME/.praimate" "$cache/praimate"; do
      [[ -d "$p" ]] && rm -rf "$p" && printf '  purged  %s\n' "$p"
    done
  else
    printf '  (config, managed tools and the chat DB were kept — add --purge to remove them)\n'
  fi
  [[ "$removed" == 1 ]] && printf 'PrAImate uninstalled.\n' || printf 'nothing to remove (already uninstalled?)\n'
  exit 0
}
[[ "$UNINSTALL" == 1 ]] && do_uninstall

# ---------- tty helpers ----------
# When piped via `curl | bash`, stdin is the pipe. To prompt the user
# we have to read from /dev/tty. Detect once and route every prompt
# through here so the same code handles both interactive runs and
# one-liners.
HAVE_TTY=0
if [[ -e /dev/tty ]]; then
  HAVE_TTY=1
fi

ask() {
  local prompt="$1" default="$2" reply=""
  if [[ "$YES" == "1" ]]; then
    printf '%s\n' "$default"
    return
  fi
  if [[ "$HAVE_TTY" == "0" ]]; then
    printf '%s\n' "$default"
    return
  fi
  printf '%s [%s]: ' "$prompt" "$default" >/dev/tty
  read -r reply </dev/tty || reply=""
  if [[ -z "${reply// }" ]]; then
    printf '%s' "$default"
  else
    printf '%s' "$reply"
  fi
}

yesno() {
  local prompt="$1" default="${2:-n}" reply
  reply="$(ask "$prompt $( [[ "$default" == y ]] && printf '[Y/n]' || printf '[y/N]' )" "$default")"
  [[ "${reply,,}" =~ ^y(es)?$ ]]
}

# ---------- pretty ----------
c_grn() { printf '\033[0;32m%s\033[0m\n' "$*"; }
c_red() { printf '\033[0;31m%s\033[0m\n' "$*" >&2; }
c_yel() { printf '\033[0;33m%s\033[0m\n' "$*"; }
c_dim() { printf '\033[0;90m%s\033[0m\n' "$*"; }

step() { printf '\n'; c_grn "==> $*"; }

# ---------- platform detect ----------
detect_triplet() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in
    linux)  os=linux ;;
    darwin) os=darwin ;;
    *) c_red "unsupported OS: $os (supported: Linux, Windows, macOS)"; exit 1 ;;
  esac
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64)
      if [[ "$os" == "darwin" ]]; then
        c_red "PrAImate for macOS targets Apple Silicon (arm64: M1/M2/M3/M4)."
        exit 1
      fi
      arch=amd64
      ;;
    aarch64|arm64) arch=arm64 ;;
    *) c_red "unsupported arch: $arch"; exit 1 ;;
  esac
  printf '%s-%s' "$os" "$arch"
}

TRIPLET="$(detect_triplet)"

# ---------- locate local binaries (release-archive / repo case) ----------
HERE="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd 2>/dev/null || printf '')"

find_local_bins() {
  local cand
  for cand in "$PWD" "$HERE" "$HERE/.." "$HERE/../dist/$TRIPLET"; do
    [[ -n "$cand" ]] || continue
    if [[ -x "$cand/praimate" && -x "$cand/wpc" ]]; then
      printf '%s' "$cand"
      return 0
    fi
  done
  return 1
}

LOCAL_BINS="$(find_local_bins || true)"

find_source_dir() {
  local cand
  for cand in "$PWD" "$HERE/.."; do
    [[ -n "$cand" ]] || continue
    if [[ -f "$cand/go.mod" && -d "$cand/cmd/praimate" && -d "$cand/cmd/wpc" ]]; then
      if [[ "$EDITION" == server && ! -f "$cand/internal/version/edition_server.go" ]]; then
        continue
      fi
      (cd "$cand" && pwd)
      return 0
    fi
  done
  return 1
}

SOURCE_DIR=""

# ---------- mode prompt ----------
if [[ -z "$MODE" ]]; then
  if [[ -n "$LOCAL_BINS" ]]; then
    # Inside an archive / repo — just install what's here.
    MODE="local"
    c_dim "(found local binaries in $LOCAL_BINS — skipping download/build prompt)"
  elif SOURCE_DIR="$(find_source_dir)"; then
    MODE="source"
    c_dim "(found source checkout in $SOURCE_DIR — building local source)"
  else
    cat <<EOF
How do you want to install PrAImate?

  1. Download a prebuilt release  ${YES:+(default in --yes mode)}
  2. Build from source (needs Go; will offer to install Go if missing)
  3. Cancel
EOF
    choice="$(ask 'Choose 1, 2, or 3' '1')"
    case "$choice" in
      1) MODE="binary" ;;
      2) MODE="source" ;;
      3) c_yel "cancelled."; exit 0 ;;
      *) c_red "invalid choice: $choice"; exit 1 ;;
    esac
  fi
fi

# ---------- destination ----------
choose_dest() {
  if [[ -n "$PREFIX" ]]; then
    printf '%s' "$PREFIX"
    return
  fi
  case "$PREFIX_MODE" in
    user)   printf '%s' "$HOME/.local/bin" ;;
    system) printf '%s' "/usr/local/bin"   ;;
    "")
      # auto: prefer system if writable or sudo available + user agrees,
      # otherwise drop into ~/.local/bin (no sudo needed).
      if [[ -w /usr/local/bin ]]; then
        printf '%s' "/usr/local/bin"
      elif command -v sudo >/dev/null 2>&1 && [[ "$HAVE_TTY" == "1" ]] && ! [[ "$YES" == "1" ]]; then
        if yesno "Install to /usr/local/bin (uses sudo)? Otherwise will use ~/.local/bin." y; then
          printf '%s' "/usr/local/bin"
        else
          printf '%s' "$HOME/.local/bin"
        fi
      else
        printf '%s' "$HOME/.local/bin"
      fi
      ;;
  esac
}

DEST="$(choose_dest)"
# Re-running the installer updates this installation's edition by default.
# --edition remains an explicit request to install a different edition.
if [[ "$EDITION_EXPLICIT" == 0 && -f "$DEST/praimate-edition" ]]; then
  EDITION="$(cat "$DEST/praimate-edition")"
fi
case "$EDITION" in
  desktop) ;;
  server) SOURCE_BRANCH="server"; ASSET_PREFIX="praimate-server"; BUILD_TAGS="server" ;;
  *) printf 'unknown edition: %s (use desktop or server)\n' "$EDITION" >&2; exit 2 ;;
esac
if [[ -n "$RELEASE_TAG" ]]; then
  case "$EDITION:$RELEASE_TAG" in
    desktop:v[0-9]*|server:server-v[0-9]*|server:v[0-9]*) ;;
    *) printf 'release tag %s does not belong to the %s edition\n' "$RELEASE_TAG" "$EDITION" >&2; exit 2 ;;
  esac
fi

mkdir -p "$DEST" 2>/dev/null || true

SUDO=""
if [[ ! -w "$DEST" ]]; then
  if command -v sudo >/dev/null 2>&1; then
    SUDO="sudo"
  else
    c_red "$DEST is not writable and sudo isn't available."
    c_red "Re-run with --user or --prefix=<writable dir>."
    exit 1
  fi
fi

# ---------- managed bundle files ----------
# Every install must reflect the selected bundle exactly. In particular,
# optional tools may disappear from a later release; leaving their old
# binaries behind makes the new praimate launch stale companions.
remove_managed_file() {
  local path="$1" label="$2"
  if [[ -e "$path" || -L "$path" ]]; then
    $SUDO rm -f -- "$path"
    c_dim "  removed stale $label"
  fi
}

graphify_bin() {
  printf '%s' "${XDG_CONFIG_HOME:-$HOME/.config}/praimate/bin/praimate-graphify"
}

sync_samples() {
  local bundle="$1" samples_dest
  samples_dest="$(dirname "$DEST")/share/praimate/samples"
  if [[ -d "$bundle/samples" ]]; then
    # This is an installer-managed share directory, not the user's
    # workspace. Replacing it avoids retaining samples removed upstream.
    $SUDO rm -rf -- "$samples_dest"
    $SUDO mkdir -p "$samples_dest"
    $SUDO cp -R "$bundle/samples/." "$samples_dest/"
    c_dim "  samples → $samples_dest"
  else
    $SUDO rm -rf -- "$samples_dest"
    c_dim "  removed stale samples"
  fi
}

sync_bundle_extras() {
  local bundle="$1" graphify

  if [[ -f "$bundle/praimate-cli" ]]; then
    $SUDO install -m 0755 "$bundle/praimate-cli" "$DEST/praimate-cli"
  fi

  if [[ -f "$bundle/praimate-gui" ]]; then
    $SUDO install -m 0755 "$bundle/praimate-gui" "$DEST/praimate-gui"
    c_grn "  praimate-gui installed (launch with: praimate)"
  else
    c_red "bundle is missing mandatory praimate-gui"
    return 1
  fi

  if [[ -f "$bundle/praimate-code" ]]; then
    $SUDO install -m 0755 "$bundle/praimate-code" "$DEST/praimate-code"
    [[ -f "$bundle/PRAIMATE-CODE-LICENSE" ]] && $SUDO cp "$bundle/PRAIMATE-CODE-LICENSE" "$DEST/"
    [[ -f "$bundle/PRAIMATE-CODE-NOTICE" ]] && $SUDO cp "$bundle/PRAIMATE-CODE-NOTICE" "$DEST/"
    c_grn "  praimate-code installed (launch with: praimate code)"
  else
    remove_managed_file "$DEST/praimate-code" "praimate-code"
    remove_managed_file "$DEST/PRAIMATE-CODE-LICENSE" "Praimate Code license"
    remove_managed_file "$DEST/PRAIMATE-CODE-NOTICE" "Praimate Code notice"
  fi

  graphify="$(graphify_bin)"
  if [[ -f "$bundle/praimate-graphify" ]]; then
    mkdir -p "$(dirname "$graphify")"
    install -m 0755 "$bundle/praimate-graphify" "$graphify"
    c_grn "  bundled graphify installed (used for agent RAG indexing)"
  else
    remove_managed_file "$graphify" "praimate-graphify"
  fi

  sync_samples "$bundle"
}

# A legacy unmarked bundle is desktop-only. Check before replacing any files.
validate_bundle_edition() {
  local bundle="$1" actual="desktop"
  if [[ -f "$bundle/praimate-edition" ]]; then
    actual="$(cat "$bundle/praimate-edition")"
  fi
  if [[ "$actual" != "$EDITION" ]]; then
    c_red "This is a $actual bundle, but $EDITION was requested. No binaries were replaced."
    return 1
  fi
  [[ -f "$bundle/praimate" && -f "$bundle/wpc" && -f "$bundle/praimate-gui" ]] || {
    c_red 'bundle is missing a mandatory PrAImate executable'; return 1;
  }
}

record_installed_edition() {
  local marker
  marker="$(mktemp)"
  printf '%s\n' "$EDITION" > "$marker"
  $SUDO install -m 0644 "$marker" "$DEST/praimate-edition"
  rm -f "$marker"
}

# ---------- binary path: GitHub release ----------
detect_downloader() {
  if command -v curl >/dev/null 2>&1; then printf 'curl'
  elif command -v wget >/dev/null 2>&1; then printf 'wget'
  else c_red "need curl or wget"; exit 1; fi
}

fetch() {
  # fetch <url> <out>
  local url="$1" out="$2"
  local dl
  dl="$(detect_downloader)"
  if [[ "$dl" == "curl" ]]; then
    curl -fsSL --retry 3 -o "$out" "$url"
  else
    wget -q -O "$out" "$url"
  fi
}

resolve_latest_tag() {
  local dl tag
  tag=""
  dl="$(detect_downloader)"
  if [[ "$EDITION" == server ]]; then
    # A joint v* release is compatible only when it actually contains Server.
    local releases_url="https://api.github.com/repos/$REPO/releases?per_page=100" payload=""
    if [[ "$dl" == curl ]]; then
      payload="$(curl -fsSL -H 'User-Agent: praimate-installer' "$releases_url")" || return 1
    else
      payload="$(wget -q -O - "$releases_url")" || return 1
    fi
    if command -v python3 >/dev/null 2>&1; then
      tag="$(printf '%s' "$payload" | python3 -c '
import json,sys
for r in json.load(sys.stdin):
 t=r.get("tag_name","")
 if not r.get("draft") and (t.startswith("server-v") or (t.startswith("v") and any(a.get("name","").startswith("praimate-server-") and a.get("name","").endswith((".tar.gz",".zip")) for a in r.get("assets",[])))):
  print(t);break
')"
    else
      tag="$(printf '%s' "$payload" | tr ',' '\n' | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\(server-v[0-9][^"]*\)".*/\1/p')"
      if [[ -z "$tag" && "$dl" == curl ]]; then
        local web_tag shared_os shared_arch
        web_tag="$(curl -fsSI "$REPO_URL/releases/latest" 2>/dev/null | sed -n 's|.*[Ll]ocation:.*\/tag\/\([^[:space:]\r]*\).*|\1|p' | head -1)"
        shared_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
        shared_arch="$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')"
        if [[ "$web_tag" == v[0-9]* ]] && curl -fsSI "$REPO_URL/releases/download/$web_tag/praimate-server-$shared_os-$shared_arch.tar.gz" >/dev/null 2>&1; then tag="$web_tag"; fi
      fi
    fi
    tag="${tag%%$'\n'*}"
    [[ -n "$tag" ]] || { c_red 'No server release has been published yet. Use --source --edition server.'; return 1; }
    printf '%s' "$tag"
    return
  fi

  # 1. Try web redirect first to bypass GitHub API 60 req/hr rate limit / 403 Forbidden
  if [[ "$dl" == "curl" ]]; then
    tag="$(curl -fsSI "$REPO_URL/releases/latest" 2>/dev/null | grep -i '^location:' | sed -n 's/.*\/tag\/\([^[:space:]\r\n]*\).*/\1/p' | head -1)"
  fi
  if [[ -n "$tag" ]]; then
    printf '%s' "$tag"
    return
  fi

  # 2. Fall back to GitHub REST API
  local auth_headers=()
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    auth_headers=(-H "Authorization: token $GITHUB_TOKEN")
  fi
  if [[ "$dl" == "curl" ]]; then
    tag="$(curl -fsSL -H 'User-Agent: praimate-installer' -H 'Accept: application/vnd.github.v3+json' "${auth_headers[@]}" "$RELEASE_API_URL" 2>/dev/null \
      | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
  else
    tag="$(wget -q -O - --header='User-Agent: praimate-installer' --header='Accept: application/vnd.github.v3+json' "$RELEASE_API_URL" 2>/dev/null \
      | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
  fi
  if [[ -z "$tag" ]]; then
    c_red "couldn't query GitHub for the latest release."
    c_red "Set RELEASE_TAG=<tag> in the environment to pin a version and re-run."
    exit 1
  fi
  printf '%s' "$tag"
}

install_from_release() {
  step "Resolving release"
  if [[ -z "$RELEASE_TAG" ]]; then
    RELEASE_TAG="$(resolve_latest_tag)"
  fi
  local fname="${ASSET_PREFIX}-${TRIPLET}.tar.gz"
  local url="$REPO_URL/releases/download/${RELEASE_TAG}/${fname}"
  c_dim "  tag:     $RELEASE_TAG"
  c_dim "  asset:   $fname"
  c_dim "  url:     $url"

  step "Downloading"
  local tmp
  tmp="$(mktemp -d)"
  # Double-quote so $tmp is expanded NOW, while it's still in scope.
  # Single quotes would defer expansion to EXIT-time, by which point
  # `local tmp` is gone and `set -u` would error with "tmp: unbound".
  trap "rm -rf '$tmp'" EXIT
  fetch "$url" "$tmp/$fname" || { c_red "download failed."; exit 1; }
  c_grn "  downloaded $(du -h "$tmp/$fname" | cut -f1)"

  step "Extracting"
  tar -xzf "$tmp/$fname" -C "$tmp"
  local extracted="$tmp/$TRIPLET"
  [[ -d "$extracted" ]] || { c_red "unexpected archive layout under $tmp"; exit 1; }
  validate_bundle_edition "$extracted"

  step "Installing to $DEST"
  $SUDO install -m 0755 "$extracted/praimate" "$DEST/praimate"
  $SUDO install -m 0755 "$extracted/wpc"   "$DEST/wpc"
  sync_bundle_extras "$extracted"
  record_installed_edition
  c_grn "  ✓ praimate + wpc installed"
}

# ---------- source path: clone + go build ----------
have_go() { command -v go >/dev/null 2>&1; }

gui_ext() {
  case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) printf '.exe' ;;
    *) printf '' ;;
  esac
}

build_gui_from_source() {
  local src="$1"
  local missing=()
  if ! command -v npm >/dev/null 2>&1; then
    missing+=("npm")
  fi
  if ! command -v pkg-config >/dev/null 2>&1; then
    missing+=("pkg-config")
  fi
  if [[ "$(uname -s)" == "Linux" ]]; then
    if command -v pkg-config >/dev/null 2>&1; then
      pkg-config --exists webkit2gtk-4.1 || missing+=("libwebkit2gtk-4.1-dev")
      pkg-config --exists gtk+-3.0 || missing+=("libgtk-3-dev")
    else
      missing+=("libwebkit2gtk-4.1-dev" "libgtk-3-dev")
    fi
  fi

  if ((${#missing[@]})); then
    c_yel "Skipping praimate-gui build; missing: ${missing[*]}"
    if [[ "$(uname -s)" == "Linux" ]]; then
      c_dim "  On Debian/Kali install them with:"
      c_dim "    sudo apt-get install -y npm pkg-config libwebkit2gtk-4.1-dev libgtk-3-dev"
    fi
    return 1
  fi

  (cd "$src/cmd/praimate-gui" && ./build.sh)
}

detect_pkg_manager() {
  # Print the apt/dnf/pacman/zypper/apk install command for Go,
  # or empty if we don't know the system.
  if command -v apt-get >/dev/null 2>&1; then
    printf '%s' "sudo apt-get update && sudo apt-get install -y golang-go"
  elif command -v dnf >/dev/null 2>&1; then
    printf '%s' "sudo dnf install -y golang"
  elif command -v pacman >/dev/null 2>&1; then
    printf '%s' "sudo pacman -S --noconfirm go"
  elif command -v zypper >/dev/null 2>&1; then
    printf '%s' "sudo zypper install -y go"
  elif command -v apk >/dev/null 2>&1; then
    printf '%s' "sudo apk add go"
  fi
}

install_go() {
  local cmd
  cmd="$(detect_pkg_manager)"
  if [[ -z "$cmd" ]]; then
    c_red "Go isn't installed and we can't find a known package manager."
    c_red "Install Go manually from https://go.dev/dl/ and re-run this script."
    return 1
  fi
  c_yel "Go isn't installed. The script can install it with:"
  printf '\n    %s\n\n' "$cmd"
  if ! yesno "Run that command now?" y; then
    c_red "Cancelled. Install Go yourself and re-run."
    return 1
  fi
  bash -c "$cmd"
  have_go || { c_red "Go install reported success but 'go' still isn't on PATH."; return 1; }
}

install_from_source() {
  if ! have_go; then
    step "Go not found"
    install_go || exit 1
  fi
  local src tmp builddir gui_bin ext installed
  tmp=""
  builddir="$(mktemp -d)"
  # See install_from_release for why this uses double quotes.
  trap "rm -rf '$tmp' '$builddir'" EXIT

  if src="$(find_source_dir)"; then
    step "Using local source"
    c_dim "  source: $src"
  else
    step "Cloning repo"
    tmp="$(mktemp -d)"
    git clone --depth 1 --branch "$SOURCE_BRANCH" \
      "${REPO_URL}.git" "$tmp/PrAImate" \
      || { c_red "git clone failed"; exit 1; }
    src="$tmp/PrAImate"
  fi
  SOURCE_DIR="$src"

  step "Building (this can take ~30s on first run while Go fetches deps)"
  (
    cd "$src"
    GOOS="$(uname -s | tr '[:upper:]' '[:lower:]')" \
    GOARCH="$(case $(uname -m) in x86_64|amd64) printf amd64;; aarch64|arm64) printf arm64;; esac)" \
    CGO_ENABLED=0 \
    go build -trimpath -tags "$BUILD_TAGS" -ldflags '-s -w' -o "$builddir/praimate" ./cmd/praimate
    GOOS="$(uname -s | tr '[:upper:]' '[:lower:]')" \
    GOARCH="$(case $(uname -m) in x86_64|amd64) printf amd64;; aarch64|arm64) printf arm64;; esac)" \
    CGO_ENABLED=0 \
    go build -trimpath -tags "$BUILD_TAGS" -ldflags '-s -w' -o "$builddir/wpc" ./cmd/wpc
    CGO_ENABLED=0 go build -trimpath -tags "$BUILD_TAGS" -ldflags '-s -w' -o "$builddir/praimate-cli" ./cmd/praimate-cli
  )

  step "Building GUI"
  ext="$(gui_ext)"
  gui_bin="$src/cmd/praimate-gui/praimate-gui$ext"
  if ! PRAIMATE_EDITION="$EDITION" build_gui_from_source "$src"; then
    c_red "The mandatory PrAImate desktop app could not be built."
    c_red "Install the missing GUI dependencies above and re-run this installer."
    exit 1
  fi

  step "Installing to $DEST"
  $SUDO install -m 0755 "$builddir/praimate" "$DEST/praimate"
  $SUDO install -m 0755 "$builddir/wpc"   "$DEST/wpc"
  $SUDO install -m 0755 "$builddir/praimate-cli" "$DEST/praimate-cli"
  installed="praimate + wpc"
  if [[ -f "$gui_bin" ]]; then
    $SUDO install -m 0755 "$gui_bin" "$DEST/praimate-gui$ext"
    installed="$installed + praimate-gui"
  fi
  remove_managed_file "$DEST/praimate-code" "praimate-code"
  remove_managed_file "$DEST/PRAIMATE-CODE-LICENSE" "Praimate Code license"
  remove_managed_file "$DEST/PRAIMATE-CODE-NOTICE" "Praimate Code notice"
  remove_managed_file "$(graphify_bin)" "praimate-graphify"
  sync_samples "$src"
  record_installed_edition
  c_grn "  ✓ $installed installed"
}

# ---------- local path: bins already next to us ----------
install_local() {
  validate_bundle_edition "$LOCAL_BINS"
  step "Installing to $DEST"
  $SUDO install -m 0755 "$LOCAL_BINS/praimate" "$DEST/praimate"
  $SUDO install -m 0755 "$LOCAL_BINS/wpc"   "$DEST/wpc"
  sync_bundle_extras "$LOCAL_BINS"
  record_installed_edition
  c_grn "  ✓ praimate + wpc installed"
}

# ---------- dispatch ----------
case "$MODE" in
  binary) install_from_release ;;
  source) install_from_source ;;
  local)  install_local ;;
  *) c_red "internal: unknown MODE $MODE"; exit 1 ;;
esac

# ---------- PATH sanity check ----------
case ":$PATH:" in
  *":$DEST:"*) ;;
  *)
    rc="$HOME/.bashrc"
    [[ -n "${ZSH_VERSION:-}" || "${SHELL:-}" == */zsh ]] && rc="$HOME/.zshrc"
    cat <<EOF

⚠  $DEST is NOT on your PATH yet.

   Add this line to your $rc (or the rc of the shell you actually use):

       export PATH="\$PATH:$DEST"

   Then open a new terminal, or run:
       source $rc
EOF
    ;;
esac


# Desktop Entry strings decode backslash escapes before Exec is parsed.
desktop_string() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\t'/\\t}"
  printf '%s' "$value"
}
desktop_exec_path() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//\$/\\$}"
  value="${value//\`/\\\`}"
  value="${value//%/%%}"
  desktop_string "$value"
}

# ---------- desktop shortcut (GUI only) ----------
create_shortcuts() {
  local icon_src="$1"
  case "$(uname -s)" in
    Linux)
      local data_home="${XDG_DATA_HOME:-$HOME/.local/share}"
      [[ "$data_home" = /* ]] || data_home="$HOME/.local/share"
      local apps="$data_home/applications"
      mkdir -p "$apps"
      local icon_line=""
      if [[ -n "$icon_src" && -f "$icon_src" ]]; then
        mkdir -p "$data_home/icons" "$data_home/icons/hicolor/512x512/apps" "$data_home/pixmaps"
        cp -f "$icon_src" "$data_home/icons/praimate.png" 2>/dev/null || true
        cp -f "$icon_src" "$data_home/icons/hicolor/512x512/apps/praimate.png" 2>/dev/null || true
        cp -f "$icon_src" "$data_home/pixmaps/praimate.png" 2>/dev/null || true
        icon_line="Icon=praimate"
      fi

      # Core startup imports managed and common CLI paths, including nvm.
      # Never source interactive shell files here: they may block, exit, or
      # contain syntax for a different shell than this launcher.
      local launcher_tmp
      launcher_tmp="$(mktemp)" || return 1
      cat > "$launcher_tmp" <<'WRAP'
#!/usr/bin/env bash
set -eu
praimate_bin="${0##*/}"
praimate_bin="${praimate_bin%-launch}"
praimate_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
exec "$praimate_dir/$praimate_bin" "$@"
WRAP
      if ! ${SUDO:-} install -m 755 "$launcher_tmp" "$DEST/praimate-launch"; then
        rm -f "$launcher_tmp"
        c_red "Failed to install the launcher in $DEST"
        return 1
      fi
      rm -f "$launcher_tmp"
      if ! ${SUDO:-} ln -sf praimate-launch "$DEST/praimate-gui-launch"; then
        c_red "Failed to create the GUI launcher in $DEST"
        return 1
      fi
      local exec_path home_path
      exec_path="$(desktop_exec_path "$DEST/praimate-gui-launch")"
      home_path="$(desktop_string "$HOME")"
      # GIO checks the first executable before expanding %% field escapes.
      # A fixed interpreter also permits literal percent signs in DEST.
      cat > "$apps/praimate.desktop" <<DESK
[Desktop Entry]
Type=Application
Name=PrAImate
Comment=Multi-CLI agent desktop app
Exec=/usr/bin/env bash "$exec_path"
Path=$home_path
Terminal=false
$icon_line
StartupWMClass=praimate
StartupNotify=true
Categories=Development;
DESK
      rm -f "$apps/praimate-gui.desktop"
      chmod 755 "$apps/praimate.desktop" 2>/dev/null || true
      command -v update-desktop-database >/dev/null 2>&1 \
        && update-desktop-database "$apps" 2>/dev/null || true
      command -v gtk-update-icon-cache >/dev/null 2>&1 \
        && gtk-update-icon-cache -f -t "$data_home/icons/hicolor" 2>/dev/null || true
      # Mirror onto the Desktop when one exists. Each copy needs its
      # own +x AND a gio "trusted" flag — otherwise the Desktop shows
      # the file with a stop-sign overlay and the user has to right-
      # click → "Allow Launching" before the icon resolves.
      local desk_dir
      desk_dir="$(command -v xdg-user-dir >/dev/null 2>&1 && xdg-user-dir DESKTOP || echo "$HOME/Desktop")"
      rm -f "$desk_dir/praimate-gui.desktop"
      if [[ -d "$desk_dir" ]]; then
        for f in praimate.desktop; do
          [[ -f "$apps/$f" ]] || continue
          cp -f "$apps/$f" "$desk_dir/" 2>/dev/null || true
          chmod 755 "$desk_dir/$f" 2>/dev/null || true
          command -v gio >/dev/null 2>&1 \
            && gio set "$desk_dir/$f" metadata::trusted true 2>/dev/null || true
        done
      fi
      c_grn "  desktop shortcuts created (app menu + Desktop)"
      ;;
    Darwin)
      # Keep one canonical executable in DEST so the updater and CLI siblings
      # keep working. Finder opens a small application bundle that launches it.
      local app_dir="$HOME/Applications/PrAImate.app" icon_name="praimate.png"
      mkdir -p "$app_dir/Contents/MacOS" "$app_dir/Contents/Resources"
      printf '#!/bin/bash\nexec %q "$@"\n' "$DEST/praimate-gui" > "$app_dir/Contents/MacOS/PrAImate"
      chmod 755 "$app_dir/Contents/MacOS/PrAImate"
      if [[ -n "$icon_src" && -f "$icon_src" ]]; then
        cp -f "$icon_src" "$app_dir/Contents/Resources/praimate.png"
        if command -v sips >/dev/null && command -v iconutil >/dev/null; then
          local iconset size
          iconset="$(mktemp -d)/praimate.iconset"
          mkdir -p "$iconset"
          for size in 16 32 128 256 512; do
            sips -z "$size" "$size" "$icon_src" --out "$iconset/icon_${size}x${size}.png" >/dev/null
            sips -z "$((size * 2))" "$((size * 2))" "$icon_src" --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
          done
          if iconutil -c icns "$iconset" -o "$app_dir/Contents/Resources/praimate.icns"; then
            icon_name="praimate.icns"
          fi
          rm -rf "${iconset%/*}"
        fi
      fi
      cat > "$app_dir/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.praimate.desktop</string>
<key>CFBundleName</key><string>PrAImate</string>
<key>CFBundleDisplayName</key><string>PrAImate</string>
<key>CFBundleExecutable</key><string>PrAImate</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleIconFile</key><string>$icon_name</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSMicrophoneUsageDescription</key><string>PrAImate uses the microphone only while you hold Dictate to transcribe speech locally.</string>
</dict></plist>
PLIST
      if [[ -d "$HOME/Desktop" && ! -e "$HOME/Desktop/PrAImate.app" ]]; then
        ln -s "$app_dir" "$HOME/Desktop/PrAImate.app"
      fi
      local lsregister="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
      [[ ! -x "$lsregister" ]] || "$lsregister" -f "$app_dir" || true
      # Clear quarantine attributes so macOS Gatekeeper allows execution
      xattr -rd com.apple.quarantine "$DEST" 2>/dev/null || true
      # Ad-hoc sign binaries if codesign is present
      if command -v codesign >/dev/null 2>&1; then
        for bin in "$DEST/praimate" "$DEST/praimate-gui" "$DEST/praimate-cli" "$DEST/wpc"; do
          [[ -f "$bin" ]] && codesign --force --deep -s - "$bin" 2>/dev/null || true
        done
      fi
      c_grn "  macOS launcher installed in ~/Applications/PrAImate.app"
      ;;
  esac
}
ICON_SRC=""
[[ -n "${extracted:-}" && -f "${extracted:-}/praimate.png" ]] && ICON_SRC="$extracted/praimate.png"
[[ -z "$ICON_SRC" && -n "${LOCAL_BINS:-}" && -f "${LOCAL_BINS:-}/praimate.png" ]] && ICON_SRC="$LOCAL_BINS/praimate.png"
[[ -z "$ICON_SRC" && -n "${SOURCE_DIR:-}" && -f "${SOURCE_DIR:-}/cmd/praimate-gui/frontend/src/assets/monke-icon.png" ]] && ICON_SRC="$SOURCE_DIR/cmd/praimate-gui/frontend/src/assets/monke-icon.png"
create_shortcuts "$ICON_SRC"

step "Done"
printf 'Edition:   %s\n' "$EDITION"
printf 'Try it:    %s -version\n' "$DEST/praimate"
printf '(after PATH update, run `praimate` to launch the desktop app)\n'
if [[ "$EDITION" == server ]]; then printf 'Headless server: praimate server --help (configure credentials and TLS before network access)\n'; fi
